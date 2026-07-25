package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/settings"
	"downloader/internal/storage"
)

type dummyCatRepo struct{}

func (d *dummyCatRepo) Create(ctx context.Context, cat *storage.Category) error { return nil }
func (d *dummyCatRepo) GetByID(ctx context.Context, id string) (*storage.Category, error) {
	return nil, nil
}
func (d *dummyCatRepo) GetByName(ctx context.Context, name string) (*storage.Category, error) {
	return nil, nil
}
func (d *dummyCatRepo) List(ctx context.Context) ([]storage.Category, error)    { return nil, nil }
func (d *dummyCatRepo) Update(ctx context.Context, cat *storage.Category) error { return nil }
func (d *dummyCatRepo) Delete(ctx context.Context, id string) error             { return nil }

type dummySettingsRepo struct{}

func (d *dummySettingsRepo) Get(ctx context.Context, key string) (string, error) { return "", nil }
func (d *dummySettingsRepo) Set(ctx context.Context, key, value string) error    { return nil }

func setupStorageTestEnv(t *testing.T) (*Manager, *fakeJobRepository, *fakeQueueRepo, *storage.StorageService, string, string) {
	t.Helper()
	tmpDir := t.TempDir()
	downloadDir := filepath.Join(tmpDir, "downloads")
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(downloadDir, 0755)
	os.MkdirAll(dataDir, 0755)

	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	catRepo := &dummyCatRepo{}
	settingsRepo := &dummySettingsRepo{}

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir)
	freeSpace := storage.NewOSFreeSpaceProvider()
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, freeSpace, downloadDir, dataDir)

	bus := newFakeEventBus()
	reg := &fakeEngineRegistry{engines: make(map[string]IEngine)}

	mgr := NewManager(jobRepo, reg, bus, downloadDir, nil, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)

	return mgr, jobRepo, queueRepo, storageSvc, downloadDir, dataDir
}

func TestMediaCompletion_MissingFinalArtifactFails(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_empty")
	storageSvc.PrepareWorkDir(ctx, "job-media-missing", workDir)

	j := &Job{
		ID:             "job-media-missing",
		Source:         "https://youtube.com/watch?v=123",
		Name:           "Video",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp-1",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	status := &EngineStatus{
		Status:     StatusCompleted,
		Progress:   100,
		OutputPath: "", // missing output file
	}

	mgr.UpdateJobFromEngine(ctx, j, status, true)

	updated, err := jobRepo.GetByID(ctx, "job-media-missing")
	if err != nil {
		t.Fatalf("GetByID failed: %v", err)
	}
	if updated.Status != StatusFailed {
		t.Errorf("expected StatusFailed when output file missing, got %s", updated.Status)
	}
	if updated.Error != "media completed but final output file was not found" {
		t.Errorf("unexpected error message: %s", updated.Error)
	}
	if updated.FinalPath != "" {
		t.Errorf("expected empty FinalPath on failure, got %s", updated.FinalPath)
	}
}

func TestMediaDispatch_PrepareWorkDirFailureDoesNotStartEngine(t *testing.T) {
	mgr, jobRepo, queueRepo, _, _, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	startCalled := false
	fakeEng := &fakeEngine{
		startFunc: func(ctx context.Context, j *Job, dir string) (string, error) {
			startCalled = true
			return "gid123", nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["fake"] = fakeEng

	blockerFile := filepath.Join(t.TempDir(), "blocker_file")
	os.WriteFile(blockerFile, []byte("file blocker"), 0644)
	invalidWorkDir := filepath.Join(blockerFile, "sub_workdir")

	j := &Job{
		ID:             "job-prep-fail",
		Source:         "https://youtube.com/watch?v=456",
		Name:           "Video Prep Fail",
		Status:         StatusQueued,
		Engine:         "fake",
		Type:           TypeMedia,
		DestinationDir: t.TempDir(),
		WorkDir:        invalidWorkDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionStart, EnqueuedAt: time.Now()})

	mgr.dispatchQueuedJob(ctx, qj)

	if startCalled {
		t.Errorf("expected engine Start NOT to be called when PrepareWorkDir fails")
	}

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusFailed {
		t.Errorf("expected job status FAILED after PrepareWorkDir failure, got %s", updated.Status)
	}
}

func TestMediaFailure_CleansMarkedWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_fail")
	storageSvc.PrepareWorkDir(ctx, "job-media-fail", workDir)

	j := &Job{
		ID:             "job-media-fail",
		Source:         "https://youtube.com/watch?v=789",
		Name:           "Video Fail",
		Status:         StatusDownloading,
		Engine:         "fake",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusFailed, Error: "download error"}, true)

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected failed media WorkDir to be cleaned up")
	}
}

