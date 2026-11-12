package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
	"downloader/internal/job"
)

// coreTestConfig returns an isolated desktop configuration. The aria2 RPC endpoint points to a closed port so a
// dispatched HTTP job fails fast and deterministically without touching the network.
func coreTestConfig(t *testing.T, root string) *config.Config {
	t.Helper()
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(root, "downloads")
	cfg.DataDir = filepath.Join(root, "data")
	cfg.Aria2RPCURL = "http://127.0.0.1:1/jsonrpc"
	cfg.YtdlpPath = filepath.Join(root, "missing-yt-dlp.exe")
	cfg.FFmpegPath = filepath.Join(root, "missing-ffmpeg.exe")
	return cfg
}

func startCoreForTest(t *testing.T, root string) *desktopCore {
	t.Helper()
	core, err := startDesktopCore(coreTestConfig(t, root), filepath.Join(root, "downloader.db"))
	if err != nil {
		t.Fatalf("startDesktopCore: %v", err)
	}
	t.Cleanup(func() { _ = core.Shutdown(5 * time.Second) })
	return core
}

func assertAllWorkersAlive(t *testing.T, a *app.App, when string) {
	t.Helper()
	if w := a.BackgroundWorkers(); !(w.Scheduler && w.Monitor && w.Reconciler && w.Tracker) {
		t.Fatalf("%s: background workers not all alive: %+v", when, w)
	}
}

// B-1: the production desktop start path must give background workers the application lifetime, not an
// initialization-scoped context that is cancelled as soon as startup returns.
func TestDesktopCoreWorkersOutliveInitialization(t *testing.T) {
	core := startCoreForTest(t, t.TempDir())
	if !waitWorkersAlive(core.App(), 2*time.Second) {
		t.Fatalf("workers did not become alive: %+v", core.App().BackgroundWorkers())
	}
	// The original defect cancelled the start context when main() moved past initialization; workers exited
	// within milliseconds. Observe them well after startDesktopCore has returned.
	time.Sleep(300 * time.Millisecond)
	assertAllWorkersAlive(t, core.App(), "after initialization returned")
}

// B-1: a queued job must actually be dispatched by the desktop runtime's scheduler.
func TestDesktopCoreDispatchesQueuedJob(t *testing.T) {
	core := startCoreForTest(t, t.TempDir())
	if !waitWorkersAlive(core.App(), 2*time.Second) {
		t.Fatalf("workers not alive: %+v", core.App().BackgroundWorkers())
	}
	ctx := context.Background()
	created, err := core.App().Manager().Create(ctx, "https://example.invalid/file.bin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		got, err := core.App().Manager().Get(ctx, created.ID)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Status != job.StatusQueued {
			// The aria2 endpoint is unreachable, so dispatch ends in a start failure; any transition out of
			// QUEUED proves the scheduler loop picked the job up.
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still QUEUED; scheduler did not dispatch (workers: %+v)", created.ID, core.App().BackgroundWorkers())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// B-1: closing the main window while close-to-tray is enabled hides the window; it must not stop the runtime.
func TestDesktopCoreCloseToTrayPreservesWorkers(t *testing.T) {
	core := startCoreForTest(t, t.TempDir())
	if !waitWorkersAlive(core.App(), 2*time.Second) {
		t.Fatalf("workers not alive: %+v", core.App().BackgroundWorkers())
	}
	lc := NewDesktopLifecycle()
	lc.SetTrayConfigured(true)
	lc.SetCloseToTray(true)
	if !lc.ShouldHideOnClose() {
		t.Fatal("expected close-to-tray to hide instead of quitting")
	}
	lc.HandleWindowClosing(nil, nil)
	if lc.IsQuitting() {
		t.Fatal("window close with tray enabled must not start quitting")
	}
	time.Sleep(100 * time.Millisecond)
	assertAllWorkersAlive(t, core.App(), "after hide-to-tray close")
}

// B-1: shutdown stops every worker and releases ownership so a fresh core on the same database gets a fresh
// lifetime with live workers.
func TestDesktopCoreShutdownStopsWorkersAndAllowsRestart(t *testing.T) {
	root := t.TempDir()
	cfg := coreTestConfig(t, root)
	dbPath := filepath.Join(root, "downloader.db")

	first, err := startDesktopCore(cfg, dbPath)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if !waitWorkersAlive(first.App(), 2*time.Second) {
		t.Fatalf("first workers not alive: %+v", first.App().BackgroundWorkers())
	}
	if err := first.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}
	if w := first.App().BackgroundWorkers(); w.Scheduler || w.Monitor || w.Reconciler || w.Tracker {
		t.Fatalf("workers still alive after shutdown: %+v", w)
	}
	// Idempotent.
	if err := first.Shutdown(5 * time.Second); err != nil {
		t.Fatalf("second shutdown call: %v", err)
	}

	second, err := startDesktopCore(coreTestConfig(t, root), dbPath)
	if err != nil {
		t.Fatalf("restart on same database: %v", err)
	}
	defer second.Shutdown(5 * time.Second)
	if !waitWorkersAlive(second.App(), 2*time.Second) {
		t.Fatalf("restarted workers not alive: %+v", second.App().BackgroundWorkers())
	}
}

// B-1: a second core on the same database is rejected and does not start a duplicate set of workers.
func TestDesktopCoreSecondInstanceRejected(t *testing.T) {
	root := t.TempDir()
	core := startCoreForTest(t, root)
	if !waitWorkersAlive(core.App(), 2*time.Second) {
		t.Fatalf("workers not alive: %+v", core.App().BackgroundWorkers())
	}
	dup, err := startDesktopCore(coreTestConfig(t, root), filepath.Join(root, "downloader.db"))
	if err == nil {
		_ = dup.Shutdown(5 * time.Second)
		t.Fatal("second core on the same database must be rejected")
	}
	if !errors.Is(err, app.ErrRuntimeAlreadyOwned) {
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got %v", err)
	}
	assertAllWorkersAlive(t, core.App(), "after rejected duplicate start")
}

// B-1: a startup failure after the runtime was constructed must release ownership.
func TestDesktopCoreStartupFailureReleasesOwnership(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "downloads-is-a-file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := coreTestConfig(t, root)
	cfg.DownloadDir = filepath.Join(blocker, "nested")
	dbPath := filepath.Join(root, "downloader.db")
	if core, err := startDesktopCore(cfg, dbPath); err == nil {
		_ = core.Shutdown(5 * time.Second)
		t.Fatal("expected startup failure when the download directory cannot be created")
	}
	core, err := startDesktopCore(coreTestConfig(t, root), dbPath)
	if err != nil {
		t.Fatalf("ownership not released after failed start: %v", err)
	}
	_ = core.Shutdown(5 * time.Second)
}

// D-1 / B-1: the diagnostic smoke runs a full runtime in its own workspace, proves workers are alive, and shuts down.
func TestDiagnosticSmokeUsesIsolatedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	if err := runSmokeInWorkspace(workspace); err != nil {
		t.Fatalf("smoke: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "downloader_smoke.db")); err != nil {
		t.Fatalf("smoke database not created inside the workspace: %v", err)
	}
}
