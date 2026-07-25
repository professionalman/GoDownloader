package job

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"downloader/internal/settings"
	"downloader/internal/storage"

	"github.com/google/uuid"
)

// Manager is the central orchestrator for job lifecycle management.
// All state transitions go through the Manager.
type Manager struct {
	repo           IJobRepository
	engines        IEngineRegistry
	bus            IEventBus
	downloadDir    string
	dataDir        string
	torrentRepo    ITorrentRepository
	queueRepo      IQueueRepository
	settings       *settings.SettingsService
	storageService storage.IStorageService
	categoryRepo   storage.ICategoryRepository
	scheduler      *Scheduler

	mu            sync.RWMutex
	activeJobs    map[string]*Job // id -> job (in-memory cache for active jobs)
	activeCancels map[string]context.CancelFunc

	monitor *Monitor
}

// NewManager creates a new job manager.
func NewManager(repo IJobRepository, engines IEngineRegistry, bus IEventBus, downloadDir string, torrentRepo ITorrentRepository, dataDir ...string) *Manager {
	dDir := "./data"
	if len(dataDir) > 0 && dataDir[0] != "" {
		dDir = dataDir[0]
	}
	m := &Manager{
		repo:          repo,
		engines:       engines,
		bus:           bus,
		downloadDir:   downloadDir,
		dataDir:       dDir,
		torrentRepo:   torrentRepo,
		activeJobs:    make(map[string]*Job),
		activeCancels: make(map[string]context.CancelFunc),
	}
	return m
}

// GetDefaultDownloadDir returns the manager's fallback default download directory.
func (m *Manager) GetDefaultDownloadDir() string {
	return m.downloadDir
}

// GetEffectiveDefaultDownloadDir returns the effective default download directory from StorageService or SettingsService.
func (m *Manager) GetEffectiveDefaultDownloadDir(ctx context.Context) string {
	if m.storageService != nil {
		return m.storageService.GetEffectiveDefaultDownloadDir(ctx)
	}
	if m.settings != nil {
		if st, err := m.settings.GetSettings(ctx); err == nil {
			return st.Storage.EffectiveDefaultDownloadDirectory
		}
	}
	if abs, err := filepath.Abs(m.downloadDir); err == nil {
		return abs
	}
	return m.downloadDir
}

// SetQueueRepository wires the queue repository.
func (m *Manager) SetQueueRepository(queueRepo IQueueRepository) {
	m.queueRepo = queueRepo
}

// SetSettingsService wires the settings service.
func (m *Manager) SetSettingsService(s *settings.SettingsService) {
	m.settings = s
}

// SetStorageService wires the storage service.
func (m *Manager) SetStorageService(svc storage.IStorageService) {
	m.storageService = svc
}

// SetCategoryRepository wires the category repository.
func (m *Manager) SetCategoryRepository(catRepo storage.ICategoryRepository) {
	m.categoryRepo = catRepo
}

// SetScheduler wires the scheduler instance.
func (m *Manager) SetScheduler(s *Scheduler) {
	m.scheduler = s
	if s != nil {
		s.SetEventBus(m.bus)
		s.SetEngineRegistry(m.engines)
		s.SetAddActiveFunc(m.addActive)
	}
}

// StartBackgroundTasks starts recovery, queue cleanup, scheduler, and progress monitor.
// Call this after creating the Manager.
func (m *Manager) StartBackgroundTasks(ctx context.Context) {
	// 1. Run recovery first
	m.recover(ctx)

	// 2. Clean up stale GoDownloader-owned workdirs on startup
	if m.storageService != nil {
		allJobs, err := m.repo.List(ctx)
		if err != nil {
			log.Printf("startup: failed to list jobs for workdir cleanup: %v", err)
		} else {
			preservedJobIDs := make(map[string]bool)
			for _, j := range allJobs {
				if j.Status == StatusQueued || j.Status == StatusPaused ||
					j.Status == StatusAnalyzing || j.Status == StatusAwaitingSelection ||
					j.Status == StatusDownloading || j.Status == StatusProcessing {
					preservedJobIDs[j.ID] = true
				}
			}
			if err := m.storageService.CleanupStaleWorkDirs(ctx, preservedJobIDs); err != nil {
				log.Printf("startup: cleanup stale workdirs error: %v", err)
			}
		}
	}

	// 3. Clean up stale queue entries on startup
	if m.queueRepo != nil {
		m.cleanupQueueOnStartup(ctx)
	}

	// 4. Start scheduler and kick once
	if m.scheduler != nil {
		m.scheduler.Start(ctx)
		m.scheduler.Kick()
	}

	// 5. Start progress monitor
	m.monitor = NewMonitor(m, 1*time.Second)
	m.monitor.Start(ctx)
}

// Stop stops background tasks, cancels active background analysis/metadata tasks, and shuts down subprocess engines.
func (m *Manager) Stop() {
	if m.scheduler != nil {
		m.scheduler.Stop()
	}
	if m.monitor != nil {
		m.monitor.Stop()
	}

	m.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.activeCancels))
	for id, cancel := range m.activeCancels {
		if cancel != nil {
			cancels = append(cancels, cancel)
		}
		delete(m.activeCancels, id)
	}
	m.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}

	if eng, ok := m.engines.Get("ytdlp"); ok {
		if sEng, ok := eng.(IShutdownableEngine); ok {
			sEng.Shutdown()
		}
	}
}

func (m *Manager) registerCancel(jobID string, cancel context.CancelFunc) {
	m.mu.Lock()
	m.activeCancels[jobID] = cancel
	m.mu.Unlock()
}

func (m *Manager) unregisterCancel(jobID string) {
	m.mu.Lock()
	delete(m.activeCancels, jobID)
	m.mu.Unlock()
}

func (m *Manager) triggerCancel(jobID string) {
	m.mu.Lock()
	cancel, exists := m.activeCancels[jobID]
	if exists {
		delete(m.activeCancels, jobID)
	}
	m.mu.Unlock()
	if exists && cancel != nil {
		cancel()
	}
}

type CreateOptions struct {
	Priority       JobPriority
	BatchID        string
	DeferSchedule  bool
	CategoryID     string
	DestinationDir string
	ConflictPolicy FilenameConflictPolicy
}

func (m *Manager) resolveJobStorage(ctx context.Context, categoryID, customDest string, policy FilenameConflictPolicy, jobID string, isMedia bool) (*storage.StorageResolution, error) {
	if m.storageService != nil {
		return m.storageService.ResolveDestination(ctx, categoryID, customDest, storage.FilenameConflictPolicy(policy), jobID, isMedia)
	}

	dest := m.downloadDir
	if customDest != "" {
		dest = customDest
	}
	pol := policy
	if pol == "" {
		pol = ConflictPolicyRename
	}
	workDir := ""
	if isMedia {
		workDir = filepath.Join(m.dataDir, "tmp", jobID)
	}
	return &storage.StorageResolution{
		CategoryID:     categoryID,
		DestinationDir: dest,
		WorkDir:        workDir,
		ConflictPolicy: storage.FilenameConflictPolicy(pol),
	}, nil
}

// Create validates the URL, creates a job, and enqueues or starts it.
func (m *Manager) Create(ctx context.Context, source string, priority ...JobPriority) (*Job, error) {
	p := JobPriorityNormal
	if len(priority) > 0 && priority[0] != "" {
		if !ValidJobPriority(priority[0]) {
			return nil, &AppError{Code: ErrInvalidPriority, Message: fmt.Sprintf("invalid job priority: %s", priority[0])}
		}
		p = priority[0]
	}
	return m.CreateWithOptions(ctx, source, CreateOptions{Priority: p})
}

