package job_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/networkpolicy"
	"downloader/internal/storage"
)

func setupPolicyTestEnv(t *testing.T) (*database.DB, *job.Manager, *database.SQLiteJobRepository, *database.SQLiteQueueRepository, *database.SQLiteExecutionRepository, *job.Scheduler, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_policy.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	storageSvc := storage.NewStorageService(nil, nil, nil, tempDir, tempDir)

	downloadDir := filepath.Join(tempDir, "downloads")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		t.Fatalf("failed to create downloadDir: %v", err)
	}

	bus := events.NewInMemoryBus()
	engReg := engine.NewRegistry()
	mgr := job.NewManager(jobRepo, engReg, bus, downloadDir, nil, tempDir)
	mgr.SetStorageService(storageSvc)
	mgr.SetExecutionRepository(execRepo)
	mgr.SetQueueRepository(queueRepo)

	limitFn := func(ctx context.Context) int { return 3 }
	dispatchFn := func(ctx context.Context, qj *job.QueuedJob) error {
		return mgr.DispatchQueuedJob(ctx, qj)
	}
	sched := job.NewScheduler(jobRepo, queueRepo, limitFn, dispatchFn)
	sched.SetEventBus(bus)
	mgr.SetScheduler(sched)

	return db, mgr, jobRepo, queueRepo, execRepo, sched, dbPath
}

// Test A: higher base priority wins initially
func TestSchedulerPolicy_A_HigherBasePriorityWinsInitially(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Create 3 jobs at t0 with different priorities
	jLow := &job.Job{ID: "j-low", Status: job.StatusQueued, Priority: job.JobPriorityLow, CreatedAt: t0, UpdatedAt: t0}
	jNorm := &job.Job{ID: "j-norm", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jHigh := &job.Job{ID: "j-high", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t0, UpdatedAt: t0}

	for _, j := range []*job.Job{jLow, jNorm, jHigh} {
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job %s: %v", j.ID, err)
		}
		if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      j.ID,
			Position:   1,
			Action:     job.QueueActionStart,
			EnqueuedAt: t0,
			UpdatedAt:  t0,
		}); err != nil {
			t.Fatalf("enqueue job %s: %v", j.ID, err)
		}
	}

	// At t0 with wait=0, High must win initially
	best, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best == nil || best.JobID != "j-high" {
		t.Fatalf("expected j-high to win initially, got %+v", best)
	}
}

