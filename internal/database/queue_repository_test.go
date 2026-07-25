package database_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/job"
)

func setupQueueTestDB(t *testing.T) (*database.DB, *database.SQLiteJobRepository, *database.SQLiteQueueRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_queue.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	return db, jobRepo, queueRepo
}

func TestSQLiteQueueRepository_EnqueueGetDelete(t *testing.T) {
	ctx := context.Background()
	db, _, queueRepo := setupQueueTestDB(t)
	defer db.Close()

	now := time.Now()
	entry := &job.QueueEntry{
		JobID:      "job_1",
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}

	if err := queueRepo.Enqueue(ctx, entry); err != nil {
		t.Fatalf("enqueue failed: %v", err)
	}

	got, err := queueRepo.Get(ctx, "job_1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got == nil || got.JobID != "job_1" || got.Position != 1 || got.Action != job.QueueActionStart {
		t.Errorf("unexpected queue entry: %+v", got)
	}

	if err := queueRepo.Delete(ctx, "job_1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	deleted, err := queueRepo.Get(ctx, "job_1")
	if err != nil {
		t.Fatalf("get deleted failed: %v", err)
	}
	if deleted != nil {
		t.Errorf("expected nil after delete, got %+v", deleted)
	}
}

func TestSQLiteQueueRepository_NextRunnablePriorityOrdering(t *testing.T) {
	ctx := context.Background()
	db, jobRepo, queueRepo := setupQueueTestDB(t)
	defer db.Close()

	now := time.Now()

	createQueuedJob := func(id, name string, priority job.JobPriority, pos int64) {
		j := &job.Job{
			ID:        id,
			Source:    "http://example.com/" + id,
			Name:      name,
			Status:    job.StatusQueued,
			Type:      job.TypeDownload,
			Engine:    "aria2",
			Priority:  priority,
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

	createQueuedJob("job_low", "Low Job", job.JobPriorityLow, 1)
	createQueuedJob("job_norm1", "Normal 1", job.JobPriorityNormal, 1)
	createQueuedJob("job_norm2", "Normal 2", job.JobPriorityNormal, 2)
	createQueuedJob("job_high", "High Job", job.JobPriorityHigh, 1)

	// High priority job must be NextRunnable
	next1, err := queueRepo.NextRunnable(ctx)
	if err != nil || next1 == nil {
		t.Fatalf("failed to get next runnable: %v", err)
	}
	if next1.JobID != "job_high" {
		t.Errorf("expected job_high to be next runnable, got %s", next1.JobID)
	}

	// Delete high priority job
	queueRepo.Delete(ctx, "job_high")

	// Next runnable must be Normal 1 (FIFO)
	next2, err := queueRepo.NextRunnable(ctx)
	if err != nil || next2 == nil {
		t.Fatalf("failed to get next runnable: %v", err)
	}
	if next2.JobID != "job_norm1" {
		t.Errorf("expected job_norm1 to be next runnable, got %s", next2.JobID)
	}
}

func TestSQLiteQueueRepository_ReorderAndReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "test_reopen.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	now := time.Now()

	createQueuedJob := func(id string, pos int64) {
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

	createQueuedJob("j1", 1)
	createQueuedJob("j2", 2)
	createQueuedJob("j3", 3)

	// Reorder lane to: j3, j1, j2
	if err := queueRepo.Reorder(ctx, job.JobPriorityNormal, []string{"j3", "j1", "j2"}); err != nil {
		t.Fatalf("reorder failed: %v", err)
	}

	db.Close()

	// Reopen database
	db2, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen db failed: %v", err)
	}
	defer db2.Close()

	queueRepo2 := database.NewSQLiteQueueRepository(db2)
	items, err := queueRepo2.List(ctx)
	if err != nil {
		t.Fatalf("list after reopen failed: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("expected 3 items after reopen, got %d", len(items))
	}
	if items[0].JobID != "j3" || items[1].JobID != "j1" || items[2].JobID != "j2" {
		t.Errorf("unexpected ordering after reopen: %s, %s, %s", items[0].JobID, items[1].JobID, items[2].JobID)
	}
}
