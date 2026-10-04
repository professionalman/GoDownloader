package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"downloader/internal/api"
	"downloader/internal/app"
	"downloader/internal/buildinfo"
	"downloader/internal/config"
	"downloader/internal/events"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("%v", err)
	}
}

func run() error {
	cfg := config.New()

	log.Printf("GoDownloader Server %s", buildinfo.Current().Version)
	log.Printf("Listen: %s", cfg.ListenAddr)
	log.Printf("Download dir: %s", cfg.DownloadDir)
	log.Printf("Aria2 RPC: %s", cfg.Aria2RPCURL)

	// Context for application lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Parse optional --db flag or DB_PATH env
	dbPath := os.Getenv("DB_PATH")
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "--db" && i+1 < len(os.Args) {
			dbPath = os.Args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--db=") {
			dbPath = strings.TrimPrefix(arg, "--db=")
		}
	}

	var opts []app.Option
	if dbPath != "" {
		opts = append(opts, app.WithDBPath(dbPath))
	}

	// Initialize reusable application runtime
	application, err := app.New(ctx, cfg, opts...)
	if err != nil {
		if errors.Is(err, app.ErrRuntimeAlreadyOwned) {
			return fmt.Errorf("GoDownloader runtime is already using this data store")
		}
		return fmt.Errorf("failed to initialize application runtime: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := application.Shutdown(shutdownCtx); err != nil {
			log.Printf("application shutdown error: %v", err)
		}
	}()

	if err := cfg.EnsureDirectories(); err != nil {
		return fmt.Errorf("failed to create server directories: %w", err)
	}

	// Start application background tasks and recovery
	if err := application.Start(ctx); err != nil {
		return fmt.Errorf("failed to start application runtime: %w", err)
	}

	// Enforce loopback binding policy
	if err := api.CheckNonLoopbackBind(cfg.ListenAddr); err != nil {
		return fmt.Errorf("security violation: %w", err)
	}

	// Setup SSE handler wired to application event bus and manager cursor
	sseHandler := events.NewSSEHandler(application.EventBus())
	sseHandler.SetCursorProvider(application.Manager())

	// Setup security and router
	secManager := api.NewSecurityManager(cfg)
	router := api.NewRouter(cfg, application.Manager(), sseHandler, application.Settings(), secManager, application.Categories(), application.Tracker(), application.MediaAuth())

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
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	log.Println("Server stopped cleanly.")
	return nil
}
