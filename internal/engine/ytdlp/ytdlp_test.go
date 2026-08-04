package ytdlp

import (
	"context"
	"os"
	"path/filepath"
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

func TestStructuredProgressLine_Scenarios(t *testing.T) {
	t.Run("1. Valid structured progress line with estimate", func(t *testing.T) {
		line := "download:__GODOWNLOADER_PROGRESS__:42.7%|10485760|NA|24576000|1048576.5|14"
		got := parseProgressLine(line)
		if got == nil {
			t.Fatalf("expected non-nil progress info")
		}
		if got.Percent != 42.7 {
			t.Errorf("expected 42.7, got %f", got.Percent)
		}
		if got.DownloadedBytes != 10485760 {
			t.Errorf("expected 10485760, got %d", got.DownloadedBytes)
		}
		if got.TotalBytes != 24576000 {
			t.Errorf("expected 24576000 (estimated), got %d", got.TotalBytes)
		}
		if got.Speed != 1048576 {
			t.Errorf("expected 1048576, got %d", got.Speed)
		}
		if got.ETASeconds != 14 {
			t.Errorf("expected 14, got %d", got.ETASeconds)
		}
	})

	t.Run("2. Leading spaces and percent symbol", func(t *testing.T) {
		line := "__GODOWNLOADER_PROGRESS__:  15.0% | 1500 | 10000 | 10000 | 500 | 5"
		got := parseProgressLine(line)
		if got == nil {
			t.Fatalf("expected non-nil progress info")
		}
		if got.Percent != 15.0 {
			t.Errorf("expected 15.0, got %f", got.Percent)
		}
	})

	t.Run("3. Both totals NA", func(t *testing.T) {
		line := "__GODOWNLOADER_PROGRESS__:10.0%|1000|NA|NA|500|10"
		got := parseProgressLine(line)
		if got == nil {
			t.Fatalf("expected non-nil progress info")
		}
		if got.TotalBytes != 0 {
			t.Errorf("expected total bytes 0, got %d", got.TotalBytes)
		}
	})

	t.Run("4. Clamping negative values", func(t *testing.T) {
		line := "__GODOWNLOADER_PROGRESS__:-10.0%|-50|-100|-100|-50|-5"
		got := parseProgressLine(line)
		if got == nil {
			t.Fatalf("expected non-nil progress info")
		}
		if got.Percent != 0 {
			t.Errorf("expected percent clamped to 0, got %f", got.Percent)
		}
		if got.DownloadedBytes != 0 || got.TotalBytes != 0 || got.Speed != 0 || got.ETASeconds != 0 {
			t.Errorf("expected negative values clamped to 0, got %+v", got)
		}
	})

	t.Run("5. Final path marker not parsed as progress", func(t *testing.T) {
		line := "__GODOWNLOADER_FINAL_PATH__:C:\\video.mp4"
		got := parseProgressLine(line)
		if got != nil {
			t.Errorf("expected nil progress info for final path marker, got %+v", got)
		}
	})
}

func TestParseFinalPathLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		expected string
	}{
		{
			name:     "1. Windows path",
			line:     "__GODOWNLOADER_FINAL_PATH__:C:\\Temp\\video.mkv",
			expected: "C:\\Temp\\video.mkv",
		},
		{
			name:     "2. Spaces and commas",
			line:     "__GODOWNLOADER_FINAL_PATH__:C:\\Temp\\My File, Final.mkv",
			expected: "C:\\Temp\\My File, Final.mkv",
		},
		{
			name:     "3. Brackets and parentheses",
			line:     "__GODOWNLOADER_FINAL_PATH__:C:\\Temp\\Video [1080p] (Official).mp4",
			expected: "C:\\Temp\\Video [1080p] (Official).mp4",
		},
		{
			name:     "4. Unicode title",
			line:     "__GODOWNLOADER_FINAL_PATH__:C:\\Temp\\Barbaadiyan (Full Video) Shiddat Sunny K.webm",
			expected: "C:\\Temp\\Barbaadiyan (Full Video) Shiddat Sunny K.webm",
		},
		{
			name:     "5. Apostrophe in title",
			line:     "__GODOWNLOADER_FINAL_PATH__:C:\\Temp\\Song's Title.m4a",
			expected: "C:\\Temp\\Song's Title.m4a",
		},
		{
			name:     "6. Quoted path",
			line:     "__GODOWNLOADER_FINAL_PATH__:\"C:\\Temp\\video.mkv\"",
			expected: "C:\\Temp\\video.mkv",
		},
		{
			name:     "7. Ordinary progress lines return empty",
			line:     "download:45.5%|47710208|104857600|5505024|10",
			expected: "",
		},
		{
			name:     "8. [download] Destination is not final path marker",
			line:     "[download] Destination: C:\\Temp\\video.f137.webm",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFinalPathLine(tt.line)
			if got != tt.expected {
				t.Errorf("parseFinalPathLine(%q) = %q, expected %q", tt.line, got, tt.expected)
			}
		})
	}
}

