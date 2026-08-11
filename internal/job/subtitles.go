package job

import (
	"fmt"
	"regexp"
	"strings"
)

var validLangCodeRegex = regexp.MustCompile(`^[a-zA-Z0-9]+([-_.:][a-zA-Z0-9]+)*$`)

const maxSubtitleLanguages = 50

// ValidateSubtitleOptions checks that the provided subtitle options are well-formed and supported.
func ValidateSubtitleOptions(opts *SubtitleOptions, caps *SubtitleCapabilities) error {
	if opts == nil {
		return nil
	}

	// If no languages and no English translation requested, it's a no-op
	if len(opts.Languages) == 0 && !opts.EnglishTranslation {
		return nil
	}

	// Validate mode
	mode := strings.ToLower(strings.TrimSpace(string(opts.Mode)))
	if mode == "" {
		opts.Mode = SubtitleModeSeparate
	} else {
		switch SubtitleMode(mode) {
		case SubtitleModeSeparate, SubtitleModeEmbed, SubtitleModeBoth:
			opts.Mode = SubtitleMode(mode)
		default:
			return fmt.Errorf("unsupported subtitle output mode: must be separate, embed, or both")
		}
	}

	// Validate format
	format := strings.ToLower(strings.TrimSpace(string(opts.Format)))
	if format == "" {
		opts.Format = SubtitleFormatSRT
	} else {
		switch SubtitleFormat(format) {
		case SubtitleFormatSRT, SubtitleFormatVTT:
			opts.Format = SubtitleFormat(format)
		default:
			return fmt.Errorf("unsupported subtitle format: must be srt or vtt")
		}
	}

	// Validate language count
	if len(opts.Languages) > maxSubtitleLanguages {
		return fmt.Errorf("too many subtitle languages selected (maximum %d)", maxSubtitleLanguages)
	}

	// Validate each language code
	for _, l := range opts.Languages {
		lang := strings.TrimSpace(l)
		if lang == "" {
			return fmt.Errorf("empty subtitle language code")
		}
		if strings.HasPrefix(lang, "-") {
			return fmt.Errorf("invalid subtitle language code: cannot start with a dash")
		}
		if !validLangCodeRegex.MatchString(lang) {
			return fmt.Errorf("invalid characters in subtitle language code: %q", lang)
		}
	}

	// Validate English translation availability if requested
	if opts.EnglishTranslation {
		if caps != nil && !caps.EnglishTranslationAvailable {
			return fmt.Errorf("English translation is unavailable for this media")
		}
	}

	return nil
}
