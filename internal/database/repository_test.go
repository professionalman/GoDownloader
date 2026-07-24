package database

import (
	"context"
	"database/sql"
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

func TestRepository_GetActiveTorrentJobByInfoHash(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	jobRepo := NewSQLiteJobRepository(db)
	torrentRepo := NewSQLiteTorrentRepository(db)
	ctx := context.Background()

	hash := "1234567890abcdef1234567890abcdef12345678"

	t.Run("one active job -> returned", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)
		j := &job.Job{
			ID: "active-1", Source: "magnet:?xt=urn:btih:" + hash, Name: "torrent-1",
			Status: job.StatusDownloading, Type: job.TypeTorrent, Engine: "qbittorrent",
			CreatedAt: now, UpdatedAt: now,
		}
		jobRepo.Create(ctx, j)
		torrentRepo.CreateTorrentJob(ctx, &job.TorrentJobRecord{
			JobID: "active-1", InfoHash: hash, Name: "torrent-1",
		})

		got, err := torrentRepo.GetActiveTorrentJobByInfoHash(ctx, hash)
		if err != nil {
			t.Fatalf("GetActiveTorrentJobByInfoHash failed: %v", err)
		}
		if got == nil || got.JobID != "active-1" {
			t.Errorf("expected active-1, got %v", got)
		}
	})

	t.Run("failed old + active new -> active new returned", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)
		hash2 := "2222222222abcdef1234567890abcdef12345678"

		// Old failed job
		jFailed := &job.Job{
			ID: "failed-old", Source: "magnet:?xt=urn:btih:" + hash2, Name: "torrent-2",
			Status: job.StatusFailed, Type: job.TypeTorrent, Engine: "qbittorrent",
			CreatedAt: now, UpdatedAt: now,
		}
		jobRepo.Create(ctx, jFailed)
		torrentRepo.CreateTorrentJob(ctx, &job.TorrentJobRecord{
			JobID: "failed-old", InfoHash: hash2, Name: "torrent-2",
		})

		// New active job
		jActive := &job.Job{
			ID: "active-new", Source: "magnet:?xt=urn:btih:" + hash2, Name: "torrent-2",
			Status: job.StatusDownloading, Type: job.TypeTorrent, Engine: "qbittorrent",
			CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second),
		}
		jobRepo.Create(ctx, jActive)
		torrentRepo.CreateTorrentJob(ctx, &job.TorrentJobRecord{
			JobID: "active-new", InfoHash: hash2, Name: "torrent-2",
		})

		got, err := torrentRepo.GetActiveTorrentJobByInfoHash(ctx, hash2)
		if err != nil {
			t.Fatalf("GetActiveTorrentJobByInfoHash failed: %v", err)
		}
		if got == nil || got.JobID != "active-new" {
			t.Errorf("expected active-new returned, got %v", got)
		}
	})

	t.Run("terminal jobs only -> returns nil", func(t *testing.T) {
		now := time.Now().Truncate(time.Second)
		hashTerm := "3333333333abcdef1234567890abcdef12345678"

		for _, status := range []job.JobStatus{job.StatusFailed, job.StatusCompleted, job.StatusCancelled} {
			id := "term-" + string(status)
			j := &job.Job{
				ID: id, Source: "magnet:?xt=urn:btih:" + hashTerm, Name: "torrent-term",
				Status: status, Type: job.TypeTorrent, Engine: "qbittorrent",
				CreatedAt: now, UpdatedAt: now,
			}
			jobRepo.Create(ctx, j)
			torrentRepo.CreateTorrentJob(ctx, &job.TorrentJobRecord{
				JobID: id, InfoHash: hashTerm, Name: "torrent-term",
			})
		}

		got, err := torrentRepo.GetActiveTorrentJobByInfoHash(ctx, hashTerm)
		if err != nil {
			t.Fatalf("GetActiveTorrentJobByInfoHash failed: %v", err)
		}
		if got != nil {
			t.Errorf("expected nil for terminal jobs only, got %v", got)
		}
	})
}