// Test B: stable FIFO for equal priority
func TestSchedulerPolicy_B_FIFOForEqualPriority(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	j1 := &job.Job{ID: "j-pos1", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j-pos2", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}

	for _, j := range []*job.Job{j1, j2} {
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
	}
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	best, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best == nil || best.JobID != "j-pos1" {
		t.Fatalf("expected position 1 to win over position 2, got %+v", best)
	}
}

// Test C: deterministic stable final tie-break
func TestSchedulerPolicy_C_DeterministicStableFinalTieBreak(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Equal priority, equal position, equal enqueued_at
	jB := &job.Job{ID: "job-bbb", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jA := &job.Job{ID: "job-aaa", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}

	for _, j := range []*job.Job{jB, jA} {
		jobRepo.Create(ctx, j)
		queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	}

	// Tie-break by JobID: "job-aaa" < "job-bbb"
	for round := 1; round <= 3; round++ {
		best, err := queueRepo.NextRunnable(ctx, t0)
		if err != nil {
			t.Fatalf("round %d NextRunnable failed: %v", round, err)
		}
		if best == nil || best.JobID != "job-aaa" {
			t.Fatalf("round %d: expected stable tie-break to select job-aaa, got %+v", round, best)
		}
	}
}

// Test D: aging increases rank monotonically
func TestSchedulerPolicy_D_AgingIncreasesRankMonotonically(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cfg := job.DefaultAgingConfig()
	qj := &job.QueuedJob{
		JobID:      "job-test",
		Position:   1,
		EnqueuedAt: t0,
		Job:        job.Job{ID: "job-test", Priority: job.JobPriorityLow},
	}

	prevRank := int64(-1)
	for min := 0; min <= 100; min++ {
		evalTime := t0.Add(time.Duration(min) * time.Minute)
		rank := job.EffectivePriority(qj, evalTime, cfg)
		if rank < prevRank {
			t.Fatalf("effective rank decreased at minute %d: prev=%d, curr=%d", min, prevRank, rank)
		}
		prevRank = rank
	}
	// At minute 50 and 100, rank should be capped at 0 + 250 = 250
	if rank50 := job.EffectivePriority(qj, t0.Add(50*time.Minute), cfg); rank50 != 250 {
		t.Fatalf("expected rank 250 at 50min, got %d", rank50)
	}
	if rank100 := job.EffectivePriority(qj, t0.Add(100*time.Minute), cfg); rank100 != 250 {
		t.Fatalf("expected rank 250 at 100min, got %d", rank100)
	}
}

// Test E: low-priority old job eventually outranks newer high-priority work (starvation test)
func TestSchedulerPolicy_E_LowPriorityOldJobEventuallyOutranksNewerHighPriorityWork(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Low job queued at t0
	jLow := &job.Job{ID: "j-low-old", Status: job.StatusQueued, Priority: job.JobPriorityLow, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jLow)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jLow.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// At t0 + 45 minutes, a new High job arrives
	t45 := t0.Add(45 * time.Minute)
	jHigh := &job.Job{ID: "j-high-new", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t45, UpdatedAt: t45}
	jobRepo.Create(ctx, jHigh)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jHigh.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t45, UpdatedAt: t45})

	// When evaluated at t45:
	// jLow has waited 45 minutes: score = 0 + (45 * 5 = 225) = 225
	// jHigh has waited 0 minutes: score = 200 + 0 = 200
	// jLow must outrank jHigh!
	best, err := queueRepo.NextRunnable(ctx, t45)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best == nil || best.JobID != "j-low-old" {
		t.Fatalf("expected aged low job j-low-old to outrank newly arrived high job, got %+v", best)
	}
}

// Test F: restart preserves decision
func TestSchedulerPolicy_F_RestartPreservesDecision(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, dbPath := setupPolicyTestEnv(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jLow := &job.Job{ID: "j-low-old", Status: job.StatusQueued, Priority: job.JobPriorityLow, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jLow)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jLow.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	t45 := t0.Add(45 * time.Minute)
	jHigh := &job.Job{ID: "j-high-new", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t45, UpdatedAt: t45}
	jobRepo.Create(ctx, jHigh)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jHigh.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t45, UpdatedAt: t45})

	// Decision before close
	decisionBefore, err := queueRepo.NextRunnable(ctx, t45)
	if err != nil {
		t.Fatalf("NextRunnable before close failed: %v", err)
	}
	db.Close()

	// Reopen DB
	reopenedDB, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen db: %v", err)
	}
	defer reopenedDB.Close()

	reopenedQueueRepo := database.NewSQLiteQueueRepository(reopenedDB)
	decisionAfter, err := reopenedQueueRepo.NextRunnable(ctx, t45)
	if err != nil {
		t.Fatalf("NextRunnable after reopen failed: %v", err)
	}

	if decisionBefore.JobID != decisionAfter.JobID {
		t.Fatalf("restart inconsistency: before=%s, after=%s", decisionBefore.JobID, decisionAfter.JobID)
	}
	if decisionAfter.JobID != "j-low-old" {
		t.Fatalf("expected j-low-old after restart, got %s", decisionAfter.JobID)
	}
}

// Test G: future not_before job is ineligible
func TestSchedulerPolicy_G_FutureNotBeforeJobIsIneligible(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	futureTime := t0.Add(15 * time.Minute)
	jFuture := &job.Job{ID: "j-future", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jFuture)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jFuture.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		NotBefore:  &futureTime,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	// Evaluated at t0: futureTime is in future, so job is NOT eligible
	best, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best != nil {
		t.Fatalf("expected no eligible jobs, got %+v", best)
	}
}

// Test H: not_before job becomes eligible when clock advances
func TestSchedulerPolicy_H_NotBeforeJobBecomesEligibleWhenClockAdvances(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	futureTime := t0.Add(15 * time.Minute)
	jFuture := &job.Job{ID: "j-future", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jFuture)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jFuture.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		NotBefore:  &futureTime,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	// Advance clock past futureTime
	t16 := t0.Add(16 * time.Minute)
	best, err := queueRepo.NextRunnable(ctx, t16)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best == nil || best.JobID != "j-future" {
		t.Fatalf("expected j-future to become eligible at t16, got %+v", best)
	}
}

