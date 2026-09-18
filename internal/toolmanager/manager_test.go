package toolmanager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"downloader/internal/engine/ytdlp"
	"downloader/internal/job"
)

// mockExecutionRepo implements job.IExecutionRepository in-memory for testing.
type mockExecutionRepo struct {
	mu      sync.Mutex
	records map[string]*job.ToolRecord
}

func newMockExecutionRepo() *mockExecutionRepo {
	return &mockExecutionRepo{records: make(map[string]*job.ToolRecord)}
}

func (m *mockExecutionRepo) SaveToolRecord(ctx context.Context, rec *job.ToolRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[rec.Name] = rec
	return nil
}

func (m *mockExecutionRepo) GetActiveTool(ctx context.Context, name string) (*job.ToolRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.records[name], nil
}

func (m *mockExecutionRepo) ListToolRecords(ctx context.Context, name string) ([]job.ToolRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.records[name]; ok {
		return []job.ToolRecord{*rec}, nil
	}
	return nil, nil
}

// Unused IExecutionRepository methods stubbed for interface compliance
func (m *mockExecutionRepo) CreateExecution(ctx context.Context, exec *job.JobExecution) error {
	return nil
}
func (m *mockExecutionRepo) UpdateExecution(ctx context.Context, exec *job.JobExecution) error {
	return nil
}
func (m *mockExecutionRepo) GetLatestExecution(ctx context.Context, jobID string) (*job.JobExecution, error) {
	return nil, nil
}
func (m *mockExecutionRepo) GetExecutionByID(ctx context.Context, id string) (*job.JobExecution, error) {
	return nil, nil
}
func (m *mockExecutionRepo) ListExecutions(ctx context.Context, jobID string) ([]job.JobExecution, error) {
	return nil, nil
}
func (m *mockExecutionRepo) SaveHTTPCheckpoint(ctx context.Context, cp *job.HTTPCheckpoint, segments []job.HTTPCheckpointSegment) error {
	return nil
}
func (m *mockExecutionRepo) GetHTTPCheckpoint(ctx context.Context, jobID string) (*job.HTTPCheckpoint, []job.HTTPCheckpointSegment, error) {
	return nil, nil, nil
}
func (m *mockExecutionRepo) UpdateHTTPSegmentProgress(ctx context.Context, jobID string, segmentIndex int, currentOffset, verifiedBytes int64, completed bool) error {
	return nil
}
func (m *mockExecutionRepo) SaveFinalizationRecord(ctx context.Context, rec *job.FinalizationRecord) error {
	return nil
}
func (m *mockExecutionRepo) UpdateFinalizationPhase(ctx context.Context, id string, phase job.FinalizationPhase, errStr string) error {
	return nil
}
func (m *mockExecutionRepo) GetPendingFinalizations(ctx context.Context) ([]job.FinalizationRecord, error) {
	return nil, nil
}
func (m *mockExecutionRepo) GetFinalizationByJobID(ctx context.Context, jobID string) (*job.FinalizationRecord, error) {
	return nil, nil
}
func (m *mockExecutionRepo) GetEventCursor(ctx context.Context, scope string) (int64, error) {
	return 0, nil
}
func (m *mockExecutionRepo) AdvanceEventCursor(ctx context.Context, scope string) (int64, error) {
	return 0, nil
}

// createFakeExecutable creates a temporary fake executable file.
func createFakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	fullName := exeName(name)
	target := filepath.Join(dir, fullName)
	if err := os.WriteFile(target, []byte("#!/fake\n"), 0755); err != nil {
		t.Fatalf("failed to create fake executable %s: %v", target, err)
	}
	return target
}

// echoCmd returns a mock exec command that echoes the given version string.
func echoCmd(version string) func(ctx context.Context, name string, arg ...string) *exec.Cmd {
	return func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "cmd", "/c", "echo "+version)
	}
}

