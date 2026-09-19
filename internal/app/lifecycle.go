package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/engine/aria2"
	"downloader/internal/engine/qbittorrent"
	"downloader/internal/engine/ytdlp"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/mediaauth"
	"downloader/internal/process"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/toolmanager"
	"downloader/internal/tracker"
)

// cleanupStack manages LIFO rollback of partially initialized resources during New.
type cleanupStack struct {
	cleanups []func()
}

func (s *cleanupStack) add(fn func()) {
	s.cleanups = append(s.cleanups, fn)
}

func (s *cleanupStack) run() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
}

// New constructs and wires all durable backend subsystems into a unified App runtime.
// If construction fails halfway through, all already-opened resources are safely rolled back
// in reverse allocation order.
func New(ctx context.Context, cfg *config.Config, opts ...Option) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	opt := options{
		dbPath: filepath.Join(".", "downloader.db"),
	}
	for _, o := range opts {
		o(&opt)
	}

	var cleanup cleanupStack
	success := false
	defer func() {
		if !success {
			cleanup.run()
		}
	}()

	// 1. Initialize SQLite Database
	db, err := database.New(opt.dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	cleanup.add(func() {
		_ = db.Close()
	})

	// 2. Create repositories
	repo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	secretRepo := database.NewSQLiteSecretRepository(db)
	trackerRepo := database.NewSQLiteTrackerRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	execRepo := database.NewSQLiteExecutionRepository(db)
	torrentRepo := database.NewSQLiteTorrentRepository(db)

	// 3. Initialize master key and secure store
	keyMgr := securestore.NewDefaultMasterKeyManager()
	cipher, keyStatus, keyErr := keyMgr.ResolveCipher(ctx, secretRepo)
	if keyErr != nil {
		return nil, fmt.Errorf("master key initialization failed: %w", keyErr)
	}
	log.Printf("settings encryption active (provider=%s, status=%s)", keyMgr.ProviderName(), keyStatus)
	secretStore := securestore.NewStore(secretRepo, cipher)
	settingsService := settings.NewSettingsService(settingsRepo, cfg.DownloadDir, cfg.DataDir, secretStore)

	// 4. Initialize ToolManager & resolve external tools
	toolMgr := toolmanager.New(cfg.DataDir, execRepo)
	toolMgr.RegisterConfig(toolmanager.ToolYtdlp, cfg.YtdlpPath)
	toolMgr.RegisterConfig(toolmanager.ToolFFmpeg, cfg.FFmpegPath)
	toolMgr.RegisterConfig(toolmanager.ToolAria2, cfg.Aria2RPCURL)
	toolMgr.RegisterConfig(toolmanager.ToolQBittorrent, cfg.QBitURL)

	_ = toolMgr.RevalidateActiveRecords(ctx)

	ytdlpInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolYtdlp)
	ffmpegInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolFFmpeg)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolAria2)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolQBittorrent)

	// 5. Initialize media auth service & perform startup stale temp cleanup
	mediaAuthService := mediaauth.NewService(settingsRepo, secretStore, filepath.Join(cfg.DataDir, "tmp", "auth"))
	if err := mediaAuthService.CleanupStaleTempFiles(); err != nil {
		log.Printf("media auth startup cleanup: %v", err)
	}

	// 6. Initialize storage service
	storageService := storage.NewStorageService(catRepo, settingsService, storage.NewOSFreeSpaceProvider(), cfg.DownloadDir, cfg.DataDir)

	// 7. Initialize ProcessSupervisor for application-owned child process trees
	processSupervisor := process.NewSupervisor()
	cleanup.add(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = processSupervisor.Shutdown(shutdownCtx)
	})

	// 8. Initialize engines & registry
	eng := aria2.NewEngine(cfg.Aria2RPCURL, cfg.Aria2Secret)
	registry := engine.NewRegistry()
	registry.Register("aria2", eng)

	resolvedYtdlpPath := cfg.YtdlpPath
	if ytdlpInfo != nil && ytdlpInfo.Available() {
		resolvedYtdlpPath = ytdlpInfo.ExecutablePath
	}
	resolvedFFmpegPath := cfg.FFmpegPath
	if ffmpegInfo != nil && ffmpegInfo.Available() {
		resolvedFFmpegPath = ffmpegInfo.ExecutablePath
	}

	ytdlpEng := ytdlp.NewEngine(resolvedYtdlpPath, resolvedFFmpegPath)
	ytdlpEng.SetAuthProvider(mediaAuthService)
	ytdlpEng.SetProcessSupervisor(processSupervisor)
	if ytdlpEng.Available() {
		registry.Register("ytdlp", ytdlpEng)
		log.Printf("yt-dlp engine: available (provenance=%s, version=%s, path=%s)", ytdlpInfo.Provenance, ytdlpInfo.Version, resolvedYtdlpPath)
	} else {
		if ytdlpInfo != nil {
			log.Printf("yt-dlp engine: not available (%s)", ytdlpInfo.Diagnostic)
		} else {
			log.Printf("yt-dlp engine: not available")
		}
	}

	qbitEng := qbittorrent.NewEngine(cfg.QBitURL, cfg.QBitUsername, cfg.QBitPassword, cfg.QBitTimeout)
	registry.Register("qbittorrent", qbitEng)
	if err := qbitEng.HealthCheck(ctx); err != nil {
		log.Printf("qBittorrent engine: registered but not reachable (%v)", err)
	} else {
		log.Printf("qBittorrent engine: available at %s", cfg.QBitURL)
	}

	// 9. Initialize event bus & tracker service
	bus := events.NewInMemoryBus()
	trackerService := tracker.NewService(trackerRepo, bus)

	// 10. Initialize job manager, governor, scheduler
	manager := job.NewManager(repo, registry, bus, cfg.DownloadDir, torrentRepo, cfg.DataDir)
	manager.SetMetadataTimeoutSeconds(cfg.QBitMetadataTimeoutSeconds)
	manager.SetQueueRepository(queueRepo)
	manager.SetSettingsService(settingsService)
	manager.SetStorageService(storageService)
	manager.SetCategoryRepository(catRepo)
	manager.SetTrackerEntryProvider(trackerService)
	manager.SetExecutionRepository(execRepo)

	resourceGovernor := job.NewResourceGovernor(job.DefaultResourceGovernorConfig(), func() int {
		return settingsService.EffectiveMaxConcurrentDownloads(context.Background())
	})
	manager.SetResourceGovernor(resourceGovernor)

	scheduler := job.NewScheduler(repo, queueRepo, settingsService.EffectiveMaxConcurrentDownloads, manager.DispatchQueuedJob)
	scheduler.SetResourceGovernor(resourceGovernor)
	manager.SetScheduler(scheduler)

	a := &App{
		cfg:               cfg,
		db:                db,
		repo:              repo,
		queueRepo:         queueRepo,
		settingsRepo:      settingsRepo,
		secretRepo:        secretRepo,
		trackerRepo:       trackerRepo,
		catRepo:           catRepo,
		execRepo:          execRepo,
		torrentRepo:       torrentRepo,
		keyMgr:            keyMgr,
		secretStore:       secretStore,
		settingsService:   settingsService,
		toolMgr:           toolMgr,
		storageService:    storageService,
		mediaAuthService:  mediaAuthService,
		processSupervisor: processSupervisor,
		registry:          registry,
		bus:               bus,
		trackerService:    trackerService,
		manager:           manager,
		resourceGovernor:  resourceGovernor,
		scheduler:         scheduler,
	}

	success = true
	return a, nil
}

