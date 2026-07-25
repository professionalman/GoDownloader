package ytdlp

import (
	"context"
	"testing"

	"downloader/internal/job"
)

func TestParseProgressLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected *progressInfo
	}{
		{
			name: "Structured progress line",
			line: "download:45.5%|47710208|104857600|5505024|10",
		},
		{
			name: "Standard progress line",
			line: "[download]  45.5% of 100.00MiB at  5.25MiB/s ETA 00:10",
		},
		{
			name: "Post-processing line",
			line: "[ExtractAudio] Destination: song.mp3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseProgressLine(tt.line)
			if tt.name == "Standard progress line" || tt.name == "Structured progress line" {
				if got == nil {
					t.Fatalf("expected non-nil progress info")
				}
				if got.Percent != 45.5 {
					t.Errorf("expected percent 45.5, got %f", got.Percent)
				}
				if got.TotalBytes != 104857600 {
					t.Errorf("expected total bytes 104857600, got %d", got.TotalBytes)
				}
			} else {
				if got != nil {
					t.Errorf("expected nil for non-progress line, got %+v", got)
				}
			}
		})
	}
}

func TestDownloadState_CancelledStatus(t *testing.T) {
	eng := NewEngine("ytdlp", "ffmpeg")
	state := &downloadState{
		done:      true,
		cancelled: true,
	}
	eng.downloads["job-cancel-test"] = state

	j := &job.Job{ID: "job-cancel-test"}
	status, err := eng.Status(context.Background(), j)
	if err != nil {
		t.Fatalf("expected Status to succeed, got %v", err)
	}
	if status.Status != job.StatusCancelled {
		t.Errorf("expected status cancelled, got %s", status.Status)
	}
}

func TestYTDLP_TerminalStatusRemainsUntilExplicitCleanup(t *testing.T) {
	eng := NewEngine("ytdlp", "ffmpeg")

	state := &downloadState{
		done:     true,
		progress: progressInfo{Percent: 100, TotalBytes: 5000},
	}
	eng.downloads["job-idempotent-test"] = state

	j := &job.Job{ID: "job-idempotent-test"}

	// 1. First status read returns COMPLETED
	s1, err := eng.Status(context.Background(), j)
	if err != nil || s1.Status != job.StatusCompleted {
		t.Fatalf("first status read failed or returned wrong status: %v, status=%v", err, s1)
	}

	// 2. Second status read MUST still return COMPLETED (idempotent status)
	s2, err := eng.Status(context.Background(), j)
	if err != nil || s2.Status != job.StatusCompleted {
		t.Fatalf("second status read failed; status must be idempotent: %v, status=%v", err, s2)
	}

	// 3. Explicit cleanup removes tracking
	eng.Cleanup("job-idempotent-test")

	// 4. Status read after explicit cleanup fails
	_, err = eng.Status(context.Background(), j)
	if err == nil {
		t.Error("expected Status to fail after explicit Cleanup()")
	}
}

func TestEngine_ShutdownCancelsActiveJobs(t *testing.T) {
	eng := NewEngine("ytdlp", "ffmpeg")

	var cancelled bool
	state := &downloadState{
		done: false,
		cancel: func() {
			cancelled = true
		},
	}
	eng.downloads["job-shutdown-test"] = state

	eng.Shutdown()
	if !cancelled {
		t.Error("expected active download cancel function to be called on Shutdown()")
	}
	if len(eng.downloads) != 0 {
		t.Errorf("expected downloads map to be empty after Shutdown, got len %d", len(eng.downloads))
	}
}
