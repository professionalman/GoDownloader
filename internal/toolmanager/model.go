package toolmanager

import (
	"errors"
	"time"
)

// Stable tool identities.
const (
	ToolYtdlp       = "yt-dlp"
	ToolFFmpeg      = "ffmpeg"
	ToolFFprobe     = "ffprobe"
	ToolQBittorrent = "qbittorrent"
	ToolAria2       = "aria2"
)

// Tool ownership modes.
const (
	OwnershipManaged       = "managed"       // GoDownloader manages this binary (yt-dlp)
	OwnershipExternalUser  = "external_user" // System or user-provided tool (FFmpeg, qBittorrent)
	OwnershipCompatibility = "compatibility" // Transition / compatibility tool (aria2)
)

// Provenance types.
const (
	ProvenanceManaged        = "managed"         // In application-owned managed directory (dataDir/bin)
	ProvenanceUserConfigured = "user_configured" // Explicitly configured by user in config or environment
	ProvenanceSystemPath     = "system_path"     // Discovered via system PATH lookup
	ProvenanceExternalAPI    = "external_api"    // Verified external service / daemon endpoint
	// ProvenanceManagedRelease marks a content-addressed managed binary selected by the authenticated ledger.
	ProvenanceManagedRelease = "managed_release"
	// ProvenanceManagedLegacy marks the historical dataDir/bin/yt-dlp(.exe) location. It is only version-probed;
	// presence in the managed directory never implies authenticity.
	ProvenanceManagedLegacy = "managed_legacy"
)

// Verification statuses (matching job.ToolRecord).
const (
	VerificationStatusVerified   = "version_probed"
	VerificationStatusUnverified = "unverified"
	VerificationStatusFailed     = "failed"
	// VerificationStatusAuthenticated means the executable's SHA-256 matched a digest from a manifest whose
	// detached OpenPGP signature verified against the pinned release key. It is strictly stronger than
	// VerificationStatusVerified and is never assigned from a version probe alone.
	VerificationStatusAuthenticated = "authenticated_release"
)

// Tool operational statuses (matching job.ToolRecord).
const (
	StatusActive        = "active"
	StatusLastKnownGood = "last_known_good"
	StatusMissing       = "missing"
	StatusUnreachable   = "unreachable"
)

// Sentinel errors.
var (
	ErrToolNotFound        = errors.New("tool not found")
	ErrToolExecutionFailed = errors.New("tool execution failed")
	ErrToolProbeTimeout    = errors.New("tool probe timed out")

	// Managed acquisition errors. Each maps to a stable public code via AcquisitionErrorCode.
	ErrUnsupportedPlatform   = errors.New("managed yt-dlp acquisition is only supported on Windows amd64")
	ErrToolInUse             = errors.New("yt-dlp is in use by an active analysis or download")
	ErrAcquisitionNetwork    = errors.New("release download failed")
	ErrInvalidSignature      = errors.New("release signature verification failed")
	ErrHashMismatch          = errors.New("downloaded executable does not match the authenticated digest")
	ErrUntrustedSigningKey   = errors.New("release was not signed by the trusted yt-dlp signing key")
	ErrInvalidRelease        = errors.New("release metadata or manifest is invalid")
	ErrDowngradeRefused      = errors.New("refusing to replace the active yt-dlp with an older release")
	ErrActivationFailed      = errors.New("managed yt-dlp activation failed")
	ErrRestartRequired       = errors.New("yt-dlp was updated; restart GoDownloader to use it")
	ErrAcquisitionInProgress = errors.New("a yt-dlp installation or update is already in progress")
)

// ToolInfo represents the resolved state and provenance of an external tool.
type ToolInfo struct {
	Name               string    `json:"name"`
	Version            string    `json:"version"`
	PlatformArch       string    `json:"platformArch"`
	OwnershipMode      string    `json:"ownershipMode"`
	ExecutablePath     string    `json:"executablePath"`
	SHA256             string    `json:"sha256,omitempty"`
	VerificationStatus string    `json:"verificationStatus"`
	Status             string    `json:"status"`
	Provenance         string    `json:"provenance"`
	Diagnostic         string    `json:"diagnostic,omitempty"`
	ProbedAt           time.Time `json:"probedAt"`
}

// Available returns true if the tool is resolved and active, either version-probed or an authenticated release.
func (t *ToolInfo) Available() bool {
	return t != nil && t.Status == StatusActive &&
		(t.VerificationStatus == VerificationStatusVerified || t.VerificationStatus == VerificationStatusAuthenticated)
}
