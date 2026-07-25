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
	dbPath := filepath.Join(t.TempDir(), "test_settings.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	repo := database.NewSQLiteSettingsRepository(db)
	service := settings.NewSettingsService(repo)
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
}

func TestSettingsService_EnvironmentOverride(t *testing.T) {
	ctx := context.Background()
	svc, db := setupTestSettings(t)
	defer db.Close()

	t.Setenv("MAX_CONCURRENT_DOWNLOADS", "7")

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
}
