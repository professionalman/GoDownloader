package job

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrResourceCapacityExceeded is returned when the required resources cannot be admitted.
var ErrResourceCapacityExceeded = errors.New("resource capacity exceeded")

// ResourceRequirements specifies the scarce capacity needed to execute a job.
type ResourceRequirements struct {
	Engine string // "aria2", "qbittorrent", "ytdlp", etc.
}

// ResourceLease represents an acquired, tracked set of resources for a job.
type ResourceLease struct {
	JobID      string
	Engine     string
	AcquiredAt time.Time
}

// ResourceGovernorConfig defines capacity thresholds for the governor.
type ResourceGovernorConfig struct {
	GlobalTransferLimit int
	EngineLimits        map[string]int
}

const (
	DefaultGlobalTransferLimit = 3
	DefaultYtdlpEngineLimit    = 2
)

// DefaultResourceGovernorConfig returns safe, conservative baseline limits.
// Only ytdlp has an explicit engine limit (2) by default due to subprocess safety;
// qbittorrent and aria2 default to bounded by the global transfer limit unless
// explicitly configured in EngineLimits.
func DefaultResourceGovernorConfig() ResourceGovernorConfig {
	return ResourceGovernorConfig{
		GlobalTransferLimit: DefaultGlobalTransferLimit,
		EngineLimits: map[string]int{
			"ytdlp": DefaultYtdlpEngineLimit,
		},
	}
}

// ResourceGovernorSnapshot represents a point-in-time view of limits and usage.
type ResourceGovernorSnapshot struct {
	GlobalTransferLimit int            `json:"globalTransferLimit"`
	ActiveTransfers     int            `json:"activeTransfers"`
	EngineLimits        map[string]int `json:"engineLimits"`
	ActivePerEngine     map[string]int `json:"activePerEngine"`
	ActiveLeases        int            `json:"activeLeases"`
}

// ResourceGovernor manages admission of scarce runtime execution resources.
// It is fully in-memory, concurrency-safe, and reconstructible after restarts.
type ResourceGovernor struct {
	mu                  sync.Mutex
	globalLimitFn       func() int
	globalTransferLimit int
	engineLimits        map[string]int

	leases          map[string]*ResourceLease
	activeTransfers int
	activePerEngine map[string]int
}

// NewResourceGovernor creates a new ResourceGovernor instance.
// If globalLimitFn is supplied, it is invoked dynamically to read the global transfer limit
// (e.g. from settingsService.EffectiveMaxConcurrentDownloads).
func NewResourceGovernor(cfg ResourceGovernorConfig, globalLimitFn ...func() int) *ResourceGovernor {
	gLimit := cfg.GlobalTransferLimit
	if gLimit <= 0 {
		gLimit = DefaultGlobalTransferLimit
	}

	eLimits := make(map[string]int)
	if cfg.EngineLimits != nil {
		for k, v := range cfg.EngineLimits {
			if v > 0 {
				eLimits[k] = v
			}
		}
	}
	if _, ok := eLimits["ytdlp"]; !ok {
		eLimits["ytdlp"] = DefaultYtdlpEngineLimit
	}

	var fn func() int
	if len(globalLimitFn) > 0 && globalLimitFn[0] != nil {
		fn = globalLimitFn[0]
	}

	return &ResourceGovernor{
		globalLimitFn:       fn,
		globalTransferLimit: gLimit,
		engineLimits:        eLimits,
		leases:              make(map[string]*ResourceLease),
		activePerEngine:     make(map[string]int),
	}
}

func (g *ResourceGovernor) effectiveGlobalTransferLimit() int {
	if g.globalLimitFn != nil {
		val := g.globalLimitFn()
		if val > 0 {
			return val
		}
	}
	if g.globalTransferLimit > 0 {
		return g.globalTransferLimit
	}
	return DefaultGlobalTransferLimit
}

func (g *ResourceGovernor) effectiveEngineLimit(engine string) int {
	if limit, ok := g.engineLimits[engine]; ok && limit > 0 {
		return limit
	}
	// Fall back to the effective global limit if no engine-specific limit is declared
	return g.effectiveGlobalTransferLimit()
}

// CanAdmit performs a non-mutating check whether the given requirements can currently be admitted.
func (g *ResourceGovernor) CanAdmit(req ResourceRequirements) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.canAdmitLocked(req)
}

func (g *ResourceGovernor) canAdmitLocked(req ResourceRequirements) (bool, string) {
	// Normal download transfer
	globalLimit := g.effectiveGlobalTransferLimit()
	if g.activeTransfers >= globalLimit {
		return false, fmt.Sprintf("global transfer capacity full (%d/%d)", g.activeTransfers, globalLimit)
	}

	if req.Engine != "" {
		engLimit := g.effectiveEngineLimit(req.Engine)
		currentEng := g.activePerEngine[req.Engine]
		if currentEng >= engLimit {
			return false, fmt.Sprintf("engine %q capacity full (%d/%d)", req.Engine, currentEng, engLimit)
		}
	}

	return true, ""
}

