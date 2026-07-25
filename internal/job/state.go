package job

import (
	"fmt"
	"time"
)

// State machine transition policies:
// - User-command transitions: validated using ValidateTransition before execution.
// - Engine/status reconciliation: controlled centrally by UpdateJobFromEngine during live monitoring.
// - Startup recovery reconciliation: controlled by recoverJob, directly reconciling persisted database state against external daemon status.
var validTransitions = map[JobStatus][]JobStatus{
	StatusQueued:            {StatusDownloading, StatusPaused, StatusCancelled, StatusFailed, StatusAnalyzing},
	StatusDownloading:       {StatusPaused, StatusFailed, StatusCompleted, StatusCancelled, StatusProcessing, StatusSeeding},
	StatusPaused:            {StatusQueued, StatusDownloading, StatusCancelled},
	StatusFailed:            {StatusQueued, StatusAnalyzing},
	StatusAnalyzing:         {StatusQueued, StatusDownloading, StatusFailed, StatusCancelled, StatusAwaitingSelection},
	StatusProcessing:        {StatusCompleted, StatusFailed, StatusCancelled},
	StatusAwaitingSelection: {StatusQueued, StatusDownloading, StatusCancelled},
	StatusSeeding:           {StatusCompleted, StatusFailed},
	// completed and cancelled are terminal
}

// ValidateTransition checks if a state transition is allowed.
func ValidateTransition(from, to JobStatus) error {
	allowed, exists := validTransitions[from]
	if !exists {
		return fmt.Errorf("no transitions allowed from status %q", from)
	}
	for _, s := range allowed {
		if s == to {
			return nil
		}
	}
	return fmt.Errorf("invalid transition from %q to %q", from, to)
}

// TransitionJob validates and applies a state transition on a job.
func TransitionJob(j *Job, to JobStatus) error {
	if err := ValidateTransition(j.Status, to); err != nil {
		return err
	}
	j.Status = to
	j.UpdatedAt = time.Now()
	return nil
}

// IsTerminal returns true if the status is a terminal state.
func IsTerminal(s JobStatus) bool {
	return s == StatusCompleted || s == StatusCancelled
}

// IsActive returns true if the job is actively downloading.
func IsActive(s JobStatus) bool {
	return s == StatusDownloading || s == StatusProcessing || s == StatusSeeding
}

// IsRecoverable returns true if the job should be recovered on restart.
func IsRecoverable(s JobStatus) bool {
	return s == StatusQueued || s == StatusDownloading || s == StatusPaused || s == StatusAnalyzing || s == StatusAwaitingSelection || s == StatusSeeding
}
