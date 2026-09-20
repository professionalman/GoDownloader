package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"downloader/internal/app"
	"downloader/internal/job"
	"downloader/internal/mediaauth"
	"downloader/internal/networkpolicy"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// autostartController abstracts login startup management for testability and runtime isolation.
type autostartController interface {
	IsEnabled() (bool, error)
	EnableWithOptions(opts application.AutostartOptions) error
	Disable() error
}

// DesktopPreferences represents user-configurable desktop lifecycle preferences.
type DesktopPreferences struct {
	CloseToTray      bool `json:"closeToTray"`
	AutostartEnabled bool `json:"autostartEnabled"`
}

// SingleInstanceStatus holds status information about the primary instance and secondary launches.
type SingleInstanceStatus struct {
	PrimaryPID         int      `json:"primaryPid"`
	BackendInstanceID  string   `json:"backendInstanceId"`
	CoreInitCount      int      `json:"coreInitCount"`
	SecondLaunchCount  int      `json:"secondLaunchCount"`
	LastForwardedArgs  []string `json:"lastForwardedArgs"`
	LastForwardedCwd   string   `json:"lastForwardedCwd"`
	ReloadCount        int      `json:"reloadCount"`
	DeliveryCount      int      `json:"deliveryCount"`
	LastEventToken     string   `json:"lastEventToken"`
	StateSyncCompleted bool     `json:"stateSyncCompleted"`
	StateSyncCursor    int64    `json:"stateSyncCursor"`
	TrayShowCount      int      `json:"trayShowCount"`
	TrayQuitCount      int      `json:"trayQuitCount"`
	CloseToTray        bool     `json:"closeToTray"`
	AutostartEnabled   bool     `json:"autostartEnabled"`
}

// EventsReplayResult contains events replayed since a requested cursor.
type EventsReplayResult struct {
	Events        []job.Event `json:"events"`
	CurrentCursor int64       `json:"currentCursor"`
	GapDetected   bool        `json:"gapDetected"`
}

// TrackersResult carries tracker results for addTorrentTrackers.
type TrackersResult struct {
	Trackers []networkpolicy.Tracker `json:"trackers"`
}

// TrackerRefreshSummary carries summary info for tracker refresh operations.
type TrackerRefreshSummary struct {
	FailureCount int `json:"failureCount"`
}

// DesktopService exposes the unified GoDownloader application runtime
// to the embedded desktop frontend via Wails v3 in-process IPC.
type DesktopService struct {
	mu                 sync.Mutex
	app                *app.App
	wailsApp           *application.App
	mainWindow         *application.WebviewWindow
	lifecycle          *DesktopLifecycle
	autostart          autostartController
	instanceID         string
	dataRoot           string
	coreInitCount      int
	secondLaunchCount  int
	lastForwardedArgs  []string
	lastForwardedCwd   string
	reloadCount        int
	deliveryCount      int
	lastEventToken     string
	stateSyncCompleted bool
	stateSyncCursor    int64
	trayShowCount      int
	trayQuitCount      int
}

// NewDesktopService constructs a DesktopService wrapping the primary App instance.
func NewDesktopService(appInstance *app.App, dataRoot string) *DesktopService {
	instID := fmt.Sprintf("core-%d-%04x", time.Now().UnixMilli(), time.Now().Nanosecond()%0xffff)
	svc := &DesktopService{
		app:           appInstance,
		instanceID:    instID,
		dataRoot:      dataRoot,
		coreInitCount: 1,
	}
	svc.persistStatus()
	return svc
}

func (s *DesktopService) persistStatus() {
	if s.dataRoot == "" {
		return
	}
	closeToTray := true
	if s.lifecycle != nil {
		closeToTray = s.lifecycle.CloseToTray()
	}
	autostart := false
	if s.autostart != nil {
		if enabled, err := s.autostart.IsEnabled(); err == nil {
			autostart = enabled
		}
	}
	status := SingleInstanceStatus{
		PrimaryPID:         os.Getpid(),
		BackendInstanceID:  s.instanceID,
		CoreInitCount:      s.coreInitCount,
		SecondLaunchCount:  s.secondLaunchCount,
		LastForwardedArgs:  s.lastForwardedArgs,
		LastForwardedCwd:   s.lastForwardedCwd,
		ReloadCount:        s.reloadCount,
		DeliveryCount:      s.deliveryCount,
		LastEventToken:     s.lastEventToken,
		StateSyncCompleted: s.stateSyncCompleted,
		StateSyncCursor:    s.stateSyncCursor,
		TrayShowCount:      s.trayShowCount,
		TrayQuitCount:      s.trayQuitCount,
		CloseToTray:        closeToTray,
		AutostartEnabled:   autostart,
	}
	data, err := json.MarshalIndent(status, "", "  ")
	if err == nil {
		_ = os.WriteFile(filepath.Join(s.dataRoot, "single_instance_status.json"), data, 0644)
	}
}