func TestMediaCancel_CleansMarkedWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cancel")
	storageSvc.PrepareWorkDir(ctx, "job-media-cancel", workDir)

	j := &Job{
		ID:             "job-media-cancel",
		Source:         "https://youtube.com/watch?v=000",
		Name:           "Video Cancel",
		Status:         StatusDownloading,
		Engine:         "fake",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	mgr.Cancel(ctx, j.ID)

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected cancelled media WorkDir to be cleaned up")
	}
}

func TestStartupCleanup_RemovesTerminalMarkedWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "tmp", "workdir_terminal")
	storageSvc.PrepareWorkDir(ctx, "job-terminal-1", workDir)

	j := &Job{
		ID:             "job-terminal-1",
		Source:         "https://example.com/file",
		Name:           "Terminal Job",
		Status:         StatusFailed,
		Engine:         "fake",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	mgr.StartBackgroundTasks(ctx)

	if _, err := os.Stat(workDir); !os.IsNotExist(err) {
		t.Errorf("expected terminal marked WorkDir to be cleaned up on startup")
	}
}

func TestStartupCleanup_PreservesActiveWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "tmp", "workdir_active")
	storageSvc.PrepareWorkDir(ctx, "job-active-1", workDir)

	j := &Job{
		ID:             "job-active-1",
		Source:         "https://example.com/file",
		Name:           "Active Job",
		Status:         StatusQueued,
		Engine:         "fake",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	mgr.StartBackgroundTasks(ctx)

	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Errorf("expected active job WorkDir to be preserved on startup")
	}
}

func TestStartupCleanup_IgnoresUnmarkedDirectory(t *testing.T) {
	mgr, _, _, _, _, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	unmarkedDir := filepath.Join(dataDir, "tmp", "unmarked_user_dir")
	os.MkdirAll(unmarkedDir, 0755)
	testFilePath := filepath.Join(unmarkedDir, "user_file.txt")
	os.WriteFile(testFilePath, []byte("important user data"), 0644)

	mgr.StartBackgroundTasks(ctx)

	if _, err := os.Stat(unmarkedDir); os.IsNotExist(err) {
		t.Errorf("UNMARKED directory was deleted! Startup cleanup MUST NOT delete unmarked directories")
	}
}

func TestTorrentSelection_UpdatesSelectedTotalBytes(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	fakeT := &fakeTorrentEngine{
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "file1.iso", Size: 5 * 1024 * 1024 * 1024},  // 5 GB
				{Index: 1, Path: "file2.iso", Size: 10 * 1024 * 1024 * 1024}, // 10 GB
			}, nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = fakeT

	j := &Job{
		ID:             "torrent-select-1",
		Source:         "https://example.com/test.torrent",
		Name:           "Test Torrent",
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		EngineID:       "hash123",
		Type:           TypeTorrent,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	updatedJ, err := mgr.StartTorrent(ctx, j.ID, selections, false)
	if err != nil {
		t.Fatalf("StartTorrent failed: %v", err)
	}

	expectedTotal := int64(5 * 1024 * 1024 * 1024)
	if updatedJ.TotalBytes != expectedTotal {
		t.Errorf("expected TotalBytes %d for selected files, got %d", expectedTotal, updatedJ.TotalBytes)
	}

	inRepo, _ := jobRepo.GetByID(ctx, j.ID)
	if inRepo.TotalBytes != expectedTotal {
		t.Errorf("expected persisted TotalBytes %d, got %d", expectedTotal, inRepo.TotalBytes)
	}
}

func TestTorrentSelectedSize_DiskPreflight(t *testing.T) {
	_, _, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	err := storageSvc.Preflight(ctx, downloadDir, "", 5*1024*1024*1024, 0)
	if err != nil {
		t.Errorf("expected preflight to pass for 5GB requirement, got %v", err)
	}
}

