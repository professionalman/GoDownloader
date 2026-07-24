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
			if tt.name == "Standard progress line" {
				if got == nil {
					t.Fatalf("expected non-nil progress info")
				}
				if got.Percent != 45.5 {
					t.Errorf("expected percent 45.5, got %f", got.Percent)
				}
				if got.TotalBytes != 100*1024*1024 {
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
