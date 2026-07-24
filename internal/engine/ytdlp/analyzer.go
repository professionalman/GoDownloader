package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"downloader/internal/job"
)

// ytdlpJSON is the raw JSON structure from yt-dlp --dump-json.
type ytdlpJSON struct {
	Title     string        `json:"title"`
	Duration  float64       `json:"duration"`
	Thumbnail string        `json:"thumbnail"`
	URL       string        `json:"webpage_url"`
	Formats   []ytdlpFormat `json:"formats"`
}

type ytdlpFormat struct {
	FormatID   string  `json:"format_id"`
	Extension  string  `json:"ext"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	FileSize   int64   `json:"filesize"`
	FileSizeAp int64   `json:"filesize_approx"`
	VCodec     string  `json:"vcodec"`
	ACodec     string  `json:"acodec"`
	FPS        float64 `json:"fps"`
	FormatNote string  `json:"format_note"`
	Quality    float64 `json:"quality"`
	TBR        float64 `json:"tbr"`
}

// Analyze runs yt-dlp --dump-json to extract media metadata.
func (e *Engine) Analyze(ctx context.Context, url string) (*job.MediaInfo, error) {
	args := []string{
		"--dump-json",
		"--no-download",
		"--no-playlist",
		"--no-warnings",
	}

	if e.ffmpegPath != "" {
		args = append(args, "--ffmpeg-location", e.ffmpegPath)
	}

	args = append(args, url)

	cmd := exec.CommandContext(ctx, e.ytdlpPath, args...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			return nil, fmt.Errorf("yt-dlp analysis failed: %s", cleanError(stderr))
		}
		return nil, fmt.Errorf("yt-dlp analysis failed: %w", err)
	}

	var raw ytdlpJSON
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse yt-dlp output: %w", err)
	}

	formats := normalizeFormats(raw.Formats)

	return &job.MediaInfo{
		Title:     raw.Title,
		Duration:  raw.Duration,
		Thumbnail: raw.Thumbnail,
		URL:       raw.URL,
		Formats:   formats,
	}, nil
}

// normalizeFormats converts raw yt-dlp formats into clean MediaFormat entries.
func normalizeFormats(rawFormats []ytdlpFormat) []job.MediaFormat {
	var formats []job.MediaFormat

	for _, f := range rawFormats {
		// Skip formats with no useful codec info
		if f.VCodec == "none" && f.ACodec == "none" {
			continue
		}

		// Skip storyboard/mhtml formats
		if f.Extension == "mhtml" {
			continue
		}

		fileSize := f.FileSize
		if fileSize == 0 {
			fileSize = f.FileSizeAp
		}

		resolution := ""
		if f.Width > 0 && f.Height > 0 {
			resolution = strconv.Itoa(f.Width) + "x" + strconv.Itoa(f.Height)
		}

		quality := buildQualityLabel(f)

		vcodec := f.VCodec
		if vcodec == "none" {
			vcodec = ""
		}
		acodec := f.ACodec
		if acodec == "none" {
			acodec = ""
		}

		formats = append(formats, job.MediaFormat{
			FormatID:   f.FormatID,
			Extension:  f.Extension,
			Resolution: resolution,
			FileSize:   fileSize,
			VCodec:     vcodec,
			ACodec:     acodec,
			FPS:        f.FPS,
			Quality:    quality,
			Note:       f.FormatNote,
		})
	}

	// Sort by quality: video+audio first, then by resolution descending
	sort.Slice(formats, func(i, j int) bool {
		iScore := qualityScore(formats[i])
		jScore := qualityScore(formats[j])
		return iScore > jScore
	})

	return formats
}

// buildQualityLabel generates a human-readable quality string.
func buildQualityLabel(f ytdlpFormat) string {
	if f.VCodec == "none" && f.ACodec != "none" {
		return "audio only"
	}

	if f.Height > 0 {
		label := strconv.Itoa(f.Height) + "p"
		if f.FPS > 30 {
			label += strconv.Itoa(int(f.FPS))
		}
		return label
	}

	if f.FormatNote != "" {
		return f.FormatNote
	}

	return "unknown"
}

// qualityScore assigns a numeric score for sorting.
func qualityScore(f job.MediaFormat) int {
	score := 0

	// Prefer video+audio combined
	if f.VCodec != "" && f.ACodec != "" {
		score += 100000
	}

	// Prefer video over audio-only
	if f.VCodec != "" {
		score += 50000
	}

	// Use resolution height for ranking
	if f.Resolution != "" {
		parts := strings.Split(f.Resolution, "x")
		if len(parts) == 2 {
			h, _ := strconv.Atoi(parts[1])
			score += h
		}
	}

	return score
}

// cleanError extracts meaningful error text from yt-dlp stderr.
func cleanError(stderr string) string {
	lines := strings.Split(strings.TrimSpace(stderr), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "ERROR") {
			// Strip the "ERROR:" prefix
			if idx := strings.Index(line, "ERROR:"); idx >= 0 {
				return strings.TrimSpace(line[idx+6:])
			}
			return line
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1]
	}
	return "unknown error"
}
