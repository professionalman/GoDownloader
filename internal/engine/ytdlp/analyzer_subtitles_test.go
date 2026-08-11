package ytdlp

import (
	"encoding/json"
	"reflect"
	"testing"

	"downloader/internal/job"
)

func TestNormalizeSubtitles(t *testing.T) {
	tests := []struct {
		name       string
		rawSubs    map[string][]ytdlpSubtitleEntry
		rawAuto    map[string][]ytdlpSubtitleEntry
		spokenLang string
		wantCaps   *job.SubtitleCapabilities
	}{
		{
			name: "1. manual subtitles only",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"ja": {
					{Ext: "vtt", Name: "Japanese", URL: "https://example.com/sub/ja.vtt"},
				},
			},
			rawAuto:    nil,
			spokenLang: "ja",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "ja",
						Name:     "Japanese",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name:    "2. automatic captions only",
			rawSubs: nil,
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"fr": {
					{Ext: "vtt", Name: "French", URL: "https://example.com/auto/fr.vtt"},
				},
			},
			spokenLang: "fr",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "fr",
						Name:     "French",
						Manual:   false,
						Auto:     true,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "3. same language exists in both manual and auto",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English", URL: "https://example.com/sub/en.vtt"},
				},
			},
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "srv3", Name: "English (auto)", URL: "https://example.com/auto/en.srv3"},
					{Ext: "vtt", Name: "English (auto)", URL: "https://example.com/auto/en.vtt"},
				},
			},
			spokenLang: "en",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "en",
						Name:     "English",
						Manual:   true,
						Auto:     true,
						Formats:  []string{"srv3", "vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "4. multiple manual languages",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"es": {
					{Ext: "vtt", Name: "Spanish", URL: "https://example.com/sub/es.vtt"},
				},
				"de": {
					{Ext: "vtt", Name: "German", URL: "https://example.com/sub/de.vtt"},
				},
			},
			rawAuto:    nil,
			spokenLang: "de",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "de",
						Name:     "German",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"vtt"},
					},
					{
						Language: "es",
						Name:     "Spanish",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name:    "5. multiple auto languages",
			rawSubs: nil,
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"it": {
					{Ext: "vtt", Name: "Italian", URL: "https://example.com/auto/it.vtt"},
				},
				"ko": {
					{Ext: "vtt", Name: "Korean", URL: "https://example.com/auto/ko.vtt"},
				},
			},
			spokenLang: "it",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "it",
						Name:     "Italian",
						Manual:   false,
						Auto:     true,
						Formats:  []string{"vtt"},
					},
					{
						Language: "ko",
						Name:     "Korean",
						Manual:   false,
						Auto:     true,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "6. mixture of manual and auto languages",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"ja": {
					{Ext: "vtt", Name: "Japanese", URL: "https://example.com/sub/ja.vtt"},
				},
			},
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"fr": {
					{Ext: "vtt", Name: "French", URL: "https://example.com/auto/fr.vtt"},
				},
			},
			spokenLang: "ja",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "fr",
						Name:     "French",
						Manual:   false,
						Auto:     true,
						Formats:  []string{"vtt"},
					},
					{
						Language: "ja",
						Name:     "Japanese",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "7. duplicate format entries",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English", URL: "https://example.com/1.vtt"},
					{Ext: "vtt", Name: "English", URL: "https://example.com/2.vtt"},
					{Ext: "srt", Name: "English", URL: "https://example.com/1.srt"},
					{Ext: "srt", Name: "English", URL: "https://example.com/2.srt"},
				},
			},
			rawAuto:    nil,
			spokenLang: "en",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "en",
						Name:     "English",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"srt", "vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name:       "8. missing subtitles",
			rawSubs:    nil,
			rawAuto:    nil,
			spokenLang: "",
			wantCaps: &job.SubtitleCapabilities{
				Tracks:                      []job.SubtitleTrack{},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name:       "10. empty subtitles object",
			rawSubs:    map[string][]ytdlpSubtitleEntry{},
			rawAuto:    map[string][]ytdlpSubtitleEntry{},
			spokenLang: "",
			wantCaps: &job.SubtitleCapabilities{
				Tracks:                      []job.SubtitleTrack{},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "14. malformed optional subtitle entries",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"": { // empty language key should be skipped
					{Ext: "vtt", Name: "Unknown"},
				},
				"en": {
					{Ext: "", Name: "English"},    // empty ext should be ignored
					{Ext: " ", Name: "English"},   // whitespace ext should be ignored
					{Ext: "vtt", Name: "English"}, // valid
				},
			},
			rawAuto:    nil,
			spokenLang: "en",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "en",
						Name:     "English",
						Manual:   true,
						Auto:     false,
						Formats:  []string{"vtt"},
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "15. language entries with no valid formats",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "", Name: "English"},
				},
			},
			rawAuto:    nil,
			spokenLang: "en",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{
						Language: "en",
						Name:     "English",
						Manual:   true,
						Auto:     false,
						Formats:  nil,
					},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "16. deterministic sorting",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"zh": {
					{Ext: "vtt", Name: "Chinese"},
				},
				"ar": {
					{Ext: "vtt", Name: "Arabic"},
				},
				"es": {
					{Ext: "vtt", Name: "Spanish"},
				},
			},
			rawAuto:    nil,
			spokenLang: "es",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ar", Name: "Arabic", Manual: true, Formats: []string{"vtt"}},
					{Language: "es", Name: "Spanish", Manual: true, Formats: []string{"vtt"}},
					{Language: "zh", Name: "Chinese", Manual: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "19. source with no translation capability",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"es": {
					{Ext: "vtt", Name: "Spanish"},
				},
			},
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"es": {
					{Ext: "vtt", Name: "Spanish"},
				},
			},
			spokenLang: "es",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "es", Name: "Spanish", Manual: true, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "20. source with English translated capability (Japanese video with English auto-caption)",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"ja": {
					{Ext: "vtt", Name: "Japanese"},
				},
			},
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"ja": {
					{Ext: "vtt", Name: "Japanese"},
				},
				"en": {
					{Ext: "vtt", Name: "English"},
				},
			},
			spokenLang: "ja",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "en", Name: "English", Manual: false, Auto: true, Formats: []string{"vtt"}},
					{Language: "ja", Name: "Japanese", Manual: true, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
		},
		{
			name: "21. native English manual subtitle but NO English translation",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English"},
				},
			},
			rawAuto:    nil,
			spokenLang: "en",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "en", Name: "English", Manual: true, Auto: false, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: false,
			},
		},
		{
			name: "22. native English + separate English translation explicitly noted",
			rawSubs: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English"},
				},
			},
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English (translated)"},
				},
			},
			spokenLang: "ja",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "en", Name: "English", Manual: true, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
		},
		{
			name:    "23. translated English capability does not create duplicate normal subtitle row incorrectly",
			rawSubs: nil,
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"en": {
					{Ext: "vtt", Name: "English"},
					{Ext: "srv3", Name: "English"},
				},
			},
			spokenLang: "fr",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "en", Name: "English", Manual: false, Auto: true, Formats: []string{"srv3", "vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
		},
		{
			name:    "24. other translated languages do NOT become exposed UI translation choices",
			rawSubs: nil,
			rawAuto: map[string][]ytdlpSubtitleEntry{
				"hi": {
					{Ext: "vtt", Name: "Hindi"},
				},
				"es": {
					{Ext: "vtt", Name: "Spanish"},
				},
			},
			spokenLang: "ja",
			wantCaps: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "es", Name: "Spanish", Manual: false, Auto: true, Formats: []string{"vtt"}},
					{Language: "hi", Name: "Hindi", Manual: false, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeSubtitles(tt.rawSubs, tt.rawAuto, tt.spokenLang)
			if !reflect.DeepEqual(got, tt.wantCaps) {
				t.Errorf("normalizeSubtitles() mismatch:\ngot:  %+v\nwant: %+v", got, tt.wantCaps)
			}
		})
	}
}

