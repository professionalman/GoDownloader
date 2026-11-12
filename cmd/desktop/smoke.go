package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"downloader/internal/config"
)

// runDiagnosticSmoke starts and stops a complete core runtime in an isolated, disposable workspace.
//
// It deliberately does not use the desktop data root: a smoke runtime with its own database but the main
// application's data directory would run startup cleanup that cannot prove ownership of the main application's
// in-progress media work directories and auth exports, and would delete them. The workspace is removed afterwards.
func runDiagnosticSmoke() error {
	workspace, err := os.MkdirTemp("", "godownloader-smoke-*")
	if err != nil {
		return fmt.Errorf("create isolated workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	return runSmokeInWorkspace(workspace)
}

func runSmokeInWorkspace(workspace string) error {
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(workspace, "downloads")
	cfg.DataDir = filepath.Join(workspace, "data")

	core, err := startDesktopCore(cfg, filepath.Join(workspace, "downloader_smoke.db"))
	if err != nil {
		return fmt.Errorf("start core: %w", err)
	}
	appInstance := core.App()
	if appInstance.Manager() == nil || appInstance.Settings() == nil || appInstance.EventBus() == nil {
		_ = core.Shutdown(5 * time.Second)
		return fmt.Errorf("missing core services")
	}
	// Workers must outlive initialization (regression guard for the cancelled start context).
	if !waitWorkersAlive(appInstance, 2*time.Second) {
		w := appInstance.BackgroundWorkers()
		_ = core.Shutdown(5 * time.Second)
		return fmt.Errorf("background workers not alive after start: %+v", w)
	}
	svc := NewDesktopService(appInstance, workspace)
	if svc.GetBackendInstanceID() == "" {
		_ = core.Shutdown(5 * time.Second)
		return fmt.Errorf("invalid instance ID")
	}
	if err := core.Shutdown(5 * time.Second); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
