package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"downloader/internal/networkpolicy"
	"downloader/internal/securestore"
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
	trackerEntries ITrackerEntryProvider
	scheduler      *Scheduler

	metadataTimeoutSeconds int

	mu              sync.RWMutex
	activeJobs      map[string]*Job // id -> job (in-memory cache for active jobs)
	activeCancels   map[string]context.CancelFunc
	appliedLimits   map[string]int64
	terminalLocks   map[string]*sync.Mutex
	reconcileCancel context.CancelFunc

	monitor *Monitor
}

// NewManager creates a new job manager.
func NewManager(repo IJobRepository, engines IEngineRegistry, bus IEventBus, downloadDir string, torrentRepo ITorrentRepository, dataDir ...string) *Manager {
	dDir := "./data"
	if len(dataDir) > 0 && dataDir[0] != "" {
		dDir = dataDir[0]
	}
	m := &Manager{
		repo:                   repo,
		engines:                engines,
		bus:                    bus,
		downloadDir:            downloadDir,
		dataDir:                dDir,
		torrentRepo:            torrentRepo,
		metadataTimeoutSeconds: 300,
		activeJobs:             make(map[string]*Job),
		activeCancels:          make(map[string]context.CancelFunc),
		appliedLimits:          make(map[string]int64),
		terminalLocks:          make(map[string]*sync.Mutex),
	}
	return m
}

func (m *Manager) SetMetadataTimeoutSeconds(sec int) {
	if sec < 1 {
		sec = 300
	}
	if sec > 3600 {
		sec = 3600
	}
	m.mu.Lock()
	m.metadataTimeoutSeconds = sec
	m.mu.Unlock()
}

func (m *Manager) getMetadataTimeoutSeconds() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.metadataTimeoutSeconds <= 0 {
		return 300
	}
	return m.metadataTimeoutSeconds
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

type ITrackerEntryProvider interface {
	EnabledEntries(ctx context.Context) ([]string, error)
}

func (m *Manager) SetTrackerEntryProvider(provider ITrackerEntryProvider) {
	m.trackerEntries = provider
}

func (m *Manager) prepareJobForActivation(ctx context.Context, j *Job) error {
	return m.hydrateTorrentState(ctx, j)
}

// SetScheduler wires the scheduler instance.
func (m *Manager) SetScheduler(s *Scheduler) {
	m.scheduler = s
	if s != nil {
		s.SetEventBus(m.bus)
		s.SetEngineRegistry(m.engines)
		s.SetAddActiveFunc(m.addActive)
		s.SetPrepareActiveJobFunc(m.prepareJobForActivation)
	}
}

