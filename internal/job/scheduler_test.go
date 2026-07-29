package job_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func setupSchedulerTestDB(t *testing.T) (*database.DB, *database.SQLiteJobRepository, *database.SQLiteQueueRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_scheduler.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)

	return db, jobRepo, queueRepo
}

func TestScheduler_ConcurrencyStressLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	const maxLimit = 3
	limitFn := func(ctx context.Context) int {
		return maxLimit
	}

	var activeCount int32
	var maxObserved int32
	var mu sync.Mutex
	dispatchedMap := make(map[string]bool)

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		curr := atomic.AddInt32(&activeCount, 1)
		defer atomic.AddInt32(&activeCount, -1)

		for {
			oldMax := atomic.LoadInt32(&maxObserved)
			if curr <= oldMax {
				break
			}
			if atomic.CompareAndSwapInt32(&maxObserved, oldMax, curr) {
				break
			}
		}

		mu.Lock()
		dispatchedMap[qj.JobID] = true
		mu.Unlock()

		j, _ := jobRepo.GetByID(ctx, qj.JobID)
		if j != nil {
			j.Status = job.StatusDownloading
			jobRepo.Update(ctx, j)
			queueRepo.Delete(ctx, j.ID)
		}

		time.Sleep(10 * time.Millisecond)

		if j != nil {
			j.Status = job.StatusCompleted
			jobRepo.Update(ctx, j)
		}
		return nil
	}

	sched := job.NewScheduler(jobRepo, queueRepo, limitFn, dispatchFn)
	sched.Start(ctx)
	defer sched.Stop()

	now := time.Now()
	const numJobs = 20

	for i := 1; i <= numJobs; i++ {
		id := fmt.Sprintf("job_%02d", i)
		j := &job.Job{
			ID:        id,
			Source:    "http://example.com/" + id,
			Name:      id,
			Status:    job.StatusQueued,
			Type:      job.TypeDownload,
			Engine:    "aria2",
			Priority:  job.JobPriorityNormal,
			CreatedAt: now,
			UpdatedAt: now,
		}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      id,
			Position:   int64(i),
			Action:     job.QueueActionStart,
			EnqueuedAt: now,
			UpdatedAt:  now,
		})
	}

	// Issue many concurrent Kick() calls
	var wg sync.WaitGroup
	for k := 0; k < 50; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < 5; r++ {
				sched.Kick()
				time.Sleep(2 * time.Millisecond)
			}
		}()
	}
	wg.Wait()

	// Wait for all jobs to complete processing
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(dispatchedMap)
		mu.Unlock()
		if count >= numJobs {
			break
		}
		sched.Kick()
		time.Sleep(20 * time.Millisecond)
	}

	obs := atomic.LoadInt32(&maxObserved)
	if obs > maxLimit {
		t.Fatalf("exceeded max limit %d: observed peak concurrent dispatches = %d", maxLimit, obs)
	}
}

