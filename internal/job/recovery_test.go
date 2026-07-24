package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupRecoveryTest(t *testing.T, statusFunc func(ctx context.Context, j *Job) (*EngineStatus, error)) (*Manager, IJobRepository, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	repo := newFakeJobRepository()
	fakeEng := &fakeEngine{statusFunc: statusFunc}
	bus := newFakeEventBus()
	downloadDir := filepath.Join(tmpDir, "downloads")
	os.MkdirAll(downloadDir, 0755)

	m := NewManager(repo, fakeEng, bus, downloadDir, nil)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return m, repo, cleanup
}

func createTestJob(t *testing.T, repo IJobRepository, id string, status JobStatus, engineID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	j := &Job{
		ID:        id,
		Source:    "https://example.com/file.zip",
		Name:      "file.zip",
		Status:    status,
		Engine:    "aria2",
		EngineID:  engineID,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}
}

func TestRecovery_ActiveJobReconnects(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:              StatusDownloading,
			TotalBytes:          1000000,
			CompletedBytes:      500000,
			SpeedBytesPerSecond: 50000,
			Progress:            50.0,
		}, nil
	})
	defer cleanup()

	createTestJob(t, repo, "recover-1", StatusDownloading, "gid-1")

	ctx := context.Background()
	m.recover(ctx)

	// Check the job was recovered as active
	activeJobs := m.GetActiveJobs()
	if _, exists := activeJobs["recover-1"]; !exists {
		t.Error("expected job to be in active jobs after recovery")
	}

	got, _ := repo.GetByID(ctx, "recover-1")
	if got.Status != StatusDownloading {
		t.Errorf("expected downloading, got %s", got.Status)
	}
	if got.Progress != 50.0 {
		t.Errorf("expected progress 50.0, got %f", got.Progress)
	}
}

func TestRecovery_PausedJobReconnects(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusPaused,
			TotalBytes:     1000000,
			CompletedBytes: 300000,
			Progress:       30.0,
		}, nil
	})
	defer cleanup()

	createTestJob(t, repo, "recover-2", StatusPaused, "gid-2")

	ctx := context.Background()
	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "recover-2")
	if got.Status != StatusPaused {
		t.Errorf("expected paused, got %s", got.Status)
	}
}

func TestRecovery_CompletedEngineJob(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusCompleted,
			TotalBytes:     1000000,
			CompletedBytes: 1000000,
			Progress:       100.0,
		}, nil
	})
	defer cleanup()

	createTestJob(t, repo, "recover-3", StatusDownloading, "gid-3")

	ctx := context.Background()
	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "recover-3")
	if got.Status != StatusCompleted {
		t.Errorf("expected completed, got %s", got.Status)
	}
	if got.Progress != 100.0 {
		t.Errorf("expected progress 100, got %f", got.Progress)
	}
}

func TestRecovery_MissingEngineJob(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return nil, fmt.Errorf("GID not found")
	})
	defer cleanup()

	createTestJob(t, repo, "recover-4", StatusDownloading, "gid-4")

	ctx := context.Background()
	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "recover-4")
	if got.Status != StatusFailed {
		t.Errorf("expected failed, got %s", got.Status)
	}
	if got.Error == "" {
		t.Error("expected error message for failed recovery")
	}
}

func TestRecovery_NoEngineID(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()

	createTestJob(t, repo, "recover-5", StatusDownloading, "")

	ctx := context.Background()
	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "recover-5")
	if got.Status != StatusFailed {
		t.Errorf("expected failed, got %s", got.Status)
	}
	if got.Error == "" {
		t.Error("expected error message")
	}
}

func TestRecovery_EngineUnavailable(t *testing.T) {
	// Engine is unavailable — should not crash
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return nil, fmt.Errorf("connection refused")
	})
	defer cleanup()

	createTestJob(t, repo, "recover-6", StatusQueued, "gid-6")

	ctx := context.Background()
	// This should not panic
	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "recover-6")
	if got.Status != StatusFailed {
		t.Errorf("expected failed, got %s", got.Status)
	}
}

func TestRecovery_MediaJobFailsOnRestart(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()

	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	mediaJob := &Job{
		ID:        "media-recover-1",
		Source:    "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Name:      "test media",
		Status:    StatusDownloading,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		EngineID:  "media-recover-1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("failed to create media job: %v", err)
	}

	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "media-recover-1")
	if got.Status != StatusFailed {
		t.Errorf("expected media job to be marked failed on restart, got %s", got.Status)
	}
	if got.Error == "" {
		t.Error("expected error message for interrupted media job")
	}
}

