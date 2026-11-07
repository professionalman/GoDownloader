package ytdlp

import (
	"errors"
	"sync"
)

var (
	// ErrExclusiveMaintenance is returned when execution is attempted while exclusive maintenance is active.
	ErrExclusiveMaintenance = errors.New("tool is undergoing exclusive maintenance")

	// ErrToolInUse is returned when exclusive maintenance is attempted while tool executions are active.
	ErrToolInUse = errors.New("tool is currently in use")
)

// UsageGate coordinates shared execution (downloads, analyses) and exclusive maintenance.
type UsageGate struct {
	mu        sync.Mutex
	activeUse int
	exclusive bool
}

// NewUsageGate creates a new UsageGate instance.
func NewUsageGate() *UsageGate {
	return &UsageGate{}
}

// AcquireUse atomically registers a shared usage of yt-dlp.
// It returns an idempotent release function that must be called when execution terminates.
// If exclusive maintenance is currently held, AcquireUse returns ErrExclusiveMaintenance.
func (g *UsageGate) AcquireUse() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.exclusive {
		return nil, ErrExclusiveMaintenance
	}

	g.activeUse++
	var once sync.Once
	release := func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.activeUse--
			if g.activeUse < 0 {
				g.activeUse = 0
			}
		})
	}
	return release, nil
}

// TryExclusive attempts to acquire exclusive maintenance access to yt-dlp.
// It returns an idempotent release function to reopen the gate after maintenance.
// If active usages exist or exclusive access is already held, it returns ErrToolInUse.
func (g *UsageGate) TryExclusive() (func(), error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.exclusive || g.activeUse > 0 {
		return nil, ErrToolInUse
	}

	g.exclusive = true
	var once sync.Once
	release := func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			g.exclusive = false
		})
	}
	return release, nil
}

// InUse returns true if there are any active usages running.
func (g *UsageGate) InUse() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.activeUse > 0 || g.exclusive
}

// IsExclusive returns true if exclusive maintenance is held.
func (g *UsageGate) IsExclusive() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.exclusive
}
