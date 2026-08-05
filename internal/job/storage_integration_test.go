package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if updated.Error != "media completed but engine output path was not provided" {
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

func TestScheduler_ReconciliationCompleted_RestoresMonitorOwnership(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "reconcile_completed_restore",
		Source:         "https://example.com/fileDone.zip",
		Name:           "Reconcile Restore",
		Status:         StatusQueued,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_gid_restore",
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
	ch := bus.Subscribe()

	activeAdded := false
	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	sched.SetAddActiveFunc(func(activeJ *Job) {
		activeAdded = true
	})
	mgr.scheduler = sched

	// Simulate dirty external reservation
	sched.markReservationDirtyExternal(j.ID, QueueActionStart, j.EngineID)

	// Reconcile
	sched.reconcileJob(ctx, j.ID)

	// Assert job status restored to DOWNLOADING
	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusDownloading {
		t.Errorf("expected StatusDownloading after reconciliation, got %s", updated.Status)
	}

	// Queue row deleted
	qItem, _ := queueRepo.Get(ctx, j.ID)
	if qItem != nil {
		t.Errorf("expected queue entry to be deleted after reconciliation")
	}

	// Active tracking added
	if !activeAdded {
		t.Errorf("expected job to be added to active tracking")
	}

	// Reservation released
	sched.mu.Lock()
	_, inFlight := sched.inFlight[j.ID]
	sched.mu.Unlock()
	if inFlight {
		t.Errorf("expected in-flight reservation to be released")
	}

	// EventJobCompleted NOT emitted by Scheduler
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted MUST NOT be emitted by Scheduler reconciliation")
		}
	}

	// Engine Cleanup NOT called
	if cleanEng.cleaned[j.ID] {
		t.Errorf("ICleanupableEngine.Cleanup MUST NOT be called by Scheduler reconciliation")
	}
}

func TestScheduler_ReconciliationCompleted_MediaFinalizesThroughManager(t *testing.T) {
	mgr, jobRepo, queueRepo, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "temp_reconcile_media")
	os.MkdirAll(workDir, 0755)
	storageSrv.PrepareWorkDir(ctx, "job_rec_media", workDir)

	// Put artifact in WorkDir
	artifactPath := filepath.Join(workDir, "video.mp4")
	os.WriteFile(artifactPath, []byte("media video content"), 0644)

	j := &Job{
		ID:             "job_rec_media",
		Source:         "https://youtube.com/watch?v=rec_media",
		Name:           "Reconcile Media Test",
		Status:         StatusQueued,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_rec_gid",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionStart})

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100, OutputPath: artifactPath},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(bus)
	mgr.scheduler = sched

	// Step 1: Scheduler reconciliation
	sched.markReservationDirtyExternal(j.ID, QueueActionStart, j.EngineID)
	sched.reconcileJob(ctx, j.ID)

	// Assert Step 1: Restored to DOWNLOADING, engine state present, FinalPath empty, no event
	updated1, _ := jobRepo.GetByID(ctx, j.ID)
	if updated1.Status != StatusDownloading {
		t.Errorf("Step 1: expected status DOWNLOADING, got %s", updated1.Status)
	}
	if updated1.FinalPath != "" {
		t.Errorf("Step 1: FinalPath must remain empty prior to Manager finalization, got %s", updated1.FinalPath)
	}
	if cleanEng.cleaned[j.ID] {
		t.Errorf("Step 1: Engine cleanup MUST NOT be called during Scheduler reconciliation")
	}

	compEventCount := 0
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			compEventCount++
		}
	}
	if compEventCount != 0 {
		t.Errorf("Step 1: EventJobCompleted count must be 0, got %d", compEventCount)
	}

	// Step 2: Monitor / Manager terminal handling
	mgr.UpdateJobFromEngine(ctx, updated1, &EngineStatus{
		Status:     StatusCompleted,
		Progress:   100,
		OutputPath: artifactPath,
	}, true)

	// Assert Step 2: Finalized in DestinationDir, Status = COMPLETED, FinalPath set, event published once, engine cleaned
	updated2, _ := jobRepo.GetByID(ctx, j.ID)
	if updated2.Status != StatusCompleted {
		t.Errorf("Step 2: expected StatusCompleted after Manager finalization, got %s", updated2.Status)
	}
	if updated2.FinalPath == "" || !strings.HasPrefix(updated2.FinalPath, downloadDir) {
		t.Errorf("Step 2: expected FinalPath in DestinationDir, got %s", updated2.FinalPath)
	}

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("Step 2: Engine cleanup expected after Manager finalization")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted && ev.Job.ID == j.ID {
			compEventCount++
		}
	}
	if compEventCount != 1 {
		t.Errorf("Step 2: EventJobCompleted count must be exactly 1, got %d", compEventCount)
	}
}

func TestMediaCompletion_MissingArtifact_CleansEngineAfterFailedStatePersisted(t *testing.T) {
	mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "temp_media_missing")
	os.MkdirAll(workDir, 0755)
	storageSrv.PrepareWorkDir(ctx, "job_missing_art", workDir)

	j := &Job{
		ID:             "job_missing_art",
		Source:         "https://youtube.com/watch?v=missing_art",
		Name:           "Missing Artifact Test",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_missing_gid",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
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

	// Update with completed status but no output file in workDir
	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusFailed {
		t.Errorf("expected status FAILED when artifact missing, got %s", updated.Status)
	}

	failEventEmitted := false
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobFailed && ev.Job.ID == j.ID {
			failEventEmitted = true
		}
	}
	if !failEventEmitted {
		t.Errorf("expected EventJobFailed to be emitted when artifact missing")
	}

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("expected engine cleanup to be called when missing artifact FAILED status persistence succeeds")
	}
}