// CreateWithOptions creates a job with custom priority and batch settings.
func (m *Manager) CreateWithOptions(ctx context.Context, source string, opts CreateOptions) (*Job, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, &AppError{Code: ErrInvalidURL, Message: "source cannot be empty"}
	}
	if opts.Priority == "" {
		opts.Priority = JobPriorityNormal
	} else if !ValidJobPriority(opts.Priority) {
		return nil, &AppError{Code: ErrInvalidPriority, Message: fmt.Sprintf("invalid job priority: %s", opts.Priority)}
	}

	// Handle magnet URIs
	if strings.HasPrefix(strings.ToLower(source), "magnet:") {
		return m.createTorrentJobWithOptions(ctx, source, "", opts)
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

	jobID := "job_" + uuid.New().String()[:8]
	res, resErr := m.resolveJobStorage(ctx, opts.CategoryID, opts.DestinationDir, opts.ConflictPolicy, jobID, engineName == "ytdlp")
	if resErr != nil {
		return nil, &AppError{Code: ErrInvalidStorageSelection, Message: resErr.Error()}
	}

	// Extract filename from URL
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		name = "download"
	}

	now := time.Now()
	j := &Job{
		ID:             jobID,
		Source:         source,
		Name:           name,
		Status:         StatusQueued,
		Type:           TypeDownload,
		Engine:         engineName,
		Priority:       opts.Priority,
		BatchID:        opts.BatchID,
		CategoryID:     res.CategoryID,
		DestinationDir: res.DestinationDir,
		WorkDir:        res.WorkDir,
		ConflictPolicy: FilenameConflictPolicy(res.ConflictPolicy),
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	// For media engine, set type and start analysis
	if engineName == "ytdlp" {
		j.Type = TypeMedia
		j.Status = StatusAnalyzing
		j.Name = "Analyzing..."

		if err := m.repo.Create(ctx, j); err != nil {
			return nil, fmt.Errorf("persist job: %w", err)
		}

		m.publish(EventJobCreated, j)
		go m.analyzeMedia(ctx, j.ID, source)
		return j, nil
	}

	// Standard download flow (aria2)
	j.Type = TypeDownload

	if err := m.enqueueJob(ctx, j, QueueActionStart); err != nil {
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue job: %v", err)}
	}

	if err := m.repo.Create(ctx, j); err != nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		return nil, fmt.Errorf("persist job: %w", err)
	}

	// Fallback for test doubles created without scheduler
	if m.scheduler == nil {
		eng, ok := m.engines.Get(engineName)
		if !ok {
			j.Status = StatusFailed
			j.Error = fmt.Sprintf("engine %q not available", engineName)
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
		j.Status = StatusDownloading
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		m.addActive(j)
		m.publish(EventJobCreated, j)
		return j, nil
	}

	m.publish(EventJobCreated, j)

	if !opts.DeferSchedule && m.scheduler != nil {
		m.scheduler.Kick()
	}

	return j, nil
}

// CreateBatch creates multiple jobs best-effort from a batch submission.
func (m *Manager) CreateBatch(ctx context.Context, req CreateBatchRequest) (*CreateBatchResponse, error) {
	if len(req.Inputs) == 0 && len(req.Sources) > 0 {
		req.Inputs = make([]BatchInput, len(req.Sources))
		for i, s := range req.Sources {
			req.Inputs[i] = BatchInput{
				Source:         s,
				Priority:       req.Priority,
				CategoryID:     req.CategoryID,
				DestinationDir: req.DestinationDir,
				ConflictPolicy: req.ConflictPolicy,
			}
		}
	}

	if len(req.Inputs) == 0 {
		return nil, &AppError{Code: ErrInvalidRequest, Message: "batch inputs cannot be empty"}
	}
	if len(req.Inputs) > 100 {
		return nil, &AppError{Code: ErrBatchLimitExceeded, Message: "batch inputs cannot exceed 100"}
	}

	batchID := fmt.Sprintf("batch_%s", uuid.New().String()[:8])
	resp := &CreateBatchResponse{
		BatchID: batchID,
		Items:   make([]BatchItemResult, 0, len(req.Inputs)),
	}

	for i, input := range req.Inputs {
		source := strings.TrimSpace(input.Source)
		if source == "" {
			resp.Failed++
			resp.Items = append(resp.Items, BatchItemResult{
				Index: i,
				Error: &AppError{Code: ErrInvalidURL, Message: "source input is empty"},
			})
			continue
		}

		catID := input.CategoryID
		if catID == "" {
			catID = req.CategoryID
		}
		destDir := input.DestinationDir
		if destDir == "" {
			destDir = req.DestinationDir
		}
		conflictPol := input.ConflictPolicy
		if conflictPol == "" {
			conflictPol = req.ConflictPolicy
		}
		p := input.Priority
		if p == "" {
			p = req.Priority
		}
		if p == "" {
			p = JobPriorityNormal
		}

		j, err := m.CreateWithOptions(ctx, source, CreateOptions{
			Priority:       p,
			BatchID:        batchID,
			DeferSchedule:  true,
			CategoryID:     catID,
			DestinationDir: destDir,
			ConflictPolicy: conflictPol,
		})

		if err != nil {
			resp.Failed++
			appErr, ok := err.(*AppError)
			if !ok {
				appErr = &AppError{Code: ErrInternalError, Message: err.Error()}
			}
			resp.Items = append(resp.Items, BatchItemResult{
				Index: i,
				Error: appErr,
			})
		} else {
			resp.Created++
			resp.Items = append(resp.Items, BatchItemResult{
				Index: i,
				Job:   j,
			})
		}
	}

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	return resp, nil
}

