package job

import (
	"context"
	"fmt"
	"log"
	"sync"
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
		kickCh:     make(chan struct{}, 1),
	}
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

		if running >= max {
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

		// Revalidate job state before dispatch
		current, err := s.repo.GetByID(s.ctx, next.JobID)
		if err != nil || current == nil || current.Status != StatusQueued {
			log.Printf("scheduler: job %s is no longer queued (status=%v), skipping", next.JobID, currentStatus(current))
			// Remove stale queue row if job is terminal or active
			if current != nil && (current.Status == StatusCompleted || current.Status == StatusFailed || current.Status == StatusCancelled || current.Status == StatusDownloading || current.Status == StatusProcessing || current.Status == StatusSeeding) {
				s.queueRepo.Delete(s.ctx, next.JobID)
			}
			continue
		}

		entry, err := s.queueRepo.Get(s.ctx, next.JobID)
		if err != nil || entry == nil {
			log.Printf("scheduler: queue entry for job %s missing, skipping", next.JobID)
			continue
		}

		// Dispatch execution
		dispatchErr := s.dispatchFn(s.ctx, next)
		if dispatchErr != nil {
			log.Printf("scheduler: failed to dispatch job %s (action=%s): %v", next.JobID, next.Action, dispatchErr)
			if next.Action == QueueActionStart {
				// Mark job FAILED, remove queue entry
				current.Status = StatusFailed
				current.Error = fmt.Sprintf("failed to start queued download: %v", dispatchErr)
				current.SpeedBytesPerSecond = 0
				current.ETASeconds = 0
				s.repo.Update(s.ctx, current)
				s.queueRepo.Delete(s.ctx, next.JobID)
			} else {
				// QueueActionResume failure: Mark job PAUSED, retain queue entry for retry
				current.Status = StatusPaused
				current.Error = fmt.Sprintf("failed to resume queued download: %v", dispatchErr)
				current.SpeedBytesPerSecond = 0
				current.ETASeconds = 0
				s.repo.Update(s.ctx, current)
			}
			// Continue loop to allow next valid job to run
		}
	}
}

func currentStatus(j *Job) string {
	if j == nil {
		return "<nil>"
	}
	return string(j.Status)
}
