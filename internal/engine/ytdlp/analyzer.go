package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

// ytdlpJSON is the raw JSON structure from yt-dlp --dump-json.
type ytdlpJSON struct {
	Title             string                          `json:"title"`
	Duration          float64                         `json:"duration"`
	Thumbnail         string                          `json:"thumbnail"`
	URL               string                          `json:"webpage_url"`
	Formats           []ytdlpFormat                   `json:"formats"`
	Subtitles         map[string][]ytdlpSubtitleEntry `json:"subtitles"`
	AutomaticCaptions map[string][]ytdlpSubtitleEntry `json:"automatic_captions"`
	Language          string                          `json:"language"`
}

type ytdlpSubtitleEntry struct {
	Ext      string `json:"ext"`
	URL      string `json:"url"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
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
	ABR        float64 `json:"abr"`
}

// Analyze runs yt-dlp --dump-json to extract media metadata.
func (e *Engine) Analyze(ctx context.Context, url string) (*job.MediaInfo, error) {
	return e.AnalyzeWithPolicy(ctx, url, nil)
}

// AnalyzeWithPolicy applies server-resolved network controls and media authentication without exposing raw flags.
func (e *Engine) AnalyzeWithPolicy(ctx context.Context, url string, policy *networkpolicy.RuntimePolicy) (*job.MediaInfo, error) {
	var authArgs []string
	cleanup := func() {}
	e.mu.RLock()
	provider := e.authProvider
	e.mu.RUnlock()

	if provider != nil {
		var err error
		authArgs, cleanup, err = provider.PrepareAuthArgs(ctx)
		if err != nil {
			return nil, fmt.Errorf("media auth preparation failed: %w", err)
		}
	}
	defer cleanup()

	args := []string{
		"--dump-json",
		"--no-download",
		"--no-playlist",
		"--no-warnings",
	}

	if e.ffmpegPath != "" {
		args = append(args, "--ffmpeg-location", e.ffmpegPath)
	}
	args = appendNetworkArgs(args, policy)
	args = append(args, authArgs...)

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
	bestAudio := selectBestAudioFormat(raw.Formats, formats)
	subtitles := normalizeSubtitles(raw.Subtitles, raw.AutomaticCaptions, raw.Language)

	return &job.MediaInfo{
		Title:           raw.Title,
		Duration:        raw.Duration,
		Thumbnail:       raw.Thumbnail,
		URL:             raw.URL,
		Formats:         formats,
		BestAudioFormat: bestAudio,
		Subtitles:       subtitles,
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
			ABR:        f.ABR,
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

// selectBestAudioFormat resolves the best audio-only format matching yt-dlp's bestaudio stream selection.
func selectBestAudioFormat(rawFormats []ytdlpFormat, normalizedFormats []job.MediaFormat) *job.MediaFormat {
	rawMap := make(map[string]ytdlpFormat)
	for _, rf := range rawFormats {
		rawMap[rf.FormatID] = rf
	}

	var audioCandidates []job.MediaFormat
	for _, f := range normalizedFormats {
		// Must be audio-only: no video codec, has audio codec
		if (f.VCodec == "" || f.VCodec == "none") && (f.ACodec != "" && f.ACodec != "none") {
			audioCandidates = append(audioCandidates, f)
		}
	}

	if len(audioCandidates) == 0 {
		return nil
	}

	sort.Slice(audioCandidates, func(i, j int) bool {
		a := audioCandidates[i]
		b := audioCandidates[j]
		rawA := rawMap[a.FormatID]
		rawB := rawMap[b.FormatID]

		// 1. Audio Bitrate (ABR)
		if rawA.ABR != rawB.ABR {
			return rawA.ABR > rawB.ABR
		}
		// 2. Total Bitrate (TBR)
		if rawA.TBR != rawB.TBR {
			return rawA.TBR > rawB.TBR
		}
		// 3. Codec preference (opus > mp4a / aac > others)
		prefA := audioCodecPriority(a.ACodec)
		prefB := audioCodecPriority(b.ACodec)
		if prefA != prefB {
			return prefA < prefB
		}
		// 4. FileSize / FileSizeAp
		if b.FileSize != a.FileSize {
			return b.FileSize > a.FileSize
		}
		return a.FormatID < b.FormatID
	})

	best := audioCandidates[0]
	return &best
}

func audioCodecPriority(acodec string) int {
	ac := strings.ToLower(acodec)
	if strings.Contains(ac, "opus") {
		return 1
	}
	if strings.Contains(ac, "mp4a") || strings.Contains(ac, "aac") {
		return 2
	}
	return 3
}

// normalizeSubtitles processes raw subtitle dictionaries into a clean, safe, deduplicated SubtitleCapabilities object.
func normalizeSubtitles(rawSubs map[string][]ytdlpSubtitleEntry, rawAuto map[string][]ytdlpSubtitleEntry, spokenLang string) *job.SubtitleCapabilities {
	tracksMap := make(map[string]*job.SubtitleTrack)
	formatSets := make(map[string]map[string]struct{})

	// 1. Process manual subtitles
	for langKey, entries := range rawSubs {
		lang := strings.TrimSpace(langKey)
		if lang == "" {
			continue
		}
		var name string
		fset := make(map[string]struct{})
		for _, e := range entries {
			ext := strings.TrimSpace(e.Ext)
			if ext != "" {
				fset[ext] = struct{}{}
			}
			if name == "" && strings.TrimSpace(e.Name) != "" {
				name = strings.TrimSpace(e.Name)
			}
		}

		tracksMap[lang] = &job.SubtitleTrack{
			Language: lang,
			Name:     name,
			Manual:   true,
			Auto:     false,
		}
		formatSets[lang] = fset
	}

	// 2. Process automatic captions
	for langKey, entries := range rawAuto {
		lang := strings.TrimSpace(langKey)
		if lang == "" {
			continue
		}
		var name string
		fset := make(map[string]struct{})
		for _, e := range entries {
			ext := strings.TrimSpace(e.Ext)
			if ext != "" {
				fset[ext] = struct{}{}
			}
			if name == "" && strings.TrimSpace(e.Name) != "" {
				name = strings.TrimSpace(e.Name)
			}
		}

		if existing, ok := tracksMap[lang]; ok {
			existing.Auto = true
			if existing.Name == "" && name != "" {
				existing.Name = name
			}
			for ext := range fset {
				formatSets[lang][ext] = struct{}{}
			}
		} else {
			tracksMap[lang] = &job.SubtitleTrack{
				Language: lang,
				Name:     name,
				Manual:   false,
				Auto:     true,
			}
			formatSets[lang] = fset
		}
	}

	// 3. Convert map to sorted slice
	tracks := make([]job.SubtitleTrack, 0, len(tracksMap))
	for lang, track := range tracksMap {
		var formats []string
		if fset, ok := formatSets[lang]; ok {
			for ext := range fset {
				formats = append(formats, ext)
			}
			sort.Strings(formats)
		}
		track.Formats = formats
		tracks = append(tracks, *track)
	}

	sort.Slice(tracks, func(i, j int) bool {
		return tracks[i].Language < tracks[j].Language
	})

	// 4. Discover English translation capability
	englishTranslationAvailable := false
	translationLanguageKey := ""

	cleanSpoken := strings.ToLower(strings.TrimSpace(spokenLang))

	for k, entries := range rawAuto {
		lk := strings.ToLower(strings.TrimSpace(k))
		if lk == "en" || strings.HasPrefix(lk, "en-") || strings.HasPrefix(lk, "en_") {
			for _, e := range entries {
				if isEnglishTranslationEntry(e, k, rawSubs, rawAuto, cleanSpoken) {
					englishTranslationAvailable = true
					translationLanguageKey = strings.TrimSpace(k)
					break
				}
			}
			if englishTranslationAvailable {
				break
			}
		}
	}

	return &job.SubtitleCapabilities{
		Tracks:                      tracks,
		EnglishTranslationAvailable: englishTranslationAvailable,
		TranslationLanguageKey:      translationLanguageKey,
	}
}

// isEnglishTranslationEntry checks if an automatic caption entry represents a genuine translated-to-English caption.
func isEnglishTranslationEntry(entry ytdlpSubtitleEntry, langKey string, rawSubs map[string][]ytdlpSubtitleEntry, rawAuto map[string][]ytdlpSubtitleEntry, cleanSpoken string) bool {
	lk := strings.ToLower(strings.TrimSpace(langKey))
	if lk != "en" && !strings.HasPrefix(lk, "en-") && !strings.HasPrefix(lk, "en_") {
		return false
	}

	// 1. Check entry name for explicit translation keywords
	nameLower := strings.ToLower(entry.Name)
	if strings.Contains(nameLower, "translat") || strings.Contains(nameLower, " from ") {
		return true
	}

	// 2. Inspect URL parameters for YouTube timedtext translation markers (e.g. tlang=en)
	if entry.URL != "" {
		if parsedURL, err := url.Parse(entry.URL); err == nil {
			q := parsedURL.Query()
			tlang := strings.ToLower(strings.TrimSpace(q.Get("tlang")))
			lang := strings.ToLower(strings.TrimSpace(q.Get("lang")))

			if tlang == "en" || strings.HasPrefix(tlang, "en-") || strings.HasPrefix(tlang, "en_") {
				return true
			}
			// If URL has lang=en and no tlang, it is definitely a native English auto-caption, not a translation.
			if (lang == "en" || strings.HasPrefix(lang, "en-") || strings.HasPrefix(lang, "en_")) && tlang == "" {
				return false
			}
		}
	}

	// 3. Check for presence of -orig tracks in rawAuto
	hasNonEnglishOrig := false
	hasEnglishOrig := false
	for k := range rawAuto {
		kLower := strings.ToLower(strings.TrimSpace(k))
		if strings.HasSuffix(kLower, "-orig") {
			if strings.HasPrefix(kLower, "en") {
				hasEnglishOrig = true
			} else {
				hasNonEnglishOrig = true
			}
		}
	}

	if hasEnglishOrig {
		return false
	}
	if hasNonEnglishOrig && !strings.HasSuffix(lk, "-orig") {
		return true
	}

	// 4. If spoken language is explicitly known and non-English
	isSpokenEnglish := cleanSpoken == "en" || strings.HasPrefix(cleanSpoken, "en-") || strings.HasPrefix(cleanSpoken, "en_")
	if cleanSpoken != "" {
		if isSpokenEnglish {
			return false
		}
		if !strings.HasSuffix(lk, "-orig") {
			return true
		}
	}

	// 5. Check if there are non-English manual tracks
	hasNonEnglishManual := false
	hasEnglishManual := false
	for k := range rawSubs {
		kLower := strings.ToLower(strings.TrimSpace(k))
		if kLower == "en" || strings.HasPrefix(kLower, "en-") || strings.HasPrefix(kLower, "en_") {
			hasEnglishManual = true
		} else {
			hasNonEnglishManual = true
		}
	}
	if hasNonEnglishManual && !hasEnglishManual && !strings.HasSuffix(lk, "-orig") {
		return true
	}

	return false
}
