package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"downloader/internal/api"
	"downloader/internal/app"
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

	log.Printf("Download Manager V0.7.1")
	log.Printf("Listen: %s", cfg.ListenAddr)
	log.Printf("Download dir: %s", cfg.DownloadDir)
	log.Printf("Aria2 RPC: %s", cfg.Aria2RPCURL)

	// Context for application lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize reusable application runtime
	application, err := app.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize application runtime: %w", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := application.Shutdown(shutdownCtx); err != nil {
			log.Printf("application shutdown error: %v", err)
		}
	}()

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
