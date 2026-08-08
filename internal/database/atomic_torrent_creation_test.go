package database

import (
	"context"
	"testing"
	"time"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func TestCreateTorrentJobAtomic_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:             "job-atomic-success",
		Source:         "magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12",
		Name:           "Ubuntu ISO",
		Status:         job.StatusAnalyzing,
		Type:           job.TypeTorrent,
		Engine:         "qbittorrent",
		Priority:       job.JobPriorityNormal,
		DestinationDir: "/downloads",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	rec := &job.TorrentJobRecord{
		JobID:             j.ID,
		InfoHash:          "abcdef1234567890abcdef1234567890abcdef12",
		Name:              "Ubuntu ISO",
		TorrentFilePath:   "",
		SeedAfterComplete: true,
		SeedingPolicy:     networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeUnlimited},
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, j, rec)
	if err != nil {
		t.Fatalf("CreateTorrentJobAtomic failed: %v", err)
	}

	// 1. Verify jobs row exists
	savedJob, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil {
		t.Fatalf("jobRepo.GetByID failed: %v", err)
	}
	if savedJob == nil {
		t.Fatal("expected jobs row to exist")
	}
	if savedJob.ID != j.ID || savedJob.Name != "Ubuntu ISO" {
		t.Fatalf("saved job mismatch: %+v", savedJob)
	}

	// 2. Verify torrent_jobs row exists
	savedRec, err := torrentRepo.GetTorrentJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("torrentRepo.GetTorrentJob failed: %v", err)
	}
	if savedRec == nil {
		t.Fatal("expected torrent_jobs row to exist")
	}
	if savedRec.InfoHash != rec.InfoHash || savedRec.SeedAfterComplete != true {
		t.Fatalf("saved torrent record mismatch: %+v", savedRec)
	}
}

