package runtimeguard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrRuntimeAlreadyOwned is the typed sentinel error returned when another
// GoDownloader runtime already owns the durable data store.
var ErrRuntimeAlreadyOwned = errors.New("GoDownloader runtime is already using this data store")

// Guard holds exclusive ownership of the durable SQLite data store.
// Ownership is tied to an active operating system lock handle, not mere file existence.
type Guard struct {
	lockPath string
	impl     guardImpl
	closed   bool
}

// LockPath returns the derived lock file path. Used for verification and tests.
func (g *Guard) LockPath() string {
	if g == nil {
		return ""
	}
	return g.lockPath
}

// Acquire normalizes the provided database path, derives the sibling lock file path
// (<dbPath>.runtime.lock), and acquires an exclusive OS lock.
// It returns ErrRuntimeAlreadyOwned if another running process holds the lock.
func Acquire(dbPath string) (*Guard, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("database path cannot be empty")
	}

	absDBPath, err := filepath.Abs(filepath.Clean(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to normalize database path: %w", err)
	}

	lockPath := absDBPath + ".runtime.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory for runtime lock: %w", err)
	}

	impl, err := acquireLock(lockPath)
	if err != nil {
		if errors.Is(err, ErrRuntimeAlreadyOwned) {
			return nil, ErrRuntimeAlreadyOwned
		}
		return nil, fmt.Errorf("failed to acquire runtime lock: %w", err)
	}

	return &Guard{
		lockPath: lockPath,
		impl:     impl,
	}, nil
}

// Close releases the OS lock handle. It is safe and idempotent.
func (g *Guard) Close() error {
	if g == nil || g.closed {
		return nil
	}
	g.closed = true
	return g.impl.close()
}
