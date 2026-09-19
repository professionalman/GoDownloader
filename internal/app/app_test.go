package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/events"
	"downloader/internal/job"
)

func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	tempDir := t.TempDir()
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(tempDir, "downloads")
	cfg.DataDir = filepath.Join(tempDir, "data")
	cfg.ListenAddr = "127.0.0.1:0"
	return cfg
}

func TestApp_New_Success(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() {
		_ = application.Shutdown(context.Background())
	}()

	// Verify required public accessors
	if application.Manager() == nil {
		t.Error("expected non-nil Manager")
	}
	if application.Settings() == nil {
		t.Error("expected non-nil Settings")
	}
	if application.Categories() == nil {
		t.Error("expected non-nil Categories")
	}
	if application.Tracker() == nil {
		t.Error("expected non-nil Tracker")
	}
	if application.MediaAuth() == nil {
		t.Error("expected non-nil MediaAuth")
	}
	if application.EventBus() == nil {
		t.Error("expected non-nil EventBus")
	}

	// Verify internally owned subsystems
	if application.db == nil {
		t.Error("expected non-nil db")
	}
	if application.storageService == nil {
		t.Error("expected non-nil storageService")
	}
	if application.toolMgr == nil {
		t.Error("expected non-nil toolMgr")
	}
	if application.processSupervisor == nil {
		t.Error("expected non-nil processSupervisor")
	}
	if application.resourceGovernor == nil {
		t.Error("expected non-nil resourceGovernor")
	}
	if application.scheduler == nil {
		t.Error("expected non-nil scheduler")
	}

	// Verify initial lifecycle flags
	application.mu.Lock()
	started := application.started
	stopped := application.stopped
	application.mu.Unlock()

	if started {
		t.Error("expected started to be false before Start()")
	}
	if stopped {
		t.Error("expected stopped to be false before Shutdown()")
	}
}

func TestApp_New_NilConfig(t *testing.T) {
	_, err := New(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error when config is nil, got nil")
	}
}

func TestApp_New_InvalidDBPath_Cleanup(t *testing.T) {
	cfg := newTestConfig(t)
	// Passing an invalid DB path where a directory cannot be created
	invalidPath := filepath.Join("Z:\\nonexistent_drive_mount_root", "invalid.db")

	_, err := New(context.Background(), cfg, WithDBPath(invalidPath))
	if err == nil {
		t.Fatal("expected error with invalid DB path, got nil")
	}
}

func TestApp_Start_Lifecycle(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_start.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() {
		_ = application.Shutdown(context.Background())
	}()

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	application.mu.Lock()
	started := application.started
	application.mu.Unlock()

	if !started {
		t.Error("expected started to be true after Start()")
	}

	// Calling Start a second time must fail
	if err := application.Start(ctx); err == nil {
		t.Error("expected error on duplicate Start(), got nil")
	}
}

func TestApp_Start_After_Shutdown_Fails(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_start_stopped.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if err := application.Start(ctx); err == nil {
		t.Error("expected error starting stopped application runtime, got nil")
	}
}

func TestApp_Shutdown_Idempotent(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_idempotent.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	// First call
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("first Shutdown failed: %v", err)
	}

	application.mu.Lock()
	stopped := application.stopped
	application.mu.Unlock()

	if !stopped {
		t.Error("expected stopped to be true after Shutdown()")
	}

	// Second call
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Errorf("second Shutdown failed: %v", err)
	}

	// Concurrent calls
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := application.Shutdown(shutdownCtx); err != nil {
				t.Errorf("concurrent Shutdown failed: %v", err)
			}
		}()
	}
	wg.Wait()
}

func TestApp_Shutdown_Ordering_DBClosedAfterManagerStop(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_order.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Before shutdown, DB must be open and responsive
	if err := application.db.Conn().Ping(); err != nil {
		t.Fatalf("DB ping failed before shutdown: %v", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// After shutdown, DB connection must be closed
	if err := application.db.Conn().Ping(); err == nil {
		t.Error("expected DB ping to fail after Shutdown, but it succeeded (connection not closed)")
	}
}

func TestApp_Shutdown_BeforeStart(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_shutdown_before_start.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Ping DB before shutdown
	if err := application.db.Conn().Ping(); err != nil {
		t.Fatalf("DB ping failed before shutdown: %v", err)
	}

	// Shutdown without calling Start
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown before Start failed: %v", err)
	}

	// Verify DB is closed
	if err := application.db.Conn().Ping(); err == nil {
		t.Error("expected DB ping to fail after shutdown-before-start, but it succeeded")
	}

	// Subsequent Start must fail
	if err := application.Start(ctx); err == nil {
		t.Error("expected Start to fail after shutdown-before-start, got nil")
	}
}

func TestApp_Shutdown_CancelledContext(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_shutdown_cancelled.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Pass pre-cancelled context to Shutdown
	cancelledCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn() // Cancel immediately

	_ = application.Shutdown(cancelledCtx)

	// Invariant: DB must still be closed even if context was cancelled
	if err := application.db.Conn().Ping(); err == nil {
		t.Error("expected DB ping to fail after shutdown with cancelled context")
	}

	application.mu.Lock()
	stopped := application.stopped
	application.mu.Unlock()

	if !stopped {
		t.Error("expected stopped to be true after Shutdown with cancelled context")
	}
}

func TestApp_Shutdown_Concurrent_Waiting(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_concurrent_waiting.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	const callers = 8
	errs := make([]error, callers)
	var wg sync.WaitGroup

	for i := 0; i < callers; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			errs[idx] = application.Shutdown(shutdownCtx)
		}()
	}
	wg.Wait()

	// All concurrent callers must succeed without error
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d received error from Shutdown: %v", i, err)
		}
	}

	if err := application.db.Conn().Ping(); err == nil {
		t.Error("expected DB ping to fail after concurrent shutdown")
	}
}

