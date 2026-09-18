package job

import (
	"sort"
	"strings"
	"time"
)

const (
	PriorityScoreHigh   int64 = 200
	PriorityScoreNormal int64 = 100
	PriorityScoreLow    int64 = 0

	DefaultAgingInterval          = 60 * time.Second
	DefaultAgingPointsPerInterval = int64(5)
	DefaultMaxAgingBonus          = int64(250)

	RunNowPositionSentinel int64 = 0
)

// AgingConfig configures the deterministic aging parameters for queue scheduling.
type AgingConfig struct {
	AgingInterval          time.Duration
	AgingPointsPerInterval int64
	MaxAgingBonus          int64
}

// DefaultAgingConfig returns the standard production aging configuration.
func DefaultAgingConfig() AgingConfig {
	return AgingConfig{
		AgingInterval:          DefaultAgingInterval,
		AgingPointsPerInterval: DefaultAgingPointsPerInterval,
		MaxAgingBonus:          DefaultMaxAgingBonus,
	}
}

// PriorityScore maps a JobPriority to its base numerical score.
func PriorityScore(p JobPriority) int64 {
	switch strings.ToLower(string(p)) {
	case string(JobPriorityHigh):
		return PriorityScoreHigh
	case string(JobPriorityLow):
		return PriorityScoreLow
	case string(JobPriorityNormal):
		return PriorityScoreNormal
	default:
		return PriorityScoreNormal
	}
}

// AgingContribution calculates the bounded aging bonus score based on wait duration.
func AgingContribution(enqueuedAt, now time.Time, cfg AgingConfig) int64 {
	if cfg.AgingInterval <= 0 || cfg.AgingPointsPerInterval <= 0 || cfg.MaxAgingBonus <= 0 {
		return 0
	}
	wait := now.Sub(enqueuedAt)
	if wait <= 0 {
		return 0
	}
	intervals := int64(wait / cfg.AgingInterval)
	bonus := intervals * cfg.AgingPointsPerInterval
	if bonus > cfg.MaxAgingBonus {
		bonus = cfg.MaxAgingBonus
	}
	return bonus
}

// EffectivePriority computes the combined base priority and bounded aging bonus.
func EffectivePriority(qj *QueuedJob, now time.Time, cfg AgingConfig) int64 {
	if qj == nil {
		return 0
	}
	base := PriorityScore(qj.Job.Priority)
	bonus := AgingContribution(qj.EnqueuedAt, now, cfg)
	return base + bonus
}

// IsRunNow reports whether a queued job has an active Run Now override.
// Run Now is represented by Position <= 0.
func IsRunNow(qj *QueuedJob) bool {
	if qj == nil {
		return false
	}
	return qj.Position <= RunNowPositionSentinel
}

// CompareQueuedJobs orders two queued jobs deterministically.
// Returns:
//   - negative if a should be dispatched before b
//   - positive if b should be dispatched before a
//   - zero if exactly equal (should not occur with unique job IDs)
func CompareQueuedJobs(a, b *QueuedJob, now time.Time, cfg AgingConfig) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1
	}
	if b == nil {
		return -1
	}

	aRunNow := IsRunNow(a)
	bRunNow := IsRunNow(b)

	// 1. Run Now takes absolute precedence over non-Run Now
	if aRunNow != bRunNow {
		if aRunNow {
			return -1
		}
		return 1
	}

	// If both are Run Now:
	if aRunNow && bRunNow {
		// Lower position wins (e.g. -1 < 0)
		if a.Position != b.Position {
			if a.Position < b.Position {
				return -1
			}
			return 1
		}
		// Earlier UpdatedAt wins (first requested Run Now)
		if !a.UpdatedAt.Equal(b.UpdatedAt) {
			if a.UpdatedAt.Before(b.UpdatedAt) {
				return -1
			}
			return 1
		}
		// Earlier EnqueuedAt wins
		if !a.EnqueuedAt.Equal(b.EnqueuedAt) {
			if a.EnqueuedAt.Before(b.EnqueuedAt) {
				return -1
			}
			return 1
		}
		// Stable ID tie-breaker
		return strings.Compare(a.JobID, b.JobID)
	}

	// 2. Both are non-Run Now candidates:
	// Apply one unified, globally consistent precedence chain to all candidates:
	// A. Higher effective priority (base priority + aging contribution) wins.
	effA := EffectivePriority(a, now, cfg)
	effB := EffectivePriority(b, now, cfg)
	if effA != effB {
		if effA > effB {
			return -1
		}
		return 1
	}

	// B. Higher base priority breaks effective score ties (e.g. fresh Normal vs aged Low).
	baseA := PriorityScore(a.Job.Priority)
	baseB := PriorityScore(b.Job.Priority)
	if baseA != baseB {
		if baseA > baseB {
			return -1
		}
		return 1
	}

	// C. Explicit durable queue position breaks exact scheduling ties (lower position wins).
	if a.Position != b.Position {
		if a.Position < b.Position {
			return -1
		}
		return 1
	}

	// D. Earlier durable enqueue timestamp breaks position ties.
	if !a.EnqueuedAt.Equal(b.EnqueuedAt) {
		if a.EnqueuedAt.Before(b.EnqueuedAt) {
			return -1
		}
		return 1
	}

	// E. Deterministic stable JobID lexicographical comparison as the final tie-breaker.
	return strings.Compare(a.JobID, b.JobID)
}

// RankQueuedJobs sorts a slice of queued jobs in-place in dispatch order (best candidate first).
func RankQueuedJobs(jobs []QueuedJob, now time.Time, cfg AgingConfig) {
	sort.SliceStable(jobs, func(i, j int) bool {
		return CompareQueuedJobs(&jobs[i], &jobs[j], now, cfg) < 0
	})
}

// SelectNextRunnable returns the highest-priority runnable candidate from the slice, or nil if empty.
func SelectNextRunnable(jobs []QueuedJob, now time.Time, cfg AgingConfig) *QueuedJob {
	if len(jobs) == 0 {
		return nil
	}
	best := 0
	for i := 1; i < len(jobs); i++ {
		if CompareQueuedJobs(&jobs[i], &jobs[best], now, cfg) < 0 {
			best = i
		}
	}
	result := jobs[best]
	return &result
}
