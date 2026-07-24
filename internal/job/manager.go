package job

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager is the central orchestrator for job lifecycle management.
// All state transitions go through the Manager.
type Manager struct {
	repo        IJobRepository
	engines     IEngineRegistry
	bus         IEventBus
	downloadDir string
	torrentRepo ITorrentRepository

	mu         sync.RWMutex
	activeJobs map[string]*Job // id -> job (in-memory cache for active jobs)

	monitor *Monitor
}

// NewManager creates a new job manager.
func NewManager(repo IJobRepository, engines IEngineRegistry, bus IEventBus, downloadDir string, torrentRepo ITorrentRepository) *Manager {
	m := &Manager{
		repo:        repo,
		engines:     engines,
		bus:         bus,
		downloadDir: downloadDir,
		torrentRepo: torrentRepo,
		activeJobs:  make(map[string]*Job),
	}
	return m
}

// StartBackgroundTasks starts the progress monitor and recovery.
// Call this after creating the Manager.
func (m *Manager) StartBackgroundTasks(ctx context.Context) {
	// Run recovery first
	m.recover(ctx)

	// Start progress monitor
	m.monitor = NewMonitor(m, 1*time.Second)
	m.monitor.Start(ctx)
}

// Stop stops background tasks.
func (m *Manager) Stop() {
	if m.monitor != nil {
		m.monitor.Stop()
	}
}

