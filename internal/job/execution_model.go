package job

import "time"

// ExecutionStatus represents the state of a specific execution attempt.
type ExecutionStatus string

const (
	ExecutionStatusActive     ExecutionStatus = "active"
	ExecutionStatusCompleted  ExecutionStatus = "completed"
	ExecutionStatusFailed     ExecutionStatus = "failed"
	ExecutionStatusCancelled  ExecutionStatus = "cancelled"
	ExecutionStatusSuperseded ExecutionStatus = "superseded"
)

// EngineFamily classifies the underlying engine family.
type EngineFamily string

const (
	EngineFamilyAria2       EngineFamily = "aria2"
	EngineFamilyNativeHTTP  EngineFamily = "native_http"
	EngineFamilyQBittorrent EngineFamily = "qbittorrent"
	EngineFamilyYtDlp       EngineFamily = "ytdlp"
)

// RuntimeMode indicates whether the engine runtime is managed, system, or external.
type RuntimeMode string

const (
	RuntimeModeManaged             RuntimeMode = "managed"
	RuntimeModeSystem              RuntimeMode = "system"
	RuntimeModeExternalUserManaged RuntimeMode = "external_user_managed"
	RuntimeModePrivateManaged      RuntimeMode = "private_managed"
)

// JobExecution represents a concrete execution attempt for a Job.
type JobExecution struct {
	ID                    string          `json:"id"`
	JobID                 string          `json:"jobId"`
	AttemptNumber         int             `json:"attemptNumber"`
	EngineFamily          EngineFamily    `json:"engineFamily"`
	RuntimeMode           RuntimeMode     `json:"runtimeMode"`
	EngineCorrelationID   string          `json:"engineCorrelationId,omitempty"`
	Status                ExecutionStatus `json:"status"`
	FailureClassification string          `json:"failureClassification,omitempty"`
	FailureDetail         string          `json:"failureDetail,omitempty"`
	StartedAt             time.Time       `json:"startedAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
	CompletedAt           *time.Time      `json:"completedAt,omitempty"`
}

// HTTPCheckpoint represents the persistent checkpoint header for a native HTTP download.
type HTTPCheckpoint struct {
	JobID             string    `json:"jobId"`
	ExecutionID       string    `json:"executionId,omitempty"`
	CheckpointVersion int       `json:"checkpointVersion"`
	EffectiveURL      string    `json:"effectiveUrl"`
	ETag              string    `json:"etag,omitempty"`
	LastModified      string    `json:"lastModified,omitempty"`
	ContentLength     int64     `json:"contentLength"`
	RangeSupported    bool      `json:"rangeSupported"`
	StagingPath       string    `json:"stagingPath"`
	VerifiedBytes     int64     `json:"verifiedBytes"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// HTTPCheckpointSegment represents a positional range/segment in a segmented HTTP download.
type HTTPCheckpointSegment struct {
	JobID         string    `json:"jobId"`
	SegmentIndex  int       `json:"segmentIndex"`
	StartOffset   int64     `json:"startOffset"`
	CurrentOffset int64     `json:"currentOffset"`
	EndOffset     int64     `json:"endOffset"`
	VerifiedBytes int64     `json:"verifiedBytes"`
	Completed     bool      `json:"completed"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ToolRecord represents an entry in the tool ledger (managed/system tools).
type ToolRecord struct {
	Name               string    `json:"name"`
	Version            string    `json:"version"`
	PlatformArch       string    `json:"platformArch"`
	OwnershipMode      string    `json:"ownershipMode"`
	ExecutablePath     string    `json:"executablePath"`
	SHA256             string    `json:"sha256,omitempty"`
	VerificationStatus string    `json:"verificationStatus"`
	Status             string    `json:"status"` // "active", "last_known_good", "deprecated", "quarantined"
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// EventCursor tracks the durable sequence cursor for StateSync.
type EventCursor struct {
	Scope          string    `json:"scope"`
	SequenceNumber int64     `json:"sequenceNumber"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// FinalizationPhase represents the phase of an artifact finalization journal record.
type FinalizationPhase string

const (
	FinalizationPhasePrepared       FinalizationPhase = "prepared"
	FinalizationPhaseInProgress     FinalizationPhase = "in_progress"
	FinalizationPhaseValidated      FinalizationPhase = "validated"
	FinalizationPhaseDBCommitted    FinalizationPhase = "db_committed"
	FinalizationPhaseCleanupPending FinalizationPhase = "cleanup_pending"
	FinalizationPhaseComplete       FinalizationPhase = "complete"
	FinalizationPhaseFailed         FinalizationPhase = "failed"
)

// FinalizationRecord represents a durable journal entry for atomic filesystem-to-DB artifact commitment.
type FinalizationRecord struct {
	ID              string            `json:"id"`
	JobID           string            `json:"jobId"`
	ExecutionID     string            `json:"executionId,omitempty"`
	StagingPath     string            `json:"stagingPath"`
	DestinationPath string            `json:"destinationPath"`
	ExpectedSize    int64             `json:"expectedSize"`
	ExpectedDigest  string            `json:"expectedDigest,omitempty"`
	ConflictPolicy  string            `json:"conflictPolicy"`
	Phase           FinalizationPhase `json:"phase"`
	CleanupPath     string            `json:"cleanupPath,omitempty"`
	Error           string            `json:"error,omitempty"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	CompletedAt     *time.Time        `json:"completedAt,omitempty"`
}