func TestMediaCompletion_MissingArtifact_PersistenceFailurePreservesEngineState(t *testing.T) {
	mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "temp_media_missing_pers_fail")
	os.MkdirAll(workDir, 0755)
	storageSrv.PrepareWorkDir(ctx, "job_missing_pers_fail", workDir)

	j := &Job{
		ID:             "job_missing_pers_fail",
		Source:         "https://youtube.com/watch?v=missing_pers_fail",
		Name:           "Missing Artifact Persistence Failure",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_missing_fail_gid",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
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
	jobRepo.updateErr = errors.New("simulated DB update error")

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed MUST NOT be emitted on DB update failure")
		}
	}

	if cleanEng.cleaned[j.ID] {
		t.Errorf("engine Cleanup MUST NOT be called when FAILED state persistence fails")
	}
}

func TestMediaCompletion_FinalizeError_CleansEngineAfterFailedPersistence(t *testing.T) {
	mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "temp_media_finalize_fail")
	os.MkdirAll(workDir, 0755)
	storageSrv.PrepareWorkDir(ctx, "job_finalize_fail", workDir)

	// Make destination directory unwritable/invalid so FinalizeFile fails
	invalidDest := filepath.Join(downloadDir, "non_existent_file_as_dir")
	os.WriteFile(invalidDest, []byte("file blocker"), 0644)

	artifactPath := filepath.Join(workDir, "video.mp4")
	os.WriteFile(artifactPath, []byte("media content"), 0644)

	j := &Job{
		ID:             "job_finalize_fail",
		Source:         "https://youtube.com/watch?v=finalize_fail",
		Name:           "Finalize Fail Test",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_finalize_fail_gid",
		Type:           TypeMedia,
		DestinationDir: invalidDest, // Will fail FinalizeFile
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100, OutputPath: artifactPath},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100, OutputPath: artifactPath}, true)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusFailed {
		t.Errorf("expected status FAILED when FinalizeFile fails, got %s", updated.Status)
	}

	failEventEmitted := false
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobFailed && ev.Job.ID == j.ID {
			failEventEmitted = true
		}
	}
	if !failEventEmitted {
		t.Errorf("expected EventJobFailed to be emitted on finalization failure")
	}

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("expected engine cleanup to be called when FinalizeFile failure persists FAILED status")
	}
}