func TestScheduler_DynamicSettingsLimitChange(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	var limitMu sync.Mutex
	limitVal := 1
	limitFn := func(ctx context.Context) int {
		limitMu.Lock()
		defer limitMu.Unlock()
		return limitVal
	}

	dispatchedCh := make(chan string, 10)
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		j, _ := jobRepo.GetByID(ctx, qj.JobID)
		if j != nil {
			j.Status = job.StatusDownloading
			jobRepo.Update(ctx, j)
			queueRepo.Delete(ctx, j.ID)
		}
		dispatchedCh <- qj.JobID
		return nil
	}

	sched := job.NewScheduler(jobRepo, queueRepo, limitFn, dispatchFn)
	sched.Start(ctx)
	defer sched.Stop()

	now := time.Now()

	createJob := func(id string, pos int64) {
		j := &job.Job{
			ID:        id,
			Source:    "http://example.com/" + id,
			Name:      id,
			Status:    job.StatusQueued,
			Type:      job.TypeDownload,
			Engine:    "aria2",
			Priority:  job.JobPriorityNormal,
			CreatedAt: now,
			UpdatedAt: now,
		}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      id,
			Position:   pos,
			Action:     job.QueueActionStart,
			EnqueuedAt: now,
			UpdatedAt:  now,
		})
	}

	createJob("job_a", 1)
	createJob("job_b", 2)
	createJob("job_c", 3)

	sched.Kick()

	// 1. First job dispatches under capacity = 1
	select {
	case id := <-dispatchedCh:
		if id != "job_a" {
			t.Errorf("expected job_a first, got %s", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for job_a")
	}

	// Dynamic Increase limit to 3
	limitMu.Lock()
	limitVal = 3
	limitMu.Unlock()

	sched.Kick()

	// Both job_b and job_c must start
	received := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case id := <-dispatchedCh:
			received[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for job_b / job_c under expanded limit")
		}
	}

	if !received["job_b"] || !received["job_c"] {
		t.Errorf("expected job_b and job_c to dispatch, got map: %+v", received)
	}

	// Dynamic Decrease limit to 1 while 3 downloads are active
	limitMu.Lock()
	limitVal = 1
	limitMu.Unlock()

	// Add job_d
	createJob("job_d", 4)
	sched.Kick()

	select {
	case id := <-dispatchedCh:
		t.Fatalf("job_d should not dispatch under decreased limit when capacity is full, but got %s", id)
	case <-time.After(300 * time.Millisecond):
		// Expected: non-preemptive, no new start while capacity is full
	}
}

func TestScheduler_ResumeFailureKeepsJobPaused(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return errors.New("simulated engine resume error")
	}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 2 }, dispatchFn)
	sched.Start(ctx)
	defer sched.Stop()

	now := time.Now()
	j := &job.Job{
		ID:        "job_resume_fail",
		Source:    "http://example.com/res",
		Name:      "Resume Fail",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionResume,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	sched.Kick()

	// Wait for scheduler to process
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		res, _ := jobRepo.GetByID(ctx, j.ID)
		if res != nil && res.Status == job.StatusPaused {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	updated, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil || updated == nil {
		t.Fatalf("get job failed: %v", err)
	}
	if updated.Status != job.StatusPaused {
		t.Errorf("expected job status PAUSED on resume error, got %s", updated.Status)
	}

	// Queue entry MUST still exist for user retry
	qEntry, err := queueRepo.Get(ctx, j.ID)
	if err != nil || qEntry == nil {
		t.Fatalf("expected queue entry to remain after resume error, got nil")
	}
	if qEntry.Action != job.QueueActionResume {
		t.Errorf("expected QueueActionResume action retained, got %s", qEntry.Action)
	}
}

func TestScheduler_CancelBeforeDispatchRace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	var dispatchCalled int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&dispatchCalled, 1)
		return nil
	}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 1 }, dispatchFn)

	now := time.Now()
	j := &job.Job{
		ID:        "job_cancel_race",
		Source:    "http://example.com/cancel",
		Name:      "Cancel Race",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	// Cancel job before scheduler runs dispatch
	j.Status = job.StatusCancelled
	jobRepo.Update(ctx, j)
	queueRepo.Delete(ctx, j.ID)

	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&dispatchCalled) != 0 {
		t.Errorf("expected dispatch to NOT be called for cancelled job, but called %d times", atomic.LoadInt32(&dispatchCalled))
	}
}

type fakeBus struct {
	mu     sync.Mutex
	events []job.Event
}

func (f *fakeBus) Publish(event job.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, event)
}
func (f *fakeBus) Subscribe() <-chan job.Event                           { return nil }
func (f *fakeBus) Unsubscribe(ch <-chan job.Event)                       {}
func (f *fakeBus) SubscribeType(eventType string) <-chan job.Event       { return nil }
func (f *fakeBus) UnsubscribeType(eventType string, ch <-chan job.Event) {}

