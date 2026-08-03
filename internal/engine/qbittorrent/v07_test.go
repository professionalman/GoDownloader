package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
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