func TestDispatch_ExternalExecutionPersistenceKind(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job_ext_kind_test",
		Source:         "https://example.com/fileExtKind.zip",
		Name:           "External Persistence Kind Test",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: j.ID, Action: QueueActionStart})

	eng := &fakeEngine{
		startFunc: func(ctx context.Context, j *Job, dir string) (string, error) {
			return "gid_ext_kind_123", nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = eng

	// Inject DB persistence failure after engine Start succeeds
	jobRepo.updateErr = errors.New("simulated DB update failure post-start")

	err := mgr.dispatchQueuedJob(ctx, qj)
	if err == nil {
		t.Fatalf("expected error from dispatchQueuedJob when DB update fails")
	}

	var pErr *DispatchPersistenceError
	if !errors.As(err, &pErr) {
		t.Fatalf("expected *DispatchPersistenceError, got %T: %v", err, err)
	}

	if pErr.Kind != DispatchFailureExternalExecutionPersistence {
		t.Errorf("expected Kind == DispatchFailureExternalExecutionPersistence, got %s", pErr.Kind)
	}

	if pErr.EngineID != "gid_ext_kind_123" {
		t.Errorf("expected EngineID == gid_ext_kind_123, got %s", pErr.EngineID)
	}
}

func TestMediaCompletion_FinalizationSuccessPersistenceFailureRetriesWithoutRefinalizing(t *testing.T) {
	mgr, jobRepo, _, storageSrv, downloadDir, dataDir := setupStorageTestEnv(t)
	ctx := context.Background()

	workDir := filepath.Join(dataDir, "temp_media_retry")
	os.MkdirAll(workDir, 0755)
	storageSrv.PrepareWorkDir(ctx, "job_media_retry", workDir)

	artifactPath := filepath.Join(workDir, "video.mp4")
	os.WriteFile(artifactPath, []byte("media retry content"), 0644)

	j := &Job{
		ID:             "job_media_retry",
		Source:         "https://youtube.com/watch?v=media_retry",
		Name:           "Media Retry Test",
		Status:         StatusDownloading,
		Engine:         "ytdlp",
		EngineID:       "ytdlp_retry_gid",
		Type:           TypeMedia,
		DestinationDir: downloadDir,
		WorkDir:        workDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	mgr.addActive(j)

	cleanEng := &fakeCleanupEngine{
		statusMap: map[string]*EngineStatus{
			j.ID: {Status: StatusCompleted, Progress: 100, OutputPath: artifactPath},
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["ytdlp"] = cleanEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence failure on Attempt 1
	jobRepo.updateErr = errors.New("simulated DB update error attempt 1")

	// Attempt 1: Fetch copy #1 from ActiveJobs
	activeCopies1 := mgr.GetActiveJobs()
	copy1 := activeCopies1[j.ID]
	if copy1 == nil {
		t.Fatalf("expected job in activeJobs")
	}

	mgr.UpdateJobFromEngine(ctx, copy1, &EngineStatus{Status: StatusCompleted, Progress: 100, OutputPath: artifactPath}, true)

	// Assert Attempt 1
	destArtifact := filepath.Join(downloadDir, "video.mp4")
	if _, err := os.Stat(destArtifact); err != nil {
		t.Fatalf("expected destination file to exist after attempt 1, got error: %v", err)
	}
	if _, err := os.Stat(artifactPath); err == nil {
		t.Errorf("expected WorkDir source file to no longer exist after attempt 1")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted MUST NOT be emitted on attempt 1 DB failure")
		}
	}

	if cleanEng.cleaned[j.ID] {
		t.Errorf("engine Cleanup MUST NOT be called on attempt 1 DB failure")
	}

	// Verify Manager activeJob retained FinalPath (using copy #2 from GetActiveJobs)
	activeCopies2 := mgr.GetActiveJobs()
	copy2 := activeCopies2[j.ID]
	if copy2 == nil || copy2.FinalPath == "" {
		t.Fatalf("expected Manager activeJob to retain FinalPath after attempt 1")
	}

	// Recover DB for Attempt 2
	jobRepo.updateErr = nil

	// Attempt 2: Update using copy #2 from GetActiveJobs
	mgr.UpdateJobFromEngine(ctx, copy2, &EngineStatus{Status: StatusCompleted, Progress: 100, OutputPath: artifactPath}, true)

	// Assert Attempt 2
	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after attempt 2, got %s", updated.Status)
	}

	if updated.FinalPath != destArtifact {
		t.Errorf("expected FinalPath %s, got %s", destArtifact, updated.FinalPath)
	}

	compEventCount := 0
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted && ev.Job.ID == j.ID {
			compEventCount++
		}
	}
	if compEventCount != 1 {
		t.Errorf("expected EventJobCompleted exactly once, got %d", compEventCount)
	}

	if !cleanEng.cleaned[j.ID] {
		t.Errorf("expected engine Cleanup to be called after attempt 2 success")
	}
}

func TestTorrentSelectedSize_RemainsSelectedSizeAfterStatusUpdate(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	const selectedSize = int64(5 * 1024 * 1024 * 1024)

	j := &Job{
		ID:             "job_qb_size_test",
		Source:         "magnet:?xt=urn:btih:1111222233334444555566667777888899990000",
		Name:           "QB Size Test",
		Status:         StatusDownloading,
		Engine:         "qbittorrent",
		EngineID:       "1111222233334444555566667777888899990000",
		Type:           TypeTorrent,
		TotalBytes:     selectedSize,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	mgr.addActive(j)

	qbEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{
				Status:     StatusDownloading,
				TotalBytes: selectedSize,
				Progress:   10.0,
			}, nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = qbEng

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{
		Status:     StatusDownloading,
		TotalBytes: selectedSize,
		Progress:   10.0,
	}, true)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.TotalBytes != selectedSize {
		t.Errorf("expected Job.TotalBytes to remain selected size %d, got %d", selectedSize, updated.TotalBytes)
	}
}

func TestStartTorrent_RejectsPartialFileSelection(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job_partial_sel",
		Source:         "magnet:?xt=urn:btih:aaaaabbbbbcccccdddddeeeeefffffffffff",
		Name:           "Partial Selection Test",
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		EngineID:       "aaaaabbbbbcccccdddddeeeeefffffffffff",
		Type:           TypeTorrent,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	prioSetCount := 0
	torrentEng := &fakeTorrentEngine{
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "file1.mp4", Size: 100, Priority: PriorityNormal},
				{Index: 1, Path: "file2.mp4", Size: 200, Priority: PriorityNormal},
				{Index: 2, Path: "file3.mp4", Size: 300, Priority: PriorityNormal},
			}, nil
		},
		setPrioritiesFunc: func(hash string) error {
			prioSetCount++
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	// Send partial selection (only 2 out of 3 files)
	partialSelections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PrioritySkip},
	}

	_, err := mgr.StartTorrent(ctx, j.ID, partialSelections, false)
	if err == nil {
		t.Fatalf("expected error for partial file selection")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrInvalidRequest {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}

	if prioSetCount != 0 {
		t.Errorf("SetFilePriorities MUST NOT be called when selection validation fails")
	}

	qItem, _ := queueRepo.Get(ctx, j.ID)
	if qItem != nil {
		t.Errorf("queue entry MUST NOT be created on selection failure")
	}

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusAwaitingSelection {
		t.Errorf("job status MUST remain StatusAwaitingSelection, got %s", updated.Status)
	}
}

func TestStartTorrent_AcceptsCompleteSelectionSet(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job_full_sel",
		Source:         "magnet:?xt=urn:btih:11111222223333344444555556666677",
		Name:           "Complete Selection Test",
		Status:         StatusAwaitingSelection,
		Engine:         "qbittorrent",
		EngineID:       "11111222223333344444555556666677",
		Type:           TypeTorrent,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	prioSetCount := 0
	torrentEng := &fakeTorrentEngine{
		getFilesFunc: func(hash string) ([]TorrentFile, error) {
			return []TorrentFile{
				{Index: 0, Path: "file1.mp4", Size: 100, Priority: PriorityNormal},
				{Index: 1, Path: "file2.mp4", Size: 200, Priority: PriorityNormal},
				{Index: 2, Path: "file3.mp4", Size: 300, Priority: PriorityNormal},
			}, nil
		},
		setPrioritiesFunc: func(hash string) error {
			prioSetCount++
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	completeSelections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal}, // 100
		{Index: 1, Priority: PrioritySkip},   // skipped
		{Index: 2, Priority: PriorityHigh},   // 300
	}

	updatedJ, err := mgr.StartTorrent(ctx, j.ID, completeSelections, false)
	if err != nil {
		t.Fatalf("expected StartTorrent to succeed, got %v", err)
	}

	if prioSetCount != 1 {
		t.Errorf("expected SetFilePriorities to be called once, got %d", prioSetCount)
	}

	if updatedJ.TotalBytes != 400 {
		t.Errorf("expected selected TotalBytes == 400 (100+300), got %d", updatedJ.TotalBytes)
	}

	if updatedJ.Status != StatusQueued && updatedJ.Status != StatusDownloading {
		t.Errorf("expected job status StatusQueued or StatusDownloading, got %s", updatedJ.Status)
	}
}