// Start initiates recovery and launches background tasks (tracker runner, manager background tasks, scheduler).
// It returns an error if called more than once or if the application has been stopped.
func (a *App) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped || a.stopping {
		a.mu.Unlock()
		return fmt.Errorf("cannot start stopped application runtime")
	}
	if a.started {
		a.mu.Unlock()
		return fmt.Errorf("application runtime already started")
	}
	a.started = true

	// Set up managed tracker lifecycle context
	trackerCtx, trackerCancel := context.WithCancel(ctx)
	a.trackerCancel = trackerCancel
	a.trackerDone = make(chan struct{})
	trackerDone := a.trackerDone
	trackerService := a.trackerService
	manager := a.manager
	a.mu.Unlock()

	// 1. Start tracker background runner with managed drain channel
	go func() {
		defer close(trackerDone)
		trackerService.Run(trackerCtx)
	}()

	// 2. Start Manager background tasks (journal reconciliation, recovery, governor reconstruction, workdir cleanup, scheduler start, monitor start)
	manager.StartBackgroundTasks(ctx)

	return nil
}

// Shutdown gracefully terminates the application runtime in reverse dependency order:
//  1. Cancels and drains runtime-owned background goroutines (TrackerService)
//  2. Manager.Stop() (stops scheduler, reconciler, monitor, active tasks, ytdlp subprocess)
//  3. ProcessSupervisor.Shutdown(ctx) (terminates remaining owned process trees via Win32 Job Objects)
//  4. Database.Close() (closes SQLite connection strictly after manager and supervisor persistence completes)
//
// Shutdown is concurrency-safe and strictly idempotent: repeated or concurrent calls wait for the active
// teardown and receive the same outcome without running duplicate teardown passes.
func (a *App) Shutdown(ctx context.Context) error {
	a.mu.Lock()
	if a.stopped {
		err := a.shutdownErr
		a.mu.Unlock()
		return err
	}
	if a.stopping {
		done := a.shutdownDone
		a.mu.Unlock()
		<-done
		a.mu.Lock()
		err := a.shutdownErr
		a.mu.Unlock()
		return err
	}
	a.stopping = true
	a.shutdownDone = make(chan struct{})
	trackerCancel := a.trackerCancel
	trackerDone := a.trackerDone
	manager := a.manager
	supervisor := a.processSupervisor
	db := a.db
	a.mu.Unlock()

	var errs []error

	// Step 1: Cancel and drain tracker background runner before touching DB
	if trackerCancel != nil {
		trackerCancel()
	}
	if trackerDone != nil {
		select {
		case <-trackerDone:
		case <-ctx.Done():
			errs = append(errs, fmt.Errorf("tracker drain context error: %w", ctx.Err()))
		}
	}

	// Step 2: Stop Manager and background loops
	if manager != nil {
		manager.Stop()
	}

	// Step 3: Shutdown ProcessSupervisor with timeout context
	if supervisor != nil {
		if err := supervisor.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("process supervisor shutdown: %w", err))
		}
	}

	// Step 4: Close database strictly after manager and supervisor persistence completes
	if db != nil {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("database close: %w", err))
		}
	}

	var combinedErr error
	if len(errs) > 0 {
		combinedErr = errors.Join(errs...)
	}

	a.mu.Lock()
	a.stopped = true
	a.stopping = false
	a.shutdownErr = combinedErr
	close(a.shutdownDone)
	a.mu.Unlock()

	return combinedErr
}