// Test I: Run Now promotes queued work
func TestSchedulerPolicy_I_RunNowPromotesQueuedWork(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jHigh := &job.Job{ID: "j-high", Status: job.StatusQueued, Priority: job.JobPriorityHigh, CreatedAt: t0, UpdatedAt: t0}
	jLow := &job.Job{ID: "j-low", Status: job.StatusQueued, Priority: job.JobPriorityLow, CreatedAt: t0, UpdatedAt: t0}

	jobRepo.Create(ctx, jHigh)
	jobRepo.Create(ctx, jLow)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jHigh.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jLow.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Before Run Now, jHigh is next
	best, _ := queueRepo.NextRunnable(ctx, t0)
	if best.JobID != "j-high" {
		t.Fatalf("expected j-high before Run Now, got %s", best.JobID)
	}

	// Invoke Run Now on jLow
	updatedJob, err := mgr.RunNow(ctx, jLow.ID)
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}
	if updatedJob.Status != job.StatusQueued {
		t.Fatalf("expected status queued, got %s", updatedJob.Status)
	}

	// Now jLow must be selected immediately
	bestAfter, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable after Run Now failed: %v", err)
	}
	if bestAfter == nil || bestAfter.JobID != "j-low" {
		t.Fatalf("expected j-low to be promoted by Run Now, got %+v", bestAfter)
	}
	if bestAfter.Position != 0 {
		t.Fatalf("expected position 0 for Run Now, got %d", bestAfter.Position)
	}
}

// Test J: Run Now does not exceed concurrency capacity
func TestSchedulerPolicy_J_RunNowDoesNotExceedConcurrencyCapacity(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, sched, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	var dispatchedCount int32
	// Custom scheduler with limit=2
	customSched := job.NewScheduler(
		jobRepo,
		queueRepo,
		func(ctx context.Context) int { return 2 },
		func(ctx context.Context, qj *job.QueuedJob) error {
			atomic.AddInt32(&dispatchedCount, 1)
			qj.Job.Status = job.StatusDownloading
			jobRepo.Update(ctx, &qj.Job)
			queueRepo.Delete(ctx, qj.JobID)
			return nil
		},
	)
	mgr.SetScheduler(customSched)
	_ = sched

	// Create 2 already downloading jobs
	jobRepo.Create(ctx, &job.Job{ID: "running-1", Status: job.StatusDownloading, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	jobRepo.Create(ctx, &job.Job{ID: "running-2", Status: job.StatusDownloading, CreatedAt: time.Now(), UpdatedAt: time.Now()})

	// Create queued job
	jQueued := &job.Job{ID: "queued-runnow", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, jQueued)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jQueued.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})

	customSched.Start(ctx)
	defer customSched.Stop()

	// Call Run Now on queued job
	_, err := mgr.RunNow(ctx, jQueued.ID)
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}

	// Give scheduler a brief moment to evaluate capacity
	time.Sleep(50 * time.Millisecond)

	// Since limit is 2 and running count is 2, job must NOT be dispatched yet
	if count := atomic.LoadInt32(&dispatchedCount); count != 0 {
		t.Fatalf("expected 0 dispatches due to capacity limit, got %d", count)
	}

	// Complete one running job to free capacity
	running1, _ := jobRepo.GetByID(ctx, "running-1")
	running1.Status = job.StatusCompleted
	jobRepo.Update(ctx, running1)

	// Kick scheduler
	customSched.Kick()
	time.Sleep(100 * time.Millisecond)

	// Now it must be dispatched!
	if count := atomic.LoadInt32(&dispatchedCount); count != 1 {
		t.Fatalf("expected 1 dispatch after capacity freed, got %d", count)
	}
}

