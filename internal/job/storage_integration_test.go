package job_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

type fakeFreeSpaceProvider struct {
	freeBytes map[string]int64
}

func (f *fakeFreeSpaceProvider) FreeBytes(path string) (int64, error) {
	cleaned := filepath.Clean(path)
	if val, ok := f.freeBytes[cleaned]; ok {
		return val, nil
	}
	return 10 * 1024 * 1024 * 1024, nil
}

func setupStorageTestManager(t *testing.T, freeSpace map[string]int64) (*job.Manager, *database.DB, *storage.StorageService, storage.ICategoryRepository, *settings.SettingsService) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	torrentRepo := database.NewSQLiteTorrentRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())

	downloadDir := filepath.Join(tempDir, "downloads")
	dataDir := filepath.Join(tempDir, "data")

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir)
	freeProvider := &fakeFreeSpaceProvider{freeBytes: freeSpace}
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, freeProvider, downloadDir, dataDir)

	bus := &fakeBus{}
	registry := engine.NewRegistry()
	fakeEng := &fakeEngine{}
	registry.Register("aria2", fakeEng)

	mgr := job.NewManager(jobRepo, registry, bus, downloadDir, torrentRepo, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)
	mgr.SetCategoryRepository(catRepo)

	sched := job.NewScheduler(jobRepo, queueRepo, func(ctx context.Context) int {
		return 1
	}, mgr.DispatchQueuedJob)
	mgr.SetScheduler(sched)
	sched.Start(context.Background())

	return mgr, db, storageSvc, catRepo, settingsSvc
}

func TestStorageIntegration_InsufficientStartSpace(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	lowPath := filepath.Join(tempDir, "low_space")
	highPath := filepath.Join(tempDir, "high_space")

	freeSpace := map[string]int64{
		filepath.Clean(lowPath):  100 * 1024 * 1024,        // 100 MiB (insufficient for 1 GiB reserve)
		filepath.Clean(highPath): 10 * 1024 * 1024 * 1024, // 10 GiB
	}

	mgr, db, _, _, _ := setupStorageTestManager(t, freeSpace)
	defer db.Close()

	// Job A: low space dest -> START preflight fails -> FAILED, removed from queue
	jobA, err := mgr.CreateWithOptions(ctx, "http://example.com/fileA.zip", job.CreateOptions{
		DestinationDir: lowPath,
		Priority:       job.JobPriorityHigh,
	})
	if err != nil {
		t.Fatalf("failed to create jobA: %v", err)
	}

	// Job B: high space dest -> START preflight succeeds -> DOWNLOADING
	jobB, err := mgr.CreateWithOptions(ctx, "http://example.com/fileB.zip", job.CreateOptions{
		DestinationDir: highPath,
		Priority:       job.JobPriorityNormal,
	})
	if err != nil {
		t.Fatalf("failed to create jobB: %v", err)
	}

	sched := mgr.GetScheduler()
	sched.Kick()
	time.Sleep(100 * time.Millisecond)

	updatedA, _ := mgr.Get(ctx, jobA.ID)
	if updatedA.Status != job.StatusFailed {
		t.Errorf("expected Job A status FAILED due to insufficient disk space, got %s", updatedA.Status)
	}

	updatedB, _ := mgr.Get(ctx, jobB.ID)
	if updatedB.Status != job.StatusDownloading {
		t.Errorf("expected Job B status DOWNLOADING, got %s", updatedB.Status)
	}
}

func TestStorageIntegration_InsufficientResumeSpace(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	path := filepath.Join(tempDir, "space")
	freeMap := map[string]int64{
		filepath.Clean(path): 10 * 1024 * 1024 * 1024, // 10 GiB initially
	}

	mgr, db, _, _, _ := setupStorageTestManager(t, freeMap)
	defer db.Close()

	// 1. Create job with high space -> START succeeds
	j, err := mgr.CreateWithOptions(ctx, "http://example.com/file.zip", job.CreateOptions{
		DestinationDir: path,
		Priority:       job.JobPriorityNormal,
	})
	if err != nil {
		t.Fatalf("create job error: %v", err)
	}

	sched := mgr.GetScheduler()
	sched.Kick()
	time.Sleep(100 * time.Millisecond)

	// 2. Pause job -> status becomes PAUSED, queue entry gets QueueActionResume
	if _, err := mgr.Pause(ctx, j.ID); err != nil {
		t.Fatalf("failed to pause job: %v", err)
	}

	// 3. Drop disk space to low space
	freeMap[filepath.Clean(path)] = 100 * 1024 * 1024 // 100 MiB (insufficient for 1 GiB reserve)

	// 4. Resume job -> preflight fails -> reverts to StatusPaused, retains queue row
	if _, err := mgr.Resume(ctx, j.ID); err != nil {
		t.Fatalf("failed to resume job: %v", err)
	}

	sched.Kick()
	time.Sleep(100 * time.Millisecond)

	updated, _ := mgr.Get(ctx, j.ID)
	if updated.Status != job.StatusPaused {
		t.Errorf("expected Job status PAUSED after insufficient space resume, got %s", updated.Status)
	}
}

func TestStorageIntegration_DestinationSnapshotImmutability(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	mgr, db, _, catRepo, settingsSvc := setupStorageTestManager(t, nil)
	defer db.Close()

	cat := &storage.Category{Name: "Movies", Directory: "Movies"}
	catRepo.Create(ctx, cat)

	// Create Job A with category
	jobA, err := mgr.CreateWithOptions(ctx, "http://example.com/movie1.mp4", job.CreateOptions{
		CategoryID: cat.ID,
	})
	if err != nil {
		t.Fatalf("create job A error: %v", err)
	}

	destA := jobA.DestinationDir

	// Update settings & category directory
	newDefault := filepath.Join(tempDir, "NewDefault")
	settingsSvc.UpdateStorageSettings(ctx, &settings.UpdateSettingsRequest{
		Storage: &struct {
			DefaultDownloadDirectory *string `json:"defaultDownloadDirectory,omitempty"`
			TemporaryDirectory       *string `json:"temporaryDirectory,omitempty"`
			MinimumFreeSpaceBytes    *int64  `json:"minimumFreeSpaceBytes,omitempty"`
			DefaultConflictPolicy    *string `json:"defaultConflictPolicy,omitempty"`
		}{
			DefaultDownloadDirectory: &newDefault,
		},
	})

	cat.Directory = "Films"
	catRepo.Update(ctx, cat)

	// Assert Job A's destination was NOT mutated
	fetchedA, _ := mgr.Get(ctx, jobA.ID)
	if fetchedA.DestinationDir != destA {
		t.Errorf("expected Job A destination %s to remain unchanged, got %s", destA, fetchedA.DestinationDir)
	}

	// Delete category
	catRepo.Delete(ctx, cat.ID)

	// Assert Job A's destination still unchanged after category deletion
	fetchedAAfterDel, _ := mgr.Get(ctx, jobA.ID)
	if fetchedAAfterDel.DestinationDir != destA {
		t.Errorf("expected Job A destination %s after category deletion, got %s", destA, fetchedAAfterDel.DestinationDir)
	}
}
