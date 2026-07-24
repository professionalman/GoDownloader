package job

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"path"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Manager is the central orchestrator for job lifecycle management.
// All state transitions go through the Manager.
type Manager struct {
	repo        JobRepository
	engines     EngineRegistry
	bus         EventBus
	downloadDir string

	mu         sync.RWMutex
	activeJobs map[string]*Job // id -> job (in-memory cache for active jobs)

	monitor *Monitor
}

// NewManager creates a new job manager.
func NewManager(repo JobRepository, engines EngineRegistry, bus EventBus, downloadDir string) *Manager {
	m := &Manager{
		repo:        repo,
		engines:     engines,
		bus:         bus,
		downloadDir: downloadDir,
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

	analyzer, ok := eng.(MediaAnalyzer)
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
func (m *Manager) GetEngine(name string) (Engine, bool) {
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