func TestMediaRetry_InvalidWorkDirMarkerReturnsError(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_invalid_marker")
	os.MkdirAll(workDir, 0755)
	// Write wrong marker (job ID mismatch)
	os.WriteFile(filepath.Join(workDir, storage.WorkDirMarkerFilename), []byte("wrong_job_id\n"), 0644)
	os.WriteFile(filepath.Join(workDir, "unowned.txt"), []byte("data"), 0644)

	j := &Job{
		ID:             "job-retry-bad-marker",
		Source:         "https://youtube.com/watch?v=999",
		Name:           "Failed Media Job",
		Status:         StatusFailed,
		Engine:         "teststorage",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	_, err := mgr.Retry(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected Retry to fail on invalid workdir marker, got nil")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrStorageError {
		t.Errorf("expected ErrStorageError on invalid marker retry, got %v", err)
	}

	// WorkDir must NOT be deleted!
	if _, statErr := os.Stat(workDir); os.IsNotExist(statErr) {
		t.Errorf("unowned WorkDir was deleted on retry failure!")
	}
}

func TestMediaRetry_WorkDirCleanupFailureReturnsError(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cleanup_fail")
	storageSvc.PrepareWorkDir(ctx, "job-retry-cleanup-fail", workDir)

	j := &Job{
		ID:             "job-retry-cleanup-fail",
		Source:         "https://youtube.com/watch?v=777",
		Name:           "Failed Media Job",
		Status:         StatusFailed,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	// Inject error on removeAllFunc (which is called inside CleanupWorkDir)
	reset := storage.SetRemoveAllFuncForTest(func(path string) error {
		return errors.New("simulated cleanup error")
	})
	defer reset()

	_, err := mgr.Retry(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected Retry to fail when CleanupWorkDir returns an error, got nil")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrStorageError {
		t.Errorf("expected ErrStorageError on cleanup failure, got %v", err)
	}

	// Verify status in repo is STILL FAILED (analysis did not start)
	inRepo, _ := jobRepo.GetByID(ctx, j.ID)
	if inRepo.Status != StatusFailed {
		t.Errorf("expected job status to remain FAILED when cleanup fails, got %s", inRepo.Status)
	}
}

func TestMediaRetry_CleansValidWorkDirBeforeAnalysis(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_retry_valid")
	storageSvc.PrepareWorkDir(ctx, "job-retry-valid", workDir)

	staleFile := filepath.Join(workDir, "stale_attempt.mp4")
	os.WriteFile(staleFile, []byte("stale attempt data"), 0644)

	j := &Job{
		ID:             "job-retry-valid",
		Source:         "https://youtube.com/watch?v=111",
		Name:           "Failed Media Job Valid",
		Status:         StatusFailed,
		Engine:         "teststorage",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	updated, err := mgr.Retry(ctx, j.ID)
	if err != nil {
		t.Fatalf("Retry failed for valid marked workdir: %v", err)
	}

	if updated.Status != StatusAnalyzing {
		t.Errorf("expected status ANALYZING after retry, got %s", updated.Status)
	}

	// Stale file from previous attempt must be cleaned up
	if _, statErr := os.Stat(staleFile); !os.IsNotExist(statErr) {
		t.Errorf("stale attempt file survived retry!")
	}
}

func TestMediaCompletion_PersistsFinalPathBeforeCompletedEvent(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_completion_event")
	storageSvc.PrepareWorkDir(ctx, "job-completion-order", workDir)
	srcFile := filepath.Join(workDir, "video.mp4")
	os.WriteFile(srcFile, []byte("completed video content"), 0644)

	j := &Job{
		ID:             "job-completion-order",
		Source:         "https://youtube.com/watch?v=222",
		Name:           "Video",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp-1",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	var publishedEvent Event
	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	status := &EngineStatus{
		Status:     StatusCompleted,
		Progress:   100,
		OutputPath: srcFile,
	}

	mgr.UpdateJobFromEngine(ctx, j, status, true)

	select {
	case publishedEvent = <-ch:
	default:
	}

	if publishedEvent.Type != EventJobCompleted {
		t.Fatalf("expected EventJobCompleted, got %s", publishedEvent.Type)
	}

	eventJob := publishedEvent.Job
	if eventJob.Status != StatusCompleted || eventJob.FinalPath == "" {
		t.Errorf("event observer received incomplete job state: status=%s, finalPath=%s", eventJob.Status, eventJob.FinalPath)
	}

	inRepo, _ := jobRepo.GetByID(ctx, j.ID)
	if inRepo.Status != StatusCompleted || inRepo.FinalPath != eventJob.FinalPath {
		t.Errorf("persisted repo job does not match completed state: status=%s, finalPath=%s", inRepo.Status, inRepo.FinalPath)
	}
}

func TestMediaCompletion_PersistenceFailureDoesNotPublishCompleted(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_completion_fail")
	storageSvc.PrepareWorkDir(ctx, "job-completion-fail", workDir)
	srcFile := filepath.Join(workDir, "video.mp4")
	os.WriteFile(srcFile, []byte("video content"), 0644)

	j := &Job{
		ID:             "job-completion-fail",
		Source:         "https://youtube.com/watch?v=333",
		Name:           "Video Fail",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp-1",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	// Inject error on repo.Update
	jobRepo.updateErr = errors.New("simulated repo update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	status := &EngineStatus{
		Status:     StatusCompleted,
		Progress:   100,
		OutputPath: srcFile,
	}

	mgr.UpdateJobFromEngine(ctx, j, status, true)

	// Verify NO event was published
	select {
	case ev := <-ch:
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted was published even though DB persistence failed!")
		}
	default:
	}

	// WorkDir must NOT be cleaned up when persistence fails
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Errorf("WorkDir was cleaned up when DB persistence failed!")
	}
}

func TestMediaFailure_PersistenceFailurePreservesWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_fail_persistence")
	storageSvc.PrepareWorkDir(ctx, "job-fail-pers", workDir)

	j := &Job{
		ID:             "job-fail-pers",
		Source:         "https://youtube.com/watch?v=444",
		Name:           "Video Fail",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	// Inject error on repo.Update
	jobRepo.updateErr = errors.New("simulated repo update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusFailed, Error: "download error"}, true)

	select {
	case ev := <-ch:
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed was published even though DB persistence failed!")
		}
	default:
	}

	// WorkDir must NOT be cleaned up
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Errorf("WorkDir was cleaned up when DB persistence failed!")
	}
}

func TestMediaCancelled_PersistenceFailurePreservesWorkDir(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cancel_persistence")
	storageSvc.PrepareWorkDir(ctx, "job-cancel-pers", workDir)

	j := &Job{
		ID:             "job-cancel-pers",
		Source:         "https://youtube.com/watch?v=555",
		Name:           "Video Cancel",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	// Inject error on repo.Update
	jobRepo.updateErr = errors.New("simulated repo update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCancelled}, true)

	select {
	case ev := <-ch:
		if ev.Type == EventJobCancelled {
			t.Errorf("EventJobCancelled was published even though DB persistence failed!")
		}
	default:
	}

	// WorkDir must NOT be cleaned up
	if _, err := os.Stat(workDir); os.IsNotExist(err) {
		t.Errorf("WorkDir was cleaned up when DB persistence failed!")
	}
}

func TestManager_Cancel_PersistenceFailureDoesNotPublishOrCleanup(t *testing.T) {
	mgr, jobRepo, queueRepo, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cancel_pers_fail")
	storageSvc.PrepareWorkDir(ctx, "job-cancel-pers-fail", workDir)

	j := &Job{
		ID:             "job-cancel-pers-fail",
		Source:         "https://youtube.com/watch?v=888",
		Name:           "Cancel Persist Fail Video",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionStart}

	// Inject error on repo.Update
	jobRepo.updateErr = errors.New("simulated DB update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	_, err := mgr.Cancel(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected Cancel to fail when DB update fails, got nil")
	}

	// 1. EventJobCancelled MUST NOT be emitted
	select {
	case ev := <-ch:
		if ev.Type == EventJobCancelled {
			t.Errorf("EventJobCancelled was emitted even though DB update failed!")
		}
	default:
	}

	// 2. WorkDir MUST NOT be deleted
	if _, statErr := os.Stat(workDir); os.IsNotExist(statErr) {
		t.Errorf("WorkDir was deleted even though DB update failed!")
	}

	// 3. Queue row MUST NOT be deleted
	if _, exists := queueRepo.entries[j.ID]; !exists {
		t.Errorf("Queue entry was deleted even though DB update failed!")
	}
}

func TestManager_Cancel_PersistsBeforeQueueDeleteAndCleanup(t *testing.T) {
	mgr, jobRepo, queueRepo, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cancel_order")
	storageSvc.PrepareWorkDir(ctx, "job-cancel-order", workDir)

	j := &Job{
		ID:             "job-cancel-order",
		Source:         "https://youtube.com/watch?v=999",
		Name:           "Cancel Order Video",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionStart}

	var publishedEvent Event
	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	cancelledJ, err := mgr.Cancel(ctx, j.ID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	if cancelledJ.Status != StatusCancelled {
		t.Errorf("expected status CANCELLED, got %s", cancelledJ.Status)
	}

	select {
	case publishedEvent = <-ch:
	default:
	}

	if publishedEvent.Type != EventJobCancelled {
		t.Errorf("expected EventJobCancelled, got %s", publishedEvent.Type)
	}

	// Queue entry must be cleaned up after successful durable cancellation
	if _, exists := queueRepo.entries[j.ID]; exists {
		t.Errorf("expected queue entry to be deleted after cancellation")
	}

	// WorkDir must be cleaned up
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("expected WorkDir to be cleaned up after cancellation")
	}
}

func TestDispatch_PreflightStartPersistenceFailureKeepsQueue(t *testing.T) {
	mgr, jobRepo, queueRepo, _, _, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "preflight-start-fail",
		Source:         "https://example.com/huge.iso",
		Name:           "Huge Iso",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024, // 10 TB requirement (fails preflight)
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionStart}

	// Inject DB persistence error
	jobRepo.updateErr = errors.New("simulated DB update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	err := mgr.DispatchQueuedJob(ctx, qj)
	if err == nil {
		t.Fatalf("expected dispatch to fail, got nil")
	}

	// Queue entry MUST remain
	if _, exists := queueRepo.entries[j.ID]; !exists {
		t.Errorf("queue row was deleted when DB persistence failed!")
	}

	// EventJobFailed MUST NOT be published
	select {
	case ev := <-ch:
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed was published when DB persistence failed!")
		}
	default:
	}
}

func TestDispatch_PreflightResumePersistenceFailureKeepsResumeRow(t *testing.T) {
	mgr, jobRepo, queueRepo, _, _, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "preflight-resume-fail",
		Source:         "https://example.com/huge.iso",
		Name:           "Huge Iso Resume",
		Status:         StatusPaused,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024, // 10 TB
		DestinationDir: t.TempDir(),
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionResume}
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionResume}

	// Inject DB persistence error
	jobRepo.updateErr = errors.New("simulated DB update failure")

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	err := mgr.DispatchQueuedJob(ctx, qj)
	if err == nil {
		t.Fatalf("expected dispatch to fail, got nil")
	}

	// QueueActionResume row MUST remain
	if entry, exists := queueRepo.entries[j.ID]; !exists || entry.Action != QueueActionResume {
		t.Errorf("QueueActionResume row was removed or modified!")
	}

	// No false PAUSED event published
	select {
	case ev := <-ch:
		if ev.Type == EventJobUpdated {
			t.Errorf("EventJobUpdated was published when DB persistence failed!")
		}
	default:
	}
}

