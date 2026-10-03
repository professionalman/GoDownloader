package supportbundle

import (
	"context"
	"regexp"
	"strings"
	"time"
)

// Maximum length for untrusted tool version strings before parsing.
const maxRawVersionLength = 256

// Regex to validate that a normalized version token is safe and compact.
// Allows alphanumeric characters, dots, hyphens, plus signs, and underscores.
var safeVersionTokenRegex = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.\-+_]{0,47}$`)

// NormalizeToolVersion derives a safe, compact version token from external tool version strings.
// Rules:
// - Bounded maximum length.
// - Rejects control characters, slashes, backslashes, colons, spaces, and query characters.
// - For ffmpeg: parses token following "ffmpeg version " if present.
// - Returns "unknown" if no valid, safe token can be extracted.
func NormalizeToolVersion(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxRawVersionLength {
		return "unknown"
	}

	// Extract first line only
	if idx := strings.IndexAny(raw, "\r\n"); idx != -1 {
		raw = strings.TrimSpace(raw[:idx])
	}

	// Special-case ffmpeg banner format: e.g. "ffmpeg version 6.1.1-system ..."
	if strings.HasPrefix(strings.ToLower(raw), "ffmpeg version ") {
		fields := strings.Fields(raw)
		if len(fields) >= 3 {
			raw = fields[2]
		}
	}

	// Clean out common prefixes like "v" if followed by digit
	if (strings.HasPrefix(raw, "v") || strings.HasPrefix(raw, "V")) && len(raw) > 1 && raw[1] >= '0' && raw[1] <= '9' {
		raw = raw[1:]
	}

	// Strictly reject paths, URLs, query parameters, or delimiters
	if strings.ContainsAny(raw, "/\\:?&=@~ \t$|;,") {
		return "unknown"
	}

	if safeVersionTokenRegex.MatchString(raw) {
		return raw
	}
	return "unknown"
}

// Fixed allowlist sanitizers to ensure arbitrary string corruption never leaks through property names or values.

func SanitizeConflictPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "rename", "overwrite", "fail":
		return strings.ToLower(strings.TrimSpace(policy))
	default:
		return "unknown"
	}
}

func SanitizeProxyMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "disabled", "system", "custom":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "unknown"
	}
}

func SanitizeProxyProtocol(proto string) string {
	switch strings.ToLower(strings.TrimSpace(proto)) {
	case "http", "https", "socks5":
		return strings.ToLower(strings.TrimSpace(proto))
	default:
		return "unknown"
	}
}

func SanitizeSeedingMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "none", "unlimited", "ratio", "duration", "ratio_or_duration":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "unknown"
	}
}

func SanitizeIssueKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "interrupted_media", "finalization_failed", "engine_unavailable",
		"external_state_unrecoverable", "torrent_metadata_unrecoverable":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return "other"
	}
}

func SanitizeEngineName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "aria2", "qbittorrent", "ytdlp", "native":
		return strings.ToLower(strings.TrimSpace(name))
	default:
		return "other"
	}
}

func SanitizeToolName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "yt-dlp", "ffmpeg", "ffprobe", "qbittorrent", "aria2":
		return strings.ToLower(strings.TrimSpace(name))
	default:
		return "other"
	}
}

func SanitizeProcessPurpose(purpose string) string {
	switch strings.ToLower(strings.TrimSpace(purpose)) {
	case "download", "mux", "probe", "analysis":
		return strings.ToLower(strings.TrimSpace(purpose))
	default:
		return "other"
	}
}

func SanitizeProcessState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running", "exited", "terminated":
		return strings.ToLower(strings.TrimSpace(state))
	default:
		return "other"
	}
}

func SanitizeJobStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "analyzing", "awaiting_selection", "downloading",
		"processing", "paused", "seeding", "completed", "failed", "cancelled":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "other"
	}
}

// ----------------------------------------------------------------------------
// Host and Core Snapshots
// ----------------------------------------------------------------------------

// HostSnapshot holds host-lifecycle facts gathered by DesktopService.
type HostSnapshot struct {
	CloseToTray        bool   `json:"closeToTray"`
	AutostartEnabled   bool   `json:"autostartEnabled"`
	DataRootKind       string `json:"dataRootKind"`       // "appdata"
	DataRootNormalized string `json:"dataRootNormalized"` // "%APPDATA%\\GoDownloader"
}

// CoreSnapshot holds sanitized facts gathered from application domain services.
type CoreSnapshot struct {
	Settings        SafeSettingsDTO        `json:"settings"`
	Tools           []SafeToolDTO          `json:"tools"`
	Engines         []SafeEngineDTO        `json:"engines"`
	Scheduler       SafeSchedulerDTO       `json:"scheduler"`
	Recovery        SafeRecoveryDTO        `json:"recovery"`
	DatabaseSummary SafeDatabaseSummaryDTO `json:"databaseSummary"`
}

// SnapshotProvider is the single capability required by Builder to read sanitized core facts.
type SnapshotProvider interface {
	Snapshot(ctx context.Context) (CoreSnapshot, error)
}

// ----------------------------------------------------------------------------
// DTOs for the 9 Fixed Archive Files
// ----------------------------------------------------------------------------

// ManifestDTO corresponds to manifest.json.
type ManifestDTO struct {
	BundleSchemaVersion  int    `json:"bundleSchemaVersion"`
	PrivacyPolicyVersion int    `json:"privacyPolicyVersion"`
	GeneratedAt          string `json:"generatedAt"` // RFC3339 UTC
	AppVersion           string `json:"appVersion"`
	Platform             string `json:"platform"`
	Architecture         string `json:"architecture"`
	Commit               string `json:"commit,omitempty"`
}

// RuntimeDTO corresponds to runtime.json.
type RuntimeDTO struct {
	AppVersion         string `json:"appVersion"`
	Commit             string `json:"commit"`
	GoVersion          string `json:"goVersion"`
	OS                 string `json:"os"`
	Arch               string `json:"arch"`
	DataRootKind       string `json:"dataRootKind"`
	DataRootNormalized string `json:"dataRootNormalized"`
	CloseToTray        bool   `json:"closeToTray"`
	AutostartEnabled   bool   `json:"autostartEnabled"`
}

// SafeSettingsDTO corresponds to settings.json.
type SafeSettingsDTO struct {
	Queue   SafeQueueSettings   `json:"queue"`
	Storage SafeStorageSettings `json:"storage"`
	Network SafeNetworkSettings `json:"network"`
	Torrent SafeTorrentSettings `json:"torrent"`
}

type SafeQueueSettings struct {
	MaxConcurrentDownloads          int  `json:"maxConcurrentDownloads"`
	EffectiveMaxConcurrentDownloads int  `json:"effectiveMaxConcurrentDownloads"`
	EnvironmentOverride             bool `json:"environmentOverride"`
}

type SafeStorageSettings struct {
	MinimumFreeSpaceBytes int64 `json:"minimumFreeSpaceBytes"`
	DefaultConflictPolicy string `json:"defaultConflictPolicy"`
	OverridesPresent      bool  `json:"overridesPresent"`
}

type SafeProxySettings struct {
	Mode                  string `json:"mode"`
	Protocol              string `json:"protocol,omitempty"`
	Port                  int    `json:"port,omitempty"`
	CredentialsConfigured bool   `json:"credentialsConfigured"`
}

type SafeNetworkSettings struct {
	GlobalDownloadLimitBytesPerSecond int64             `json:"globalDownloadLimitBytesPerSecond"`
	Proxy                             SafeProxySettings `json:"proxy"`
	HasCustomUserAgent                bool              `json:"hasCustomUserAgent"`
	CustomHeaderCount                 int               `json:"customHeaderCount"`
	RetryMaxAttempts                  int               `json:"retryMaxAttempts"`
	RetryWaitSeconds                  int               `json:"retryWaitSeconds"`
	ConnectTimeoutSeconds             int               `json:"connectTimeoutSeconds"`
	RequestTimeoutSeconds             int               `json:"requestTimeoutSeconds"`
	MaxConnectionsPerServer           int               `json:"maxConnectionsPerServer"`
	Split                             int               `json:"split"`
}

type SafeTorrentSettings struct {
	DownloadLimitBytesPerSecond            int64  `json:"downloadLimitBytesPerSecond"`
	UploadLimitBytesPerSecond              int64  `json:"uploadLimitBytesPerSecond"`
	SeedingMode                            string `json:"seedingMode"`
	ApplyTrackerSubscriptionsToNewTorrents bool   `json:"applyTrackerSubscriptionsToNewTorrents"`
	ManageQBitGlobalNetworkSettings        bool   `json:"manageQBitGlobalNetworkSettings"`
}

// SafeToolDTO corresponds to entries in tools.json.
type SafeToolDTO struct {
	Name               string    `json:"name"`
	Version            string    `json:"version"`
	PlatformArch       string    `json:"platformArch"`
	OwnershipMode      string    `json:"ownershipMode"`
	VerificationStatus string    `json:"verificationStatus"`
	Status             string    `json:"status"`
	Provenance         string    `json:"provenance"`
	ProbedAt           time.Time `json:"probedAt"`
}

type SafeToolsDTO struct {
	Tools []SafeToolDTO `json:"tools"`
}

// SafeEngineDTO corresponds to entries in engines.json.
type SafeEngineDTO struct {
	Name             string `json:"name"`
	Registered       bool   `json:"registered"`
	OwnershipMode    string `json:"ownershipMode"`
	CachedToolStatus string `json:"cachedToolStatus,omitempty"`
}

type SafeEnginesDTO struct {
	Engines []SafeEngineDTO `json:"engines"`
}

// SafeSchedulerDTO corresponds to scheduler.json.
type SafeSchedulerDTO struct {
	Governor  SafeGovernorDTO `json:"governor"`
	Queue     SafeQueueDTO    `json:"queue"`
	Processes SafeProcessDTO  `json:"processes"`
}

type SafeGovernorDTO struct {
	GlobalTransferLimit int            `json:"globalTransferLimit"`
	ActiveTransfers     int            `json:"activeTransfers"`
	ActiveLeases        int            `json:"activeLeases"`
	EngineLimits        map[string]int `json:"engineLimits"`
	ActivePerEngine     map[string]int `json:"activePerEngine"`
}

type SafeQueueDTO struct {
	QueuedJobsCount int `json:"queuedJobsCount"`
}

type SafeProcessDTO struct {
	ActiveSupervisedCount int            `json:"activeSupervisedCount"`
	CountsByTool          map[string]int `json:"countsByTool"`
	CountsByPurpose       map[string]int `json:"countsByPurpose"`
	CountsByState         map[string]int `json:"countsByState"`
}

// SafeRecoveryDTO corresponds to recovery.json.
type SafeRecoveryDTO struct {
	ReconciledFinalizations       int            `json:"reconciledFinalizations"`
	ReattachedTransfers           int            `json:"reattachedTransfers"`
	RestartedMetadataAcquisitions int            `json:"restartedMetadataAcquisitions"`
	InterruptedMediaJobs          int            `json:"interruptedMediaJobs"`
	IssueCountsByKind             map[string]int `json:"issueCountsByKind"`
}

// SafeDatabaseSummaryDTO corresponds to database-summary.json.
type SafeDatabaseSummaryDTO struct {
	MigrationMarkers          map[string]bool `json:"migrationMarkers"`
	TablesPresent             map[string]bool `json:"tablesPresent"`
	PendingFinalizationsCount int             `json:"pendingFinalizationsCount"`
	StateSyncCursor           int64           `json:"stateSyncCursor"`
	JobCountsByStatus         map[string]int  `json:"jobCountsByStatus"`
	JobCountsByEngine         map[string]int  `json:"jobCountsByEngine"`
}

// SupportBundleExportResult is returned to callers upon export attempt.
type SupportBundleExportResult struct {
	Status   string `json:"status"` // "saved" | "cancelled"
	FileName string `json:"fileName,omitempty"`
}