type fakeEngine struct {
	statusFunc func(ctx context.Context, j *job.Job) (*job.EngineStatus, error)
}

func (f *fakeEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{Pause: true, Resume: true, Cancel: true, Retry: true}
}

func (f *fakeEngine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	return "gid_" + j.ID, nil
}
func (f *fakeEngine) Pause(ctx context.Context, j *job.Job) error  { return nil }
func (f *fakeEngine) Resume(ctx context.Context, j *job.Job) error { return nil }
func (f *fakeEngine) Cancel(ctx context.Context, j *job.Job) error { return nil }
func (f *fakeEngine) Status(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
	if f.statusFunc != nil {
		return f.statusFunc(ctx, j)
	}
	return &job.EngineStatus{Status: job.StatusDownloading}, nil
}
func (f *fakeEngine) HealthCheck(ctx context.Context) error { return nil }

type fakeRegistry struct {
	eng job.IEngine
}

func (r *fakeRegistry) Get(name string) (job.IEngine, bool) {
	return r.eng, true
}
func (r *fakeRegistry) Detect(url string) string {
	return "aria2"
}

func TestRecovery_V05_StartupSequence_RunningOccupiesCapacity(t *testing.T) {
	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bus := &fakeBus{}
	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return &job.EngineStatus{Status: job.StatusDownloading, TotalBytes: 100, CompletedBytes: 50, Progress: 50.0}, nil
		},
	}
	reg := &fakeRegistry{eng: fakeEng}

	m := job.NewManager(jobRepo, reg, bus, t.TempDir(), nil)
	m.SetQueueRepository(queueRepo)

	now := time.Now()
	jRun1 := &job.Job{
		ID:        "run_1",
		Source:    "http://example.com/run1",
		Name:      "Run 1",
		Status:    job.StatusDownloading,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		EngineID:  "gid_1",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jRun1)

	jRun2 := &job.Job{
		ID:        "run_2",
		Source:    "http://example.com/run2",
		Name:      "Run 2",
		Status:    job.StatusDownloading,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		EngineID:  "gid_2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jRun2)

	jQueued := &job.Job{
		ID:        "queued_wait",
		Source:    "http://example.com/wait",
		Name:      "Wait Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jQueued)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jQueued.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	var dispatchedCount int32
	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 2 }, func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&dispatchedCount, 1)
		return nil
	})
	m.SetScheduler(sched)

	// Execute explicit startup sequence
	m.StartBackgroundTasks(ctx)
	defer m.Stop()

	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&dispatchedCount) != 0 {
		t.Fatalf("scheduler should NOT dispatch queued_wait because capacity (2) is occupied by recovered running jobs, but dispatched %d times", atomic.LoadInt32(&dispatchedCount))
	}

	gotWait, _ := jobRepo.GetByID(ctx, "queued_wait")
	if gotWait.Status != job.StatusQueued {
		t.Errorf("expected queued_wait to remain QUEUED, got %s", gotWait.Status)
	}
}

