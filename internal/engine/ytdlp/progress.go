package ytdlp

import (
	"regexp"
	"strconv"
	"strings"
)

const finalPathPrefix = "__GODOWNLOADER_FINAL_PATH__:"

// parseFinalPathLine extracts the authoritative final output filepath from --print after_move lines.
func parseFinalPathLine(line string) string {
	line = strings.TrimSpace(line)
	if idx := strings.Index(line, finalPathPrefix); idx != -1 {
		raw := line[idx+len(finalPathPrefix):]
		raw = strings.TrimSpace(raw)
		if len(raw) >= 2 {
			if (raw[0] == '"' && raw[len(raw)-1] == '"') || (raw[0] == '\'' && raw[len(raw)-1] == '\'') {
				raw = raw[1 : len(raw)-1]
			}
		}
		return strings.TrimSpace(raw)
	}
	return ""
}

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

const progressPrefix = "__GODOWNLOADER_PROGRESS__:"

// parseProgressLine parses structured or standard yt-dlp progress lines into progressInfo.
func parseProgressLine(line string) *progressInfo {
	line = strings.TrimSpace(line)

	if idx := strings.Index(line, progressPrefix); idx != -1 {
		content := line[idx+len(progressPrefix):]
		parts := strings.Split(content, "|")
		if len(parts) >= 5 {
			percentStr := strings.TrimSuffix(strings.TrimSpace(parts[0]), "%")
			percent, err := strconv.ParseFloat(percentStr, 64)
			if err != nil {
				percent = 0
			}
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}

			dlBytes := parseNumberOrNA(parts[1])
			totalBytes := parseNumberOrNA(parts[2])
			estTotalBytes := int64(0)
			if len(parts) >= 6 {
				estTotalBytes = parseNumberOrNA(parts[3])
				speed := parseNumberOrNA(parts[4])
				eta := parseNumberOrNA(parts[5])

				effectiveTotal := totalBytes
				if effectiveTotal <= 0 {
					effectiveTotal = estTotalBytes
				}

				if effectiveTotal > 0 && dlBytes <= 0 && percent > 0 {
					dlBytes = int64(float64(effectiveTotal) * percent / 100.0)
				}

				if dlBytes < 0 {
					dlBytes = 0
				}
				if effectiveTotal < 0 {
					effectiveTotal = 0
				}
				if speed < 0 {
					speed = 0
				}
				if eta < 0 {
					eta = 0
				}

				return &progressInfo{
					Percent:         percent,
					TotalBytes:      effectiveTotal,
					DownloadedBytes: dlBytes,
					Speed:           speed,
					ETASeconds:      eta,
				}
			}

			speed := parseNumberOrNA(parts[3])
			eta := parseNumberOrNA(parts[4])

			if totalBytes > 0 && dlBytes <= 0 && percent > 0 {
				dlBytes = int64(float64(totalBytes) * percent / 100.0)
			}

			if dlBytes < 0 {
				dlBytes = 0
			}
			if totalBytes < 0 {
				totalBytes = 0
			}
			if speed < 0 {
				speed = 0
			}
			if eta < 0 {
				eta = 0
			}

			return &progressInfo{
				Percent:         percent,
				TotalBytes:      totalBytes,
				DownloadedBytes: dlBytes,
				Speed:           speed,
				ETASeconds:      eta,
			}
		}
	}

	if strings.HasPrefix(line, "download:") {
		content := strings.TrimPrefix(line, "download:")
		parts := strings.Split(content, "|")
		if len(parts) >= 5 {
			percentStr := strings.TrimSuffix(strings.TrimSpace(parts[0]), "%")
			percent, _ := strconv.ParseFloat(percentStr, 64)
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}

			dlBytes := parseNumberOrNA(parts[1])
			totalBytes := parseNumberOrNA(parts[2])
			speed := parseNumberOrNA(parts[3])
			eta := parseNumberOrNA(parts[4])

			if totalBytes > 0 && dlBytes == 0 && percent > 0 {
				dlBytes = int64(float64(totalBytes) * percent / 100.0)
			}

			if dlBytes < 0 {
				dlBytes = 0
			}
			if totalBytes < 0 {
				totalBytes = 0
			}
			if speed < 0 {
				speed = 0
			}
			if eta < 0 {
				eta = 0
			}

			return &progressInfo{
				Percent:         percent,
				TotalBytes:      totalBytes,
				DownloadedBytes: dlBytes,
				Speed:           speed,
				ETASeconds:      eta,
			}
		}
	}

	// Fallback regex parsing for standard output
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

func parseNumberOrNA(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "Unknown" || s == "~" {
		return 0
	}
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fVal, errFloat := strconv.ParseFloat(s, 64)
		if errFloat == nil {
			return int64(fVal)
		}
		return 0
	}
	return val
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

// parseOutputPathLine extracts file output path from yt-dlp progress/merger output lines.
func parseOutputPathLine(line string) string {
	line = strings.TrimSpace(line)
	if strings.HasPrefix(line, "[ExtractAudio] Destination: ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "[ExtractAudio] Destination: "))
	}
	if strings.HasPrefix(line, "[ffmpeg] Destination: ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "[ffmpeg] Destination: "))
	}
	if strings.HasPrefix(line, "[ffmpeg] Merging formats into \"") {
		s := strings.TrimPrefix(line, "[ffmpeg] Merging formats into \"")
		return strings.TrimSuffix(s, "\"")
	}
	if strings.HasPrefix(line, "[download] Destination: ") {
		return strings.TrimSpace(strings.TrimPrefix(line, "[download] Destination: "))
	}
	if strings.HasPrefix(line, "[Merger] Merging formats into \"") {
		s := strings.TrimPrefix(line, "[Merger] Merging formats into \"")
		return strings.TrimSuffix(s, "\"")
	}
	if strings.HasPrefix(line, "[VideoConvertor] Converting video to \"") {
		s := strings.TrimPrefix(line, "[VideoConvertor] Converting video to \"")
		return strings.TrimSuffix(s, "\"")
	}
	if strings.HasPrefix(line, "[download] ") && strings.HasSuffix(line, " has already been downloaded") {
		s := strings.TrimPrefix(line, "[download] ")
		return strings.TrimSuffix(s, " has already been downloaded")
	}
	return ""
}
