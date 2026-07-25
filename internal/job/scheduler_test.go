package job_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
)

func setupSchedulerTest(t *testing.T) (*database.SQLiteJobRepository, *database.SQLiteQueueRepository, *database.DB) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_scheduler.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)

	return jobRepo, queueRepo, db
}

func TestScheduler_PriorityDispatchAndSlotLimit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "test_scheduler.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)

	limitFn := func(ctx context.Context) int {
		return 1
	}

	var dispatchedCount int32
	dispatchedCh := make(chan string, 10)

	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		atomic.AddInt32(&dispatchedCount, 1)
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

	// 1. Create a low priority job
	jLow := &job.Job{
		ID:        "job_low",
		Source:    "http://example.com/low",
		Name:      "Low Priority",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityLow,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jLow)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jLow.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: now, UpdatedAt: now})

	// 2. Create a high priority job
	jHigh := &job.Job{
		ID:        "job_high",
		Source:    "http://example.com/high",
		Name:      "High Priority",
		Status:    job.StatusQueued,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		Priority:  job.JobPriorityHigh,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jHigh)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jHigh.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: now, UpdatedAt: now})

	// Kick scheduler
	sched.Kick()

	select {
	case dispatchedID := <-dispatchedCh:
		if dispatchedID != "job_high" {
			t.Errorf("expected high priority job to dispatch first, got %s", dispatchedID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for dispatch")
	}

	// Because capacity limit is 1 and job_high is now DOWNLOADING, job_low must NOT dispatch yet
	select {
	case secondID := <-dispatchedCh:
		t.Fatalf("job_low should not dispatch while capacity (1) is occupied by job_high, but %s dispatched", secondID)
	case <-time.After(300 * time.Millisecond):
		// Expected: capacity full
	}

	// Transition job_high to COMPLETED (releases slot)
	jHigh.Status = job.StatusCompleted
	jobRepo.Update(ctx, jHigh)

	sched.Kick()

	select {
	case dispatchedID := <-dispatchedCh:
		if dispatchedID != "job_low" {
			t.Errorf("expected low priority job to dispatch after slot freed, got %s", dispatchedID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second job dispatch")
	}
}

func TestScheduler_ProcessingAndSeedingReleaseSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "test_slots.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 1 }, func(ctx context.Context, qj *job.QueuedJob) error { return nil })

	now := time.Now()

	jProc := &job.Job{
		ID:        "job_proc",
		Source:    "http://example.com/proc",
		Name:      "Processing Job",
		Status:    job.StatusProcessing,
		Type:      job.TypeMedia,
		Engine:    "ytdlp",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jProc)

	jSeed := &job.Job{
		ID:        "job_seed",
		Source:    "magnet:?xt=urn:btih:123",
		Name:      "Seeding Job",
		Status:    job.StatusSeeding,
		Type:      job.TypeTorrent,
		Engine:    "qbittorrent",
		Priority:  job.JobPriorityNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
	jobRepo.Create(ctx, jSeed)

	runningCount, err := jobRepo.CountDownloading(ctx)
	if err != nil {
		t.Fatalf("count downloading error: %v", err)
	}
	if runningCount != 0 {
		t.Errorf("expected 0 slots consumed for PROCESSING & SEEDING jobs, got %d", runningCount)
	}

	_ = sched
}