func TestSubtitleJSONParsingAndPrivacy(t *testing.T) {
	t.Run("9 & 12. null subtitles and null automatic_captions in raw JSON", func(t *testing.T) {
		rawJSON := `{"title":"Test","subtitles":null,"automatic_captions":null}`
		var raw ytdlpJSON
		if err := json.Unmarshal([]byte(rawJSON), &raw); err != nil {
			t.Fatalf("json unmarshal failed: %v", err)
		}
		caps := normalizeSubtitles(raw.Subtitles, raw.AutomaticCaptions, raw.Language)
		if len(caps.Tracks) != 0 {
			t.Errorf("expected 0 tracks, got %d", len(caps.Tracks))
		}
		if caps.EnglishTranslationAvailable {
			t.Errorf("expected EnglishTranslationAvailable=false")
		}
	})

	t.Run("17 & 18. Subtitle URLs and raw extractor data are NOT leaked in SubtitleTrack DTO", func(t *testing.T) {
		rawSubs := map[string][]ytdlpSubtitleEntry{
			"en": {
				{Ext: "vtt", URL: "https://secret.cdn.com/sub?auth_token=supersecret123", Name: "English", Protocol: "https"},
			},
		}
		caps := normalizeSubtitles(rawSubs, nil, "en")
		data, err := json.Marshal(caps)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		jsonStr := string(data)
		if jsonStr == "" {
			t.Fatal("empty json")
		}
		if jsonStr != `{"tracks":[{"language":"en","name":"English","manual":true,"auto":false,"formats":["vtt"]}],"englishTranslationAvailable":false}` {
			t.Errorf("unexpected marshaled JSON: %s", jsonStr)
		}
		if reflect.ValueOf(caps.Tracks[0]).FieldByName("URL").IsValid() {
			t.Error("SubtitleTrack struct unexpectedly exposed URL field")
		}
	})
}
