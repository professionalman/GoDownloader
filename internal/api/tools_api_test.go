package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
	"downloader/internal/toolmanager"
)

type fakeToolService struct {
	installErr error
	installs   int
}

func (f *fakeToolService) YtdlpStatus(context.Context) toolmanager.ManagedToolStatus {
	return toolmanager.ManagedToolStatus{Tool: "yt-dlp", Available: true, Version: "2026.10.26", Ownership: "managed",
		Authenticated: true, PlatformSupported: true, CanUpdate: true}
}

func (f *fakeToolService) InstallYtdlp(context.Context) (*toolmanager.AcquisitionResult, error) {
	f.installs++
	if f.installErr != nil {
		return nil, f.installErr
	}
	return &toolmanager.AcquisitionResult{Tool: "yt-dlp", Version: "2026.10.26", Ownership: "managed", Status: "restart_required", RestartRequired: true}, nil
}

func (f *fakeToolService) UpdateYtdlp(ctx context.Context) (*toolmanager.AcquisitionResult, error) {
	return f.InstallYtdlp(ctx)
}

func (f *fakeToolService) CheckYtdlpUpdate(context.Context) (*toolmanager.UpdateCheckResult, error) {
	return &toolmanager.UpdateCheckResult{Tool: "yt-dlp", LatestVersion: "2026.10.26"}, nil
}

func setupToolsRouter(t *testing.T, svc api.ManagedToolService) (http.Handler, *api.SecurityManager) {
	return setupToolsRouterWithAuth(t, svc, false)
}

