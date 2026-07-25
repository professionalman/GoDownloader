package database_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func TestV06Migration_LegacyDestinationUsesEffectiveDefault(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy.db")

	// 1. Create legacy V0.5 DB with a job having empty destination_dir
	rawConn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("failed to open raw sqlite db: %v", err)
	}
	createV05Schema := `
	CREATE TABLE jobs (
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
		priority TEXT NOT NULL DEFAULT 'normal',
		batch_id TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	);`
	if _, err := rawConn.Exec(createV05Schema); err != nil {
		t.Fatalf("create V05 schema failed: %v", err)
	}
	insertJob := `INSERT INTO jobs (id, source, name, status, engine, type, created_at, updated_at) VALUES ('job-legacy-1', 'https://example.com/file.zip', 'file.zip', 'queued', 'aria2', 'download', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`
	if _, err := rawConn.Exec(insertJob); err != nil {
		t.Fatalf("insert legacy job failed: %v", err)
	}
	rawConn.Close()

	// 2. Open via database.New (runs V0.6 migration)
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New failed: %v", err)
	}
	defer db.Close()

	repo := database.NewSQLiteJobRepository(db)
	j, err := repo.GetByID(context.Background(), "job-legacy-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	// V0.6 DB migration must leave legacy destination_dir empty
	if j.DestinationDir != "" {
		t.Errorf("expected empty DestinationDir after DB migration, got %s", j.DestinationDir)
	}

	// 3. Hydrate via Manager with custom effective download dir
	customEffectiveDir := filepath.Join(tmpDir, "CustomDownloads")
	os.MkdirAll(customEffectiveDir, 0755)

	settingsRepo := database.NewSQLiteSettingsRepository(db)
	settingsSvc := settings.NewSettingsService(settingsRepo, customEffectiveDir, tmpDir)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), customEffectiveDir, tmpDir)

	reg := engine.NewRegistry()
	bus := events.NewInMemoryBus()
	mgr := job.NewManager(repo, reg, bus, customEffectiveDir, nil, tmpDir)
	mgr.SetStorageService(storageSvc)

	mgr.StartBackgroundTasks(context.Background())
	defer mgr.Stop()

	// Verify job DestinationDir is hydrated to customEffectiveDir (NOT ./downloads)
	jHydrated, err := repo.GetByID(context.Background(), "job-legacy-1")
	if err != nil {
		t.Fatalf("GetByID post-hydration failed: %v", err)
	}
	if jHydrated.DestinationDir != customEffectiveDir {
		t.Errorf("expected hydrated DestinationDir %s, got %s", customEffectiveDir, jHydrated.DestinationDir)
	}
}

func TestV06Migration_PreservesExistingDestination(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "legacy_existing.db")

	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New failed: %v", err)
	}
	defer db.Close()

	repo := database.NewSQLiteJobRepository(db)
	ctx := context.Background()
	existingDest := filepath.Join(tmpDir, "ExistingFolder")

	j := &job.Job{
		ID:             "job-existing-1",
		Source:         "https://example.com/file.zip",
		Name:           "file.zip",
		Status:         job.StatusQueued,
		Engine:         "aria2",
		DestinationDir: existingDest,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("repo.Create failed: %v", err)
	}

	// Reopen DB and hydrate
	db.Close()

	db2, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("database.New reopen failed: %v", err)
	}
	defer db2.Close()

	repo2 := database.NewSQLiteJobRepository(db2)
	got, err := repo2.GetByID(ctx, "job-existing-1")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}

	if got.DestinationDir != existingDest {
		t.Errorf("expected preserved DestinationDir %s, got %s", existingDest, got.DestinationDir)
	}
}

func TestV06Migration_SeedCategoriesOnlyOnce(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "seed.db")

	// Initial migration to V0.6 -> seeds 4 default categories
	db1, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("initial database.New failed: %v", err)
	}

	catRepo1 := storage.NewSQLiteCategoryRepository(db1.Conn())
	cats, err := catRepo1.List(context.Background())
	if err != nil {
		t.Fatalf("list categories failed: %v", err)
	}
	if len(cats) != 4 {
		t.Fatalf("expected 4 seeded categories on initial V0.6 migration, got %d", len(cats))
	}

	// User intentionally deletes all categories
	for _, c := range cats {
		if err := catRepo1.Delete(context.Background(), c.ID); err != nil {
			t.Fatalf("failed to delete category %s: %v", c.Name, err)
		}
	}

	remaining, err := catRepo1.List(context.Background())
	if err != nil || len(remaining) != 0 {
		t.Fatalf("expected 0 categories after deletion, got %d", len(remaining))
	}

	db1.Close()

	// Reopen DB (migrateToV06 runs again) -> MUST NOT re-seed default categories!
	db2, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("reopen database.New failed: %v", err)
	}
	defer db2.Close()

	catRepo2 := storage.NewSQLiteCategoryRepository(db2.Conn())
	catsAfterReopen, err := catRepo2.List(context.Background())
	if err != nil {
		t.Fatalf("list categories after reopen failed: %v", err)
	}

	if len(catsAfterReopen) != 0 {
		t.Errorf("expected categories to remain empty after restart, got %d categories", len(catsAfterReopen))
	}
}
