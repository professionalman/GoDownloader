package toolmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"downloader/internal/job"
)

// Manager manages external tool discovery, probing, provenance, and durability.
type Manager struct {
	dataDir         string
	execRepo        job.IExecutionRepository
	probeTimeout    time.Duration
	mu              sync.RWMutex
	tools           map[string]*ToolInfo
	configuredPaths map[string]string

	// Hooks for testing
	lookPathFn    func(file string) (string, error)
	execCommandFn func(ctx context.Context, name string, arg ...string) *exec.Cmd
	// probeQBitFn probes a qBittorrent endpoint and returns (version, error).
	probeQBitFn func(ctx context.Context, url string) (string, error)
	// probeAria2Fn probes an aria2 RPC endpoint and returns (version, error).
	probeAria2Fn func(ctx context.Context, url string) (string, error)
	httpClient   *http.Client
}

// New creates a new ToolManager instance.
func New(dataDir string, execRepo job.IExecutionRepository) *Manager {
	httpCl := &http.Client{Timeout: 5 * time.Second}
	m := &Manager{
		dataDir:         dataDir,
		execRepo:        execRepo,
		probeTimeout:    3 * time.Second,
		tools:           make(map[string]*ToolInfo),
		configuredPaths: make(map[string]string),
		lookPathFn:      exec.LookPath,
		execCommandFn:   exec.CommandContext,
		httpClient:      httpCl,
	}
	m.probeQBitFn = m.defaultProbeQBit
	m.probeAria2Fn = m.defaultProbeAria2

	// Ensure managed directory exists
	_ = os.MkdirAll(m.ManagedDir(), 0755)
	return m
}

// ManagedDir returns the application-owned managed tool directory (dataDir/bin).
func (m *Manager) ManagedDir() string {
	return filepath.Join(m.dataDir, "bin")
}

// ManagedPath returns the expected executable path for a managed tool.
func (m *Manager) ManagedPath(tool string) string {
	return filepath.Join(m.ManagedDir(), exeName(tool))
}

// RegisterConfig sets an explicit configured path or URL for a tool.
func (m *Manager) RegisterConfig(tool, pathOrURL string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configuredPaths[tool] = strings.TrimSpace(pathOrURL)
}

// Get returns the cached ToolInfo for a given tool name, or nil if unresolved.
func (m *Manager) Get(tool string) *ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.tools[tool]
}

// GetAll returns a copy of all resolved tool states.
func (m *Manager) GetAll() map[string]*ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*ToolInfo, len(m.tools))
	for k, v := range m.tools {
		cp := *v
		out[k] = &cp
	}
	return out
}

// Resolve resolves a specific tool by identity following strict discovery precedence.
func (m *Manager) Resolve(ctx context.Context, tool string) (*ToolInfo, error) {
	switch tool {
	case ToolYtdlp:
		return m.resolveYtdlp(ctx)
	case ToolFFmpeg:
		return m.resolveFFmpeg(ctx)
	case ToolAria2:
		return m.resolveAria2(ctx)
	case ToolQBittorrent:
		return m.resolveQBittorrent(ctx)
	default:
		return nil, fmt.Errorf("%w: %s", ErrToolNotFound, tool)
	}
}

// ResolveAll resolves all supported tools in order.
func (m *Manager) ResolveAll(ctx context.Context) map[string]*ToolInfo {
	for _, tool := range []string{ToolYtdlp, ToolFFmpeg, ToolAria2, ToolQBittorrent} {
		_, _ = m.Resolve(ctx, tool)
	}
	return m.GetAll()
}

// AcquireYtdlp is the entry point for managed yt-dlp acquisition.
// In FND-4A, remote download is gated pending signed manifest verification (ADR-SYN2-006).
func (m *Manager) AcquireYtdlp(ctx context.Context) error {
	return ErrAcquisitionGated
}

