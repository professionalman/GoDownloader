package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrResourceCapacityExceeded is returned when the required resources cannot be admitted.
var ErrResourceCapacityExceeded = errors.New("resource capacity exceeded")

// ResourceRequirements specifies the scarce capacity needed to execute a job.
type ResourceRequirements struct {
	Engine           string // "aria2", "qbittorrent", "ytdlp", etc.
	MediaAnalysis    bool   // Acquire analysis capacity (does not acquire global transfer)
	FFmpegProcessing bool   // Acquire conservative FFmpeg processing reservation with download
}

// ResourceLease represents an acquired, tracked set of resources for a job.
type ResourceLease struct {
	JobID            string
	Engine           string
	AcquiredAt       time.Time
	GlobalTransfer   bool
	MediaAnalysis    bool
	FFmpegProcessing bool
}

// matchesRequirements checks if an existing lease matches requested requirements.
func (l *ResourceLease) matchesRequirements(req ResourceRequirements) bool {
	if req.MediaAnalysis {
		return l.MediaAnalysis && !l.GlobalTransfer && !l.FFmpegProcessing
	}
	return !l.MediaAnalysis && l.GlobalTransfer && l.Engine == req.Engine && l.FFmpegProcessing == req.FFmpegProcessing
}

// ResourceGovernorConfig defines capacity thresholds for the governor.
type ResourceGovernorConfig struct {
	GlobalTransferLimit   int
	EngineLimits          map[string]int
	MediaAnalysisLimit    int
	FFmpegProcessingLimit int
}

const (
	DefaultGlobalTransferLimit   = 3
	DefaultYtdlpEngineLimit      = 2
	DefaultMediaAnalysisLimit    = 2
	DefaultFFmpegProcessingLimit = 1
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
		MediaAnalysisLimit:    DefaultMediaAnalysisLimit,
		FFmpegProcessingLimit: DefaultFFmpegProcessingLimit,
	}
}

// ResourceGovernorSnapshot represents a point-in-time view of limits and usage.
type ResourceGovernorSnapshot struct {
	GlobalTransferLimit    int            `json:"globalTransferLimit"`
	ActiveTransfers        int            `json:"activeTransfers"`
	EngineLimits           map[string]int `json:"engineLimits"`
	ActivePerEngine        map[string]int `json:"activePerEngine"`
	MediaAnalysisLimit     int            `json:"mediaAnalysisLimit"`
	ActiveAnalysis         int            `json:"activeAnalysis"`
	FFmpegProcessingLimit  int            `json:"ffmpegProcessingLimit"`
	ActiveFFmpegProcessing int            `json:"activeFFmpegProcessing"`
	ActiveLeases           int            `json:"activeLeases"`
}

type analysisWaiter struct {
	jobID string
	ready chan struct{}
	lease *ResourceLease
}

// ResourceGovernor manages admission of scarce runtime execution resources.
// It is fully in-memory, concurrency-safe, and reconstructible after restarts.
type ResourceGovernor struct {
	mu                    sync.Mutex
	globalLimitFn         func() int
	globalTransferLimit   int
	engineLimits          map[string]int
	mediaAnalysisLimit    int
	ffmpegProcessingLimit int

	leases                 map[string]*ResourceLease
	activeTransfers        int
	activePerEngine        map[string]int
	activeAnalysis         int
	activeFFmpegProcessing int

	analysisWaiters []*analysisWaiter
	closed          bool
}

