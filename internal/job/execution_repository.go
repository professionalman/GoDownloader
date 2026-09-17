package job

import "context"

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
