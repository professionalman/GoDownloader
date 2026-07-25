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

// AddActiveFunc defines the callback for adding a job to Manager's active set.
type AddActiveFunc func(j *Job)

// ReconciliationKind distinguishes external execution failure from state persistence failure.
type ReconciliationKind string

const (
	ReconciliationExternalExecution ReconciliationKind = "external_execution"
	ReconciliationStatePersistence  ReconciliationKind = "state_persistence"
)

// DispatchReservation tracks in-flight and dirty dispatch state for a job.
type DispatchReservation struct {
	JobID        string
	Action       QueueAction
	EngineID     string
	Dirty        bool
	Kind         ReconciliationKind
	TargetStatus JobStatus
	TargetError  string
}

// Scheduler manages queued download execution policy and capacity constraints.
type Scheduler struct {
	repo        IJobRepository
	queueRepo   IQueueRepository
	getLimit    SchedulerLimitFunc
	dispatchFn  SchedulerDispatchFunc
	bus         IEventBus
	engines     IEngineRegistry
	addActiveFn AddActiveFunc

	mu       sync.Mutex
	inFlight map[string]*DispatchReservation

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
		inFlight:   make(map[string]*DispatchReservation),
		kickCh:     make(chan struct{}, 1),
	}
}

// SetEventBus injects an optional event bus for publishing failure/update events.
func (s *Scheduler) SetEventBus(bus IEventBus) {
	s.bus = bus
}

// SetEngineRegistry injects the engine registry for status queries during reconciliation.
func (s *Scheduler) SetEngineRegistry(engines IEngineRegistry) {
	s.engines = engines
}

// SetAddActiveFunc injects the callback to register active jobs upon successful reconciliation.
func (s *Scheduler) SetAddActiveFunc(fn AddActiveFunc) {
	s.addActiveFn = fn
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

func (s *Scheduler) reserveInFlight(id string, action QueueAction) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.inFlight[id]; ok {
		return false
	}
	s.inFlight[id] = &DispatchReservation{
		JobID:  id,
		Action: action,
	}
	return true
}

func (s *Scheduler) releaseInFlight(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.inFlight, id)
}

func (s *Scheduler) markReservationDirtyExternal(id string, action QueueAction, engineID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if res, ok := s.inFlight[id]; ok {
		res.Dirty = true
		res.Action = action
		res.Kind = ReconciliationExternalExecution
		if engineID != "" {
			res.EngineID = engineID
		}
	} else {
		s.inFlight[id] = &DispatchReservation{
			JobID:    id,
			Action:   action,
			EngineID: engineID,
			Dirty:    true,
			Kind:     ReconciliationExternalExecution,
		}
	}
}

func (s *Scheduler) markReservationDirtyState(id string, action QueueAction, targetStatus JobStatus, targetError string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if res, ok := s.inFlight[id]; ok {
		res.Dirty = true
		res.Action = action
		res.Kind = ReconciliationStatePersistence
		res.TargetStatus = targetStatus
		res.TargetError = targetError
	} else {
		s.inFlight[id] = &DispatchReservation{
			JobID:        id,
			Action:       action,
			Dirty:        true,
			Kind:         ReconciliationStatePersistence,
			TargetStatus: targetStatus,
			TargetError:  targetError,
		}
	}
}

func (s *Scheduler) hasUnresolvedReconciliations() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, res := range s.inFlight {
		if res.Dirty {
			return true
		}
	}
	return false
}

func (s *Scheduler) getDirtyReservations() []*DispatchReservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	var list []*DispatchReservation
	for _, res := range s.inFlight {
		if res.Dirty {
			cp := *res
			list = append(list, &cp)
		}
	}
	return list
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

		if s.hasUnresolvedReconciliations() {
			s.reconcileAll(s.ctx)
			if s.hasUnresolvedReconciliations() {
				return
			}
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

		if !s.reserveInFlight(next.JobID, next.Action) {
			// Highest-priority runnable job is already reserved/in-flight.
			// Return immediately to prevent tight CPU spin or skip-ahead.
			return
		}

		if err := s.dispatchSingle(next); err != nil {
			if errors.Is(err, ErrDispatchPersistenceFailed) {
				log.Printf("scheduler: stopping fill loop due to persistence failure for job %s", next.JobID)
				return
			}
		}
	}
}

func (s *Scheduler) reconcileAll(ctx context.Context) {
	dirtyList := s.getDirtyReservations()
	for _, res := range dirtyList {
		s.reconcileJob(ctx, res.JobID)
	}
}

