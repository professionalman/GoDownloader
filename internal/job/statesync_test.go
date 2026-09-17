package job_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/storage"
)

func setupStateSyncTest(t *testing.T) (*job.Manager, *database.SQLiteJobRepository, *database.SQLiteExecutionRepository, *events.InMemoryBus) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_statesync.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
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

	return mgr, jobRepo, execRepo, bus
}

func setupStateSyncTestWithQueue(t *testing.T) (*job.Manager, *database.SQLiteJobRepository, *database.SQLiteExecutionRepository, *events.InMemoryBus, *database.SQLiteQueueRepository) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_statesync.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
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

	return mgr, jobRepo, execRepo, bus, queueRepo
}

func TestStateSync_MonotonicSequence(t *testing.T) {
	ctx := context.Background()
	mgr, _, execRepo, _ := setupStateSyncTest(t)

	// Initial cursor for unrecorded global scope is 0
	initialCur := mgr.GetCurrentCursor(ctx)
	if initialCur != 0 {
		t.Fatalf("expected initial cursor 0, got %d", initialCur)
	}

	// Advance sequentially
	for expected := int64(1); expected <= 10; expected++ {
		cur := mgr.AdvanceEventCursor(ctx)
		if cur != expected {
			t.Fatalf("expected cursor %d, got %d", expected, cur)
		}
	}

	// Confirm GetCurrentCursor matches latest advance
	got := mgr.GetCurrentCursor(ctx)
	if got != 10 {
		t.Fatalf("expected cursor 10, got %d", got)
	}

	// Confirm direct query to SQLite execution repo matches
	dbCur, err := execRepo.GetEventCursor(ctx, "global")
	if err != nil {
		t.Fatalf("failed to query execRepo cursor: %v", err)
	}
	if dbCur != 10 {
		t.Fatalf("expected db cursor 10, got %d", dbCur)
	}
}

func TestStateSync_ConcurrentAllocation(t *testing.T) {
	ctx := context.Background()
	mgr, _, execRepo, _ := setupStateSyncTest(t)

	const goroutines = 20
	const advancesPerGoroutine = 25
	const totalAdvances = goroutines * advancesPerGoroutine

	var wg sync.WaitGroup
	var mu sync.Mutex
	allocated := make(map[int64]bool)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < advancesPerGoroutine; i++ {
				seq := mgr.AdvanceEventCursor(ctx)
				mu.Lock()
				if allocated[seq] {
					t.Errorf("duplicate sequence allocated: %d", seq)
				}
				allocated[seq] = true
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(allocated) != totalAdvances {
		t.Fatalf("expected %d unique sequences, got %d", totalAdvances, len(allocated))
	}

	// Verify all sequences 1..totalAdvances exist contiguously
	for seq := int64(1); seq <= totalAdvances; seq++ {
		if !allocated[seq] {
			t.Fatalf("missing sequence %d in concurrent allocation", seq)
		}
	}

	// Verify db cursor
	dbCur, err := execRepo.GetEventCursor(ctx, "global")
	if err != nil {
		t.Fatalf("get db cursor: %v", err)
	}
	if dbCur != totalAdvances {
		t.Fatalf("expected final db cursor %d, got %d", totalAdvances, dbCur)
	}
}

func TestStateSync_SnapshotConsistency(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, _ := setupStateSyncTest(t)

	// Create 3 jobs in repository
	for i := 1; i <= 3; i++ {
		j := &job.Job{
			ID:        fmt.Sprintf("job-snap-%d", i),
			Source:    fmt.Sprintf("https://example.com/file-%d.mp4", i),
			Status:    job.StatusQueued,
			Type:      job.TypeMedia,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("failed to create job: %v", err)
		}
	}

	// Advance cursor to 5
	for i := 0; i < 5; i++ {
		mgr.AdvanceEventCursor(ctx)
	}

	snapshot, err := mgr.GetSyncSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetSyncSnapshot failed: %v", err)
	}

	if snapshot.Cursor != 5 {
		t.Fatalf("expected snapshot cursor 5, got %d", snapshot.Cursor)
	}
	if len(snapshot.Jobs) != 3 {
		t.Fatalf("expected 3 jobs in snapshot, got %d", len(snapshot.Jobs))
	}
	if snapshot.Queue == nil {
		t.Fatalf("expected non-nil Queue in snapshot")
	}
	if snapshot.Timestamp.IsZero() {
		t.Fatalf("expected valid timestamp in snapshot")
	}
}

