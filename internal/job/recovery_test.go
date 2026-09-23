package job

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	createTestJob(t, repo, "recover-6", StatusDownloading, "gid-6")

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
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID})

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
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID})

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

func TestRecovery_Torrent_Seeding_RemoveTorrentFailure(t *testing.T) {
	m, _, bus, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		return fmt.Errorf("qBittorrent API timeout")
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
		ID:        "torrent-seed-fail",
		Source:    "magnet:?xt=urn:btih:hash-seed-fail",
		Name:      "seeding-torrent-fail",
		Status:    StatusSeeding,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-seed-fail",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:             "torrent-seed-fail",
		InfoHash:          "hash-seed-fail",
		SeedAfterComplete: false,
	})

	m.recover(ctx)

	got, _ := m.Get(ctx, "torrent-seed-fail")
	if got.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted for RemoveTorrent failure during recovery, got %s", got.Status)
	}
	if !got.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == true when RemoveTorrent fails during recovery")
	}

	for len(sub) > 0 {
		ev := <-sub
		if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
			t.Errorf("expected no completion or failure events when RemoveTorrent fails during recovery, got %s", ev.Type)
		}
	}
}

func TestRecovery_V05_DirectQueued_SurvivesRestart(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	createTestJob(t, repo, "queued-direct-1", StatusQueued, "")

	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "queued-direct-1")
	if got.Status != StatusQueued {
		t.Errorf("expected QUEUED direct job to remain QUEUED across restart, got %s", got.Status)
	}
}

func TestRecovery_V05_MediaQueued_SurvivesRestart(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	now := time.Now()
	j := &Job{
		ID:        "queued-media-1",
		Source:    "https://example.com/video.mp4",
		Name:      "test media",
		Status:    StatusQueued,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		EngineID:  "",
		CreatedAt: now,
		UpdatedAt: now,
	}
	repo.Create(ctx, j)

	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "queued-media-1")
	if got.Status != StatusQueued {
		t.Errorf("expected QUEUED media job to remain QUEUED across restart, got %s", got.Status)
	}
}

func TestRecovery_V05_TorrentQueued_SurvivesRestart(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var startDownloadCalled bool
	fakeTorrent.startDownloadFunc = func(hash string) error {
		startDownloadCalled = true
		return nil
	}
	fakeTorrent.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{Status: StatusPaused}, nil
	}

	j := &Job{
		ID:        "queued-torrent-1",
		Source:    "magnet:?xt=urn:btih:hash-q-1",
		Name:      "queued torrent",
		Status:    StatusQueued,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-q-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID})

	m.recover(ctx)

	got, _ := m.repo.GetByID(ctx, "queued-torrent-1")
	if got.Status != StatusQueued {
		t.Errorf("expected QUEUED torrent job to remain QUEUED across restart, got %s", got.Status)
	}
	if startDownloadCalled {
		t.Error("StartDownload MUST NOT be called during restart recovery for queued torrent")
	}
}

func TestRecovery_V05_PausedJob_SurvivesRestart(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	createTestJob(t, repo, "paused-job-1", StatusPaused, "")

	m.recover(ctx)

	got, _ := repo.GetByID(ctx, "paused-job-1")
	if got.Status != StatusPaused {
		t.Errorf("expected PAUSED job to remain PAUSED across restart, got %s", got.Status)
	}
}

func TestRecoverySummary_CleanStartup(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	// Preserved queued and paused jobs should NOT count as recovery interventions
	createTestJob(t, repo, "q-job", StatusQueued, "")
	createTestJob(t, repo, "p-job", StatusPaused, "")

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReconciledFinalizations != 0 {
		t.Errorf("expected ReconciledFinalizations=0, got %d", summary.ReconciledFinalizations)
	}
	if summary.ReattachedTransfers != 0 {
		t.Errorf("expected ReattachedTransfers=0, got %d", summary.ReattachedTransfers)
	}
	if summary.RestartedMetadataAcquisitions != 0 {
		t.Errorf("expected RestartedMetadataAcquisitions=0, got %d", summary.RestartedMetadataAcquisitions)
	}
	if summary.InterruptedMediaJobs != 0 {
		t.Errorf("expected InterruptedMediaJobs=0, got %d", summary.InterruptedMediaJobs)
	}
	if len(summary.Issues) != 0 {
		t.Errorf("expected Issues to be empty, got %v", summary.Issues)
	}
}