func TestTorrent_SeedingWithoutSeedAfterComplete_SetsFinalPath(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_seed_false",
		Source:            "magnet:?xt=urn:btih:seedfalse111122223333444455556666",
		Name:              "Seed False Test",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "seedfalse111122223333444455556666",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected status StatusCompleted, got %s", updated.Status)
	}
	if updated.FinalPath != downloadDir {
		t.Errorf("expected FinalPath == %s, got %s", downloadDir, updated.FinalPath)
	}
}

func TestStopSeeding_SetsFinalPath(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_stop_seeding",
		Source:            "magnet:?xt=urn:btih:stopseeding11112222333344445555",
		Name:              "Stop Seeding Test",
		Status:            StatusSeeding,
		Engine:            "qbittorrent",
		EngineID:          "stopseeding11112222333344445555",
		Type:              TypeTorrent,
		SeedAfterComplete: true,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		stopDownloadFunc:  func(hash string) error { return nil },
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	updatedJ, err := mgr.StopSeeding(ctx, j.ID)
	if err != nil {
		t.Fatalf("expected StopSeeding to succeed, got %v", err)
	}

	if updatedJ.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", updatedJ.Status)
	}

	if updatedJ.FinalPath != downloadDir {
		t.Errorf("expected FinalPath == %s, got %s", downloadDir, updatedJ.FinalPath)
	}
}

func TestRecovery_TorrentCompleted_SetsFinalPath(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_rec_torrent_done",
		Source:            "magnet:?xt=urn:btih:rectorrentdone1111222233334444",
		Name:              "Recovery Torrent Done",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "rectorrentdone1111222233334444",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{Status: StatusCompleted, Progress: 100}, nil
		},
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	mgr.recover(ctx)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after recovery, got %s", updated.Status)
	}

	if updated.FinalPath != downloadDir {
		t.Errorf("expected FinalPath == %s, got %s", downloadDir, updated.FinalPath)
	}
}

func TestRecovery_DirectCompleted_SetsFinalPath(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job_rec_direct_done",
		Source:         "https://example.com/ubuntu.iso",
		Name:           "Recovery Direct Done",
		Status:         StatusDownloading,
		Engine:         "aria2",
		EngineID:       "aria2_gid_direct_done",
		Type:           TypeDownload,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)

	ariaEng := &fakeEngine{
		statusFunc: func(ctx context.Context, j *Job) (*EngineStatus, error) {
			return &EngineStatus{Status: StatusCompleted, Progress: 100, FileName: "ubuntu.iso"}, nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = ariaEng

	mgr.recover(ctx)

	updated, _ := jobRepo.GetByID(ctx, j.ID)
	if updated.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted after recovery, got %s", updated.Status)
	}

	expectedFinal := filepath.Join(downloadDir, "ubuntu.iso")
	if updated.FinalPath != expectedFinal {
		t.Errorf("expected FinalPath %s, got %s", expectedFinal, updated.FinalPath)
	}
}

func TestStopSeeding_PersistenceFailureDoesNotPublishCompleted(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_stop_seeding_pers_fail",
		Source:            "magnet:?xt=urn:btih:stopseedingfail111122223333",
		Name:              "Stop Seeding Pers Fail",
		Status:            StatusSeeding,
		Engine:            "qbittorrent",
		EngineID:          "stopseedingfail111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: true,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		stopDownloadFunc:  func(hash string) error { return nil },
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence failure
	jobRepo.updateErr = errors.New("simulated DB error on StopSeeding")

	_, err := mgr.StopSeeding(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected error from StopSeeding when DB update fails")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted MUST NOT be emitted on StopSeeding DB failure")
		}
	}
}

func TestTorrentSeedingCompletion_PersistenceFailureDoesNotPublishCompleted(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_torrent_seeding_pers_fail",
		Source:            "magnet:?xt=urn:btih:torrentseedingfail11112222",
		Name:              "Torrent Seeding Pers Fail",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "torrentseedingfail11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence failure
	jobRepo.updateErr = errors.New("simulated DB error on Seeding completion")

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted MUST NOT be emitted on Seeding completion DB failure")
		}
	}
}

func TestTorrentCompletion_PersistenceFailureDoesNotRemoveTorrent(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_torrent_pers_fail_no_remove",
		Source:            "magnet:?xt=urn:btih:persfailnoremove11112222",
		Name:              "Persist Fail No Remove",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "persfailnoremove11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	var removeCalls int
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalls++
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Inject DB persistence failure
	jobRepo.updateErr = errors.New("simulated DB update error")

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	if removeCalls != 0 {
		t.Errorf("expected RemoveTorrent to NOT be called when DB persistence fails, got %d calls", removeCalls)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
			t.Errorf("no terminal events should be emitted when persistence fails, got %s", ev.Type)
		}
	}
}

func TestTorrentCompletion_PersistsBeforeRemoveTorrent(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_torrent_order_test",
		Source:            "magnet:?xt=urn:btih:order1111222233334444",
		Name:              "Order Test",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "order1111222233334444",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	var checkedStatusInDB JobStatus
	var checkedPendingInDB bool
	var checkedFinalPathInDB string

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			inDB, err := jobRepo.GetByID(ctx, j.ID)
			if err == nil && inDB != nil {
				checkedStatusInDB = inDB.Status
				checkedPendingInDB = inDB.EngineCleanupPending
				checkedFinalPathInDB = inDB.FinalPath
			}
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	if checkedStatusInDB != StatusCompleted {
		t.Errorf("expected DB status COMPLETED inside RemoveTorrent call, got %s", checkedStatusInDB)
	}
	if !checkedPendingInDB {
		t.Error("expected DB EngineCleanupPending == true inside RemoveTorrent call")
	}
	if checkedFinalPathInDB != downloadDir {
		t.Errorf("expected DB FinalPath == %s inside RemoveTorrent call, got %s", downloadDir, checkedFinalPathInDB)
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after RemoveTorrent success")
	}

	var completedEventCount int
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			completedEventCount++
		}
	}
	if completedEventCount != 1 {
		t.Errorf("expected EventJobCompleted exactly once, got %d", completedEventCount)
	}
}