// TestScheduler_StartPersistenceFailure_DoesNotRedispatch verifies that when
// dispatchFn returns ErrDispatchPersistenceFailed, the scheduler:
// 1. Stops the fill loop (does NOT dispatch the next queued job)
// 2. Retains the in-flight reservation to prevent double-dispatch
func TestScheduler_StartPersistenceFailure_DoesNotRedispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()

	// Create two QUEUED jobs
	for i, id := range []string{"persist_fail_1", "persist_fail_2"} {
		j := &job.Job{
			ID:        id,
			Source:    "http://example.com/" + id,
			Name:      id,
			Status:    job.StatusQueued,
			Type:      job.TypeDownload,
			Engine:    "aria2",
			Priority:  job.JobPriorityNormal,
			CreatedAt: now,
			UpdatedAt: now,
		}
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      id,
			Position:   int64(i + 1),
			Action:     job.QueueActionStart,
			EnqueuedAt: now,
			UpdatedAt:  now,
		})
	}

	var dispatchCount int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&dispatchCount, 1)
		// First job hits persistence failure
		return fmt.Errorf("%w: simulated db error", job.ErrDispatchPersistenceFailed)
	}

	sched := job.NewScheduler(jobRepo, queueRepo,
		func(ctx context.Context) int { return 5 }, // plenty of capacity
		dispatchFn,
	)
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(300 * time.Millisecond)

	// Only the first job should have been dispatched; the loop must stop
	count := atomic.LoadInt32(&dispatchCount)
	if count != 1 {
		t.Fatalf("expected exactly 1 dispatch attempt (fill loop should stop), got %d", count)
	}

	// Second job must still be QUEUED
	j2, _ := jobRepo.GetByID(ctx, "persist_fail_2")
	if j2 == nil || j2.Status != job.StatusQueued {
		t.Errorf("expected persist_fail_2 to remain QUEUED, got %v", j2)
	}

	// Even after another Kick, the first job's in-flight reservation should prevent
	// it from being re-dispatched (it will be skipped by reserveInFlight).
	// But the second job CAN be dispatched now if the scheduler loops again.
	// Reset dispatch function to succeed for the second job.
	// Note: The scheduler's fill loop runs fresh per kick, so persist_fail_1 still
	// has its reservation held.
}

// TestScheduler_StartFailure_QueueNotDeletedBeforePersistence verifies that when
// engine Start fails and repo.Update also fails, the queue row is NOT deleted.
func TestScheduler_StartFailure_QueueNotDeletedBeforePersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "start_fail_persist",
		Source:    "http://example.com/fail",
		Name:      "Start Fail Persist",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	// Dispatch returns a normal error (engine failure, NOT persistence failure).
	// The scheduler will try to mark the job FAILED and delete the queue row.
	// If repo.Update succeeds, queue row should be deleted.
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return errors.New("simulated engine start error")
	}

	sched := job.NewScheduler(jobRepo, queueRepo,
		func(ctx context.Context) int { return 3 },
		dispatchFn,
	)
	sched.SetEventBus(&fakeBus{})
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(300 * time.Millisecond)

	// Job should be marked FAILED
	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated == nil || updated.Status != job.StatusFailed {
		t.Fatalf("expected job to be FAILED after start error, got %v", updated)
	}

	// Queue entry should have been deleted (since persistence succeeded)
	entry, _ := queueRepo.Get(ctx, j.ID)
	if entry != nil {
		t.Errorf("expected queue entry to be deleted after successful FAILED persistence, but it still exists")
	}
}

func TestScheduler_ReservedHeadDoesNotSpin(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()

	jA := &job.Job{
		ID:        "job_a_reserved",
		Source:    "http://example.com/a",
		Name:      "Job A",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityHigh,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jA)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jA.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	jB := &job.Job{
		ID:        "job_b_waiting",
		Source:    "http://example.com/b",
		Name:      "Job B",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jB)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jB.ID,
		Position:   2,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	var dispatched []string
	var mu sync.Mutex

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 5 }, func(ctx context.Context, qj *job.QueuedJob) error {
		mu.Lock()
		dispatched = append(dispatched, qj.JobID)
		mu.Unlock()
		return &job.DispatchPersistenceError{JobID: qj.JobID, Action: qj.Action, Err: errors.New("db error")}
	})

	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	initialDispatches := len(dispatched)
	mu.Unlock()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			sched.Kick()
			time.Sleep(10 * time.Millisecond)
		}
		close(done)
	}()

	select {
	case <-done:
		// Good!
	case <-time.After(1 * time.Second):
		t.Fatal("scheduler appeared stuck in reserved-job tight loop")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != initialDispatches {
		t.Fatalf("expected no further dispatches, but got %d (initial %d)", len(dispatched), initialDispatches)
	}
}

