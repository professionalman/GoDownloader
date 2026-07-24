package ytdlp

import (
	"testing"
)

func TestParseProgressLine_Normal(t *testing.T) {
	line := "[download]  42.3% of  123.45MiB at  5.67MiB/s ETA 00:15"
	info := parseProgressLine(line)
	if info == nil {
		t.Fatal("expected non-nil progressInfo")
	}

	if info.Percent != 42.3 {
		t.Errorf("expected percent 42.3, got %f", info.Percent)
	}

	expectedTotal := parseSizeString("123.45MiB")
	if info.TotalBytes != expectedTotal {
		t.Errorf("expected total bytes %d, got %d", expectedTotal, info.TotalBytes)
	}

	expectedSpeed := parseSpeedString("5.67MiB/s")
	if info.Speed != expectedSpeed {
		t.Errorf("expected speed %d, got %d", expectedSpeed, info.Speed)
	}

	if info.ETASeconds != 15 {
		t.Errorf("expected ETA 15s, got %d", info.ETASeconds)
	}
}

func TestParseProgressLine_Completed(t *testing.T) {
	line := "[download] 100% of 123.45MiB"
	info := parseProgressLine(line)
	if info == nil {
		t.Fatal("expected non-nil progressInfo")
	}

	if info.Percent != 100.0 {
		t.Errorf("expected 100%%, got %f", info.Percent)
	}
	if info.Speed != 0 {
		t.Errorf("expected speed 0, got %d", info.Speed)
	}
	if info.ETASeconds != 0 {
		t.Errorf("expected ETA 0, got %d", info.ETASeconds)
	}
}

func TestParseProgressLine_InvalidOrNonProgress(t *testing.T) {
	lines := []string{
		"[youtube] Extracting URL: https://youtube.com/watch?v=123",
		"[info] Downloading video",
		"random output line",
		"",
	}

	for _, line := range lines {
		if info := parseProgressLine(line); info != nil {
			t.Errorf("expected nil for non-progress line %q, got %+v", line, info)
		}
	}
}

func TestParseSizeString(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"100B", 100},
		{"1.5KiB", 1536},
		{"10MiB", 10485760},
		{"1GiB", 1073741824},
		{"Unknown", 0},
		{"~", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := parseSizeString(tt.input)
		if got != tt.expected {
			t.Errorf("parseSizeString(%q) = %d, expected %d", tt.input, got, tt.expected)
		}
	}
}

func TestParseETAString(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
	}{
		{"00:15", 15},
		{"01:30", 90},
		{"01:02:03", 3723},
		{"Unknown", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := parseETAString(tt.input)
		if got != tt.expected {
			t.Errorf("parseETAString(%q) = %d, expected %d", tt.input, got, tt.expected)
		}
	}
}

func TestIsPostProcessingLine(t *testing.T) {
	lines := []struct {
		line     string
		expected bool
	}{
		{"[Merger] Merging formats into video.mp4", true},
		{"[ffmpeg] Destination: video.mp4", true},
		{"[ExtractAudio] Destination: audio.mp3", true},
		{"[download] 50% of 10MiB", false},
	}

	for _, tt := range lines {
		got := isPostProcessingLine(tt.line)
		if got != tt.expected {
			t.Errorf("isPostProcessingLine(%q) = %v, expected %v", tt.line, got, tt.expected)
		}
	}
}
