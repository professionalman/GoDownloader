package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupAPITestRouter(t *testing.T) (http.Handler, storage.ICategoryRepository, *settings.SettingsService) {
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
	}

	router := api.NewRouter(cfg, mgr, nil, settingsSvc, catRepo)
	return router, catRepo, settingsSvc
}

func TestCategoryAPI_CRUD(t *testing.T) {
	router, catRepo, _ := setupAPITestRouter(t)

	// 1. Initial List (seeded categories)
	req := httptest.NewRequest("GET", "/api/v1/categories", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var seeded []storage.CategoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&seeded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(seeded) < 4 {
		t.Errorf("expected at least 4 seeded categories, got %d", len(seeded))
	}

	// 2. Create New Category
	createPayload := []byte(`{"name":"Software","directory":"Software"}`)
	req = httptest.NewRequest("POST", "/api/v1/categories", bytes.NewBuffer(createPayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d", rec.Code)
	}

	var created storage.CategoryResponse
	json.NewDecoder(rec.Body).Decode(&created)
	if created.Name != "Software" || created.ID == "" {
		t.Errorf("unexpected category response: %+v", created)
	}

	// 3. Update Category
	updatePayload := []byte(`{"name":"Apps & Software","directory":"Apps"}`)
	req = httptest.NewRequest("PUT", "/api/v1/categories/"+created.ID, bytes.NewBuffer(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on update, got %d", rec.Code)
	}

	var updated storage.CategoryResponse
	json.NewDecoder(rec.Body).Decode(&updated)
	if updated.Name != "Apps & Software" || updated.Directory != "Apps" {
		t.Errorf("unexpected updated response: %+v", updated)
	}

	// 4. Delete Category
	req = httptest.NewRequest("DELETE", "/api/v1/categories/"+created.ID, nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on delete, got %d", rec.Code)
	}

	fetched, _ := catRepo.GetByID(context.Background(), created.ID)
	if fetched != nil {
		t.Errorf("expected deleted category to be nil, got %+v", fetched)
	}
}

func TestSettingsAPI_StorageUpdate(t *testing.T) {
	router, _, _ := setupAPITestRouter(t)

	customDir := filepath.Join(t.TempDir(), "custom_downloads")

	// Update storage settings
	payload := []byte(`{
		"storage": {
			"defaultDownloadDirectory": "` + filepath.ToSlash(customDir) + `",
			"minimumFreeSpaceBytes": 2147483648,
			"defaultConflictPolicy": "overwrite"
		}
	}`)

	req := httptest.NewRequest("PUT", "/api/v1/settings", bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK on settings update, got %d: %s", rec.Code, rec.Body.String())
	}

	var st settings.AppSettings
	if err := json.NewDecoder(rec.Body).Decode(&st); err != nil {
		t.Fatalf("decode settings error: %v", err)
	}

	if st.Storage.DefaultDownloadDirectory != filepath.Clean(customDir) {
		t.Errorf("expected default download dir %s, got %s", customDir, st.Storage.DefaultDownloadDirectory)
	}
	if st.Storage.MinimumFreeSpaceBytes != 2147483648 {
		t.Errorf("expected min free space bytes 2147483648, got %d", st.Storage.MinimumFreeSpaceBytes)
	}
	if st.Storage.DefaultConflictPolicy != "overwrite" {
		t.Errorf("expected default conflict policy overwrite, got %s", st.Storage.DefaultConflictPolicy)
	}
}

func TestCategoriesAPI_UsesUpdatedDefaultDirectory(t *testing.T) {
	router, catRepo, settingsSvc := setupAPITestRouter(t)

	// Create relative category "Books" -> "Books"
	cat := &storage.Category{Name: "Books", Directory: "Books"}
	if err := catRepo.Create(context.Background(), cat); err != nil {
		t.Fatalf("Create category failed: %v", err)
	}

	newDir := filepath.Join(t.TempDir(), "new_root")
	os.MkdirAll(newDir, 0755)

	// Update settings
	reqPayload := &settings.UpdateSettingsRequest{}
	reqPayload.Storage = &struct {
		DefaultDownloadDirectory *string `json:"defaultDownloadDirectory,omitempty"`
		TemporaryDirectory       *string `json:"temporaryDirectory,omitempty"`
		MinimumFreeSpaceBytes    *int64  `json:"minimumFreeSpaceBytes,omitempty"`
		DefaultConflictPolicy    *string `json:"defaultConflictPolicy,omitempty"`
	}{
		DefaultDownloadDirectory: &newDir,
	}

	if _, err := settingsSvc.UpdateStorageSettings(context.Background(), reqPayload); err != nil {
		t.Fatalf("UpdateStorageSettings failed: %v", err)
	}

	// GET /api/v1/categories must use updated default directory immediately
	req := httptest.NewRequest("GET", "/api/v1/categories", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var categories []storage.CategoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&categories); err != nil {
		t.Fatalf("decode categories: %v", err)
	}

	var found *storage.CategoryResponse
	for i := range categories {
		if categories[i].ID == cat.ID {
			found = &categories[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("created category Books not found in API response")
	}

	expectedResolved := filepath.Join(newDir, "Books")
	if found.ResolvedDirectory != expectedResolved {
		t.Errorf("expected resolvedDirectory %s, got %s", expectedResolved, found.ResolvedDirectory)
	}
}
