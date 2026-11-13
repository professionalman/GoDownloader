package toolmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Media dependency diagnostics. FFmpeg and FFprobe stay externally managed (user-configured or on PATH); GoDownloader
// never installs them, and never installs or manages a JavaScript runtime. These diagnostics only inspect and
// version-probe executables, and report path-free results suitable for the Settings UI.

// Dependency states.
const (
	DependencyAvailable   = "available"
	DependencyMissing     = "missing"
	DependencyInvalid     = "invalid"      // present but not a working executable of the expected tool
	DependencyProbeFailed = "probe_failed" // present but the version check did not complete in time
)

// Dependency sources (where the effective executable was found).
const (
	DependencySourceUserConfigured  = "user_configured"
	DependencySourceSystemPath      = "system_path"
	DependencySourceAlongsideFFmpeg = "alongside_ffmpeg"
	DependencySourceNone            = "none"
)

// ExternalDependencyStatus is the path-free status of one externally managed executable.
type ExternalDependencyStatus struct {
	Tool       string `json:"tool"`
	State      string `json:"state"`
	Available  bool   `json:"available"`
	Version    string `json:"version,omitempty"`
	Ownership  string `json:"ownership"`
	Source     string `json:"source"`
	Diagnostic string `json:"diagnostic,omitempty"`
	// RestartRequired is set when the tool became usable after startup: the running media engine still uses the
	// executable resolved at startup, so a restart is needed to use it.
	RestartRequired bool `json:"restartRequired"`
}

// JavaScriptRuntimeStatus reports whether yt-dlp's default JavaScript runtime (Deno) is discoverable on PATH.
type JavaScriptRuntimeStatus struct {
	Detected   bool   `json:"detected"`
	Runtime    string `json:"runtime,omitempty"`
	Diagnostic string `json:"diagnostic"`
}

// WorkflowReadiness states whether a media workflow's required tools are available.
type WorkflowReadiness struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Requires   []string `json:"requires"`
	Ready      bool     `json:"ready"`
	Advisory   bool     `json:"advisory"`
	Diagnostic string   `json:"diagnostic,omitempty"`
}