// resolveYtdlp resolves yt-dlp following precedence:
// 1. Managed path (dataDir/bin/yt-dlp[.exe])
// 2. Explicitly configured path (if non-default)
// 3. System PATH lookup
func (m *Manager) resolveYtdlp(ctx context.Context) (*ToolInfo, error) {
	m.mu.RLock()
	cfgPath := m.configuredPaths[ToolYtdlp]
	m.mu.RUnlock()

	managedCandidate := m.ManagedPath(ToolYtdlp)

	// Step 1: Check managed application-owned binary
	if isRegularFile(managedCandidate) {
		ver, err := m.probeVersion(ctx, managedCandidate, "--version")
		if err == nil {
			info := &ToolInfo{
				Name:               ToolYtdlp,
				Version:            ver,
				PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
				OwnershipMode:      OwnershipManaged,
				ExecutablePath:     managedCandidate,
				VerificationStatus: VerificationStatusVerified,
				Status:             StatusActive,
				Provenance:         ProvenanceManaged,
				ProbedAt:           time.Now().UTC(),
			}
			m.recordTool(ctx, info)
			return info, nil
		}
		log.Printf("toolmanager: managed yt-dlp at %s probe failed: %v", managedCandidate, err)
	}

	// Step 2: Check explicit user-configured path
	if cfgPath != "" && cfgPath != ToolYtdlp && cfgPath != exeName(ToolYtdlp) {
		absPath, err := filepath.Abs(cfgPath)
		if err == nil && isRegularFile(absPath) {
			ver, err := m.probeVersion(ctx, absPath, "--version")
			if err == nil {
				info := &ToolInfo{
					Name:               ToolYtdlp,
					Version:            ver,
					PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
					OwnershipMode:      OwnershipExternalUser,
					ExecutablePath:     absPath,
					VerificationStatus: VerificationStatusVerified,
					Status:             StatusActive,
					Provenance:         ProvenanceUserConfigured,
					ProbedAt:           time.Now().UTC(),
				}
				m.recordTool(ctx, info)
				return info, nil
			}
			log.Printf("toolmanager: configured yt-dlp at %s probe failed: %v", absPath, err)
		} else {
			log.Printf("toolmanager: configured yt-dlp path %s does not exist or is not a regular file", cfgPath)
		}
	}

	// Step 3: Check system PATH
	if sysPath, err := m.lookPathFn(ToolYtdlp); err == nil && isRegularFile(sysPath) {
		ver, err := m.probeVersion(ctx, sysPath, "--version")
		if err == nil {
			info := &ToolInfo{
				Name:               ToolYtdlp,
				Version:            ver,
				PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
				OwnershipMode:      OwnershipExternalUser,
				ExecutablePath:     sysPath,
				VerificationStatus: VerificationStatusVerified,
				Status:             StatusActive,
				Provenance:         ProvenanceSystemPath,
				ProbedAt:           time.Now().UTC(),
			}
			m.recordTool(ctx, info)
			return info, nil
		}
		log.Printf("toolmanager: system PATH yt-dlp at %s probe failed: %v", sysPath, err)
	}

	// Step 4: Not found anywhere
	missingInfo := &ToolInfo{
		Name:               ToolYtdlp,
		PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		OwnershipMode:      OwnershipManaged,
		VerificationStatus: VerificationStatusUnverified,
		Status:             StatusMissing,
		Diagnostic:         fmt.Sprintf("yt-dlp is required for media extraction and download. Place yt-dlp in %s or configure YTDLP_PATH or add yt-dlp to system PATH.", m.ManagedDir()),
		ProbedAt:           time.Now().UTC(),
	}
	m.recordTool(ctx, missingInfo)
	return missingInfo, fmt.Errorf("%w: %s", ErrToolNotFound, ToolYtdlp)
}