// analyzeMedia runs yt-dlp analysis in the background and updates the job.
func (m *Manager) analyzeMedia(parentCtx context.Context, jobID, source string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.registerCancel(jobID, cancel)
	defer m.unregisterCancel(jobID)

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
		if ctx.Err() != nil {
			log.Printf("analyzeMedia: job %s analysis was cancelled", jobID)
			return
		}
		j.Status = StatusFailed
		j.Error = fmt.Sprintf("Media analysis failed: %v", err)
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Verify job is still in analyzing state before committing metadata
	current, err := m.repo.GetByID(ctx, jobID)
	if err != nil || current == nil || current.Status == StatusCancelled {
		log.Printf("analyzeMedia: job %s was cancelled or deleted, aborting commit", jobID)
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

// SelectFormat selects a format for a media job and enqueues or starts the download.
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

	// Set selected format & transition to StatusQueued
	j.MediaInfo.SelectedFmt = formatID
	oldStatus := j.Status

	if err := m.enqueueJob(ctx, j, QueueActionStart); err != nil {
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue media job: %v", err)}
	}

	j.Status = StatusQueued
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		j.Status = oldStatus
		return nil, fmt.Errorf("update job status: %w", err)
	}

	// Fallback for test doubles created without scheduler
	if m.scheduler == nil {
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
		j.Status = StatusDownloading
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		m.addActive(j)
		m.publish(EventJobUpdated, j)
		return j, nil
	}

	m.publish(EventJobUpdated, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	return j, nil
}

func (m *Manager) createTorrentJob(ctx context.Context, source, torrentFilePath string) (*Job, error) {
	return m.createTorrentJobWithOptions(ctx, source, torrentFilePath, CreateOptions{Priority: JobPriorityNormal})
}

func (m *Manager) createTorrentJobWithOptions(ctx context.Context, source, torrentFilePath string, opts CreateOptions) (*Job, error) {
	jobID := "job_" + uuid.New().String()[:8]
	return m.createTorrentJobWithIDAndOptions(ctx, jobID, source, torrentFilePath, opts)
}

func (m *Manager) createTorrentJobWithID(ctx context.Context, jobID, source, torrentFilePath string) (*Job, error) {
	return m.createTorrentJobWithIDAndOptions(ctx, jobID, source, torrentFilePath, CreateOptions{Priority: JobPriorityNormal})
}

func (m *Manager) createTorrentJobWithIDAndOptions(ctx context.Context, jobID, source, torrentFilePath string, opts CreateOptions) (*Job, error) {
	engineName := m.engines.Detect(source)
	if engineName != "qbittorrent" {
		return nil, &AppError{Code: ErrEngineError, Message: "qBittorrent engine not available for torrent downloads"}
	}

	if opts.Priority == "" {
		opts.Priority = JobPriorityNormal
	} else if !ValidJobPriority(opts.Priority) {
		return nil, &AppError{Code: ErrInvalidPriority, Message: fmt.Sprintf("invalid job priority: %s", opts.Priority)}
	}

	// Check for duplicate info hash if source is a magnet link
	if hash, err := ExtractMagnetHash(source); err == nil && hash != "" && m.torrentRepo != nil {
		rec, err := m.torrentRepo.GetActiveTorrentJobByInfoHash(ctx, hash)
		if err != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to verify torrent ownership: %v", err)}
		}
		if rec != nil {
			return nil, &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("a torrent with info hash %s is already managed by job %s", hash, rec.JobID)}
		}
	}

	res, resErr := m.resolveJobStorage(ctx, opts.CategoryID, opts.DestinationDir, ConflictPolicyEngineManaged, jobID, false)
	if resErr != nil {
		return nil, &AppError{Code: ErrInvalidStorageSelection, Message: resErr.Error()}
	}

	now := time.Now()
	j := &Job{
		ID:             jobID,
		Source:         source,
		Name:           "Fetching metadata...",
		Status:         StatusAnalyzing,
		Type:           TypeTorrent,
		Engine:         engineName,
		Priority:       opts.Priority,
		BatchID:        opts.BatchID,
		CategoryID:     res.CategoryID,
		DestinationDir: res.DestinationDir,
		WorkDir:        "",
		ConflictPolicy: ConflictPolicyEngineManaged,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := m.repo.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("persist job: %w", err)
	}

	m.publish(EventJobCreated, j)

	// Run metadata acquisition in background
	go m.acquireTorrentMetadata(j.ID, source, torrentFilePath)

	return j, nil
}

// CreateTorrentFromFile creates a torrent job from an uploaded .torrent file and persists it in DATA_DIR.
func (m *Manager) CreateTorrentFromFile(ctx context.Context, torrentFilePath string) (*Job, error) {
	return m.CreateTorrentFromFileWithOptions(ctx, torrentFilePath, CreateOptions{Priority: JobPriorityNormal})
}

// CreateTorrentFromFileWithOptions copies the uploaded .torrent file and creates a torrent job with options.
func (m *Manager) CreateTorrentFromFileWithOptions(ctx context.Context, torrentFilePath string, opts CreateOptions) (*Job, error) {
	data, err := os.ReadFile(torrentFilePath)
	if err != nil {
		return nil, fmt.Errorf("read uploaded torrent file: %w", err)
	}

	jobID := "job_" + uuid.New().String()[:8]
	persistedPath := filepath.Join(m.dataDir, "torrents", jobID+".torrent")

	if err := os.MkdirAll(filepath.Dir(persistedPath), 0755); err != nil {
		return nil, fmt.Errorf("create torrents data dir: %w", err)
	}

	if err := os.WriteFile(persistedPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write persisted torrent file: %w", err)
	}

	os.Remove(torrentFilePath)

	return m.createTorrentJobWithIDAndOptions(ctx, jobID, "torrent://"+persistedPath, persistedPath, opts)
}

// acquireTorrentMetadata adds the torrent to qBittorrent and polls for metadata.
func (m *Manager) acquireTorrentMetadata(jobID, source, torrentFilePath string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.registerCancel(jobID, cancel)
	defer m.unregisterCancel(jobID)

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

	if (strings.HasPrefix(source, "torrent://") || filepath.Ext(source) == ".torrent") && torrentFilePath == "" {
		j.Status = StatusFailed
		j.Error = "torrent metainfo file path missing; cannot acquire metadata"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		return
	}

	// Add torrent to qBittorrent (stopped)
	saveDir := j.DestinationDir
	if saveDir == "" {
		saveDir = m.downloadDir
	}

	var infoHash string
	if torrentFilePath != "" {
		infoHash, err = torrentEng.AddTorrentFile(ctx, torrentFilePath, saveDir, jobID)
	} else {
		infoHash, err = torrentEng.AddMagnet(ctx, source, saveDir, jobID)
	}
	if err != nil {
		if ctx.Err() != nil {
			log.Printf("acquireTorrentMetadata: job %s cancelled during add", jobID)
			return
		}
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

	// Authoritative duplicate check: verify if another active job owns this infoHash
	if m.torrentRepo != nil && infoHash != "" {
		rec, err := m.torrentRepo.GetActiveTorrentJobByInfoHash(ctx, infoHash)
		if err != nil {
			log.Printf("acquireTorrentMetadata: active job lookup failed for job %s (infoHash=%s): %v", jobID, infoHash, err)
			// Ownership lookup failed due to DB error. Fail closed without calling RemoveTorrent to prevent deleting an existing active torrent.
			errText := fmt.Sprintf("failed to verify torrent ownership: %v", err)
			if createErr := m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
				JobID:           jobID,
				InfoHash:        infoHash,
				TorrentFilePath: torrentFilePath,
			}); createErr != nil {
				log.Printf("acquireTorrentMetadata: failed to save torrent record for job %s: %v", jobID, createErr)
				errText = fmt.Sprintf("%s; failed to preserve torrent retry metadata: %v", errText, createErr)
			}
			j.Status = StatusFailed
			j.Error = errText
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobFailed, j)
			return
		}
		if rec != nil && rec.JobID != jobID {
			log.Printf("acquireTorrentMetadata: duplicate info hash %s detected for job %s (already managed by active job %s)", infoHash, jobID, rec.JobID)
			// DO NOT call RemoveTorrent(infoHash) because qBittorrent deduplicates by infoHash!
			// DO NOT delete torrentFilePath! Preserve new job's .torrent file and TorrentJobRecord so Retry() remains possible after original job completes.
			errTxt := fmt.Sprintf("a torrent with info hash %s is already managed by job %s", infoHash, rec.JobID)
			if createErr := m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
				JobID:           jobID,
				InfoHash:        infoHash,
				TorrentFilePath: torrentFilePath,
			}); createErr != nil {
				log.Printf("acquireTorrentMetadata: failed to preserve torrent retry metadata for job %s: %v", jobID, createErr)
				errTxt = fmt.Sprintf("%s (failed to preserve retry metadata: %v)", errTxt, createErr)
			}
			j.Status = StatusFailed
			j.Error = errTxt
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobFailed, j)
			return
		}
	}

	// Poll for metadata with cancellation support & file readiness validation
	var metadata *TorrentInfo
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	timeoutCh := time.After(120 * time.Second)