func (s *DesktopService) setWailsContext(wailsApp *application.App, win *application.WebviewWindow) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.wailsApp = wailsApp
	s.mainWindow = win
	if wailsApp != nil && wailsApp.Autostart != nil {
		s.autostart = wailsApp.Autostart
	}
}

func (s *DesktopService) setAutostartController(ac autostartController) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autostart = ac
}

func (s *DesktopService) setLifecycle(l *DesktopLifecycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycle = l
}

func (s *DesktopService) recordTrayShow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trayShowCount++
	s.persistStatus()
}

func (s *DesktopService) recordTrayQuit() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trayQuitCount++
	s.persistStatus()
}

func (s *DesktopService) recordSecondLaunch(args []string, cwd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secondLaunchCount++
	s.lastForwardedArgs = args
	s.lastForwardedCwd = cwd
	s.persistStatus()
}

func toIPCError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *job.AppError
	if errors.As(err, &appErr) {
		return fmt.Errorf("[%s] %s", appErr.Code, appErr.Message)
	}
	return fmt.Errorf("[INTERNAL_ERROR] %s", err.Error())
}

func makeIPCError(code, message string) error {
	return fmt.Errorf("[%s] %s", code, message)
}

// =========================================================================
// 1. JOBS DOMAIN
// =========================================================================

func (s *DesktopService) GetJobs() ([]job.Job, error) {
	jobs, err := s.app.Manager().List(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	if jobs == nil {
		jobs = []job.Job{}
	}
	return jobs, nil
}

func (s *DesktopService) GetJob(id string) (*job.Job, error) {
	j, err := s.app.Manager().Get(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) CreateJob(
	source string,
	priority string,
	categoryID string,
	destinationDir string,
	conflictPolicy string,
	networkPolicyJSON string,
	seedingPolicyJSON string,
	trackers []string,
) (*job.Job, error) {
	if source == "" {
		return nil, makeIPCError(job.ErrInvalidRequest, "source URL is required")
	}

	opts := job.CreateOptions{
		Priority:       job.JobPriorityNormal,
		CategoryID:     categoryID,
		DestinationDir: destinationDir,
		ConflictPolicy: job.FilenameConflictPolicy(conflictPolicy),
		Trackers:       trackers,
	}

	if priority != "" {
		opts.Priority = job.JobPriority(priority)
		if !job.ValidJobPriority(opts.Priority) {
			return nil, makeIPCError(job.ErrInvalidPriority, "invalid priority: "+priority)
		}
	}

	if networkPolicyJSON != "" {
		var np networkpolicy.JobNetworkPolicyOverride
		if err := json.Unmarshal([]byte(networkPolicyJSON), &np); err != nil {
			return nil, makeIPCError(job.ErrInvalidNetworkPolicy, "invalid networkPolicy")
		}
		opts.NetworkPolicy = &np
	}

	if seedingPolicyJSON != "" {
		var sp networkpolicy.SeedingPolicy
		if err := json.Unmarshal([]byte(seedingPolicyJSON), &sp); err != nil {
			return nil, makeIPCError(job.ErrInvalidSeedingPolicy, "invalid seedingPolicy")
		}
		opts.SeedingPolicy = &sp
	}

	j, err := s.app.Manager().CreateWithOptions(context.Background(), source, opts)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) CreateBatchJobs(inputsJSON string) (*job.CreateBatchResponse, error) {
	var req job.CreateBatchRequest
	if err := json.Unmarshal([]byte(inputsJSON), &req); err != nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "invalid batch request: "+err.Error())
	}
	resp, err := s.app.Manager().CreateBatch(context.Background(), req)
	if err != nil {
		return nil, toIPCError(err)
	}
	return resp, nil
}

