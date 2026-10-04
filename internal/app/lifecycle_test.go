package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/runtimeguard"
)

func TestApp_RuntimeGuardContention(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "contention.db")

	ctx := context.Background()

	// 1. First App successfully acquires runtime ownership
	app1, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("first App.New failed: %v", err)
	}

	// 2. Second App targeting the SAME dbPath must fail fast with ErrRuntimeAlreadyOwned
	app2, err := New(ctx, cfg, WithDBPath(dbPath))
	if err == nil {
		_ = app2.Shutdown(ctx)
		_ = app1.Shutdown(ctx)
		t.Fatalf("expected second App.New to fail with ErrRuntimeAlreadyOwned, succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		_ = app1.Shutdown(ctx)
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got: %v", err)
	}

	// 3. Shutdown first App - must release runtime ownership
	if err := app1.Shutdown(ctx); err != nil {
		t.Fatalf("app1.Shutdown failed: %v", err)
	}

	// 4. Third App targeting the SAME dbPath must now succeed
	app3, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("expected App.New after shutdown to succeed, got: %v", err)
	}
	_ = app3.Shutdown(ctx)
}

func TestApp_DBNotCreatedOnContention(t *testing.T) {
	cfg := newTestConfig(t)
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "unborn.db")

	// Ensure database file does not exist before test
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected dbPath to not exist initially")
	}

	// Hold the runtime lock for unborn.db directly via runtimeguard
	lock, err := runtimeguard.Acquire(dbPath)
	if err != nil {
		t.Fatalf("failed to acquire mock runtime lock: %v", err)
	}
	defer lock.Close()

	// Attempt to construct App. It must fail ownership check BEFORE database.New
	ctx := context.Background()
	_, err = New(ctx, cfg, WithDBPath(dbPath))
	if err == nil {
		t.Fatalf("expected App.New to fail on contended lock, succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got: %v", err)
	}

	// Hard invariant: neither unborn.db nor WAL/SHM must exist
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Errorf("unborn.db was created on disk despite contention failure!")
	}
	if _, err := os.Stat(dbPath + "-wal"); !os.IsNotExist(err) {
		t.Errorf("unborn.db-wal was created on disk!")
	}
	if _, err := os.Stat(dbPath + "-shm"); !os.IsNotExist(err) {
		t.Errorf("unborn.db-shm was created on disk!")
	}
}

func TestApp_NoProbeOnContention(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "noprobe.db")

	// Set up a sentinel HTTP server simulating qBittorrent
	var reqCount int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg.QBitURL = ts.URL

	// Pre-lock the database path
	lock, err := runtimeguard.Acquire(dbPath)
	if err != nil {
		t.Fatalf("failed to acquire runtime lock: %v", err)
	}
	defer lock.Close()

	// Attempt App.New with the mock qB URL configured
	ctx := context.Background()
	_, err = New(ctx, cfg, WithDBPath(dbPath))
	if err == nil {
		t.Fatalf("expected App.New to fail, succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got: %v", err)
	}

	// Invariant: rejected second core must make exactly 0 HTTP requests to engine
	if count := atomic.LoadInt64(&reqCount); count != 0 {
		t.Errorf("expected 0 probe requests to qBittorrent, but saw %d", count)
	}
}

func TestApp_CleanupStackReleasesGuardOnFailure(t *testing.T) {
	cfg := newTestConfig(t)
	tempDir := t.TempDir()

	// Create a directory where the database file would be, making SQLite open fail
	badDBPath := filepath.Join(tempDir, "baddir.db")
	if err := os.MkdirAll(badDBPath, 0755); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	ctx := context.Background()
	_, err := New(ctx, cfg, WithDBPath(badDBPath))
	if err == nil {
		t.Fatalf("expected App.New to fail when dbPath is a directory, succeeded")
	}

	// Verify that cleanupStack released the runtime guard so we can re-acquire immediately
	guard, err := runtimeguard.Acquire(badDBPath)
	if err != nil {
		t.Fatalf("expected runtime lock to be released after App.New failure, got error: %v", err)
	}
	defer guard.Close()
}

func TestApp_ShutdownConcurrentIdempotent(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "idempotent.db")

	ctx := context.Background()
	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			errs[idx] = application.Shutdown(shutdownCtx)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("Shutdown goroutine %d failed: %v", i, err)
		}
	}

	// Lock must be released after concurrent shutdown
	guard, err := runtimeguard.Acquire(dbPath)
	if err != nil {
		t.Fatalf("expected lock to be available after concurrent shutdown: %v", err)
	}
	defer guard.Close()
}