func TestApp_Shutdown_Concurrent_Join_IgnoresContextCancellation(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_concurrent_join.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Controlled teardown hold point: replace trackerDone with an unclosed channel
	holdCh := make(chan struct{})
	application.mu.Lock()
	application.trackerDone = holdCh
	application.mu.Unlock()

	// 1. Caller A begins Shutdown (owns teardown)
	callerADone := make(chan struct{})
	var errA error
	go func() {
		defer close(callerADone)
		errA = application.Shutdown(context.Background())
	}()

	// Wait until application has transitioned to stopping state
	for {
		application.mu.Lock()
		stopping := application.stopping
		application.mu.Unlock()
		if stopping {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// 2. Caller B calls Shutdown with an ALREADY-CANCELLED context (joiner)
	cancelledCtx, cancelFn := context.WithCancel(context.Background())
	cancelFn() // Pre-cancelled!

	callerBDone := make(chan struct{})
	var errB error
	go func() {
		defer close(callerBDone)
		errB = application.Shutdown(cancelledCtx)
	}()

	// Deterministic proof: Caller B MUST NOT return while Caller A is held
	time.Sleep(30 * time.Millisecond)
	select {
	case <-callerBDone:
		t.Fatal("Caller B returned prematurely while shared teardown was still running!")
	default:
		// Caller B is correctly blocked waiting for Caller A to finish
	}

	// 3. Unblock Caller A to complete teardown
	close(holdCh)

	// Both callers must now finish cleanly
	select {
	case <-callerADone:
	case <-time.After(5 * time.Second):
		t.Fatal("Caller A timed out")
	}

	select {
	case <-callerBDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Caller B timed out")
	}

	// Both callers must observe the identical shutdown outcome (nil error)
	if errA != nil {
		t.Errorf("Caller A returned error: %v", errA)
	}
	if errB != nil {
		t.Errorf("Caller B returned error: %v", errB)
	}

	// DB must be closed
	if err := application.db.Conn().Ping(); err == nil {
		t.Error("expected DB ping to fail after shutdown, but it succeeded")
	}
}

func TestApp_EventBus_ManagerIntegration(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_bus_manager.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() {
		_ = application.Shutdown(context.Background())
	}()

	bus := application.EventBus()
	if bus == nil {
		t.Fatal("expected non-nil event bus")
	}

	// Verify identity: bus returned is exactly the application's internal bus
	if bus != application.bus {
		t.Error("expected EventBus() to return application.bus instance")
	}

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	// Publish test event
	bus.Publish(job.Event{
		Type: job.EventJobCreated,
		Data: "test-integration-payload",
	})

	select {
	case evt := <-sub:
		if evt.Type != job.EventJobCreated {
			t.Errorf("expected EventJobCreated, got %v", evt.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event on shared event bus")
	}
}

func TestServer_EndToEnd_Smoke(t *testing.T) {
	cfg := newTestConfig(t)
	dbPath := filepath.Join(t.TempDir(), "test_smoke.db")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	if err := application.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wire HTTP router using identical composition as cmd/server/main.go
	sseHandler := events.NewSSEHandler(application.EventBus())
	sseHandler.SetCursorProvider(application.Manager())

	secManager := api.NewSecurityManager(cfg)
	router := api.NewRouter(cfg, application.Manager(), sseHandler, application.Settings(), secManager, application.Categories(), application.Tracker(), application.MediaAuth())

	ts := httptest.NewServer(router)
	defer ts.Close()

	// 1. Verify GET /api/v1/auth/session succeeds (bootstraps session and returns 200)
	req, err := http.NewRequest("GET", ts.URL+"/api/v1/auth/session", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/auth/session failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200 from /api/v1/auth/session, got %d", resp.StatusCode)
	}

	// 2. Verify clean shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := application.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
}