loop:
	for {
		select {
		case <-ctx.Done():
			log.Printf("acquireTorrentMetadata: job %s background task cancelled", jobID)
			return
		case <-timeoutCh:
			break loop
		case <-ticker.C:
			current, err := m.repo.GetByID(ctx, jobID)
			if err != nil || current == nil || current.Status == StatusCancelled {
				log.Printf("acquireTorrentMetadata: job %s cancelled in DB", jobID)
				return
			}

			info, err := torrentEng.GetTorrentInfo(ctx, infoHash)
			if err == nil && info != nil && info.Name != "" && info.Name != infoHash {
				files, errFiles := torrentEng.GetFiles(ctx, infoHash)
				if errFiles == nil && len(files) > 0 {
					metadata = info
					break loop
				}
			}
		}
	}

	if metadata == nil {
		j.Status = StatusFailed
		j.Error = "Timed out waiting for torrent metadata from peers"
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)
		torrentEng.RemoveTorrent(ctx, infoHash, false)
		return
	}

	// Verify job state before updating
	current, err := m.repo.GetByID(ctx, jobID)
	if err != nil || current == nil || current.Status == StatusCancelled {
		log.Printf("acquireTorrentMetadata: job %s was cancelled before completion", jobID)
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
			JobID:           jobID,
			InfoHash:        infoHash,
			Name:            metadata.Name,
			TotalSize:       metadata.TotalSize,
			TorrentFilePath: torrentFilePath,
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

	eng, ok := m.engines.Get("qbittorrent")
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "qBittorrent engine not available"}
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine does not support torrent operations"}
	}

	// 1. Fetch available files from engine for index validation
	existingFiles, err := torrentEng.GetFiles(ctx, j.EngineID)
	if err != nil || len(existingFiles) == 0 {
		return nil, &AppError{Code: ErrEngineError, Message: "failed to retrieve torrent file list for validation"}
	}
	validIndices := make(map[int]bool)
	for _, f := range existingFiles {
		validIndices[f.Index] = true
	}

	// 2. Validate selections: index, duplicates, priorities, count >= 1
	seenIndex := make(map[int]bool)
	hasSelected := false

	for _, s := range selections {
		if !validIndices[s.Index] {
			return nil, &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("unknown file index: %d", s.Index)}
		}
		if seenIndex[s.Index] {
			return nil, &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("duplicate file index: %d", s.Index)}
		}
		seenIndex[s.Index] = true

		if !ValidPriority(s.Priority) {
			return nil, &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("invalid priority %q for file index %d", s.Priority, s.Index)}
		}

		if s.Priority != PrioritySkip {
			hasSelected = true
		}
	}

	if !hasSelected {
		return nil, &AppError{Code: ErrNoFilesSelected, Message: "at least one file must be selected"}
	}

	// 3. Apply file priorities
	if err := torrentEng.SetFilePriorities(ctx, j.EngineID, selections); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("failed to set file priorities: %v", err)}
	}

	// 4. Calculate selected payload bytes & Save selections & SeedAfterComplete to DB
	fileSizeMap := make(map[int]int64)
	for _, f := range existingFiles {
		fileSizeMap[f.Index] = f.Size
	}

	selectedBytes := int64(0)
	for _, s := range selections {
		if s.Priority != PrioritySkip {
			selectedBytes += fileSizeMap[s.Index]
		}
	}

	j.TotalBytes = selectedBytes
	j.SeedAfterComplete = seedAfterComplete

	if m.torrentRepo != nil {
		var records []TorrentFileRecord
		for _, s := range selections {
			records = append(records, TorrentFileRecord{
				JobID:     id,
				FileIndex: s.Index,
				Selected:  s.Priority != PrioritySkip,
				Priority:  string(s.Priority),
			})
		}
		if err := m.torrentRepo.UpdateTorrentFileSelections(ctx, id, records); err != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to save torrent file selections: %v", err)}
		}

		rec, err := m.torrentRepo.GetTorrentJob(ctx, id)
		if err != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to get torrent job record: %v", err)}
		}
		if rec != nil {
			rec.SeedAfterComplete = seedAfterComplete
			if err := m.torrentRepo.UpdateTorrentJob(ctx, rec); err != nil {
				return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to update torrent job record: %v", err)}
			}
		}
	}

	// 5. Enqueue queue entry FIRST before updating job status to QUEUED
	if err := m.enqueueJob(ctx, j, QueueActionStart); err != nil {
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue torrent job: %v", err)}
	}

	j.Status = StatusQueued
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		return nil, fmt.Errorf("update job status: %w", err)
	}

	// Fallback for test doubles created without scheduler
	if m.scheduler == nil {
		if err := torrentEng.StartDownload(ctx, j.EngineID); err != nil {
			j.Status = StatusFailed
			j.Error = fmt.Sprintf("failed to start torrent: %v", err)
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			if m.queueRepo != nil {
				m.queueRepo.Delete(ctx, j.ID)
			}
			m.publish(EventJobFailed, j)
			return j, nil
		}
		j.Status = StatusDownloading
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		m.addActive(j)
		m.publish(EventJobUpdated, j)
		return j, nil
	}

	m.publish(EventJobUpdated, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

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
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine does not support torrent operations"}
	}

	if err := torrentEng.StopDownload(ctx, j.EngineID); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("failed to stop torrent seeding: %v", err)}
	}
	if err := torrentEng.RemoveTorrent(ctx, j.EngineID, false); err != nil {
		return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("failed to remove torrent from daemon: %v", err)}
	}

	j.Status = StatusCompleted
	j.Progress = 100
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	m.repo.Update(ctx, j)
	m.removeActive(j.ID)
	m.publish(EventJobCompleted, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

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

// Pause pauses an active or queued download.
func (m *Manager) Pause(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusPaused); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot pause a %s job", j.Status)}
	}

	// Pausing a QUEUED job
	if j.Status == StatusQueued {
		j.Status = StatusPaused
		j.UpdatedAt = time.Now()
		if err := m.repo.Update(ctx, j); err != nil {
			return nil, fmt.Errorf("pause queued job: %w", err)
		}
		m.publish(EventJobUpdated, j)
		return j, nil
	}

	// Pausing an actively downloading job
	if j.EngineID != "" {
		eng, ok := m.engines.Get(j.Engine)
		if !ok {
			return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine %q not available", j.Engine)}
		}
		if err := eng.Pause(ctx, j); err != nil {
			return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine pause failed: %v", err)}
		}
	}

	j.Status = StatusPaused
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()

	var enqueueErr error
	if m.queueRepo != nil {
		enqueueErr = m.enqueueJob(ctx, j, QueueActionResume)
	}

	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("Pause: update job status for %s failed: %v", id, err)
		if enqueueErr != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue resume queue entry and update job status: %v", err)}
		}
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("pause succeeded externally but failed to update job status in DB: %v", err)}
	}

	if enqueueErr != nil {
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue resume queue entry: %v", enqueueErr)}
	}

	m.removeActive(id)
	m.publish(EventJobUpdated, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	return j, nil
}

