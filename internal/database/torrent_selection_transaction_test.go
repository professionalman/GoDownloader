package database

import (
	"context"
	"testing"
	"time"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func TestSQLiteTransaction_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "job-tx-success",
		Source:    "magnet:?xt=urn:btih:1234",
		Name:      "test.iso",
		Status:    job.StatusAwaitingSelection,
		Type:      job.TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash123",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("jobRepo.Create failed: %v", err)
	}

	rec := &job.TorrentJobRecord{
		JobID:    j.ID,
		InfoHash: "1234",
		Name:     "test.iso",
	}
	if err := torrentRepo.CreateTorrentJob(ctx, rec); err != nil {
		t.Fatalf("CreateTorrentJob failed: %v", err)
	}

	files := []job.TorrentFileRecord{
		{JobID: j.ID, FileIndex: 0, Path: "f0", Size: 700 * 1024 * 1024, Selected: true, Priority: "normal"},
		{JobID: j.ID, FileIndex: 1, Path: "f1", Size: 700 * 1024 * 1024, Selected: true, Priority: "normal"},
	}
	if err := torrentRepo.SaveTorrentFiles(ctx, j.ID, files); err != nil {
		t.Fatalf("SaveTorrentFiles failed: %v", err)
	}

	j.TotalBytes = 1400 * 1024 * 1024
	j.Status = job.StatusQueued
	j.UpdatedAt = time.Now()

	qe := &job.QueueEntry{
		JobID:      j.ID,
		Position:   100,
		Action:     job.QueueActionStart,
		EnqueuedAt: time.Now(),
		UpdatedAt:  time.Now(),
	}

	err := torrentRepo.PersistTorrentSelectionAndEnqueue(ctx, j, files, rec, qe)
	if err != nil {
		t.Fatalf("PersistTorrentSelectionAndEnqueue failed: %v", err)
	}

	// Verify job is StatusQueued in DB
	updatedJ, err := jobRepo.GetByID(ctx, j.ID)
	if err != nil || updatedJ.Status != job.StatusQueued {
		t.Fatalf("expected job status = queued, got %v (err: %v)", updatedJ.Status, err)
	}

	// Verify queue entry exists
	queueRepo := NewSQLiteQueueRepository(db)
	entry, err := queueRepo.Get(ctx, j.ID)
	if err != nil || entry == nil || entry.Action != job.QueueActionStart {
		t.Fatalf("expected queue entry with QueueActionStart, got %v (err: %v)", entry, err)
	}
}

func TestSQLiteTransaction_RollbackMissingFileRow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "job-tx-missing-file",
		Source:    "magnet:?xt=urn:btih:1234",
		Name:      "test.iso",
		Status:    job.StatusAwaitingSelection,
		Type:      job.TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash123",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = jobRepo.Create(ctx, j)
	_ = torrentRepo.CreateTorrentJob(ctx, &job.TorrentJobRecord{JobID: j.ID, InfoHash: "1234", Name: "test.iso"})

	// File index 99 does NOT exist in torrent_files
	files := []job.TorrentFileRecord{
		{JobID: j.ID, FileIndex: 99, Path: "missing", Size: 700 * 1024 * 1024, Selected: true, Priority: "normal"},
	}

	j.Status = job.StatusQueued
	qe := &job.QueueEntry{JobID: j.ID, Position: 100, Action: job.QueueActionStart}

	err := torrentRepo.PersistTorrentSelectionAndEnqueue(ctx, j, files, nil, qe)
	if err == nil {
		t.Fatal("expected PersistTorrentSelectionAndEnqueue to fail due to missing file row")
	}

	// Verify job remains StatusAwaitingSelection in DB
	durableJ, _ := jobRepo.GetByID(ctx, j.ID)
	if durableJ.Status != job.StatusAwaitingSelection {
		t.Fatalf("expected job status to remain awaiting_selection, got %s", durableJ.Status)
	}

	// Verify no queue entry inserted
	queueRepo := NewSQLiteQueueRepository(db)
	entry, _ := queueRepo.Get(ctx, j.ID)
	if entry != nil {
		t.Fatal("expected NO queue entry after transaction rollback")
	}
}

func TestSQLiteTransaction_RollbackMissingTorrentJobRow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "job-tx-missing-tj",
		Source:    "magnet:?xt=urn:btih:1234",
		Name:      "test.iso",
		Status:    job.StatusAwaitingSelection,
		Type:      job.TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash123",
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = jobRepo.Create(ctx, j)

	// Save file row
	files := []job.TorrentFileRecord{
		{JobID: j.ID, FileIndex: 0, Path: "f0", Size: 700 * 1024 * 1024, Selected: true, Priority: "normal"},
	}
	_ = torrentRepo.SaveTorrentFiles(ctx, j.ID, files)

	// Provide rec for job-tx-missing-tj which is NOT in torrent_jobs table
	rec := &job.TorrentJobRecord{JobID: j.ID, InfoHash: "1234", Name: "test.iso", SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}}

	j.Status = job.StatusQueued
	qe := &job.QueueEntry{JobID: j.ID, Position: 100, Action: job.QueueActionStart}

	err := torrentRepo.PersistTorrentSelectionAndEnqueue(ctx, j, files, rec, qe)
	if err == nil {
		t.Fatal("expected PersistTorrentSelectionAndEnqueue to fail due to missing torrent_jobs row")
	}

	// Verify job remains StatusAwaitingSelection
	durableJ, _ := jobRepo.GetByID(ctx, j.ID)
	if durableJ.Status != job.StatusAwaitingSelection {
		t.Fatalf("expected job status to remain awaiting_selection, got %s", durableJ.Status)
	}
}

func TestSQLiteTransaction_RollbackMissingJobRow(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	// Non-existent job in jobs table
	j := &job.Job{
		ID:        "non-existent-job",
		Status:    job.StatusQueued,
		UpdatedAt: time.Now(),
	}

	files := []job.TorrentFileRecord{
		{JobID: j.ID, FileIndex: 0, Path: "f0", Size: 700 * 1024 * 1024, Selected: true, Priority: "normal"},
	}
	_ = torrentRepo.SaveTorrentFiles(ctx, j.ID, files)

	qe := &job.QueueEntry{JobID: j.ID, Position: 100, Action: job.QueueActionStart}

	err := torrentRepo.PersistTorrentSelectionAndEnqueue(ctx, j, files, nil, qe)
	if err == nil {
		t.Fatal("expected PersistTorrentSelectionAndEnqueue to fail due to missing jobs row")
	}

	queueRepo := NewSQLiteQueueRepository(db)
	entry, _ := queueRepo.Get(ctx, j.ID)
	if entry != nil {
		t.Fatal("expected NO queue entry after transaction rollback")
	}
}
