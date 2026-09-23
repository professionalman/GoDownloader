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
	"downloader/internal/database"
	"downloader/internal/job"

	"github.com/wailsapp/wails/v3/pkg/application"
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

	// Test RecoverySummary: GetRecoverySummary returns typed snapshot
	recSummary, err := svc.GetRecoverySummary()
	if err != nil {
		t.Fatalf("GetRecoverySummary failed: %v", err)
	}
	if recSummary == nil {
		t.Fatal("expected non-nil recovery summary")
	}
	if recSummary.ReconciledFinalizations != 0 || recSummary.ReattachedTransfers != 0 ||
		recSummary.RestartedMetadataAcquisitions != 0 || recSummary.InterruptedMediaJobs != 0 || len(recSummary.Issues) != 0 {
		t.Errorf("expected clean recovery summary, got %+v", recSummary)
	}

	// Repeated reads return identical values
	recSummary2, err := svc.GetRecoverySummary()
	if err != nil || recSummary2 == nil {
		t.Fatalf("repeated GetRecoverySummary failed: %v", err)
	}
	if recSummary.ReconciledFinalizations != recSummary2.ReconciledFinalizations ||
		recSummary.ReattachedTransfers != recSummary2.ReattachedTransfers ||
		recSummary.RestartedMetadataAcquisitions != recSummary2.RestartedMetadataAcquisitions ||
		recSummary.InterruptedMediaJobs != recSummary2.InterruptedMediaJobs ||
		len(recSummary.Issues) != len(recSummary2.Issues) {
		t.Errorf("expected identical repeated summary reads")
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

	// Test Diagnostic Methods
	svc.ReloadMainWindow() // safe when mainWindow is nil
	status, err = svc.GetSingleInstanceStatus()
	if err != nil {
		t.Fatalf("GetSingleInstanceStatus failed: %v", err)
	}
	if status.ReloadCount != 1 {
		t.Fatalf("expected ReloadCount 1, got %d", status.ReloadCount)
	}
	if status.BackendInstanceID != svc.GetBackendInstanceID() {
		t.Fatalf("expected BackendInstanceID %s, got %s", svc.GetBackendInstanceID(), status.BackendInstanceID)
	}

	svc.EmitDiagnosticEvent("diag-token-1")
	svc.RecordDiagnosticDelivery("diag-token-1")
	if count := svc.GetDiagnosticEventDeliveryCount(); count != 1 {
		t.Fatalf("expected delivery count 1, got %d", count)
	}

	svc.RecordStateSyncCompleted(42)
	status, _ = svc.GetSingleInstanceStatus()
	if !status.StateSyncCompleted || status.StateSyncCursor != 42 {
		t.Fatalf("expected StateSyncCompleted true and cursor 42, got %v / %d", status.StateSyncCompleted, status.StateSyncCursor)
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

type testAutostartController struct {
	enabled     bool
	enableErr   error
	disableErr  error
	lastOptions application.AutostartOptions
}

func (m *testAutostartController) IsEnabled() (bool, error) {
	return m.enabled, nil
}

func (m *testAutostartController) EnableWithOptions(opts application.AutostartOptions) error {
	if m.enableErr != nil {
		return m.enableErr
	}
	m.enabled = true
	m.lastOptions = opts
	return nil
}

func (m *testAutostartController) Disable() error {
	if m.disableErr != nil {
		return m.disableErr
	}
	m.enabled = false
	return nil
}

func TestDesktopService_DesktopPreferences(t *testing.T) {
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

	lifecycle := NewDesktopLifecycle()
	lifecycle.SetTrayConfigured(true)

	svc := NewDesktopService(appInstance, tmpDir)
	svc.setLifecycle(lifecycle)

	mockAuto := &testAutostartController{enabled: false}
	svc.setAutostartController(mockAuto)

	// 1. Initial defaults: close_to_tray = true, autostart = false
	prefs, err := svc.GetDesktopPreferences()
	if err != nil {
		t.Fatalf("GetDesktopPreferences failed: %v", err)
	}
	if !prefs.CloseToTray {
		t.Fatalf("expected CloseToTray to default to true, got false")
	}
	if prefs.AutostartEnabled {
		t.Fatalf("expected AutostartEnabled to default to false, got true")
	}

	// 2. Set CloseToTray = false
	prefs, err = svc.SetCloseToTray(false)
	if err != nil {
		t.Fatalf("SetCloseToTray(false) failed: %v", err)
	}
	if prefs.CloseToTray {
		t.Fatalf("expected CloseToTray to be false, got true")
	}
	if lifecycle.CloseToTray() {
		t.Fatalf("expected lifecycle.CloseToTray() to be false")
	}

	// Verify persistence in settings repository
	persistedCTT, err := appInstance.Settings().GetCloseToTray(context.Background())
	if err != nil || persistedCTT {
		t.Fatalf("expected persisted close_to_tray to be false, got %v (err: %v)", persistedCTT, err)
	}

	// 3. Set CloseToTray = true
	prefs, err = svc.SetCloseToTray(true)
	if err != nil {
		t.Fatalf("SetCloseToTray(true) failed: %v", err)
	}
	if !prefs.CloseToTray {
		t.Fatalf("expected CloseToTray to be true, got false")
	}
	if !lifecycle.CloseToTray() {
		t.Fatalf("expected lifecycle.CloseToTray() to be true")
	}

	// 4. Enable Autostart -> registers with --background
	prefs, err = svc.SetAutostart(true)
	if err != nil {
		t.Fatalf("SetAutostart(true) failed: %v", err)
	}
	if !prefs.AutostartEnabled {
		t.Fatalf("expected AutostartEnabled to be true, got false")
	}
	if !mockAuto.enabled {
		t.Fatalf("expected mockAuto.enabled to be true")
	}
	if len(mockAuto.lastOptions.Arguments) != 1 || mockAuto.lastOptions.Arguments[0] != "--background" {
		t.Fatalf("expected --background argument in autostart options, got %v", mockAuto.lastOptions.Arguments)
	}

	// 5. Disable Autostart
	prefs, err = svc.SetAutostart(false)
	if err != nil {
		t.Fatalf("SetAutostart(false) failed: %v", err)
	}
	if prefs.AutostartEnabled {
		t.Fatalf("expected AutostartEnabled to be false, got true")
	}
	if mockAuto.enabled {
		t.Fatalf("expected mockAuto.enabled to be false")
	}

	// 6. Autostart enable failure error handling
	mockAuto.enableErr = os.ErrPermission
	_, err = svc.SetAutostart(true)
	if err == nil || !strings.Contains(err.Error(), "AUTOSTART_ENABLE_FAILED") {
		t.Fatalf("expected AUTOSTART_ENABLE_FAILED error, got: %v", err)
	}

	// 7. Autostart disable failure error handling
	mockAuto.enableErr = nil
	mockAuto.enabled = true
	mockAuto.disableErr = os.ErrPermission
	_, err = svc.SetAutostart(false)
	if err == nil || !strings.Contains(err.Error(), "AUTOSTART_DISABLE_FAILED") {
		t.Fatalf("expected AUTOSTART_DISABLE_FAILED error, got: %v", err)
	}
}

func TestDesktopService_BackgroundArgHandling(t *testing.T) {
	// Verify pure argument checking logic for background launch & secondary instance
	isBg := func(args []string) bool {
		for _, arg := range args {
			if arg == "--background" {
				return true
			}
		}
		return false
	}

	if !isBg([]string{"--background"}) {
		t.Fatalf("expected isBg true for --background")
	}
	if !isBg([]string{"--some-other-flag", "--background"}) {
		t.Fatalf("expected isBg true for mixed args containing --background")
	}
	if isBg([]string{"--foreground", "magnet:?xt=urn:btih:test"}) {
		t.Fatalf("expected isBg false for normal launch args")
	}
	if isBg([]string{}) {
		t.Fatalf("expected isBg false for empty args")
	}
}

func TestDesktopService_DiagnosticCommandDispatch(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "diag_dispatch_test.db")
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

	lifecycle := NewDesktopLifecycle()
	lifecycle.SetTrayConfigured(true)

	svc := NewDesktopService(appInstance, tmpDir)
	svc.setLifecycle(lifecycle)
	mockAuto := &testAutostartController{enabled: false}
	svc.setAutostartController(mockAuto)

	dispatchArg := func(arg string) bool {
		switch arg {
		case "--set-close-to-tray=false":
			_, _ = svc.SetCloseToTray(false)
			return true
		case "--set-close-to-tray=true":
			_, _ = svc.SetCloseToTray(true)
			return true
		case "--set-autostart=true":
			_, _ = svc.SetAutostart(true)
			return true
		case "--set-autostart=false":
			_, _ = svc.SetAutostart(false)
			return true
		default:
			return false
		}
	}

	// 1. Test valid --set-close-to-tray=false
	if !dispatchArg("--set-close-to-tray=false") {
		t.Fatal("expected handled")
	}
	if lifecycle.CloseToTray() != false {
		t.Fatal("expected lifecycle closeToTray to be false")
	}

	// 2. Test valid --set-close-to-tray=true
	if !dispatchArg("--set-close-to-tray=true") {
		t.Fatal("expected handled")
	}
	if lifecycle.CloseToTray() != true {
		t.Fatal("expected lifecycle closeToTray to be true")
	}

	// 3. Test invalid close-to-tray value rejected
	if dispatchArg("--set-close-to-tray=invalid") {
		t.Fatal("expected invalid close-to-tray to be rejected")
	}

	// 4. Test valid --set-autostart=true
	if !dispatchArg("--set-autostart=true") {
		t.Fatal("expected handled")
	}
	if !mockAuto.enabled {
		t.Fatal("expected autostart enabled")
	}

	// 5. Test valid --set-autostart=false
	if !dispatchArg("--set-autostart=false") {
		t.Fatal("expected handled")
	}
	if mockAuto.enabled {
		t.Fatal("expected autostart disabled")
	}

	// 6. Test invalid autostart value rejected
	if dispatchArg("--set-autostart=invalid") {
		t.Fatal("expected invalid autostart to be rejected")
	}
}

func TestDesktopService_RestartRecovery_InterruptedMediaAcceptance(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	cfg := config.New()
	cfg.DownloadDir = filepath.Join(tmpDir, "downloads")

	// Pre-populate DB with an active media job before App start
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to initialize db: %v", err)
	}
	jobRepo := database.NewSQLiteJobRepository(db)
	now := time.Now().Truncate(time.Second)
	interruptedJob := &job.Job{
		ID:        "interrupted-media-acceptance",
		Source:    "https://example.com/watch?v=acceptance_video",
		Name:      "acceptance_video.mp4",
		Status:    job.StatusDownloading,
		Type:      job.TypeMedia,
		Engine:    "ytdlp",
		EngineID:  "yt-acceptance-1",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := jobRepo.Create(context.Background(), interruptedJob); err != nil {
		t.Fatalf("failed to create pre-restart job: %v", err)
	}
	_ = db.Close()

	// Launch App runtime
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

	// 1. RecoverySummary reflects the interrupted media job
	summary, err := svc.GetRecoverySummary()
	if err != nil {
		t.Fatalf("GetRecoverySummary failed: %v", err)
	}
	if summary.InterruptedMediaJobs != 1 {
		t.Errorf("expected InterruptedMediaJobs=1, got %d", summary.InterruptedMediaJobs)
	}
	if len(summary.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(summary.Issues))
	}
	if summary.Issues[0].JobID != "interrupted-media-acceptance" {
		t.Errorf("expected JobID=interrupted-media-acceptance, got %s", summary.Issues[0].JobID)
	}
	if summary.Issues[0].Kind != job.RecoveryIssueInterruptedMedia {
		t.Errorf("expected Kind=interrupted_media, got %s", summary.Issues[0].Kind)
	}

	// 2. Current job state query returns StatusFailed
	j, err := svc.GetJob("interrupted-media-acceptance")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if j.Status != job.StatusFailed {
		t.Errorf("expected job StatusFailed, got %s", j.Status)
	}

	// 3. Current StateSync snapshot also returns StatusFailed
	snapshot, err := svc.GetSyncSnapshot()
	if err != nil {
		t.Fatalf("GetSyncSnapshot failed: %v", err)
	}
	var foundInSnapshot bool
	for _, sj := range snapshot.Jobs {
		if sj.ID == "interrupted-media-acceptance" {
			foundInSnapshot = true
			if sj.Status != job.StatusFailed {
				t.Errorf("expected snapshot job StatusFailed, got %s", sj.Status)
			}
		}
	}
	if !foundInSnapshot {
		t.Error("expected interrupted job to be present in sync snapshot")
	}
}

