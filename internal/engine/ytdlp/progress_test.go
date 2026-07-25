package ytdlp

import (
	"testing"
)

func TestParseOutputPathLine_Patterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "download destination",
			input:    "[download] Destination: video.webm",
			expected: "video.webm",
		},
		{
			name:     "merger destination",
			input:    `[Merger] Merging formats into "video.mp4"`,
			expected: "video.mp4",
		},
		{
			name:     "ffmpeg merger destination",
			input:    `[ffmpeg] Merging formats into "output.mp4"`,
			expected: "output.mp4",
		},
		{
			name:     "extract audio destination",
			input:    "[ExtractAudio] Destination: song.mp3",
			expected: "song.mp3",
		},
		{
			name:     "ffmpeg destination",
			input:    "[ffmpeg] Destination: converted.m4a",
			expected: "converted.m4a",
		},
		{
			name:     "video convertor destination",
			input:    `[VideoConvertor] Converting video to "clip.mp4"`,
			expected: "clip.mp4",
		},
		{
			name:     "already downloaded",
			input:    "[download] file.mkv has already been downloaded",
			expected: "file.mkv",
		},
		{
			name:     "unrelated line",
			input:    "[download]  42.3% of  123.45MiB at  5.67MiB/s ETA 00:15",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOutputPathLine(tt.input)
			if got != tt.expected {
				t.Errorf("parseOutputPathLine(%q) = %q; expected %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestParseOutputPathLine_PostprocessorOverridesRawDownload(t *testing.T) {
	lines := []string{
		"[download] Destination: video.f137.webm",
		"[download]  100% of 10.00MiB in 00:01",
		"[ExtractAudio] Destination: song.mp3",
	}

	var currentPath string
	for _, line := range lines {
		if path := parseOutputPathLine(line); path != "" {
			currentPath = path
		}
	}

	if currentPath != "song.mp3" {
		t.Errorf("expected final OutputPath to be 'song.mp3', got %q", currentPath)
	}
}