func TestDispatch_PrepareWorkDirStartPersistenceFailure(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	// Make workDir path invalid to force PrepareWorkDir failure
	workDir := filepath.Join(dataDir, "invalid_path_fail\x00")

	j := &Job{
		ID:             "prepare-start-fail",
		Source:         "https://youtube.com/watch?v=000",
		Name:           "Prepare Fail",
		Status:         StatusQueued,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionStart}

	// Inject DB update error
	jobRepo.updateErr = errors.New("simulated DB update failure")

	err := mgr.DispatchQueuedJob(ctx, qj)
	if err == nil {
		t.Fatalf("expected PrepareWorkDir dispatch to fail, got nil")
	}

	// Queue row MUST remain when persistence fails
	if _, exists := queueRepo.entries[j.ID]; !exists {
		t.Errorf("queue row was deleted when DB persistence failed!")
	}
}

func TestDispatch_PrepareWorkDirResumePersistenceFailure(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "invalid_path_fail\x00")

	j := &Job{
		ID:             "prepare-resume-fail",
		Source:         "https://youtube.com/watch?v=000",
		Name:           "Prepare Resume Fail",
		Status:         StatusPaused,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionResume}
	queueRepo.entries[j.ID] = &QueueEntry{JobID: j.ID, Action: QueueActionResume}

	// Inject DB update error
	jobRepo.updateErr = errors.New("simulated DB update failure")

	err := mgr.DispatchQueuedJob(ctx, qj)
	if err == nil {
		t.Fatalf("expected PrepareWorkDir dispatch to fail, got nil")
	}

	// QueueActionResume row MUST remain
	if entry, exists := queueRepo.entries[j.ID]; !exists || entry.Action != QueueActionResume {
		t.Errorf("QueueActionResume row was removed or modified!")
	}
}

