package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downloader/internal/app"
	"downloader/internal/config"
	"downloader/internal/job"
)

func TestResolveDesktopDataRoot(t *testing.T) {
	root, err := ResolveDesktopDataRoot()
	if err != nil {
		t.Fatalf("ResolveDesktopDataRoot failed: %v", err)
	}
	if root == "" {
		t.Fatal("expected non-empty data root")
	}

	appData := os.Getenv("APPDATA")
	if appData != "" && !strings.HasPrefix(root, appData) {
		t.Fatalf("expected data root to start with APPDATA %q, got %q", appData, root)
	}

	// Verify required directories were created
	dirs := []string{
		root,
		filepath.Join(root, "data"),
		filepath.Join(root, "data", "torrents"),
		filepath.Join(root, "downloads"),
	}
	for _, dir := range dirs {
		stat, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("expected directory %s to exist: %v", dir, err)
		}
		if !stat.IsDir() {
			t.Fatalf("expected %s to be a directory", dir)
		}
	}
}

func TestDesktopService_Basics(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(tmpDir, "downloads")

	appInstance, err := app.New(context.Background(), cfg, app.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("failed to create app: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := appInstance.Start(ctx); err != nil {
		t.Fatalf("failed to start app: %v", err)
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = appInstance.Shutdown(shutdownCtx)
	}()

	svc := NewDesktopService(appInstance, tmpDir)

	// Test Instance ID
	instID := svc.GetBackendInstanceID()
	if !strings.HasPrefix(instID, "core-") {
		t.Fatalf("expected instance ID prefix core-, got %s", instID)
	}

	// Test DataRootInfo
	info, err := svc.GetDataRootInfo()
	if err != nil {
		t.Fatalf("GetDataRootInfo failed: %v", err)
	}
	if info.ResolvedDataRoot != tmpDir {
		t.Fatalf("expected resolved data root %s, got %s", tmpDir, info.ResolvedDataRoot)
	}

	// Test SingleInstanceStatus
	status, err := svc.GetSingleInstanceStatus()
	if err != nil {
		t.Fatalf("GetSingleInstanceStatus failed: %v", err)
	}
	if status.PrimaryPID != os.Getpid() {
		t.Fatalf("expected PID %d, got %d", os.Getpid(), status.PrimaryPID)
	}
	if status.CoreInitCount != 1 {
		t.Fatalf("expected CoreInitCount 1, got %d", status.CoreInitCount)
	}

	// Test recordSecondLaunch
	svc.recordSecondLaunch([]string{"--arg1", "magnet:?xt=urn:btih:test"}, tmpDir)
	statusAfter, _ := svc.GetSingleInstanceStatus()
	if statusAfter.SecondLaunchCount != 1 {
		t.Fatalf("expected SecondLaunchCount 1, got %d", statusAfter.SecondLaunchCount)
	}
	if len(statusAfter.LastForwardedArgs) != 2 || statusAfter.LastForwardedArgs[0] != "--arg1" {
		t.Fatalf("expected forwarded args [--arg1 ...], got %v", statusAfter.LastForwardedArgs)
	}

	// Test Jobs: GetJobs returns empty slice
	jobs, err := svc.GetJobs()
	if err != nil {
		t.Fatalf("GetJobs failed: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}

	// Test Settings: GetSettings returns settings
	st, err := svc.GetSettings()
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if st == nil {
		t.Fatal("expected non-nil settings")
	}

	// Test Queue: GetQueueSnapshot returns snapshot
	queue, err := svc.GetQueueSnapshot()
	if err != nil {
		t.Fatalf("GetQueueSnapshot failed: %v", err)
	}
	if queue == nil {
		t.Fatal("expected non-nil queue snapshot")
	}

	// Test Categories: GetCategories returns default categories
	cats, err := svc.GetCategories()
	if err != nil {
		t.Fatalf("GetCategories failed: %v", err)
	}
	if cats == nil {
		t.Fatal("expected non-nil categories slice")
	}

	// Test Tracker: GetTrackerSources returns slice
	sources, err := svc.GetTrackerSources()
	if err != nil {
		t.Fatalf("GetTrackerSources failed: %v", err)
	}
	if sources == nil {
		t.Fatal("expected non-nil tracker sources")
	}

	// Test MediaAuth: GetMediaAuthSettings returns settings
	mediaAuth, err := svc.GetMediaAuthSettings()
	if err != nil {
		t.Fatalf("GetMediaAuthSettings failed: %v", err)
	}
	if mediaAuth == nil {
		t.Fatal("expected non-nil media auth settings")
	}

	// Test Sync: GetSyncSnapshot returns sync snapshot
	syncSnap, err := svc.GetSyncSnapshot()
	if err != nil {
		t.Fatalf("GetSyncSnapshot failed: %v", err)
	}
	if syncSnap == nil {
		t.Fatal("expected non-nil sync snapshot")
	}

	// Test GetEventsAfter
	eventsRes, err := svc.GetEventsAfter(syncSnap.Cursor)
	if err != nil {
		t.Fatalf("GetEventsAfter failed: %v", err)
	}
	if eventsRes.GapDetected {
		t.Fatal("expected no gap when cursor matches currentCursor")
	}
	if len(eventsRes.Events) != 0 {
		t.Fatalf("expected 0 replayed events, got %d", len(eventsRes.Events))
	}

	// Test GetEventsAfter with future cursor -> gap detected
	futureRes, err := svc.GetEventsAfter(syncSnap.Cursor + 100)
	if err != nil {
		t.Fatalf("GetEventsAfter failed: %v", err)
	}
	if !futureRes.GapDetected {
		t.Fatal("expected gap detected for future cursor")
	}

	// Test status file persistence
	statusFile := filepath.Join(tmpDir, "single_instance_status.json")
	if _, err := os.Stat(statusFile); err != nil {
		t.Fatalf("expected single_instance_status.json to exist: %v", err)
	}

	// Test Capabilities
	caps := svc.GetCapabilities()
	if caps == nil || caps["profiles"] == nil {
		t.Fatal("expected non-nil capability profiles")
	}
}

func TestDesktopService_ErrorFormatting(t *testing.T) {
	appErr := &job.AppError{
		Code:    job.ErrJobNotFound,
		Message: "job does not exist",
	}
	ipcErr := toIPCError(appErr)
	if ipcErr == nil || ipcErr.Error() != "[JOB_NOT_FOUND] job does not exist" {
		t.Fatalf("expected [JOB_NOT_FOUND] job does not exist, got %v", ipcErr)
	}

	rawErr := os.ErrNotExist
	rawIpcErr := toIPCError(rawErr)
	if !strings.HasPrefix(rawIpcErr.Error(), "[INTERNAL_ERROR]") {
		t.Fatalf("expected [INTERNAL_ERROR] prefix, got %v", rawIpcErr)
	}

	madeErr := makeIPCError(job.ErrInvalidRequest, "missing parameter")
	if madeErr.Error() != "[INVALID_REQUEST] missing parameter" {
		t.Fatalf("expected [INVALID_REQUEST] missing parameter, got %v", madeErr)
	}
}