func TestCreateTorrentJobAtomic_TorrentJobsFailureRollsBackJobs(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	// Pre-insert a torrent_jobs record with job_id "existing-job"
	now := time.Now().Truncate(time.Second)
	existingJob := &job.Job{
		ID:        "existing-job",
		Source:    "magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
		Name:      "Existing",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	existingRec := &job.TorrentJobRecord{
		JobID:    existingJob.ID,
		InfoHash: "1111111111111111111111111111111111111111",
	}
	if err := torrentRepo.CreateTorrentJobAtomic(ctx, existingJob, existingRec); err != nil {
		t.Fatalf("pre-insert failed: %v", err)
	}

	// Attempt atomic creation with a new job whose torrent_jobs record collides on primary key (job_id = "existing-job")
	collidingJob := &job.Job{
		ID:        "job-should-be-rolled-back",
		Source:    "magnet:?xt=urn:btih:2222222222222222222222222222222222222222",
		Name:      "Colliding Torrent",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	collidingRec := &job.TorrentJobRecord{
		JobID:    existingJob.ID, // Collides with existing job_id primary key in torrent_jobs
		InfoHash: "2222222222222222222222222222222222222222",
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, collidingJob, collidingRec)
	if err == nil {
		t.Fatal("expected CreateTorrentJobAtomic to fail due to primary key conflict in torrent_jobs")
	}

	// Verify collidingJob was rolled back and DOES NOT exist in jobs table
	savedJob, err := jobRepo.GetByID(ctx, collidingJob.ID)
	if err != nil {
		t.Fatalf("jobRepo.GetByID failed: %v", err)
	}
	if savedJob != nil {
		t.Fatalf("expected jobs row for %s to be rolled back, but found: %+v", collidingJob.ID, savedJob)
	}
}

func TestCreateTorrentJobAtomic_JobsFailureRollsBackEverything(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j1 := &job.Job{
		ID:        "job-duplicate-id",
		Source:    "https://example.com/1",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(ctx, j1); err != nil {
		t.Fatalf("initial create failed: %v", err)
	}

	// Attempt to create duplicate job ID atomically
	j2 := &job.Job{
		ID:        "job-duplicate-id",
		Source:    "https://example.com/2",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	rec2 := &job.TorrentJobRecord{
		JobID:    j2.ID,
		InfoHash: "3333333333333333333333333333333333333333",
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, j2, rec2)
	if err == nil {
		t.Fatal("expected CreateTorrentJobAtomic to fail on duplicate job ID")
	}

	// Verify torrent_jobs row was NOT created
	rec, err := torrentRepo.GetTorrentJob(ctx, j2.ID)
	if err != nil {
		t.Fatalf("GetTorrentJob failed: %v", err)
	}
	if rec != nil {
		t.Fatalf("expected torrent_jobs row to be rolled back, but found: %+v", rec)
	}
}

func TestCreateTorrentJobAtomic_NilJob_ReturnsErrorAndZeroRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	rec := &job.TorrentJobRecord{
		JobID:    "some-job-id",
		InfoHash: "hash123",
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, nil, rec)
	if err == nil {
		t.Fatal("expected error on nil job")
	}

	jobs, err := jobRepo.List(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expected 0 jobs in DB, got %d, err=%v", len(jobs), err)
	}

	savedRec, err := torrentRepo.GetTorrentJob(ctx, "some-job-id")
	if err != nil || savedRec != nil {
		t.Fatalf("expected nil torrent record in DB, got %+v, err=%v", savedRec, err)
	}
}

func TestCreateTorrentJobAtomic_NilTorrentRecord_ReturnsErrorAndZeroRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "job-nil-rec",
		Source:    "magnet:?xt=urn:btih:5555",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, j, nil)
	if err == nil {
		t.Fatal("expected error on nil torrent record")
	}

	savedJob, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil || savedJob != nil {
		t.Fatalf("expected nil job in DB, got %+v, err=%v", savedJob, err)
	}

	savedRec, err := torrentRepo.GetTorrentJob(ctx, j.ID)
	if err != nil || savedRec != nil {
		t.Fatalf("expected nil torrent record in DB, got %+v, err=%v", savedRec, err)
	}
}

func TestCreateTorrentJobAtomic_MismatchedJobID_ReturnsErrorAndZeroRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "job-id-1",
		Source:    "magnet:?xt=urn:btih:6666",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	rec := &job.TorrentJobRecord{
		JobID:    "job-id-2", // Mismatched ID
		InfoHash: "6666",
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, j, rec)
	if err == nil {
		t.Fatal("expected error on mismatched job ID")
	}

	savedJob1, err := jobRepo.GetByID(ctx, "job-id-1")
	if err != nil || savedJob1 != nil {
		t.Fatalf("expected nil job-id-1 in DB, got %+v, err=%v", savedJob1, err)
	}
	savedJob2, err := jobRepo.GetByID(ctx, "job-id-2")
	if err != nil || savedJob2 != nil {
		t.Fatalf("expected nil job-id-2 in DB, got %+v, err=%v", savedJob2, err)
	}
	savedRec1, err := torrentRepo.GetTorrentJob(ctx, "job-id-1")
	if err != nil || savedRec1 != nil {
		t.Fatalf("expected nil torrent record job-id-1 in DB, got %+v, err=%v", savedRec1, err)
	}
	savedRec2, err := torrentRepo.GetTorrentJob(ctx, "job-id-2")
	if err != nil || savedRec2 != nil {
		t.Fatalf("expected nil torrent record job-id-2 in DB, got %+v, err=%v", savedRec2, err)
	}
}

func TestCreateTorrentJobAtomic_EmptyJobID_ReturnsErrorAndZeroRows(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "",
		Source:    "magnet:?xt=urn:btih:7777",
		Status:    job.StatusAnalyzing,
		Type:      job.TypeTorrent,
		CreatedAt: now,
		UpdatedAt: now,
	}
	rec := &job.TorrentJobRecord{
		JobID:    "",
		InfoHash: "7777",
	}

	err := torrentRepo.CreateTorrentJobAtomic(ctx, j, rec)
	if err == nil {
		t.Fatal("expected error on empty job ID")
	}

	jobs, err := jobRepo.List(ctx)
	if err != nil || len(jobs) != 0 {
		t.Fatalf("expected 0 jobs in DB, got %d, err=%v", len(jobs), err)
	}
}
