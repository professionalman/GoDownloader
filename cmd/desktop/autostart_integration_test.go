//go:build windows && integration

package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
	"downloader/web"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows/registry"
)

func TestAutostartIntegration_RealWindowsRegistry(t *testing.T) {
	// 1. Capture original state
	const subKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	const valueName = "godownloader"

	origKey, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.QUERY_VALUE)
	var origExists bool
	var origVal string
	if err == nil {
		if val, _, err := origKey.GetStringValue(valueName); err == nil {
			origExists = true
			origVal = val
		}
		origKey.Close()
	}

	// Ensure cleanup always restores original state
	defer func() {
		k, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.SET_VALUE)
		if err == nil {
			defer k.Close()
			if origExists {
				_ = k.SetStringValue(valueName, origVal)
			} else {
				_ = k.DeleteValue(valueName)
			}
		}
	}()

	// 2. Setup Wails application and DesktopService
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "autostart_test.db")
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(tmpDir, "downloads")

	appInstance, err := app.New(context.Background(), cfg, app.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := appInstance.Start(ctx); err != nil {
		t.Fatalf("failed to start app: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = appInstance.Shutdown(shutdownCtx)
	}()

	lifecycle := NewDesktopLifecycle()
	lifecycle.SetTrayConfigured(true)

	wailsApp := application.New(application.Options{
		Name: "GoDownloader",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(web.Assets),
		},
	})

	svc := NewDesktopService(appInstance, tmpDir)
	svc.setLifecycle(lifecycle)
	svc.setWailsContext(wailsApp, nil)

	// 3. Test Enable Autostart
	prefs, err := svc.SetAutostart(true)
	if err != nil {
		t.Fatalf("SetAutostart(true) failed: %v", err)
	}
	if !prefs.AutostartEnabled {
		t.Fatalf("expected prefs.AutostartEnabled to be true, got false")
	}

	// Verify directly in Windows Registry
	checkKey, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("failed to open registry key: %v", err)
	}
	regVal, _, err := checkKey.GetStringValue(valueName)
	checkKey.Close()
	if err != nil {
		t.Fatalf("failed to find %s in registry after enable: %v", valueName, err)
	}

	if !strings.Contains(regVal, "--background") {
		t.Fatalf("expected registry value to contain --background argument, got: %s", regVal)
	}

	// Verify no sensitive tokens/passwords/URLs in registry command
	disallowed := []string{"password", "secret", "token", "credential", "cookie", "key", "http:", "https:"}
	for _, bad := range disallowed {
		if strings.Contains(strings.ToLower(regVal), bad) {
			t.Fatalf("registry command contains sensitive token %q: %s", bad, regVal)
		}
	}

	// 4. Test Disable Autostart
	prefs, err = svc.SetAutostart(false)
	if err != nil {
		t.Fatalf("SetAutostart(false) failed: %v", err)
	}
	if prefs.AutostartEnabled {
		t.Fatalf("expected prefs.AutostartEnabled to be false, got true")
	}

	// Verify deleted from Windows Registry
	checkKey2, err := registry.OpenKey(registry.CURRENT_USER, subKey, registry.QUERY_VALUE)
	if err == nil {
		_, _, err = checkKey2.GetStringValue(valueName)
		checkKey2.Close()
		if err == nil {
			t.Fatalf("expected %s to be deleted from registry after disable, but it was found", valueName)
		}
	}

	// 5. Final state check
	finalPrefs, err := svc.GetDesktopPreferences()
	if err != nil {
		t.Fatalf("GetDesktopPreferences failed: %v", err)
	}
	if finalPrefs.AutostartEnabled {
		t.Fatalf("expected finalPrefs.AutostartEnabled to be false")
	}
}