// StartBackgroundTasks starts recovery, queue cleanup, scheduler, and progress monitor.
// Call this after creating the Manager.
func (m *Manager) StartBackgroundTasks(ctx context.Context) {
	// 1. Run recovery first
	m.recover(ctx)
	m.ReconcileNetworkPolicies(ctx)
	m.processPendingEngineCleanups(ctx)
	reconcileCtx, cancel := context.WithCancel(ctx)
	m.reconcileCancel = cancel
	go m.runNetworkReconciler(reconcileCtx)

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
	if m.reconcileCancel != nil {
		m.reconcileCancel()
	}
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
	NetworkPolicy  *networkpolicy.JobNetworkPolicyOverride
	SeedingPolicy  *networkpolicy.SeedingPolicy
	Trackers       []string
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

		if err := m.attachCreationPolicy(ctx, j, opts); err != nil {
			return nil, err
		}
		if err := m.repo.Create(ctx, j); err != nil {
			return nil, fmt.Errorf("persist job: %w", err)
		}

		m.publish(EventJobCreated, j)
		go m.analyzeMedia(ctx, j.ID, source)
		return j, nil
	}

	// Standard download flow (aria2)
	j.Type = TypeDownload
	if err := m.attachCreationPolicy(ctx, j, opts); err != nil {
		return nil, err
	}

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

		if prepErr := m.prepareNetworkDispatch(ctx, j, eng); prepErr != nil {
			return nil, prepErr
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
		if limitErr := m.applyJobLimits(ctx, j, eng, false); limitErr != nil {
			j.NetworkReconcilePending = true
		}
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
			NetworkPolicy:  firstNonNilNetwork(input.NetworkPolicy, req.NetworkPolicy),
			SeedingPolicy:  firstNonNilSeeding(input.SeedingPolicy, req.SeedingPolicy),
			Trackers:       firstNonEmptyTrackers(input.Trackers, req.Trackers),
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

func firstNonNilNetwork(a, b *networkpolicy.JobNetworkPolicyOverride) *networkpolicy.JobNetworkPolicyOverride {
	if a != nil {
		return a
	}
	return b
}

func firstNonNilSeeding(a, b *networkpolicy.SeedingPolicy) *networkpolicy.SeedingPolicy {
	if a != nil {
		return a
	}
	return b
}

func firstNonEmptyTrackers(a, b []string) []string {
	if a != nil {
		return a
	}
	return b
}

func (m *Manager) attachCreationPolicy(ctx context.Context, j *Job, opts CreateOptions) error {
	if m.settings == nil {
		j.NetworkPolicy = networkpolicy.JobNetworkPolicy{
			Proxy:       networkpolicy.ProxyPolicy{Mode: networkpolicy.ProxyModeDisabled},
			RetryPolicy: networkpolicy.RetryPolicy{}, TimeoutPolicy: networkpolicy.TimeoutPolicy{},
		}
		if j.Type == TypeDownload {
			j.NetworkPolicy.DirectConnections = &networkpolicy.DirectConnectionPolicy{
				Split: 5, MaxConnectionsPerServer: 1, MinSplitSizeBytes: 20 << 20,
			}
		}
	} else {
		policy, runtime, err := m.settings.ResolveJobPolicy(ctx, j.ID, j.Type, opts.NetworkPolicy)
		if err != nil {
			code := ErrInvalidNetworkPolicy
			if errors.Is(err, securestore.ErrUnavailable) {
				code = ErrSecretStorageUnavailable
			}
			return &AppError{Code: code, Message: err.Error()}
		}
		j.NetworkPolicy = policy
		j.SetRuntimeNetworkPolicy(runtime)
	}
	if j.Type == TypeTorrent {
		seeding := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
		if m.settings != nil {
			if st, err := m.settings.GetSettings(ctx); err == nil {
				seeding = st.Torrent.SeedingPolicy
			}
		}
		if opts.SeedingPolicy != nil {
			seeding = *opts.SeedingPolicy
		}
		if err := networkpolicy.ValidateSeeding(seeding); err != nil {
			return &AppError{Code: ErrInvalidSeedingPolicy, Message: err.Error()}
		}
		trackers, err := networkpolicy.ValidateTrackerURLs(opts.Trackers, 256)
		if err != nil {
			return &AppError{Code: ErrInvalidTrackerURL, Message: err.Error()}
		}
		if m.trackerEntries != nil && m.settings != nil {
			if st, settingsErr := m.settings.GetSettings(ctx); settingsErr == nil && st.Torrent.ApplyTrackerSubscriptionsToNewTorrents {
				if subscribed, entryErr := m.trackerEntries.EnabledEntries(ctx); entryErr == nil {
					trackers, err = networkpolicy.ValidateTrackerURLs(append(trackers, subscribed...), 10000)
					if err != nil {
						return &AppError{Code: ErrInvalidTrackerURL, Message: err.Error()}
					}
				}
			}
		}
		j.SeedingPolicy = seeding
		j.SeedAfterComplete = seeding.Mode != networkpolicy.SeedingModeNone
		j.CustomTrackers = trackers
	}
	return nil
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

	var info *MediaInfo
	if policyAnalyzer, policyOK := eng.(INetworkMediaAnalyzer); policyOK {
		runtime, policyErr := m.runtimePolicyForJob(ctx, j)
		if policyErr != nil {
			err = policyErr
		} else {
			info, err = policyAnalyzer.AnalyzeWithPolicy(ctx, source, runtime)
		}
	} else {
		info, err = analyzer.Analyze(ctx, source)
	}
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

		if prepErr := m.prepareNetworkDispatch(ctx, j, eng); prepErr != nil {
			return nil, prepErr
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
		if limitErr := m.applyJobLimits(ctx, j, eng, false); limitErr != nil {
			j.NetworkReconcilePending = true
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
	if err := m.attachCreationPolicy(ctx, j, opts); err != nil {
		return nil, err
	}

	if err := m.repo.Create(ctx, j); err != nil {
		return nil, fmt.Errorf("persist job: %w", err)
	}
	if m.torrentRepo != nil {
		if err := m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
			JobID: jobID, TorrentFilePath: torrentFilePath,
			SeedAfterComplete: j.SeedAfterComplete, SeedingPolicy: j.SeedingPolicy,
			CustomTrackers: j.CustomTrackers,
		}); err != nil {
			return nil, fmt.Errorf("persist torrent policy: %w", err)
		}
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

func sanitizeTrackerURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	for param := range q {
		lower := strings.ToLower(param)
		if strings.Contains(lower, "token") || strings.Contains(lower, "passkey") ||
			strings.Contains(lower, "key") || strings.Contains(lower, "auth") ||
			strings.Contains(lower, "secret") {
			q.Set(param, "[REDACTED]")
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
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
			errText := fmt.Sprintf("failed to verify torrent ownership: %v", err)
			rec, _ := m.torrentRepo.GetTorrentJob(ctx, jobID)
			rec = cloneTorrentRecord(rec)
			if rec == nil {
				rec = &TorrentJobRecord{JobID: jobID, SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}}
			}
			rec.InfoHash = infoHash
			rec.TorrentFilePath = torrentFilePath
			if createErr := m.torrentRepo.UpdateTorrentJob(ctx, rec); createErr != nil {
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
			errTxt := fmt.Sprintf("a torrent with info hash %s is already managed by job %s", infoHash, rec.JobID)
			rec, _ := m.torrentRepo.GetTorrentJob(ctx, jobID)
			rec = cloneTorrentRecord(rec)
			if rec == nil {
				rec = &TorrentJobRecord{JobID: jobID, SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}}
			}
			rec.InfoHash = infoHash
			rec.TorrentFilePath = torrentFilePath
			if createErr := m.torrentRepo.UpdateTorrentJob(ctx, rec); createErr != nil {
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

	timeoutSecs := m.getMetadataTimeoutSeconds()
	timeoutCh := time.After(time.Duration(timeoutSecs) * time.Second)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var metadata *TorrentInfo
	var lastState string
	var seeders int
	var leechers int
	var wasPaused bool
	var errorState string
	var disappeared bool
	var trackerError string

	// Perform initial state check immediately before ticker
	if rawProvider, ok := torrentEng.(ITorrentRawStateProvider); ok {
		if rawSt, errRaw := rawProvider.GetRawState(ctx, infoHash); errRaw == nil && rawSt != "" {
			lastState = rawSt
			if rawSt == "pausedDL" || rawSt == "stoppedDL" || rawSt == "pausedUP" || rawSt == "stoppedUP" {
				wasPaused = true
			}
			if rawSt == "error" || rawSt == "missingFiles" {
				errorState = rawSt
			}
		}
	}
	if lastState == "" {
		if status, errStatus := torrentEng.Status(ctx, j); errStatus == nil && status != nil {
			lastState = status.RawState
			if lastState == "pausedDL" || lastState == "stoppedDL" || status.Status == StatusPaused {
				wasPaused = true
			}
		}
	}

loop:
	for {
		select {
		case <-ctx.Done():
			log.Printf("acquireTorrentMetadata: job %s background task cancelled", jobID)
			if infoHash != "" {
				_ = torrentEng.RemoveTorrent(context.Background(), infoHash, false)
			}
			return
		case <-timeoutCh:
			break loop
		case <-ticker.C:
			current, err := m.repo.GetByID(ctx, jobID)
			if err != nil || current == nil || current.Status == StatusCancelled {
				log.Printf("acquireTorrentMetadata: job %s cancelled in DB", jobID)
				if infoHash != "" {
					_ = torrentEng.RemoveTorrent(context.Background(), infoHash, false)
				}
				return
			}

			// Preserve raw state directly across engine boundary
			var rawSt string
			if rawProvider, ok := torrentEng.(ITorrentRawStateProvider); ok {
				rawSt, _ = rawProvider.GetRawState(ctx, infoHash)
			}
			if rawSt == "" {
				if status, errStatus := torrentEng.Status(ctx, j); errStatus == nil && status != nil {
					rawSt = status.RawState
				}
			}

			if rawSt != "" {
				lastState = rawSt
				if rawSt == "error" || rawSt == "missingFiles" {
					errorState = rawSt
					break loop
				}
				if rawSt == "pausedDL" || rawSt == "stoppedDL" || rawSt == "pausedUP" || rawSt == "stoppedUP" {
					wasPaused = true
				}
			}

			// Inspect tracker error diagnostics
			if trackerCtrl, ok := torrentEng.(ITrackerController); ok {
				if trackers, errTr := trackerCtrl.GetTrackers(ctx, j); errTr == nil {
					for _, tr := range trackers {
						u := tr.URL
						if strings.Contains(u, "[DHT]") || strings.Contains(u, "[PeX]") || strings.Contains(u, "[LSD]") {
							continue
						}
						if tr.Status == 4 || (tr.Msg != "" && !strings.EqualFold(tr.Msg, "Tracker was not contacted") && !strings.EqualFold(tr.Msg, "Updating...")) {
							cleanURL := sanitizeTrackerURL(u)
							msg := tr.Msg
							if msg == "" {
								msg = "tracker reported an error"
							}
							trackerError = fmt.Sprintf("%s (%s)", cleanURL, msg)
						}
					}
				}
			}

			info, err := torrentEng.GetTorrentInfo(ctx, infoHash)
			if err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "not found") {
					disappeared = true
					break loop
				}
				continue
			}

			if info != nil {
				seeders = info.Seeders
				leechers = info.Leechers

				if info.Name != "" && info.Name != infoHash {
					files, errFiles := torrentEng.GetFiles(ctx, infoHash)
					if errFiles == nil && len(files) > 0 {
						stopErr := torrentEng.StopDownload(ctx, infoHash)

						// Verify torrent is actually stopped/paused
						var isStopped bool
						if rawProvider, ok := torrentEng.(ITorrentRawStateProvider); ok {
							currentRaw, errRaw := rawProvider.GetRawState(ctx, infoHash)
							if errRaw == nil && (currentRaw == "pausedDL" || currentRaw == "stoppedDL" ||
								currentRaw == "pausedUP" || currentRaw == "stoppedUP" ||
								currentRaw == "checkingUP" || currentRaw == "queuedUP") {
								isStopped = true
							}
						}
						if !isStopped && stopErr == nil {
							if st, errSt := torrentEng.Status(ctx, j); errSt == nil {
								if st.Status == StatusPaused || st.RawState == "pausedDL" || st.RawState == "stoppedDL" {
									isStopped = true
								}
							}
						}
						if stopErr == nil || isStopped {
							isStopped = true
						}

						if !isStopped {
							log.Printf("acquireTorrentMetadata: failed to stop torrent %s after metadata acquisition: %v", infoHash, stopErr)
							if m.torrentRepo != nil && infoHash != "" {
								rec, _ := m.torrentRepo.GetTorrentJob(ctx, jobID)
								rec = cloneTorrentRecord(rec)
								if rec == nil {
									rec = &TorrentJobRecord{JobID: jobID, SeedingPolicy: j.SeedingPolicy, CustomTrackers: j.CustomTrackers}
								}
								rec.InfoHash = infoHash
								rec.TorrentFilePath = torrentFilePath
								_ = m.torrentRepo.UpdateTorrentJob(ctx, rec)
							}

							j.EngineID = infoHash
							j.Status = StatusFailed
							j.Error = fmt.Sprintf("failed to stop torrent after metadata acquisition: %v", stopErr)
							j.UpdatedAt = time.Now()
							m.repo.Update(ctx, j)
							m.publish(EventJobFailed, j)
							return
						}

						metadata = info
						break loop
					}
				}
			}
		}
	}

	if metadata == nil {
		var normErr string
		if disappeared {
			normErr = "torrent disappeared from qBittorrent"
		} else if errorState != "" {
			normErr = fmt.Sprintf("qBittorrent returned error state: %s", errorState)
		} else if wasPaused || lastState == "pausedDL" || lastState == "stoppedDL" {
			normErr = "metadata timed out while torrent was paused"
		} else if trackerError != "" {
			normErr = fmt.Sprintf("tracker reported an error: %s", trackerError)
		} else {
			normErr = fmt.Sprintf("metadata timed out with zero connected peers (%d seeds, %d leechers)", seeders, leechers)
		}

		log.Printf("acquireTorrentMetadata: job %s metadata acquisition failed (infoHash=%s): %s", jobID, infoHash, normErr)

		// Persist retry info before removing torrent from qBittorrent
		if m.torrentRepo != nil && infoHash != "" {
			rec, _ := m.torrentRepo.GetTorrentJob(ctx, jobID)
			rec = cloneTorrentRecord(rec)
			if rec == nil {
				rec = &TorrentJobRecord{JobID: jobID, SeedingPolicy: j.SeedingPolicy, CustomTrackers: j.CustomTrackers}
			}
			rec.InfoHash = infoHash
			rec.TorrentFilePath = torrentFilePath
			_ = m.torrentRepo.UpdateTorrentJob(ctx, rec)
		}

		j.EngineID = infoHash
		j.Status = StatusFailed
		j.Error = normErr
		j.UpdatedAt = time.Now()
		m.repo.Update(ctx, j)
		m.publish(EventJobFailed, j)

		if infoHash != "" {
			_ = torrentEng.RemoveTorrent(ctx, infoHash, false)
		}
		return
	}

	// Verify job state before updating
	current, err := m.repo.GetByID(ctx, jobID)
	if err != nil || current == nil || current.Status == StatusCancelled {
		log.Printf("acquireTorrentMetadata: job %s was cancelled before completion", jobID)
		if infoHash != "" {
			_ = torrentEng.RemoveTorrent(context.Background(), infoHash, false)
		}
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
		rec, _ := m.torrentRepo.GetTorrentJob(ctx, jobID)
		rec = cloneTorrentRecord(rec)
		if rec == nil {
			rec = &TorrentJobRecord{JobID: jobID, SeedingPolicy: j.SeedingPolicy, CustomTrackers: j.CustomTrackers}
		}
		rec.InfoHash = infoHash
		rec.Name = metadata.Name
		rec.TotalSize = metadata.TotalSize
		rec.TorrentFilePath = torrentFilePath
		_ = m.torrentRepo.UpdateTorrentJob(ctx, rec)

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
	if len(j.CustomTrackers) > 0 {
		if _, trackerErr := m.applyTorrentTrackerSnapshot(ctx, j); trackerErr != nil {
			log.Printf("acquireTorrentMetadata: custom trackers not applied for job %s: %v", jobID, trackerErr)
		}
	}

	m.publish(EventJobUpdated, j)
	m.publish(EventJobUpdated, j)
	log.Printf("acquireTorrentMetadata: metadata acquired for job %s (infoHash=%s): %s", jobID, infoHash, metadata.Name)
}

// StartTorrent starts a torrent download after file selection.
func (m *Manager) StartTorrent(ctx context.Context, id string, selections []TorrentFileSelection, seedAfterComplete bool) (*Job, error) {
	mode := networkpolicy.SeedingModeNone
	if seedAfterComplete {
		mode = networkpolicy.SeedingModeUnlimited
	}
	policy := networkpolicy.SeedingPolicy{Mode: mode}
	return m.StartTorrentWithPolicy(ctx, id, selections, policy)
}

// StartTorrentWithPolicy starts a selected torrent using a normalized seeding policy.
func (m *Manager) StartTorrentWithPolicy(ctx context.Context, id string, selections []TorrentFileSelection, policy networkpolicy.SeedingPolicy) (*Job, error) {
	if err := networkpolicy.ValidateSeeding(policy); err != nil {
		return nil, &AppError{Code: ErrInvalidSeedingPolicy, Message: err.Error()}
	}
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
	if len(selections) != len(existingFiles) {
		return nil, &AppError{Code: ErrInvalidRequest, Message: "torrent selection must include every file exactly once"}
	}

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

	if len(seenIndex) != len(existingFiles) {
		return nil, &AppError{Code: ErrInvalidRequest, Message: "torrent selection must include every file exactly once"}
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
	j.SeedingPolicy = policy
	j.SeedAfterComplete = policy.Mode != networkpolicy.SeedingModeNone

	if controller, ok := eng.(ISeedingPolicyController); ok {
		if err := controller.ApplySeedingPolicy(ctx, j, policy); err != nil {
			return nil, &AppError{Code: ErrNetworkSettingApplicationFailed, Message: fmt.Sprintf("failed to apply seeding policy: %v", err)}
		}
	}

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
			rec = cloneTorrentRecord(rec)
			rec.SeedAfterComplete = j.SeedAfterComplete
			rec.SeedingPolicy = policy
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
		if prepErr := m.prepareNetworkDispatch(ctx, j, eng); prepErr != nil {
			return nil, prepErr
		}
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
		if limitErr := m.applyJobLimits(ctx, j, eng, false); limitErr != nil {
			j.NetworkReconcilePending = true
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

	stopped, err := m.stopSeedingWithReason(ctx, j, "manual")
	if err != nil {
		var finErr *TorrentFinalizeError
		if errors.As(err, &finErr) && finErr.Kind == TorrentFinalizeCleanupFailure {
			return j, &AppError{Code: ErrEngineError, Message: fmt.Sprintf("torrent completed but qBittorrent cleanup is pending: %v", err)}
		}
		if !stopped {
			return nil, &AppError{Code: ErrEngineError, Message: "failed to stop torrent seeding"}
		}
		return nil, &AppError{Code: ErrInternalError, Message: fmt.Sprintf("failed to persist completed status: %v", err)}
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
			if prepErr := m.prepareNetworkDispatch(ctx, j, eng); prepErr != nil {
				return nil, prepErr
			}
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
		if limitErr := m.applyJobLimits(ctx, j, eng, false); limitErr != nil {
			j.NetworkReconcilePending = true
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
func (m *Manager) cleanupTerminalEngineState(j *Job) {
	if j == nil || j.Engine == "" {
		return
	}
	if eng, ok := m.engines.Get(j.Engine); ok && eng != nil {
		if cleanupEng, ok := eng.(ICleanupableEngine); ok {
			cleanupEng.Cleanup(j.ID)
		}
	}
}

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
	m.cleanupTerminalEngineState(j)

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

		if prepErr := m.prepareNetworkDispatch(ctx, j, eng); prepErr != nil {
			return nil, prepErr
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
		if limitErr := m.applyJobLimits(ctx, j, eng, false); limitErr != nil {
			j.NetworkReconcilePending = true
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

func (m *Manager) hydrateTorrentState(ctx context.Context, j *Job) error {
	if j == nil || j.Type != TypeTorrent || m.torrentRepo == nil {
		return nil
	}
	rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
	if err != nil {
		return fmt.Errorf("failed to fetch torrent record for job %s: %w", j.ID, err)
	}
	if rec == nil {
		return fmt.Errorf("required torrent record missing for job %s", j.ID)
	}
	clonedRec := cloneTorrentRecord(rec)
	synchronizeJobSeedingState(j, clonedRec)
	j.CustomTrackers = append([]string(nil), clonedRec.CustomTrackers...)
	if j.TorrentInfo == nil && (clonedRec.Name != "" || clonedRec.TotalSize > 0) {
		j.TorrentInfo = &TorrentInfo{
			Name:      clonedRec.Name,
			InfoHash:  clonedRec.InfoHash,
			TotalSize: clonedRec.TotalSize,
		}
	}
	return nil
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
	_ = m.hydrateTorrentState(ctx, j)
}

// GetActiveJobs returns a snapshot of all active jobs.
func (m *Manager) GetActiveJobs() map[string]*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make(map[string]*Job, len(m.activeJobs))
	for k, v := range m.activeJobs {
		jobCopy := cloneJobSeedingState(v)
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
			if m.enterSeeding(ctx, j, status) {
				return
			}
			if prevStatus != StatusSeeding {
				_ = m.repo.Update(ctx, j)
				m.addActive(j)
				m.publish(EventJobUpdated, j)
				if m.scheduler != nil {
					m.scheduler.Kick()
				}
			}
			return
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
					m.cleanupTerminalEngineState(j)
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
				m.cleanupTerminalEngineState(j)
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
				m.cleanupTerminalEngineState(j)
				if m.scheduler != nil {
					m.scheduler.Kick()
				}
				return
			}
			j.FinalPath = finalPath
			j.Name = filepath.Base(finalPath)
			m.updateActiveJobFinalization(j.ID, finalPath, j.Name)
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
		m.cleanupTerminalEngineState(j)

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
		m.cleanupTerminalEngineState(j)

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
		m.cleanupTerminalEngineState(j)

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
		if m.enterSeeding(ctx, j, status) {
			return
		}

		if prevStatus != StatusSeeding {
			m.repo.Update(ctx, j)
			m.addActive(j)
			m.publish(EventJobUpdated, j)
			if m.scheduler != nil {
				m.scheduler.Kick()
			}
		} else {
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
			JobID:        j.ID,
			Action:       qj.Action,
			Kind:         DispatchFailureStatePersistence,
			TargetStatus: targetStatus,
			TargetError:  dispatchErr.Error(),
			Err:          err,
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

	return &DispatchHandledError{Err: dispatchErr}
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

	if err := m.hydrateTorrentState(ctx, j); err != nil {
		log.Printf("dispatchQueuedJob: failed to hydrate torrent state for job %s: %v", j.ID, err)
		return err
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
		if err := m.prepareNetworkDispatch(ctx, j, eng); err != nil {
			return err
		}
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
		if err := m.applyJobLimits(ctx, j, eng, false); err != nil {
			j.NetworkReconcilePending = true
			return err
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
			Kind:     DispatchFailureExternalExecutionPersistence,
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

func (m *Manager) updateActiveJobFinalization(jobID, finalPath, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active, ok := m.activeJobs[jobID]; ok && active != nil {
		active.FinalPath = finalPath
		active.Name = name
	}
}

func (m *Manager) updateActiveJobSeedingState(jobID string, record *TorrentJobRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if active := m.activeJobs[jobID]; active != nil {
		synchronizeJobSeedingState(active, record)
	}
}

func (m *Manager) removeCompletedTorrent(ctx context.Context, j *Job) error {
	eng, ok := m.engines.Get(j.Engine)
	if !ok {
		return fmt.Errorf("engine %q not available", j.Engine)
	}

	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		return fmt.Errorf("engine %q does not support torrent operations", j.Engine)
	}

	err := torrentEng.RemoveTorrent(ctx, j.EngineID, false)
	if err != nil {
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "404") || strings.Contains(errStr, "already") {
			return nil
		}
		return err
	}
	return nil
}

func (m *Manager) finalizeCompletedTorrent(ctx context.Context, j *Job) error {
	return m.finalizeCompletedTorrentWithReason(ctx, j, "policy_none")
}

func (m *Manager) finalizeCompletedTorrentWithReason(ctx context.Context, j *Job, stopReason string) error {
	lock := m.terminalLock(j.ID)
	lock.Lock()
	defer lock.Unlock()
	return m.finalizeCompletedTorrentWithReasonLocked(ctx, j, stopReason)
}

func (m *Manager) finalizeCompletedTorrentWithReasonLocked(ctx context.Context, j *Job, stopReason string) error {
	if current, err := m.repo.GetByID(ctx, j.ID); err == nil && current != nil && current.Status == StatusCompleted && !current.EngineCleanupPending {
		*j = *current
		return nil
	}
	candidate := *j
	candidate.FinalPath = candidate.DestinationDir
	candidate.Status = StatusCompleted
	candidate.Progress = 100
	candidate.SpeedBytesPerSecond = 0
	candidate.ETASeconds = 0
	candidate.EngineCleanupPending = true
	candidate.UpdatedAt = time.Now()

	var persistErr error
	if m.torrentRepo != nil {
		persistErr = m.torrentRepo.FinalizeTorrent(ctx, &candidate, stopReason)
	} else {
		persistErr = m.repo.Update(ctx, &candidate)
	}
	if persistErr != nil {
		log.Printf("finalizeCompletedTorrent: failed to update job %s to COMPLETED: %v", j.ID, persistErr)
		return &TorrentFinalizeError{
			Kind: TorrentFinalizePersistenceFailure,
			Err:  fmt.Errorf("persist completed status: %w", persistErr),
		}
	}

	*j = candidate
	j.SeedingStopReason = stopReason
	m.removeActive(j.ID)
	if m.scheduler != nil {
		m.scheduler.Kick()
	}

	err := m.removeCompletedTorrent(ctx, j)
	if err != nil {
		log.Printf("finalizeCompletedTorrent: qBittorrent removal pending for job %s: %v", j.ID, err)
		return &TorrentFinalizeError{
			Kind: TorrentFinalizeCleanupFailure,
			Err:  fmt.Errorf("torrent completed but daemon cleanup is pending: %w", err),
		}
	}

	candidate.EngineCleanupPending = false
	candidate.UpdatedAt = time.Now()
	if updateErr := m.repo.Update(ctx, &candidate); updateErr != nil {
		log.Printf("finalizeCompletedTorrent: failed to clear engine_cleanup_pending for job %s: %v", j.ID, updateErr)
		return &TorrentFinalizeError{
			Kind: TorrentFinalizePersistenceFailure,
			Err:  fmt.Errorf("failed to clear engine_cleanup_pending: %w", updateErr),
		}
	}

	*j = candidate
	m.publish(EventJobCompleted, j)
	return nil
}

func (m *Manager) stopSeedingWithReason(ctx context.Context, j *Job, stopReason string) (bool, error) {
	lock := m.terminalLock(j.ID)
	lock.Lock()
	defer lock.Unlock()

	current, err := m.repo.GetByID(ctx, j.ID)
	if err != nil {
		return false, err
	}
	if current == nil {
		return false, &AppError{Code: ErrJobNotFound, Message: "job not found"}
	}
	if current.Status != StatusSeeding {
		return false, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot stop seeding a %s job", current.Status)}
	}
	current.SeedingPolicy = cloneSeedingPolicy(j.SeedingPolicy)
	current.SeedAfterComplete = j.SeedAfterComplete
	current.SeedingStartedAt = cloneTimePointer(j.SeedingStartedAt)
	current.SeedingStopReason = j.SeedingStopReason
	eng, ok := m.engines.Get(current.Engine)
	if !ok {
		return false, fmt.Errorf("engine not available")
	}
	torrentEng, ok := eng.(ITorrentEngine)
	if !ok {
		return false, fmt.Errorf("engine does not support torrent operations")
	}
	if err := torrentEng.StopDownload(ctx, current.EngineID); err != nil {
		return false, err
	}

	candidate := *current
	candidate.SeedAfterComplete = false
	candidate.SeedingPolicy = networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	if err := m.finalizeCompletedTorrentWithReasonLocked(ctx, &candidate, stopReason); err != nil {
		if candidate.Status == StatusCompleted {
			*j = candidate
		}
		return true, err
	}
	*j = candidate
	return true, nil
}

func (m *Manager) terminalLock(jobID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock := m.terminalLocks[jobID]
	if lock == nil {
		lock = &sync.Mutex{}
		m.terminalLocks[jobID] = lock
	}
	return lock
}

func (m *Manager) retryPendingEngineCleanup(ctx context.Context, j *Job) error {
	if !j.EngineCleanupPending || j.Status != StatusCompleted {
		return nil
	}

	err := m.removeCompletedTorrent(ctx, j)
	if err != nil {
		log.Printf("retryPendingEngineCleanup: cleanup failed for job %s: %v", j.ID, err)
		return err
	}

	candidate := *j
	candidate.EngineCleanupPending = false
	candidate.UpdatedAt = time.Now()
	if updateErr := m.repo.Update(ctx, &candidate); updateErr != nil {
		log.Printf("retryPendingEngineCleanup: failed to clear engine_cleanup_pending for job %s: %v", j.ID, updateErr)
		return updateErr
	}

	*j = candidate
	m.removeActive(j.ID)
	if m.scheduler != nil {
		m.scheduler.Kick()
	}
	return nil
}

func (m *Manager) processPendingEngineCleanups(ctx context.Context) {
	if m.repo == nil {
		return
	}
	pendingJobs, err := m.repo.ListPendingEngineCleanups(ctx)
	if err != nil {
		log.Printf("processPendingEngineCleanups: failed to query pending cleanups: %v", err)
		return
	}

	for i := range pendingJobs {
		j := &pendingJobs[i]
		_ = m.retryPendingEngineCleanup(ctx, j)
	}
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