// Test K: Run Now behavior with future not_before is explicit (clears delay)
func TestSchedulerPolicy_K_RunNowWithFutureNotBeforeClearsDelay(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	futureTime := t0.Add(1 * time.Hour)
	jDelay := &job.Job{ID: "j-delayed", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jDelay)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jDelay.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		NotBefore:  &futureTime,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	// Before Run Now, not runnable at t0
	best, _ := queueRepo.NextRunnable(ctx, t0)
	if best != nil {
		t.Fatalf("expected nil runnable before Run Now, got %+v", best)
	}

	// Call Run Now
	_, err := mgr.RunNow(ctx, jDelay.ID)
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}

	// Queue entry must now have not_before cleared
	entry, err := queueRepo.Get(ctx, jDelay.ID)
	if err != nil {
		t.Fatalf("get queue entry failed: %v", err)
	}
	if entry.NotBefore != nil {
		t.Fatalf("expected NotBefore to be cleared to nil, got %v", entry.NotBefore)
	}

	// Immediately runnable now!
	bestNow, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if bestNow == nil || bestNow.JobID != "j-delayed" {
		t.Fatalf("expected j-delayed to be immediately runnable, got %+v", bestNow)
	}
}

// Test L: retry_count mutation semantics
func TestSchedulerPolicy_L_RetryCountMutationSemantics(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	// Initial job
	j := &job.Job{ID: "j-retry-test", Status: job.StatusFailed, Error: "connection timeout", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, j)

	// Manual Retry
	retriedJob, err := mgr.Retry(ctx, j.ID)
	if err != nil {
		t.Fatalf("Retry failed: %v", err)
	}
	if retriedJob.Status != job.StatusQueued {
		t.Fatalf("expected status queued, got %s", retriedJob.Status)
	}

	// Verify retry_count is 1
	entry, err := queueRepo.Get(ctx, j.ID)
	if err != nil {
		t.Fatalf("get queue entry failed: %v", err)
	}
	if entry.RetryCount != 1 {
		t.Fatalf("expected RetryCount 1, got %d", entry.RetryCount)
	}

	// Schedule second retry
	_, err = mgr.ScheduleRetry(ctx, j.ID, 30*time.Second)
	if err != nil {
		t.Fatalf("ScheduleRetry failed: %v", err)
	}
	entry2, _ := queueRepo.Get(ctx, j.ID)
	if entry2.RetryCount != 2 {
		t.Fatalf("expected RetryCount 2 after second retry, got %d", entry2.RetryCount)
	}
	if entry2.NotBefore == nil {
		t.Fatalf("expected NotBefore to be set for scheduled retry")
	}
}

// Test M: manual queue reorder remains deterministic
func TestSchedulerPolicy_M_ManualQueueReorderRemainsDeterministic(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	j1 := &job.Job{ID: "j-reorder-1", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	j2 := &job.Job{ID: "j-reorder-2", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}

	jobRepo.Create(ctx, j1)
	jobRepo.Create(ctx, j2)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j1.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j2.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Reorder to: [j2, j1]
	err := mgr.ReorderQueue(ctx, job.JobPriorityNormal, []string{j2.ID, j1.ID})
	if err != nil {
		t.Fatalf("ReorderQueue failed: %v", err)
	}

	best, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if best == nil || best.JobID != j2.ID {
		t.Fatalf("expected j-reorder-2 to win after reorder, got %+v", best)
	}
}

// Test N: concurrent scheduling cannot dispatch same job twice
func TestSchedulerPolicy_N_ConcurrentSchedulingCannotDispatchSameJobTwice(t *testing.T) {
	db, _, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	j := &job.Job{ID: "job-single-flight", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})

	var dispatchCount int32
	sched := job.NewScheduler(
		jobRepo,
		queueRepo,
		func(ctx context.Context) int { return 5 },
		func(ctx context.Context, qj *job.QueuedJob) error {
			atomic.AddInt32(&dispatchCount, 1)
			// Simulate dispatch work
			time.Sleep(10 * time.Millisecond)
			j.Status = job.StatusDownloading
			jobRepo.Update(ctx, j)
			queueRepo.Delete(ctx, j.ID)
			return nil
		},
	)

	sched.Start(ctx)
	defer sched.Stop()

	// Launch 15 concurrent Kicks
	var wg sync.WaitGroup
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sched.Kick()
		}()
	}
	wg.Wait()

	// Wait briefly for scheduler loop
	time.Sleep(100 * time.Millisecond)

	if count := atomic.LoadInt32(&dispatchCount); count != 1 {
		t.Fatalf("expected exactly 1 dispatch under concurrent kicks, got %d", count)
	}
}

