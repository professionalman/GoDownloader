package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupStateSyncAPIRouter(t *testing.T) (http.Handler, *database.SQLiteJobRepository, *database.SQLiteExecutionRepository, *job.Manager) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api_statesync.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	execRepo := database.NewSQLiteExecutionRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())

	downloadDir := filepath.Join(tempDir, "downloads")
	dataDir := filepath.Join(tempDir, "data")

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), downloadDir, dataDir)
	registry := engine.NewRegistry()
	bus := events.NewInMemoryBus()

	mgr := job.NewManager(jobRepo, registry, bus, downloadDir, nil, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)
	mgr.SetExecutionRepository(execRepo)

	sseHandler := events.NewSSEHandler(bus)
	sseHandler.SetCursorProvider(mgr)

	cfg := &config.Config{
		DownloadDir: downloadDir,
		DataDir:     dataDir,
		DisableAuth: true,
	}

	router := api.NewRouter(cfg, mgr, sseHandler, settingsSvc, catRepo)
	return router, jobRepo, execRepo, mgr
}

func TestSyncSnapshotEndpoint_ReturnsAuthoritativeSnapshot(t *testing.T) {
	router, jobRepo, _, mgr := setupStateSyncAPIRouter(t)
	ctx := context.Background()

	// Insert 2 jobs
	j1 := &job.Job{
		ID:        "job-api-1",
		Source:    "https://example.com/1.mp4",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	j2 := &job.Job{
		ID:        "job-api-2",
		Source:    "https://example.com/2.mp4",
		Status:    job.StatusQueued,
		Type:      job.TypeMedia,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(ctx, j1)
	_ = jobRepo.Create(ctx, j2)

	// Advance cursor
	mgr.AdvanceEventCursor(ctx)
	mgr.AdvanceEventCursor(ctx)
	mgr.AdvanceEventCursor(ctx)

	// Test GET /api/v1/sync/snapshot
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sync/snapshot", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var snapshot job.SyncSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("failed to decode snapshot: %v", err)
	}

	if snapshot.Cursor != 3 {
		t.Fatalf("expected cursor 3, got %d", snapshot.Cursor)
	}
	if len(snapshot.Jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(snapshot.Jobs))
	}
	if snapshot.Queue == nil {
		t.Fatalf("expected non-nil queue snapshot")
	}

	// Test GET /api/v1/jobs X-Cursor header
	reqJobs := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	recJobs := httptest.NewRecorder()
	router.ServeHTTP(recJobs, reqJobs)

	if recJobs.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recJobs.Code)
	}
	if xCursor := recJobs.Header().Get("X-Cursor"); xCursor != "3" {
		t.Fatalf("expected X-Cursor 3, got %q", xCursor)
	}
}
