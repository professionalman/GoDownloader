package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupRecoveryTest(t *testing.T, statusFunc func(ctx context.Context, j *Job) (*EngineStatus, error)) (*Manager, JobRepository, func()) {
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

func createTestJob(t *testing.T, repo JobRepository, id string, status JobStatus, engineID string) {
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