func TestMediaCompletion_PersistenceFailureRetriesTerminalState(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_completion_retry")
	storageSvc.PrepareWorkDir(ctx, "job-comp-retry", workDir)
	srcFile := filepath.Join(workDir, "video.mp4")
	os.WriteFile(srcFile, []byte("video content"), 0644)

	j := &Job{
		ID:             "job-comp-retry",
		Source:         "https://youtube.com/watch?v=comp1",
		Name:           "Video",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp-comp1",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	status := &EngineStatus{
		Status:     StatusCompleted,
		Progress:   100,
		OutputPath: srcFile,
	}

	// First attempt: DB persistence fails
	jobRepo.updateErr = errors.New("temporary DB write error")
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	select {
	case ev := <-ch:
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted during persistence failure!")
		}
	default:
	}

	if _, statErr := os.Stat(workDir); os.IsNotExist(statErr) {
		t.Errorf("WorkDir deleted during persistence failure!")
	}

	// Second attempt: DB persistence succeeds
	jobRepo.updateErr = nil
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	var ev Event
	select {
	case ev = <-ch:
	default:
	}

	if ev.Type != EventJobCompleted {
		t.Errorf("expected EventJobCompleted on retried persistence success, got %s", ev.Type)
	}

	// WorkDir should be cleaned up after successful completion persistence
	if _, statErr := os.Stat(workDir); !os.IsNotExist(statErr) {
		t.Errorf("WorkDir survived after successful completion persistence!")
	}
}

