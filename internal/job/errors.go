package job

import (
	"errors"
	"fmt"
)

// AppError represents an application-level error with a code.
type AppError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *AppError) Error() string {
	return e.Message
}

// Error codes for consistent API responses.
const (
	ErrInvalidRequest          = "INVALID_REQUEST"
	ErrInvalidURL              = "INVALID_URL"
	ErrJobNotFound             = "JOB_NOT_FOUND"
	ErrInvalidJobState         = "INVALID_JOB_STATE"
	ErrEngineError             = "ENGINE_ERROR"
	ErrRecoveryFailed          = "RECOVERY_FAILED"
	ErrInternalError           = "INTERNAL_ERROR"
	ErrMediaAnalysis           = "MEDIA_ANALYSIS_ERROR"
	ErrUnsupportedAction       = "UNSUPPORTED_ACTION"
	ErrQBitUnavailable         = "QBIT_UNAVAILABLE"
	ErrQBitAuth                = "QBIT_AUTH_FAILED"
	ErrQBitVersion             = "QBIT_UNSUPPORTED_VERSION"
	ErrInvalidMagnet           = "INVALID_MAGNET"
	ErrInvalidTorrentFile      = "INVALID_TORRENT_FILE"
	ErrMetadataTimeout         = "METADATA_TIMEOUT"
	ErrDuplicateTorrent        = "DUPLICATE_TORRENT"
	ErrNoFilesSelected         = "NO_FILES_SELECTED"
	ErrTorrentNotFound         = "TORRENT_NOT_FOUND"
	ErrInvalidPriority         = "INVALID_PRIORITY"
	ErrBatchLimitExceeded      = "BATCH_LIMIT_EXCEEDED"
	ErrInvalidStorageSelection = "INVALID_STORAGE_SELECTION"
	ErrInvalidDestination      = "INVALID_DESTINATION"
	ErrCategoryNotFound        = "CATEGORY_NOT_FOUND"
	ErrCategoryNameConflict    = "CATEGORY_NAME_CONFLICT"
	ErrInsufficientDiskSpace   = "INSUFFICIENT_DISK_SPACE"
	ErrFileConflict            = "FILE_CONFLICT"
	ErrStorageError            = "STORAGE_ERROR"
)

type TorrentFinalizeFailureKind string

const (
	TorrentFinalizePersistenceFailure TorrentFinalizeFailureKind = "persistence"
	TorrentFinalizeCleanupFailure     TorrentFinalizeFailureKind = "cleanup"
)

// TorrentFinalizeError represents a failure during torrent finalization.
type TorrentFinalizeError struct {
	Kind TorrentFinalizeFailureKind
	Err  error
}

func (e *TorrentFinalizeError) Error() string {
	return fmt.Sprintf("torrent finalization failed (%s): %v", e.Kind, e.Err)
}

func (e *TorrentFinalizeError) Unwrap() error {
	return e.Err
}

// Sentinel errors for scheduler reconciliation
var (
	ErrDispatchPersistenceFailed = errors.New("dispatch persistence failed")
	ErrDispatchFailureHandled    = errors.New("dispatch failure already handled")
)

type DispatchFailureKind string

const (
	DispatchFailureExternalExecutionPersistence DispatchFailureKind = "external_execution_persistence"
	DispatchFailureStatePersistence             DispatchFailureKind = "state_persistence"
)

// DispatchHandledError indicates that a dispatch failure was already durably persisted to DB and published by Manager.
type DispatchHandledError struct {
	Err error
}

func (e *DispatchHandledError) Error() string {
	return e.Err.Error()
}

func (e *DispatchHandledError) Unwrap() error {
	return e.Err
}

func (e *DispatchHandledError) Is(target error) bool {
	return target == ErrDispatchFailureHandled
}

// DispatchPersistenceError represents a failure to persist state during dispatch.
type DispatchPersistenceError struct {
	JobID        string
	EngineID     string
	Action       QueueAction
	Kind         DispatchFailureKind
	TargetStatus JobStatus
	TargetError  string
	Err          error
}

func (e *DispatchPersistenceError) Error() string {
	return fmt.Sprintf("dispatch persistence failed for job %s: %v", e.JobID, e.Err)
}

func (e *DispatchPersistenceError) Unwrap() error {
	return e.Err
}

func (e *DispatchPersistenceError) Is(target error) bool {
	return target == ErrDispatchPersistenceFailed
}
