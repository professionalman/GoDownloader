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
)

func main() {
	cfg := config.New()

	log.Printf("Download Manager V0.4")
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

	// Create repository
	repo := database.NewSQLiteJobRepository(db)

	// Initialize aria2 engine
	eng := aria2.NewEngine(cfg.Aria2RPCURL, cfg.Aria2Secret)

	// Initialize engine registry
	registry := engine.NewRegistry()
	registry.Register("aria2", eng)

	// Initialize yt-dlp engine if available
	ytdlpEng := ytdlp.NewEngine(cfg.YtdlpPath, cfg.FFmpegPath)
	if ytdlpEng.Available() {
		registry.Register("ytdlp", ytdlpEng)
		log.Printf("yt-dlp engine: available")
	} else {
		log.Printf("yt-dlp engine: not available (yt-dlp not found in PATH)")
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

	// Initialize SSE handler
	sseHandler := events.NewSSEHandler(bus)

	// Initialize job manager
	manager := job.NewManager(repo, registry, bus, cfg.DownloadDir, torrentRepo, cfg.DataDir)
	manager.StartBackgroundTasks(ctx)
	defer manager.Stop()

	// Setup router
	router := api.NewRouter(cfg, manager, sseHandler)

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

	log.Printf("Server running at http://localhost%s", cfg.ListenAddr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Server stopped cleanly.")
}
