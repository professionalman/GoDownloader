package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downloader/internal/job"
)

func TestSelectFormatWithSubtitlesAPI(t *testing.T) {
	router, jobRepo, _ := setupDeleteAPITestRouter(t)

	// Create a media job in analyzing status with MediaInfo and Subtitle capabilities
	j := &job.Job{
		ID:     "sub-test-job-1",
		Source: "https://example.com/watch?v=subtest",
		Type:   job.TypeMedia,
		Status: job.StatusAnalyzing,
		MediaInfo: &job.MediaInfo{
			Title: "Subtitle Test Video",
			Formats: []job.MediaFormat{
				{FormatID: "18", VCodec: "avc1", ACodec: "mp4a"},
				{FormatID: "137", VCodec: "avc1", ACodec: "none"},
			},
			Subtitles: &job.SubtitleCapabilities{
				Tracks: []job.SubtitleTrack{
					{Language: "ja", Name: "Japanese", Manual: true, Formats: []string{"vtt"}},
					{Language: "en", Name: "English", Manual: false, Auto: true, Formats: []string{"vtt"}},
				},
				EnglishTranslationAvailable: true,
				TranslationLanguageKey:      "en",
			},
		},
	}
	if err := jobRepo.Create(context.Background(), j); err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	t.Run("Valid format selection without subtitles", func(t *testing.T) {
		body := `{"formatId":"18"}`
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%s/format", j.ID), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}

		var updated job.Job
		if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.MediaInfo.SelectedFmt != "18" {
			t.Errorf("expected format 18, got %s", updated.MediaInfo.SelectedFmt)
		}
		if updated.MediaInfo.SubtitleOptions != nil {
			t.Errorf("expected nil SubtitleOptions, got %+v", updated.MediaInfo.SubtitleOptions)
		}
	})

	t.Run("Valid format selection with subtitles and English translation", func(t *testing.T) {
		// Reset job to analyzing
		j.Status = job.StatusAnalyzing
		j.MediaInfo.SelectedFmt = ""
		_ = jobRepo.Update(context.Background(), j)

		body := `{
			"formatId":"137",
			"subtitleOptions": {
				"languages": ["ja"],
				"includeAuto": true,
				"englishTranslation": true,
				"mode": "both",
				"format": "srt"
			}
		}`
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%s/format", j.ID), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}

		var updated job.Job
		if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
			t.Fatal(err)
		}
		if updated.MediaInfo.SelectedFmt != "137" {
			t.Errorf("expected format 137, got %s", updated.MediaInfo.SelectedFmt)
		}
		if updated.MediaInfo.SubtitleOptions == nil {
			t.Fatal("expected non-nil SubtitleOptions")
		}
		opts := updated.MediaInfo.SubtitleOptions
		if len(opts.Languages) != 1 || opts.Languages[0] != "ja" {
			t.Errorf("expected languages [ja], got %v", opts.Languages)
		}
		if !opts.EnglishTranslation {
			t.Errorf("expected EnglishTranslation=true")
		}
		if opts.Mode != job.SubtitleModeBoth {
			t.Errorf("expected mode 'both', got %s", opts.Mode)
		}
		if opts.Format != job.SubtitleFormatSRT {
			t.Errorf("expected format 'srt', got %s", opts.Format)
		}
	})

	t.Run("Invalid subtitle mode returns 400", func(t *testing.T) {
		j.Status = job.StatusAnalyzing
		_ = jobRepo.Update(context.Background(), j)

		body := `{
			"formatId":"18",
			"subtitleOptions": {
				"languages": ["ja"],
				"mode": "invalid_mode",
				"format": "srt"
			}
		}`
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%s/format", j.ID), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("Invalid subtitle format returns 400", func(t *testing.T) {
		j.Status = job.StatusAnalyzing
		_ = jobRepo.Update(context.Background(), j)

		body := `{
			"formatId":"18",
			"subtitleOptions": {
				"languages": ["ja"],
				"mode": "separate",
				"format": "ass"
			}
		}`
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/jobs/%s/format", j.ID), bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}