func (s *DesktopService) PauseJob(id string) (*job.Job, error) {
	j, err := s.app.Manager().Pause(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) ResumeJob(id string) (*job.Job, error) {
	j, err := s.app.Manager().Resume(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) RetryJob(id string) (*job.Job, error) {
	j, err := s.app.Manager().Retry(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) CancelJob(id string) (*job.Job, error) {
	j, err := s.app.Manager().Cancel(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) DeleteJob(id string, deleteFiles bool) error {
	if err := s.app.Manager().Delete(context.Background(), id, job.DeleteJobOptions{DeleteFiles: deleteFiles}); err != nil {
		return toIPCError(err)
	}
	return nil
}

func (s *DesktopService) BulkAction(action string, jobIDs []string) (*job.BulkActionResponse, error) {
	resp, err := s.app.Manager().BulkAction(context.Background(), job.BulkActionRequest{
		Action: action,
		JobIDs: jobIDs,
	})
	if err != nil {
		return nil, toIPCError(err)
	}
	return resp, nil
}

func (s *DesktopService) SetJobPriority(id string, priority string) (*job.Job, error) {
	p := job.JobPriority(priority)
	if !job.ValidJobPriority(p) {
		return nil, makeIPCError(job.ErrInvalidPriority, "invalid priority: "+priority)
	}
	j, err := s.app.Manager().SetJobPriority(context.Background(), id, p)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) SelectFormat(id string, formatID string, subtitleOptionsJSON string) (*job.Job, error) {
	var subOpts *job.SubtitleOptions
	if subtitleOptionsJSON != "" {
		var parsed job.SubtitleOptions
		if err := json.Unmarshal([]byte(subtitleOptionsJSON), &parsed); err == nil {
			subOpts = &parsed
		}
	}
	j, err := s.app.Manager().SelectFormat(context.Background(), id, formatID, subOpts)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) UploadTorrent(
	filename string,
	base64Data string,
	priority string,
	categoryID string,
	destinationDir string,
	networkPolicyJSON string,
	seedingPolicyJSON string,
	trackers []string,
) (*job.Job, error) {
	safeFilename := filepath.Base(filename)
	if !strings.HasSuffix(strings.ToLower(safeFilename), ".torrent") {
		return nil, makeIPCError(job.ErrInvalidTorrentFile, "file must have .torrent extension")
	}

	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return nil, makeIPCError(job.ErrInvalidTorrentFile, "failed to decode base64 torrent data")
	}
	if len(data) == 0 {
		return nil, makeIPCError(job.ErrInvalidTorrentFile, "torrent file is empty")
	}
	if data[0] != 'd' {
		return nil, makeIPCError(job.ErrInvalidTorrentFile, "invalid torrent file format: missing bencoded dictionary")
	}

	tmpFile, err := os.CreateTemp("", "godownloader-*.torrent")
	if err != nil {
		return nil, makeIPCError(job.ErrInternalError, "failed to save torrent file")
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, makeIPCError(job.ErrInternalError, "failed to save torrent file")
	}
	tmpFile.Close()

	p := job.JobPriorityNormal
	if priority != "" {
		p = job.JobPriority(priority)
		if !job.ValidJobPriority(p) {
			return nil, makeIPCError(job.ErrInvalidPriority, "invalid priority: "+priority)
		}
	}

	opts := job.CreateOptions{
		Priority:       p,
		CategoryID:     categoryID,
		DestinationDir: destinationDir,
		ConflictPolicy: job.ConflictPolicyEngineManaged,
		Trackers:       trackers,
	}

	if networkPolicyJSON != "" {
		var np networkpolicy.JobNetworkPolicyOverride
		if err := json.Unmarshal([]byte(networkPolicyJSON), &np); err == nil {
			opts.NetworkPolicy = &np
		}
	}

	if seedingPolicyJSON != "" {
		var sp networkpolicy.SeedingPolicy
		if err := json.Unmarshal([]byte(seedingPolicyJSON), &sp); err == nil {
			opts.SeedingPolicy = &sp
		}
	}

	j, err := s.app.Manager().CreateTorrentFromFileWithOptions(context.Background(), tmpPath, opts)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) GetTorrentFiles(id string) ([]job.TorrentFile, error) {
	files, err := s.app.Manager().GetTorrentFiles(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return files, nil
}

func (s *DesktopService) StartTorrent(id string, filesJSON string, seedingPolicyJSON string) (*job.Job, error) {
	var files []job.TorrentFileSelection
	if err := json.Unmarshal([]byte(filesJSON), &files); err != nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "invalid files JSON")
	}
	if len(files) == 0 {
		return nil, makeIPCError(job.ErrInvalidRequest, "file selections are required")
	}

	policy := networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone}
	if seedingPolicyJSON != "" {
		_ = json.Unmarshal([]byte(seedingPolicyJSON), &policy)
	}

	j, err := s.app.Manager().StartTorrentWithPolicy(context.Background(), id, files, policy)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) StopSeeding(id string) (*job.Job, error) {
	j, err := s.app.Manager().StopSeeding(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) GetCapabilities() map[string]any {
	return map[string]any{"profiles": s.app.Manager().CapabilityProfiles()}
}

func (s *DesktopService) ResolveCapabilities(sourceJSON string) (networkpolicy.JobCapabilities, error) {
	var sources []string
	if err := json.Unmarshal([]byte(sourceJSON), &sources); err != nil {
		var single string
		if strErr := json.Unmarshal([]byte(sourceJSON), &single); strErr != nil || single == "" {
			return networkpolicy.JobCapabilities{}, makeIPCError(job.ErrInvalidRequest, "source must be a string or string array")
		}
		sources = []string{single}
	}
	if len(sources) == 0 {
		return networkpolicy.JobCapabilities{}, makeIPCError(job.ErrInvalidRequest, "at least one source is required")
	}
	return s.app.Manager().ResolveCapabilities(sources), nil
}

func (s *DesktopService) GetJobCapabilities(id string) (networkpolicy.JobCapabilities, error) {
	caps, err := s.app.Manager().JobCapabilities(context.Background(), id)
	if err != nil {
		return networkpolicy.JobCapabilities{}, toIPCError(err)
	}
	return caps, nil
}

func (s *DesktopService) UpdateJobNetwork(id string, limitsJSON string) (*job.Job, error) {
	var req job.NetworkLimitUpdate
	if err := json.Unmarshal([]byte(limitsJSON), &req); err != nil {
		return nil, makeIPCError(job.ErrInvalidNetworkPolicy, "invalid network limits JSON")
	}
	j, err := s.app.Manager().UpdateNetworkLimits(context.Background(), id, req)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) AddTorrentTrackers(id string, trackers []string) (*TrackersResult, error) {
	res, err := s.app.Manager().AddTorrentTrackers(context.Background(), id, trackers)
	if err != nil {
		return nil, toIPCError(err)
	}
	return &TrackersResult{Trackers: res}, nil
}

func (s *DesktopService) UpdateSeedingPolicy(id string, policyJSON string) (*job.Job, error) {
	var policy networkpolicy.SeedingPolicy
	if err := json.Unmarshal([]byte(policyJSON), &policy); err != nil {
		return nil, makeIPCError(job.ErrInvalidSeedingPolicy, "invalid seeding policy")
	}
	j, err := s.app.Manager().UpdateSeedingPolicy(context.Background(), id, policy)
	if err != nil {
		return nil, toIPCError(err)
	}
	return j, nil
}

func (s *DesktopService) OpenFolder() error {
	defaultDir := s.app.Manager().GetEffectiveDefaultDownloadDir(context.Background())
	if defaultDir == "" {
		defaultDir = filepath.Join(s.dataRoot, "downloads")
	}
	_ = os.MkdirAll(defaultDir, 0755)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer", defaultDir)
	case "darwin":
		cmd = exec.Command("open", defaultDir)
	default:
		cmd = exec.Command("xdg-open", defaultDir)
	}
	return cmd.Start()
}

// =========================================================================
// 2. QUEUE DOMAIN
// =========================================================================

func (s *DesktopService) GetQueueSnapshot() (*job.QueueSnapshot, error) {
	snap, err := s.app.Manager().GetQueueSnapshot(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return snap, nil
}

func (s *DesktopService) ReorderQueue(priority string, jobIDs []string) error {
	p := job.JobPriority(priority)
	if !job.ValidJobPriority(p) {
		return makeIPCError(job.ErrInvalidPriority, "invalid priority: "+priority)
	}
	if err := s.app.Manager().ReorderQueue(context.Background(), p, jobIDs); err != nil {
		return toIPCError(err)
	}
	return nil
}

// =========================================================================
// 3. SETTINGS DOMAIN
// =========================================================================

func (s *DesktopService) GetSettings() (*settings.AppSettings, error) {
	st, err := s.app.Settings().GetSettings(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return st, nil
}

func (s *DesktopService) UpdateSettings(settingsJSON string) (*settings.AppSettings, error) {
	ctx := context.Background()
	var req settings.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(settingsJSON), &req); err != nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "invalid settings JSON")
	}

	if err := s.app.Settings().ValidateUpdate(ctx, &req); err != nil {
		if errors.Is(err, securestore.ErrUnavailable) {
			return nil, makeIPCError(job.ErrSecretStorageUnavailable, err.Error())
		}
		return nil, makeIPCError(job.ErrInvalidRequest, err.Error())
	}

	var st *settings.AppSettings
	var err error

	if req.Queue != nil {
		st, err = s.app.Settings().UpdateQueueSettings(ctx, req.Queue.MaxConcurrentDownloads)
		if err != nil {
			return nil, makeIPCError(job.ErrInvalidRequest, err.Error())
		}
	}

	if req.Storage != nil {
		st, err = s.app.Settings().UpdateStorageSettings(ctx, &req)
		if err != nil {
			return nil, makeIPCError(job.ErrInvalidRequest, err.Error())
		}
	}

	if req.Network != nil || req.Torrent != nil {
		st, err = s.app.Settings().UpdatePowerSettings(ctx, &req)
		if err != nil {
			if errors.Is(err, securestore.ErrUnavailable) {
				return nil, makeIPCError(job.ErrSecretStorageUnavailable, err.Error())
			}
			return nil, makeIPCError(job.ErrInvalidNetworkPolicy, err.Error())
		}
		if s.app.Manager() != nil {
			st.ApplicationResults = append(st.ApplicationResults, s.app.Manager().ReconcileNetworkPoliciesWithResults(ctx)...)
		}
	}

	if st == nil {
		st, err = s.app.Settings().GetSettings(ctx)
		if err != nil {
			return nil, toIPCError(err)
		}
	}

	if m := s.app.Manager(); m != nil {
		if sc := m.GetScheduler(); sc != nil {
			sc.Kick()
		}
	}

	return st, nil
}