func TestRecovery_Torrent_AwaitingSelection_SurvivesRestart(t *testing.T) {
	m, fakeEng, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var statusCalled bool
	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		statusCalled = true
		return &EngineStatus{Status: StatusAwaitingSelection}, nil
	}

	j := &Job{
		ID:        "torrent-awaiting-1",
		Source:    "magnet:?xt=urn:btih:hash-await-1",
		Name:      "test-torrent",
		Status:    StatusAwaitingSelection,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-await-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)

	m.recover(ctx)

	got, _ := m.repo.GetByID(ctx, "torrent-awaiting-1")
	if got.Status != StatusAwaitingSelection {
		t.Errorf("expected AwaitingSelection to survive restart, got %s", got.Status)
	}
	if !statusCalled {
		t.Error("expected engine Status check during awaiting_selection recovery")
	}
	_ = fakeEng
}

func TestRecovery_Torrent_Downloading_Reattaches(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusDownloading,
			TotalBytes:     1000,
			CompletedBytes: 500,
			Progress:       50.0,
		}, nil
	}

	j := &Job{
		ID:        "torrent-dl-1",
		Source:    "magnet:?xt=urn:btih:hash-dl-1",
		Name:      "downloading-torrent",
		Status:    StatusDownloading,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-dl-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-dl-1")
	if got.Status != StatusDownloading {
		t.Errorf("expected StatusDownloading after recovery, got %s", got.Status)
	}
	active := m.GetActiveJobs()
	if _, ok := active["torrent-dl-1"]; !ok {
		t.Error("expected recovering downloading torrent to be active")
	}
}

func TestRecovery_Torrent_Paused_Reattaches(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusPaused,
			TotalBytes:     1000,
			CompletedBytes: 200,
			Progress:       20.0,
		}, nil
	}

	j := &Job{
		ID:        "torrent-pause-1",
		Source:    "magnet:?xt=urn:btih:hash-pause-1",
		Name:      "paused-torrent",
		Status:    StatusPaused,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-pause-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-pause-1")
	if got.Status != StatusPaused {
		t.Errorf("expected StatusPaused after recovery, got %s", got.Status)
	}
}

func TestRecovery_Torrent_Seeding_SeedAfterCompleteTrue(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalled bool
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}
	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:      StatusSeeding,
			TotalBytes:  1000,
			Progress:    100.0,
			UploadSpeed: 500,
		}, nil
	}

	j := &Job{
		ID:        "torrent-seed-true",
		Source:    "magnet:?xt=urn:btih:hash-seed-true",
		Name:      "seeding-torrent-true",
		Status:    StatusSeeding,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-seed-true",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:             "torrent-seed-true",
		InfoHash:          "hash-seed-true",
		SeedAfterComplete: true,
	})

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-seed-true")
	if got.Status != StatusSeeding {
		t.Errorf("expected StatusSeeding for seedAfterComplete=true, got %s", got.Status)
	}
	if !got.SeedAfterComplete {
		t.Error("expected SeedAfterComplete to be hydrated as true")
	}
	if removeCalled {
		t.Error("RemoveTorrent should NOT be called for seedAfterComplete=true during recovery")
	}

	// Next engine update should keep it seeding
	m.UpdateJobFromEngine(ctx, got, &EngineStatus{Status: StatusSeeding, UploadSpeed: 600}, true)
	if got.Status != StatusSeeding {
		t.Errorf("expected job to remain StatusSeeding on update, got %s", got.Status)
	}
	if removeCalled {
		t.Error("RemoveTorrent should NOT be called on subsequent status update")
	}
}

func TestRecovery_Torrent_Seeding_SeedAfterCompleteFalse(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalled bool
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}
	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:      StatusSeeding,
			TotalBytes:  1000,
			Progress:    100.0,
			UploadSpeed: 500,
		}, nil
	}

	j := &Job{
		ID:        "torrent-seed-false",
		Source:    "magnet:?xt=urn:btih:hash-seed-false",
		Name:      "seeding-torrent-false",
		Status:    StatusSeeding,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-seed-false",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:             "torrent-seed-false",
		InfoHash:          "hash-seed-false",
		SeedAfterComplete: false,
	})

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-seed-false")
	if got.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted for seedAfterComplete=false during recovery, got %s", got.Status)
	}
	if !removeCalled {
		t.Error("expected RemoveTorrent(false) to be called for seedAfterComplete=false during recovery")
	}
}

func TestRecovery_Torrent_MissingQB(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return nil, fmt.Errorf("torrent not found in qBittorrent")
	}

	j := &Job{
		ID:        "torrent-missing-1",
		Source:    "magnet:?xt=urn:btih:hash-missing-1",
		Name:      "missing-torrent",
		Status:    StatusDownloading,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-missing-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-missing-1")
	if got.Status != StatusFailed {
		t.Errorf("expected StatusFailed for missing torrent in qB, got %s", got.Status)
	}
}
