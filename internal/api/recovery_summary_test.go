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
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupRecoveryAPITestRouter(t *testing.T, disableAuth bool) (http.Handler, job.IJobRepository, *job.Manager, string) {
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
		DisableAuth: disableAuth,
	}

	router := api.NewRouter(cfg, mgr, nil, settingsSvc, catRepo)
	return router, jobRepo, mgr, tempDir
}

func TestGetRecoverySummary_Clean(t *testing.T) {
	router, _, _, _ := setupRecoveryAPITestRouter(t, true)

	req := httptest.NewRequest("GET", "/api/v1/jobs/recovery-summary", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var summary job.RecoverySummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to unmarshal recovery summary JSON: %v", err)
	}

	if summary.ReconciledFinalizations != 0 || summary.ReattachedTransfers != 0 ||
		summary.RestartedMetadataAcquisitions != 0 || summary.InterruptedMediaJobs != 0 || len(summary.Issues) != 0 {
		t.Errorf("expected clean zero-valued summary, got %+v", summary)
	}
}

func TestGetRecoverySummary_RouteOrdering_DoesNotHitGetJob(t *testing.T) {
	router, _, _, _ := setupRecoveryAPITestRouter(t, true)

	req := httptest.NewRequest("GET", "/api/v1/jobs/recovery-summary", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// If route order was incorrect, gorilla/mux would match /jobs/{id} with id="recovery-summary",
	// which would return 404 NOT_FOUND because no job exists with id "recovery-summary".
	// Success (200) proves /jobs/recovery-summary was correctly matched first!
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from literal route, got %d (might have collided with /jobs/{id}): %s", rec.Code, rec.Body.String())
	}
}

func TestGetRecoverySummary_WithInterventions(t *testing.T) {
	router, jobRepo, mgr, _ := setupRecoveryAPITestRouter(t, true)
	ctx := context.Background()

	// Create an interrupted media job to trigger recovery intervention
	mediaJob := &job.Job{
		ID:        "media-api-test",
		Source:    "https://example.com/stream",
		Name:      "video.mp4",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		Engine:    "ytdlp",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(ctx, mediaJob)

	mgr.Recover(ctx)

	req := httptest.NewRequest("GET", "/api/v1/jobs/recovery-summary", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var summary job.RecoverySummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("failed to unmarshal recovery summary JSON: %v", err)
	}

	if summary.InterruptedMediaJobs != 1 {
		t.Errorf("expected InterruptedMediaJobs=1, got %d", summary.InterruptedMediaJobs)
	}
	if len(summary.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(summary.Issues))
	}
	if summary.Issues[0].JobID != "media-api-test" {
		t.Errorf("expected JobID=media-api-test, got %s", summary.Issues[0].JobID)
	}
	if summary.Issues[0].Kind != job.RecoveryIssueInterruptedMedia {
		t.Errorf("expected Kind=interrupted_media, got %s", summary.Issues[0].Kind)
	}
}

func TestGetRecoverySummary_AuthRequired(t *testing.T) {
	router, _, _, _ := setupRecoveryAPITestRouter(t, false)

	req := httptest.NewRequest("GET", "/api/v1/jobs/recovery-summary", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Without auth session/token, security middleware should reject the request (401 or 403)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
		t.Fatalf("expected 401 or 403 without auth, got %d", rec.Code)
	}
}
