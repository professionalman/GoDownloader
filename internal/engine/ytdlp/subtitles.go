package ytdlp

import (
	"strings"

	"downloader/internal/job"
)

// ValidateSubtitleOptions checks that the provided subtitle options are well-formed and supported.
func ValidateSubtitleOptions(opts *job.SubtitleOptions, caps *job.SubtitleCapabilities) error {
	return job.ValidateSubtitleOptions(opts, caps)
}

// appendSubtitleArgs appends the appropriate yt-dlp CLI arguments for the given subtitle configuration.
func appendSubtitleArgs(args []string, opts *job.SubtitleOptions, caps *job.SubtitleCapabilities) ([]string, error) {
	if opts == nil {
		return args, nil
	}

	if len(opts.Languages) == 0 && !opts.EnglishTranslation {
		return args, nil
	}

	if err := ValidateSubtitleOptions(opts, caps); err != nil {
		return nil, err
	}

	// Build language list
	var langs []string
	seen := make(map[string]bool)

	for _, l := range opts.Languages {
		clean := strings.TrimSpace(l)
		if clean != "" && !seen[clean] {
			langs = append(langs, clean)
			seen[clean] = true
		}
	}

	if opts.EnglishTranslation {
		transKey := "en"
		if caps != nil && caps.TranslationLanguageKey != "" {
			transKey = caps.TranslationLanguageKey
		}
		if !seen[transKey] {
			langs = append(langs, transKey)
			seen[transKey] = true
		}
	}

	if len(langs) == 0 {
		return args, nil
	}

	// Language filter flag
	args = append(args, "--sub-langs", strings.Join(langs, ","))

	// Output mode flags
	mode := opts.Mode
	if mode == "" {
		mode = job.SubtitleModeSeparate
	}

	hasManual := len(opts.Languages) > 0
	hasAuto := opts.IncludeAuto || opts.EnglishTranslation

	switch mode {
	case job.SubtitleModeSeparate:
		if hasManual {
			args = append(args, "--write-subs")
		}
		if hasAuto {
			args = append(args, "--write-auto-subs")
		}
	case job.SubtitleModeEmbed:
		args = append(args, "--embed-subs")
		if hasAuto {
			args = append(args, "--write-auto-subs")
		}
	case job.SubtitleModeBoth:
		if hasManual {
			args = append(args, "--write-subs")
		}
		args = append(args, "--embed-subs")
		if hasAuto {
			args = append(args, "--write-auto-subs")
		}
	}

	// Format conversion
	if opts.Format == job.SubtitleFormatSRT {
		args = append(args, "--convert-subs", "srt")
	}

	return args, nil
}
