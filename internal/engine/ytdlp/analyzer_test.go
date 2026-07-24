package ytdlp

import (
	"testing"
)

func TestNormalizeFormats(t *testing.T) {
	raw := []ytdlpFormat{
		{
			FormatID:   "137",
			Extension:  "mp4",
			Width:      1920,
			Height:     1080,
			FileSize:   100000000,
			VCodec:     "avc1.640028",
			ACodec:     "none",
			FPS:        60,
			FormatNote: "1080p60",
		},
		{
			FormatID:   "140",
			Extension:  "m4a",
			Width:      0,
			Height:     0,
			FileSize:   5000000,
			VCodec:     "none",
			ACodec:     "mp4a.40.2",
			FPS:        0,
			FormatNote: "medium quality audio",
		},
		{
			FormatID:   "22",
			Extension:  "mp4",
			Width:      1280,
			Height:     720,
			FileSize:   50000000,
			VCodec:     "avc1.64001f",
			ACodec:     "mp4a.40.2",
			FPS:        30,
			FormatNote: "720p",
		},
		{
			// Skip format (both none)
			FormatID:  "sb0",
			Extension: "mhtml",
			VCodec:    "none",
			ACodec:    "none",
		},
	}

	formats := normalizeFormats(raw)

	if len(formats) != 3 {
		t.Fatalf("expected 3 normalized formats, got %d", len(formats))
	}

	// Format 22 has both video and audio, so it should be ranked first by qualityScore
	if formats[0].FormatID != "22" {
		t.Errorf("expected format 22 (video+audio) to be first, got %s", formats[0].FormatID)
	}

	if formats[0].Quality != "720p" {
		t.Errorf("expected quality 720p, got %s", formats[0].Quality)
	}

	// Format 137 is video-only 1080p
	found137 := false
	for _, f := range formats {
		if f.FormatID == "137" {
			found137 = true
			if f.Quality != "1080p60" {
				t.Errorf("expected quality 1080p60, got %s", f.Quality)
			}
			if f.Resolution != "1920x1080" {
				t.Errorf("expected resolution 1920x1080, got %s", f.Resolution)
			}
		}
	}
	if !found137 {
		t.Error("format 137 not found in normalized output")
	}

	// Audio-only format
	found140 := false
	for _, f := range formats {
		if f.FormatID == "140" {
			found140 = true
			if f.Quality != "audio only" {
				t.Errorf("expected quality 'audio only', got %s", f.Quality)
			}
		}
	}
	if !found140 {
		t.Error("format 140 not found in normalized output")
	}
}

func TestCleanError(t *testing.T) {
	stderr := `[youtube] Extracting URL...
ERROR: [youtube] dQw4w9WgXcQ: Video unavailable. This video is private
`
	cleaned := cleanError(stderr)
	expected := "[youtube] dQw4w9WgXcQ: Video unavailable. This video is private"
	if cleaned != expected {
		t.Errorf("expected clean error %q, got %q", expected, cleaned)
	}
}
