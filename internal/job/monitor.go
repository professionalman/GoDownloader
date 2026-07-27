package job

import (
	"context"
	"log"
	"sync"
	"time"
)

// Monitor periodically polls the engine for active job status updates.
type Monitor struct {
	manager  *Manager
	interval time.Duration

	stopCh chan struct{}
	wg     sync.WaitGroup

	// Track last DB persist time per job for tiered persistence
	mu                  sync.Mutex
	lastPersisted       map[string]time.Time
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
	close(m.stopCh)
	m.wg.Wait()
}

func (m *Monitor) run(ctx context.Context) {
	defer m.wg.Done()

	ticker := time.NewTicker(m.interval)
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
		j.Status = StatusFailed
		j.Error = "Engine lost connection or task disappeared after repeated status check failures."
		j.SpeedBytesPerSecond = 0
		j.ETASeconds = 0
		j.UpdatedAt = time.Now()
		if err := m.manager.repo.Update(ctx, j); err != nil {
			log.Printf("monitor: failed to persist FAILED status for job %s: %v", j.ID, err)
			return
		}
		m.manager.removeActive(j.ID)
		m.manager.publish(EventJobFailed, j)
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
	delete(m.consecutiveFailures, jobID)
	m.mu.Unlock()
}
