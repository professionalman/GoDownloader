package job

import (
	"context"
	"errors"
)

// IExecutionRepository defines the persistence interface for execution attempts,
// native HTTP checkpoints, tool ledger records, event cursors, and finalization records.
type IExecutionRepository interface {
	// Execution attempts
	CreateExecution(ctx context.Context, exec *JobExecution) error
	UpdateExecution(ctx context.Context, exec *JobExecution) error
	GetLatestExecution(ctx context.Context, jobID string) (*JobExecution, error)
	GetExecutionByID(ctx context.Context, id string) (*JobExecution, error)
	ListExecutions(ctx context.Context, jobID string) ([]JobExecution, error)

	// HTTP checkpoints and segment topology
	SaveHTTPCheckpoint(ctx context.Context, cp *HTTPCheckpoint, segments []HTTPCheckpointSegment) error
	GetHTTPCheckpoint(ctx context.Context, jobID string) (*HTTPCheckpoint, []HTTPCheckpointSegment, error)
	UpdateHTTPSegmentProgress(ctx context.Context, jobID string, segmentIndex int, currentOffset int64, verifiedBytes int64, completed bool) error

	// Finalization journal
	SaveFinalizationRecord(ctx context.Context, rec *FinalizationRecord) error
	UpdateFinalizationPhase(ctx context.Context, id string, phase FinalizationPhase, errStr string) error
	GetPendingFinalizations(ctx context.Context) ([]FinalizationRecord, error)
	GetFinalizationByJobID(ctx context.Context, jobID string) (*FinalizationRecord, error)

	// Tool ledger records
	SaveToolRecord(ctx context.Context, rec *ToolRecord) error
	GetActiveTool(ctx context.Context, name string) (*ToolRecord, error)
	ListToolRecords(ctx context.Context, name string) ([]ToolRecord, error)

	// Event cursors for StateSync
	GetEventCursor(ctx context.Context, scope string) (int64, error)
	AdvanceEventCursor(ctx context.Context, scope string) (int64, error)
}

// ErrManagedLedgerConflict is returned when a ledger transition's expected prior state no longer holds.
var ErrManagedLedgerConflict = errors.New("managed tool ledger changed concurrently")

// IManagedToolLedger persists authenticated managed tool selection. It is deliberately separate from the
// generic tool_records observations so external version probes can never overwrite managed authority.
type IManagedToolLedger interface {
	// GetManagedTool returns the record for name in status (active or last_known_good), or nil.
	GetManagedTool(ctx context.Context, name, status string) (*ManagedToolRecord, error)
	// ListManagedTools returns all retained ledger rows for name.
	ListManagedTools(ctx context.Context, name string) ([]ManagedToolRecord, error)
	// CommitToolActivation atomically makes newActive the active record, demotes previousActive (which must be
	// the current active record, or nil when none exists) to last_known_good, and retires any older LKG.
	// It returns ErrManagedLedgerConflict if the current active record differs from previousActive.
	CommitToolActivation(ctx context.Context, newActive, previousActive *ManagedToolRecord) error
	// RestoreLastKnownGood atomically quarantines the active record failedActiveSHA and promotes the
	// last-known-good record lkgSHA to active. It returns ErrManagedLedgerConflict if either no longer matches.
	RestoreLastKnownGood(ctx context.Context, name, failedActiveSHA, lkgSHA string) error
}