func TestTorrentCompletion_RemoveFailureLeavesCleanupPending(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_torrent_remove_fail",
		Source:            "magnet:?xt=urn:btih:removefail111122223333",
		Name:              "Remove Fail",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "removefail111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return errors.New("daemon 500 internal server error")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted when RemoveTorrent fails, got %s", after.Status)
	}
	if !after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == true when RemoveTorrent fails")
	}
	if after.FinalPath != downloadDir {
		t.Errorf("expected FinalPath == %s, got %s", downloadDir, after.FinalPath)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
			t.Errorf("no terminal event should be published when RemoveTorrent fails, got %s", ev.Type)
		}
	}
}

func TestTorrentCleanupPending_RetrySucceeds(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                   "job_retry_pending",
		Source:               "magnet:?xt=urn:btih:retrypending11112222",
		Name:                 "Retry Pending",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "retrypending11112222",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	jobRepo.Create(ctx, j)

	var removeCalls int
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalls++
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.processPendingEngineCleanups(ctx)

	if removeCalls != 1 {
		t.Errorf("expected RemoveTorrent to be called once, got %d", removeCalls)
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after successful retry")
	}
	if after.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", after.Status)
	}

	// CRITICAL: Cleanup retry MUST NOT publish EventJobCompleted!
	var completedEvents int
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			completedEvents++
		}
	}
	if completedEvents != 0 {
		t.Errorf("expected EventJobCompleted count == 0 during cleanup retry, got %d", completedEvents)
	}
}

func TestRecovery_CompletedTorrentCleanupPending_RetriesRemoval(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                   "job_recovery_pending",
		Source:               "magnet:?xt=urn:btih:recpending111122223333",
		Name:                 "Recovery Pending",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "recpending111122223333",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	jobRepo.Create(ctx, j)

	var removeCalled bool
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	// Simulate startup background task
	mgr.processPendingEngineCleanups(ctx)

	if !removeCalled {
		t.Error("expected processPendingEngineCleanups to call RemoveTorrent for EngineCleanupPending job")
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after startup cleanup retry")
	}

	// Startup cleanup retry MUST NOT emit historical completion events
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("expected 0 EventJobCompleted during startup cleanup retry, got event %s", ev.Type)
		}
	}
}

func TestStopSeeding_PersistenceFailureDoesNotRemoveTorrent(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_stopseeding_pers_fail",
		Source:            "magnet:?xt=urn:btih:stopseedpersfail11112222",
		Name:              "Stop Seeding Pers Fail",
		Status:            StatusSeeding,
		Engine:            "qbittorrent",
		EngineID:          "stopseedpersfail11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: true,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	var stopDownloadCalled bool
	var removeCalled bool
	torrentEng := &fakeTorrentEngine{
		stopDownloadFunc: func(hash string) error {
			stopDownloadCalled = true
			return nil
		},
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	jobRepo.updateErr = errors.New("simulated DB update failure")

	returnedJob, err := mgr.StopSeeding(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected StopSeeding to fail when DB update fails")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrInternalError {
		t.Errorf("expected AppError with code INTERNAL_ERROR, got %v", err)
	}

	if returnedJob != nil && returnedJob.Status == StatusCompleted {
		t.Errorf("returned Job must NOT claim StatusCompleted when persistence fails, got %v", returnedJob)
	}

	if !stopDownloadCalled {
		t.Error("expected StopDownload to be called")
	}

	if removeCalled {
		t.Error("RemoveTorrent MUST NOT be called when DB persistence fails in StopSeeding")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted MUST NOT be emitted on persistence failure")
		}
	}

	inDB, _ := jobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusSeeding {
		t.Errorf("expected DB status to remain StatusSeeding, got %s", inDB.Status)
	}
	if inDB.EngineCleanupPending {
		t.Error("expected DB EngineCleanupPending == false on persistence failure")
	}
}

func TestStopSeeding_RemoveFailureLeavesCleanupPending(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_stopseeding_remove_fail",
		Source:            "magnet:?xt=urn:btih:stopseedremfail11112222",
		Name:              "Stop Seeding Remove Fail",
		Status:            StatusSeeding,
		Engine:            "qbittorrent",
		EngineID:          "stopseedremfail11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: true,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		stopDownloadFunc: func(hash string) error { return nil },
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return errors.New("qBittorrent daemon connection error")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	returnedJob, err := mgr.StopSeeding(ctx, j.ID)
	if err == nil {
		t.Fatalf("expected StopSeeding to return error when RemoveTorrent fails")
	}

	appErr, ok := err.(*AppError)
	if !ok || appErr.Code != ErrEngineError {
		t.Errorf("expected AppError code ENGINE_ERROR on cleanup failure, got %v", err)
	}

	if returnedJob == nil || returnedJob.Status != StatusCompleted {
		t.Fatalf("expected StopSeeding to return non-nil job with StatusCompleted on daemon cleanup error")
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", after.Status)
	}
	if !after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == true when RemoveTorrent fails in StopSeeding")
	}
	if after.FinalPath != downloadDir {
		t.Errorf("expected FinalPath == %s, got %s", downloadDir, after.FinalPath)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
			t.Errorf("no terminal events should be emitted when RemoveTorrent fails in StopSeeding, got %s", ev.Type)
		}
	}
}

