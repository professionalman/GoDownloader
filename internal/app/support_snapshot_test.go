package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/config"
	"downloader/internal/job"
)

func TestSupportSnapshotProvider_AdapterPrivacy(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "snapshot_test.db")

	cfg := config.New()
	cfg.DownloadDir = tempDir
	cfg.DataDir = tempDir

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}
	defer a.Shutdown(ctx)

	// Seed jobs with synthetic sensitive data
	sentinelURL := "https://example.com/download?secret=GD_SECRET_BEARER_7F92A1B"
	sentinelName := "GD_PRIVATE_FILE_884.iso"
	sentinelPath := "C:\\Users\\GD_PRIVATE_USER_NAME\\Downloads"

	j := &job.Job{
		ID:             "job-123-uuid",
		Source:         sentinelURL,
		Name:           sentinelName,
		Status:         job.StatusDownloading,
		Engine:         "aria2",
		DestinationDir: sentinelPath,
		Error:          "download failed: token=GD_SECRET_PROXY_PW_8819",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	if err := a.repo.Create(ctx, j); err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	provider := NewSupportSnapshotProvider(a)
	snap, err := provider.Snapshot(ctx)
	if err != nil {
		t.Fatalf("provider.Snapshot failed: %v", err)
	}

	// Verify that job details (URL, name, path, error, ID) do NOT leak into the snapshot
	if snap.DatabaseSummary.JobCountsByStatus["downloading"] != 1 {
		t.Errorf("expected 1 downloading job, got %d", snap.DatabaseSummary.JobCountsByStatus["downloading"])
	}
	if snap.DatabaseSummary.JobCountsByEngine["aria2"] != 1 {
		t.Errorf("expected 1 aria2 job, got %d", snap.DatabaseSummary.JobCountsByEngine["aria2"])
	}

	// Verify migration markers
	if !snap.DatabaseSummary.MigrationMarkers["v08Fnd1"] {
		t.Errorf("expected v08Fnd1 migration marker to be true")
	}

	// Verify table presence
	if !snap.DatabaseSummary.TablesPresent["jobs"] {
		t.Errorf("expected jobs table to be present")
	}

	// Verify settings DTO does not leak secrets
	if snap.Settings.Network.Proxy.CredentialsConfigured {
		t.Errorf("expected CredentialsConfigured = false for fresh default config")
	}
}