func (s *Scheduler) reconcileJob(ctx context.Context, jobID string) {
	s.mu.Lock()
	res, ok := s.inFlight[jobID]
	if !ok || !res.Dirty {
		s.mu.Unlock()
		return
	}
	resCopy := *res
	s.mu.Unlock()

	switch resCopy.Kind {
	case ReconciliationExternalExecution:
		s.reconcileExternalExecution(ctx, resCopy)
	case ReconciliationStatePersistence:
		s.reconcileStatePersistence(ctx, resCopy)
	default:
		log.Printf("scheduler: unknown reconciliation kind %q for job %s", resCopy.Kind, jobID)
	}
}

func (s *Scheduler) reconcileStatePersistence(ctx context.Context, res DispatchReservation) {
	current, err := s.repo.GetByID(ctx, res.JobID)
	if err != nil || current == nil {
		log.Printf("scheduler: state reconciliation failed to fetch job %s: %v", res.JobID, err)
		return
	}

	current.Status = res.TargetStatus
	current.Error = res.TargetError
	current.SpeedBytesPerSecond = 0
	current.ETASeconds = 0
	current.UpdatedAt = time.Now()

	if updateErr := s.repo.Update(ctx, current); updateErr != nil {
		log.Printf("scheduler: state reconciliation failed to persist %s state for job %s: %v", res.TargetStatus, res.JobID, updateErr)
		return
	}

	if res.TargetStatus == StatusFailed {
		if delErr := s.queueRepo.Delete(ctx, current.ID); delErr != nil {
			log.Printf("scheduler: state reconciliation failed to delete queue entry for %s: %v", current.ID, delErr)
		}
		if s.bus != nil {
			s.bus.Publish(Event{Type: EventJobFailed, Job: *current})
		}
	} else if res.TargetStatus == StatusPaused {
		if s.bus != nil {
			s.bus.Publish(Event{Type: EventJobUpdated, Job: *current})
		}
	}

	s.releaseInFlight(current.ID)
	log.Printf("scheduler: state reconciliation succeeded for job %s (%s)", current.ID, res.TargetStatus)
	s.Kick()
}

func (s *Scheduler) reconcileExternalExecution(ctx context.Context, res DispatchReservation) {
	current, err := s.repo.GetByID(ctx, res.JobID)
	if err != nil || current == nil {
		log.Printf("scheduler: external reconciliation failed to fetch job %s: %v", res.JobID, err)
		return
	}

	if current.EngineID == "" && res.EngineID != "" {
		current.EngineID = res.EngineID
	}

	if res.Action == QueueActionStart && current.EngineID == "" {
		log.Printf("scheduler: cannot reconcile external execution for job %s: missing engine ID", res.JobID)
		return
	}

	if s.engines == nil {
		log.Printf("scheduler: cannot reconcile job %s: engine registry not set", res.JobID)
		return
	}

	eng, ok := s.engines.Get(current.Engine)
	if !ok || eng == nil {
		log.Printf("scheduler: cannot reconcile job %s: engine %s unavailable", res.JobID, current.Engine)
		return
	}

	status, err := eng.Status(ctx, current)
	if err != nil {
		log.Printf("scheduler: external reconciliation engine Status query failed for job %s: %v", res.JobID, err)
		return
	}

	if status == nil {
		log.Printf("scheduler: external reconciliation engine returned nil status for job %s", res.JobID)
		return
	}

	now := time.Now()
	switch status.Status {
	case StatusDownloading:
		current.Status = StatusDownloading
		current.UpdatedAt = now
		if updateErr := s.repo.Update(ctx, current); updateErr != nil {
			log.Printf("scheduler: external reconciliation failed to persist DOWNLOADING for job %s: %v", res.JobID, updateErr)
			return
		}
		if delErr := s.queueRepo.Delete(ctx, current.ID); delErr != nil {
			log.Printf("scheduler: external reconciliation failed to delete queue entry for %s: %v", current.ID, delErr)
		}
		if s.addActiveFn != nil {
			s.addActiveFn(current)
		}
		s.releaseInFlight(current.ID)
		log.Printf("scheduler: external reconciliation succeeded for job %s, status=DOWNLOADING", current.ID)
		s.Kick()

	case StatusProcessing:
		current.Status = StatusProcessing
		current.UpdatedAt = now
		if updateErr := s.repo.Update(ctx, current); updateErr != nil {
			log.Printf("scheduler: external reconciliation failed to persist PROCESSING for job %s: %v", res.JobID, updateErr)
			return
		}
		if delErr := s.queueRepo.Delete(ctx, current.ID); delErr != nil {
			log.Printf("scheduler: external reconciliation failed to delete queue entry for %s: %v", current.ID, delErr)
		}
		if s.addActiveFn != nil {
			s.addActiveFn(current)
		}
		s.releaseInFlight(current.ID)
		log.Printf("scheduler: external reconciliation succeeded for job %s, status=PROCESSING", current.ID)
		s.Kick()

	case StatusSeeding:
		current.Status = StatusSeeding
		current.UpdatedAt = now
		if updateErr := s.repo.Update(ctx, current); updateErr != nil {
			log.Printf("scheduler: external reconciliation failed to persist SEEDING for job %s: %v", res.JobID, updateErr)
			return
		}
		if delErr := s.queueRepo.Delete(ctx, current.ID); delErr != nil {
			log.Printf("scheduler: external reconciliation failed to delete queue entry for %s: %v", current.ID, delErr)
		}
		if s.addActiveFn != nil {
			s.addActiveFn(current)
		}
		s.releaseInFlight(current.ID)
		log.Printf("scheduler: external reconciliation succeeded for job %s, status=SEEDING", current.ID)
		s.Kick()

	case StatusCompleted:
		current.Status = StatusCompleted
		current.UpdatedAt = now
		if updateErr := s.repo.Update(ctx, current); updateErr != nil {
			log.Printf("scheduler: external reconciliation failed to persist COMPLETED for job %s: %v", res.JobID, updateErr)
			return
		}
		if delErr := s.queueRepo.Delete(ctx, current.ID); delErr != nil {
			log.Printf("scheduler: external reconciliation failed to delete queue entry for %s: %v", current.ID, delErr)
		}
		s.releaseInFlight(current.ID)
		if s.bus != nil {
			s.bus.Publish(Event{Type: EventJobCompleted, Job: *current})
		}
		log.Printf("scheduler: external reconciliation succeeded for job %s, status=COMPLETED", current.ID)
		s.Kick()

	default:
		log.Printf("scheduler: external reconciliation engine status for job %s is non-active (%v), keeping reservation", res.JobID, status.Status)
	}
}

