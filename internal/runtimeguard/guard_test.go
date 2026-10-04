package runtimeguard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGuard_AcquireAndRelease(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	guard, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("expected Acquire to succeed, got %v", err)
	}

	expectedLockPath := filepath.Join(tmpDir, "test.db.runtime.lock")
	if guard.LockPath() != expectedLockPath {
		t.Errorf("expected lockPath %q, got %q", expectedLockPath, guard.LockPath())
	}

	// Verify lock file exists on disk
	if _, err := os.Stat(expectedLockPath); err != nil {
		t.Fatalf("expected lock file to exist on disk: %v", err)
	}

	if err := guard.Close(); err != nil {
		t.Fatalf("expected Close to succeed, got %v", err)
	}

	// Lock file remains on disk (non-deletion contract)
	if _, err := os.Stat(expectedLockPath); err != nil {
		t.Fatalf("expected lock file to remain on disk after close: %v", err)
	}

	// Close must be idempotent
	if err := guard.Close(); err != nil {
		t.Fatalf("expected subsequent Close to succeed, got %v", err)
	}
}

func TestGuard_ContentionSamePath(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	guard1, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	defer guard1.Close()

	// Second acquire on same DB path must fail with ErrRuntimeAlreadyOwned
	guard2, err := Acquire(dbPath)
	if err == nil {
		guard2.Close()
		t.Fatalf("expected second Acquire to fail with ErrRuntimeAlreadyOwned, succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got %v", err)
	}
}

func TestGuard_DifferentPaths(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath1 := filepath.Join(tmpDir, "db1.db")
	dbPath2 := filepath.Join(tmpDir, "db2.db")

	guard1, err := Acquire(dbPath1)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}
	defer guard1.Close()

	guard2, err := Acquire(dbPath2)
	if err != nil {
		t.Fatalf("second Acquire on different path failed: %v", err)
	}
	defer guard2.Close()
}

func TestGuard_ReacquireAfterRelease(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	guard1, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("first Acquire failed: %v", err)
	}

	if err := guard1.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	guard2, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	defer guard2.Close()
}

func TestGuard_StaleLockFileHarmless(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	lockPath := dbPath + ".runtime.lock"

	// Create an un-held stale lock file on disk
	if err := os.WriteFile(lockPath, []byte("stale-lock-data"), 0644); err != nil {
		t.Fatalf("failed to create stale lock file: %v", err)
	}

	guard, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("expected Acquire over stale file to succeed, got %v", err)
	}
	defer guard.Close()
}

func TestGuard_RelativeAndAbsoluteEquivalentCollision(t *testing.T) {
	tmpDir := t.TempDir()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get wd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()

	relPath := "test.db"
	absPath := filepath.Join(tmpDir, "test.db")

	guard1, err := Acquire(relPath)
	if err != nil {
		t.Fatalf("Acquire(relPath) failed: %v", err)
	}
	defer guard1.Close()

	guard2, err := Acquire(absPath)
	if err == nil {
		guard2.Close()
		t.Fatalf("expected Acquire(absPath) to collide with relative path, succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got %v", err)
	}
}

// TestCrashHelper is invoked by TestGuard_CrashRelease in a child process
func TestCrashHelper(t *testing.T) {
	if os.Getenv("GO_RUNTIMEGUARD_CRASH_HELPER") != "1" {
		return
	}
	dbPath := os.Getenv("GO_RUNTIMEGUARD_CRASH_DB")
	readyFile := os.Getenv("GO_RUNTIMEGUARD_READY_FILE")

	guard, err := Acquire(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ACQUIRE_ERROR: %v\n", err)
		os.Exit(2)
	}
	// Signal parent that lock is held
	_ = os.WriteFile(readyFile, []byte("READY"), 0644)

	_ = guard // keep alive
	// Block until parent terminates this process abruptly
	for {
		time.Sleep(100 * time.Millisecond)
	}
}

func TestGuard_CrashRelease(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "crash_test.db")
	readyFile := filepath.Join(tmpDir, "ready.txt")

	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(),
		"GO_RUNTIMEGUARD_CRASH_HELPER=1",
		"GO_RUNTIMEGUARD_CRASH_DB="+dbPath,
		"GO_RUNTIMEGUARD_READY_FILE="+readyFile,
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start helper process: %v", err)
	}

	// Wait for helper process to acquire lock
	deadline := time.Now().Add(5 * time.Second)
	acquired := false
	for time.Now().Before(deadline) {
		if _, err := os.Stat(readyFile); err == nil {
			acquired = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !acquired {
		_ = cmd.Process.Kill()
		t.Fatalf("helper process did not acquire lock within timeout")
	}

	// Verify that parent cannot acquire the lock while child is alive
	_, err := Acquire(dbPath)
	if err == nil {
		_ = cmd.Process.Kill()
		t.Fatalf("expected parent Acquire to fail while child holds lock, but it succeeded")
	}
	if !errors.Is(err, ErrRuntimeAlreadyOwned) {
		_ = cmd.Process.Kill()
		t.Fatalf("expected ErrRuntimeAlreadyOwned, got %v", err)
	}

	// Now abruptly kill child process (crash simulation, no graceful close)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill helper process: %v", err)
	}
	_ = cmd.Wait()

	// Child is dead. Lock file still exists on disk.
	// OS kernel must have released ownership automatically.
	guard, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("expected Acquire after crash to succeed, got %v", err)
	}
	defer guard.Close()
}
