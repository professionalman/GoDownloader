package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"downloader/internal/process"
)

// TestShutdownSleeperHelperProcess is a supervised child for the shutdown tests: it sleeps until killed.
func TestShutdownSleeperHelperProcess(t *testing.T) {
	if os.Getenv("GO_SHUTDOWN_SLEEPER") != "1" {
		return
	}
	time.Sleep(2 * time.Minute)
	os.Exit(0)
}

func startedApp(t *testing.T, dbPath string) *App {
	t.Helper()
	cfg := newTestConfig(t)
	a, err := New(context.Background(), cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return a
}

// startOwnedSleeper starts a child through the app's process supervisor (as engines do) and returns its process.
func startOwnedSleeper(t *testing.T, a *App) *process.ManagedProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestShutdownSleeperHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_SHUTDOWN_SLEEPER=1")
	proc, err := a.processSupervisor.StartOwned(context.Background(), cmd, process.ProcessSpec{ID: "shutdown-sleeper", Tool: "test", Purpose: "shutdown-test"})
	if err != nil {
		t.Fatalf("start owned child: %v", err)
	}
	t.Cleanup(func() { _ = proc.Terminate() })
	return proc
}

func waitExited(t *testing.T, proc *process.ManagedProcess, within time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() { _ = proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(within):
		t.Fatal("owned child process still running after shutdown")
	}
}

// Normal shutdown completes well within its deadline with no error.
func TestShutdown_NormalCompletesWithinDeadline(t *testing.T) {
	a := startedApp(t, filepath.Join(t.TempDir(), "normal.db"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if err := a.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("normal shutdown took %v", d)
	}
}

// Workers already stopped before Shutdown: stopping them again must be harmless (idempotent stop, no panic).
func TestShutdown_WorkersAlreadyStopped(t *testing.T) {
	a := startedApp(t, filepath.Join(t.TempDir(), "prestopped.db"))
	a.Manager().Stop()
	if err := a.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after an explicit stop: %v", err)
	}
}

// A worker that never exits cannot hold shutdown hostage: Shutdown returns after its deadline plus the bounded
// cleanup grace, reports ErrShutdownIncomplete, still terminates owned process trees, closes the database and
// releases the runtime ownership lock so the application can be relaunched on the same database.
func TestShutdown_StuckWorkerIsBounded(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "stuck.db")
	a := startedApp(t, dbPath)
	child := startOwnedSleeper(t, a)

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a.stopManager = func() { <-release } // simulates a background worker blocked in an engine call

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := a.Shutdown(ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}
	if elapsed > 300*time.Millisecond+shutdownCleanupGrace+2*time.Second {
		t.Fatalf("shutdown with a stuck worker took %v", elapsed)
	}
	waitExited(t, child, 10*time.Second)

	if _, readErr := a.repo.List(context.Background()); readErr == nil {
		t.Fatal("database still open after bounded shutdown")
	}

	relaunched, err := New(context.Background(), newTestConfig(t), WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("relaunch after bounded shutdown: %v", err)
	}
	if err := relaunched.Start(context.Background()); err != nil {
		t.Fatalf("relaunch Start: %v", err)
	}
	if err := relaunched.Shutdown(context.Background()); err != nil {
		t.Fatalf("relaunch Shutdown: %v", err)
	}
}

// Multiple Quit requests with a stuck worker: the owner's teardown is bounded by its deadline; a concurrent caller
// joins that teardown (it never returns while teardown is still running, per the established join contract) and
// receives the same outcome; later callers get the recorded outcome without a second teardown pass.
func TestShutdown_ConcurrentAndRepeatedRequests(t *testing.T) {
	a := startedApp(t, filepath.Join(t.TempDir(), "concurrent.db"))
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	calls := 0
	var mu sync.Mutex
	a.stopManager = func() {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
	}

	ownerDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		ownerDone <- a.Shutdown(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		a.mu.Lock()
		stopping := a.stopping
		a.mu.Unlock()
		if stopping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner shutdown did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}

	joinerDone := make(chan error, 1)
	go func() { joinerDone <- a.Shutdown(context.Background()) }()

	bound := 300*time.Millisecond + shutdownCleanupGrace + 5*time.Second
	var ownerErr, joinerErr error
	select {
	case ownerErr = <-ownerDone:
	case <-time.After(bound):
		t.Fatal("owner shutdown not bounded by its deadline")
	}
	select {
	case joinerErr = <-joinerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("joiner did not return after the owner's teardown completed")
	}
	if !errors.Is(ownerErr, ErrShutdownIncomplete) || !errors.Is(joinerErr, ErrShutdownIncomplete) {
		t.Fatalf("owner=%v joiner=%v; both must report the incomplete graceful phase", ownerErr, joinerErr)
	}
	if err := a.Shutdown(context.Background()); !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("repeated shutdown must return the recorded outcome, got %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("worker stop ran %d times, want 1", calls)
	}
}

// A worker stuck past the deadline must not keep the others alive: Shutdown signals every background loop before it
// starts the bounded wait, so the scheduler, monitor, reconciler and tracker all stop even though the wait for the
// stuck worker is abandoned (finding #1 of the acceptance review).
func TestShutdown_StuckWaitStillStopsOtherWorkers(t *testing.T) {
	a := startedApp(t, filepath.Join(t.TempDir(), "signal.db"))
	deadline := time.Now().Add(5 * time.Second)
	for !func() bool { w := a.BackgroundWorkers(); return w.Scheduler && w.Monitor && w.Reconciler && w.Tracker }() {
		if time.Now().After(deadline) {
			t.Fatalf("workers did not start: %+v", a.BackgroundWorkers())
		}
		time.Sleep(5 * time.Millisecond)
	}

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	a.stopManager = func() { <-release } // the wait for workers never completes

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := a.Shutdown(ctx); !errors.Is(err, ErrShutdownIncomplete) {
		t.Fatalf("expected ErrShutdownIncomplete, got %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		w := a.BackgroundWorkers()
		if !w.Scheduler && !w.Monitor && !w.Reconciler && !w.Tracker {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("background loops still running after a bounded shutdown: %+v", w)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An already-exhausted caller deadline (for example consumed by the tracker drain, or a pre-cancelled context) still
// gives the worker wait a short grace, so a manager that stops promptly is not abandoned (finding #2).
func TestShutdown_ExhaustedDeadlineStillWaitsBriefly(t *testing.T) {
	a := startedApp(t, filepath.Join(t.TempDir(), "exhausted.db"))
	stoppedCleanly := make(chan struct{})
	a.stopManager = func() {
		time.Sleep(50 * time.Millisecond)
		a.Manager().Stop()
		close(stoppedCleanly)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = a.Shutdown(ctx) // the tracker drain reports the cancelled ctx; the manager wait must not be skipped
	select {
	case <-stoppedCleanly:
	default:
		t.Fatal("manager stop was abandoned without any grace although it finished within milliseconds")
	}
}
