package database

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/job"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	return db, func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}
}

func TestRepository_CreateAndGetByID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "test-1",
		Source:    "https://example.com/file.zip",
		Name:      "file.zip",
		Status:    job.StatusQueued,
		Engine:    "aria2",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	got, err := repo.GetByID(ctx, "test-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected job, got nil")
	}
	if got.ID != "test-1" {
		t.Errorf("expected ID test-1, got %s", got.ID)
	}
	if got.Source != "https://example.com/file.zip" {
		t.Errorf("expected source, got %s", got.Source)
	}
	if got.Status != job.StatusQueued {
		t.Errorf("expected status queued, got %s", got.Status)
	}
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	got, err := repo.GetByID(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestRepository_Update(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &job.Job{
		ID:        "test-2",
		Source:    "https://example.com/file.zip",
		Name:      "file.zip",
		Status:    job.StatusQueued,
		Engine:    "aria2",
		CreatedAt: now,
		UpdatedAt: now,
	}
	repo.Create(ctx, j)

	j.Status = job.StatusDownloading
	j.Progress = 42.5
	j.TotalBytes = 1000000
	j.CompletedBytes = 425000
	j.SpeedBytesPerSecond = 50000
	j.ETASeconds = 12
	j.EngineID = "abc123"
	j.UpdatedAt = time.Now().Truncate(time.Second)

	if err := repo.Update(ctx, j); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	got, _ := repo.GetByID(ctx, "test-2")
	if got.Status != job.StatusDownloading {
		t.Errorf("expected downloading, got %s", got.Status)
	}
	if got.Progress != 42.5 {
		t.Errorf("expected progress 42.5, got %f", got.Progress)
	}
	if got.EngineID != "abc123" {
		t.Errorf("expected engineID abc123, got %s", got.EngineID)
	}
	if got.ETASeconds != 12 {
		t.Errorf("expected etaSeconds 12, got %d", got.ETASeconds)
	}
}

func TestRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	for i, status := range []job.JobStatus{job.StatusCompleted, job.StatusDownloading, job.StatusFailed} {
		now := time.Now().Add(time.Duration(i) * time.Second).Truncate(time.Second)
		j := &job.Job{
			ID:        "test-" + string(rune('a'+i)),
			Source:    "https://example.com",
			Name:      "file",
			Status:    status,
			Engine:    "aria2",
			CreatedAt: now,
			UpdatedAt: now,
		}
		repo.Create(ctx, j)
	}

	jobs, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
}

func TestRepository_ListRecoverable(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	statuses := []job.JobStatus{
		job.StatusQueued,
		job.StatusDownloading,
		job.StatusPaused,
		job.StatusCompleted,
		job.StatusFailed,
		job.StatusCancelled,
	}

	for i, s := range statuses {
		j := &job.Job{
			ID:        "job-" + string(rune('a'+i)),
			Source:    "https://example.com",
			Name:      "file",
			Status:    s,
			Engine:    "aria2",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
			UpdatedAt: now.Add(time.Duration(i) * time.Second),
		}
		repo.Create(ctx, j)
	}

	jobs, err := repo.ListRecoverable(ctx)
	if err != nil {
		t.Fatalf("ListRecoverable failed: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("expected 3 recoverable jobs, got %d", len(jobs))
	}

	for _, j := range jobs {
		if !job.IsRecoverable(j.Status) {
			t.Errorf("got non-recoverable job with status %s", j.Status)
		}
	}
}

func TestRepository_Persistence(t *testing.T) {
	// Verify that jobs survive closing and reopening the database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	ctx := context.Background()

	// Write
	{
		db, err := New(dbPath)
		if err != nil {
			t.Fatalf("failed to create db: %v", err)
		}
		repo := NewSQLiteJobRepository(db)
		now := time.Now().Truncate(time.Second)
		j := &job.Job{
			ID: "persist-1", Source: "https://example.com", Name: "file.zip",
			Status: job.StatusCompleted, Engine: "aria2",
			CreatedAt: now, UpdatedAt: now,
		}
		repo.Create(ctx, j)
		db.Close()
	}

	// Read with a new connection
	{
		db, err := New(dbPath)
		if err != nil {
			t.Fatalf("failed to reopen db: %v", err)
		}
		defer db.Close()
		repo := NewSQLiteJobRepository(db)
		got, err := repo.GetByID(ctx, "persist-1")
		if err != nil {
			t.Fatalf("GetByID failed: %v", err)
		}
		if got == nil {
			t.Fatal("expected job to persist across db reopen, got nil")
		}
		if got.Status != job.StatusCompleted {
			t.Errorf("expected completed, got %s", got.Status)
		}
	}
}

func TestRepository_MediaJobPersistence(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	repo := NewSQLiteJobRepository(db)
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	info := &job.MediaInfo{
		Title:     "Test Video",
		Duration:  120,
		Thumbnail: "https://example.com/thumb.jpg",
		URL:       "https://youtube.com/watch?v=123",
		Formats: []job.MediaFormat{
			{FormatID: "18", Extension: "mp4", Resolution: "640x360", Quality: "360p"},
		},
		SelectedFmt: "18",
	}

	mediaJob := &job.Job{
		ID:        "media-1",
		Source:    "https://youtube.com/watch?v=123",
		Name:      "Test Video",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		Engine:    "ytdlp",
		MediaInfo: info,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("Create media job failed: %v", err)
	}

	got, err := repo.GetByID(ctx, "media-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if got == nil {
		t.Fatal("expected media job, got nil")
	}

	if got.Type != job.TypeMedia {
		t.Errorf("expected type 'media', got %s", got.Type)
	}

	if got.MediaInfo == nil {
		t.Fatal("expected MediaInfo to be persisted, got nil")
	}

	if got.MediaInfo.Title != "Test Video" {
		t.Errorf("expected title 'Test Video', got %s", got.MediaInfo.Title)
	}

	if len(got.MediaInfo.Formats) != 1 || got.MediaInfo.Formats[0].FormatID != "18" {
		t.Errorf("expected format 18, got %v", got.MediaInfo.Formats)
	}
}
