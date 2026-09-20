package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
	"downloader/web"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var globalCoreInitCount int64

func main() {
	// Diagnostic flag: print CWD and resolved DATA_ROOT for verification
	for _, arg := range os.Args[1:] {
		if arg == "--print-data-root" {
			cwd, _ := os.Getwd()
			dataRoot, err := ResolveDesktopDataRoot()
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("CWD=%s\nDATA_ROOT=%s\n", cwd, dataRoot)
			return
		}
	}

	// Diagnostic smoke test: verifies core runtime, deterministic data root, and shutdown
	for _, arg := range os.Args[1:] {
		if arg == "--diagnostic-smoke" {
			dataRoot, err := ResolveDesktopDataRoot()
			if err != nil {
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: data root: %v\n", err)
				os.Exit(1)
			}
			dbPath := filepath.Join(dataRoot, "downloader_smoke.db")
			defer os.Remove(dbPath)

			cfg := config.New()
			cfg.DownloadDir = filepath.Join(dataRoot, "downloads")
			appInstance, err := app.New(context.Background(), cfg, app.WithDBPath(dbPath))
			if err != nil {
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: app.New: %v\n", err)
				os.Exit(1)
			}

			startCtx, cancelStart := context.WithTimeout(context.Background(), 10*time.Second)
			if err := appInstance.Start(startCtx); err != nil {
				cancelStart()
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: appInstance.Start: %v\n", err)
				os.Exit(1)
			}
			cancelStart()

			// Check domain services
			if appInstance.Manager() == nil || appInstance.Settings() == nil || appInstance.EventBus() == nil {
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: missing core services\n")
				os.Exit(1)
			}

			svc := NewDesktopService(appInstance, dataRoot)
			if svc.GetBackendInstanceID() == "" {
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: invalid instance ID\n")
				os.Exit(1)
			}

			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			if err := appInstance.Shutdown(shutdownCtx); err != nil {
				cancelShutdown()
				fmt.Fprintf(os.Stderr, "SMOKE_TEST_FAILED: appInstance.Shutdown: %v\n", err)
				os.Exit(1)
			}
			cancelShutdown()

			fmt.Println("SMOKE_TEST_SUCCESS: runtime initialized, data root resolved, clean shutdown")
			return
		}
	}

	// Diagnostic flag: print single instance status for runtime verification
	for _, arg := range os.Args[1:] {
		if arg == "--print-single-instance-status" {
			dataRoot, err := ResolveDesktopDataRoot()
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
				os.Exit(1)
			}
			statusPath := filepath.Join(dataRoot, "single_instance_status.json")
			data, err := os.ReadFile(statusPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR: failed to read status: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(data))
			return
		}
	}

	var secondLaunchHandler func(data application.SecondInstanceData)
	var mainWindow *application.WebviewWindow

	// =========================================================================
	// SINGLE-INSTANCE ESTABLISHMENT BEFORE CORE RUNTIME
	// Any secondary process exits cleanly with code 0 inside application.New.
	// Secondary processes NEVER reach app.New, app.Start, or DB initialization!
	// =========================================================================
	wailsApp := application.New(application.Options{
		Name:        "GoDownloader",
		Description: "GoDownloader Windows Desktop Technical Preview",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(web.Assets),
		},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.godownloader.desktop.app",
			ExitCode: 0,
			OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
				if secondLaunchHandler != nil {
					secondLaunchHandler(data)
				}
				if mainWindow != nil {
					mainWindow.Show()
					mainWindow.Restore()
					mainWindow.Focus()
				}
			},
		},
	})

	// =========================================================================
	// PRIMARY PROCESS EXECUTION BEYOND THIS POINT
	// =========================================================================

	atomic.AddInt64(&globalCoreInitCount, 1)

	dataRoot, err := ResolveDesktopDataRoot()
	if err != nil {
		log.Fatalf("Fatal: failed to resolve desktop data root: %v", err)
	}

	dbPath := filepath.Join(dataRoot, "downloader.db")
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(dataRoot, "downloads")
	appInstance, err := app.New(context.Background(), cfg, app.WithDBPath(dbPath))
	if err != nil {
		log.Fatalf("Fatal: failed to construct application core: %v", err)
	}

	startCtx, cancelStart := context.WithTimeout(context.Background(), 15*time.Second)
	if err := appInstance.Start(startCtx); err != nil {
		cancelStart()
		log.Fatalf("Fatal: failed to start application runtime: %v", err)
	}
	cancelStart()

	service := NewDesktopService(appInstance, dataRoot)
	wailsApp.RegisterService(application.NewService(service))

	secondLaunchHandler = func(data application.SecondInstanceData) {
		service.recordSecondLaunch(data.Args, data.WorkingDir)
		for _, arg := range data.Args {
			if arg == "--trigger-quit" {
				service.Quit()
				return
			}
			if arg == "--test-dialog" {
				go service.ShowNativeDialog("GoDownloader Verification", "Native dialog verification message")
				return
			}
		}
	}

	// Wire application event bus to Wails native events
	eventCh := appInstance.EventBus().Subscribe()
	go func() {
		for ev := range eventCh {
			wailsApp.Event.Emit("godownloader.event", ev)
		}
	}()

	mainWindow = wailsApp.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:   "main",
		Title:  "GoDownloader",
		Width:  1200,
		Height: 800,
		URL:    "/desktop.html",
	})

	service.setWailsContext(wailsApp, mainWindow)

	runErr := wailsApp.Run()

	// Shutdown application runtime upon exit
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = appInstance.Shutdown(shutdownCtx)

	if runErr != nil {
		log.Fatalf("Desktop application terminated with error: %v", runErr)
	}
}