// Test O: StateSync mutation event/cursor coverage for persisted changes
func TestSchedulerPolicy_O_StateSyncMutationEventCoverage(t *testing.T) {
	db, mgr, jobRepo, queueRepo, execRepo, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	cursorBefore, _ := execRepo.GetEventCursor(ctx, "global")

	j := &job.Job{ID: "j-statesync-test", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})

	// Run Now mutation
	_, err := mgr.RunNow(ctx, j.ID)
	if err != nil {
		t.Fatalf("RunNow failed: %v", err)
	}

	cursorAfterRunNow, _ := execRepo.GetEventCursor(ctx, "global")
	if cursorAfterRunNow <= cursorBefore {
		t.Fatalf("expected cursor advancement after Run Now: before=%d, after=%d", cursorBefore, cursorAfterRunNow)
	}

	// Priority mutation
	_, err = mgr.SetJobPriority(ctx, j.ID, job.JobPriorityHigh)
	if err != nil {
		t.Fatalf("SetJobPriority failed: %v", err)
	}

	cursorAfterPriority, _ := execRepo.GetEventCursor(ctx, "global")
	if cursorAfterPriority <= cursorAfterRunNow {
		t.Fatalf("expected cursor advancement after priority change: before=%d, after=%d", cursorAfterRunNow, cursorAfterPriority)
	}
}

// Test P: existing scheduler behavior remains compatible where policy is equal
func TestSchedulerPolicy_P_ExistingSchedulerBehaviorCompatible(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, sched, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	var dispatchedID string
	var mu sync.Mutex
	schedTest := job.NewScheduler(
		jobRepo,
		queueRepo,
		func(ctx context.Context) int { return 3 },
		func(ctx context.Context, qj *job.QueuedJob) error {
			mu.Lock()
			dispatchedID = qj.JobID
			mu.Unlock()
			qj.Job.Status = job.StatusDownloading
			jobRepo.Update(ctx, &qj.Job)
			queueRepo.Delete(ctx, qj.JobID)
			return nil
		},
	)
	_ = sched
	_ = mgr

	j := &job.Job{ID: "compat-job", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: j.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})

	schedTest.Start(ctx)
	defer schedTest.Stop()

	schedTest.Kick()
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	got := dispatchedID
	mu.Unlock()

	if got != "compat-job" {
		t.Fatalf("expected compat-job to be dispatched, got %q", got)
	}
}

type fakeTestEngine struct {
	resumed atomic.Bool
	started atomic.Bool
}

func (f *fakeTestEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{}
}
func (f *fakeTestEngine) Start(ctx context.Context, j *job.Job, downloadDir string) (string, error) {
	f.started.Store(true)
	return "fake-engine-id", nil
}
func (f *fakeTestEngine) Pause(ctx context.Context, j *job.Job) error { return nil }
func (f *fakeTestEngine) Resume(ctx context.Context, j *job.Job) error {
	f.resumed.Store(true)
	return nil
}
func (f *fakeTestEngine) Cancel(ctx context.Context, j *job.Job) error { return nil }
func (f *fakeTestEngine) Status(ctx context.Context, j *job.Job) (*job.EngineStatus, error) {
	return &job.EngineStatus{Status: job.StatusDownloading}, nil
}