// Resume resumes a paused download via the queue scheduler.
func (m *Manager) Resume(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusQueued); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot resume a %s job", j.Status)}
	}

	action := QueueActionResume
	if m.queueRepo != nil {
		entry, getErr := m.queueRepo.Get(ctx, id)
		if getErr != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to read queue entry: %v", getErr)}
		}
		if entry != nil {
			action = entry.Action
			entry.UpdatedAt = time.Now()
			if err := m.queueRepo.Enqueue(ctx, entry); err != nil {
				return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to update queue entry: %v", err)}
			}
		} else {
			if err := m.enqueueJob(ctx, j, QueueActionResume); err != nil {
				return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue resume queue item: %v", err)}
			}
		}
	}

	j.Status = StatusQueued
	j.Error = ""
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		return nil, fmt.Errorf("update job status: %w", err)
	}

	// Fallback for test doubles created without scheduler
	if m.scheduler == nil {
		eng, ok := m.engines.Get(j.Engine)
		if !ok {
			return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine %q not available for resume", j.Engine)}
		}
		if action == QueueActionStart {
			engineID, err := eng.Start(ctx, j, m.downloadDir)
			if err != nil {
				return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine start failed: %v", err)}
			}
			j.EngineID = engineID
		} else {
			if err := eng.Resume(ctx, j); err != nil {
				return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine resume failed: %v", err)}
			}
		}
		j.Status = StatusDownloading
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		m.addActive(j)
		m.publish(EventJobUpdated, j)
		return j, nil
	}

	m.publish(EventJobUpdated, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

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

	// If job has an EngineID, cancel in engine first and enforce success
	if j.EngineID != "" {
		eng, ok := m.engines.Get(j.Engine)
		if !ok {
			return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine %q not available for cancellation", j.Engine)}
		}
		if err := eng.Cancel(ctx, j); err != nil {
			log.Printf("engine cancel failed for job %s: %v", id, err)
			return nil, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("engine cancel failed: %v", err)}
		}
	}

	m.triggerCancel(id)

	j.Status = StatusCancelled
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()

	// Persist CANCELLED status FIRST before deleting queue entry or publishing event
	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("Cancel: external cancellation succeeded for job %s but CANCELLED state persistence failed: %v", id, err)
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to persist cancel state: %v", err)}
	}

	if m.queueRepo != nil {
		if delErr := m.queueRepo.Delete(ctx, id); delErr != nil {
			log.Printf("Cancel: queue delete failed for job %s: %v", id, delErr)
		}
	}

	m.removeActive(id)
	m.publish(EventJobCancelled, j)

	if eng, ok := m.engines.Get(j.Engine); ok {
		if cleanupEng, ok := eng.(ICleanupableEngine); ok {
			cleanupEng.Cleanup(j.ID)
		}
	}

	if j.Type == TypeMedia && m.storageService != nil && j.WorkDir != "" {
		if err := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); err != nil {
			log.Printf("Cancel: cleanup workdir error for job %s: %v", j.ID, err)
		}
	}

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	return j, nil
}

// Retry retries a failed job with a fresh engine execution via the scheduler.
func (m *Manager) Retry(ctx context.Context, id string) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	if err := ValidateTransition(j.Status, StatusQueued); err != nil {
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot retry a %s job", j.Status)}
	}

	// For media jobs, restart analysis
	if j.Type == TypeMedia {
		if j.WorkDir != "" {
			if _, err := os.Stat(j.WorkDir); err == nil {
				if validateErr := storage.ValidateWorkDirMarker(j.WorkDir, j.ID); validateErr != nil {
					log.Printf("Retry: unsafe workdir marker validation failed for job %s: %v", j.ID, validateErr)
					return nil, &AppError{Code: ErrStorageError, Message: fmt.Sprintf("cannot retry media job: workdir safety marker invalid or missing: %v", validateErr)}
				}
				if m.storageService != nil {
					if cleanupErr := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); cleanupErr != nil {
						log.Printf("Retry: cleanup workdir failed for job %s: %v", j.ID, cleanupErr)
						return nil, &AppError{Code: ErrStorageError, Message: fmt.Sprintf("cannot retry media job: cleanup workdir failed: %v", cleanupErr)}
					}
				}
			}
		}
		j.Status = StatusAnalyzing
		j.Name = "Analyzing..."
		j.MediaInfo = nil
		j.Error = ""
		j.Progress = 0
		j.CompletedBytes = 0
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()

		if err := m.repo.Update(ctx, j); err != nil {
			log.Printf("Retry: failed to persist ANALYZING state for job %s: %v", j.ID, err)
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to persist retry state: %v", err)}
		}

		m.publish(EventJobUpdated, j)

		go m.analyzeMedia(ctx, j.ID, j.Source)
		return j, nil
	}

	// For torrent jobs, restart metadata acquisition
	if j.Type == TypeTorrent {
		torrentFilePath := ""
		if m.torrentRepo != nil {
			rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
			if err != nil {
				return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to load torrent metadata: %v", err)}
			}
			if rec != nil {
				torrentFilePath = rec.TorrentFilePath
			}
		}

		if strings.HasPrefix(j.Source, "torrent://") || filepath.Ext(j.Source) == ".torrent" {
			if torrentFilePath == "" {
				return nil, &AppError{Code: ErrInvalidJobState, Message: "torrent metainfo record missing; retry not possible"}
			}
			if _, err := os.Stat(torrentFilePath); os.IsNotExist(err) {
				return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("torrent metainfo file no longer exists at %s; retry not possible", torrentFilePath)}
			}
		}

		j.Status = StatusAnalyzing
		j.Name = "Fetching metadata..."
		j.TorrentInfo = nil
		j.EngineID = ""
		j.Error = ""
		j.Progress = 0
		j.CompletedBytes = 0
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobUpdated, j)

		go m.acquireTorrentMetadata(j.ID, j.Source, torrentFilePath)
		return j, nil
	}

	// Standard download retry
	if err := m.enqueueJob(ctx, j, QueueActionStart); err != nil {
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to enqueue retry job: %v", err)}
	}

	j.Error = ""
	j.Progress = 0
	j.CompletedBytes = 0
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.Status = StatusQueued
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		return nil, fmt.Errorf("update job status: %w", err)
	}

	// Fallback for test doubles created without scheduler
	if m.scheduler == nil {
		eng, ok := m.engines.Get(j.Engine)
		if !ok {
			j.Status = StatusFailed
			j.Error = fmt.Sprintf("engine %q not available", j.Engine)
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
		j.Status = StatusDownloading
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, j.ID)
		}
		m.addActive(j)
		m.publish(EventJobUpdated, j)
		return j, nil
	}

	m.publish(EventJobUpdated, j)

	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	return j, nil
}

// Get retrieves a job by ID.
func (m *Manager) Get(ctx context.Context, id string) (*Job, error) {
	j, err := m.repo.GetByID(ctx, id)
	if err != nil || j == nil {
		return j, err
	}
	m.hydrateJob(ctx, j)
	return j, nil
}

// List retrieves all jobs.
func (m *Manager) List(ctx context.Context) ([]Job, error) {
	jobs, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		m.hydrateJob(ctx, &jobs[i])
	}
	return jobs, nil
}