func TestStopSeeding_CleanupSuccessOrdering(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_stopseeding_order",
		Source:            "magnet:?xt=urn:btih:stopseedorder11112222",
		Name:              "Stop Seeding Order",
		Status:            StatusSeeding,
		Engine:            "qbittorrent",
		EngineID:          "stopseedorder11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: true,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	var checkedStatusInDB JobStatus
	var checkedPendingInDB bool

	torrentEng := &fakeTorrentEngine{
		stopDownloadFunc: func(hash string) error { return nil },
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			inDB, err := jobRepo.GetByID(ctx, j.ID)
			if err == nil && inDB != nil {
				checkedStatusInDB = inDB.Status
				checkedPendingInDB = inDB.EngineCleanupPending
			}
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	resJob, err := mgr.StopSeeding(ctx, j.ID)
	if err != nil {
		t.Fatalf("StopSeeding failed: %v", err)
	}

	if checkedStatusInDB != StatusCompleted {
		t.Errorf("expected DB status COMPLETED inside RemoveTorrent call, got %s", checkedStatusInDB)
	}
	if !checkedPendingInDB {
		t.Error("expected DB EngineCleanupPending == true inside RemoveTorrent call")
	}

	if resJob.EngineCleanupPending {
		t.Error("expected returned Job EngineCleanupPending == false after success")
	}

	var completedEvents int
	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			completedEvents++
		}
	}
	if completedEvents != 1 {
		t.Errorf("expected EventJobCompleted exactly once in StopSeeding, got %d", completedEvents)
	}
}

type toggleMarkerFailJobRepo struct {
	IJobRepository
	updateCount      int
	failSecondUpdate bool
}

func (t *toggleMarkerFailJobRepo) Update(ctx context.Context, j *Job) error {
	t.updateCount++
	if t.failSecondUpdate && !j.EngineCleanupPending && j.Status == StatusCompleted {
		return errors.New("simulated DB update failure on clearing EngineCleanupPending")
	}
	return t.IJobRepository.Update(ctx, j)
}

func TestTorrentCompletion_CleanupMarkerPersistenceFailureDoesNotPublishCompleted(t *testing.T) {
	mgr, baseJobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	toggleRepo := &toggleMarkerFailJobRepo{IJobRepository: baseJobRepo, failSecondUpdate: true}
	mgr.repo = toggleRepo

	j := &Job{
		ID:                "job_torrent_marker_fail",
		Source:            "magnet:?xt=urn:btih:markerfail111122223333",
		Name:              "Marker Fail Test",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "markerfail111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	baseJobRepo.Create(ctx, j)

	var removeCalled bool
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	if !removeCalled {
		t.Error("expected RemoveTorrent to be called")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted when marker clear persistence failed!")
		}
	}

	inDB, _ := baseJobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusCompleted || !inDB.EngineCleanupPending {
		t.Errorf("expected DB status COMPLETED and EngineCleanupPending true, got status=%s pending=%v", inDB.Status, inDB.EngineCleanupPending)
	}

	toggleRepo.failSecondUpdate = false
	mgr.processPendingEngineCleanups(ctx)

	after, _ := baseJobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after retry")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted during cleanup retry!")
		}
	}
}

func TestTorrentCleanupPending_MarkerPersistenceFailureRemainsPending(t *testing.T) {
	mgr, baseJobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	toggleRepo := &toggleMarkerFailJobRepo{IJobRepository: baseJobRepo, failSecondUpdate: true, updateCount: 1}
	mgr.repo = toggleRepo

	j := &Job{
		ID:                   "job_retry_marker_fail",
		Source:               "magnet:?xt=urn:btih:retrymarkerfail11112222",
		Name:                 "Retry Marker Fail",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "retrymarkerfail11112222",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	baseJobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	err := mgr.retryPendingEngineCleanup(ctx, j)
	if err == nil {
		t.Fatalf("expected error when clearing marker fails in retry")
	}

	inDB, _ := baseJobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusCompleted || !inDB.EngineCleanupPending {
		t.Errorf("expected DB to remain COMPLETED and EngineCleanupPending true, got status=%s pending=%v", inDB.Status, inDB.EngineCleanupPending)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted during failed retry!")
		}
	}

	toggleRepo.failSecondUpdate = false
	err = mgr.retryPendingEngineCleanup(ctx, j)
	if err != nil {
		t.Fatalf("expected second retry to succeed, got %v", err)
	}

	after, _ := baseJobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after successful retry")
	}
}

func TestMonitor_RetriesPendingEngineCleanupDuringRuntime(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                   "job_runtime_pending_test",
		Source:               "magnet:?xt=urn:btih:runtimepending11112222",
		Name:                 "Runtime Pending Test",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "runtimepending11112222",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	jobRepo.Create(ctx, j)

	var removeCalls int
	var qbHealthy bool
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalls++
			if !qbHealthy {
				return errors.New("qBittorrent offline")
			}
			return nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mon := NewMonitor(mgr, 10*time.Millisecond)
	mon.SetCleanupSweepInterval(10 * time.Millisecond)

	if len(mgr.GetActiveJobs()) != 0 {
		t.Fatalf("expected 0 active jobs, got %d", len(mgr.GetActiveJobs()))
	}

	mon.tick(ctx)
	if removeCalls != 1 {
		t.Errorf("expected 1 remove call on tick 1, got %d", removeCalls)
	}

	inDB, _ := jobRepo.GetByID(ctx, j.ID)
	if !inDB.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == true after tick 1 failure")
	}

	time.Sleep(15 * time.Millisecond)
	qbHealthy = true

	mon.tick(ctx)
	if removeCalls != 2 {
		t.Errorf("expected 2 remove calls after tick 2, got %d", removeCalls)
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after tick 2 success")
	}
	if after.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", after.Status)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted during runtime cleanup retry!")
		}
	}
}

func TestMonitor_PendingCleanupRetryIsThrottled(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                   "job_throttled_test",
		Source:               "magnet:?xt=urn:btih:throttled111122223333",
		Name:                 "Throttled Test",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "throttled111122223333",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	jobRepo.Create(ctx, j)

	var removeCalls int
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			removeCalls++
			return errors.New("daemon offline")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	mon := NewMonitor(mgr, 10*time.Millisecond)
	mon.SetCleanupSweepInterval(1 * time.Hour)

	for i := 0; i < 5; i++ {
		mon.tick(ctx)
	}

	if removeCalls != 1 {
		t.Errorf("expected exactly 1 remove call due to throttling, got %d", removeCalls)
	}
}

