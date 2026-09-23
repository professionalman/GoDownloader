package job

// RecoveryIssueKind represents a typed, sanitized classification of a recovery intervention.
type RecoveryIssueKind string

const (
	RecoveryIssueInterruptedMedia             RecoveryIssueKind = "interrupted_media"
	RecoveryIssueFinalizationFailed           RecoveryIssueKind = "finalization_failed"
	RecoveryIssueEngineUnavailable            RecoveryIssueKind = "engine_unavailable"
	RecoveryIssueExternalStateUnrecoverable   RecoveryIssueKind = "external_state_unrecoverable"
	RecoveryIssueTorrentMetadataUnrecoverable RecoveryIssueKind = "torrent_metadata_unrecoverable"
)

// RecoveryIssue represents a specific job issue identified during startup recovery.
type RecoveryIssue struct {
	JobID string            `json:"jobId"`
	Kind  RecoveryIssueKind `json:"kind"`
}

// RecoverySummary provides an immutable snapshot of actual startup recovery actions.
type RecoverySummary struct {
	ReconciledFinalizations       int             `json:"reconciledFinalizations"`
	ReattachedTransfers           int             `json:"reattachedTransfers"`
	RestartedMetadataAcquisitions int             `json:"restartedMetadataAcquisitions"`
	InterruptedMediaJobs          int             `json:"interruptedMediaJobs"`
	Issues                        []RecoveryIssue `json:"issues,omitempty"`
}

// Clone returns a deep copy of the RecoverySummary ensuring the Issues slice cannot be mutated by callers.
func (s RecoverySummary) Clone() RecoverySummary {
	c := s
	if s.Issues != nil {
		c.Issues = make([]RecoveryIssue, len(s.Issues))
		copy(c.Issues, s.Issues)
	} else {
		c.Issues = []RecoveryIssue{}
	}
	return c
}