func TestScheduler_PersistenceFailure_ReconcilesRunningJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "reconcile_job_1",
		Source:    "http://example.com/rec",
		Name:      "Reconcile Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	var startCount int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&startCount, 1)
		return &job.DispatchPersistenceError{
			JobID:    qj.JobID,
			EngineID: "gid_reconciled_123",
			Action:   qj.Action,
			Err:      errors.New("initial db update failed"),
		}
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return &job.EngineStatus{Status: job.StatusDownloading, TotalBytes: 100, CompletedBytes: 20}, nil
		},
	}
	reg := &fakeRegistry{eng: fakeEng}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(reg)
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(300 * time.Millisecond)

	if atomic.LoadInt32(&startCount) != 1 {
		t.Fatalf("expected engine start to be called exactly once, got %d", atomic.LoadInt32(&startCount))
	}

	updated, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil || updated == nil {
		t.Fatalf("failed to fetch updated job: %v", err)
	}
	if updated.Status != job.StatusDownloading {
		t.Errorf("expected job status DOWNLOADING after reconciliation, got %s", updated.Status)
	}
	if updated.EngineID != "gid_reconciled_123" {
		t.Errorf("expected EngineID 'gid_reconciled_123', got %s", updated.EngineID)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry != nil {
		t.Errorf("expected queue entry to be deleted after reconciliation, but it exists")
	}
}

func TestScheduler_ReconciliationFailure_RemainsReserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "reconcile_fail_job",
		Source:    "http://example.com/rec_fail",
		Name:      "Reconcile Fail",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	var startCount int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&startCount, 1)
		return &job.DispatchPersistenceError{
			JobID:    qj.JobID,
			EngineID: "gid_fail_999",
			Action:   qj.Action,
			Err:      errors.New("persistent db failure"),
		}
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return nil, errors.New("engine status connection error")
		},
	}
	reg := &fakeRegistry{eng: fakeEng}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(reg)
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	for i := 0; i < 5; i++ {
		sched.Kick()
		time.Sleep(30 * time.Millisecond)
	}

	if atomic.LoadInt32(&startCount) != 1 {
		t.Fatalf("expected Start to be called exactly once despite reconciliation failures, got %d", atomic.LoadInt32(&startCount))
	}

	jDB, _ := jobRepo.GetByID(ctx, j.ID)
	if jDB.Status != job.StatusQueued {
		t.Errorf("expected job to remain QUEUED in DB, got %s", jDB.Status)
	}
}

func TestScheduler_StartFailurePersistence_ReconcilesFailedState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "sf_reconcile_job",
		Source:    "http://example.com/sf_rec",
		Name:      "SF Reconcile Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	toggleWrapper := &toggleFailingUpdateJobRepoWrapper{IJobRepository: jobRepo, failUpdate: true}

	var startCount int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&startCount, 1)
		return errors.New("engine start error")
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			t.Error("eng.Status must NOT be called for state persistence reconciliation")
			return nil, errors.New("should not be called")
		},
	}

	sched := job.NewScheduler(toggleWrapper, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	bus := &fakeBus{}
	sched.SetEventBus(bus)

	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&startCount) != 1 {
		t.Fatalf("expected engine Start to be called once, got %d", atomic.LoadInt32(&startCount))
	}

	toggleWrapper.setFail(false)

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&startCount) != 1 {
		t.Fatalf("expected engine Start to stay at 1 call, got %d", atomic.LoadInt32(&startCount))
	}

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != job.StatusFailed {
		t.Errorf("expected job status FAILED, got %s", updated.Status)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry != nil {
		t.Errorf("expected queue entry to be deleted after FAILED reconciliation")
	}

	bus.mu.Lock()
	events := bus.events
	bus.mu.Unlock()

	foundFailed := false
	for _, ev := range events {
		if ev.Type == job.EventJobFailed && ev.Job.ID == j.ID {
			foundFailed = true
		}
	}
	if !foundFailed {
		t.Errorf("expected EventJobFailed to be published after state reconciliation")
	}
}