func TestRecoverySummary_InterruptedMedia(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	mediaJob := &Job{
		ID:        "media-1",
		Source:    "https://example.com/watch?v=secret_video&auth=SECRET_TOKEN",
		Name:      "video.mp4",
		Status:    StatusDownloading,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		EngineID:  "yt-1",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("failed to create media job: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.InterruptedMediaJobs != 1 {
		t.Errorf("expected InterruptedMediaJobs=1, got %d", summary.InterruptedMediaJobs)
	}
	if len(summary.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(summary.Issues))
	}
	if summary.Issues[0].JobID != "media-1" {
		t.Errorf("expected JobID=media-1, got %s", summary.Issues[0].JobID)
	}
	if summary.Issues[0].Kind != RecoveryIssueInterruptedMedia {
		t.Errorf("expected Kind=interrupted_media, got %s", summary.Issues[0].Kind)
	}

	// Verify job transitioned to StatusFailed
	got, _ := repo.GetByID(ctx, "media-1")
	if got.Status != StatusFailed {
		t.Errorf("expected StatusFailed for interrupted media job, got %s", got.Status)
	}
}

func TestRecoverySummary_ReattachedTransfer(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:              StatusDownloading,
			TotalBytes:          2000,
			CompletedBytes:      1000,
			SpeedBytesPerSecond: 100,
			Progress:            50.0,
		}, nil
	})
	defer cleanup()
	ctx := context.Background()

	createTestJob(t, repo, "active-job", StatusDownloading, "aria2-active-id")

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReattachedTransfers != 1 {
		t.Errorf("expected ReattachedTransfers=1, got %d", summary.ReattachedTransfers)
	}
	if len(summary.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", summary.Issues)
	}
}

func TestRecoverySummary_RestartedMetadata(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	tmpDir := t.TempDir()
	torrentFile := filepath.Join(tmpDir, "test.torrent")
	if err := os.WriteFile(torrentFile, []byte("d8:announce3:fakee"), 0644); err != nil {
		t.Fatalf("failed to write torrent file: %v", err)
	}

	j := &Job{
		ID:        "torrent-analyzing",
		Source:    "torrent://" + torrentFile,
		Name:      "test.torrent",
		Status:    StatusAnalyzing,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}
	m.torrentRepo = newFakeTorrentRepository(repo)
	if err := m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:           j.ID,
		TorrentFilePath: torrentFile,
	}); err != nil {
		t.Fatalf("failed to create torrent job record: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.RestartedMetadataAcquisitions != 1 {
		t.Errorf("expected RestartedMetadataAcquisitions=1, got %d", summary.RestartedMetadataAcquisitions)
	}
}

func TestRecoverySummary_TorrentMetadataUnrecoverable(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	j := &Job{
		ID:        "torrent-missing-file",
		Source:    "torrent://non-existent-path.torrent",
		Name:      "missing.torrent",
		Status:    StatusAnalyzing,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}
	m.torrentRepo = newFakeTorrentRepository(repo)
	if err := m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
		JobID:           j.ID,
		TorrentFilePath: "non-existent-path.torrent",
	}); err != nil {
		t.Fatalf("failed to create torrent job record: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if len(summary.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(summary.Issues))
	}
	if summary.Issues[0].Kind != RecoveryIssueTorrentMetadataUnrecoverable {
		t.Errorf("expected RecoveryIssueTorrentMetadataUnrecoverable, got %s", summary.Issues[0].Kind)
	}
}

func TestRecoverySummary_GetterCopySafety(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	mediaJob := &Job{
		ID:        "media-copy-safety",
		Source:    "https://example.com/video",
		Name:      "video.mp4",
		Status:    StatusDownloading,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	m.recover(ctx)

	s1 := m.GetRecoverySummary()
	if len(s1.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(s1.Issues))
	}

	// Mutate returned copy
	s1.Issues[0].JobID = "MUTATED_ID"
	s1.Issues[0].Kind = "MUTATED_KIND"
	s1.Issues = append(s1.Issues, RecoveryIssue{JobID: "MUTATED_EXTRA", Kind: "MUTATED"})
	s1.InterruptedMediaJobs = 999

	// Read fresh copy from manager
	s2 := m.GetRecoverySummary()
	if s2.InterruptedMediaJobs != 1 {
		t.Errorf("stored summary was mutated! InterruptedMediaJobs=%d", s2.InterruptedMediaJobs)
	}
	if len(s2.Issues) != 1 {
		t.Fatalf("stored issues slice was mutated! len=%d", len(s2.Issues))
	}
	if s2.Issues[0].JobID != "media-copy-safety" {
		t.Errorf("stored issue JobID was mutated! got %s", s2.Issues[0].JobID)
	}
	if s2.Issues[0].Kind != RecoveryIssueInterruptedMedia {
		t.Errorf("stored issue Kind was mutated! got %s", s2.Issues[0].Kind)
	}
}

