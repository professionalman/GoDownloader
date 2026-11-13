package job

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// Monitor periodically polls the engine for active job status updates.
type Monitor struct {
	manager  *Manager
	interval time.Duration

	stopCh chan struct{}
	// stopOnce makes Stop idempotent (a second Stop must not close stopCh again).
	stopOnce sync.Once
	wg       sync.WaitGroup

	// running is true while the monitoring loop goroutine is alive.
	running atomic.Bool

	// Track last DB persist time per job for tiered persistence
	mu                  sync.Mutex
	lastPersisted       map[string]time.Time
	lastPolled          map[string]time.Time
	consecutiveFailures map[string]int

	lastCleanupSweep     time.Time
	cleanupSweepInterval time.Duration
}

// persistInterval controls how often progress is written to DB (vs SSE which is every tick).
const persistInterval = 3 * time.Second

// maxConsecutiveFailures defines max status query failures before marking a job failed.
const maxConsecutiveFailures = 5

// NewMonitor creates a new progress monitor.
func NewMonitor(manager *Manager, interval time.Duration) *Monitor {
	return &Monitor{
		manager:              manager,
		interval:             interval,
		stopCh:               make(chan struct{}),
		lastPersisted:        make(map[string]time.Time),
		lastPolled:           make(map[string]time.Time),
		consecutiveFailures:  make(map[string]int),
		cleanupSweepInterval: 15 * time.Second,
	}
}

// SetCleanupSweepInterval allows overriding the default 15s cleanup retry throttle interval.
func (m *Monitor) SetCleanupSweepInterval(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupSweepInterval = d
}

// Start begins the progress monitoring loop.
func (m *Monitor) Start(ctx context.Context) {
	m.wg.Add(1)
	go m.run(ctx)
}

// Stop stops the monitoring loop and waits for it to finish.
func (m *Monitor) Stop() {
	m.signalStop()
	m.wg.Wait()
}

// signalStop closes the stop channel without waiting for the loop. Idempotent.
func (m *Monitor) signalStop() {
	m.stopOnce.Do(func() { close(m.stopCh) })
}

// Running reports whether the monitoring loop goroutine is alive.
func (m *Monitor) Running() bool {
	return m != nil && m.running.Load()
}

func (m *Monitor) run(ctx context.Context) {
	defer m.wg.Done()
	m.running.Store(true)
	defer m.running.Store(false)

	baseInterval := 250 * time.Millisecond
	if m.interval > 0 && m.interval < baseInterval {
		baseInterval = m.interval
	}

	ticker := time.NewTicker(baseInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Monitor) tick(ctx context.Context) {
	m.mu.Lock()
	interval := m.cleanupSweepInterval
	lastSweep := m.lastCleanupSweep
	now := time.Now()
	shouldSweep := interval > 0 && now.Sub(lastSweep) >= interval
	if shouldSweep {
		m.lastCleanupSweep = now
	}
	m.mu.Unlock()

	if shouldSweep && m.manager != nil {
		m.manager.processPendingEngineCleanups(ctx)
	}

	activeJobs := m.manager.GetActiveJobs()
	if len(activeJobs) == 0 {
		return
	}

	for _, j := range activeJobs {
		if j.Status == StatusCompleted || j.Status == StatusFailed || j.Status == StatusCancelled {
			m.manager.removeActive(j.ID)
			m.CleanupJob(j.ID)
			continue
		}

		if j.EngineID == "" {
			continue
		}

		m.mu.Lock()
		lastPoll := m.lastPolled[j.ID]
		now := time.Now()
		var due bool
		if j.Type == TypeMedia || j.Engine == "ytdlp" {
			due = lastPoll.IsZero() || now.Sub(lastPoll) >= 250*time.Millisecond
		} else {
			due = lastPoll.IsZero() || now.Sub(lastPoll) >= 1*time.Second
		}
		if due {
			m.lastPolled[j.ID] = now
		}
		m.mu.Unlock()

		if !due {
			continue
		}

		eng, ok := m.manager.GetEngine(j.Engine)
		if !ok {
			log.Printf("monitor: engine %q not available for job %s", j.Engine, j.ID)
			m.recordFailure(ctx, j, "Engine unavailable")
			continue
		}

		status, err := eng.Status(ctx, j)
		if err != nil {
			log.Printf("monitor: failed to get status for job %s: %v", j.ID, err)
			m.recordFailure(ctx, j, err.Error())
			continue
		}

		// Reset failure counter on success
		m.resetFailure(j.ID)

		// Determine if we should persist to DB this tick
		persistNow := m.shouldPersist(j.ID, status)

		m.manager.UpdateJobFromEngine(ctx, j, status, persistNow)
	}
}

func (m *Monitor) recordFailure(ctx context.Context, j *Job, errMsg string) {
	m.mu.Lock()
	m.consecutiveFailures[j.ID]++
	count := m.consecutiveFailures[j.ID]
	m.mu.Unlock()

	if count >= maxConsecutiveFailures {
		if current, err := m.manager.repo.GetByID(ctx, j.ID); err == nil && current != nil && (current.Status == StatusCompleted || current.Status == StatusFailed || current.Status == StatusCancelled) {
			log.Printf("monitor: job %s is already terminal in DB (status=%s), purging stale active tracking", j.ID, current.Status)
			m.manager.removeActive(j.ID)
			m.CleanupJob(j.ID)
			return
		}

		log.Printf("monitor: job %s exceeded max status query failures (%d), marking failed", j.ID, count)
		// The read above and this write are not atomic: commit FAILED only if the job is still in the state this
		// snapshot observed, under the job's state lock, so a cancellation committed meanwhile is never overwritten.
		stateLock := m.manager.jobStateLock(j.ID)
		stateLock.Lock()
		observed := j.Status
		j.Status = StatusFailed
		j.Error = "Engine lost connection or task disappeared after repeated status check failures."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		won, err := m.manager.persistObservedState(ctx, j, observed)
		if err != nil || !won {
			stateLock.Unlock()
			if err != nil {
				log.Printf("monitor: failed to persist FAILED status for job %s: %v", j.ID, err)
			} else {
				log.Printf("monitor: job %s left %s concurrently; FAILED not persisted", j.ID, observed)
				m.CleanupJob(j.ID)
			}
			return
		}
		m.manager.removeActive(j.ID)
		m.manager.publish(EventJobFailed, j)
		stateLock.Unlock()
		m.manager.cleanupTerminalEngineState(j)
		m.CleanupJob(j.ID)
		if m.manager.scheduler != nil {
			m.manager.scheduler.Kick()
		}
	}
}

func (m *Monitor) resetFailure(jobID string) {
	m.mu.Lock()
	delete(m.consecutiveFailures, jobID)
	m.mu.Unlock()
}

// shouldPersist determines whether to write to DB this tick.
// State transitions always persist. Progress updates use tiered persistence.
func (m *Monitor) shouldPersist(jobID string, status interface{}) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	last, exists := m.lastPersisted[jobID]
	now := time.Now()

	if !exists || now.Sub(last) >= persistInterval {
		m.lastPersisted[jobID] = now
		return true
	}
	return false
}

// CleanupJob removes tracking data for a completed/failed/cancelled job.
func (m *Monitor) CleanupJob(jobID string) {
	m.mu.Lock()
	delete(m.lastPersisted, jobID)
	delete(m.lastPolled, jobID)
	delete(m.consecutiveFailures, jobID)
	m.mu.Unlock()
}