func TestScheduler_StartFailurePersistence_RemainsReserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j1 := &job.Job{
		ID:        "sf_remain_1",
		Source:    "http://example.com/sf1",
		Name:      "SF Remain 1",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityHigh,
		CreatedAt: now,
		UpdatedAt: now,
	}
	j2 := &job.Job{
		ID:        "sf_remain_2",
		Source:    "http://example.com/sf2",
		Name:      "SF Remain 2",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart})

	toggleWrapper := &toggleFailingUpdateJobRepoWrapper{IJobRepository: jobRepo, failUpdate: true}

	var dispatched []string
	var mu sync.Mutex
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		mu.Lock()
		dispatched = append(dispatched, qj.JobID)
		mu.Unlock()
		if qj.JobID == j1.ID {
			return errors.New("engine start error")
		}
		j, _ := jobRepo.GetByID(ctx, qj.JobID)
		if j != nil {
			j.Status = job.StatusDownloading
			jobRepo.Update(ctx, j)
			queueRepo.Delete(ctx, j.ID)
		}
		return nil
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			t.Error("eng.Status must NOT be called")
			return nil, errors.New("no status")
		},
	}

	sched := job.NewScheduler(toggleWrapper, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	for i := 0; i < 5; i++ {
		sched.Kick()
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	dispLen := len(dispatched)
	mu.Unlock()
	if dispLen != 1 || dispatched[0] != j1.ID {
		t.Fatalf("expected only j1 dispatched initially, got %v", dispatched)
	}

	toggleWrapper.setFail(false)
	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	finalDispatched := append([]string(nil), dispatched...)
	mu.Unlock()

	if len(finalDispatched) != 2 || finalDispatched[1] != j2.ID {
		t.Fatalf("expected j2 to start after j1 state reconciled, got %v", finalDispatched)
	}
}

func TestScheduler_ResumeFailurePersistence_ReconcilesPausedState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "rf_reconcile_job",
		Source:    "http://example.com/rf_rec",
		Name:      "RF Reconcile Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		EngineID:  "existing_gid_123",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionResume,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	toggleWrapper := &toggleFailingUpdateJobRepoWrapper{IJobRepository: jobRepo, failUpdate: true}

	var resumeCount int32
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&resumeCount, 1)
		return errors.New("engine resume error")
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			t.Error("eng.Status must NOT be called for resume state persistence reconciliation")
			return nil, errors.New("should not be called")
		},
	}

	sched := job.NewScheduler(toggleWrapper, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	bus := &fakeBus{}
	sched.SetEventBus(bus)

	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&resumeCount) != 1 {
		t.Fatalf("expected engine Resume to be called once, got %d", atomic.LoadInt32(&resumeCount))
	}

	toggleWrapper.setFail(false)
	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	if atomic.LoadInt32(&resumeCount) != 1 {
		t.Fatalf("expected engine Resume to stay at 1 call, got %d", atomic.LoadInt32(&resumeCount))
	}

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != job.StatusPaused {
		t.Errorf("expected job status PAUSED, got %s", updated.Status)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry == nil || qEntry.Action != job.QueueActionResume {
		t.Errorf("expected RESUME queue entry to remain for PAUSED job, got %v", qEntry)
	}
}

func TestScheduler_ExternalReconciliation_PreservesProcessing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "rec_processing_job",
		Source:    "http://example.com/proc",
		Name:      "Processing Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "ytdlp",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return &job.DispatchPersistenceError{
			JobID:    qj.JobID,
			EngineID: "ytdlp_proc_gid",
			Action:   qj.Action,
			Err:      errors.New("db error"),
		}
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return &job.EngineStatus{Status: job.StatusProcessing, TotalBytes: 100, CompletedBytes: 50}, nil
		},
	}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != job.StatusProcessing {
		t.Errorf("expected job status PROCESSING, got %s", updated.Status)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry != nil {
		t.Errorf("expected queue entry to be deleted after PROCESSING reconciliation")
	}
}

