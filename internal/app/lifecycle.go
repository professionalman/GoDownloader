package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"downloader/internal/runtimeguard"
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

	// 0. Acquire Core Runtime Ownership Guard BEFORE touching SQLite or any other subsystem
	guard, err := runtimeguard.Acquire(opt.dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire runtime ownership: %w", err)
	}
	cleanup.add(func() {
		_ = guard.Close()
	})

	// 0b. Data-directory cleanup ownership. The runtime guard is keyed on the database, so another runtime with a
	// different database (e.g. a diagnostic run) may share this DataDir. Startup cleanup of shared scratch (stale
	// media work directories, auth exports) can only prove ownership while holding this lock; a runtime that
	// cannot take it preserves those files instead of deleting another runtime's in-progress work.
	var cleanupOwner io.Closer
	if g, gErr := runtimeguard.Acquire(filepath.Join(cfg.DataDir, "cleanup-owner")); gErr == nil {
		cleanupOwner = g
		cleanup.add(func() {
			_ = g.Close()
		})
	} else {
		log.Printf("startup cleanup of shared data directory disabled: %v", gErr)
	}

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

	// Resolve effective qBittorrent connection parameters (env > settings/SecureStore > programmatic cfg > defaults)
	effectiveQBitURL, effectiveQBitUser, effectiveQBitPass, err := settingsService.GetEffectiveQBitCredentials(ctx, settings.QBitFallback{
		URL:      cfg.QBitURL,
		Username: cfg.QBitUsername,
		Password: cfg.QBitPassword,
	})
	if err != nil {
		return nil, fmt.Errorf("resolve qBittorrent connection settings: %w", err)
	}

	// 4. Initialize ToolManager & resolve external tools
	toolMgr := toolmanager.New(cfg.DataDir, execRepo)
	toolMgr.RegisterConfig(toolmanager.ToolYtdlp, cfg.YtdlpPath)
	toolMgr.RegisterConfig(toolmanager.ToolFFmpeg, cfg.FFmpegPath)
	toolMgr.RegisterConfig(toolmanager.ToolAria2, cfg.Aria2RPCURL)
	toolMgr.RegisterConfig(toolmanager.ToolQBittorrent, effectiveQBitURL)

	_ = toolMgr.RevalidateActiveRecords(ctx)

	ytdlpInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolYtdlp)
	ffmpegInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolFFmpeg)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolAria2)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolQBittorrent)
	// No yt-dlp process exists yet: remove orphaned staging files and binaries the managed ledger no longer selects.
	toolMgr.CleanupManagedStore(ctx)

	// 5. Initialize media auth service & perform startup stale temp cleanup
	mediaAuthService := mediaauth.NewService(settingsRepo, secretStore, filepath.Join(cfg.DataDir, "tmp", "auth"))
	if cleanupOwner != nil {
		if err := mediaAuthService.CleanupStaleTempFiles(); err != nil {
			log.Printf("media auth startup cleanup: %v", err)
		}
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

	resolvedYtdlpPath := ""
	ytdlpAvailable := ytdlpInfo != nil && ytdlpInfo.Available()
	if ytdlpAvailable {
		resolvedYtdlpPath = ytdlpInfo.ExecutablePath
	}
	resolvedFFmpegPath := ""
	if ffmpegInfo != nil && ffmpegInfo.Available() {
		resolvedFFmpegPath = ffmpegInfo.ExecutablePath
	}

	// Record what the media engine is configured with, so dependency diagnostics report what yt-dlp will actually
	// use (it receives --ffmpeg-location only when an FFmpeg was resolved here).
	toolMgr.SetEngineFFmpegPath(resolvedFFmpegPath)
	ytdlpEng := ytdlp.NewEngine(resolvedYtdlpPath, resolvedFFmpegPath)
	ytdlpEng.SetAuthProvider(mediaAuthService)
	ytdlpEng.SetProcessSupervisor(processSupervisor)
	// Managed activation coordinates with yt-dlp executions through this engine's shared UsageGate. The registry
	// is never changed at runtime: a newly activated binary is registered on the next backend start.
	toolMgr.SetUsageCoordinator(ytdlpEng)
	if ytdlpAvailable && ytdlpEng.Available() {
		registry.Register("ytdlp", ytdlpEng)
		log.Printf("yt-dlp engine: available (provenance=%s, version=%s, path=%s)", ytdlpInfo.Provenance, ytdlpInfo.Version, resolvedYtdlpPath)
	} else {
		if ytdlpInfo != nil {
			log.Printf("yt-dlp engine: not available (%s)", ytdlpInfo.Diagnostic)
		} else {
			log.Printf("yt-dlp engine: not available")
		}
	}

	qbitEng := qbittorrent.NewEngine(effectiveQBitURL, effectiveQBitUser, effectiveQBitPass, cfg.QBitTimeout)
	registry.Register("qbittorrent", qbitEng)
	if err := qbitEng.HealthCheck(ctx); err != nil {
		log.Printf("qBittorrent engine: registered but not reachable (%v)", err)
	} else {
		log.Printf("qBittorrent engine: available at %s", effectiveQBitURL)
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
	manager.SetSharedDataCleanupAllowed(cleanupOwner != nil)
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
		runtimeGuard:      guard,
		cleanupOwner:      cleanupOwner,
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

// ErrShutdownIncomplete reports that a graceful-shutdown phase did not finish within its deadline. The remaining
// mandatory cleanup (process-tree termination, database close, ownership-lock release) still ran.
var ErrShutdownIncomplete = errors.New("graceful shutdown incomplete")

// shutdownCleanupGrace bounds the mandatory process-tree cleanup when the caller's deadline was already consumed by
// an earlier phase, so owned processes are still terminated before the database and ownership locks are released.
const shutdownCleanupGrace = 3 * time.Second

// Shutdown terminates the application runtime in reverse dependency order, bounded by ctx:
//  1. Cancels and drains runtime-owned background goroutines (TrackerService) — waits at most until ctx is done.
//  2. Manager.SignalStop() (non-blocking: scheduler, reconciler, monitor, governor, active tasks) followed by
//     Manager.Stop() — waits at most until ctx is done (or a short grace if ctx was already exhausted). A worker that
//     does not exit in time (for example blocked in an engine call) is abandoned and ErrShutdownIncomplete is
//     reported; it is never waited for indefinitely.
//  3. ProcessSupervisor.Shutdown — always runs. If ctx is already exhausted it gets its own bounded grace
//     (shutdownCleanupGrace), so owned process trees are still terminated.
//  4. Database.Close() — always runs. New statements are refused afterwards; a statement an abandoned worker already
//     started may still complete on its connection after Close returns (database/sql closes in-use connections when
//     they are released). In the desktop and server hosts the process exits right after Shutdown; any unpersisted
//     step of an abandoned worker is reconciled by durable recovery on the next start.
//  5. Runtime ownership and cleanup-ownership locks are released after the database is closed.
//
// Shutdown is concurrency-safe and idempotent: concurrent callers join the active teardown and return its outcome
// (bounded by the owner's deadline), and repeated calls return the recorded outcome without a duplicate pass.
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
		// Joiners never return while the shared teardown is still running (they must not act as if the runtime were
		// stopped while the database and ownership locks are still held). The owner's teardown is itself bounded by
		// the owner's deadline plus the cleanup grace, so this wait is bounded whenever the owner passed one.
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
	stopManager := a.stopManager
	supervisor := a.processSupervisor
	db := a.db
	guard := a.runtimeGuard
	cleanupOwner := a.cleanupOwner
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

	// Step 2: Stop Manager and background loops, bounded by ctx. Every worker is signalled synchronously first
	// (non-blocking), so one worker stuck in an engine call cannot keep the monitor, reconciler or active tasks
	// running; only the wait for them is bounded. If ctx was already exhausted (for example by the tracker drain),
	// the wait still gets its own short grace rather than none.
	if manager != nil {
		manager.SignalStop()
		if stopManager == nil {
			stopManager = manager.Stop
		}
		waitCtx := ctx
		if ctx.Err() != nil {
			graceCtx, cancelGrace := context.WithTimeout(context.Background(), shutdownCleanupGrace)
			defer cancelGrace()
			waitCtx = graceCtx
		}
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			stopManager()
		}()
		select {
		case <-stopped:
		case <-waitCtx.Done():
			log.Printf("shutdown: background workers did not stop before the deadline; continuing with mandatory cleanup")
			errs = append(errs, fmt.Errorf("%w: background workers did not stop before the shutdown deadline", ErrShutdownIncomplete))
		}
	}

	// Step 3: Shutdown ProcessSupervisor. Owned process trees are terminated even if ctx is already exhausted.
	if supervisor != nil {
		supCtx := ctx
		if ctx.Err() != nil {
			graceCtx, cancelGrace := context.WithTimeout(context.Background(), shutdownCleanupGrace)
			defer cancelGrace()
			supCtx = graceCtx
		}
		if err := supervisor.Shutdown(supCtx); err != nil {
			errs = append(errs, fmt.Errorf("process supervisor shutdown: %w", err))
		}
	}

	// Step 4: Close database strictly after manager and supervisor persistence completes
	if db != nil {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("database close: %w", err))
		}
	}

	// Step 5: Release core runtime ownership guard strictly after database closure
	if guard != nil {
		if err := guard.Close(); err != nil {
			errs = append(errs, fmt.Errorf("runtime guard close: %w", err))
		}
	}
	if cleanupOwner != nil {
		if err := cleanupOwner.Close(); err != nil {
			errs = append(errs, fmt.Errorf("data directory cleanup lock close: %w", err))
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