func TestDualStream_LineHandling(t *testing.T) {
	eng := NewEngine("ytdlp", "ffmpeg")
	state := &downloadState{}

	// Progress on stdout
	eng.handleYTDLPLine("job1", state, "__GODOWNLOADER_PROGRESS__:25.0%|2500|10000|10000|1000|10", "stdout")
	if state.progress.Percent != 25.0 {
		t.Fatalf("expected stdout progress 25.0, got %f", state.progress.Percent)
	}

	// Progress on stderr
	eng.handleYTDLPLine("job1", state, "__GODOWNLOADER_PROGRESS__:50.0%|5000|10000|10000|1000|5", "stderr")
	if state.progress.Percent != 50.0 {
		t.Fatalf("expected stderr progress 50.0, got %f", state.progress.Percent)
	}
}

func TestPathPrecedence(t *testing.T) {
	eng := NewEngine("ytdlp", "ffmpeg")
	state := &downloadState{
		candidateOutputPath: "C:\\Temp\\intermediate.webm",
		finalOutputPath:     "C:\\Temp\\final_merged.mkv",
		done:                true,
		progress:            progressInfo{Percent: 100, TotalBytes: 50000},
	}
	eng.downloads["job-precedence-test"] = state

	j := &job.Job{ID: "job-precedence-test"}
	st, err := eng.Status(context.Background(), j)
	if err != nil {
		t.Fatalf("expected Status to succeed, got %v", err)
	}
	if st.OutputPath != "C:\\Temp\\final_merged.mkv" {
		t.Errorf("expected OutputPath C:\\Temp\\final_merged.mkv, got %q", st.OutputPath)
	}
	if st.FileName != "final_merged.mkv" {
		t.Errorf("expected FileName final_merged.mkv, got %q", st.FileName)
	}
}

func TestValidateFinalPath(t *testing.T) {
	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "output.mkv")
	if err := os.WriteFile(validFile, []byte("media content"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	t.Run("Valid file in workDir", func(t *testing.T) {
		got, err := validateFinalPath(tmpDir, validFile)
		if err != nil {
			t.Fatalf("expected path to be valid, got %v", err)
		}
		if got != validFile {
			t.Errorf("expected %q, got %q", validFile, got)
		}
	})

	t.Run("Relative path resolves under workDir", func(t *testing.T) {
		got, err := validateFinalPath(tmpDir, "output.mkv")
		if err != nil {
			t.Fatalf("expected relative path to validate, got %v", err)
		}
		if got != validFile {
			t.Errorf("expected %q, got %q", validFile, got)
		}
	})

	t.Run("Non-existent file rejected", func(t *testing.T) {
		_, err := validateFinalPath(tmpDir, filepath.Join(tmpDir, "nonexistent.mkv"))
		if err == nil {
			t.Error("expected error for non-existent file")
		}
	})

	t.Run("Directory path rejected", func(t *testing.T) {
		_, err := validateFinalPath(tmpDir, subDir)
		if err == nil {
			t.Error("expected error for directory path")
		}
	})

	t.Run("Path outside workDir rejected", func(t *testing.T) {
		outsideFile := filepath.Join(filepath.Dir(tmpDir), "outside.mp4")
		_, err := validateFinalPath(tmpDir, outsideFile)
		if err == nil {
			t.Error("expected error for path escaping work directory")
		}
	})
}

func TestDiscoverFinalMediaFile(t *testing.T) {
	t.Run("Single valid media file returned", func(t *testing.T) {
		tmpDir := t.TempDir()
		mediaFile := filepath.Join(tmpDir, "video.mp4")
		_ = os.WriteFile(mediaFile, []byte("data"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, ".godownloader-workdir"), []byte("marker"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "video.f137.part"), []byte("part"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "thumb.jpg"), []byte("img"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "info.info.json"), []byte("{}"), 0644)

		got, err := discoverFinalMediaFile(tmpDir)
		if err != nil {
			t.Fatalf("expected discovery to succeed, got %v", err)
		}
		if got != mediaFile {
			t.Errorf("expected %q, got %q", mediaFile, got)
		}
	})

	t.Run("Zero plausible files returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		_ = os.WriteFile(filepath.Join(tmpDir, ".godownloader-workdir"), []byte("marker"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "video.part"), []byte("part"), 0644)

		_, err := discoverFinalMediaFile(tmpDir)
		if err == nil {
			t.Error("expected error when zero plausible files exist")
		}
	})

	t.Run("Multiple plausible files returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		_ = os.WriteFile(filepath.Join(tmpDir, "video1.mp4"), []byte("data1"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "video2.mkv"), []byte("data2"), 0644)

		_, err := discoverFinalMediaFile(tmpDir)
		if err == nil {
			t.Error("expected error when multiple plausible media files exist")
		}
	})
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

	if sel == "137" {
		t.Fatalf("expected format selector to pair audio, got plain video ID %q", sel)
	}

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

	noSelJob := &job.Job{
		ID:     "job-nosel",
		Source: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
	}

	selDefault := buildFormatSelector(noSelJob)
	if selDefault != "bestvideo+bestaudio/best" {
		t.Fatalf("expected bestvideo+bestaudio/best, got %q", selDefault)
	}
}