func TestMediaFailure_PersistenceFailureRetriesTerminalState(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_fail_retry")
	storageSvc.PrepareWorkDir(ctx, "job-fail-retry", workDir)

	j := &Job{
		ID:             "job-fail-retry",
		Source:         "https://youtube.com/watch?v=fail1",
		Name:           "Video Fail",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	status := &EngineStatus{Status: StatusFailed, Error: "download error"}

	// First attempt: DB update fails
	jobRepo.updateErr = errors.New("temp error")
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	select {
	case ev := <-ch:
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed emitted during persistence failure!")
		}
	default:
	}

	// Second attempt: DB update succeeds
	jobRepo.updateErr = nil
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	var ev Event
	select {
	case ev = <-ch:
	default:
	}

	if ev.Type != EventJobFailed {
		t.Errorf("expected EventJobFailed on retried persistence success, got %s", ev.Type)
	}
}

func TestMediaCancelled_PersistenceFailureRetriesTerminalState(t *testing.T) {
	mgr, jobRepo, _, storageSvc, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(t.TempDir(), "workdir_cancel_retry")
	storageSvc.PrepareWorkDir(ctx, "job-cancel-retry", workDir)

	j := &Job{
		ID:             "job-cancel-retry",
		Source:         "https://youtube.com/watch?v=canc1",
		Name:           "Video Cancel",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	status := &EngineStatus{Status: StatusCancelled}

	// First attempt: DB update fails
	jobRepo.updateErr = errors.New("temp error")
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	select {
	case ev := <-ch:
		if ev.Type == EventJobCancelled {
			t.Errorf("EventJobCancelled emitted during persistence failure!")
		}
	default:
	}

	// Second attempt: DB update succeeds
	jobRepo.updateErr = nil
	mgr.UpdateJobFromEngine(ctx, j, status, true)

	var ev Event
	select {
	case ev = <-ch:
	default:
	}

	if ev.Type != EventJobCancelled {
		t.Errorf("expected EventJobCancelled on retried persistence success, got %s", ev.Type)
	}
}

func TestMonitor_StatusFailurePersistenceFailureDoesNotPublish(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job-mon-fail-pers",
		Source:         "https://youtube.com/watch?v=mon1",
		Name:           "Monitor Fail",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	mon := NewMonitor(mgr, 1*time.Second)
	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence error
	jobRepo.updateErr = errors.New("simulated DB update error")

	// Max out consecutive failures
	for i := 0; i < maxConsecutiveFailures; i++ {
		mon.recordFailure(ctx, j, "Engine lost connection")
	}

	// EventJobFailed MUST NOT be published when DB persistence fails
	select {
	case ev := <-ch:
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed emitted when DB update failed in Monitor!")
		}
	default:
	}
}

type fakeCleanupEngine struct {
	fakeEngine
	cleaned   map[string]bool
	statusMap map[string]*EngineStatus
}

func (f *fakeCleanupEngine) Cleanup(jobID string) {
	if f.cleaned == nil {
		f.cleaned = make(map[string]bool)
	}
	f.cleaned[jobID] = true
	if f.statusMap != nil {
		delete(f.statusMap, jobID)
	}
}

func (f *fakeCleanupEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	if f.statusMap != nil {
		if st, ok := f.statusMap[j.ID]; ok {
			return st, nil
		}
	}
	return &EngineStatus{Status: StatusQueued}, nil
}

