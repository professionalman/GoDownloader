package process

import (
	"context"
	"errors"
	"os/exec"
	"time"
)

// ProcessState represents the lifecycle state of a managed child process.
type ProcessState string

const (
	StateRunning    ProcessState = "running"
	StateExited     ProcessState = "exited"
	StateTerminated ProcessState = "terminated"
)

// ProcessSpec defines the identity and purpose of a process to be supervised.
type ProcessSpec struct {
	ID      string // Unique identifier for the process (typically JobID or task-specific ID)
	JobID   string // Associated Job ID if applicable
	Tool    string // Tool name (e.g. "yt-dlp", "ffmpeg")
	Purpose string // Purpose (e.g. "download", "mux")
}

// ProcessInfo represents a read-only snapshot of a managed process.
type ProcessInfo struct {
	ID         string       `json:"id"`
	JobID      string       `json:"jobId,omitempty"`
	Tool       string       `json:"tool"`
	Purpose    string       `json:"purpose"`
	PID        int          `json:"pid"`
	State      ProcessState `json:"state"`
	ExitCode   int          `json:"exitCode"`
	Terminated bool         `json:"terminated"`
	StartedAt  time.Time    `json:"startedAt"`
	ExitedAt   *time.Time   `json:"exitedAt,omitempty"`
}

// Sentinel errors for the process supervisor.
var (
	ErrSupervisorClosed      = errors.New("process supervisor is closed")
	ErrProcessNotFound       = errors.New("process not found")
	ErrProcessAlreadyRunning = errors.New("process with this ID is already running")
	ErrProcessTreeAttach     = errors.New("failed to attach process to supervision tree")
)

// ISupervisor defines the contract for supervising owned child process trees.
type ISupervisor interface {
	StartOwned(ctx context.Context, cmd *exec.Cmd, spec ProcessSpec) (*ManagedProcess, error)
	Terminate(id string) error
	TerminateAll() error
	Shutdown(ctx context.Context) error
	Get(id string) *ProcessInfo
	ActiveCount() int
	List() []*ProcessInfo
}