func setupToolsRouterWithAuth(t *testing.T, svc api.ManagedToolService, disableAuth bool) (http.Handler, *api.SecurityManager) {
	t.Helper()
	tempDir := t.TempDir()
	db, err := database.New(filepath.Join(tempDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	downloadDir, dataDir := filepath.Join(tempDir, "downloads"), filepath.Join(tempDir, "data")
	settingsSvc := settings.NewSettingsService(database.NewSQLiteSettingsRepository(db), downloadDir, dataDir)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	bus := events.NewInMemoryBus()
	mgr := job.NewManager(database.NewSQLiteJobRepository(db), engine.NewRegistry(), bus, downloadDir, nil, dataDir)
	cfg := &config.Config{ListenAddr: "127.0.0.1:8080", DownloadDir: downloadDir, DataDir: dataDir, DisableAuth: disableAuth}
	sec := api.NewSecurityManager(cfg)
	return api.NewRouter(cfg, mgr, events.NewSSEHandler(bus), settingsSvc, sec, catRepo, svc), sec
}

func toolsRequest(method, path string, sec *api.SecurityManager, authenticated bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"url":"https://evil.example/yt-dlp.exe","sha256":"00"}`))
	req.Host = "127.0.0.1:8080"
	if authenticated {
		req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: sec.SessionToken()})
		if method != http.MethodGet {
			req.Header.Set(api.CSRFHeaderName, sec.CSRFToken())
		}
	}
	return req
}

func TestToolsAPI_RequiresSessionAndCSRF(t *testing.T) {
	svc := &fakeToolService{}
	router, sec := setupToolsRouter(t, svc)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tools/yt-dlp"},
		{http.MethodPost, "/api/v1/tools/yt-dlp/install"},
		{http.MethodPost, "/api/v1/tools/yt-dlp/update"},
		{http.MethodPost, "/api/v1/tools/yt-dlp/check-update"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, toolsRequest(tc.method, tc.path, sec, false))
		if rec.Code < 400 {
			t.Fatalf("%s %s without session returned %d", tc.method, tc.path, rec.Code)
		}
	}
	// Session cookie without CSRF header must not trigger an install.
	req := toolsRequest(http.MethodPost, "/api/v1/tools/yt-dlp/install", sec, true)
	req.Header.Del(api.CSRFHeaderName)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code < 400 || svc.installs != 0 {
		t.Fatalf("install without CSRF token: status %d installs %d", rec.Code, svc.installs)
	}
}

// Regression (A-7 + final safety check B): DISABLE_AUTH bypasses session, CSRF, and the generic origin check, so without a dedicated
// guard any web page could POST a simple cross-origin form to trigger an executable install.
func TestToolsAPI_InstallRefusedWhenAuthDisabled(t *testing.T) {
	svc := &fakeToolService{}
	router, sec := setupToolsRouterWithAuth(t, svc, true)
	for _, path := range []string{"/api/v1/tools/yt-dlp/install", "/api/v1/tools/yt-dlp/update", "/api/v1/tools/yt-dlp/check-update"} {
		for _, hdr := range []struct{ name, value string }{
			{"Origin", "https://evil.example"},
			{"Referer", "https://evil.example/page"},
		} {
			req := toolsRequest(http.MethodPost, path, sec, false)
			req.Header.Set("Content-Type", "text/plain") // CORS "simple request": no preflight
			req.Header.Set(hdr.name, hdr.value)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with %s %s: got %d, want 403", path, hdr.name, hdr.value, rec.Code)
			}
		}
	}
	if svc.installs != 0 {
		t.Fatalf("cross-origin request reached the install service %d time(s)", svc.installs)
	}
	// Origin checks are not authentication: with auth disabled, a local non-browser client (no Origin/Referer,
	// e.g. another user's process on the loopback interface) must not be able to install executables either.
	for _, path := range []string{"/api/v1/tools/yt-dlp/install", "/api/v1/tools/yt-dlp/update", "/api/v1/tools/yt-dlp/check-update"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, toolsRequest(http.MethodPost, path, sec, false))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "AUTH_REQUIRED") {
			t.Fatalf("%s from local client with auth disabled: got %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if svc.installs != 0 {
		t.Fatalf("install reachable with authentication disabled (%d call(s))", svc.installs)
	}
	// Read-only status stays available.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, toolsRequest(http.MethodGet, "/api/v1/tools/yt-dlp", sec, false))
	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint with auth disabled: %d", rec.Code)
	}
}

func TestToolsAPI_AuthenticatedResponsesArePathFree(t *testing.T) {
	svc := &fakeToolService{}
	router, sec := setupToolsRouter(t, svc)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tools/yt-dlp"},
		{http.MethodPost, "/api/v1/tools/yt-dlp/install"},
		{http.MethodPost, "/api/v1/tools/yt-dlp/check-update"},
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, toolsRequest(tc.method, tc.path, sec, true))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		var decoded map[string]any
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		for _, forbidden := range []string{"executablePath", ":\\", "https://", "githubusercontent"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s response exposes %q: %s", tc.path, forbidden, body)
			}
		}
	}
}

func TestToolsAPI_TypedErrors(t *testing.T) {
	cases := map[string]struct {
		err    error
		status int
		code   string
	}{
		"in use":    {toolmanager.ErrToolInUse, http.StatusConflict, "TOOL_IN_USE"},
		"signature": {fmt.Errorf("%w: bad", toolmanager.ErrInvalidSignature), http.StatusBadGateway, "INVALID_SIGNATURE"},
		"hash":      {toolmanager.ErrHashMismatch, http.StatusBadGateway, "HASH_MISMATCH"},
		"restart":   {toolmanager.ErrRestartRequired, http.StatusConflict, "RESTART_REQUIRED"},
		"platform":  {toolmanager.ErrUnsupportedPlatform, http.StatusNotImplemented, "UNSUPPORTED_PLATFORM"},
		"downgrade": {toolmanager.ErrDowngradeRefused, http.StatusConflict, "DOWNGRADE_REFUSED"},
		"network":   {fmt.Errorf("%w: request to release-assets.githubusercontent.com failed", toolmanager.ErrAcquisitionNetwork), http.StatusBadGateway, "NETWORK_FAILURE"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			router, sec := setupToolsRouter(t, &fakeToolService{installErr: tc.err})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, toolsRequest(http.MethodPost, "/api/v1/tools/yt-dlp/install", sec, true))
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("got %d %s; want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
			if strings.Contains(rec.Body.String(), "githubusercontent") {
				t.Fatal("internal error detail leaked to the client")
			}
		})
	}
}