func TestStateSync_TerminalAndLifecycleEventsSequenced(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, bus := setupStateSyncTest(t)

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	// 1. Create a job
	j := &job.Job{
		ID:        "lifecycle-job-1",
		Source:    "https://example.com/item.bin",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Cancel job via manager
	if _, err := mgr.Cancel(ctx, j.ID); err != nil {
		t.Fatalf("cancel job: %v", err)
	}

	// Check cancelled event
	select {
	case ev := <-ch:
		if ev.Type != job.EventJobCancelled {
			t.Fatalf("expected %s, got %s", job.EventJobCancelled, ev.Type)
		}
		if ev.Sequence <= 0 {
			t.Fatalf("expected Sequence > 0, got %d", ev.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for cancelled event")
	}

	// Delete job via manager
	if err := mgr.Delete(ctx, j.ID, job.DeleteJobOptions{DeleteFiles: false}); err != nil {
		t.Fatalf("delete job: %v", err)
	}

	// Check deleted event
	select {
	case ev := <-ch:
		if ev.Type != job.EventJobDeleted {
			t.Fatalf("expected %s, got %s", job.EventJobDeleted, ev.Type)
		}
		if ev.Sequence <= 0 {
			t.Fatalf("expected Sequence > 0 for deleted event, got %d", ev.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for deleted event")
	}
}

func TestStateSync_ProgressTicksEphemeralVsDurable(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, bus := setupStateSyncTest(t)

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	j := &job.Job{
		ID:        "progress-job-1",
		Source:    "https://example.com/video.mp4",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("create job: %v", err)
	}

	cursorBefore := mgr.GetCurrentCursor(ctx)

	// 1. Ephemeral progress tick: persistNow = false
	mgr.UpdateJobFromEngine(ctx, j, &job.EngineStatus{
		Status:         job.StatusDownloading,
		Progress:       25.0,
		CompletedBytes: 2500,
		TotalBytes:     10000,
	}, false)

	select {
	case ev := <-ch:
		if ev.Type != job.EventJobUpdated {
			t.Fatalf("expected %s, got %s", job.EventJobUpdated, ev.Type)
		}
		if ev.Sequence != 0 {
			t.Fatalf("expected ephemeral Sequence == 0, got %d", ev.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ephemeral progress event")
	}

	cursorAfterTick := mgr.GetCurrentCursor(ctx)
	if cursorAfterTick != cursorBefore {
		t.Fatalf("ephemeral progress tick must NOT advance cursor (before=%d, after=%d)", cursorBefore, cursorAfterTick)
	}

	// 2. Persisted update: persistNow = true
	mgr.UpdateJobFromEngine(ctx, j, &job.EngineStatus{
		Status:         job.StatusDownloading,
		Progress:       50.0,
		CompletedBytes: 5000,
		TotalBytes:     10000,
	}, true)

	select {
	case ev := <-ch:
		if ev.Type != job.EventJobUpdated {
			t.Fatalf("expected %s, got %s", job.EventJobUpdated, ev.Type)
		}
		if ev.Sequence <= 0 {
			t.Fatalf("expected durable update Sequence > 0, got %d", ev.Sequence)
		}
		if ev.Sequence <= cursorAfterTick {
			t.Fatalf("expected Sequence > %d, got %d", cursorAfterTick, ev.Sequence)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for persisted progress event")
	}

	cursorAfterPersist := mgr.GetCurrentCursor(ctx)
	if cursorAfterPersist <= cursorAfterTick {
		t.Fatalf("persisted update MUST advance cursor (before=%d, after=%d)", cursorAfterTick, cursorAfterPersist)
	}
}

func TestStateSync_SnapshotDoubleReadProof_InterleavedMutations(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, bus := setupStateSyncTest(t)

	// Create initial jobs
	j1 := &job.Job{
		ID:        "job-double-1",
		Source:    "https://example.com/item1.mp4",
		Status:    job.StatusQueued,
		Type:      job.TypeMedia,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j1); err != nil {
		t.Fatalf("create j1: %v", err)
	}

	// Advance cursor to baseline
	_ = mgr.AdvanceEventCursor(ctx)

	// Scenario A: Mutation occurs concurrently right around snapshot reading.
	mChan := make(chan struct{})
	go func() {
		<-mChan
		_, _ = mgr.Pause(ctx, j1.ID)
	}()

	// Read initial cursor c1
	c1 := mgr.GetCurrentCursor(ctx)
	close(mChan)
	time.Sleep(20 * time.Millisecond)

	snapshot, err := mgr.GetSyncSnapshot(ctx)
	if err != nil {
		t.Fatalf("GetSyncSnapshot failed: %v", err)
	}

	// Invariant: returned snapshot cursor must be <= c1 + 1 (conservative watermark)
	if snapshot.Cursor > c1+1 {
		t.Fatalf("snapshot cursor %d should not exceed upper bound %d", snapshot.Cursor, c1+1)
	}

	// Any events that occurred after the snapshot watermark are in ReplayBuffer
	events, ok := bus.GetEventsAfter(snapshot.Cursor)
	if !ok {
		t.Fatalf("expected ReplayBuffer to bridge from snapshot cursor %d", snapshot.Cursor)
	}

	snapshotJobStatus := ""
	for _, j := range snapshot.Jobs {
		if j.ID == j1.ID {
			snapshotJobStatus = string(j.Status)
		}
	}

	hasReplayMutation := false
	for _, ev := range events {
		if ev.Job.ID == j1.ID && ev.Job.Status == job.StatusPaused {
			hasReplayMutation = true
			break
		}
	}

	if snapshotJobStatus != string(job.StatusPaused) && !hasReplayMutation {
		t.Fatalf("mutation lost: not in snapshot and not in replay buffer")
	}
}

func TestStateSync_QueueReorderSequenced(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, bus, queueRepo := setupStateSyncTestWithQueue(t)

	// Create two queued jobs in normal lane
	for i := 1; i <= 2; i++ {
		id := fmt.Sprintf("reorder-job-%d", i)
		j := &job.Job{
			ID:        id,
			Priority:  job.JobPriorityNormal,
			Status:    job.StatusQueued,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := jobRepo.Create(ctx, j); err != nil {
			t.Fatalf("create job: %v", err)
		}
		if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
			JobID:      id,
			Position:   int64(i),
			Action:     job.QueueActionStart,
			EnqueuedAt: time.Now(),
			UpdatedAt:  time.Now(),
		}); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cursorBefore := mgr.GetCurrentCursor(ctx)

	// Reorder jobs: [reorder-job-2, reorder-job-1]
	err := mgr.ReorderQueue(ctx, job.JobPriorityNormal, []string{"reorder-job-2", "reorder-job-1"})
	if err != nil {
		t.Fatalf("ReorderQueue failed: %v", err)
	}

	cursorAfter := mgr.GetCurrentCursor(ctx)
	if cursorAfter <= cursorBefore {
		t.Fatalf("ReorderQueue must advance event cursor: before=%d, after=%d", cursorBefore, cursorAfter)
	}

	select {
	case ev := <-ch:
		if ev.Sequence <= cursorBefore {
			t.Fatalf("expected sequenced event with seq > %d, got %d", cursorBefore, ev.Sequence)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for queue reorder event")
	}
}

func TestStateSync_SchedulerStateTransitionsSequenced(t *testing.T) {
	ctx := context.Background()
	mgr, jobRepo, _, bus := setupStateSyncTest(t)

	tempDir := t.TempDir()
	db, err := database.New(filepath.Join(tempDir, "test_sched_seq.db"))
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	queueRepo := database.NewSQLiteQueueRepository(db)

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int { return 1 }, func(ctx context.Context, qj *job.QueuedJob) error {
		return nil
	})
	mgr.SetScheduler(sched)

	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	cursorBefore := mgr.GetCurrentCursor(ctx)

	// Create job in repository
	j := &job.Job{
		ID:        "sched-job-1",
		Status:    job.StatusQueued,
		Priority:  job.JobPriorityNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("create job: %v", err)
	}

	// Trigger reconciliation error through scheduler
	sched.ReconcileStatePersistenceForTest(ctx, job.DispatchReservation{
		JobID:        j.ID,
		Action:       job.QueueActionStart,
		Kind:         job.ReconciliationStatePersistence,
		TargetStatus: job.StatusPaused,
		TargetError:  "simulated failure",
	})

	select {
	case ev := <-ch:
		if ev.Sequence <= cursorBefore {
			t.Fatalf("scheduler event must carry monotonic sequence > %d, got %d", cursorBefore, ev.Sequence)
		}
		if ev.Type != job.EventJobUpdated {
			t.Fatalf("expected EventJobUpdated, got %s", ev.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for scheduler event")
	}
}
