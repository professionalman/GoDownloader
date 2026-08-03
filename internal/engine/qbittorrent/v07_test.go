package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"downloader/internal/job"
	"downloader/internal/networkpolicy"
)

func TestSeedingPolicyRoundsNativeMinutesUp(t *testing.T) {
	var ratio, minutes string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/setShareLimits":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			ratio = r.Form.Get("ratioLimit")
			minutes = r.Form.Get("seedingTimeLimit")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	engine := NewEngine(server.URL, "user", "pass", 5)
	value := 1.5
	seconds := int64(61)
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "owned-hash"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatioOrDuration, RatioLimit: &value, TimeLimitSeconds: &seconds,
	}); err != nil {
		t.Fatal(err)
	}
	if ratio != "1.5" || minutes != "2" {
		t.Fatalf("ratio=%q minutes=%q", ratio, minutes)
	}
}

func TestPerTorrentLimitUsesOnlyPersistedJobHash(t *testing.T) {
	var hashes string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/setDownloadLimit":
			_ = r.ParseForm()
			hashes = r.Form.Get("hashes")
		}
	}))
	defer server.Close()
	engine := NewEngine(server.URL, "user", "pass", 5)
	if err := engine.SetDownloadLimit(context.Background(), &job.Job{ID: "job", EngineID: "persisted-hash"}, 1000); err != nil {
		t.Fatal(err)
	}
	if hashes != "persisted-hash" {
		t.Fatalf("hashes=%q", hashes)
	}
}

func TestApplySeedingPolicyModesAndFormEncoding(t *testing.T) {
	var requestedPath string
	var contentType string
	var formValues url.Values

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/setShareLimits":
			requestedPath = r.URL.Path
			contentType = r.Header.Get("Content-Type")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			formValues = r.Form
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	engine := NewEngine(server.URL, "user", "pass", 5)
	ratioVal := 1.5
	durationSec := int64(150) // 2.5 min -> 3 min

	// 1. Mode None: Must NOT make HTTP request to setShareLimits
	requestedPath = ""
	formValues = nil
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "hash-none"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeNone,
	}); err != nil {
		t.Fatalf("mode none returned error: %v", err)
	}
	if requestedPath != "" {
		t.Fatalf("expected no HTTP request for mode none, got %s", requestedPath)
	}

	// 2. Mode Unlimited: sends -1, -1, -1, -1
	requestedPath = ""
	formValues = nil
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "hash-unlimited"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeUnlimited,
	}); err != nil {
		t.Fatal(err)
	}
	if contentType != "application/x-www-form-urlencoded" {
		t.Fatalf("expected application/x-www-form-urlencoded, got %q", contentType)
	}
	if formValues.Get("hashes") != "hash-unlimited" || formValues.Get("ratioLimit") != "-1" ||
		formValues.Get("seedingTimeLimit") != "-1" || formValues.Get("inactiveSeedingTimeLimit") != "-1" ||
		formValues.Get("shareLimitAction") != "-1" {
		t.Fatalf("unexpected form values for unlimited: %+v", formValues)
	}

	// 3. Mode Ratio: ratio=1.5, time=-1, inactive=-1, action=-1
	requestedPath = ""
	formValues = nil
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "hash-ratio"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatio, RatioLimit: &ratioVal,
	}); err != nil {
		t.Fatal(err)
	}
	if formValues.Get("hashes") != "hash-ratio" || formValues.Get("ratioLimit") != "1.5" ||
		formValues.Get("seedingTimeLimit") != "-1" || formValues.Get("inactiveSeedingTimeLimit") != "-1" ||
		formValues.Get("shareLimitAction") != "-1" {
		t.Fatalf("unexpected form values for ratio: %+v", formValues)
	}

	// 4. Mode Duration: ratio=-1, time=3, inactive=-1, action=-1
	requestedPath = ""
	formValues = nil
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "hash-duration"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeDuration, TimeLimitSeconds: &durationSec,
	}); err != nil {
		t.Fatal(err)
	}
	if formValues.Get("hashes") != "hash-duration" || formValues.Get("ratioLimit") != "-1" ||
		formValues.Get("seedingTimeLimit") != "3" || formValues.Get("inactiveSeedingTimeLimit") != "-1" ||
		formValues.Get("shareLimitAction") != "-1" {
		t.Fatalf("unexpected form values for duration: %+v", formValues)
	}

	// 5. Mode RatioOrDuration: ratio=1.5, time=3, inactive=-1, action=-1
	requestedPath = ""
	formValues = nil
	if err := engine.ApplySeedingPolicy(context.Background(), &job.Job{EngineID: "hash-both"}, networkpolicy.SeedingPolicy{
		Mode: networkpolicy.SeedingModeRatioOrDuration, RatioLimit: &ratioVal, TimeLimitSeconds: &durationSec,
	}); err != nil {
		t.Fatal(err)
	}
	if formValues.Get("hashes") != "hash-both" || formValues.Get("ratioLimit") != "1.5" ||
		formValues.Get("seedingTimeLimit") != "3" || formValues.Get("inactiveSeedingTimeLimit") != "-1" ||
		formValues.Get("shareLimitAction") != "-1" {
		t.Fatalf("unexpected form values for ratio_or_duration: %+v", formValues)
	}
}

func TestPostFormBoundedResponseBodyInSanitizedError(t *testing.T) {
	var respBodyToSend string
	var statusCodeToSend int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Header().Set("Set-Cookie", "SID=12345; Path=/; HttpOnly")
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/setShareLimits":
			w.WriteHeader(statusCodeToSend)
			_, _ = w.Write([]byte(respBodyToSend))
		}
	}))
	engine := NewEngine(server.URL, "user", "pass", 5)

	// Test 1: HTTP 400 with qBittorrent error message
	statusCodeToSend = 400
	respBodyToSend = "Missing required parameters: shareLimitAction"

	err := engine.client.SetShareLimits(context.Background(), "hash123", 1.5, 60)
	if err == nil {
		t.Fatal("expected error on HTTP 400")
	}
	expectedMsg := "/api/v2/torrents/setShareLimits failed with status 400: Missing required parameters: shareLimitAction"
	if err.Error() != expectedMsg {
		t.Fatalf("expected error %q, got %q", expectedMsg, err.Error())
	}

	// Test 2: Bounded reading of large error response (>4 KiB)
	largeBody := make([]byte, 8192)
	for i := range largeBody {
		largeBody[i] = 'A'
	}
	statusCodeToSend = 500
	respBodyToSend = string(largeBody)

	err = engine.client.SetShareLimits(context.Background(), "hash123", 1.5, 60)
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
	if len(err.Error()) > 4200 { // 4096 + prefix length
		t.Fatalf("error message exceeded bounded size: length=%d", len(err.Error()))
	}
}
