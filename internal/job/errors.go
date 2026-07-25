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
	ErrInvalidRequest     = "INVALID_REQUEST"
	ErrInvalidURL         = "INVALID_URL"
	ErrJobNotFound        = "JOB_NOT_FOUND"
	ErrInvalidJobState    = "INVALID_JOB_STATE"
	ErrEngineError        = "ENGINE_ERROR"
	ErrRecoveryFailed     = "RECOVERY_FAILED"
	ErrInternalError      = "INTERNAL_ERROR"
	ErrMediaAnalysis      = "MEDIA_ANALYSIS_ERROR"
	ErrUnsupportedAction  = "UNSUPPORTED_ACTION"
	ErrQBitUnavailable    = "QBIT_UNAVAILABLE"
	ErrQBitAuth           = "QBIT_AUTH_FAILED"
	ErrQBitVersion        = "QBIT_UNSUPPORTED_VERSION"
	ErrInvalidMagnet      = "INVALID_MAGNET"
	ErrInvalidTorrentFile = "INVALID_TORRENT_FILE"
	ErrMetadataTimeout    = "METADATA_TIMEOUT"
	ErrDuplicateTorrent   = "DUPLICATE_TORRENT"
	ErrNoFilesSelected    = "NO_FILES_SELECTED"
	ErrTorrentNotFound    = "TORRENT_NOT_FOUND"
	ErrInvalidPriority    = "INVALID_PRIORITY"
	ErrBatchLimitExceeded = "BATCH_LIMIT_EXCEEDED"
)

// Sentinel errors for scheduler reconciliation
var (
	ErrDispatchPersistenceFailed = errors.New("dispatch persistence failed")
)

// DispatchPersistenceError represents a failure to persist state after an engine operation succeeded.
type DispatchPersistenceError struct {
	JobID    string
	EngineID string
	Action   QueueAction
	Err      error
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