func (m *Manager) hydrateJob(ctx context.Context, j *Job) {
	if j == nil {
		return
	}
	if j.DestinationDir == "" {
		effDir := m.GetEffectiveDefaultDownloadDir(ctx)
		j.DestinationDir = effDir
		_ = m.repo.Update(ctx, j)
	}
	if j.Type == TypeTorrent && m.torrentRepo != nil {
		rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
		if err == nil && rec != nil {
			j.SeedAfterComplete = rec.SeedAfterComplete
			if j.TorrentInfo == nil && (rec.Name != "" || rec.TotalSize > 0) {
				j.TorrentInfo = &TorrentInfo{
					Name:      rec.Name,
					InfoHash:  rec.InfoHash,
					TotalSize: rec.TotalSize,
				}
			}
		}
	}
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
		// For torrent jobs, handle seeding or removal
		if j.Type == TypeTorrent {
			if j.SeedAfterComplete {
				if prevStatus != StatusSeeding {
					j.Status = StatusSeeding
					j.Progress = 100
					j.SpeedBytesPerSecond = status.UploadSpeed
					j.ETASeconds = 0
					j.UpdatedAt = time.Now()
					m.repo.Update(ctx, j)
					m.publish(EventJobUpdated, j)
					if m.scheduler != nil {
						m.scheduler.Kick()
					}
				}
				return
			}
			// If seedAfterComplete = false, remove torrent from qBittorrent
			if eng, ok := m.engines.Get(j.Engine); ok {
				if te, ok := eng.(ITorrentEngine); ok {
					if err := te.RemoveTorrent(ctx, j.EngineID, false); err != nil {
						log.Printf("UpdateJobFromEngine: failed to remove torrent %s from daemon: %v", j.EngineID, err)
						j.Status = StatusFailed
						j.Error = fmt.Sprintf("failed to remove completed torrent from daemon: %v", err)
						j.UpdatedAt = time.Now()
						m.repo.Update(ctx, j)
						m.removeActive(j.ID)
						m.publish(EventJobFailed, j)
						if m.scheduler != nil {
							m.scheduler.Kick()
						}
						return
					}
				}
			}
		}
		// Handle media finalization before marking StatusCompleted
		if j.Type == TypeMedia && m.storageService != nil && j.WorkDir != "" && j.FinalPath == "" {
			srcFile := status.OutputPath
			if srcFile == "" && status.FileName != "" {
				cand := filepath.Join(j.WorkDir, status.FileName)
				if _, err := os.Stat(cand); err == nil {
					srcFile = cand
				}
			}
			if srcFile == "" {
				entries, err := os.ReadDir(j.WorkDir)
				if err != nil {
					log.Printf("UpdateJobFromEngine: failed to read workdir %s for job %s: %v", j.WorkDir, j.ID, err)
					j.Status = StatusFailed
					j.Error = fmt.Sprintf("failed to read media work directory: %v", err)
					j.UpdatedAt = time.Now()
					if updateErr := m.repo.Update(ctx, j); updateErr != nil {
						log.Printf("UpdateJobFromEngine: failed to persist FAILED status for job %s: %v", j.ID, updateErr)
						return
					}
					m.removeActive(j.ID)
					m.publish(EventJobFailed, j)
					if m.scheduler != nil {
						m.scheduler.Kick()
					}
					return
				}

				var bestFile string
				var bestSize int64
				for _, entry := range entries {
					if entry.IsDir() || entry.Name() == storage.WorkDirMarkerFilename {
						continue
					}
					name := entry.Name()
					lowerName := strings.ToLower(name)

					if strings.HasSuffix(lowerName, ".part") ||
						strings.HasSuffix(lowerName, ".ytdl") ||
						strings.HasSuffix(lowerName, ".vtt") ||
						strings.HasSuffix(lowerName, ".srt") ||
						strings.HasSuffix(lowerName, ".jpg") ||
						strings.HasSuffix(lowerName, ".jpeg") ||
						strings.HasSuffix(lowerName, ".png") ||
						strings.HasSuffix(lowerName, ".webp") ||
						strings.HasSuffix(lowerName, ".json") {
						continue
					}

					info, err := entry.Info()
					if err != nil {
						continue
					}
					if info.Size() > bestSize {
						bestSize = info.Size()
						bestFile = filepath.Join(j.WorkDir, name)
					}
				}
				srcFile = bestFile
			}

			if srcFile == "" {
				log.Printf("UpdateJobFromEngine: media completed but final output file was not found for job %s", j.ID)
				j.Status = StatusFailed
				j.Error = "media completed but final output file was not found"
				j.UpdatedAt = time.Now()
				if updateErr := m.repo.Update(ctx, j); updateErr != nil {
					log.Printf("UpdateJobFromEngine: failed to persist FAILED status for job %s: %v", j.ID, updateErr)
					return
				}
				m.removeActive(j.ID)
				m.publish(EventJobFailed, j)
				if m.scheduler != nil {
					m.scheduler.Kick()
				}
				return
			}

			finalPath, err := m.storageService.FinalizeFile(ctx, srcFile, j.DestinationDir, storage.FilenameConflictPolicy(j.ConflictPolicy))
			if err != nil {
				log.Printf("UpdateJobFromEngine: media finalization failed for job %s: %v", j.ID, err)
				j.Status = StatusFailed
				j.Error = fmt.Sprintf("file finalization failed: %v", err)
				j.UpdatedAt = time.Now()
				if updateErr := m.repo.Update(ctx, j); updateErr != nil {
					log.Printf("UpdateJobFromEngine: failed to persist FAILED status for job %s: %v", j.ID, updateErr)
					return
				}
				m.removeActive(j.ID)
				m.publish(EventJobFailed, j)
				if m.scheduler != nil {
					m.scheduler.Kick()
				}
				return
			}
			j.FinalPath = finalPath
			j.Name = filepath.Base(finalPath)
		}

		if j.Type == TypeDownload && status.FileName != "" {
			j.FinalPath = filepath.Join(j.DestinationDir, status.FileName)
		} else if j.Type == TypeTorrent {
			j.FinalPath = j.DestinationDir
		}

		// Normal completion
		j.Status = StatusCompleted
		j.Progress = 100
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		if err := m.repo.Update(ctx, j); err != nil {
			log.Printf("UpdateJobFromEngine: failed to update job %s to COMPLETED: %v", j.ID, err)
			return
		}
		m.removeActive(j.ID)
		m.publish(EventJobCompleted, j)
		if j.Type == TypeMedia && m.storageService != nil && j.WorkDir != "" {
			if err := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); err != nil {
				log.Printf("UpdateJobFromEngine: cleanup workdir error for completed job %s: %v", j.ID, err)
			}
		}
		if m.scheduler != nil {
			m.scheduler.Kick()
		}
		return

	case StatusFailed:
		j.Status = StatusFailed
		j.Error = status.Error
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		if err := m.repo.Update(ctx, j); err != nil {
			log.Printf("UpdateJobFromEngine: failed to persist FAILED status for job %s: %v", j.ID, err)
			return
		}
		m.removeActive(j.ID)
		m.publish(EventJobFailed, j)
		if j.Type == TypeMedia && m.storageService != nil && j.WorkDir != "" {
			if err := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); err != nil {
				log.Printf("UpdateJobFromEngine: cleanup workdir error for failed job %s: %v", j.ID, err)
			}
		}
		if m.scheduler != nil {
			m.scheduler.Kick()
		}
		return

	case StatusCancelled:
		j.Status = StatusCancelled
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		if err := m.repo.Update(ctx, j); err != nil {
			log.Printf("UpdateJobFromEngine: failed to persist CANCELLED status for job %s: %v", j.ID, err)
			return
		}
		m.removeActive(j.ID)
		m.publish(EventJobCancelled, j)
		if j.Type == TypeMedia && m.storageService != nil && j.WorkDir != "" {
			if err := m.storageService.CleanupWorkDir(ctx, j.ID, j.WorkDir); err != nil {
				log.Printf("UpdateJobFromEngine: cleanup workdir error for cancelled job %s: %v", j.ID, err)
			}
		}
		if m.scheduler != nil {
			m.scheduler.Kick()
		}
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
			if m.scheduler != nil {
				m.scheduler.Kick()
			}
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
			if m.scheduler != nil {
				m.scheduler.Kick()
			}
		}
		return

	case StatusSeeding:
		if !j.SeedAfterComplete {
			// If seedAfterComplete = false, stop/remove torrent from qBittorrent and mark completed
			if eng, ok := m.engines.Get(j.Engine); ok {
				if te, ok := eng.(ITorrentEngine); ok {
					if err := te.RemoveTorrent(ctx, j.EngineID, false); err != nil {
						log.Printf("UpdateJobFromEngine: failed to remove seeding torrent %s from daemon: %v", j.EngineID, err)
						j.Status = StatusFailed
						j.Error = fmt.Sprintf("failed to remove completed torrent from daemon: %v", err)
						j.UpdatedAt = time.Now()
						m.repo.Update(ctx, j)
						m.removeActive(j.ID)
						m.publish(EventJobFailed, j)
						if m.scheduler != nil {
							m.scheduler.Kick()
						}
						return
					}
				}
			}
			j.Status = StatusCompleted
			j.Progress = 100
			j.SpeedBytesPerSecond = 0
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.removeActive(j.ID)
			m.publish(EventJobCompleted, j)
			if m.scheduler != nil {
				m.scheduler.Kick()
			}
			return
		}

		if prevStatus != StatusSeeding {
			j.Status = StatusSeeding
			j.Progress = 100
			j.SpeedBytesPerSecond = status.UploadSpeed
			j.ETASeconds = 0
			j.UpdatedAt = time.Now()
			m.repo.Update(ctx, j)
			m.publish(EventJobUpdated, j)
			if m.scheduler != nil {
				m.scheduler.Kick()
			}
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

// --- V0.5 Queue & Settings APIs ---

// GetQueueSnapshot returns the snapshot of all queued/paused jobs and runtime capacity stats.
func (m *Manager) GetQueueSnapshot(ctx context.Context) (*QueueSnapshot, error) {
	effectiveMax := 3
	if m.settings != nil {
		effectiveMax = m.settings.EffectiveMaxConcurrentDownloads(ctx)
	}

	runningCount, err := m.repo.CountDownloading(ctx)
	if err != nil {
		return nil, fmt.Errorf("count downloading: %w", err)
	}

	var queuedItems []QueuedJob
	if m.queueRepo != nil {
		var err error
		queuedItems, err = m.queueRepo.List(ctx)
		if err != nil {
			return nil, fmt.Errorf("list queue: %w", err)
		}
	}

	queuedCount := 0
	pausedCount := 0
	items := make([]QueuedJob, 0, len(queuedItems))

	for _, item := range queuedItems {
		j, err := m.repo.GetByID(ctx, item.JobID)
		if err != nil || j == nil {
			continue
		}
		item.Job = *j
		if j.Status == StatusQueued {
			queuedCount++
			item.WaitingReason = "waiting_for_slot"
			items = append(items, item)
		} else if j.Status == StatusPaused {
			pausedCount++
			item.WaitingReason = "paused_by_user"
			items = append(items, item)
		}
	}

	return &QueueSnapshot{
		MaxConcurrentDownloads: effectiveMax,
		RunningDownloads:       runningCount,
		QueuedDownloads:        queuedCount,
		PausedDownloads:        pausedCount,
		Items:                  items,
	}, nil
}

// ReorderQueue updates position ordering for all jobs within a priority lane.
func (m *Manager) ReorderQueue(ctx context.Context, priority JobPriority, jobIDs []string) error {
	if !ValidJobPriority(priority) {
		return &AppError{Code: ErrInvalidPriority, Message: fmt.Sprintf("invalid priority: %s", priority)}
	}

	if m.queueRepo == nil {
		return nil
	}

	// Fetch all current queue items in this priority lane for full-lane validation
	allQueueItems, err := m.queueRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("list queue for reorder validation: %w", err)
	}

	var expectedLaneIDs []string
	expectedSet := make(map[string]bool)
	for _, item := range allQueueItems {
		j, err := m.repo.GetByID(ctx, item.JobID)
		if err == nil && j != nil && j.Priority == priority && (j.Status == StatusQueued || j.Status == StatusPaused) {
			expectedLaneIDs = append(expectedLaneIDs, item.JobID)
			expectedSet[item.JobID] = true
		}
	}

	if len(jobIDs) != len(expectedLaneIDs) {
		return &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("reorder list length (%d) must match total jobs in priority lane (%d)", len(jobIDs), len(expectedLaneIDs))}
	}

	seen := make(map[string]bool)
	for _, id := range jobIDs {
		if seen[id] {
			return &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("duplicate job ID in reorder list: %s", id)}
		}
		seen[id] = true

		if !expectedSet[id] {
			return &AppError{Code: ErrInvalidRequest, Message: fmt.Sprintf("job %s does not belong to priority lane %s", id, priority)}
		}
	}

	if err := m.queueRepo.Reorder(ctx, priority, jobIDs); err != nil {
		return fmt.Errorf("reorder queue lane %s: %w", priority, err)
	}

	if m.scheduler != nil {
		m.scheduler.Kick()
	}
	return nil
}