// resolveFFmpeg resolves FFmpeg (ExternalUserManaged) following precedence:
// 1. Explicitly configured path (cfg.FFmpegPath)
// 2. System PATH lookup
func (m *Manager) resolveFFmpeg(ctx context.Context) (*ToolInfo, error) {
	m.mu.RLock()
	cfgPath := m.configuredPaths[ToolFFmpeg]
	m.mu.RUnlock()

	// Step 1: Explicitly configured path
	if cfgPath != "" {
		absPath, err := filepath.Abs(cfgPath)
		if err == nil && isRegularFile(absPath) {
			ver, err := m.probeVersion(ctx, absPath, "-version")
			if err == nil {
				info := &ToolInfo{
					Name:               ToolFFmpeg,
					Version:            ver,
					PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
					OwnershipMode:      OwnershipExternalUser,
					ExecutablePath:     absPath,
					VerificationStatus: VerificationStatusVerified,
					Status:             StatusActive,
					Provenance:         ProvenanceUserConfigured,
					ProbedAt:           time.Now().UTC(),
				}
				m.recordTool(ctx, info)
				return info, nil
			}
			log.Printf("toolmanager: configured ffmpeg at %s probe failed: %v", absPath, err)
		} else {
			log.Printf("toolmanager: configured ffmpeg path %s does not exist or is not a regular file", cfgPath)
		}
	}

	// Step 2: System PATH lookup
	if sysPath, err := m.lookPathFn(ToolFFmpeg); err == nil && isRegularFile(sysPath) {
		ver, err := m.probeVersion(ctx, sysPath, "-version")
		if err == nil {
			info := &ToolInfo{
				Name:               ToolFFmpeg,
				Version:            ver,
				PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
				OwnershipMode:      OwnershipExternalUser,
				ExecutablePath:     sysPath,
				VerificationStatus: VerificationStatusVerified,
				Status:             StatusActive,
				Provenance:         ProvenanceSystemPath,
				ProbedAt:           time.Now().UTC(),
			}
			m.recordTool(ctx, info)
			return info, nil
		}
		log.Printf("toolmanager: system PATH ffmpeg at %s probe failed: %v", sysPath, err)
	}

	// Step 3: Not found
	missingInfo := &ToolInfo{
		Name:               ToolFFmpeg,
		PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		OwnershipMode:      OwnershipExternalUser,
		VerificationStatus: VerificationStatusUnverified,
		Status:             StatusMissing,
		Diagnostic:         "FFmpeg was not found. Format merging and audio extraction will be unavailable. Configure FFMPEG_PATH or install ffmpeg in system PATH.",
		ProbedAt:           time.Now().UTC(),
	}
	m.recordTool(ctx, missingInfo)
	return missingInfo, fmt.Errorf("%w: %s", ErrToolNotFound, ToolFFmpeg)
}

// resolveAria2 resolves aria2 compatibility status via RPC health probe.
func (m *Manager) resolveAria2(ctx context.Context) (*ToolInfo, error) {
	m.mu.RLock()
	rpcURL := m.configuredPaths[ToolAria2]
	m.mu.RUnlock()

	if rpcURL == "" {
		info := &ToolInfo{
			Name:               ToolAria2,
			PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			OwnershipMode:      OwnershipCompatibility,
			VerificationStatus: VerificationStatusUnverified,
			Status:             StatusMissing,
			Provenance:         ProvenanceExternalAPI,
			Diagnostic:         "aria2 RPC URL is not configured. Configure ARIA2_RPC_URL to enable direct downloads via aria2.",
			ProbedAt:           time.Now().UTC(),
		}
		m.recordTool(ctx, info)
		return info, fmt.Errorf("%w: %s", ErrToolNotFound, ToolAria2)
	}

	// Probe actual RPC endpoint
	ver, err := m.probeAria2Fn(ctx, rpcURL)
	if err != nil {
		info := &ToolInfo{
			Name:               ToolAria2,
			PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			OwnershipMode:      OwnershipCompatibility,
			ExecutablePath:     rpcURL,
			VerificationStatus: VerificationStatusFailed,
			Status:             StatusUnreachable,
			Provenance:         ProvenanceExternalAPI,
			Diagnostic:         fmt.Sprintf("aria2 RPC endpoint %s is configured but unreachable: %v", rpcURL, err),
			ProbedAt:           time.Now().UTC(),
		}
		m.recordTool(ctx, info)
		log.Printf("toolmanager: aria2 RPC at %s unreachable: %v", rpcURL, err)
		return info, nil // non-fatal
	}

	info := &ToolInfo{
		Name:               ToolAria2,
		Version:            ver,
		PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		OwnershipMode:      OwnershipCompatibility,
		ExecutablePath:     rpcURL,
		VerificationStatus: VerificationStatusVerified,
		Status:             StatusActive,
		Provenance:         ProvenanceExternalAPI,
		Diagnostic:         fmt.Sprintf("aria2 compatibility engine active via RPC (%s)", rpcURL),
		ProbedAt:           time.Now().UTC(),
	}
	m.recordTool(ctx, info)
	return info, nil
}

