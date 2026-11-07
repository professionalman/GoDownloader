package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"downloader/internal/job"
	"downloader/internal/toolmanager"
)

// TestApp_YtdlpUnavailable_DeterministicVerification proves that:
// When no managed yt-dlp exists, no valid configured binary exists, and yt-dlp is not on PATH:
// 1. ToolManager.Resolve reports yt-dlp unavailable.
// 2. No usable ytdlp engine is registered in App.registry.
// 3. Known media URLs return typed error YT_DLP_UNAVAILABLE (ErrYtDlpUnavailable).
// 4. Zero jobs and zero aria2 transfers are persisted or queued.
func TestApp_YtdlpUnavailable_DeterministicVerification(t *testing.T) {
	tempDir := t.TempDir()
	emptyPathDir := filepath.Join(tempDir, "empty_path")
	if err := os.MkdirAll(emptyPathDir, 0755); err != nil {
		t.Fatalf("failed to create empty path dir: %v", err)
	}

	// Restrict PATH so exec.LookPath cannot find system yt-dlp
	t.Setenv("PATH", emptyPathDir)

	cfg := newTestConfig(t)
	// Explicitly configure a non-existent binary path (must not produce a false-positive)
	cfg.YtdlpPath = filepath.Join(tempDir, "nonexistent-ytdlp.exe")

	dbPath := filepath.Join(tempDir, "unavailable_test.db")
	ctx := context.Background()

	app, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("App.New failed: %v", err)
	}
	defer func() {
		_ = app.Shutdown(ctx)
	}()

	// 1. ToolManager must report yt-dlp as unavailable
	toolInfo, err := app.ToolManager().Resolve(ctx, toolmanager.ToolYtdlp)
	if err == nil && toolInfo != nil && toolInfo.Available() {
		t.Fatalf("expected ToolManager to report yt-dlp unavailable, got available=true (path=%s)", toolInfo.ExecutablePath)
	}

	// 2. Engine registry must NOT have "ytdlp" registered
	if eng, ok := app.Registry().Get("ytdlp"); ok && eng != nil {
		t.Fatalf("expected ytdlp engine NOT to be registered in app registry, but found: %+v", eng)
	}

	// 3. Submitting a known media URL must return ErrYtDlpUnavailable
	knownMediaURL := "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	createdJob, createErr := app.Manager().Create(ctx, knownMediaURL)
	if createErr == nil {
		t.Fatalf("expected Create to fail for media URL when yt-dlp unavailable, but created job: %+v", createdJob)
	}

	appErr, ok := createErr.(*job.AppError)
	if !ok {
		t.Fatalf("expected *job.AppError, got: %T (%v)", createErr, createErr)
	}
	if appErr.Code != job.ErrYtDlpUnavailable {
		t.Fatalf("expected error code %q (ErrYtDlpUnavailable), got %q (%v)", job.ErrYtDlpUnavailable, appErr.Code, appErr.Message)
	}

	// 4. Zero jobs must be persisted in the repository (no media job, no aria2 fallback)
	jobs, listErr := app.Repo().List(ctx)
	if listErr != nil {
		t.Fatalf("Repo.List failed: %v", listErr)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs persisted on refusal, found %d: %+v", len(jobs), jobs)
	}
}