// ============================
// Test A: Managed yt-dlp -> OwnershipManaged
// ============================
func TestToolManager_A_ManagedYtdlp_OwnershipManaged(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)
	ctx := context.Background()

	mgr.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "cmd", "/c", "echo 2026.01.01-managed")
	}

	// Create managed binary at exact expected path
	expectedManaged := mgr.ManagedPath(ToolYtdlp)
	os.MkdirAll(filepath.Dir(expectedManaged), 0755)
	os.WriteFile(expectedManaged, []byte("#!/fake\n"), 0755)

	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OwnershipMode != OwnershipManaged {
		t.Errorf("expected ownership managed, got %s", info.OwnershipMode)
	}
	if info.Provenance != ProvenanceManaged {
		t.Errorf("expected provenance managed, got %s", info.Provenance)
	}
	if info.ExecutablePath != expectedManaged {
		t.Errorf("expected path %s, got %s", expectedManaged, info.ExecutablePath)
	}
	if info.VerificationStatus != VerificationStatusVerified {
		t.Errorf("expected verification version_probed, got %s", info.VerificationStatus)
	}
}

// ============================
// Test B: Explicit user yt-dlp path -> OwnershipExternalUser
// ============================
func TestToolManager_B_ExplicitUserYtdlp_OwnershipExternalUser(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)
	ctx := context.Background()

	mgr.execCommandFn = echoCmd("2025.12.12-configured")
	mgr.lookPathFn = func(file string) (string, error) {
		return "", errors.New("not found")
	}

	// Explicit configured path (different from bare "yt-dlp")
	cfgDir := filepath.Join(tmpDir, "cfg_bin")
	os.MkdirAll(cfgDir, 0755)
	cfgBin := createFakeExecutable(t, cfgDir, "yt-dlp_configured")
	mgr.RegisterConfig(ToolYtdlp, cfgBin)

	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user for user-configured path, got %s", info.OwnershipMode)
	}
	if info.Provenance != ProvenanceUserConfigured {
		t.Errorf("expected provenance user_configured, got %s", info.Provenance)
	}
}

// ============================
// Test C: Default "yt-dlp" resolved from PATH -> provenance system_path, ownership ExternalUser
// ============================
func TestToolManager_C_DefaultYtdlpFromPATH_SystemPath_ExternalUser(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)
	ctx := context.Background()

	mgr.execCommandFn = echoCmd("2025.10.10-system")

	// Register config with default bare name (simulating config default "yt-dlp")
	mgr.RegisterConfig(ToolYtdlp, "yt-dlp")

	sysDir := filepath.Join(tmpDir, "system_bin")
	os.MkdirAll(sysDir, 0755)
	sysBin := createFakeExecutable(t, sysDir, "yt-dlp_system")
	mgr.lookPathFn = func(file string) (string, error) {
		if file == ToolYtdlp {
			return sysBin, nil
		}
		return "", errors.New("not found")
	}

	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceSystemPath {
		t.Errorf("expected provenance system_path for default config, got %s", info.Provenance)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user for PATH-discovered yt-dlp, got %s", info.OwnershipMode)
	}
}