// resolveQBittorrent resolves qBittorrent external service status via health probe.
func (m *Manager) resolveQBittorrent(ctx context.Context) (*ToolInfo, error) {
	m.mu.RLock()
	qbitURL := m.configuredPaths[ToolQBittorrent]
	m.mu.RUnlock()

	if qbitURL == "" {
		info := &ToolInfo{
			Name:               ToolQBittorrent,
			PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			OwnershipMode:      OwnershipExternalUser,
			VerificationStatus: VerificationStatusUnverified,
			Status:             StatusMissing,
			Provenance:         ProvenanceExternalAPI,
			Diagnostic:         "qBittorrent Web API URL is not configured. Configure QBIT_URL to enable torrent downloads.",
			ProbedAt:           time.Now().UTC(),
		}
		m.recordTool(ctx, info)
		return info, fmt.Errorf("%w: %s", ErrToolNotFound, ToolQBittorrent)
	}

	// Probe actual endpoint
	ver, err := m.probeQBitFn(ctx, qbitURL)
	if err != nil {
		info := &ToolInfo{
			Name:               ToolQBittorrent,
			PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
			OwnershipMode:      OwnershipExternalUser,
			ExecutablePath:     qbitURL,
			VerificationStatus: VerificationStatusFailed,
			Status:             StatusUnreachable,
			Provenance:         ProvenanceExternalAPI,
			Diagnostic:         fmt.Sprintf("qBittorrent endpoint %s is configured but unreachable: %v", qbitURL, err),
			ProbedAt:           time.Now().UTC(),
		}
		m.recordTool(ctx, info)
		log.Printf("toolmanager: qBittorrent at %s unreachable: %v", qbitURL, err)
		return info, nil // non-fatal
	}

	info := &ToolInfo{
		Name:               ToolQBittorrent,
		Version:            ver,
		PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		OwnershipMode:      OwnershipExternalUser,
		ExecutablePath:     qbitURL,
		VerificationStatus: VerificationStatusVerified,
		Status:             StatusActive,
		Provenance:         ProvenanceExternalAPI,
		Diagnostic:         fmt.Sprintf("qBittorrent external torrent engine active via Web API (%s)", qbitURL),
		ProbedAt:           time.Now().UTC(),
	}
	m.recordTool(ctx, info)
	return info, nil
}

// RevalidateActiveRecords inspects persisted active tool records and revalidates their presence.
// If an active executable is missing or invalid, re-runs discovery.
func (m *Manager) RevalidateActiveRecords(ctx context.Context) error {
	if m.execRepo == nil {
		return nil
	}

	// Revalidate executable-backed tools (check file existence)
	for _, name := range []string{ToolYtdlp, ToolFFmpeg} {
		rec, err := m.execRepo.GetActiveTool(ctx, name)
		if err != nil || rec == nil {
			continue
		}

		// Check if executable path is still valid
		if rec.ExecutablePath != "" && !isRegularFile(rec.ExecutablePath) {
			log.Printf("toolmanager: active tool %s recorded at %s is no longer accessible; re-resolving", name, rec.ExecutablePath)
			_, _ = m.Resolve(ctx, name)
		}
	}

	// Revalidate API-backed tools (re-probe endpoints)
	for _, name := range []string{ToolAria2, ToolQBittorrent} {
		rec, err := m.execRepo.GetActiveTool(ctx, name)
		if err != nil || rec == nil {
			continue
		}

		// Re-resolve triggers a fresh health probe
		log.Printf("toolmanager: revalidating API-backed tool %s", name)
		_, _ = m.Resolve(ctx, name)
	}
	return nil
}

