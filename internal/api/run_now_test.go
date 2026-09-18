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

func setupRunNowAPIRouter(t *testing.T, disableAuth bool) (http.Handler, *database.SQLiteJobRepository, *database.SQLiteQueueRepository, *job.Manager, *config.Config) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api_runnow.db")
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
		DisableAuth: disableAuth,
	}

	router := api.NewRouter(cfg, mgr, sseHandler, settingsSvc, catRepo)
	return router, jobRepo, queueRepo, mgr, cfg
}

func TestRunNowEndpoint_Success(t *testing.T) {
	router, jobRepo, queueRepo, _, _ := setupRunNowAPIRouter(t, true)
	ctx := context.Background()

	j := &job.Job{
		ID:        "job-runnow-api",
		Source:    "https://example.com/file.zip",
		Status:    job.StatusQueued,
		Priority:  job.JobPriorityNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &job.QueueEntry{
		JobID:      j.ID,
		Position:   5,
		Action:     job.QueueActionStart,
		EnqueuedAt: time.Now(),
		UpdatedAt:  time.Now(),
	})

	req := httptest.NewRequest("POST", "/api/v1/jobs/"+j.ID+"/run-now", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp job.Job
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID != j.ID || resp.Status != job.StatusQueued {
		t.Fatalf("unexpected job response: %+v", resp)
	}

	// Queue entry must now be at position 0
	entry, err := queueRepo.Get(ctx, j.ID)
	if err != nil {
		t.Fatalf("failed to get queue entry: %v", err)
	}
	if entry.Position != 0 {
		t.Fatalf("expected position 0 for Run Now, got %d", entry.Position)
	}
}

func TestRunNowEndpoint_InvalidStates(t *testing.T) {
	router, jobRepo, _, _, _ := setupRunNowAPIRouter(t, true)
	ctx := context.Background()

	// Downloading job
	jDownloading := &job.Job{ID: "j-downloading", Status: job.StatusDownloading, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, jDownloading)

	req := httptest.NewRequest("POST", "/api/v1/jobs/"+jDownloading.ID+"/run-now", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for downloading job, got %d", w.Code)
	}

	// Completed job
	jCompleted := &job.Job{ID: "j-completed", Status: job.StatusCompleted, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, jCompleted)

	req2 := httptest.NewRequest("POST", "/api/v1/jobs/"+jCompleted.ID+"/run-now", nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for completed job, got %d", w2.Code)
	}

	// Non-existent job
	req3 := httptest.NewRequest("POST", "/api/v1/jobs/nonexistent-id/run-now", nil)
	w3 := httptest.NewRecorder()
	router.ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for non-existent job, got %d", w3.Code)
	}
}

func TestRunNowEndpoint_SecurityMiddlewareEnforced(t *testing.T) {
	router, jobRepo, _, _, _ := setupRunNowAPIRouter(t, false) // Auth enabled!
	ctx := context.Background()

	j := &job.Job{ID: "j-sec-test", Status: job.StatusQueued, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	jobRepo.Create(ctx, j)

	// Unauthenticated request should be rejected (401 or 403)
	req := httptest.NewRequest("POST", "/api/v1/jobs/"+j.ID+"/run-now", nil)
	req.Host = "127.0.0.1:8080"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 for unauthenticated Run Now request, got %d", w.Code)
	}
}