// Test Q: Multiple Run Now jobs order deterministically (earlier requested Run Now wins)
func TestSchedulerPolicy_Q_MultipleRunNowDeterministicOrder(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jA := &job.Job{ID: "j-run-a", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jB := &job.Job{ID: "j-run-b", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jA)
	jobRepo.Create(ctx, jB)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jA.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jB.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Run Now A first
	if _, err := mgr.RunNow(ctx, jA.ID); err != nil {
		t.Fatalf("RunNow A: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	// Run Now B second
	if _, err := mgr.RunNow(ctx, jB.ID); err != nil {
		t.Fatalf("RunNow B: %v", err)
	}

	best, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable: %v", err)
	}
	if best == nil || best.JobID != jA.ID {
		t.Fatalf("expected j-run-a (requested first) to win, got %+v", best)
	}
}

// Test R: Multiple Run Now ordering survives DB reopen
func TestSchedulerPolicy_R_MultipleRunNowSurvivesDBReopen(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, dbPath := setupPolicyTestEnv(t)
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jA := &job.Job{ID: "j-persist-a", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jB := &job.Job{ID: "j-persist-b", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jA)
	jobRepo.Create(ctx, jB)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jA.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jB.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Run Now A first, then B
	mgr.RunNow(ctx, jA.ID)
	time.Sleep(10 * time.Millisecond)
	mgr.RunNow(ctx, jB.ID)

	// Close DB
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	// Reopen DB
	dbReopened, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer dbReopened.Close()

	queueRepoReopened := database.NewSQLiteQueueRepository(dbReopened)
	best, err := queueRepoReopened.NextRunnable(ctx, t0)
	if err != nil {
		t.Fatalf("NextRunnable after reopen: %v", err)
	}
	if best == nil || best.JobID != jA.ID {
		t.Fatalf("expected j-persist-a to win after reopen, got %+v", best)
	}
}

// Test S: ReorderQueue overrides Run Now intent
func TestSchedulerPolicy_S_ReorderQueueOverridesRunNow(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	jA := &job.Job{ID: "j-override-a", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jB := &job.Job{ID: "j-override-b", Status: job.StatusQueued, Priority: job.JobPriorityNormal, CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, jA)
	jobRepo.Create(ctx, jB)
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jA.ID, Position: 1, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})
	queueRepo.Enqueue(ctx, &job.QueueEntry{JobID: jB.ID, Position: 2, Action: job.QueueActionStart, EnqueuedAt: t0, UpdatedAt: t0})

	// Make jA Run Now
	mgr.RunNow(ctx, jA.ID)
	bestBefore, _ := queueRepo.NextRunnable(ctx, t0)
	if bestBefore.JobID != jA.ID {
		t.Fatalf("expected j-override-a before reorder, got %s", bestBefore.JobID)
	}

	// User explicitly reorders lane: [jB, jA]
	if err := mgr.ReorderQueue(ctx, job.JobPriorityNormal, []string{jB.ID, jA.ID}); err != nil {
		t.Fatalf("ReorderQueue: %v", err)
	}

	// Now jB gets position 1, jA gets position 2 -> Run Now consumed/overridden
	entryA, _ := queueRepo.Get(ctx, jA.ID)
	entryB, _ := queueRepo.Get(ctx, jB.ID)
	if entryA.Position != 2 || entryB.Position != 1 {
		t.Fatalf("expected posA=2, posB=1, got posA=%d, posB=%d", entryA.Position, entryB.Position)
	}

	bestAfter, _ := queueRepo.NextRunnable(ctx, t0)
	if bestAfter == nil || bestAfter.JobID != jB.ID {
		t.Fatalf("expected j-override-b to win after manual reorder, got %+v", bestAfter)
	}
}

// Test T: Paused Run Now actually dispatches through scheduler
func TestSchedulerPolicy_T_PausedRunNowActuallyDispatches(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	fakeEng := &fakeTestEngine{}
	mgr.GetEngineRegistry().(*engine.Registry).Register("fake", fakeEng)

	// Create a paused job that was actively downloading previously
	j := &job.Job{
		ID:        "j-paused-runnow",
		Status:    job.StatusPaused,
		Engine:    "fake",
		EngineID:  "gid-paused-123",
		Priority:  job.JobPriorityNormal,
		CreatedAt: t0,
		UpdatedAt: t0,
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionResume,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	var dispatchedCount int32
	sched := job.NewScheduler(
		jobRepo,
		queueRepo,
		func(ctx context.Context) int { return 2 },
		func(ctx context.Context, qj *job.QueuedJob) error {
			atomic.AddInt32(&dispatchedCount, 1)
			return mgr.DispatchQueuedJob(ctx, qj)
		},
	)
	mgr.SetScheduler(sched)
	sched.Start(ctx)
	defer sched.Stop()

	// Call Run Now on the paused job
	updatedJob, err := mgr.RunNow(ctx, j.ID)
	if err != nil {
		t.Fatalf("RunNow on paused job failed: %v", err)
	}
	if updatedJob.Status != job.StatusQueued {
		t.Fatalf("expected status queued after RunNow, got %s", updatedJob.Status)
	}

	// Verify entry has position 0 and action Resume
	entry, err := queueRepo.Get(ctx, j.ID)
	if err != nil || entry == nil {
		// Might already have been dispatched by scheduler!
	} else {
		if entry.Position != 0 {
			t.Fatalf("expected position 0, got %d", entry.Position)
		}
		if entry.Action != job.QueueActionResume {
			t.Fatalf("expected QueueActionResume, got %s", entry.Action)
		}
	}

	// Wait briefly for scheduler dispatch
	time.Sleep(100 * time.Millisecond)

	if count := atomic.LoadInt32(&dispatchedCount); count != 1 {
		t.Fatalf("expected exactly 1 dispatch through scheduler, got %d", count)
	}
	if !fakeEng.resumed.Load() {
		t.Fatalf("expected fakeEng.Resume to be invoked during dispatch")
	}

	// Verify job in DB is now DOWNLOADING
	freshJob, _ := jobRepo.GetByID(ctx, j.ID)
	if freshJob.Status != job.StatusDownloading {
		t.Fatalf("expected status DOWNLOADING in DB, got %s", freshJob.Status)
	}

	// Verify queue row was deleted
	qRow, _ := queueRepo.Get(ctx, j.ID)
	if qRow != nil {
		t.Fatalf("expected queue row to be deleted after dispatch, got %+v", qRow)
	}
}

// Test U: Active and terminal states rejected by Run Now
func TestSchedulerPolicy_U_RunNowRejectsActiveAndTerminalStates(t *testing.T) {
	db, mgr, jobRepo, _, _, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()

	activeStatuses := []job.JobStatus{
		job.StatusDownloading,
		job.StatusProcessing,
		job.StatusSeeding,
	}
	for _, st := range activeStatuses {
		jID := "j-active-" + string(st)
		jobRepo.Create(ctx, &job.Job{ID: jID, Status: st, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		_, err := mgr.RunNow(ctx, jID)
		if err == nil {
			t.Fatalf("expected RunNow to reject active status %s, got nil error", st)
		}
	}

	terminalStatuses := []job.JobStatus{
		job.StatusCompleted,
		job.StatusCancelled,
		job.StatusFailed,
	}
	for _, st := range terminalStatuses {
		jID := "j-term-" + string(st)
		jobRepo.Create(ctx, &job.Job{ID: jID, Status: st, CreatedAt: time.Now(), UpdatedAt: time.Now()})
		_, err := mgr.RunNow(ctx, jID)
		if err == nil {
			t.Fatalf("expected RunNow to reject terminal status %s, got nil error", st)
		}
	}
}

// Test V: retry_count survives dequeue -> failure -> requeue lifecycle
func TestSchedulerPolicy_V_RetryCountSurvivesDispatchFailureRequeue(t *testing.T) {
	db, mgr, jobRepo, queueRepo, execRepo, _, _ := setupPolicyTestEnv(t)
	defer db.Close()
	ctx := context.Background()
	t0 := time.Now()

	fakeEng := &fakeTestEngine{}
	mgr.GetEngineRegistry().(*engine.Registry).Register("fake", fakeEng)

	j := &job.Job{ID: "j-lifecycle-retry", Status: job.StatusQueued, Engine: "fake", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j)
	// Enqueued initially with retry_count = 1
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		RetryCount: 1,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	// 1. Scheduler dispatches job
	qj, err := queueRepo.NextRunnable(ctx, t0)
	if err != nil || qj == nil {
		t.Fatalf("NextRunnable failed: %v", err)
	}
	if err := mgr.DispatchQueuedJob(ctx, qj); err != nil {
		t.Fatalf("DispatchQueuedJob failed: %v", err)
	}

	// Verify queue row deleted
	deletedEntry, _ := queueRepo.Get(ctx, j.ID)
	if deletedEntry != nil {
		t.Fatalf("expected queue entry deleted upon dispatch")
	}

	// Verify execution recorded with attempt 2 (1 + 1)
	exec, err := execRepo.GetLatestExecution(ctx, j.ID)
	if err != nil || exec == nil {
		t.Fatalf("GetLatestExecution failed: %v", err)
	}
	if exec.AttemptNumber != 2 {
		t.Fatalf("expected attempt number 2, got %d", exec.AttemptNumber)
	}

	// 2. Execution fails
	freshJob, _ := jobRepo.GetByID(ctx, j.ID)
	freshJob.Status = job.StatusFailed
	freshJob.Error = "connection reset"
	jobRepo.Update(ctx, freshJob)

	// 3. Job is retried
	retriedJob, err := mgr.Retry(ctx, j.ID)
	if err != nil {
		t.Fatalf("Retry failed: %v", err)
	}
	if retriedJob.Status != job.StatusQueued {
		t.Fatalf("expected status queued after retry, got %s", retriedJob.Status)
	}

	// Verify retry_count became 2 (N+1)
	newEntry, err := queueRepo.Get(ctx, j.ID)
	if err != nil || newEntry == nil {
		t.Fatalf("get new queue entry failed: %v", err)
	}
	if newEntry.RetryCount != 2 {
		t.Fatalf("expected RetryCount 2 (survived dequeue/failure/requeue), got %d", newEntry.RetryCount)
	}
}

// Test W: retry_count remains durable across restart
func TestSchedulerPolicy_W_RetryCountDurableAcrossDBRestart(t *testing.T) {
	db, mgr, jobRepo, queueRepo, _, _, dbPath := setupPolicyTestEnv(t)
	ctx := context.Background()
	t0 := time.Now()

	fakeEng := &fakeTestEngine{}
	mgr.GetEngineRegistry().(*engine.Registry).Register("fake", fakeEng)

	j := &job.Job{ID: "j-restart-retry", Status: job.StatusQueued, Engine: "fake", CreatedAt: t0, UpdatedAt: t0}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		RetryCount: 1,
		EnqueuedAt: t0,
		UpdatedAt:  t0,
	})

	// Dispatch -> deletes queue entry, records execution attempt 2
	qj, _ := queueRepo.NextRunnable(ctx, t0)
	mgr.DispatchQueuedJob(ctx, qj)

	// Mark failed and retry -> retry_count = 2 in queue
	jFail, _ := jobRepo.GetByID(ctx, j.ID)
	jFail.Status = job.StatusFailed
	jobRepo.Update(ctx, jFail)
	mgr.Retry(ctx, j.ID)

	// Close DB
	db.Close()

	// Reopen DB
	db2, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	defer db2.Close()

	jobRepo2 := database.NewSQLiteJobRepository(db2)
	queueRepo2 := database.NewSQLiteQueueRepository(db2)
	execRepo2 := database.NewSQLiteExecutionRepository(db2)
	engReg2 := engine.NewRegistry()
	engReg2.Register("fake", fakeEng)
	mgr2 := job.NewManager(jobRepo2, engReg2, nil, t.TempDir(), nil, t.TempDir())
	mgr2.SetQueueRepository(queueRepo2)
	mgr2.SetExecutionRepository(execRepo2)

	// Assert queue entry still has retry_count 2 after reopen
	entryAfterReopen, err := queueRepo2.Get(ctx, j.ID)
	if err != nil || entryAfterReopen == nil {
		t.Fatalf("get entry after reopen: %v", err)
	}
	if entryAfterReopen.RetryCount != 2 {
		t.Fatalf("expected RetryCount 2 after reopen, got %d", entryAfterReopen.RetryCount)
	}

	// Call ScheduleRetry again -> increments to 3
	_, err = mgr2.ScheduleRetry(ctx, j.ID, 10*time.Second)
	if err != nil {
		t.Fatalf("ScheduleRetry after reopen: %v", err)
	}
	entryScheduled, _ := queueRepo2.Get(ctx, j.ID)
	if entryScheduled.RetryCount != 3 {
		t.Fatalf("expected RetryCount 3 after second retry on reopened DB, got %d", entryScheduled.RetryCount)
	}
}