// =========================================================================
// DESKTOP PREFERENCES & AUTOSTART
// =========================================================================

// GetDesktopPreferences returns the current desktop lifecycle preferences.
// close_to_tray is read from the settings repository (defaulting to true).
// autostartEnabled is queried directly from the Windows Registry via Wails AutostartManager.
func (s *DesktopService) GetDesktopPreferences() (*DesktopPreferences, error) {
	s.mu.Lock()
	l := s.lifecycle
	ac := s.autostart
	appInst := s.app
	s.mu.Unlock()

	closeToTray := true
	if l != nil {
		closeToTray = l.CloseToTray()
	} else if appInst != nil && appInst.Settings() != nil {
		if val, err := appInst.Settings().GetCloseToTray(context.Background()); err == nil {
			closeToTray = val
		}
	}

	autostart := false
	if ac != nil {
		if enabled, err := ac.IsEnabled(); err == nil {
			autostart = enabled
		}
	}

	return &DesktopPreferences{
		CloseToTray:      closeToTray,
		AutostartEnabled: autostart,
	}, nil
}

// SetCloseToTray persists the close_to_tray preference to the settings database
// and immediately applies it to the active DesktopLifecycle coordinator.
func (s *DesktopService) SetCloseToTray(enabled bool) (*DesktopPreferences, error) {
	s.mu.Lock()
	appInst := s.app
	l := s.lifecycle
	s.mu.Unlock()

	if appInst == nil || appInst.Settings() == nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "settings service unavailable")
	}

	if err := appInst.Settings().SetCloseToTray(context.Background(), enabled); err != nil {
		return nil, toIPCError(err)
	}

	if l != nil {
		l.SetCloseToTray(enabled)
	}

	s.persistStatus()
	return s.GetDesktopPreferences()
}