func TestRecoverySummary_RepeatedReads(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	mediaJob := &Job{
		ID:        "media-repeat",
		Source:    "https://example.com/video",
		Name:      "video.mp4",
		Status:    StatusDownloading,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	m.recover(ctx)

	s1 := m.GetRecoverySummary()
	s2 := m.GetRecoverySummary()

	if s1.InterruptedMediaJobs != s2.InterruptedMediaJobs {
		t.Errorf("repeated reads mismatch: %d != %d", s1.InterruptedMediaJobs, s2.InterruptedMediaJobs)
	}
	if len(s1.Issues) != len(s2.Issues) {
		t.Errorf("repeated reads issues length mismatch: %d != %d", len(s1.Issues), len(s2.Issues))
	}
	if len(s1.Issues) > 0 && s1.Issues[0] != s2.Issues[0] {
		t.Errorf("repeated reads issue content mismatch")
	}
}

func TestRecoverySummary_PrivacySanitization(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, nil)
	defer cleanup()
	ctx := context.Background()

	secretURL := "https://private.domain.com/path?secret=SUPER_SECRET_TOKEN&auth=BEARER_TOKEN&token=SECRET_VALUE"
	mediaJob := &Job{
		ID:        "job-privacy-check",
		Source:    secretURL,
		Name:      "C:\\Users\\SecretUser\\TopSecret\\file.part",
		Status:    StatusDownloading,
		Type:      TypeMedia,
		Engine:    "ytdlp",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := repo.Create(ctx, mediaJob); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("failed to marshal summary: %v", err)
	}
	jsonStr := string(data)

	// Structural privacy invariants: Data minimization, no free-form strings
	forbiddenSecrets := []string{
		"SUPER_SECRET_TOKEN",
		"BEARER_TOKEN",
		"SECRET_VALUE",
		"TopSecret",
		"SecretUser",
		"private.domain.com",
		"token=",
		"auth=",
		"secret=",
		"Bearer",
		"Authorization",
	}
	for _, forbidden := range forbiddenSecrets {
		if strings.Contains(jsonStr, forbidden) {
			t.Errorf("privacy invariant violated: found forbidden sensitive pattern %q in serialized recovery summary: %s", forbidden, jsonStr)
		}
	}

	// Verify only expected DTO fields are serialized
	var rawMap map[string]interface{}
	if err := json.Unmarshal(data, &rawMap); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}
	for key := range rawMap {
		switch key {
		case "reconciledFinalizations", "reattachedTransfers", "restartedMetadataAcquisitions", "interruptedMediaJobs", "issues":
			// expected fields
		default:
			t.Errorf("unexpected field in serialized RecoverySummary: %s", key)
		}
	}
}

func TestRecoverySummary_ExternalStateUnrecoverable(t *testing.T) {
	// Status() returns an error simulating lost or unreachable external transfer state
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return nil, fmt.Errorf("torrent not found in engine")
	})
	defer cleanup()
	ctx := context.Background()

	createTestJob(t, repo, "lost-transfer", StatusDownloading, "aria2-missing-id")

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReattachedTransfers != 0 {
		t.Errorf("expected ReattachedTransfers=0, got %d", summary.ReattachedTransfers)
	}
	if len(summary.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(summary.Issues))
	}
	if summary.Issues[0].JobID != "lost-transfer" {
		t.Errorf("expected JobID=lost-transfer, got %s", summary.Issues[0].JobID)
	}
	if summary.Issues[0].Kind != RecoveryIssueExternalStateUnrecoverable {
		t.Errorf("expected Kind=external_state_unrecoverable, got %s", summary.Issues[0].Kind)
	}

	// Job should be marked StatusFailed
	got, _ := repo.GetByID(ctx, "lost-transfer")
	if got.Status != StatusFailed {
		t.Errorf("expected StatusFailed, got %s", got.Status)
	}
}

func TestRecoverySummary_EngineReportedStatusFailed_DiscoveredState(t *testing.T) {
	// Engine itself returns StatusFailed. Per Section 8, this is discovered current state from the daemon,
	// NOT a failure caused by restart recovery, so it does not add an issue to RecoverySummary.
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status: StatusFailed,
			Error:  "tracker connection timed out in external daemon",
		}, nil
	})
	defer cleanup()
	ctx := context.Background()

	createTestJob(t, repo, "daemon-failed-job", StatusDownloading, "aria2-failed-id")

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if len(summary.Issues) != 0 {
		t.Errorf("expected 0 recovery issues for discovered engine failure, got %v", summary.Issues)
	}

	// Job should be persisted as StatusFailed with the engine's error
	got, _ := repo.GetByID(ctx, "daemon-failed-job")
	if got.Status != StatusFailed {
		t.Errorf("expected StatusFailed, got %s", got.Status)
	}
	if got.Error != "tracker connection timed out in external daemon" {
		t.Errorf("expected error from engine, got %q", got.Error)
	}
}

