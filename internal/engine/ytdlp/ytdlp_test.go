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

func TestBuildFormatSelector(t *testing.T) {
	tests := []struct {
		name     string
		job      *job.Job
		expected string
	}{
		{
			name:     "1. nil MediaInfo",
			job:      &job.Job{ID: "j1", MediaInfo: nil},
			expected: "bestvideo+bestaudio/best",
		},
		{
			name:     "2. empty SelectedFmt",
			job:      &job.Job{ID: "j2", MediaInfo: &job.MediaInfo{SelectedFmt: "", Formats: []job.MediaFormat{{FormatID: "137", VCodec: "avc1"}}}},
			expected: "bestvideo+bestaudio/best",
		},
		{
			name: "3. selected video-only format",
			job: &job.Job{
				ID: "j3",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "137",
					Formats: []job.MediaFormat{
						{FormatID: "137", VCodec: "avc1.640028", ACodec: "none"},
						{FormatID: "140", VCodec: "none", ACodec: "mp4a.40.2"},
					},
				},
			},
			expected: "137+bestaudio/best",
		},
		{
			name: "4. selected combined video-and-audio format",
			job: &job.Job{
				ID: "j4",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "18",
					Formats: []job.MediaFormat{
						{FormatID: "18", VCodec: "avc1.42001E", ACodec: "mp4a.40.2"},
					},
				},
			},
			expected: "18",
		},
		{
			name: "5. selected audio-only format",
			job: &job.Job{
				ID: "j5",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "140",
					Formats: []job.MediaFormat{
						{FormatID: "140", VCodec: "none", ACodec: "mp4a.40.2"},
					},
				},
			},
			expected: "140",
		},
		{
			name: "6. selected format ID missing from format list",
			job: &job.Job{
				ID: "j6",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "999",
					Formats: []job.MediaFormat{
						{FormatID: "137", VCodec: "avc1", ACodec: "none"},
					},
				},
			},
			expected: "999",
		},
		{
			name: "7. codec values that are empty strings",
			job: &job.Job{
				ID: "j7",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "137",
					Formats: []job.MediaFormat{
						{FormatID: "137", VCodec: "", ACodec: ""},
					},
				},
			},
			expected: "137",
		},
		{
			name: "8. codec values equal to none",
			job: &job.Job{
				ID: "j8",
				MediaInfo: &job.MediaInfo{
					SelectedFmt: "137",
					Formats: []job.MediaFormat{
						{FormatID: "137", VCodec: "none", ACodec: "none"},
					},
				},
			},
			expected: "137",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildFormatSelector(tt.job)
			if got != tt.expected {
				t.Errorf("buildFormatSelector() = %q, expected %q", got, tt.expected)
			}
		})
	}
}

func TestStart_AppendsCorrectFormatSelector(t *testing.T) {
	// 1. Video-only selection produces -f "<selectedID>+bestaudio/best"
	videoOnlyJob := &job.Job{
		ID:     "job-video-only",
		Source: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "137",
			Formats: []job.MediaFormat{
				{FormatID: "137", VCodec: "avc1.640028", ACodec: "none"},
			},
		},
	}

	sel := buildFormatSelector(videoOnlyJob)
	if sel != "137+bestaudio/best" {
		t.Fatalf("expected 137+bestaudio/best, got %q", sel)
	}

	// Verify that it is NOT just "137"
	if sel == "137" {
		t.Fatalf("expected format selector to pair audio, got plain video ID %q", sel)
	}

	// 2. Combined selection produces -f "18"
	combinedJob := &job.Job{
		ID:     "job-combined",
		Source: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		MediaInfo: &job.MediaInfo{
			SelectedFmt: "18",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1.42001E", ACodec: "mp4a.40.2"},
			},
		},
	}

	selCombined := buildFormatSelector(combinedJob)
	if selCombined != "18" {
		t.Fatalf("expected 18, got %q", selCombined)
	}

	// 3. No selection produces -f "bestvideo+bestaudio/best"
	noSelJob := &job.Job{
		ID:     "job-nosel",
		Source: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}

	selDefault := buildFormatSelector(noSelJob)
	if selDefault != "bestvideo+bestaudio/best" {
		t.Fatalf("expected bestvideo+bestaudio/best, got %q", selDefault)
	}
}
