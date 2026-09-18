package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/engine/aria2"
	"downloader/internal/engine/qbittorrent"
	"downloader/internal/engine/ytdlp"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/mediaauth"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/toolmanager"
	"downloader/internal/tracker"
)

func main() {
	cfg := config.New()

	log.Printf("Download Manager V0.7.1")
	log.Printf("Listen: %s", cfg.ListenAddr)
	log.Printf("Download dir: %s", cfg.DownloadDir)
	log.Printf("Aria2 RPC: %s", cfg.Aria2RPCURL)

	// Context for application lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize database
	dbPath := filepath.Join(".", "downloader.db")
	db, err := database.New(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// Create repositories
	repo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	secretRepo := database.NewSQLiteSecretRepository(db)
	trackerRepo := database.NewSQLiteTrackerRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	execRepo := database.NewSQLiteExecutionRepository(db)

	// Initialize master key and secure store
	keyMgr := securestore.NewDefaultMasterKeyManager()
	cipher, keyStatus, keyErr := keyMgr.ResolveCipher(ctx, secretRepo)
	if keyErr != nil {
		log.Fatalf("Master key initialization failed: %v", keyErr)
	}
	log.Printf("settings encryption active (provider=%s, status=%s)", keyMgr.ProviderName(), keyStatus)
	secretStore := securestore.NewStore(secretRepo, cipher)
	settingsService := settings.NewSettingsService(settingsRepo, cfg.DownloadDir, cfg.DataDir, secretStore)

	// Initialize ToolManager & resolve external tools
	toolMgr := toolmanager.New(cfg.DataDir, execRepo)
	toolMgr.RegisterConfig(toolmanager.ToolYtdlp, cfg.YtdlpPath)
	toolMgr.RegisterConfig(toolmanager.ToolFFmpeg, cfg.FFmpegPath)
	toolMgr.RegisterConfig(toolmanager.ToolAria2, cfg.Aria2RPCURL)
	toolMgr.RegisterConfig(toolmanager.ToolQBittorrent, cfg.QBitURL)

	// Revalidate active records from tool ledger
	_ = toolMgr.RevalidateActiveRecords(ctx)

	// Resolve external tools
	ytdlpInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolYtdlp)
	ffmpegInfo, _ := toolMgr.Resolve(ctx, toolmanager.ToolFFmpeg)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolAria2)
	_, _ = toolMgr.Resolve(ctx, toolmanager.ToolQBittorrent)

	// Initialize media auth service & perform startup stale temp cleanup
	mediaAuthService := mediaauth.NewService(settingsRepo, secretStore, filepath.Join(cfg.DataDir, "tmp", "auth"))
	if err := mediaAuthService.CleanupStaleTempFiles(); err != nil {
		log.Printf("media auth startup cleanup: %v", err)
	}

	// Initialize storage service
	storageService := storage.NewStorageService(catRepo, settingsService, storage.NewOSFreeSpaceProvider(), cfg.DownloadDir, cfg.DataDir)

	// Initialize aria2 engine
	eng := aria2.NewEngine(cfg.Aria2RPCURL, cfg.Aria2Secret)

	// Initialize engine registry
	registry := engine.NewRegistry()
	registry.Register("aria2", eng)

	// Initialize yt-dlp engine using resolved paths
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
	if ytdlpEng.Available() {
		registry.Register("ytdlp", ytdlpEng)
		log.Printf("yt-dlp engine: available (provenance=%s, version=%s, path=%s)", ytdlpInfo.Provenance, ytdlpInfo.Version, resolvedYtdlpPath)
	} else {
		log.Printf("yt-dlp engine: not available (%s)", ytdlpInfo.Diagnostic)
	}

	// Initialize qBittorrent engine
	qbitEng := qbittorrent.NewEngine(cfg.QBitURL, cfg.QBitUsername, cfg.QBitPassword, cfg.QBitTimeout)
	registry.Register("qbittorrent", qbitEng)
	if err := qbitEng.HealthCheck(context.Background()); err != nil {
		log.Printf("qBittorrent engine: registered but not reachable (%v)", err)
	} else {
		log.Printf("qBittorrent engine: available at %s", cfg.QBitURL)
	}

	// Create torrent repository
	torrentRepo := database.NewSQLiteTorrentRepository(db)

	// Initialize event bus
	bus := events.NewInMemoryBus()
	trackerService := tracker.NewService(trackerRepo, bus)
	go trackerService.Run(ctx)

	// Initialize SSE handler
	sseHandler := events.NewSSEHandler(bus)

	// Initialize job manager & scheduler
	manager := job.NewManager(repo, registry, bus, cfg.DownloadDir, torrentRepo, cfg.DataDir)
	sseHandler.SetCursorProvider(manager)
	manager.SetMetadataTimeoutSeconds(cfg.QBitMetadataTimeoutSeconds)
	manager.SetQueueRepository(queueRepo)
	manager.SetSettingsService(settingsService)
	manager.SetStorageService(storageService)
	manager.SetCategoryRepository(catRepo)
	manager.SetTrackerEntryProvider(trackerService)
	manager.SetExecutionRepository(execRepo)

	scheduler := job.NewScheduler(repo, queueRepo, settingsService.EffectiveMaxConcurrentDownloads, manager.DispatchQueuedJob)
	manager.SetScheduler(scheduler)

	manager.StartBackgroundTasks(ctx)
	defer manager.Stop()

	// Enforce loopback binding policy
	if err := api.CheckNonLoopbackBind(cfg.ListenAddr); err != nil {
		log.Fatalf("Security violation: %v", err)
	}

	// Setup security and router
	secManager := api.NewSecurityManager(cfg)
	router := api.NewRouter(cfg, manager, sseHandler, settingsService, secManager, catRepo, trackerService, mediaAuthService)

	// Start server
	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: router,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println()
		log.Println("Shutting down...")

		// Cancel context to stop background tasks
		cancel()

		// Shutdown HTTP server
		server.Close()
	}()

	log.Printf("Server running at http://%s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped cleanly.")
}
