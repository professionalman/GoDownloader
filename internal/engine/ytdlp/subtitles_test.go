package ytdlp

import (
	"reflect"
	"testing"

	"downloader/internal/job"
)

func TestAppendSubtitleArgs(t *testing.T) {
	tests := []struct {
		name     string
		baseArgs []string
		opts     *job.SubtitleOptions
		caps     *job.SubtitleCapabilities
		wantArgs []string
		wantErr  bool
	}{
		{
			name:     "27. no subtitle options -> no subtitle-related argv changes",
			baseArgs: []string{"-f", "best", "https://example.com/video"},
			opts:     nil,
			caps:     nil,
			wantArgs: []string{"-f", "best", "https://example.com/video"},
			wantErr:  false,
		},
		{
			name:     "27b. empty languages and no english translation -> no subtitle-related argv changes",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:          []string{},
				EnglishTranslation: false,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best"},
			wantErr:  false,
		},
		{
			name:     "28. separate + one manual language",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--write-subs"},
			wantErr:  false,
		},
		{
			name:     "29. separate + multiple manual languages",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja", "fr", "es"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,fr,es", "--write-subs"},
			wantErr:  false,
		},
		{
			name:     "30. auto-only language + includeAuto",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"fr"},
				IncludeAuto: true,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "fr", "--write-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "31. manual + auto language behavior (auto enabled)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"ja", "fr"},
				IncludeAuto: true,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,fr", "--write-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "32. embed mode",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeEmbed,
				Format:    job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--embed-subs"},
			wantErr:  false,
		},
		{
			name:     "33. both mode",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeBoth,
				Format:    job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--write-subs", "--embed-subs"},
			wantErr:  false,
		},
		{
			name:     "34. SRT conversion",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--write-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "35. VTT behavior (no convert flag needed)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatVTT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--write-subs"},
			wantErr:  false,
		},
		{
			name:     "36. multiple languages with both mode and SRT conversion",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"ja", "de"},
				IncludeAuto: true,
				Mode:        job.SubtitleModeBoth,
				Format:      job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,de", "--write-subs", "--embed-subs", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "37. English translation only (uses --write-auto-subs)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "38. normal manual subtitle + English translation (uses --write-subs AND --write-auto-subs)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:          []string{"ja"},
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Manual: true, Auto: false},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,en", "--write-subs", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "38b. English translation + IncludeAuto=true (no duplicate --write-auto-subs)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				IncludeAuto:        true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "38c. embed mode + English translation",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				Mode:               job.SubtitleModeEmbed,
				Format:             job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--embed-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "38d. both mode + English translation only",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				Mode:               job.SubtitleModeBoth,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--embed-subs", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "38e. both mode + manual subtitle + English translation",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:          []string{"ja"},
				EnglishTranslation: true,
				Mode:               job.SubtitleModeBoth,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Manual: true, Auto: false},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,en", "--write-subs", "--embed-subs", "--write-auto-subs", "--convert-subs", "srt"},
			wantErr:  false,
		},
		{
			name:     "38f. VTT translation (no convert flag needed)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "38g. auto-only language with IncludeAuto=false is rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"fr"},
				IncludeAuto: false,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "fr", Manual: false, Auto: true},
				},
			},
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "38h. auto-only language with IncludeAuto=true succeeds",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"fr"},
				IncludeAuto: true,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "fr", Manual: false, Auto: true},
				},
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "fr", "--write-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "38i. manual-only language with IncludeAuto=false succeeds",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"ja"},
				IncludeAuto: false,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Manual: true, Auto: false},
				},
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "ja", "--write-subs"},
			wantErr:  false,
		},
		{
			name:     "38j. manual+auto language with IncludeAuto=false succeeds via manual",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:   []string{"en"},
				IncludeAuto: false,
				Mode:        job.SubtitleModeSeparate,
				Format:      job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "en", Manual: true, Auto: true},
				},
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "en", "--write-subs"},
			wantErr:  false,
		},
		{
			name:     "39. multiple normal subtitles + English translation (no duplicate en if already present)",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:          []string{"ja", "en", "es"},
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Manual: true, Auto: false},
					{Language: "en", Manual: true, Auto: false},
					{Language: "es", Manual: true, Auto: false},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,en,es", "--write-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "25. translation internal identifier is correctly mapped to request argv",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages:          []string{"ja"},
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatVTT,
			},
			caps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Manual: true, Auto: false},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en-orig",
			},
			wantArgs: []string{"-f", "best", "--sub-langs", "ja,en-orig", "--write-subs", "--write-auto-subs"},
			wantErr:  false,
		},
		{
			name:     "26. requesting English translation when unavailable is rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				EnglishTranslation: true,
				Mode:               job.SubtitleModeSeparate,
				Format:             job.SubtitleFormatSRT,
			},
			caps: &job.SubtitleCapabilities{
				EnglishTranslationAvailable: false,
			},
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "40. unsupported format rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormat("ass"),
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "41. unsupported mode rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja"},
				Mode:      job.SubtitleMode("burn_in"),
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "42. empty language rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{" "},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "43. argument-looking language rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"--exec", "rm -rf /"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "44. control-character language rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: []string{"ja\n--dangerous-flag"},
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
		{
			name:     "45. excessive language count rejected",
			baseArgs: []string{"-f", "best"},
			opts: &job.SubtitleOptions{
				Languages: make([]string, 51),
				Mode:      job.SubtitleModeSeparate,
				Format:    job.SubtitleFormatSRT,
			},
			caps:     nil,
			wantArgs: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotArgs, err := appendSubtitleArgs(tt.baseArgs, tt.opts, tt.caps)
			if (err != nil) != tt.wantErr {
				t.Fatalf("appendSubtitleArgs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if !reflect.DeepEqual(gotArgs, tt.wantArgs) {
					t.Errorf("appendSubtitleArgs() mismatch:\ngot:  %v\nwant: %v", gotArgs, tt.wantArgs)
				}
			}
		})
	}
}