// SetAutostart enables or disables Windows login startup via Wails AutostartManager.
// When enabled, it registers GoDownloader with the --background argument.
// Operational truth is queried directly from Windows to ensure no divergent state.
func (s *DesktopService) SetAutostart(enabled bool) (*DesktopPreferences, error) {
	s.mu.Lock()
	ac := s.autostart
	s.mu.Unlock()

	if ac == nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "autostart controller unavailable")
	}

	if enabled {
		if err := ac.EnableWithOptions(application.AutostartOptions{
			Arguments: []string{"--background"},
		}); err != nil {
			return nil, makeIPCError("AUTOSTART_ENABLE_FAILED", fmt.Sprintf("failed to enable autostart: %v", err))
		}
	} else {
		if err := ac.Disable(); err != nil {
			return nil, makeIPCError("AUTOSTART_DISABLE_FAILED", fmt.Sprintf("failed to disable autostart: %v", err))
		}
	}

	s.persistStatus()
	return s.GetDesktopPreferences()
}

// =========================================================================
// 4. CATEGORIES DOMAIN
// =========================================================================

func (s *DesktopService) GetCategories() ([]storage.CategoryResponse, error) {
	ctx := context.Background()
	cats, err := s.app.Categories().List(ctx)
	if err != nil {
		return nil, toIPCError(err)
	}

	defaultDir := s.app.Manager().GetEffectiveDefaultDownloadDir(ctx)
	resp := make([]storage.CategoryResponse, 0, len(cats))
	for _, c := range cats {
		resDir := c.Directory
		if !filepath.IsAbs(c.Directory) {
			resDir = filepath.Join(defaultDir, c.Directory)
		}
		resp = append(resp, storage.CategoryResponse{
			ID:                c.ID,
			Name:              c.Name,
			Directory:         c.Directory,
			ResolvedDirectory: resDir,
			CreatedAt:         c.CreatedAt,
			UpdatedAt:         c.UpdatedAt,
		})
	}
	return resp, nil
}

