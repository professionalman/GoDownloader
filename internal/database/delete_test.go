package database

import (
	"context"
	"testing"
	"time"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func TestDeleteJobCascade_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	queueRepo := NewSQLiteQueueRepository(db)
	secretRepo := NewSQLiteSecretRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)

	// Create Job 1 (to be deleted)
	j1 := &job.Job{
		ID:             "job-delete-1",
		Source:         "magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
		Name:           "Torrent Job 1",
		Status:         job.StatusCompleted,
		Type:           job.TypeTorrent,
		Engine:         "qbittorrent",
		Priority:       job.JobPriorityNormal,
		DestinationDir: "/downloads",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	rec1 := &job.TorrentJobRecord{
		JobID:         j1.ID,
		InfoHash:      "1111111111111111111111111111111111111111",
		Name:          "Torrent Job 1",
		SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone},
	}
	if err := torrentRepo.CreateTorrentJobAtomic(ctx, j1, rec1); err != nil {
		t.Fatalf("CreateTorrentJobAtomic failed: %v", err)
	}
	if err := torrentRepo.SaveTorrentFiles(ctx, j1.ID, []job.TorrentFileRecord{
		{JobID: j1.ID, FileIndex: 0, Path: "file1.mkv", Size: 1000, Selected: true},
		{JobID: j1.ID, FileIndex: 1, Path: "file2.nfo", Size: 50, Selected: false},
	}); err != nil {
		t.Fatalf("SaveTorrentFiles failed: %v", err)
	}
	if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j1.ID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("queue Enqueue failed: %v", err)
	}
	if err := secretRepo.SetSecret(ctx, "job", j1.ID, "proxy_pass", []byte("secret123")); err != nil {
		t.Fatalf("SetSecret failed: %v", err)
	}

	// Create Job 2 (unrelated job, must be preserved)
	j2 := &job.Job{
		ID:             "job-delete-2",
		Source:         "https://example.com/file2.iso",
		Name:           "Direct Job 2",
		Status:         job.StatusCompleted,
		Type:           job.TypeDownload,
		Engine:         "aria2",
		Priority:       job.JobPriorityNormal,
		DestinationDir: "/downloads",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := jobRepo.Create(ctx, j2); err != nil {
		t.Fatalf("jobRepo.Create failed: %v", err)
	}

	// Execute DeleteJobCascade on Job 1
	if err := jobRepo.DeleteJobCascade(ctx, j1.ID); err != nil {
		t.Fatalf("DeleteJobCascade failed: %v", err)
	}

	// 1. Verify Job 1 rows are completely gone across all tables
	if saved, err := jobRepo.GetByID(ctx, j1.ID); err != nil || saved != nil {
		t.Errorf("expected jobs row for %s to be deleted, got err=%v, job=%+v", j1.ID, err, saved)
	}
	if savedRec, err := torrentRepo.GetTorrentJob(ctx, j1.ID); err == nil && savedRec != nil {
		t.Errorf("expected torrent_jobs row to be deleted")
	}
	if files, err := torrentRepo.GetTorrentFiles(ctx, j1.ID); err != nil || len(files) != 0 {
		t.Errorf("expected torrent_files to be empty, got: %v (err=%v)", files, err)
	}
	if qEntry, err := queueRepo.Get(ctx, j1.ID); err == nil && qEntry != nil {
		t.Errorf("expected job_queue entry to be deleted, got: %+v", qEntry)
	}
	if hasSec, err := secretRepo.HasSecret(ctx, "job", j1.ID, "proxy_pass"); err != nil || hasSec {
		t.Errorf("expected secret to be deleted")
	}

	// 2. Verify Job 2 is completely intact
	saved2, err := jobRepo.GetByID(ctx, j2.ID)
	if err != nil || saved2 == nil {
		t.Fatalf("expected job 2 to remain untouched, got err=%v, job=%+v", err, saved2)
	}
	if saved2.ID != j2.ID {
		t.Errorf("job 2 mismatch: %+v", saved2)
	}
}

func TestDeleteJobCascade_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	jobRepo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	err := jobRepo.DeleteJobCascade(ctx, "nonexistent-job-id")
	if err == nil {
		t.Fatalf("expected error when deleting nonexistent job")
	}
}

