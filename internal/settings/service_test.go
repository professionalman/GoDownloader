package settings_test

import (
	"context"
	"path/filepath"
	"testing"

	"downloader/internal/database"
	"downloader/internal/settings"
)

func setupTestSettings(t *testing.T) (*settings.SettingsService, *database.DB) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_settings.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	repo := database.NewSQLiteSettingsRepository(db)
	service := settings.NewSettingsService(repo, filepath.Join(tempDir, "downloads"), filepath.Join(tempDir, "data"))
	return service, db
}

func TestSettingsService_Defaults(t *testing.T) {
	ctx := context.Background()
	svc, db := setupTestSettings(t)
	defer db.Close()

	st, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("unexpected error getting settings: %v", err)
	}

	if st.Queue.MaxConcurrentDownloads != 3 {
		t.Errorf("expected default maxConcurrentDownloads=3, got %d", st.Queue.MaxConcurrentDownloads)
	}
	if st.Storage.MinimumFreeSpaceBytes != 1073741824 {
		t.Errorf("expected default 1 GiB free space reserve, got %d", st.Storage.MinimumFreeSpaceBytes)
	}
	if st.Storage.DefaultConflictPolicy != "rename" {
		t.Errorf("expected default conflict policy rename, got %s", st.Storage.DefaultConflictPolicy)
	}
}

func TestSettingsService_UpdateAndValidation(t *testing.T) {
	ctx := context.Background()
	svc, db := setupTestSettings(t)
	defer db.Close()

	// Invalid value < 1
	if _, err := svc.UpdateQueueSettings(ctx, 0); err == nil {
		t.Errorf("expected error for maxConcurrentDownloads=0, got nil")
	}

	// Invalid value > 20
	if _, err := svc.UpdateQueueSettings(ctx, 25); err == nil {
		t.Errorf("expected error for maxConcurrentDownloads=25, got nil")
	}

	// Valid update
	updated, err := svc.UpdateQueueSettings(ctx, 5)
	if err != nil {
		t.Fatalf("unexpected error updating settings: %v", err)
	}

	if updated.Queue.MaxConcurrentDownloads != 5 {
		t.Errorf("expected maxConcurrentDownloads=5, got %d", updated.Queue.MaxConcurrentDownloads)
	}

	if svc.EffectiveMaxConcurrentDownloads(ctx) != 5 {
		t.Errorf("expected effective limit=5, got %d", svc.EffectiveMaxConcurrentDownloads(ctx))
	}

	// Test storage settings update
	tempDir := t.TempDir()
	newDownloadDir := filepath.Join(tempDir, "NewDownloads")
	newMinFree := int64(209715200) // 200 MB
	invalidPolicy := "invalid_policy"
	validPolicy := "overwrite"

	reqInvalid := &settings.UpdateSettingsRequest{
		Storage: &struct {
			DefaultDownloadDirectory *string `json:"defaultDownloadDirectory,omitempty"`
			TemporaryDirectory       *string `json:"temporaryDirectory,omitempty"`
			MinimumFreeSpaceBytes    *int64  `json:"minimumFreeSpaceBytes,omitempty"`
			DefaultConflictPolicy    *string `json:"defaultConflictPolicy,omitempty"`
		}{
			DefaultConflictPolicy: &invalidPolicy,
		},
	}
	if _, err := svc.UpdateStorageSettings(ctx, reqInvalid); err == nil {
		t.Errorf("expected error for invalid conflict policy, got nil")
	}

	reqValid := &settings.UpdateSettingsRequest{
		Storage: &struct {
			DefaultDownloadDirectory *string `json:"defaultDownloadDirectory,omitempty"`
			TemporaryDirectory       *string `json:"temporaryDirectory,omitempty"`
			MinimumFreeSpaceBytes    *int64  `json:"minimumFreeSpaceBytes,omitempty"`
			DefaultConflictPolicy    *string `json:"defaultConflictPolicy,omitempty"`
		}{
			DefaultDownloadDirectory: &newDownloadDir,
			MinimumFreeSpaceBytes:    &newMinFree,
			DefaultConflictPolicy:    &validPolicy,
		},
	}
	updatedSt, err := svc.UpdateStorageSettings(ctx, reqValid)
	if err != nil {
		t.Fatalf("failed to update storage settings: %v", err)
	}

	if updatedSt.Storage.DefaultDownloadDirectory != newDownloadDir {
		t.Errorf("expected defaultDownloadDir %s, got %s", newDownloadDir, updatedSt.Storage.DefaultDownloadDirectory)
	}
	if updatedSt.Storage.MinimumFreeSpaceBytes != newMinFree {
		t.Errorf("expected minimumFreeSpaceBytes %d, got %d", newMinFree, updatedSt.Storage.MinimumFreeSpaceBytes)
	}
	if updatedSt.Storage.DefaultConflictPolicy != "overwrite" {
		t.Errorf("expected conflict policy overwrite, got %s", updatedSt.Storage.DefaultConflictPolicy)
	}
}

func TestSettingsService_EnvironmentOverride(t *testing.T) {
	ctx := context.Background()
	svc, db := setupTestSettings(t)
	defer db.Close()

	t.Setenv("MAX_CONCURRENT_DOWNLOADS", "7")
	t.Setenv("DEFAULT_CONFLICT_POLICY", "fail")

	if svc.EffectiveMaxConcurrentDownloads(ctx) != 7 {
		t.Errorf("expected env override limit=7, got %d", svc.EffectiveMaxConcurrentDownloads(ctx))
	}

	st, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings error: %v", err)
	}
	if st.Queue.MaxConcurrentDownloads != 3 {
		t.Errorf("expected stored maxConcurrentDownloads=3, got %d", st.Queue.MaxConcurrentDownloads)
	}
	if st.Queue.EffectiveMaxConcurrentDownloads != 7 {
		t.Errorf("expected effectiveMaxConcurrentDownloads=7 under env override, got %d", st.Queue.EffectiveMaxConcurrentDownloads)
	}
	if !st.Queue.EnvironmentOverride {
		t.Errorf("expected EnvironmentOverride=true, got false")
	}
	if st.Storage.EffectiveDefaultConflictPolicy != "fail" {
		t.Errorf("expected effective conflict policy fail from ENV, got %s", st.Storage.EffectiveDefaultConflictPolicy)
	}
	if !st.Storage.Overrides.DefaultConflictPolicy {
		t.Errorf("expected DefaultConflictPolicy override true")
	}
}
