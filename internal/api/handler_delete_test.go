package api_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupDeleteAPITestRouter(t *testing.T) (http.Handler, job.IJobRepository, *job.Manager) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())

	downloadDir := filepath.Join(tempDir, "downloads")
	dataDir := filepath.Join(tempDir, "data")

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), downloadDir, dataDir)
	registry := engine.NewRegistry()
	mgr := job.NewManager(jobRepo, registry, nil, downloadDir, nil, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)

	cfg := &config.Config{
		DownloadDir: downloadDir,
		DataDir:     dataDir,
		DisableAuth: true,
	}

	router := api.NewRouter(cfg, mgr, nil, settingsSvc, catRepo)
	return router, jobRepo, mgr
}

func TestDeleteJob_Completed_Returns204(t *testing.T) {
	router, jobRepo, _ := setupDeleteAPITestRouter(t)
	ctx := context.Background()

	j := &job.Job{
		ID:        "job-delete-api-1",
		Source:    "https://example.com/test.iso",
		Name:      "test.iso",
		Status:    job.StatusCompleted,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("jobRepo.Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/job-delete-api-1", bytes.NewBufferString(`{"deleteFiles":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify job is removed from repository
	saved, err := jobRepo.GetByID(ctx, "job-delete-api-1")
	if err != nil || saved != nil {
		t.Fatalf("expected job to be deleted, got err=%v, job=%+v", err, saved)
	}
}

func TestDeleteJob_Active_Returns409(t *testing.T) {
	router, jobRepo, _ := setupDeleteAPITestRouter(t)
	ctx := context.Background()

	j := &job.Job{
		ID:        "job-delete-api-active",
		Source:    "https://example.com/test.iso",
		Name:      "test.iso",
		Status:    job.StatusDownloading,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobRepo.Create(ctx, j); err != nil {
		t.Fatalf("jobRepo.Create failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/job-delete-api-active", bytes.NewBufferString(`{"deleteFiles":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status 409 Conflict for downloading job, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Cancel the download before deleting it") {
		t.Fatalf("expected understandable error message, got: %s", rec.Body.String())
	}
}

func TestDeleteJob_NotFound_Returns404(t *testing.T) {
	router, _, _ := setupDeleteAPITestRouter(t)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/nonexistent-id", bytes.NewBufferString(`{"deleteFiles":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteJob_UnknownField_Returns400(t *testing.T) {
	router, jobRepo, _ := setupDeleteAPITestRouter(t)
	ctx := context.Background()

	j := &job.Job{
		ID:        "job-delete-api-bad",
		Source:    "https://example.com/test.iso",
		Name:      "test.iso",
		Status:    job.StatusCompleted,
		Type:      job.TypeDownload,
		Engine:    "aria2",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(ctx, j)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/job-delete-api-bad", bytes.NewBufferString(`{"deleteFiles":false,"unknown":true}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request on unknown field, got %d: %s", rec.Code, rec.Body.String())
	}
}