func (s *DesktopService) CreateCategory(name string, directory string) (*storage.CategoryResponse, error) {
	ctx := context.Background()
	cat := &storage.Category{
		Name:      name,
		Directory: directory,
	}

	if err := s.app.Categories().Create(ctx, cat); err != nil {
		return nil, makeIPCError(job.ErrCategoryNameConflict, err.Error())
	}

	defaultDir := s.app.Manager().GetEffectiveDefaultDownloadDir(ctx)
	resDir := cat.Directory
	if !filepath.IsAbs(cat.Directory) {
		resDir = filepath.Join(defaultDir, cat.Directory)
	}

	return &storage.CategoryResponse{
		ID:                cat.ID,
		Name:              cat.Name,
		Directory:         cat.Directory,
		ResolvedDirectory: resDir,
		CreatedAt:         cat.CreatedAt,
		UpdatedAt:         cat.UpdatedAt,
	}, nil
}

func (s *DesktopService) UpdateCategory(id string, name string, directory string) (*storage.CategoryResponse, error) {
	ctx := context.Background()
	if id == "" {
		return nil, makeIPCError(job.ErrInvalidRequest, "category ID is required")
	}

	existing, err := s.app.Categories().GetByID(ctx, id)
	if err != nil || existing == nil {
		return nil, makeIPCError(job.ErrCategoryNotFound, "category not found")
	}

	existing.Name = name
	existing.Directory = directory

	if err := s.app.Categories().Update(ctx, existing); err != nil {
		return nil, makeIPCError(job.ErrCategoryNameConflict, err.Error())
	}

	defaultDir := s.app.Manager().GetEffectiveDefaultDownloadDir(ctx)
	resDir := existing.Directory
	if !filepath.IsAbs(existing.Directory) {
		resDir = filepath.Join(defaultDir, existing.Directory)
	}

	return &storage.CategoryResponse{
		ID:                existing.ID,
		Name:              existing.Name,
		Directory:         existing.Directory,
		ResolvedDirectory: resDir,
		CreatedAt:         existing.CreatedAt,
		UpdatedAt:         existing.UpdatedAt,
	}, nil
}

func (s *DesktopService) DeleteCategory(id string) error {
	ctx := context.Background()
	if id == "" {
		return makeIPCError(job.ErrInvalidRequest, "category ID is required")
	}
	if err := s.app.Categories().Delete(ctx, id); err != nil {
		return makeIPCError(job.ErrCategoryNotFound, err.Error())
	}
	return nil
}

// =========================================================================
// 5. TRACKER DOMAIN
// =========================================================================