// NewResourceGovernor creates a new ResourceGovernor instance.
// If globalLimitFn is supplied, it is invoked dynamically to read the global transfer limit
// (e.g. from settingsService.EffectiveMaxConcurrentDownloads).
func NewResourceGovernor(cfg ResourceGovernorConfig, globalLimitFn ...func() int) *ResourceGovernor {
	gLimit := cfg.GlobalTransferLimit
	if gLimit <= 0 {
		gLimit = DefaultGlobalTransferLimit
	}

	analysisLimit := cfg.MediaAnalysisLimit
	if analysisLimit <= 0 {
		analysisLimit = DefaultMediaAnalysisLimit
	}

	procLimit := cfg.FFmpegProcessingLimit
	if procLimit <= 0 {
		procLimit = DefaultFFmpegProcessingLimit
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
		globalLimitFn:         fn,
		globalTransferLimit:   gLimit,
		engineLimits:          eLimits,
		mediaAnalysisLimit:    analysisLimit,
		ffmpegProcessingLimit: procLimit,
		leases:                make(map[string]*ResourceLease),
		activePerEngine:       make(map[string]int),
		analysisWaiters:       make([]*analysisWaiter, 0),
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
	return g.effectiveGlobalTransferLimit()
}

func (g *ResourceGovernor) effectiveAnalysisLimit() int {
	if g.mediaAnalysisLimit > 0 {
		return g.mediaAnalysisLimit
	}
	return DefaultMediaAnalysisLimit
}

func (g *ResourceGovernor) effectiveProcessingLimit() int {
	if g.ffmpegProcessingLimit > 0 {
		return g.ffmpegProcessingLimit
	}
	return DefaultFFmpegProcessingLimit
}

// CanAdmit performs a non-mutating check whether the given requirements can currently be admitted.
func (g *ResourceGovernor) CanAdmit(req ResourceRequirements) (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.canAdmitLocked(req)
}

func (g *ResourceGovernor) canAdmitLocked(req ResourceRequirements) (bool, string) {
	if req.MediaAnalysis {
		// Non-blocking admission must not jump ahead of FIFO waiters.
		if len(g.analysisWaiters) > 0 {
			return false, fmt.Sprintf("media analysis admission queue not empty (%d waiting)", len(g.analysisWaiters))
		}
		analysisLimit := g.effectiveAnalysisLimit()
		if g.activeAnalysis >= analysisLimit {
			return false, fmt.Sprintf("media analysis capacity full (%d/%d)", g.activeAnalysis, analysisLimit)
		}
		return true, ""
	}

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

	if req.FFmpegProcessing {
		procLimit := g.effectiveProcessingLimit()
		if g.activeFFmpegProcessing >= procLimit {
			return false, fmt.Sprintf("ffmpeg processing capacity full (%d/%d)", g.activeFFmpegProcessing, procLimit)
		}
	}

	return true, ""
}

// TryAcquire atomically validates and acquires transfer capacity for jobID.
// If any resource dimension is unavailable, zero capacity is allocated and an error is returned.
// Calling TryAcquire for a jobID that already holds an equivalent lease is an idempotent no-op.
// Calling TryAcquire with different requirements returns ErrLeaseConflict.
func (g *ResourceGovernor) TryAcquire(jobID string, req ResourceRequirements) (*ResourceLease, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID cannot be empty")
	}

	if req.MediaAnalysis && req.FFmpegProcessing {
		return nil, fmt.Errorf("invalid resource requirements: cannot request MediaAnalysis and FFmpegProcessing together")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.closed {
		return nil, ErrGovernorClosed
	}

	// Check existing lease for this jobID
	if existing, ok := g.leases[jobID]; ok && existing != nil {
		if existing.matchesRequirements(req) {
			return existing, nil
		}
		return nil, ErrLeaseConflict
	}

	admissible, reason := g.canAdmitLocked(req)
	if !admissible {
		return nil, fmt.Errorf("%w: %s", ErrResourceCapacityExceeded, reason)
	}

	lease := &ResourceLease{
		JobID:            jobID,
		Engine:           req.Engine,
		AcquiredAt:       time.Now(),
		GlobalTransfer:   !req.MediaAnalysis,
		MediaAnalysis:    req.MediaAnalysis,
		FFmpegProcessing: req.FFmpegProcessing,
	}

	if req.MediaAnalysis {
		g.activeAnalysis++
	} else {
		g.activeTransfers++
		if req.Engine != "" {
			g.activePerEngine[req.Engine]++
		}
		if req.FFmpegProcessing {
			g.activeFFmpegProcessing++
		}
	}

	g.leases[jobID] = lease
	return lease, nil
}

// WaitAcquireAnalysis acquires a media analysis slot for jobID, waiting in FIFO order if capacity is full.
func (g *ResourceGovernor) WaitAcquireAnalysis(ctx context.Context, jobID string) (*ResourceLease, error) {
	if jobID == "" {
		return nil, fmt.Errorf("jobID cannot be empty")
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil, ErrGovernorClosed
	}

	if err := ctx.Err(); err != nil {
		g.mu.Unlock()
		return nil, err
	}

	// Check if already holding a lease
	if existing, ok := g.leases[jobID]; ok && existing != nil {
		if existing.MediaAnalysis {
			g.mu.Unlock()
			return existing, nil
		}
		g.mu.Unlock()
		return nil, ErrLeaseConflict
	}

	// Check if already waiting
	for _, w := range g.analysisWaiters {
		if w.jobID == jobID {
			g.mu.Unlock()
			return nil, fmt.Errorf("job %s is already waiting for analysis admission", jobID)
		}
	}

	// Immediate admission if no waiters and capacity available
	if len(g.analysisWaiters) == 0 && g.activeAnalysis < g.effectiveAnalysisLimit() {
		lease := &ResourceLease{
			JobID:         jobID,
			Engine:        "ytdlp",
			AcquiredAt:    time.Now(),
			MediaAnalysis: true,
		}
		g.activeAnalysis++
		g.leases[jobID] = lease
		g.mu.Unlock()
		return lease, nil
	}

	waiter := &analysisWaiter{
		jobID: jobID,
		ready: make(chan struct{}),
	}
	g.analysisWaiters = append(g.analysisWaiters, waiter)
	g.mu.Unlock()

	select {
	case <-ctx.Done():
		g.mu.Lock()
		select {
		case <-waiter.ready:
			// Lease was already assigned before lock was acquired; release only that exact lease.
			if waiter.lease != nil && g.leases[waiter.jobID] == waiter.lease {
				g.releaseLocked(waiter.jobID)
			}
			g.mu.Unlock()
			return nil, ctx.Err()
		default:
			// Remove from waiter queue
			for i, w := range g.analysisWaiters {
				if w == waiter {
					g.analysisWaiters = append(g.analysisWaiters[:i], g.analysisWaiters[i+1:]...)
					break
				}
			}
			g.mu.Unlock()
			return nil, ctx.Err()
		}

	case <-waiter.ready:
		if waiter.lease == nil {
			return nil, ErrGovernorClosed
		}
		if err := ctx.Err(); err != nil {
			g.ReleaseLease(waiter.lease)
			return nil, err
		}
		return waiter.lease, nil
	}
}

func (g *ResourceGovernor) wakeNextAnalysisWaiterLocked() {
	if g.closed {
		return
	}
	for len(g.analysisWaiters) > 0 && g.activeAnalysis < g.effectiveAnalysisLimit() {
		w := g.analysisWaiters[0]
		g.analysisWaiters = g.analysisWaiters[1:]

		lease := &ResourceLease{
			JobID:         w.jobID,
			Engine:        "ytdlp",
			AcquiredAt:    time.Now(),
			MediaAnalysis: true,
		}
		g.activeAnalysis++
		g.leases[w.jobID] = lease
		w.lease = lease
		close(w.ready)
	}
}

// Stop closes the governor and unparks any waiting analysis callers.
func (g *ResourceGovernor) Stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	for _, w := range g.analysisWaiters {
		close(w.ready)
	}
	g.analysisWaiters = nil
}