// Create validates the URL, creates a job, and starts the engine download.
// For media URLs (yt-dlp), the job enters "analyzing" state first.
func (m *Manager) Create(ctx context.Context, source string) (*Job, error) {
	// Handle magnet URIs
	if strings.HasPrefix(strings.ToLower(source), "magnet:") {
		return m.createTorrentJob(ctx, source, "")
	}

	// Validate URL
	u, err := url.ParseRequestURI(source)
	if err != nil {
		return nil, &AppError{Code: ErrInvalidURL, Message: "invalid URL: " + err.Error()}
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, &AppError{Code: ErrInvalidURL, Message: "only HTTP and HTTPS URLs are supported"}
	}

	// Detect engine
	engineName := m.engines.Detect(source)

	// Extract filename from URL
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		name = "download"
	}

	now := time.Now()
	j := &Job{
		ID:        "job_" + uuid.New().String()[:8],
		Source:    source,
		Name:      name,
		Status:    StatusQueued,
		Type:      TypeDownload,
		Engine:    engineName,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// For media engine, set type and start analysis
	if engineName == "ytdlp" {
		j.Type = TypeMedia
		j.Status = StatusAnalyzing
		j.Name = "Analyzing..."

		// Persist
		if err := m.repo.Create(ctx, j); err != nil {
			return nil, fmt.Errorf("persist job: %w", err)
		}

		m.publish(EventJobCreated, j)

		// Run analysis in background
		go m.analyzeMedia(ctx, j.ID, source)

		return j, nil
	}

	// Standard download flow (aria2)
	j.Type = TypeDownload

	// Persist
	if err := m.repo.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("persist job: %w", err)
	}

	eng, ok := m.engines.Get(engineName)
	if !ok {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("engine %q not available", engineName)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	// Start engine download
	engineID, err := eng.Start(ctx, j, m.downloadDir)
	if err != nil {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Failed to start download: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	j.EngineID = engineID
	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	m.addActive(j)
	m.publish(EventJobCreated, j)

	return j, nil
}

// analyzeMedia runs yt-dlp analysis in the background and updates the job.
func (m *Manager) analyzeMedia(parentCtx context.Context, jobID, source string) {
	ctx := context.Background() // Use background context so analysis survives request context

	j, err := m.repo.GetByID(ctx, jobID)
	if err != nil || j == nil {
		log.Printf("analyzeMedia: job %s not found", jobID)
		return
	}

	eng, ok := m.engines.Get("ytdlp")
	if !ok {
		j.Status = StatusFailed
		j.Error = "yt-dlp engine not available"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	analyzer, ok := eng.(IMediaAnalyzer)
	if !ok {
		j.Status = StatusFailed
		j.Error = "yt-dlp engine does not support media analysis"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	info, err := analyzer.Analyze(ctx, source)
	if err != nil {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Media analysis failed: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Update job with analysis results
	j.MediaInfo = info
	if info.Title != "" {
		j.Name = info.Title
	}
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)
	m.publish(EventJobUpdated, j)

	log.Printf("analyzeMedia: job %s analyzed successfully: %s (%d formats)", jobID, info.Title, len(info.Formats))
}

// SelectFormat selects a format for a media job and starts the download.
func (m *Manager) SelectFormat(ctx context.Context, id, formatID string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if j.Type != TypeMedia {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "format selection is only available for media jobs"}
	}

	if j.Status != StatusAnalyzing {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot select format for a %s job", j.Status)}
	}

	if j.MediaInfo == nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "media analysis not complete yet"}
	}

	// Validate format ID exists
	validFormat := false
	for _, f := range j.MediaInfo.Formats {
		if f.FormatID == formatID {
			validFormat = true
			break
		}
	}
	if !validFormat {
		return nil, &AppError{Code: ErrInvalidRequest, Message: "invalid format ID"}
	}

	// Set selected format
	j.MediaInfo.SelectedFmt = formatID

	// Transition to downloading
	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()

	eng, ok := m.engines.Get("ytdlp")
	if !ok {
		j.Status = StatusFailed
		j.Error = "yt-dlp engine not available"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	engineID, err := eng.Start(ctx, j, m.downloadDir)
	if err != nil {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Failed to start download: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	j.EngineID = engineID
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	m.addActive(j)
	m.publish(EventJobUpdated, j)

	return j, nil
}

// createTorrentJob creates a new torrent download job.
func (m *Manager) createTorrentJob(ctx context.Context, source, torrentFilePath string) (*Job, error) {
	engineName := m.engines.Detect(source)
	if engineName != "qbittorrent" {
		return nil, &AppError{Code: ErrEngineError, Message: "qBittorrent engine not available for torrent downloads"}
	}

	now := time.Now()
	j := &Job{
		ID:        "job_" + uuid.New().String()[:8],
		Source:    source,
		Name:      "Fetching metadata...",
		Status:    StatusAnalyzing,
		Type:      TypeTorrent,
		Engine:    engineName,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := m.repo.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("persist job: %w", err)
	}

	m.publish(EventJobCreated, j)

	// Run metadata acquisition in background
	go m.acquireTorrentMetadata(j.ID, source, torrentFilePath)

	return j, nil
}

// CreateTorrentFromFile creates a torrent job from an uploaded .torrent file.
func (m *Manager) CreateTorrentFromFile(ctx context.Context, torrentFilePath string) (*Job, error) {
	return m.createTorrentJob(ctx, "torrent://"+torrentFilePath, torrentFilePath)
}

// acquireTorrentMetadata adds the torrent to qBittorrent and polls for metadata.
func (m *Manager) acquireTorrentMetadata(jobID, source, torrentFilePath string) {
	ctx := context.Background()

	j, err := m.repo.GetByID(ctx, jobID)
	if err != nil || j == nil {
		log.Printf("acquireTorrentMetadata: job %s not found", jobID)
		return
	}

	eng, ok := m.engines.Get("qbittorrent")
	if !ok {
		j.Status = StatusFailed
		j.Error = "qBittorrent engine not available"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		j.Status = StatusFailed
		j.Error = "qBittorrent engine does not support torrent operations"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Add torrent to qBittorrent (stopped)
	var infoHash string
	if torrentFilePath != "" {
		infoHash, err = torrentEng.AddTorrentFile(ctx, torrentFilePath, m.downloadDir, jobID)
	} else {
		infoHash, err = torrentEng.AddMagnet(ctx, source, m.downloadDir, jobID)
	}
	if err != nil {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Failed to add torrent: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	j.EngineID = infoHash
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	// Poll for metadata (torrent name, file list)
	// qBittorrent fetches metadata from peers for magnet links
	var metadata *TorrentInfo
	for attempt := 0; attempt < 120; attempt++ { // ~2 minutes timeout
		time.Sleep(1 * time.Second)

		info, err := torrentEng.GetTorrentInfo(ctx, infoHash)
		if err != nil {
			continue
		}
		if info != nil && info.Name != "" && info.Name != infoHash {
			metadata = info
			break
		}
	}

	if metadata == nil {
		j.Status = StatusFailed
		j.Error = "Timed out waiting for torrent metadata from peers"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		// Clean up from qBittorrent
		torrentEng.RemoveTorrent(ctx, infoHash, false)
		return
	}

	// Save torrent info
	j.Name = metadata.Name
	j.TorrentInfo = metadata
	j.TotalBytes = metadata.TotalSize
	j.Status = StatusAwaitingSelection
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	// Save torrent job record
	if m.torrentRepo != nil {
		m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
			JobID:     jobID,
			InfoHash:  infoHash,
			Name:      metadata.Name,
			TotalSize: metadata.TotalSize,
		})

		// Fetch and save file list
		files, err := torrentEng.GetFiles(ctx, infoHash)
		if err == nil && len(files) > 0 {
			var records []TorrentFileRecord
			for _, f := range files {
				records = append(records, TorrentFileRecord{
					JobID:     jobID,
					FileIndex: f.Index,
					Path:      f.Path,
					Size:      f.Size,
					Selected:  true,
					Priority:  string(PriorityNormal),
				})
			}
			m.torrentRepo.SaveTorrentFiles(ctx, jobID, records)
		}
	}

	m.publish(EventJobUpdated, j)
	log.Printf("acquireTorrentMetadata: job %s metadata acquired: %s (%d files)", jobID, metadata.Name, 0)
}

// StartTorrent starts a torrent download after file selection.
func (m *Manager) StartTorrent(ctx context.Context, id string, selections []TorrentFileSelection, seedAfterComplete bool) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if j.Type != TypeTorrent {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "torrent operations are only available for torrent jobs"}
	}

	if j.Status != StatusAwaitingSelection {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot start torrent from %s state", j.Status)}
	}

	// Validate at least one file is selected
	hasSelected := false
	for _, s := range selections {
		if s.Priority != PrioritySkip {
			hasSelected = true
			break
		}
	}
	if !hasSelected {
		return nil, &AppError{Code: ErrNoFilesSelected, Message: "at least one file must be selected"}
	}

	eng, ok := m.engines.Get("qbittorrent")
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "qBittorrent engine not available"}
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine does not support torrent operations"}
	}

	// Apply file priorities
	if err := torrentEng.SetFilePriorities(ctx, j.EngineID, selections); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("failed to set file priorities: %v", err)}
	}

	// Start the torrent
	if err := torrentEng.StartDownload(ctx, j.EngineID); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("failed to start torrent: %v", err)}
	}

	j.SeedAfterComplete = seedAfterComplete
	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	// Save selections to DB
	if m.torrentRepo != nil {
		var records []TorrentFileRecord
		for _, s := range selections {
			records = append(records, TorrentFileRecord{
				FileIndex: s.Index,
				Selected:  s.Priority != PrioritySkip,
				Priority:  string(s.Priority),
			})
		}
		m.torrentRepo.UpdateTorrentFileSelections(ctx, id, records)

		// Update seed preference
		rec, _ := m.torrentRepo.GetTorrentJob(ctx, id)
		if rec != nil {
			rec.SeedAfterComplete = seedAfterComplete
			m.torrentRepo.UpdateTorrentJob(ctx, rec)
		}
	}

	m.addActive(j)
	m.publish(EventJobUpdated, j)

	return j, nil
}