func TestScheduler_Dispatch_StartPreflightSuccess_HandledOnce(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	// Job A will fail preflight (huge total bytes > disk)
	jA := &Job{
		ID:             "job-sched-pf-start-a",
		Source:         "https://example.com/fileA.zip",
		Name:           "Job A",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024 * 1024, // 10 PB (exceeds disk)
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, jA)

	// Job B is normal
	jB := &Job{
		ID:             "job-sched-pf-start-b",
		Source:         "https://example.com/fileB.zip",
		Name:           "Job B",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     1024,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, jB)

	queueRepo.Enqueue(ctx, &QueueEntry{JobID: jA.ID, Action: QueueActionStart})
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: jB.ID, Action: QueueActionStart})

	startCalled := false
	eng := &fakeEngine{
		startFunc: func(ctx context.Context, j *Job, dir string) (string, error) {
			startCalled = true
			return "gid_b", nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = eng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	// Dispatch Job A manually via Scheduler
	qjA := &QueuedJob{JobID: jA.ID, Action: QueueActionStart}
	sched.reserveInFlight(jA.ID, QueueActionStart)
	sched.dispatchSingle(qjA)

	// Assert Job A: FAILED, queue entry removed, EventJobFailed emitted
	updatedA, _ := jobRepo.GetByID(ctx, jA.ID)
	if updatedA.Status != StatusFailed {
		t.Errorf("expected Job A status FAILED, got %s", updatedA.Status)
	}

	qA, _ := queueRepo.Get(ctx, jA.ID)
	if qA != nil {
		t.Errorf("expected queue entry for Job A to be deleted")
	}

	// Count failed events for A
	failEventCount := 0
	for len(ch) > 0 {
		ev := <-ch
		if ev.Job.ID == jA.ID && ev.Type == EventJobFailed {
			failEventCount++
		}
	}
	if failEventCount != 1 {
		t.Errorf("expected EventJobFailed exactly once for Job A, got %d", failEventCount)
	}

	// Dispatch Job B after A's failure reservation was released
	qjB := &QueuedJob{JobID: jB.ID, Action: QueueActionStart}
	sched.reserveInFlight(jB.ID, QueueActionStart)
	sched.dispatchSingle(qjB)

	// Job B should have been dispatched cleanly
	updatedB, _ := jobRepo.GetByID(ctx, jB.ID)
	if updatedB.Status != StatusDownloading {
		t.Errorf("expected Job B to dispatch and reach DOWNLOADING, got %s", updatedB.Status)
	}

	if !startCalled {
		t.Errorf("expected eng.Start to be called for Job B")
	}
}

func TestScheduler_Dispatch_ResumePreflightSuccess_HandledOnce(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job-sched-pf-resume",
		Source:         "https://example.com/fileResume.zip",
		Name:           "Job Resume",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024 * 1024, // 10 PB
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionResume})

	resumeCalled := false
	eng := &fakeEngine{
		resumeFunc: func(ctx context.Context, j *Job) error {
			resumeCalled = true
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = eng

	bus := mgr.bus.(*fakeEventBus)

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionResume}
	sched.reserveInFlight(j.ID, QueueActionResume)
	sched.dispatchSingle(qj)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusPaused {
		t.Errorf("expected Job status PAUSED on resume preflight failure, got %s", updated.Status)
	}

	// QueueActionResume row MUST be retained
	qItem, _ := queueRepo.Get(ctx, j.ID)
	if qItem == nil || qItem.Action != QueueActionResume {
		t.Errorf("expected QueueActionResume entry to be retained")
	}

	if resumeCalled {
		t.Errorf("eng.Resume MUST NOT be called when preflight fails")
	}
}