func TestTorrentCleanupPending_AlreadyRemovedTreatedAsSuccess(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                   "job_already_removed_test",
		Source:               "magnet:?xt=urn:btih:alreadyremoved11112222",
		Name:                 "Already Removed Test",
		Status:               StatusCompleted,
		Engine:               "qbittorrent",
		EngineID:             "alreadyremoved11112222",
		Type:                 TypeTorrent,
		SeedAfterComplete:    false,
		DestinationDir:       downloadDir,
		FinalPath:            downloadDir,
		EngineCleanupPending: true,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}
	jobRepo.Create(ctx, j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return errors.New("Torrent not found")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	err := mgr.retryPendingEngineCleanup(ctx, j)
	if err != nil {
		t.Fatalf("expected already-removed torrent error to be treated as success, got %v", err)
	}

	after, _ := jobRepo.GetByID(ctx, j.ID)
	if after.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == false after already-removed cleanup")
	}
	if after.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", after.Status)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted during cleanup retry!")
		}
	}
}

func TestTorrentCompletion_MarkerClearFailureRemovesActiveTracking(t *testing.T) {
	mgr, baseJobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	toggleRepo := &toggleMarkerFailJobRepo{IJobRepository: baseJobRepo, failSecondUpdate: true}
	mgr.repo = toggleRepo

	j := &Job{
		ID:                "job_marker_clear_active_rem",
		Source:            "magnet:?xt=urn:btih:markeractive111122223333",
		Name:              "Marker Active Test",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "markeractive111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	baseJobRepo.Create(ctx, j)
	mgr.addActive(j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusSeeding, Progress: 100}, true)

	inDB, _ := baseJobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusCompleted || !inDB.EngineCleanupPending {
		t.Errorf("expected DB status COMPLETED and EngineCleanupPending true, got status=%s pending=%v", inDB.Status, inDB.EngineCleanupPending)
	}

	if activeJobs := mgr.GetActiveJobs(); len(activeJobs) != 0 {
		t.Errorf("expected activeJobs to be empty after durable COMPLETED persistence, got %d active jobs", len(activeJobs))
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted {
			t.Errorf("EventJobCompleted emitted when marker clear persistence failed!")
		}
	}
}

func TestTorrentCompletion_MarkerClearFailureCannotBeOverwrittenByMonitorFailure(t *testing.T) {
	mgr, baseJobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	toggleRepo := &toggleMarkerFailJobRepo{IJobRepository: baseJobRepo, failSecondUpdate: true}
	mgr.repo = toggleRepo

	j := &Job{
		ID:                "job_marker_fail_no_overwrite",
		Source:            "magnet:?xt=urn:btih:nooverwrite111122223333",
		Name:              "No Overwrite Test",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "nooverwrite111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	baseJobRepo.Create(ctx, j)
	mgr.addActive(j)

	var statusQueries int
	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error { return nil },
		statusFunc: func(ctx context.Context, job *Job) (*EngineStatus, error) {
			statusQueries++
			return nil, errors.New("torrent not found in qBittorrent")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	if activeJobs := mgr.GetActiveJobs(); len(activeJobs) != 0 {
		t.Fatalf("expected job to be removed from activeJobs")
	}

	staleCopy := *j
	staleCopy.Status = StatusDownloading
	mgr.addActive(&staleCopy)

	mon := NewMonitor(mgr, 10*time.Millisecond)
	mon.SetCleanupSweepInterval(1 * time.Hour)

	for i := 0; i < maxConsecutiveFailures+1; i++ {
		mon.tick(ctx)
	}

	inDB, _ := baseJobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusCompleted {
		t.Errorf("expected DB status to remain StatusCompleted, got %s", inDB.Status)
	}
	if !inDB.EngineCleanupPending {
		t.Error("expected EngineCleanupPending to remain true")
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed was published over durable COMPLETED job!")
		}
	}
}

func TestTorrentCompletion_CleanupFailureReleasesActiveTracking(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_cleanup_fail_release_active",
		Source:            "magnet:?xt=urn:btih:cleanupsfailrel11112222",
		Name:              "Cleanup Fail Release Active",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "cleanupsfailrel11112222",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)
	mgr.addActive(j)

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return errors.New("daemon RPC error")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	inDB, _ := jobRepo.GetByID(ctx, j.ID)
	if inDB.Status != StatusCompleted || !inDB.EngineCleanupPending {
		t.Errorf("expected DB status COMPLETED and EngineCleanupPending true, got status=%s pending=%v", inDB.Status, inDB.EngineCleanupPending)
	}

	if activeJobs := mgr.GetActiveJobs(); len(activeJobs) != 0 {
		t.Errorf("expected activeJobs to be empty, got %d active jobs", len(activeJobs))
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
			t.Errorf("no terminal events should be emitted, got %s", ev.Type)
		}
	}
}

func TestTorrentCompletion_KicksSchedulerBeforeDaemonCleanupCompletes(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:                "job_kick_before_cleanup",
		Source:            "magnet:?xt=urn:btih:kickbeforecleanup1111",
		Name:              "Kick Before Cleanup",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "kickbeforecleanup1111",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, j)

	var schedulerKickedBeforeCleanup bool

	limitFn := func(ctx context.Context) int { return 1 }
	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	mgr.scheduler = sched

	torrentEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			if len(mgr.GetActiveJobs()) == 0 {
				schedulerKickedBeforeCleanup = true
			}
			return errors.New("daemon error")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	mgr.addActive(j)

	mgr.UpdateJobFromEngine(ctx, j, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	if !schedulerKickedBeforeCleanup {
		t.Error("expected activeJobs to be cleared and scheduler kicked before daemon cleanup completes")
	}
}