func TestDeleteJobCascade_LimitsSecretDeletionToJobScope(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	jobRepo := NewSQLiteJobRepository(db)
	secretRepo := NewSQLiteSecretRepository(db)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	jobID := "shared-owner-id-1"
	j := &job.Job{
		ID:             jobID,
		Source:         "https://example.com/file.iso",
		Name:           "Test Job",
		Status:         job.StatusCompleted,
		Type:           job.TypeDownload,
		Engine:         "aria2",
		Priority:       job.JobPriorityNormal,
		DestinationDir: "/downloads",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("jobRepo.Create failed: %v", err)
	}

	// Secret 1: scope='job'
	if err := secretRepo.SetSecret(ctx, "job", jobID, "proxy_pass", []byte("job-secret")); err != nil {
		t.Fatalf("SetSecret job failed: %v", err)
	}
	// Secret 2: scope='other-scope' with identical owner_id
	if err := secretRepo.SetSecret(ctx, "other-scope", jobID, "proxy_pass", []byte("other-secret")); err != nil {
		t.Fatalf("SetSecret other-scope failed: %v", err)
	}

	if err := jobRepo.DeleteJobCascade(ctx, jobID); err != nil {
		t.Fatalf("DeleteJobCascade failed: %v", err)
	}

	hasJobSec, err := secretRepo.HasSecret(ctx, "job", jobID, "proxy_pass")
	if err != nil || hasJobSec {
		t.Errorf("expected job-scoped secret to be deleted, got hasSecret=%v, err=%v", hasJobSec, err)
	}

	hasOtherSec, err := secretRepo.HasSecret(ctx, "other-scope", jobID, "proxy_pass")
	if err != nil || !hasOtherSec {
		t.Errorf("expected other-scoped secret to remain intact, got hasSecret=%v, err=%v", hasOtherSec, err)
	}
}

func TestDeleteJobCascade_RollbackOnFailure(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	queueRepo := NewSQLiteQueueRepository(db)
	secretRepo := NewSQLiteSecretRepository(db)
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)

	jobID := "job-rollback-test"
	j := &job.Job{
		ID:             jobID,
		Source:         "magnet:?xt=urn:btih:2222222222222222222222222222222222222222",
		Name:           "Rollback Torrent Job",
		Status:         job.StatusCompleted,
		Type:           job.TypeTorrent,
		Engine:         "qbittorrent",
		Priority:       job.JobPriorityNormal,
		DestinationDir: "/downloads",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	rec := &job.TorrentJobRecord{
		JobID:         jobID,
		InfoHash:      "2222222222222222222222222222222222222222",
		Name:          "Rollback Torrent Job",
		SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone},
	}
	if err := torrentRepo.CreateTorrentJobAtomic(ctx, j, rec); err != nil {
		t.Fatalf("CreateTorrentJobAtomic failed: %v", err)
	}
	if err := torrentRepo.SaveTorrentFiles(ctx, jobID, []job.TorrentFileRecord{
		{JobID: jobID, FileIndex: 0, Path: "movie.mkv", Size: 5000, Selected: true},
	}); err != nil {
		t.Fatalf("SaveTorrentFiles failed: %v", err)
	}
	if err := queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      jobID,
		Position:   1,
		Action:     job.QueueActionStart,
		EnqueuedAt: now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("queue Enqueue failed: %v", err)
	}
	if err := secretRepo.SetSecret(ctx, "job", jobID, "proxy_pass", []byte("secret-to-keep")); err != nil {
		t.Fatalf("SetSecret failed: %v", err)
	}

	// Install a trigger that aborts the final jobs DELETE, testing transactional rollback of earlier child deletes
	_, err := db.conn.Exec(`
		CREATE TRIGGER abort_jobs_delete
		BEFORE DELETE ON jobs
		BEGIN
			SELECT RAISE(ABORT, 'forced trigger failure during jobs delete');
		END;
	`)
	if err != nil {
		t.Fatalf("failed to create abort trigger: %v", err)
	}

	// DeleteJobCascade should execute child DELETEs and then fail on jobs DELETE
	err = jobRepo.DeleteJobCascade(ctx, jobID)
	if err == nil {
		t.Fatalf("expected DeleteJobCascade to fail due to trigger")
	}

	// Verify complete transactional rollback: all child records and job record must remain intact
	savedJob, err := jobRepo.GetByID(ctx, jobID)
	if err != nil || savedJob == nil {
		t.Errorf("expected jobs row to remain after rollback, got err=%v, job=%+v", err, savedJob)
	}
	savedRec, err := torrentRepo.GetTorrentJob(ctx, jobID)
	if err != nil || savedRec == nil {
		t.Errorf("expected torrent_jobs row to remain after rollback, got err=%v, rec=%+v", err, savedRec)
	}
	files, err := torrentRepo.GetTorrentFiles(ctx, jobID)
	if err != nil || len(files) != 1 {
		t.Errorf("expected torrent_files rows to remain after rollback, got %d files, err=%v", len(files), err)
	}
	qEntry, err := queueRepo.Get(ctx, jobID)
	if err != nil || qEntry == nil {
		t.Errorf("expected job_queue row to remain after rollback, got err=%v, entry=%+v", err, qEntry)
	}
	hasSec, err := secretRepo.HasSecret(ctx, "job", jobID, "proxy_pass")
	if err != nil || !hasSec {
		t.Errorf("expected secret to remain after rollback, got hasSec=%v, err=%v", hasSec, err)
	}
}
