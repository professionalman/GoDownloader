package job

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
	ErrInvalidRequest  = "INVALID_REQUEST"
	ErrInvalidURL      = "INVALID_URL"
	ErrJobNotFound     = "JOB_NOT_FOUND"
	ErrInvalidJobState = "INVALID_JOB_STATE"
	ErrEngineError     = "ENGINE_ERROR"
	ErrRecoveryFailed  = "RECOVERY_FAILED"
	ErrInternalError   = "INTERNAL_ERROR"
)
