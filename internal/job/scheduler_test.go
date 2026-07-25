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

type fakeBus struct{}

func (f *fakeBus) Publish(event job.Event)                                {}
func (f *fakeBus) Subscribe() <-chan job.Event                            { return nil }
func (f *fakeBus) Unsubscribe(ch <-chan job.Event)                        {}
func (f *fakeBus) SubscribeType(eventType string) <-chan job.Event        { return nil }
func (f *fakeBus) UnsubscribeType(eventType string, ch <-chan job.Event) {}

type fakeEngine struct {
	statusFunc func(ctx context.Context, j *job.Job) (*job.EngineStatus, error)
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