func TestTorrentCompletion_NextQueuedJobDispatchesWhileCleanupPending(t *testing.T) {
	mgr, jobRepo, queueRepo, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	limitFn := func(ctx context.Context) int { return 1 }

	jA := &Job{
		ID:                "job_a_cleanup_pending",
		Source:            "magnet:?xt=urn:btih:jobacleanup111122223333",
		Name:              "Job A",
		Status:            StatusDownloading,
		Engine:            "qbittorrent",
		EngineID:          "jobacleanup111122223333",
		Type:              TypeTorrent,
		SeedAfterComplete: false,
		DestinationDir:    downloadDir,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	jobRepo.Create(ctx, jA)
	mgr.addActive(jA)

	jB := &Job{
		ID:             "job_b_queued",
		Source:         "https://example.com/fileB.zip",
		Name:           "Job B",
		Status:         StatusQueued,
		Engine:         "aria2",
		Type:           TypeDownload,
		TotalBytes:     1024,
		Priority:       JobPriorityNormal,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, jB)
	queueRepo.Enqueue(ctx, &QueueEntry{JobID: jB.ID, Action: QueueActionStart, Position: 1, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})

	ariaEng := &fakeEngine{
		startFunc: func(ctx context.Context, j *Job, dir string) (string, error) {
			return "gid_b", nil
		},
	}
	qbitEng := &fakeTorrentEngine{
		removeTorrentFunc: func(hash string, deleteFiles bool) error {
			return errors.New("qBittorrent offline")
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["aria2"] = ariaEng
	reg.engines["qbittorrent"] = qbitEng

	sched := NewScheduler(jobRepo, queueRepo, limitFn, mgr.dispatchQueuedJob)
	sched.SetEngineRegistry(reg)
	sched.SetEventBus(mgr.bus)
	mgr.scheduler = sched
	sched.Start(ctx)
	defer sched.Stop()

	mgr.UpdateJobFromEngine(ctx, jA, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)

	inDBA, _ := jobRepo.GetByID(ctx, jA.ID)
	if inDBA.Status != StatusCompleted || !inDBA.EngineCleanupPending {
		t.Fatalf("expected Job A status COMPLETED and EngineCleanupPending true")
	}
	if len(mgr.GetActiveJobs()) != 0 {
		t.Fatalf("expected activeJobs to be empty after Job A completion")
	}

	sched.Kick()

	var updatedB *Job
	for i := 0; i < 40; i++ {
		time.Sleep(25 * time.Millisecond)
		updatedB, _ = jobRepo.GetByID(ctx, jB.ID)
		if updatedB != nil && updatedB.Status == StatusDownloading {
			break
		}
	}

	if updatedB == nil || updatedB.Status != StatusDownloading {
		t.Errorf("expected Job B to dispatch and reach StatusDownloading while Job A cleanup is pending, got %v", updatedB)
	}
}

func TestMonitor_DoesNotPollTerminalActiveJob(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	j := &Job{
		ID:             "job_mon_terminal_skip",
		Source:         "magnet:?xt=urn:btih:terminalskip11112222",
		Name:           "Terminal Skip",
		Status:         StatusCompleted,
		Engine:         "qbittorrent",
		EngineID:       "terminalskip11112222",
		Type:           TypeTorrent,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	jobRepo.Create(ctx, j)
	mgr.addActive(j)

	var statusCalled bool
	torrentEng := &fakeTorrentEngine{
		statusFunc: func(ctx context.Context, job *Job) (*EngineStatus, error) {
			statusCalled = true
			return &EngineStatus{Status: StatusCompleted}, nil
		},
	}
	reg := mgr.engines.(*fakeEngineRegistry)
	reg.engines["qbittorrent"] = torrentEng

	mon := NewMonitor(mgr, 10*time.Millisecond)
	mon.SetCleanupSweepInterval(1 * time.Hour)

	mon.tick(ctx)

	if statusCalled {
		t.Error("Monitor MUST NOT call engine.Status for terminal jobs")
	}

	if len(mgr.GetActiveJobs()) != 0 {
		t.Error("Monitor should remove terminal job from activeJobs")
	}
}

func TestMonitor_RecordFailureDoesNotOverwriteDurableCompletedJob(t *testing.T) {
	mgr, jobRepo, _, _, downloadDir, _ := setupStorageTestEnv(t)
	ctx := context.Background()

	staleCopy := &Job{
		ID:             "job_record_fail_guard",
		Source:         "magnet:?xt=urn:btih:recordfailguard11112222",
		Name:           "Record Fail Guard",
		Status:         StatusDownloading,
		Engine:         "qbittorrent",
		EngineID:       "recordfailguard11112222",
		Type:           TypeTorrent,
		DestinationDir: downloadDir,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	durableJob := *staleCopy
	durableJob.Status = StatusCompleted
	durableJob.EngineCleanupPending = true
	jobRepo.Create(ctx, &durableJob)

	mgr.addActive(staleCopy)

	mon := NewMonitor(mgr, 10*time.Millisecond)
	bus := mgr.bus.(*fakeEventBus)
	ch := bus.Subscribe()

	for i := 0; i < maxConsecutiveFailures; i++ {
		mon.recordFailure(ctx, staleCopy, "Engine connection reset")
	}

	inDB, _ := jobRepo.GetByID(ctx, staleCopy.ID)
	if inDB.Status != StatusCompleted {
		t.Errorf("expected DB status to remain StatusCompleted, got %s", inDB.Status)
	}

	for len(ch) > 0 {
		ev := <-ch
		if ev.Type == EventJobFailed {
			t.Errorf("EventJobFailed emitted over durable COMPLETED job!")
		}
	}

	if len(mgr.GetActiveJobs()) != 0 {
		t.Error("expected stale active job to be removed from activeJobs")
	}
}
