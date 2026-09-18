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
	OwnershipManaged       = "managed"        // GoDownloader manages this binary (yt-dlp)
	OwnershipExternalUser  = "external_user"  // System or user-provided tool (FFmpeg, qBittorrent)
	OwnershipCompatibility = "compatibility"  // Transition / compatibility tool (aria2)
)

// Provenance types.
const (
	ProvenanceManaged        = "managed"         // In application-owned managed directory (dataDir/bin)
	ProvenanceUserConfigured = "user_configured" // Explicitly configured by user in config or environment
	ProvenanceSystemPath     = "system_path"     // Discovered via system PATH lookup
	ProvenanceExternalAPI    = "external_api"    // Verified external service / daemon endpoint
)

// Verification statuses (matching job.ToolRecord).
const (
	VerificationStatusVerified   = "version_probed"
	VerificationStatusUnverified = "unverified"
	VerificationStatusFailed     = "failed"
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
	ErrAcquisitionGated    = errors.New("remote tool acquisition is gated pending signed manifest verification (ADR-SYN2-006)")
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

// Available returns true if the tool is resolved and active.
func (t *ToolInfo) Available() bool {
	return t != nil && t.Status == StatusActive && t.VerificationStatus == VerificationStatusVerified
}
