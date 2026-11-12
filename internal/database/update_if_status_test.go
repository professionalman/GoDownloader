package database

import (
	"context"
	"testing"
	"time"

	"downloader/internal/job"
)

// H-1: the conditional full-row update commits only while the persisted status equals the expected one, as a
// single statement, so a dispatch and a cancel that both observed QUEUED cannot both commit.
func TestUpdateIfStatus_FirstTransitionWins(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	base := job.Job{ID: "cas-1", Source: "https://example.com/a", Name: "a", Status: job.StatusQueued,
		Type: job.TypeDownload, Engine: "aria2", Priority: job.JobPriorityNormal, CreatedAt: now, UpdatedAt: now}
	if err := repo.Create(ctx, &base); err != nil {
		t.Fatal(err)
	}

	cancelled := base
	cancelled.Status = job.StatusCancelled
	won, err := repo.UpdateIfStatus(ctx, &cancelled, job.StatusQueued)
	if err != nil || !won {
		t.Fatalf("first transition: won=%v err=%v", won, err)
	}

	downloading := base
	downloading.Status = job.StatusDownloading
	downloading.EngineID = "gid-late"
	won, err = repo.UpdateIfStatus(ctx, &downloading, job.StatusQueued)
	if err != nil || won {
		t.Fatalf("second transition from the same observed state must lose: won=%v err=%v", won, err)
	}
	got, err := repo.GetByID(ctx, base.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != job.StatusCancelled || got.EngineID != "" {
		t.Fatalf("losing update was written: status=%s engineID=%q", got.Status, got.EngineID)
	}

	missing := base
	missing.ID = "cas-missing"
	if won, err := repo.UpdateIfStatus(ctx, &missing, job.StatusQueued); err != nil || won {
		t.Fatalf("missing row: won=%v err=%v", won, err)
	}
}