// probeVersion executes a command with a bounded timeout and captures the first line of output.
func (m *Manager) probeVersion(ctx context.Context, executable string, args ...string) (string, error) {
	probeCtx, cancel := context.WithTimeout(ctx, m.probeTimeout)
	defer cancel()

	cmd := m.execCommandFn(probeCtx, executable, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if probeCtx.Err() == context.DeadlineExceeded {
			return "", ErrToolProbeTimeout
		}
		return "", fmt.Errorf("%w: %v", ErrToolExecutionFailed, err)
	}

	// Capture first line from bounded reader
	limited := io.LimitReader(&stdout, 4096)
	line, err := readFirstLine(limited)
	if err == nil && line != "" {
		return line, nil
	}

	limitedErr := io.LimitReader(&stderr, 4096)
	lineErr, err2 := readFirstLine(limitedErr)
	if err2 == nil && lineErr != "" {
		return lineErr, nil
	}

	return "unknown", nil
}

func (m *Manager) recordTool(ctx context.Context, info *ToolInfo) {
	m.mu.Lock()
	m.tools[info.Name] = info
	m.mu.Unlock()

	// Persist to durable tool_records if repository is present and tool has meaningful state
	if m.execRepo != nil && (info.Status == StatusActive || info.Status == StatusUnreachable) && info.Name != "" {
		ver := info.Version
		if ver == "" {
			ver = "unknown"
		}
		rec := &job.ToolRecord{
			Name:               info.Name,
			Version:            ver,
			PlatformArch:       info.PlatformArch,
			OwnershipMode:      info.OwnershipMode,
			ExecutablePath:     info.ExecutablePath,
			SHA256:             info.SHA256,
			VerificationStatus: info.VerificationStatus,
			Status:             info.Status,
			CreatedAt:          info.ProbedAt,
			UpdatedAt:          info.ProbedAt,
		}
		if err := m.execRepo.SaveToolRecord(ctx, rec); err != nil {
			log.Printf("toolmanager: persist tool record %s@%s failed: %v", rec.Name, rec.Version, err)
		}
	}
}

// defaultProbeQBit performs an HTTP GET to qBittorrent's /api/v2/app/version endpoint.
func (m *Manager) defaultProbeQBit(ctx context.Context, baseURL string) (string, error) {
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v2/app/version"
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	ver := strings.TrimSpace(string(body))
	if ver == "" {
		return "", fmt.Errorf("empty version response")
	}
	return ver, nil
}

// defaultProbeAria2 performs a JSON-RPC call to aria2.getVersion.
func (m *Manager) defaultProbeAria2(ctx context.Context, rpcURL string) (string, error) {
	type rpcReq struct {
		JSONRPC string        `json:"jsonrpc"`
		ID      string        `json:"id"`
		Method  string        `json:"method"`
		Params  []interface{} `json:"params"`
	}
	type rpcResp struct {
		Result struct {
			Version string `json:"version"`
		} `json:"result"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	payload, _ := json.Marshal(rpcReq{JSONRPC: "2.0", ID: "tm-probe", Method: "aria2.getVersion", Params: []interface{}{}})
	req, err := http.NewRequestWithContext(ctx, "POST", rpcURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var rpcR rpcResp
	if err := json.Unmarshal(body, &rpcR); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if rpcR.Error != nil {
		return "", fmt.Errorf("aria2 RPC error %d: %s", rpcR.Error.Code, rpcR.Error.Message)
	}
	if rpcR.Result.Version == "" {
		return "", fmt.Errorf("empty version in response")
	}
	return rpcR.Result.Version, nil
}

func readFirstLine(r io.Reader) (string, error) {
	var buf [512]byte
	n, err := r.Read(buf[:])
	if err != nil && n == 0 {
		return "", err
	}
	s := string(buf[:n])
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed != "" {
			return trimmed, nil
		}
	}
	return "", nil
}

func isRegularFile(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !fi.IsDir()
}

func exeName(base string) string {
	if runtime.GOOS == "windows" && !strings.HasSuffix(strings.ToLower(base), ".exe") {
		return base + ".exe"
	}
	return base
}
