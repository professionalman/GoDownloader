package api_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
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