// TryAcquire atomically validates and acquires transfer capacity for jobID.
// If any resource dimension is unavailable, zero capacity is allocated and an error is returned.
// Calling TryAcquire for a jobID that already holds an equivalent lease is an idempotent no-op.
func (g *ResourceGovernor) TryAcquire(jobID string, req ResourceRequirements) (*ResourceLease, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID cannot be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// Idempotency: if already holding lease for this job, return it directly
	if existing, ok := g.leases[jobID]; ok && existing != nil {
		return existing, nil
	}

	admissible, reason := g.canAdmitLocked(req)
	if !admissible {
		return nil, fmt.Errorf("%w: %s", ErrResourceCapacityExceeded, reason)
	}

	lease := &ResourceLease{
		JobID:      jobID,
		Engine:     req.Engine,
		AcquiredAt: time.Now(),
	}

	g.activeTransfers++
	if req.Engine != "" {
		g.activePerEngine[req.Engine]++
	}

	g.leases[jobID] = lease
	return lease, nil
}

// Release idempotently restores capacity held by jobID.
// It returns true if a lease was found and released, false otherwise.
func (g *ResourceGovernor) Release(jobID string) bool {
	if jobID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	lease, ok := g.leases[jobID]
	if !ok || lease == nil {
		return false
	}

	delete(g.leases, jobID)

	g.activeTransfers--
	if g.activeTransfers < 0 {
		g.activeTransfers = 0
	}
	if lease.Engine != "" {
		g.activePerEngine[lease.Engine]--
		if g.activePerEngine[lease.Engine] < 0 {
			g.activePerEngine[lease.Engine] = 0
		}
	}

	return true
}

// ReconstructJob records an active job into governor accounting during startup recovery.
// It is idempotent and prevents double-counting by keying on Job UUID.
// Jobs in StatusDownloading and StatusProcessing are reconstructed as active transfers
// holding global transfer and per-engine permits. StatusSeeding does not consume
// transfer capacity. If recovered active transfers exceed current limits, the usage
// is recorded accurately (above limit) without rejection, blocking new admissions.
func (g *ResourceGovernor) ReconstructJob(j *Job) bool {
	if j == nil || j.ID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// If already registered, skip to prevent double-counting
	if _, ok := g.leases[j.ID]; ok {
		return false
	}

	now := time.Now()
	lease := &ResourceLease{
		JobID:      j.ID,
		Engine:     j.Engine,
		AcquiredAt: now,
	}

	switch j.Status {
	case StatusDownloading, StatusProcessing:
		g.activeTransfers++
		if j.Engine != "" {
			g.activePerEngine[j.Engine]++
		}
	case StatusSeeding:
		// StatusSeeding does not consume transfer capacity.
		// Seeding capacity is an explicitly deferred hard admission dimension under YAGNI.
		return false
	default:
		// Not in a resource-consuming state
		return false
	}

	g.leases[j.ID] = lease
	return true
}

// GetLease returns a copy of the lease for jobID, or nil if not held.
func (g *ResourceGovernor) GetLease(jobID string) *ResourceLease {
	g.mu.Lock()
	defer g.mu.Unlock()

	l, ok := g.leases[jobID]
	if !ok || l == nil {
		return nil
	}
	cp := *l
	return &cp
}

// Snapshot returns a point-in-time immutable copy of limits and active usages.
func (g *ResourceGovernor) Snapshot() ResourceGovernorSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()

	engLimits := make(map[string]int, len(g.engineLimits))
	for k, v := range g.engineLimits {
		engLimits[k] = v
	}

	activeEng := make(map[string]int, len(g.activePerEngine))
	for k, v := range g.activePerEngine {
		activeEng[k] = v
	}

	return ResourceGovernorSnapshot{
		GlobalTransferLimit: g.effectiveGlobalTransferLimit(),
		ActiveTransfers:     g.activeTransfers,
		EngineLimits:        engLimits,
		ActivePerEngine:     activeEng,
		ActiveLeases:        len(g.leases),
	}
}

// SetGlobalTransferLimit dynamically updates the fallback global limit.
func (g *ResourceGovernor) SetGlobalTransferLimit(limit int) {
	if limit <= 0 {
		limit = DefaultGlobalTransferLimit
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.globalTransferLimit = limit
}

// SetEngineLimit dynamically updates or configures a per-engine limit.
func (g *ResourceGovernor) SetEngineLimit(engine string, limit int) {
	if engine == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if limit > 0 {
		g.engineLimits[engine] = limit
	} else {
		delete(g.engineLimits, engine)
	}
}