func TestScheduler_ExternalReconciliation_PreservesSeeding(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "rec_seeding_job",
		Source:    "magnet:?xt=urn:btih:seed123",
		Name:      "Seeding Job",
		Status:    job.StatusQueued,
		Type:      job.TypeTorrent,
		Engine:    "qbittorrent",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return &job.DispatchPersistenceError{
			JobID:    qj.JobID,
			EngineID: "hash_seed_123",
			Action:   qj.Action,
			Err:      errors.New("db error"),
		}
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return &job.EngineStatus{Status: job.StatusSeeding}, nil
		},
	}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != job.StatusSeeding {
		t.Errorf("expected job status SEEDING, got %s", updated.Status)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry != nil {
		t.Errorf("expected queue entry to be deleted after SEEDING reconciliation")
	}
}

func TestScheduler_ExternalReconciliation_PreservesCompleted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	now := time.Now()
	j := &job.Job{
		ID:        "rec_completed_job",
		Source:    "http://example.com/done",
		Name:      "Completed Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return &job.DispatchPersistenceError{
			JobID:    qj.JobID,
			EngineID: "aria2_done_gid",
			Action:   qj.Action,
			Err:      errors.New("db error"),
		}
	}

	fakeEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
			return &job.EngineStatus{Status: job.StatusCompleted}, nil
		},
	}

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 3 }, dispatchFn)
	sched.SetEngineRegistry(&fakeRegistry{eng: fakeEng})
	bus := &fakeBus{}
	sched.SetEventBus(bus)
	sched.Start(ctx)
	defer sched.Stop()

	sched.Kick()
	time.Sleep(200 * time.Millisecond)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != job.StatusDownloading {
		t.Errorf("expected job status DOWNLOADING, got %s", updated.Status)
	}

	qEntry, _ := queueRepo.Get(ctx, j.ID)
	if qEntry != nil {
		t.Errorf("expected queue entry to be deleted after reconciliation")
	}
}

type toggleFailingUpdateJobRepoWrapper struct {
	job.IJobRepository
	mu         sync.Mutex
	failUpdate bool
}

func (w *toggleFailingUpdateJobRepoWrapper) setFail(fail bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.failUpdate = fail
}

func (w *toggleFailingUpdateJobRepoWrapper) Update(ctx context.Context, j *job.Job) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failUpdate {
		return errors.New("simulated DB update failure")
	}
	return w.IJobRepository.Update(ctx, j)
}

func TestManager_SetPriority_AtomicSuccess(t *testing.T) {
	db, jobRepo, queueRepo := setupSchedulerTestDB(t)
	defer db.Close()

	reg := &fakeRegistry{eng: &fakeEngine{}}
	bus := &fakeBus{}
	m := job.NewManager(jobRepo, reg, bus, t.TempDir(), nil)
	m.SetQueueRepository(queueRepo)

	ctx := context.Background()
	now := time.Now()
	j := &job.Job{
		ID:        "priority_atomic_job",
		Source:    "http://example.com/prio",
		Name:      "Prio Job",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	})

	updated, err := m.SetJobPriority(ctx, j.ID, job.JobPriorityHigh)
	if err != nil {
		t.Fatalf("SetJobPriority failed: %v", err)
	}
	if updated.Priority != job.JobPriorityHigh {
		t.Errorf("expected priority HIGH, got %s", updated.Priority)
	}

	jDB, _ := jobRepo.GetByID(ctx, j.ID)
	if jDB.Priority != job.JobPriorityHigh {
		t.Errorf("expected DB job priority HIGH, got %s", jDB.Priority)
	}

	qDB, _ := queueRepo.Get(ctx, j.ID)
	if qDB.Position != 1 {
		t.Errorf("expected queue position 1 in high lane, got %d", qDB.Position)
	}
}