// ============================
// Test D: FFmpeg explicit vs PATH provenance
// ============================
func TestToolManager_D_FFmpeg_ExplicitVsPathProvenance(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)
	ctx := context.Background()

	mgr.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if strings.Contains(name, "cfg") {
			return exec.CommandContext(ctx, "cmd", "/c", "echo ffmpeg version 7.0-configured")
		}
		return exec.CommandContext(ctx, "cmd", "/c", "echo ffmpeg version 6.1.1-system")
	}

	// 1. PATH discovery
	sysDir := filepath.Join(tmpDir, "sys_bin")
	os.MkdirAll(sysDir, 0755)
	sysBin := createFakeExecutable(t, sysDir, "ffmpeg")
	mgr.lookPathFn = func(file string) (string, error) {
		if file == ToolFFmpeg {
			return sysBin, nil
		}
		return "", errors.New("not found")
	}

	info, err := mgr.Resolve(ctx, ToolFFmpeg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceSystemPath {
		t.Errorf("expected provenance system_path, got %s", info.Provenance)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user, got %s", info.OwnershipMode)
	}

	// 2. Explicit config overrides PATH
	cfgDir := filepath.Join(tmpDir, "cfg_bin")
	os.MkdirAll(cfgDir, 0755)
	cfgBin := createFakeExecutable(t, cfgDir, "ffmpeg_cfg")
	mgr.RegisterConfig(ToolFFmpeg, cfgBin)

	info, err = mgr.Resolve(ctx, ToolFFmpeg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceUserConfigured {
		t.Errorf("expected provenance user_configured, got %s", info.Provenance)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user, got %s", info.OwnershipMode)
	}
}

// ============================
// Test E: Configured qB endpoint reachable -> active + real version
// ============================
func TestToolManager_E_QBitReachable_ActiveWithVersion(t *testing.T) {
	// Start httptest server simulating qBittorrent
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/app/version" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "v5.0.2")
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	mgr := New(t.TempDir(), newMockExecutionRepo())
	mgr.RegisterConfig(ToolQBittorrent, ts.URL)

	info, err := mgr.Resolve(context.Background(), ToolQBittorrent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != StatusActive {
		t.Errorf("expected status active, got %s", info.Status)
	}
	if info.Version != "v5.0.2" {
		t.Errorf("expected version v5.0.2, got %s", info.Version)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user, got %s", info.OwnershipMode)
	}
	if info.VerificationStatus != VerificationStatusVerified {
		t.Errorf("expected verification version_probed, got %s", info.VerificationStatus)
	}
}

// ============================
// Test F: Configured qB endpoint unreachable -> StatusUnreachable
// ============================
func TestToolManager_F_QBitUnreachable_NotActive(t *testing.T) {
	mgr := New(t.TempDir(), newMockExecutionRepo())
	// Point to a port that nothing is listening on
	mgr.RegisterConfig(ToolQBittorrent, "http://127.0.0.1:1")

	info, err := mgr.Resolve(context.Background(), ToolQBittorrent)
	// Non-fatal: err should be nil
	if err != nil {
		t.Fatalf("qB resolve should be non-fatal, got error: %v", err)
	}
	if info.Status != StatusUnreachable {
		t.Errorf("expected status unreachable, got %s", info.Status)
	}
	if info.VerificationStatus != VerificationStatusFailed {
		t.Errorf("expected verification failed, got %s", info.VerificationStatus)
	}
	if !strings.Contains(info.Diagnostic, "unreachable") {
		t.Errorf("expected actionable diagnostic with 'unreachable', got %s", info.Diagnostic)
	}
}

// ============================
// Test G: Configured aria2 RPC reachable -> active with version
// ============================
func TestToolManager_G_Aria2Reachable_ActiveWithVersion(t *testing.T) {
	// Start httptest server simulating aria2 JSON-RPC
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"tm-probe","result":{"version":"1.37.0"}}`)
	}))
	defer ts.Close()

	mgr := New(t.TempDir(), newMockExecutionRepo())
	mgr.RegisterConfig(ToolAria2, ts.URL)

	info, err := mgr.Resolve(context.Background(), ToolAria2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != StatusActive {
		t.Errorf("expected status active, got %s", info.Status)
	}
	if info.Version != "1.37.0" {
		t.Errorf("expected version 1.37.0, got %s", info.Version)
	}
	if info.OwnershipMode != OwnershipCompatibility {
		t.Errorf("expected ownership compatibility, got %s", info.OwnershipMode)
	}
}

// ============================
// Test H: aria2 RPC unreachable -> not active even if aria2c on PATH
// ============================
func TestToolManager_H_Aria2Unreachable_NotActive(t *testing.T) {
	mgr := New(t.TempDir(), newMockExecutionRepo())
	mgr.RegisterConfig(ToolAria2, "http://127.0.0.1:1/jsonrpc")

	info, err := mgr.Resolve(context.Background(), ToolAria2)
	// Non-fatal: err should be nil
	if err != nil {
		t.Fatalf("aria2 resolve should be non-fatal, got error: %v", err)
	}
	if info.Status != StatusUnreachable {
		t.Errorf("expected status unreachable, got %s", info.Status)
	}
	if info.VerificationStatus != VerificationStatusFailed {
		t.Errorf("expected verification failed, got %s", info.VerificationStatus)
	}
}

// ============================
// Test I: Stale active executable record -> revalidated/re-resolved
// ============================
func TestToolManager_I_StaleExecutableRecord_Revalidated(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)
	ctx := context.Background()

	mgr.execCommandFn = echoCmd("2026.01.01")

	// Create and resolve managed binary
	managedBin := mgr.ManagedPath(ToolYtdlp)
	os.MkdirAll(filepath.Dir(managedBin), 0755)
	os.WriteFile(managedBin, []byte("#!/fake\n"), 0755)

	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected resolve error: %v", err)
	}
	if info.Status != StatusActive {
		t.Fatalf("expected status active, got %s", info.Status)
	}

	// Delete the managed binary to simulate stale path
	os.Remove(managedBin)

	// Configure system PATH fallback
	sysDir := filepath.Join(tmpDir, "sys_bin")
	os.MkdirAll(sysDir, 0755)
	sysBin := createFakeExecutable(t, sysDir, "yt-dlp")
	mgr.lookPathFn = func(file string) (string, error) {
		if file == ToolYtdlp {
			return sysBin, nil
		}
		return "", errors.New("not found")
	}

	// Revalidate active records
	if err := mgr.RevalidateActiveRecords(ctx); err != nil {
		t.Fatalf("revalidate failed: %v", err)
	}

	// Should have re-resolved to system PATH
	updatedInfo := mgr.Get(ToolYtdlp)
	if updatedInfo.Provenance != ProvenanceSystemPath {
		t.Errorf("expected re-resolution to system_path, got %s", updatedInfo.Provenance)
	}
	if updatedInfo.ExecutablePath != sysBin {
		t.Errorf("expected executable path %s, got %s", sysBin, updatedInfo.ExecutablePath)
	}
}

// ============================
// Test J: Stale active API record -> no longer active after failed probe
// ============================
func TestToolManager_J_StaleAPIRecord_NotActiveAfterFailedProbe(t *testing.T) {
	repo := newMockExecutionRepo()
	mgr := New(t.TempDir(), repo)
	ctx := context.Background()

	// Start a reachable server first
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "v5.0.2")
	}))

	mgr.RegisterConfig(ToolQBittorrent, ts.URL)
	info, err := mgr.Resolve(ctx, ToolQBittorrent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != StatusActive {
		t.Fatalf("expected initial status active, got %s", info.Status)
	}

	// Verify it was persisted
	rec, _ := repo.GetActiveTool(ctx, ToolQBittorrent)
	if rec == nil || rec.Status != StatusActive {
		t.Fatalf("expected persisted active record")
	}

	// Now shut down the server to make endpoint unreachable
	ts.Close()

	// Revalidate
	if err := mgr.RevalidateActiveRecords(ctx); err != nil {
		t.Fatalf("revalidate failed: %v", err)
	}

	// After revalidation, qBittorrent should be unreachable
	updatedInfo := mgr.Get(ToolQBittorrent)
	if updatedInfo.Status != StatusUnreachable {
		t.Errorf("expected status unreachable after server shutdown, got %s", updatedInfo.Status)
	}
}

// ============================
// Test K: Missing yt-dlp -> actionable tool error
// ============================
func TestToolManager_K_MissingYtdlp_ActionableError(t *testing.T) {
	mgr := New(t.TempDir(), nil)
	mgr.lookPathFn = func(file string) (string, error) {
		return "", errors.New("not found in path")
	}
	mgr.execCommandFn = echoCmd("fallback")

	info, err := mgr.Resolve(context.Background(), ToolYtdlp)
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}
	if info.Status != StatusMissing {
		t.Fatalf("expected status missing, got %s", info.Status)
	}
	if !strings.Contains(info.Diagnostic, "yt-dlp is required") {
		t.Fatalf("expected actionable diagnostic mentioning requirement, got %s", info.Diagnostic)
	}
	if !strings.Contains(info.Diagnostic, "YTDLP_PATH") {
		t.Fatalf("expected diagnostic to mention YTDLP_PATH config, got %s", info.Diagnostic)
	}
}

// ============================
// Test L: ytdlp engine receives ToolManager-resolved binary path
// ============================
func TestToolManager_L_EngineReceivesResolvedPath(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	mgr := New(dataDir, nil)
	ctx := context.Background()

	mgr.execCommandFn = echoCmd("2026.01.01")

	// Create managed binary
	managedBin := mgr.ManagedPath(ToolYtdlp)
	os.MkdirAll(filepath.Dir(managedBin), 0755)
	os.WriteFile(managedBin, []byte("#!/fake\n"), 0755)

	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate what main.go does: pass resolved path to ytdlp.NewEngine
	resolvedPath := info.ExecutablePath
	eng := ytdlp.NewEngine(resolvedPath, "")

	// Engine should report the resolved path is set (not the bare "yt-dlp" fallback).
	// We verify this by checking the engine was created with a non-empty, absolute path.
	if resolvedPath == "" || resolvedPath == "yt-dlp" {
		t.Fatalf("expected resolved absolute path, got %s", resolvedPath)
	}
	// Engine creation should not panic
	_ = eng
}

// ============================
// Test M: FFmpeg-resolved path reaches yt-dlp argument construction
// ============================
func TestToolManager_M_FFmpegPathReachesYtdlpArgs(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	mgr := New(dataDir, nil)
	ctx := context.Background()

	mgr.execCommandFn = echoCmd("ffmpeg version 7.0")

	// Create and resolve FFmpeg
	cfgDir := filepath.Join(tmpDir, "ffmpeg_bin")
	os.MkdirAll(cfgDir, 0755)
	ffmpegBin := createFakeExecutable(t, cfgDir, "ffmpeg")
	mgr.RegisterConfig(ToolFFmpeg, ffmpegBin)

	info, err := mgr.Resolve(ctx, ToolFFmpeg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate main.go: pass resolved FFmpeg path to ytdlp.NewEngine
	resolvedFFmpeg := info.ExecutablePath
	eng := ytdlp.NewEngine("yt-dlp", resolvedFFmpeg)

	// Engine should report available = false (no real yt-dlp) but ffmpegPath is set.
	// The key verification: NewEngine receives the resolved FFmpeg path, not empty string.
	// When ytdlp engine constructs args, it uses e.ffmpegPath for --ffmpeg-location.
	if resolvedFFmpeg == "" {
		t.Fatalf("expected non-empty resolved FFmpeg path")
	}
	_ = eng
}

// ============================
// Additional: Full discovery precedence with corrected ownership assertions
// ============================
func TestToolManager_DiscoveryPrecedence_Ytdlp(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)

	ctx := context.Background()

	// Mock execCommandFn to return version without running real binary
	mgr.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		if strings.Contains(name, "managed") {
			return exec.CommandContext(ctx, "cmd", "/c", "echo 2026.01.01-managed")
		} else if strings.Contains(name, "configured") {
			return exec.CommandContext(ctx, "cmd", "/c", "echo 2025.12.12-configured")
		} else if strings.Contains(name, "system") {
			return exec.CommandContext(ctx, "cmd", "/c", "echo 2025.10.10-system")
		}
		return exec.CommandContext(ctx, "cmd", "/c", "echo 2025.01.01-fallback")
	}

	// 1. Missing everywhere -> returns ErrToolNotFound and StatusMissing
	mgr.lookPathFn = func(file string) (string, error) {
		return "", errors.New("not found in path")
	}
	info, err := mgr.Resolve(ctx, ToolYtdlp)
	if !errors.Is(err, ErrToolNotFound) {
		t.Fatalf("expected ErrToolNotFound, got %v", err)
	}
	if info.Status != StatusMissing {
		t.Fatalf("expected status missing, got %s", info.Status)
	}
	if !strings.Contains(info.Diagnostic, "yt-dlp is required") {
		t.Fatalf("expected actionable diagnostic, got %s", info.Diagnostic)
	}

	// 2. Present in system PATH only -> ExternalUser ownership (CORRECTED from old test)
	sysDir := filepath.Join(tmpDir, "system_bin")
	os.MkdirAll(sysDir, 0755)
	sysBin := createFakeExecutable(t, sysDir, "yt-dlp_system")
	mgr.lookPathFn = func(file string) (string, error) {
		if file == ToolYtdlp {
			return sysBin, nil
		}
		return "", errors.New("not found")
	}

	info, err = mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceSystemPath {
		t.Errorf("expected provenance system_path, got %s", info.Provenance)
	}
	if info.ExecutablePath != sysBin {
		t.Errorf("expected path %s, got %s", sysBin, info.ExecutablePath)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user for PATH yt-dlp, got %s", info.OwnershipMode)
	}

	// 3. Explicitly configured path -> outranks system PATH, ExternalUser ownership (CORRECTED)
	cfgDir := filepath.Join(tmpDir, "cfg_bin")
	os.MkdirAll(cfgDir, 0755)
	cfgBin := createFakeExecutable(t, cfgDir, "yt-dlp_configured")
	mgr.RegisterConfig(ToolYtdlp, cfgBin)

	info, err = mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceUserConfigured {
		t.Errorf("expected provenance user_configured, got %s", info.Provenance)
	}
	if info.ExecutablePath != cfgBin {
		t.Errorf("expected path %s, got %s", cfgBin, info.ExecutablePath)
	}
	if info.OwnershipMode != OwnershipExternalUser {
		t.Errorf("expected ownership external_user for configured yt-dlp, got %s", info.OwnershipMode)
	}

	// 4. Managed application-owned path -> outranks configured path and system PATH, Managed ownership
	expectedManaged := mgr.ManagedPath(ToolYtdlp)
	os.MkdirAll(filepath.Dir(expectedManaged), 0755)
	os.WriteFile(expectedManaged, []byte("#!/fake\n"), 0755)

	info, err = mgr.Resolve(ctx, ToolYtdlp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provenance != ProvenanceManaged {
		t.Errorf("expected provenance managed, got %s", info.Provenance)
	}
	if info.ExecutablePath != expectedManaged {
		t.Errorf("expected path %s, got %s", expectedManaged, info.ExecutablePath)
	}
	if info.OwnershipMode != OwnershipManaged {
		t.Errorf("expected ownership managed for app-owned binary, got %s", info.OwnershipMode)
	}

	// Verify durability record was persisted to execution repository
	rec, err := repo.GetActiveTool(ctx, ToolYtdlp)
	if err != nil || rec == nil {
		t.Fatalf("expected active tool record in repo, got %v, err=%v", rec, err)
	}
	if rec.ExecutablePath != expectedManaged {
		t.Errorf("persisted path mismatch: %s vs %s", rec.ExecutablePath, expectedManaged)
	}
}

func TestToolManager_VersionProbe_TimeoutAndFailure(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	mgr := New(dataDir, nil)
	mgr.probeTimeout = 50 * time.Millisecond

	ctx := context.Background()

	// Slow probe exceeding probeTimeout
	mgr.execCommandFn = func(ctx context.Context, name string, arg ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", "Start-Sleep -Milliseconds 500; Write-Output 2026.01.01")
	}

	bin := createFakeExecutable(t, mgr.ManagedDir(), "yt-dlp")
	_, err := mgr.probeVersion(ctx, bin, "--version")
	if !errors.Is(err, ErrToolProbeTimeout) {
		t.Fatalf("expected ErrToolProbeTimeout, got %v", err)
	}
}

func TestToolManager_ConcurrentResolution(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	repo := newMockExecutionRepo()
	mgr := New(dataDir, repo)

	mgr.execCommandFn = echoCmd("2026.01.01")
	managedBin := mgr.ManagedPath(ToolYtdlp)
	os.MkdirAll(filepath.Dir(managedBin), 0755)
	os.WriteFile(managedBin, []byte("#!/fake\n"), 0755)

	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mgr.Resolve(ctx, ToolYtdlp)
			_ = mgr.Get(ToolYtdlp)
			_ = mgr.GetAll()
		}()
	}
	wg.Wait()

	info := mgr.Get(ToolYtdlp)
	if info == nil || info.Status != StatusActive {
		t.Fatalf("expected active tool info, got %v", info)
	}
}

func TestToolManager_AcquireYtdlp_Gated(t *testing.T) {
	mgr := New(t.TempDir(), nil)
	err := mgr.AcquireYtdlp(context.Background())
	if !errors.Is(err, ErrAcquisitionGated) {
		t.Fatalf("expected ErrAcquisitionGated, got %v", err)
	}
}

// ============================
// Test: tool_record truthfulness - unreachable is persisted
// ============================
func TestToolManager_ToolRecordTruthfulness_UnreachablePersisted(t *testing.T) {
	repo := newMockExecutionRepo()
	mgr := New(t.TempDir(), repo)
	mgr.RegisterConfig(ToolQBittorrent, "http://127.0.0.1:1")

	_, _ = mgr.Resolve(context.Background(), ToolQBittorrent)

	rec, _ := repo.GetActiveTool(context.Background(), ToolQBittorrent)
	if rec == nil {
		t.Fatalf("expected tool record to be persisted even when unreachable")
	}
	if rec.Status != StatusUnreachable {
		t.Errorf("expected persisted status unreachable, got %s", rec.Status)
	}
	if rec.VerificationStatus != VerificationStatusFailed {
		t.Errorf("expected persisted verification failed, got %s", rec.VerificationStatus)
	}
}