// StopSeeding stops seeding a completed torrent and marks it completed.
func (m *Manager) StopSeeding(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if j.Status != StatusSeeding {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot stop seeding a %s job", j.Status)}
	}

	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine not available"}
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if ok {
		torrentEng.StopDownload(ctx, j.EngineID)
	}

	j.Status = StatusCompleted
	j.Progress = 100
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)
	m.removeActive(j.ID)
	m.publish(EventJobCompleted, j)

	return j, nil
}

// GetTorrentFiles returns the file list for a torrent job.
func (m *Manager) GetTorrentFiles(ctx context.Context, id string) ([]TorrentFile, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if j.Type != TypeTorrent {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "not a torrent job"}
	}

	if j.EngineID == "" {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "torrent metadata not yet available"}
	}

	eng, ok := m.engines.Get("qbittorrent")
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "qBittorrent engine not available"}
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine does not support torrent operations"}
	}

	return torrentEng.GetFiles(ctx, j.EngineID)
}

// Pause pauses an active download.
func (m *Manager) Pause(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusPaused); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot pause a %s job", j.Status)}
	}

	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine %q not available", j.Engine)}
	}

	if err := eng.Pause(ctx, j); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine pause failed: %v", err)}
	}

	j.Status = StatusPaused
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()

	m.repo.Update(ctx, j)
	m.removeActive(id)
	m.publish(EventJobUpdated, j)

	return j, nil
}

