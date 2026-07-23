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
	mu            sync.Mutex
	lastPersisted map[string]time.Time
}

// persistInterval controls how often progress is written to DB (vs SSE which is every tick).
const persistInterval = 3 * time.Second

// NewMonitor creates a new progress monitor.
func NewMonitor(manager *Manager, interval time.Duration) *Monitor {
	return &Monitor{
		manager:       manager,
		interval:      interval,
		stopCh:        make(chan struct{}),
		lastPersisted: make(map[string]time.Time),
	}
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
	activeJobs := m.manager.GetActiveJobs()
	if len(activeJobs) == 0 {
		return
	}

	for _, j := range activeJobs {
		if j.EngineID == "" {
			continue
		}

		status, err := m.manager.engine.Status(ctx, j)
		if err != nil {
			log.Printf("monitor: failed to get status for job %s: %v", j.ID, err)
			continue
		}

		// Determine if we should persist to DB this tick
		persistNow := m.shouldPersist(j.ID, status)

		m.manager.UpdateJobFromEngine(ctx, j, status, persistNow)
	}
}

// shouldPersist determines whether to write to DB this tick.
// State transitions always persist. Progress updates use tiered persistence.
func (m *Monitor) shouldPersist(jobID string, status interface{ }) bool {
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
	m.mu.Unlock()
}