func TestSQLite_Migrations(t *testing.T) {
	dir, err := os.MkdirTemp("", "sqlitemig-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	t.Run("Fresh and Reopen DB", func(t *testing.T) {
		dbPath := filepath.Join(dir, "fresh.db")
		db, err := New(dbPath)
		if err != nil {
			t.Fatalf("New() fresh database failed: %v", err)
		}
		db.Close()

		db, err = New(dbPath)
		if err != nil {
			t.Fatalf("New() reopening existing database failed: %v", err)
		}
		db.Close()
	})

	t.Run("Migrate V0.1-like schema", func(t *testing.T) {
		dbPath := filepath.Join(dir, "v01.db")
		conn, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("failed to open v01 db: %v", err)
		}
		// Create V0.1 table with legacy columns: aria2_gid, speed, uppercase status
		v01Table := `CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'DOWNLOADING',
			total_bytes INTEGER NOT NULL DEFAULT 0,
			completed_bytes INTEGER NOT NULL DEFAULT 0,
			progress REAL NOT NULL DEFAULT 0,
			speed INTEGER NOT NULL DEFAULT 1024,
			error TEXT NOT NULL DEFAULT '',
			aria2_gid TEXT NOT NULL DEFAULT 'gid-v01-123',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`
		if _, err := conn.Exec(v01Table); err != nil {
			t.Fatalf("failed to create V0.1 table: %v", err)
		}
		conn.Exec(`INSERT INTO jobs (id, source, name, status, speed, aria2_gid, created_at, updated_at) 
			VALUES ('job-1', 'http://example.com/file', 'file', 'DOWNLOADING', 1024, 'gid-123', datetime('now'), datetime('now'))`)
		conn.Close()

		db, err := New(dbPath)
		if err != nil {
			t.Fatalf("New() migrate V0.1 DB failed: %v", err)
		}
		defer db.Close()

		j, err := db.GetJob("job-1")
		if err != nil || j == nil {
			t.Fatalf("failed to read migrated V0.1 job: %v", err)
		}
		if j.EngineID != "gid-123" {
			t.Errorf("expected engine_id 'gid-123', got %q", j.EngineID)
		}
		if j.SpeedBytesPerSecond != 1024 {
			t.Errorf("expected speed_bytes_per_second 1024, got %d", j.SpeedBytesPerSecond)
		}
		if j.Status != job.StatusDownloading {
			t.Errorf("expected status 'downloading', got %s", j.Status)
		}
	})

	t.Run("Migrate V0.3-like schema", func(t *testing.T) {
		dbPath := filepath.Join(dir, "v03.db")
		conn, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			t.Fatalf("failed to open v03 db: %v", err)
		}
		v03Table := `CREATE TABLE jobs (
			id TEXT PRIMARY KEY,
			source TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'queued',
			total_bytes INTEGER NOT NULL DEFAULT 0,
			completed_bytes INTEGER NOT NULL DEFAULT 0,
			progress REAL NOT NULL DEFAULT 0,
			speed_bytes_per_second INTEGER NOT NULL DEFAULT 0,
			eta_seconds INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			engine TEXT NOT NULL DEFAULT 'aria2',
			engine_id TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT 'download',
			media_info TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			updated_at DATETIME NOT NULL
		);`
		if _, err := conn.Exec(v03Table); err != nil {
			t.Fatalf("failed to create V0.3 table: %v", err)
		}
		conn.Close()

		db, err := New(dbPath)
		if err != nil {
			t.Fatalf("New() migrate V0.3 DB failed: %v", err)
		}
		defer db.Close()

		// Verify torrent tables & index exist
		var count int
		err = db.conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('torrent_jobs', 'torrent_files')").Scan(&count)
		if err != nil || count != 2 {
			t.Errorf("expected 2 torrent tables after V0.4 migration, got count %d, err=%v", count, err)
		}
		var idxCount int
		err = db.conn.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_torrent_jobs_info_hash'").Scan(&idxCount)
		if err != nil || idxCount != 1 {
			t.Errorf("expected idx_torrent_jobs_info_hash index after V0.4 migration, got count %d, err=%v", idxCount, err)
		}
	})
}