func (s *Scheduler) dispatchSingle(next *QueuedJob) error {
	releaseReservation := true
	defer func() {
		if releaseReservation {
			s.releaseInFlight(next.JobID)
		}
	}()

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

	dispatchErr := s.dispatchFn(s.ctx, next)
	if dispatchErr != nil {
		var pErr *DispatchPersistenceError
		if errors.As(dispatchErr, &pErr) || errors.Is(dispatchErr, ErrDispatchPersistenceFailed) {
			engineID := getEngineIDFromErr(dispatchErr)
			log.Printf("scheduler: reconciliation required for job %s (engineID=%s): %v", next.JobID, engineID, dispatchErr)

			releaseReservation = false
			s.markReservationDirtyExternal(next.JobID, next.Action, engineID)
			s.reconcileJob(s.ctx, next.JobID)

			return dispatchErr
		}

		log.Printf("scheduler: engine failed for job %s (action=%s): %v", next.JobID, next.Action, dispatchErr)
		now := time.Now()

		if next.Action == QueueActionStart {
			targetErr := fmt.Sprintf("failed to start queued download: %v", dispatchErr)
			current.Status = StatusFailed
			current.Error = targetErr
			current.SpeedBytesPerSecond = 0
			current.ETASeconds = 0
			current.UpdatedAt = now
			if updateErr := s.repo.Update(s.ctx, current); updateErr != nil {
				log.Printf("scheduler: failed to persist FAILED state for job %s: %v", next.JobID, updateErr)
				releaseReservation = false
				s.markReservationDirtyState(next.JobID, next.Action, StatusFailed, targetErr)
				return &DispatchPersistenceError{
					JobID:  next.JobID,
					Action: next.Action,
					Err:    updateErr,
				}
			} else {
				if delErr := s.queueRepo.Delete(s.ctx, next.JobID); delErr != nil {
					log.Printf("scheduler: failed to delete queue entry for failed job %s: %v", next.JobID, delErr)
				}
				if s.bus != nil {
					s.bus.Publish(Event{Type: EventJobFailed, Job: *current})
				}
			}
		} else {
			targetErr := fmt.Sprintf("failed to resume queued download: %v", dispatchErr)
			current.Status = StatusPaused
			current.Error = targetErr
			current.SpeedBytesPerSecond = 0
			current.ETASeconds = 0
			current.UpdatedAt = now
			if updateErr := s.repo.Update(s.ctx, current); updateErr != nil {
				log.Printf("scheduler: failed to persist PAUSED state for job %s: %v", next.JobID, updateErr)
				releaseReservation = false
				s.markReservationDirtyState(next.JobID, next.Action, StatusPaused, targetErr)
				return &DispatchPersistenceError{
					JobID:  next.JobID,
					Action: next.Action,
					Err:    updateErr,
				}
			} else {
				if s.bus != nil {
					s.bus.Publish(Event{Type: EventJobUpdated, Job: *current})
				}
			}
		}
	}
	return nil
}

func getEngineIDFromErr(err error) string {
	var pErr *DispatchPersistenceError
	if errors.As(err, &pErr) {
		return pErr.EngineID
	}
	return ""
}

func currentStatus(j *Job) string {
	if j == nil {
		return "<nil>"
	}
	return string(j.Status)
}