// Release idempotently restores capacity held by jobID.
// It returns true if a lease was found and released, false otherwise.
func (g *ResourceGovernor) Release(jobID string) bool {
	if jobID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	return g.releaseLocked(jobID)
}

// ReleaseLease releases the capacity held by lease only if the lease matches the currently held lease for that JobID.
// This prevents older workers from releasing a lease that was subsequently acquired by a newer worker for the same JobID.
func (g *ResourceGovernor) ReleaseLease(lease *ResourceLease) bool {
	if lease == nil || lease.JobID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	current, ok := g.leases[lease.JobID]
	if !ok || current != lease {
		return false
	}

	return g.releaseLocked(lease.JobID)
}

// ReleaseTransfer releases jobID's lease only if it is not a media analysis lease.
// Analysis leases are owned by the analysis worker and released solely via ReleaseLease once the
// probe subprocess has terminated, so generic terminal cleanup cannot free a permit still in use.
func (g *ResourceGovernor) ReleaseTransfer(jobID string) bool {
	if jobID == "" {
		return false
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if lease, ok := g.leases[jobID]; ok && lease != nil && lease.MediaAnalysis {
		return false
	}
	return g.releaseLocked(jobID)
}

func (g *ResourceGovernor) releaseLocked(jobID string) bool {
	lease, ok := g.leases[jobID]
	if !ok || lease == nil {
		return false
	}

	delete(g.leases, jobID)

	if lease.MediaAnalysis {
		g.activeAnalysis--
		if g.activeAnalysis < 0 {
			g.activeAnalysis = 0
		}
		g.wakeNextAnalysisWaiterLocked()
	} else {
		if lease.GlobalTransfer {
			g.activeTransfers--
			if g.activeTransfers < 0 {
				g.activeTransfers = 0
			}
		}
		if lease.Engine != "" {
			g.activePerEngine[lease.Engine]--
			if g.activePerEngine[lease.Engine] < 0 {
				g.activePerEngine[lease.Engine] = 0
			}
		}
		if lease.FFmpegProcessing {
			g.activeFFmpegProcessing--
			if g.activeFFmpegProcessing < 0 {
				g.activeFFmpegProcessing = 0
			}
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
		JobID:          j.ID,
		Engine:         j.Engine,
		AcquiredAt:     now,
		GlobalTransfer: true,
	}

	switch j.Status {
	case StatusDownloading, StatusProcessing:
		g.activeTransfers++
		if j.Engine != "" {
			g.activePerEngine[j.Engine]++
		}
	case StatusSeeding:
		// StatusSeeding does not consume transfer capacity.
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
		GlobalTransferLimit:    g.effectiveGlobalTransferLimit(),
		ActiveTransfers:        g.activeTransfers,
		EngineLimits:           engLimits,
		ActivePerEngine:        activeEng,
		MediaAnalysisLimit:     g.effectiveAnalysisLimit(),
		ActiveAnalysis:         g.activeAnalysis,
		FFmpegProcessingLimit:  g.effectiveProcessingLimit(),
		ActiveFFmpegProcessing: g.activeFFmpegProcessing,
		ActiveLeases:           len(g.leases),
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

// SetMediaAnalysisLimit dynamically updates the media analysis limit.
func (g *ResourceGovernor) SetMediaAnalysisLimit(limit int) {
	if limit <= 0 {
		limit = DefaultMediaAnalysisLimit
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.mediaAnalysisLimit = limit
	g.wakeNextAnalysisWaiterLocked()
}

// SetFFmpegProcessingLimit dynamically updates the FFmpeg processing limit.
func (g *ResourceGovernor) SetFFmpegProcessingLimit(limit int) {
	if limit <= 0 {
		limit = DefaultFFmpegProcessingLimit
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ffmpegProcessingLimit = limit
}