// Resume resumes a paused download.
func (m *Manager) Resume(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusDownloading); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot resume a %s job", j.Status)}
	}

	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine %q not available", j.Engine)}
	}

	if err := eng.Resume(ctx, j); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine resume failed: %v", err)}
	}

	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()

	m.repo.Update(ctx, j)
	m.addActive(j)
	m.publish(EventJobUpdated, j)

	return j, nil
}

// Cancel cancels a download.
func (m *Manager) Cancel(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusCancelled); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot cancel a %s job", j.Status)}
	}

	// For torrent jobs, also remove from qBittorrent
	if j.Type == TypeTorrent && j.EngineID != "" {
		if eng, ok := m.engines.Get(j.Engine); ok {
			if te, ok := eng.(ITorrentEngine); ok {
				te.RemoveTorrent(ctx, j.EngineID, false)
			}
		}
	}

	// Try to cancel in engine (ignore errors for idempotency)
	if j.EngineID != "" {
		if eng, ok := m.engines.Get(j.Engine); ok {
			if err := eng.Cancel(ctx, j); err != nil {
				log.Printf("warning: engine cancel failed for job %s: %v", id, err)
			}
		}
	}

	j.Status = StatusCancelled
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()

	m.repo.Update(ctx, j)
	m.removeActive(id)
	m.publish(EventJobCancelled, j)

	return j, nil
}

// Retry retries a failed job with a fresh engine execution.
func (m *Manager) Retry(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusQueued); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot retry a %s job", j.Status)}
	}

	// Clear old state
	j.Error = ""
	j.Progress = 0
	j.CompletedBytes = 0
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.Status = StatusQueued
	j.UpdatedAt = time.Now()

	m.repo.Update(ctx, j)

	// For media jobs, restart analysis
	if j.Type == TypeMedia {
		j.Status = StatusAnalyzing
		j.Name = "Analyzing..."
		j.MediaInfo = nil
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobUpdated, j)

		go m.analyzeMedia(ctx, j.ID, j.Source)
		return j, nil
	}

	// For torrent jobs, restart metadata acquisition
	if j.Type == TypeTorrent {
		j.Status = StatusAnalyzing
		j.Name = "Fetching metadata..."
		j.TorrentInfo = nil
		j.EngineID = ""
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobUpdated, j)

		torrentFilePath := ""
		if m.torrentRepo != nil {
			rec, _ := m.torrentRepo.GetTorrentJob(ctx, j.ID)
			if rec != nil {
				torrentFilePath = rec.TorrentFilePath
			}
		}

		go m.acquireTorrentMetadata(j.ID, j.Source, torrentFilePath)
		return j, nil
	}

	// Standard download retry
	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("engine %q not available", j.Engine)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	// Start fresh engine execution
	engineID, err := eng.Start(ctx, j, m.downloadDir)
	if err != nil {
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Failed to start download: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return j, nil
	}

	j.EngineID = engineID
	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)

	m.addActive(j)
	m.publish(EventJobUpdated, j)

	return j, nil
}

// Get retrieves a job by ID.
func (m *Manager) Get(ctx context.Context, id string) (*Job, error) {
	return m.repo.GetByID(ctx, id)
}

// List retrieves all jobs.
func (m *Manager) List(ctx context.Context) ([]Job, error) {
	return m.repo.List(ctx)
}

// GetActiveJobs returns a snapshot of all active jobs.
func (m *Manager) GetActiveJobs() map[string]*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make(map[string]*Job, len(m.activeJobs))
	for k, v := range m.activeJobs {
		jobCopy := *v
		snapshot[k] = &jobCopy
	}
	return snapshot
}