func (s *DesktopService) GetTrackerSources() ([]networkpolicy.TrackerSource, error) {
	sources, err := s.app.Tracker().List(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return sources, nil
}

func (s *DesktopService) CreateTrackerSource(name string, url string, enabled bool, refreshIntervalSeconds int) (*networkpolicy.TrackerSource, error) {
	input := networkpolicy.TrackerSourceInput{
		Name:                   name,
		URL:                    url,
		Enabled:                enabled,
		RefreshIntervalSeconds: int64(refreshIntervalSeconds),
	}
	src, err := s.app.Tracker().Create(context.Background(), input)
	if err != nil {
		return nil, toIPCError(err)
	}
	return src, nil
}

func (s *DesktopService) UpdateTrackerSource(id string, name string, url string, enabled bool, refreshIntervalSeconds int) (*networkpolicy.TrackerSource, error) {
	input := networkpolicy.TrackerSourceInput{
		Name:                   name,
		URL:                    url,
		Enabled:                enabled,
		RefreshIntervalSeconds: int64(refreshIntervalSeconds),
	}
	src, err := s.app.Tracker().Update(context.Background(), id, input)
	if err != nil {
		return nil, toIPCError(err)
	}
	return src, nil
}

func (s *DesktopService) DeleteTrackerSource(id string) error {
	if err := s.app.Tracker().Delete(context.Background(), id); err != nil {
		return toIPCError(err)
	}
	return nil
}

func (s *DesktopService) RefreshTrackerSource(id string) (*networkpolicy.TrackerSource, error) {
	src, err := s.app.Tracker().Refresh(context.Background(), id)
	if err != nil {
		return nil, toIPCError(err)
	}
	return src, nil
}

func (s *DesktopService) RefreshAllTrackerSources() (*TrackerRefreshSummary, error) {
	errs := s.app.Tracker().RefreshAll(context.Background())
	return &TrackerRefreshSummary{FailureCount: len(errs)}, nil
}

// =========================================================================
// 6. MEDIA AUTH DOMAIN
// =========================================================================

func (s *DesktopService) GetMediaAuthSettings() (*mediaauth.Settings, error) {
	st, err := s.app.MediaAuth().GetSettings(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return st, nil
}

func (s *DesktopService) UpdateMediaAuthSettings(reqJSON string) (*mediaauth.Settings, error) {
	var req mediaauth.UpdateSettingsRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return nil, makeIPCError(job.ErrInvalidRequest, "invalid media auth request JSON")
	}
	st, err := s.app.MediaAuth().UpdateSettings(context.Background(), req)
	if err != nil {
		return nil, makeIPCError(job.ErrInvalidRequest, err.Error())
	}
	return st, nil
}

func (s *DesktopService) ImportMediaCookies(content string) (*mediaauth.Settings, error) {
	data := []byte(content)
	if len(data) > mediaauth.MaxCookieFileSize {
		return nil, makeIPCError(job.ErrInvalidRequest, fmt.Sprintf("cookie file exceeds maximum allowed size of %d bytes", mediaauth.MaxCookieFileSize))
	}
	st, err := s.app.MediaAuth().ImportCookies(context.Background(), data)
	if err != nil {
		if errors.Is(err, mediaauth.ErrSecretStorageUnavailable) {
			return nil, makeIPCError(job.ErrSecretStorageUnavailable, "secret storage is unavailable; cannot securely store cookies")
		}
		return nil, makeIPCError(job.ErrInvalidRequest, err.Error())
	}
	return st, nil
}

func (s *DesktopService) DeleteMediaCookies() (*mediaauth.Settings, error) {
	st, err := s.app.MediaAuth().DeleteCookies(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return st, nil
}

// =========================================================================
// 7. SYNC DOMAIN
// =========================================================================

func (s *DesktopService) GetSyncSnapshot() (*job.SyncSnapshot, error) {
	snap, err := s.app.Manager().GetSyncSnapshot(context.Background())
	if err != nil {
		return nil, toIPCError(err)
	}
	return snap, nil
}

func (s *DesktopService) GetEventsAfter(cursor int64) (*EventsReplayResult, error) {
	s.mu.Lock()
	appInst := s.app
	s.mu.Unlock()

	if appInst == nil {
		return nil, toIPCError(errors.New("application core not initialized"))
	}

	ctx := context.Background()
	currentCursor := appInst.Manager().GetCurrentCursor(ctx)

	if cursor >= currentCursor {
		return &EventsReplayResult{
			Events:        []job.Event{},
			CurrentCursor: currentCursor,
			GapDetected:   cursor > currentCursor,
		}, nil
	}

	if bus := appInst.EventBus(); bus != nil {
		evs, ok := bus.GetEventsAfter(cursor)
		if ok {
			return &EventsReplayResult{
				Events:        evs,
				CurrentCursor: currentCursor,
				GapDetected:   false,
			}, nil
		}
	}

	return &EventsReplayResult{
		Events:        nil,
		CurrentCursor: currentCursor,
		GapDetected:   true,
	}, nil
}

// =========================================================================
// 8. DIAGNOSTICS & LIFECYCLE
// =========================================================================

func (s *DesktopService) GetBackendInstanceID() string {
	return s.instanceID
}

func (s *DesktopService) GetDataRootInfo() (*DataRootInfo, error) {
	exePath, _ := os.Executable()
	cwd, _ := os.Getwd()
	return &DataRootInfo{
		ExecutablePath:   exePath,
		WorkingDir:       cwd,
		ResolvedDataRoot: s.dataRoot,
		IsDeterministic:  true,
	}, nil
}

func (s *DesktopService) GetSingleInstanceStatus() (*SingleInstanceStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &SingleInstanceStatus{
		PrimaryPID:         os.Getpid(),
		BackendInstanceID:  s.instanceID,
		CoreInitCount:      s.coreInitCount,
		SecondLaunchCount:  s.secondLaunchCount,
		LastForwardedArgs:  s.lastForwardedArgs,
		LastForwardedCwd:   s.lastForwardedCwd,
		ReloadCount:        s.reloadCount,
		DeliveryCount:      s.deliveryCount,
		LastEventToken:     s.lastEventToken,
		StateSyncCompleted: s.stateSyncCompleted,
		StateSyncCursor:    s.stateSyncCursor,
	}, nil
}

// ReloadMainWindow forces a reload of the main webview window.
func (s *DesktopService) ReloadMainWindow() {
	s.mu.Lock()
	win := s.mainWindow
	s.deliveryCount = 0
	s.lastEventToken = ""
	s.stateSyncCompleted = false
	s.reloadCount++
	s.persistStatus()
	s.mu.Unlock()

	if win != nil {
		win.Reload()
	}
}

// EmitDiagnosticEvent publishes a deterministic diagnostic event across the application event bus.
func (s *DesktopService) EmitDiagnosticEvent(token string) {
	s.mu.Lock()
	s.deliveryCount = 0
	s.lastEventToken = token
	appInst := s.app
	s.persistStatus()
	s.mu.Unlock()

	if appInst != nil && appInst.EventBus() != nil {
		appInst.EventBus().Publish(job.Event{
			Type: "diagnostic.ping",
			Data: token,
		})
	}
}

// RecordDiagnosticDelivery records an event delivery acknowledged by the frontend.
func (s *DesktopService) RecordDiagnosticDelivery(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastEventToken == "" || s.lastEventToken == token {
		s.deliveryCount++
		s.persistStatus()
	}
}

// GetDiagnosticEventDeliveryCount returns the delivery count for the last emitted diagnostic event.
func (s *DesktopService) GetDiagnosticEventDeliveryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deliveryCount
}

// RecordStateSyncCompleted records completion of frontend StateSync initialization or catch-up.
func (s *DesktopService) RecordStateSyncCompleted(cursor int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateSyncCompleted = true
	s.stateSyncCursor = cursor
	s.persistStatus()
}

func (s *DesktopService) ShowNativeDialog(title, message string) {
	if s.wailsApp != nil && s.wailsApp.Dialog != nil {
		dialog := s.wailsApp.Dialog.Info().SetTitle(title).SetMessage(message)
		if s.mainWindow != nil {
			dialog.AttachToWindow(s.mainWindow)
		}
		dialog.Show()
	}
}

func (s *DesktopService) Quit() {
	s.mu.Lock()
	l := s.lifecycle
	s.mu.Unlock()
	if l != nil {
		l.RequestQuit()
		return
	}
	log.Println("Desktop: Orderly quit initiated.")
	if s.app != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.app.Shutdown(ctx)
	}
	if s.wailsApp != nil {
		s.wailsApp.Quit()
	}
}
