package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/mediaauth"
	"downloader/internal/runtimeguard"
	"downloader/internal/storage"
)

// D-1: a second runtime that shares the data directory but uses a different database (the old diagnostic smoke
// shape) cannot prove ownership of in-progress work in that directory. Its startup cleanup must not delete the
// first runtime's marked work directories or ephemeral auth exports.
func TestSecondRuntimeOnSharedDataDirPreservesOwnersTempFiles(t *testing.T) {
	cfg := newTestConfig(t)
	root := t.TempDir()

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	owner, err := New(ownerCtx, cfg, WithDBPath(filepath.Join(root, "owner.db")))
	if err != nil {
		t.Fatalf("owner New: %v", err)
	}
	defer owner.Shutdown(context.Background())
	if err := owner.Start(ownerCtx); err != nil {
		t.Fatalf("owner Start: %v", err)
	}

	// In-progress state owned by the first runtime: a marked work directory for a job only its database knows,
	// and a live cookie export.
	workDir := filepath.Join(cfg.DataDir, "tmp", "job-owned-by-main")
	if err := storage.WriteWorkDirMarker(workDir, "job-owned-by-main"); err != nil {
		t.Fatalf("marker: %v", err)
	}
	partial := filepath.Join(workDir, "video.part")
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(cfg.DataDir, "tmp", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cookie := filepath.Join(authDir, mediaauth.TempCookiePrefix+"inuse"+mediaauth.TempCookieSuffix)
	if err := os.WriteFile(cookie, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Second runtime: different database, same data directory.
	secondCfg := *cfg
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	second, err := New(secondCtx, &secondCfg, WithDBPath(filepath.Join(root, "second.db")))
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if err := second.Start(secondCtx); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := second.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}

	for _, p := range []string{workDir, partial, cookie} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("second runtime deleted the owner's in-progress file %s: %v", filepath.Base(p), err)
		}
	}
}

// D-1 complement: once no other runtime holds the data directory, startup cleanup still removes stale, unowned
// work directories and cookie exports (the gating must not disable cleanup permanently).
func TestSoleRuntimeStillCleansStaleTempFiles(t *testing.T) {
	cfg := newTestConfig(t)
	root := t.TempDir()

	stale := filepath.Join(cfg.DataDir, "tmp", "job-stale")
	if err := storage.WriteWorkDirMarker(stale, "job-stale"); err != nil {
		t.Fatalf("marker: %v", err)
	}
	authDir := filepath.Join(cfg.DataDir, "tmp", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cookie := filepath.Join(authDir, mediaauth.TempCookiePrefix+"stale"+mediaauth.TempCookieSuffix)
	if err := os.WriteFile(cookie, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := New(ctx, cfg, WithDBPath(filepath.Join(root, "only.db")))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := a.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, p := range []string{stale, cookie} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("stale %s not cleaned by the owning runtime (err=%v)", filepath.Base(p), err)
		}
	}
}

// D-1: the cleanup-ownership lock is released on shutdown, so a later runtime on the same data directory becomes
// the cleanup owner again.
func TestCleanupOwnershipReleasedOnShutdown(t *testing.T) {
	cfg := newTestConfig(t)
	root := t.TempDir()
	ctx := context.Background()

	first, err := New(ctx, cfg, WithDBPath(filepath.Join(root, "a.db")))
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if first.cleanupOwner == nil {
		t.Fatal("sole runtime must own cleanup of its data directory")
	}
	second, err := New(ctx, cfg, WithDBPath(filepath.Join(root, "b.db")))
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	if second.cleanupOwner != nil {
		t.Fatal("concurrent runtime on the same data directory must not own cleanup")
	}
	_ = second.Shutdown(ctx)
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	third, err := New(ctx, cfg, WithDBPath(filepath.Join(root, "c.db")))
	if err != nil {
		t.Fatalf("third New: %v", err)
	}
	defer third.Shutdown(ctx)
	if third.cleanupOwner == nil {
		t.Fatal("cleanup ownership not released by shutdown")
	}
}

// TestCleanupOwnerHelperProcess is run by TestCleanupLockExcludesOtherProcess in a child process. It holds the
// data-directory cleanup lock until the parent creates the release file (bounded so it can never linger).
func TestCleanupOwnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_CLEANUP_OWNER_HELPER") != "1" {
		return
	}
	dataDir := os.Getenv("GO_CLEANUP_OWNER_DATADIR")
	guard, err := runtimeguard.Acquire(filepath.Join(dataDir, "cleanup-owner"))
	if err != nil {
		os.Exit(2)
	}
	defer guard.Close()
	_ = os.WriteFile(filepath.Join(dataDir, "..", "helper.ready"), []byte("1"), 0o600)
	for i := 0; i < 600; i++ {
		if _, err := os.Stat(filepath.Join(dataDir, "..", "helper.release")); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// D-1 cross-process: while another process holds the data-directory cleanup lock, a runtime started in this process
// must not run destructive startup cleanup; once the other process exits, ownership (and cleanup) is regained.
func TestCleanupLockExcludesOtherProcess(t *testing.T) {
	cfg := newTestConfig(t)
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(cfg.DataDir)

	helper := exec.Command(os.Args[0], "-test.run=^TestCleanupOwnerHelperProcess$")
	helper.Env = append(os.Environ(), "GO_CLEANUP_OWNER_HELPER=1", "GO_CLEANUP_OWNER_DATADIR="+cfg.DataDir)
	if err := helper.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	helperDone := make(chan error, 1)
	go func() { helperDone <- helper.Wait() }()
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(parent, "helper.release"), []byte("1"), 0o600)
		select {
		case <-helperDone:
		case <-time.After(10 * time.Second):
			_ = helper.Process.Kill() // our own child only
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(parent, "helper.ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not acquire the cleanup lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	stale := filepath.Join(cfg.DataDir, "tmp", "job-not-in-this-db")
	if err := storage.WriteWorkDirMarker(stale, "job-not-in-this-db"); err != nil {
		t.Fatal(err)
	}
	authDir := filepath.Join(cfg.DataDir, "tmp", "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cookie := filepath.Join(authDir, mediaauth.TempCookiePrefix+"other-process"+mediaauth.TempCookieSuffix)
	if err := os.WriteFile(cookie, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	runOnce := func(db string) *App {
		t.Helper()
		ctx := context.Background()
		a, err := New(ctx, cfg, WithDBPath(filepath.Join(parent, db)))
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if err := a.Start(ctx); err != nil {
			t.Fatalf("Start: %v", err)
		}
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.Shutdown(sctx); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
		return a
	}

	if a := runOnce("blocked.db"); a.cleanupOwner != nil {
		t.Fatal("runtime acquired cleanup ownership while another process holds it")
	}
	for _, p := range []string{stale, cookie} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("cleanup ran without ownership and deleted %s: %v", filepath.Base(p), err)
		}
	}

	_ = os.WriteFile(filepath.Join(parent, "helper.release"), []byte("1"), 0o600)
	select {
	case err := <-helperDone:
		if err != nil {
			t.Fatalf("helper exited with error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("helper did not release the lock")
	}

	if a := runOnce("owner.db"); a.cleanupOwner == nil {
		t.Fatal("cleanup ownership not regained after the other process exited")
	}
	for _, p := range []string{stale, cookie} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("owning runtime did not clean stale %s (err=%v)", filepath.Base(p), err)
		}
	}
}