// GetEngine returns the engine for a given name.
func (m *Manager) GetEngine(name string) (IEngine, bool) {
	return m.engines.Get(name)
}

// UpdateJobFromEngine updates a job with engine status and persists/publishes.
func (m *Manager) UpdateJobFromEngine(ctx context.Context, j *Job, status *EngineStatus, persistNow bool) {
	j.TotalBytes = status.TotalBytes
	j.CompletedBytes = status.CompletedBytes
	j.SpeedBytesPerSecond = status.SpeedBytesPerSecond
	j.ETASeconds = status.ETASeconds
	j.Progress = status.Progress
	if status.FileName != "" {
		j.Name = status.FileName
	}

	prevStatus := j.Status

	switch status.Status {
	case StatusCompleted:
		// For torrent jobs with seed-after-complete, transition to seeding instead
		if j.Type == TypeTorrent && j.SeedAfterComplete && prevStatus == StatusDownloading {
			j.Status = StatusSeeding
			j.Progress = 100
			j.SpeedBytesPerSecond = status.UploadSpeed
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobUpdated, j)
			return
		}
		// Normal completion
		j.Status = StatusCompleted
		j.Progress = 100
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.removeActive(j.ID)
		m.publish(EventJobCompleted, j)
		return

	case StatusFailed:
		j.Status = StatusFailed
		j.Error = status.Error
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.removeActive(j.ID)
		m.publish(EventJobFailed, j)
		return

	case StatusCancelled:
		j.Status = StatusCancelled
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.removeActive(j.ID)
		m.publish(EventJobCancelled, j)
		return

	case StatusPaused:
		if prevStatus != StatusPaused {
			j.Status = StatusPaused
			j.SpeedBytesPerSecond = 0
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.removeActive(j.ID)
			m.publish(EventJobUpdated, j)
		}
		return

	case StatusProcessing:
		if prevStatus != StatusProcessing {
			j.Status = StatusProcessing
			j.SpeedBytesPerSecond = 0
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobUpdated, j)
		}
		return

	case StatusSeeding:
		if prevStatus != StatusSeeding {
			j.Status = StatusSeeding
			j.Progress = 100
			j.SpeedBytesPerSecond = status.UploadSpeed
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			// Keep in activeJobs for monitoring
			m.publish(EventJobUpdated, j)
		} else {
			// Update upload stats while seeding
			if j.TorrentInfo != nil {
				j.TorrentInfo.UploadSpeed = status.UploadSpeed
				j.TorrentInfo.Uploaded = status.Uploaded
				j.TorrentInfo.Ratio = status.Ratio
				j.TorrentInfo.Seeders = status.Seeders
				j.TorrentInfo.Leechers = status.Leechers
			}
			j.SpeedBytesPerSecond = status.UploadSpeed
			j.UpdatedAt = time.Now()
			if persistNow {
				m.repo.Update(ctx, j)
			}
			m.mu.Lock()
			m.activeJobs[j.ID] = j
			m.mu.Unlock()
			m.publish(EventJobUpdated, j)
		}
		return
	}

	// Still downloading — update in-memory and optionally persist
	j.Status = StatusDownloading
	j.UpdatedAt = time.Now()

	// Update the active job in-memory
	m.mu.Lock()
	m.activeJobs[j.ID] = j
	m.mu.Unlock()

	if persistNow {
		m.repo.Update(ctx, j)
	}

	m.publish(EventJobUpdated, j)
}

// --- helpers ---

func (m *Manager) getJobOrError(ctx context.Context, id string) (*Job, error) {
	j, err := m.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	if j == nil {
		return nil, &AppError{Code: ErrJobNotFound, Message: "job not found"}
	}
	return j, nil
}

func (m *Manager) addActive(j *Job) {
	m.mu.Lock()
	m.activeJobs[j.ID] = j
	m.mu.Unlock()
}

func (m *Manager) removeActive(id string) {
	m.mu.Lock()
	delete(m.activeJobs, id)
	m.mu.Unlock()
}

func (m *Manager) publish(eventType string, j *Job) {
	m.bus.Publish(Event{
		Type: eventType,
		Job:  *j,
	})
}