// SetJobPriority changes a job's priority and moves its queue entry to the end of the new lane atomically.
func (m *Manager) SetJobPriority(ctx context.Context, id string, p JobPriority) (*Job, error) {
	if !ValidJobPriority(p) {
		return nil, &AppError{Code: ErrInvalidPriority, Message: fmt.Sprintf("invalid job priority: %s", p)}
	}

	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}

	oldPriority := j.Priority
	if oldPriority == p {
		return j, nil
	}

	// Read queue entry position if present
	var entry *QueueEntry
	if m.queueRepo != nil {
		var getErr error
		entry, getErr = m.queueRepo.Get(ctx, id)
		if getErr != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to read queue state: %v", getErr)}
		}
	}

	if entry != nil && m.queueRepo != nil {
		// Job has a queue entry - calculate next position in target lane and update atomically
		nextPos, posErr := m.queueRepo.NextPosition(ctx, p)
		if posErr != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to calculate next position: %v", posErr)}
		}

		if err := m.repo.UpdateJobPriorityAndQueuePosition(ctx, id, p, nextPos); err != nil {
			return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("atomic priority update failed: %v", err)}
		}
		j.Priority = p
		j.UpdatedAt = time.Now()
	} else {
		// Job without queue entry (running or terminal)
		j.Priority = p
		j.UpdatedAt = time.Now()
		if err := m.repo.Update(ctx, j); err != nil {
			j.Priority = oldPriority
			return nil, fmt.Errorf("update job priority: %w", err)
		}
	}

	m.publish(EventJobUpdated, j)
	if m.scheduler != nil {
		m.scheduler.Kick()
	}
	return j, nil
}

