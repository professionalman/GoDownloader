package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
)

func TestDesktop_CWDIndependence(t *testing.T) {
	// 1. Set up an isolated mock APPDATA
	isolatedAppData := t.TempDir()
	t.Setenv("APPDATA", isolatedAppData)

	// 2. Set up an arbitrary working directory
	arbitraryCwd := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current working directory: %v", err)
	}
	if err := os.Chdir(arbitraryCwd); err != nil {
		t.Fatalf("failed to chdir to arbitrary directory: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	// 3. Execute desktop bootstrap logic
	dataRoot, err := ResolveDesktopDataRoot()
	if err != nil {
		t.Fatalf("failed to resolve desktop data root: %v", err)
	}
	expectedRoot := filepath.Join(isolatedAppData, "GoDownloader")
	if dataRoot != expectedRoot {
		t.Fatalf("expected dataRoot %q, got %q", expectedRoot, dataRoot)
	}

	dbPath := filepath.Join(dataRoot, "downloader.db")
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(dataRoot, "downloads")
	cfg.DataDir = filepath.Join(dataRoot, "data")
	cfg.ListenAddr = "127.0.0.1:0"

	if err := cfg.EnsureDirectories(); err != nil {
		t.Fatalf("failed to ensure directories: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	appInstance, err := app.New(ctx, cfg, app.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("app.New failed: %v", err)
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
	if err := appInstance.Start(startCtx); err != nil {
		cancelStart()
		_ = appInstance.Shutdown(context.Background())
		t.Fatalf("app.Start failed: %v", err)
	}
	cancelStart()

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := appInstance.Shutdown(shutdownCtx); err != nil {
		cancelShutdown()
		t.Fatalf("app.Shutdown failed: %v", err)
	}
	cancelShutdown()

	// 4. Assert that arbitraryCwd contains NO leaked runtime artifacts
	entries, err := os.ReadDir(arbitraryCwd)
	if err != nil {
		t.Fatalf("failed to read arbitrary working directory: %v", err)
	}
	if len(entries) != 0 {
		var leaked []string
		for _, e := range entries {
			leaked = append(leaked, e.Name())
		}
		t.Fatalf("expected arbitrary CWD to have 0 entries, but found leaked runtime artifacts: %v", leaked)
	}

	// 5. Assert that mutable runtime state was created exclusively in dataRoot
	if _, err := os.Stat(filepath.Join(dataRoot, "downloader.db")); err != nil {
		t.Errorf("expected downloader.db in dataRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "downloader.db.runtime.lock")); err != nil {
		t.Errorf("expected runtime lock in dataRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "data")); err != nil {
		t.Errorf("expected data dir in dataRoot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "downloads")); err != nil {
		t.Errorf("expected downloads dir in dataRoot: %v", err)
	}
}