func TestScheduler_Dispatch_StartPreflightPersistenceFailure_StatePersistence(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	jA := &Job{
		ID:             "job-pf-pers-fail-a",
		Source:         "https://example.com/filePF.zip",
		Name:           "Job A PF Fail",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024 * 1024, // 10 PB
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, jA)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: jA.ID, Action: QueueActionStart})

	eng := &fakeEngine{}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = eng

	bus := mgr.bus.(*fakeEventBus)

	// Inject DB persistence error
	jobRepo.updateErr = errors.New("simulated DB update error")

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	qj := &QueuedJob{JobID: jA.ID, Action: QueueActionStart}
	sched.reserveInFlight(jA.ID, QueueActionStart)
	sched.dispatchSingle(qj)

	// Reservation must be StatePersistence (NOT ExternalExecution)
	sched.mu.Lock()
	res, ok := sched.inFlight[jA.ID]
	if !ok {
		t.Fatalf("expected dirty reservation for %s", jA.ID)
	}
	if res.Kind != ReconciliationStatePersistence {
		t.Errorf("expected ReconciliationStatePersistence, got %v", res.Kind)
	}
	sched.mu.Unlock()

	// Recover DB
	jobRepo.updateErr = nil

	// Reconcile
	sched.reconcileJob(ctx, jA.ID)

	// After DB recovery, FAILED state is persisted and reservation released
	updatedA, _ := jobRepo.GetByID(ctx, jA.ID)
	if updatedA.Status != StatusFailed {
		t.Errorf("expected status FAILED after reconciliation, got %s", updatedA.Status)
	}

	qA, _ := queueRepo.Get(ctx, jA.ID)
	if qA != nil {
		t.Errorf("expected queue entry to be deleted after successful state reconciliation")
	}
}

func TestScheduler_Dispatch_ResumePreflightPersistenceFailure_StatePersistence(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job-pf-resume-pers-fail",
		Source:         "https://example.com/fileResumePF.zip",
		Name:           "Job Resume PF Fail",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     10 * 1024 * 1024 * 1024 * 1024 * 1024,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionResume})

	eng := &fakeEngine{}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = eng

	bus := mgr.bus.(*fakeEventBus)

	jobRepo.updateErr = errors.New("simulated DB update error")

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionResume}
	sched.reserveInFlight(j.ID, QueueActionResume)
	sched.dispatchSingle(qj)

	sched.mu.Lock()
	res, ok := sched.inFlight[j.ID]
	if !ok || res.Kind != ReconciliationStatePersistence {
		t.Fatalf("expected ReconciliationStatePersistence reservation, got %v", res.Kind)
	}
	sched.mu.Unlock()

	jobRepo.updateErr = nil
	sched.reconcileJob(ctx, j.ID)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusPaused {
		t.Errorf("expected status PAUSED after reconciliation, got %s", updated.Status)
	}

	qItem, _ := queueRepo.Get(ctx, j.ID)
	if qItem == nil || qItem.Action != QueueActionResume {
		t.Errorf("expected QueueActionResume entry to remain")
	}
}

func TestMediaCompletion_SuccessCleansTerminalEngineState(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job-comp-cleanup",
		Source:         "https://youtube.com/watch?v=clean1",
		Name:           "Test Cleanup",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Update job from engine -> COMPLETED
	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	// Assert event emitted
	select {
	case ev := <-ch:
		if ev.Type != EventJobCompleted {
			t.Errorf("expected EventJobCompleted, got %s", ev.Type)
		}
	default:
		t.Errorf("expected EventJobCompleted event")
	}

	// Assert cleanup called
	if !cleanEng.cleaned[j.ID] {
		t.Errorf("expected ICleanupableEngine.Cleanup to be called for job %s", j.ID)
	}
}

func TestMediaCompletion_PersistenceFailurePreservesTerminalEngineState(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job-comp-pers-fail",
		Source:         "https://youtube.com/watch?v=clean2",
		Name:           "Test Cleanup Pers Fail",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence failure
	jobRepo.updateErr = errors.New("simulated DB error")

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	// Event MUST NOT be emitted
	select {
	case ev := <-ch:
		t.Errorf("unexpected event %s emitted on persistence failure", ev.Type)
	default:
	}

	// Engine MUST NOT be cleaned up
	if cleanEng.cleaned[j.ID] {
		t.Errorf("ICleanupableEngine.Cleanup MUST NOT be called when DB persistence fails")
	}

	// Recover DB
	jobRepo.updateErr = nil

	// Second attempt succeeds
	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("ICleanupableEngine.Cleanup expected after DB recovery and persistence success")
	}
}

func TestScheduler_ReconciliationCOMPLETED_CleansTerminalEngineState(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "reconcile_completed_cleanup",
		Source:         "https://example.com/fileDone.zip",
		Name:           "Reconcile Done Cleanup",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_gid_done",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionStart})

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	// Simulate dirty external reservation
	sched.markReservationDirtyExternal(j.ID, QueueActionStart, j.EngineID)

	// Reconcile
	sched.reconcileJob(ctx, j.ID)

	// Assert status COMPLETED and engine cleaned
	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after reconciliation, got %s", updated.Status)
	}

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("expected ICleanupableEngine.Cleanup to be called during external reconciliation of COMPLETED status")
	}
}