// BulkAction performs a lifecycle operation (pause, resume, cancel, retry) best-effort on up to 100 job IDs.
func (m *Manager) BulkAction(ctx context.Context, req BulkActionRequest) (*BulkActionResponse, error) {
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "pause" && action != "resume" && action != "cancel" && action != "retry" {
		return nil, &AppError{Code: ErrUnsupportedAction, Message: fmt.Sprintf("unsupported bulk action: %s", req.Action)}
	}
	if len(req.JobIDs) == 0 {
		return nil, &AppError{Code: ErrInvalidRequest, Message: "jobIds cannot be empty"}
	}
	if len(req.JobIDs) > 100 {
		return nil, &AppError{Code: ErrBatchLimitExceeded, Message: "bulk operation cannot exceed 100 job IDs"}
	}

	seen := make(map[string]bool)
	uniqueIDs := make([]string, 0, len(req.JobIDs))
	for _, id := range req.JobIDs {
		if !seen[id] && id != "" {
			seen[id] = true
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	resp := &BulkActionResponse{
		Action:  action,
		Results: make([]BulkItemResult, 0, len(uniqueIDs)),
	}

	for _, id := range uniqueIDs {
		var updatedJ *Job
		var err error

		switch action {
		case "pause":
			updatedJ, err = m.Pause(ctx, id)
		case "resume":
			updatedJ, err = m.Resume(ctx, id)
		case "cancel":
			updatedJ, err = m.Cancel(ctx, id)
		case "retry":
			updatedJ, err = m.Retry(ctx, id)
		}

		if err != nil {
			resp.Failed++
			appErr, ok := err.(*AppError)
			if !ok {
				appErr = &AppError{Code: ErrInternalError, Message: err.Error()}
			}
			resp.Results = append(resp.Results, BulkItemResult{
				JobID:   id,
				Success: false,
				Error:   appErr,
			})
		} else {
			resp.Succeeded++
			resp.Results = append(resp.Results, BulkItemResult{
				JobID:   id,
				Success: true,
				Job:     updatedJ,
			})
		}
	}

	return resp, nil
}

// GetScheduler returns the wired Scheduler instance.
func (m *Manager) GetScheduler() *Scheduler {
	return m.scheduler
}

func (m *Manager) persistDispatchFailure(ctx context.Context, j *Job, qj *QueuedJob, targetStatus JobStatus, dispatchErr error) error {
	j.Status = targetStatus
	j.Error = dispatchErr.Error()
	j.SpeedBytesPerSecond = 0
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()

	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("persistDispatchFailure: failed to persist %s for job %s: %v", targetStatus, j.ID, err)
		return &DispatchPersistenceError{
			JobID:  j.ID,
			Action: qj.Action,
			Err:    err,
		}
	}

	if qj.Action == QueueActionStart {
		if m.queueRepo != nil {
			if delErr := m.queueRepo.Delete(ctx, j.ID); delErr != nil {
				log.Printf("persistDispatchFailure: delete queue entry for %s failed: %v", j.ID, delErr)
			}
		}
		m.publish(EventJobFailed, j)
	} else {
		// QueueActionResume: retain QueueActionResume row, publish update
		m.publish(EventJobUpdated, j)
	}

	return dispatchErr
}

// DispatchQueuedJob dispatches a queued job to its target engine.
func (m *Manager) DispatchQueuedJob(ctx context.Context, qj *QueuedJob) error {
	return m.dispatchQueuedJob(ctx, qj)
}

func (m *Manager) dispatchQueuedJob(ctx context.Context, qj *QueuedJob) error {
	if qj == nil {
		return fmt.Errorf("queued job is nil")
	}

	j, err := m.repo.GetByID(ctx, qj.JobID)
	if err != nil || j == nil {
		if m.queueRepo != nil {
			m.queueRepo.Delete(ctx, qj.JobID)
		}
		return fmt.Errorf("job %s not found: %v", qj.JobID, err)
	}

	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		return fmt.Errorf("engine %q not available", j.Engine)
	}

	targetDir := j.DestinationDir
	if targetDir == "" {
		targetDir = m.downloadDir
	}

	if m.storageService != nil {
		if preflightErr := m.storageService.Preflight(ctx, targetDir, j.WorkDir, j.TotalBytes, j.CompletedBytes); preflightErr != nil {
			log.Printf("dispatchQueuedJob: storage preflight failed for job %s (action=%s): %v", j.ID, qj.Action, preflightErr)
			targetStatus := StatusPaused
			if qj.Action == QueueActionStart {
				targetStatus = StatusFailed
			}
			return m.persistDispatchFailure(ctx, j, qj, targetStatus, preflightErr)
		}

		if j.WorkDir != "" {
			if prepareErr := m.storageService.PrepareWorkDir(ctx, j.ID, j.WorkDir); prepareErr != nil {
				log.Printf("dispatchQueuedJob: prepare workdir failed for job %s (action=%s): %v", j.ID, qj.Action, prepareErr)
				targetStatus := StatusPaused
				if qj.Action == QueueActionStart {
					targetStatus = StatusFailed
				}
				return m.persistDispatchFailure(ctx, j, qj, targetStatus, prepareErr)
			}
		}
	}

	execDir := targetDir
	if j.Type == TypeMedia && j.WorkDir != "" {
		execDir = j.WorkDir
	}

	if qj.Action == QueueActionStart {
		if j.Type == TypeTorrent {
			torrentEng, ok := eng.(ITorrentEngine)
			if !ok {
				return fmt.Errorf("engine %q does not support torrent operations", j.Engine)
			}
			if err := torrentEng.StartDownload(ctx, j.EngineID); err != nil {
				return err
			}
		} else {
			engineID, err := eng.Start(ctx, j, execDir)
			if err != nil {
				return err
			}
			j.EngineID = engineID
		}
	} else {
		// QueueActionResume
		if err := eng.Resume(ctx, j); err != nil {
			return err
		}
	}

	j.Status = StatusDownloading
	j.Error = ""
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		log.Printf("dispatchQueuedJob: update job %s status to DOWNLOADING failed: %v", j.ID, err)
		return &DispatchPersistenceError{
			JobID:    j.ID,
			EngineID: j.EngineID,
			Action:   qj.Action,
			Err:      err,
		}
	}

	if m.queueRepo != nil {
		if delErr := m.queueRepo.Delete(ctx, j.ID); delErr != nil {
			log.Printf("dispatchQueuedJob: delete queue entry for %s failed: %v", j.ID, delErr)
		}
	}

	m.addActive(j)
	m.publish(EventJobUpdated, j)
	return nil
}

func (m *Manager) cleanupQueueOnStartup(ctx context.Context) {
	if m.queueRepo == nil {
		return
	}
	items, err := m.queueRepo.List(ctx)
	if err != nil {
		log.Printf("cleanupQueueOnStartup: failed to list queue entries: %v", err)
		return
	}
	for _, item := range items {
		j, err := m.repo.GetByID(ctx, item.JobID)
		if err != nil || j == nil {
			m.queueRepo.Delete(ctx, item.JobID)
			continue
		}
		if j.Status == StatusCompleted || j.Status == StatusFailed || j.Status == StatusCancelled || j.Status == StatusDownloading || j.Status == StatusProcessing || j.Status == StatusSeeding {
			log.Printf("cleanupQueueOnStartup: removing stale queue entry for job %s (status=%s)", j.ID, j.Status)
			m.queueRepo.Delete(ctx, j.ID)
		}
	}
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

func (m *Manager) enqueueJob(ctx context.Context, j *Job, action QueueAction) error {
	if m.queueRepo == nil {
		return nil
	}
	nextPos, err := m.queueRepo.NextPosition(ctx, j.Priority)
	if err != nil {
		return fmt.Errorf("calculate next queue position: %w", err)
	}
	now := time.Now()
	err = m.queueRepo.Enqueue(ctx, &QueueEntry{
		JobID:      j.ID,
		Position:   nextPos,
		Action:     action,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})
	if err != nil {
		return fmt.Errorf("enqueue queue entry: %w", err)
	}
	return nil
}
