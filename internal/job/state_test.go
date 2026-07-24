package job

import (
	"testing"
	"time"
)

func TestValidateTransition_Valid(t *testing.T) {
	valid := []struct {
		from, to JobStatus
	}{
		{StatusQueued, StatusDownloading},
		{StatusQueued, StatusAnalyzing},
		{StatusQueued, StatusCancelled},
		{StatusQueued, StatusFailed},
		{StatusAnalyzing, StatusDownloading},
		{StatusAnalyzing, StatusFailed},
		{StatusAnalyzing, StatusCancelled},
		{StatusDownloading, StatusPaused},
		{StatusDownloading, StatusFailed},
		{StatusDownloading, StatusCompleted},
		{StatusDownloading, StatusCancelled},
		{StatusDownloading, StatusProcessing},
		{StatusProcessing, StatusCompleted},
		{StatusProcessing, StatusFailed},
		{StatusProcessing, StatusCancelled},
		{StatusPaused, StatusDownloading},
		{StatusPaused, StatusCancelled},
		{StatusFailed, StatusQueued},
		{StatusAnalyzing, StatusAwaitingSelection},
		{StatusAwaitingSelection, StatusDownloading},
		{StatusAwaitingSelection, StatusCancelled},
		{StatusDownloading, StatusSeeding},
		{StatusSeeding, StatusCompleted},
		{StatusSeeding, StatusFailed},
	}

	for _, tt := range valid {
		if err := ValidateTransition(tt.from, tt.to); err != nil {
			t.Errorf("expected valid transition %s → %s, got error: %v", tt.from, tt.to, err)
		}
	}
}

func TestValidateTransition_Invalid(t *testing.T) {
	invalid := []struct {
		from, to JobStatus
	}{
		{StatusCompleted, StatusPaused},
		{StatusCompleted, StatusDownloading},
		{StatusCancelled, StatusDownloading},
		{StatusCancelled, StatusPaused},
		{StatusQueued, StatusCompleted},
		{StatusAwaitingSelection, StatusCompleted},
		{StatusSeeding, StatusDownloading},
	}

	for _, tt := range invalid {
		if err := ValidateTransition(tt.from, tt.to); err == nil {
			t.Errorf("expected invalid transition %s → %s to return error, got nil", tt.from, tt.to)
		}
	}
}

func TestTransitionJob(t *testing.T) {
	now := time.Now().Add(-1 * time.Minute)
	j := &Job{
		ID:        "test-1",
		Status:    StatusQueued,
		UpdatedAt: now,
	}

	if err := TransitionJob(j, StatusDownloading); err != nil {
		t.Fatalf("expected transition to succeed, got: %v", err)
	}

	if j.Status != StatusDownloading {
		t.Errorf("expected status %s, got %s", StatusDownloading, j.Status)
	}

	if !j.UpdatedAt.After(now) {
		t.Errorf("expected UpdatedAt to be updated")
	}
}

func TestTransitionJob_Invalid(t *testing.T) {
	j := &Job{ID: "test-2", Status: StatusCompleted}

	if err := TransitionJob(j, StatusPaused); err == nil {
		t.Errorf("expected error for invalid transition, got nil")
	}

	if j.Status != StatusCompleted {
		t.Errorf("expected status to remain %s, got %s", StatusCompleted, j.Status)
	}
}

func TestIsTerminal(t *testing.T) {
	if !IsTerminal(StatusCompleted) {
		t.Errorf("expected completed to be terminal")
	}
	if !IsTerminal(StatusCancelled) {
		t.Errorf("expected cancelled to be terminal")
	}
	if IsTerminal(StatusFailed) {
		t.Errorf("expected failed to NOT be terminal")
	}
	if IsTerminal(StatusDownloading) {
		t.Errorf("expected downloading to NOT be terminal")
	}
}

func TestIsActive(t *testing.T) {
	if !IsActive(StatusDownloading) {
		t.Errorf("expected downloading to be active")
	}
	if !IsActive(StatusProcessing) {
		t.Errorf("expected processing to be active")
	}
	if IsActive(StatusPaused) {
		t.Errorf("expected paused to NOT be active")
	}
	if IsActive(StatusQueued) {
		t.Errorf("expected queued to NOT be active")
	}
	if !IsActive(StatusSeeding) {
		t.Errorf("expected seeding to be active")
	}
}

func TestIsRecoverable(t *testing.T) {
	if !IsRecoverable(StatusQueued) {
		t.Errorf("expected queued to be recoverable")
	}
	if !IsRecoverable(StatusDownloading) {
		t.Errorf("expected downloading to be recoverable")
	}
	if !IsRecoverable(StatusPaused) {
		t.Errorf("expected paused to be recoverable")
	}
	if !IsRecoverable(StatusAnalyzing) {
		t.Errorf("expected analyzing to be recoverable")
	}
	if !IsRecoverable(StatusAwaitingSelection) {
		t.Errorf("expected awaiting selection to be recoverable")
	}
	if !IsRecoverable(StatusSeeding) {
		t.Errorf("expected seeding to be recoverable")
	}
	if IsRecoverable(StatusCompleted) {
		t.Errorf("expected completed to NOT be recoverable")
	}
	if IsRecoverable(StatusFailed) {
		t.Errorf("expected failed to NOT be recoverable")
	}
}