func TestRecoverySummary_Seeding_SeedAfterCompleteFalse_NotCountedAsReattached(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusSeeding,
			TotalBytes:     5000000,
			CompletedBytes: 5000000,
			Progress:       100.0,
			UploadSpeed:    250000,
		}, nil
	})
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &Job{
		ID:                "seed-complete-false",
		Source:            "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Name:              "torrent-data",
		Type:              TypeTorrent,
		Status:            StatusDownloading,
		Engine:            "aria2",
		EngineID:          "torrent-gid-1",
		DestinationDir:    t.TempDir(),
		SeedAfterComplete: false,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReattachedTransfers != 0 {
		t.Errorf("expected ReattachedTransfers == 0 for SeedAfterComplete=false, got %d", summary.ReattachedTransfers)
	}
	if len(summary.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", summary.Issues)
	}

	// Job should have finalized and transitioned to StatusCompleted
	got, err := repo.GetByID(ctx, "seed-complete-false")
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", got.Status)
	}

	// Must NOT be actively tracked in activeJobs
	activeJobs := m.GetActiveJobs()
	if _, exists := activeJobs["seed-complete-false"]; exists {
		t.Error("expected job not to be in activeJobs when SeedAfterComplete is false")
	}
}

func TestRecoverySummary_Seeding_SeedAfterCompleteTrue_CountedAsReattached(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:         StatusSeeding,
			TotalBytes:     5000000,
			CompletedBytes: 5000000,
			Progress:       100.0,
			UploadSpeed:    250000,
		}, nil
	})
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &Job{
		ID:                "seed-complete-true",
		Source:            "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Name:              "torrent-data",
		Type:              TypeTorrent,
		Status:            StatusDownloading,
		Engine:            "aria2",
		EngineID:          "torrent-gid-2",
		DestinationDir:    t.TempDir(),
		SeedAfterComplete: true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReattachedTransfers != 1 {
		t.Errorf("expected ReattachedTransfers == 1 for active seeding reattach, got %d", summary.ReattachedTransfers)
	}
	if len(summary.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", summary.Issues)
	}

	// Job should be actively tracked in activeJobs
	activeJobs := m.GetActiveJobs()
	if _, exists := activeJobs["seed-complete-true"]; !exists {
		t.Error("expected job to be in activeJobs when actively reattached for seeding")
	}

	// Persisted status should be StatusSeeding
	got, err := repo.GetByID(ctx, "seed-complete-true")
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if got.Status != StatusSeeding {
		t.Errorf("expected StatusSeeding, got %s", got.Status)
	}
}

func TestRecoverySummary_Downloading_ActiveReattach(t *testing.T) {
	m, repo, cleanup := setupRecoveryTest(t, func(ctx context.Context, j *Job) (*EngineStatus, error) {
		return &EngineStatus{
			Status:              StatusDownloading,
			TotalBytes:          10000000,
			CompletedBytes:      4000000,
			Progress:            40.0,
			SpeedBytesPerSecond: 100000,
		}, nil
	})
	defer cleanup()
	ctx := context.Background()

	now := time.Now().Truncate(time.Second)
	j := &Job{
		ID:             "download-active-reattach",
		Source:         "https://example.com/file.iso",
		Name:           "file.iso",
		Type:           TypeDownload,
		Status:         StatusDownloading,
		Engine:         "aria2",
		EngineID:       "aria2-gid-download",
		DestinationDir: t.TempDir(),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create test job: %v", err)
	}

	m.recover(ctx)

	summary := m.GetRecoverySummary()
	if summary.ReattachedTransfers != 1 {
		t.Errorf("expected ReattachedTransfers == 1 for active download reattach, got %d", summary.ReattachedTransfers)
	}
	if len(summary.Issues) != 0 {
		t.Errorf("expected 0 issues, got %v", summary.Issues)
	}

	// Job should be actively tracked in activeJobs via m.addActive(j)
	activeJobs := m.GetActiveJobs()
	if _, exists := activeJobs["download-active-reattach"]; !exists {
		t.Error("expected job to be in activeJobs after m.addActive path")
	}

	got, err := repo.GetByID(ctx, "download-active-reattach")
	if err != nil {
		t.Fatalf("failed to get job: %v", err)
	}
	if got.Status != StatusDownloading {
		t.Errorf("expected StatusDownloading, got %s", got.Status)
	}
	if got.Progress != 40.0 {
		t.Errorf("expected Progress=40.0, got %f", got.Progress)
	}
}
