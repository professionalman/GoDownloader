package ytdlp

import (
	"regexp"
	"strconv"
	"strings"
)

// progressInfo holds parsed progress data from yt-dlp output.
type progressInfo struct {
	Percent         float64
	TotalBytes      int64
	DownloadedBytes int64
	Speed           int64
	ETASeconds      int64
}

// Regex patterns for yt-dlp download progress lines.
var (
	// Matches: [download]  42.3% of  123.45MiB at  5.67MiB/s ETA 00:15
	progressRe = regexp.MustCompile(
		`\[download\]\s+(\d+\.?\d*)%\s+of\s+~?(\S+)\s+at\s+(\S+)\s+ETA\s+(\S+)`,
	)
	// Matches: [download] 100% of 123.45MiB
	completeRe = regexp.MustCompile(
		`\[download\]\s+100%\s+of\s+(\S+)`,
	)
	// Matches: [Merger] Merging formats into ...
	mergerRe = regexp.MustCompile(`\[Merger\]|\[ffmpeg\]|\[ExtractAudio\]`)
)

// parseProgressLine parses a single line of yt-dlp output into progress info.
// Returns nil if the line is not a progress line.
func parseProgressLine(line string) *progressInfo {
	line = strings.TrimSpace(line)

	// Check for 100% completion
	if m := completeRe.FindStringSubmatch(line); len(m) > 0 {
		totalBytes := parseSizeString(m[1])
		return &progressInfo{
			Percent:         100.0,
			TotalBytes:      totalBytes,
			DownloadedBytes: totalBytes,
			Speed:           0,
			ETASeconds:      0,
		}
	}

	// Check for progress line
	if m := progressRe.FindStringSubmatch(line); len(m) > 0 {
		percent, _ := strconv.ParseFloat(m[1], 64)
		totalBytes := parseSizeString(m[2])
		speed := parseSpeedString(m[3])
		eta := parseETAString(m[4])

		downloaded := int64(0)
		if totalBytes > 0 {
			downloaded = int64(float64(totalBytes) * percent / 100.0)
		}

		return &progressInfo{
			Percent:         percent,
			TotalBytes:      totalBytes,
			DownloadedBytes: downloaded,
			Speed:           speed,
			ETASeconds:      eta,
		}
	}

	return nil
}

// isPostProcessingLine returns true if the line indicates FFmpeg post-processing.
func isPostProcessingLine(line string) bool {
	return mergerRe.MatchString(line)
}

// parseSizeString converts strings like "123.45MiB" to bytes.
func parseSizeString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "~" || strings.Contains(s, "Unknown") {
		return 0
	}

	multiplier := int64(1)
	numStr := s

	if strings.HasSuffix(s, "GiB") {
		multiplier = 1024 * 1024 * 1024
		numStr = strings.TrimSuffix(s, "GiB")
	} else if strings.HasSuffix(s, "MiB") {
		multiplier = 1024 * 1024
		numStr = strings.TrimSuffix(s, "MiB")
	} else if strings.HasSuffix(s, "KiB") {
		multiplier = 1024
		numStr = strings.TrimSuffix(s, "KiB")
	} else if strings.HasSuffix(s, "B") {
		numStr = strings.TrimSuffix(s, "B")
	}

	numStr = strings.TrimSpace(numStr)
	val, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0
	}

	return int64(val * float64(multiplier))
}

// parseSpeedString converts strings like "5.67MiB/s" to bytes/sec.
func parseSpeedString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "Unknown") {
		return 0
	}

	s = strings.TrimSuffix(s, "/s")
	return parseSizeString(s)
}

// parseETAString converts strings like "00:15" or "01:23:45" to seconds.
func parseETAString(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "Unknown") {
		return 0
	}

	parts := strings.Split(s, ":")
	total := int64(0)

	for i, p := range parts {
		val, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return 0
		}

		power := len(parts) - 1 - i
		switch power {
		case 0:
			total += val
		case 1:
			total += val * 60
		case 2:
			total += val * 3600
		}
	}

	return total
}
