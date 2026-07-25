package job

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// SchedulerDispatchFunc defines the callback for dispatching a queued job to an engine.
type SchedulerDispatchFunc func(ctx context.Context, qj *QueuedJob) error

// SchedulerLimitFunc returns the current effective maximum concurrent downloads.
type SchedulerLimitFunc func(ctx context.Context) int

// Scheduler manages queued download execution policy and capacity constraints.
type Scheduler struct {
	repo       IJobRepository
	queueRepo  IQueueRepository
	getLimit   SchedulerLimitFunc
	dispatchFn SchedulerDispatchFunc
	bus        IEventBus

	mu       sync.Mutex
	inFlight map[string]struct{}

	kickCh chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewScheduler creates a new Scheduler instance.
func NewScheduler(
	repo IJobRepository,
	queueRepo IQueueRepository,
	getLimit SchedulerLimitFunc,
	dispatchFn SchedulerDispatchFunc,
) *Scheduler {
	return &Scheduler{
		repo:       repo,
		queueRepo:  queueRepo,
		getLimit:   getLimit,
		dispatchFn: dispatchFn,
		inFlight:   make(map[string]struct{}),
		kickCh:     make(chan struct{}, 1),
	}
}

// SetEventBus injects an optional event bus for publishing failure/update events.
func (s *Scheduler) SetEventBus(bus IEventBus) {
	s.bus = bus
}

// Start launches the single scheduler background loop.
func (s *Scheduler) Start(parentCtx context.Context) {
	s.ctx, s.cancel = context.WithCancel(parentCtx)
	s.wg.Add(1)
	go s.loop()
}

// Stop terminates the scheduler background loop cleanly.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
}

// Kick signals the scheduler to check for available capacity and process queued jobs.
// It is non-blocking and safe to call from any goroutine.
func (s *Scheduler) Kick() {
	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

func (s *Scheduler) loop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.kickCh:
			s.schedule()
		}
	}
}

func (s *Scheduler) isInFlight(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inFlight[id]
	return ok
}

func (s *Scheduler) reserveInFlight(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inFlight[id]; ok {
		return false
	}
	s.inFlight[id] = struct{}{}
	return true
}

func (s *Scheduler) releaseInFlight(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}

func (s *Scheduler) inFlightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.inFlight)
}

func (s *Scheduler) schedule() {
	for {
		if s.ctx.Err() != nil {
			return
		}

		max := s.getLimit(s.ctx)
		running, err := s.repo.CountDownloading(s.ctx)
		if err != nil {
			log.Printf("scheduler: failed to count downloading jobs: %v", err)
			return
		}

		effectiveRunning := running + s.inFlightCount()
		if effectiveRunning >= max {
			return
		}

		next, err := s.queueRepo.NextRunnable(s.ctx)
		if err != nil {
			log.Printf("scheduler: failed to query next runnable job: %v", err)
			return
		}
		if next == nil {
			return
		}

		if !s.reserveInFlight(next.JobID) {
			// Already in flight in another dispatch step
			continue
		}

		if err := s.dispatchSingle(next); err != nil {
			if errors.Is(err, ErrDispatchPersistenceFailed) {
				// Stop fill loop: persistence is unreliable, do not dispatch more.
				// In-flight reservation is retained to prevent double-dispatch.
				log.Printf("scheduler: stopping fill loop due to persistence failure for job %s", next.JobID)
				return
			}
		}
	}
}

func (s *Scheduler) dispatchSingle(next *QueuedJob) error {
	// NOTE: in-flight reservation is released here for all paths EXCEPT
	// ErrDispatchPersistenceFailed, where it is retained to prevent double-dispatch.
	releaseReservation := true
	defer func() {
		if releaseReservation {
			s.releaseInFlight(next.JobID)
		}
	}()

	// Revalidate job state immediately before dispatch
	current, err := s.repo.GetByID(s.ctx, next.JobID)
	if err != nil || current == nil || current.Status != StatusQueued {
		log.Printf("scheduler: job %s is no longer queued (status=%v), skipping", next.JobID, currentStatus(current))
		if current != nil && (current.Status == StatusCompleted || current.Status == StatusFailed || current.Status == StatusCancelled || current.Status == StatusDownloading || current.Status == StatusProcessing || current.Status == StatusSeeding) {
			s.queueRepo.Delete(s.ctx, next.JobID)
		}
		return nil
	}

	entry, err := s.queueRepo.Get(s.ctx, next.JobID)
	if err != nil || entry == nil || entry.Action != next.Action {
		log.Printf("scheduler: queue entry for job %s missing or changed, skipping", next.JobID)
		return nil
	}

	// Dispatch execution to engine
	dispatchErr := s.dispatchFn(s.ctx, next)
	if dispatchErr != nil {
		if errors.Is(dispatchErr, ErrDispatchPersistenceFailed) {
			log.Printf("scheduler: reconciliation required for job %s: %v", next.JobID, dispatchErr)
			// Retain in-flight reservation so the job cannot be double-dispatched
			releaseReservation = false
			return dispatchErr
		}

		log.Printf("scheduler: engine failed for job %s (action=%s): %v", next.JobID, next.Action, dispatchErr)
		now := time.Now()

		if next.Action == QueueActionStart {
			// START failure: mark FAILED, delete queue row only after persistence succeeds
			current.Status = StatusFailed
			current.Error = fmt.Sprintf("failed to start queued download: %v", dispatchErr)
			current.SpeedBytesPerSecond = 0
			current.ETASeconds = 0
			current.UpdatedAt = now
			if updateErr := s.repo.Update(s.ctx, current); updateErr != nil {
				log.Printf("scheduler: failed to persist FAILED state for job %s: %v", next.JobID, updateErr)
				// Do NOT delete queue row — FAILED state was not persisted
			} else {
				s.queueRepo.Delete(s.ctx, next.JobID)
			}
			if s.bus != nil {
				s.bus.Publish(Event{Type: EventJobFailed, Job: *current})
			}
		} else {
			// RESUME failure: mark PAUSED, retain queue entry for user retry
			current.Status = StatusPaused
			current.Error = fmt.Sprintf("failed to resume queued download: %v", dispatchErr)
			current.SpeedBytesPerSecond = 0
			current.ETASeconds = 0
			current.UpdatedAt = now
			if updateErr := s.repo.Update(s.ctx, current); updateErr != nil {
				log.Printf("scheduler: failed to persist PAUSED state for job %s: %v", next.JobID, updateErr)
			}
			if s.bus != nil {
				s.bus.Publish(Event{Type: EventJobUpdated, Job: *current})
			}
		}
	}
	return nil
}

func currentStatus(j *Job) string {
	if j == nil {
		return "<nil>"
	}
	return string(j.Status)
}
