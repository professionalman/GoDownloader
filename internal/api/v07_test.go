package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"downloader/internal/settings"
)

func TestV07StrictDTOsRejectUnknownEnginePassthroughs(t *testing.T) {
	router, _, _ := setupAPITestRouter(t)
	for _, test := range []struct {
		path string
		body string
	}{
		{"/api/v1/jobs", `{"source":"https://example.com/file","max-download-limit":"1M"}`},
		{"/api/v1/capabilities/resolve", `{"source":"https://example.com/file","rawOptions":{}}`},
		{"/api/v1/settings", `{"network":{"globalDownloadLimitBytesPerSecond":0,"aria2Options":{}}}`},
	} {
		req := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(test.body))
		if test.path == "/api/v1/settings" {
			req = httptest.NewRequest(http.MethodPut, test.path, bytes.NewBufferString(test.body))
		}
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", test.path, rec.Code, rec.Body.String())
		}
	}
}

func TestSettingsValidateCompleteRequestBeforePersistence(t *testing.T) {
	router, _, service := setupAPITestRouter(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(
		`{"queue":{"maxConcurrentDownloads":5},"network":{"globalDownloadLimitBytesPerSecond":-1}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, err := service.GetSettings(req.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.Queue.MaxConcurrentDownloads != settings.DefaultMaxConcurrent {
		t.Fatalf("queue was partially persisted: %d", got.Queue.MaxConcurrentDownloads)
	}
}

func TestSettingsUpdateReturnsTruthfulApplicationResultsAndKeepsDesiredState(t *testing.T) {
	router, _, service := setupAPITestRouter(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", bytes.NewBufferString(
		`{"network":{"globalDownloadLimitBytesPerSecond":4096}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response settings.AppSettings
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, result := range response.ApplicationResults {
		results[result.Target] = result.Status
	}
	if results["settings"] != "persisted" || results["aria2"] != "unavailable" ||
		results["qbittorrent"] != "unavailable" || results["yt-dlp"] != "unavailable" {
		t.Fatalf("unexpected application results: %+v", response.ApplicationResults)
	}
	durable, err := service.GetSettings(req.Context())
	if err != nil {
		t.Fatal(err)
	}
	if durable.Network.GlobalDownloadLimitBytesPerSecond != 4096 {
		t.Fatalf("desired state was discarded while engines were unavailable: %+v", durable.Network)
	}
}

func TestTorrentStartRejectsLegacyAndNormalizedSeedingTogether(t *testing.T) {
	router, _, _ := setupAPITestRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/missing/torrent/start", bytes.NewBufferString(
		`{"files":[{"index":0,"priority":"normal"}],"seedAfterComplete":true,"seedingPolicy":{"mode":"unlimited"}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestTorrentStartRejectsEmptyAndInvalidSeedingPolicyMode(t *testing.T) {
	router, _, _ := setupAPITestRouter(t)
	for _, mode := range []string{"", "invalid_mode"} {
		body := fmt.Sprintf(`{"files":[{"index":0,"priority":"normal"}],"seedingPolicy":{"mode":%q}}`, mode)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/missing/torrent/start", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("mode=%q expected 400, got status=%d body=%s", mode, rec.Code, rec.Body.String())
		}
	}
}