// MediaDependencyStatus aggregates the media toolchain the Settings UI shows.
type MediaDependencyStatus struct {
	Ytdlp             ManagedToolStatus        `json:"ytdlp"`
	FFmpeg            ExternalDependencyStatus `json:"ffmpeg"`
	FFprobe           ExternalDependencyStatus `json:"ffprobe"`
	JavaScriptRuntime JavaScriptRuntimeStatus  `json:"javascriptRuntime"`
	Workflows         []WorkflowReadiness      `json:"workflows"`
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func sourceLabel(source string) string {
	switch source {
	case DependencySourceUserConfigured:
		return "configured"
	case DependencySourceAlongsideFFmpeg:
		return "FFmpeg folder"
	case DependencySourceSystemPath:
		return "system PATH"
	}
	return "expected"
}

// SetEngineFFmpegPath records the FFmpeg executable the media engine was configured with at startup ("" when none was
// resolved, in which case yt-dlp receives no --ffmpeg-location). Diagnostics compare against it; it is never exposed.
func (m *Manager) SetEngineFFmpegPath(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.engineFFmpegPath = path
	m.engineFFmpegKnown = true
}

// engineFFmpeg returns the FFmpeg path the running engine uses and whether it is known. Without an explicit record it
// falls back to the startup resolution cached by Resolve.
func (m *Manager) engineFFmpeg() (string, bool) {
	m.mu.RLock()
	path, known := m.engineFFmpegPath, m.engineFFmpegKnown
	m.mu.RUnlock()
	if known {
		return path, true
	}
	if info := m.Get(ToolFFmpeg); info != nil {
		if info.Available() {
			return info.ExecutablePath, true
		}
		return "", true
	}
	return "", false
}

func samePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// inspectExecutable classifies one candidate executable. displayName is the user-facing tool name and versionPrefix
// the lower-case prefix its "-version" output must start with (for example "ffmpeg version").
func (m *Manager) inspectExecutable(ctx context.Context, tool, displayName, path, source, versionPrefix string) ExternalDependencyStatus {
	st, _ := m.inspectExecutableRuns(ctx, tool, displayName, path, source, versionPrefix)
	return st
}

// inspectExecutableRuns is inspectExecutable that also reports whether the executable runs. The media engine selects
// any executable whose version probe succeeds, so one that runs but does not identify as the expected tool is still
// the engine's choice; it is reported as invalid.
func (m *Manager) inspectExecutableRuns(ctx context.Context, tool, displayName, path, source, versionPrefix string) (ExternalDependencyStatus, bool) {
	st := ExternalDependencyStatus{Tool: tool, Ownership: OwnershipExternalUser, Source: source}
	fi, err := os.Stat(path)
	if err != nil {
		st.State = DependencyMissing
		st.Diagnostic = fmt.Sprintf("%s was not found at the %s location.", displayName, sourceLabel(source))
		return st, false
	}
	if !fi.Mode().IsRegular() {
		st.State = DependencyInvalid
		st.Diagnostic = fmt.Sprintf("The %s %s location is not an executable file.", sourceLabel(source), displayName)
		return st, false
	}
	out, err := m.probeVersion(ctx, path, "-version")
	switch {
	case errors.Is(err, ErrToolProbeTimeout):
		st.State = DependencyProbeFailed
		st.Diagnostic = fmt.Sprintf("%s (%s) did not answer a version check in time.", displayName, sourceLabel(source))
		return st, false
	case err != nil:
		st.State = DependencyInvalid
		st.Diagnostic = fmt.Sprintf("%s (%s) could not be run. It may be damaged, blocked, or built for another platform.", displayName, sourceLabel(source))
		return st, false
	}
	lower := strings.ToLower(strings.TrimSpace(out))
	if !strings.HasPrefix(lower, versionPrefix) {
		st.State = DependencyInvalid
		st.Diagnostic = fmt.Sprintf("The %s executable runs but does not identify itself as %s; media post-processing will likely fail.", sourceLabel(source), displayName)
		return st, true
	}
	st.State = DependencyAvailable
	st.Available = true
	if fields := strings.Fields(strings.TrimSpace(out)); len(fields) >= 3 {
		st.Version = fields[2]
	}
	return st, true
}

// inspectFFmpeg follows the engine's selection exactly (configured path, then PATH; an executable is selected when its
// version probe runs) and returns the status of the executable a (re)started engine would use, plus its path
// (internal only; never exposed).
func (m *Manager) inspectFFmpeg(ctx context.Context) (ExternalDependencyStatus, string) {
	m.mu.RLock()
	cfgPath := m.configuredPaths[ToolFFmpeg]
	m.mu.RUnlock()

	var configured *ExternalDependencyStatus
	if cfgPath != "" {
		abs, err := filepath.Abs(cfgPath)
		if err != nil {
			abs = cfgPath
		}
		st, runs := m.inspectExecutableRuns(ctx, ToolFFmpeg, "FFmpeg", abs, DependencySourceUserConfigured, "ffmpeg version")
		if runs {
			return st, abs
		}
		configured = &st
	}
	if sysPath, err := m.lookPathFn(ToolFFmpeg); err == nil {
		st, runs := m.inspectExecutableRuns(ctx, ToolFFmpeg, "FFmpeg", sysPath, DependencySourceSystemPath, "ffmpeg version")
		if runs {
			if configured != nil {
				st.Diagnostic = strings.TrimSpace("The configured FFmpeg could not be used (" + configured.Diagnostic +
					") The FFmpeg on the system PATH is used instead. " + st.Diagnostic)
			}
			return st, sysPath
		}
		if configured == nil {
			return st, ""
		}
	}
	if configured != nil {
		return *configured, ""
	}
	return ExternalDependencyStatus{
		Tool: ToolFFmpeg, State: DependencyMissing, Ownership: OwnershipExternalUser, Source: DependencySourceNone,
		Diagnostic: "FFmpeg was not found. Install FFmpeg and add it to PATH (or configure FFMPEG_PATH), then restart GoDownloader.",
	}, ""
}

// inspectFFprobe follows yt-dlp's lookup for the running engine. When the engine passes --ffmpeg-location (an FFmpeg
// was resolved at startup), yt-dlp looks for ffprobe only in that executable's folder, never on PATH. Without it,
// yt-dlp searches the PATH it inherits from GoDownloader.
func (m *Manager) inspectFFprobe(ctx context.Context, engineFFmpegPath string) ExternalDependencyStatus {
	if engineFFmpegPath != "" {
		sibling := filepath.Join(filepath.Dir(engineFFmpegPath), executableName(ToolFFprobe))
		if _, err := os.Stat(sibling); err == nil {
			return m.inspectExecutable(ctx, ToolFFprobe, "FFprobe", sibling, DependencySourceAlongsideFFmpeg, "ffprobe version")
		}
		return ExternalDependencyStatus{
			Tool: ToolFFprobe, State: DependencyMissing, Ownership: OwnershipExternalUser, Source: DependencySourceNone,
			Diagnostic: "FFprobe was not found next to the FFmpeg GoDownloader uses. yt-dlp looks for FFprobe only in that " +
				"folder (not on PATH): place ffprobe next to ffmpeg.",
		}
	}
	if sysPath, err := m.lookPathFn(ToolFFprobe); err == nil {
		return m.inspectExecutable(ctx, ToolFFprobe, "FFprobe", sysPath, DependencySourceSystemPath, "ffprobe version")
	}
	return ExternalDependencyStatus{
		Tool: ToolFFprobe, State: DependencyMissing, Ownership: OwnershipExternalUser, Source: DependencySourceNone,
		Diagnostic: "FFprobe was not found. It ships with FFmpeg: place ffprobe next to ffmpeg or add it to PATH.",
	}
}

func (m *Manager) inspectJavaScriptRuntime() JavaScriptRuntimeStatus {
	if _, err := m.lookPathFn("deno"); err == nil {
		return JavaScriptRuntimeStatus{Detected: true, Runtime: "deno",
			Diagnostic: "Deno was found on PATH. yt-dlp uses it for sites that require JavaScript challenge solving."}
	}
	return JavaScriptRuntimeStatus{Diagnostic: "No Deno runtime was found on PATH. Some sites (for example YouTube) may " +
		"require a JavaScript runtime for yt-dlp. GoDownloader does not install one; install Deno and add it to PATH if " +
		"such downloads fail."}
}

// MediaDependencies inspects the media toolchain without changing any state.
func (m *Manager) MediaDependencies(ctx context.Context) MediaDependencyStatus {
	ffmpeg, ffmpegPath := m.inspectFFmpeg(ctx)
	enginePath, engineKnown := m.engineFFmpeg()
	// A restart is required when the running engine would not use the FFmpeg a restart would select: the engine is
	// pinned (--ffmpeg-location) to a different executable, or it has none and the new one comes from the configured
	// path (yt-dlp, run without --ffmpeg-location, only finds FFmpeg on the PATH it inherited from GoDownloader).
	if engineKnown && ffmpegPath != "" {
		pinnedElsewhere := enginePath != "" && !samePath(enginePath, ffmpegPath)
		configuredLater := enginePath == "" && ffmpeg.Source == DependencySourceUserConfigured
		if pinnedElsewhere || configuredLater {
			ffmpeg.RestartRequired = true
			ffmpeg.Diagnostic = strings.TrimSpace(ffmpeg.Diagnostic + " The running media engine is still configured " +
				"with the FFmpeg found at startup; restart GoDownloader to use this one.")
		}
	}
	probeBase := enginePath
	if !engineKnown {
		probeBase = ffmpegPath
	}
	ffprobe := m.inspectFFprobe(ctx, probeBase)
	st := MediaDependencyStatus{
		Ytdlp:             m.YtdlpStatus(ctx),
		FFmpeg:            ffmpeg,
		FFprobe:           ffprobe,
		JavaScriptRuntime: m.inspectJavaScriptRuntime(),
	}
	st.Workflows = mediaWorkflows(st)
	return st
}

// requirement is one tool a workflow needs and whether it is ready.
type requirement struct {
	ok   bool
	name string
}

// missingRequirements names the requirements that are not ready, or returns "" when all are.
func missingRequirements(reqs ...requirement) string {
	var names []string
	for _, r := range reqs {
		if !r.ok {
			names = append(names, r.name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return "Unavailable until " + strings.Join(names, " and ") + " is ready."
}

// mediaWorkflows derives per-workflow readiness. Requirements follow yt-dlp's documented dependencies: FFmpeg and
// FFprobe are required for merging separate video and audio streams and for post-processing (subtitle embedding and
// conversion).
func mediaWorkflows(st MediaDependencyStatus) []WorkflowReadiness {
	ytdlpReady := st.Ytdlp.Available && !st.Ytdlp.RestartRequired
	ffmpegReady := st.FFmpeg.Available && !st.FFmpeg.RestartRequired
	ffprobeReady := st.FFprobe.Available

	ytdlpReq := requirement{ytdlpReady, "yt-dlp"}
	ffmpegReq := requirement{ffmpegReady, "FFmpeg"}
	ffprobeReq := requirement{ffprobeReady, "FFprobe"}
	missing := missingRequirements

	return []WorkflowReadiness{
		{ID: "media_analysis", Label: "Analyze media links and download single-file formats",
			Requires: []string{ToolYtdlp}, Ready: ytdlpReady, Diagnostic: missing(ytdlpReq)},
		{ID: "merge_video_audio", Label: "Download best quality (separate video and audio merged)",
			Requires: []string{ToolYtdlp, ToolFFmpeg, ToolFFprobe}, Ready: ytdlpReady && ffmpegReady && ffprobeReady,
			Diagnostic: missing(ytdlpReq, ffmpegReq, ffprobeReq)},
		{ID: "subtitle_postprocessing", Label: "Embed subtitles or convert them to SRT",
			Requires: []string{ToolYtdlp, ToolFFmpeg, ToolFFprobe}, Ready: ytdlpReady && ffmpegReady && ffprobeReady,
			Diagnostic: missing(ytdlpReq, ffmpegReq, ffprobeReq)},
		{ID: "javascript_challenges", Label: "Sites that need JavaScript challenge solving (for example YouTube)",
			Requires: []string{ToolYtdlp, "deno"}, Ready: ytdlpReady && st.JavaScriptRuntime.Detected, Advisory: true,
			Diagnostic: func() string {
				if st.JavaScriptRuntime.Detected {
					return missing(ytdlpReq)
				}
				return "May fail without a JavaScript runtime (Deno). Not required for every site."
			}()},
	}
}
