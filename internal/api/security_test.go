package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/events"
	"downloader/internal/job"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupSecurityTestRouter(t *testing.T, disableAuth bool) (http.Handler, *api.SecurityManager, *config.Config) {
	return setupSecurityTestRouterWithDevMode(t, disableAuth, false)
}

func setupSecurityTestRouterWithDevMode(t *testing.T, disableAuth, devMode bool) (http.Handler, *api.SecurityManager, *config.Config) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())

	downloadDir := filepath.Join(tempDir, "downloads")
	dataDir := filepath.Join(tempDir, "data")

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), downloadDir, dataDir)
	registry := engine.NewRegistry()
	bus := events.NewInMemoryBus()
	mgr := job.NewManager(jobRepo, registry, bus, downloadDir, nil, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)

	sseHandler := events.NewSSEHandler(bus)
	sseHandler.SetCursorProvider(mgr)

	cfg := &config.Config{
		ListenAddr:  "127.0.0.1:8080",
		DownloadDir: downloadDir,
		DataDir:     dataDir,
		DisableAuth: disableAuth,
		DevMode:     devMode,
	}

	secManager := api.NewSecurityManager(cfg)
	router := api.NewRouter(cfg, mgr, sseHandler, settingsSvc, secManager, catRepo)
	return router, secManager, cfg
}

func TestSecurity_ProductionDefaultIsLoopbackBound(t *testing.T) {
	cfg := config.New()
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("expected default ListenAddr to be 127.0.0.1:8080, got %q", cfg.ListenAddr)
	}
	if cfg.DisableAuth {
		t.Fatalf("production default DisableAuth must be false")
	}
	if cfg.DevMode {
		t.Fatalf("production default DevMode must be false")
	}

	// Loopback addresses pass validation
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		if err := api.CheckNonLoopbackBind(addr); err != nil {
			t.Fatalf("expected loopback %q to pass, got: %v", addr, err)
		}
	}
}

func TestSecurity_NonLoopbackRejectedUnconditionally(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8080", "192.168.1.100:8080", ":8080"} {
		if err := api.CheckNonLoopbackBind(addr); err == nil {
			t.Fatalf("expected non-loopback %q to fail unconditionally", addr)
		}
	}

	// Even if environment variable was previously set, override is removed and fails unconditionally
	t.Setenv("GODOWNLOADER_ALLOW_NON_LOOPBACK", "1")
	if err := api.CheckNonLoopbackBind("0.0.0.0:8080"); err == nil {
		t.Fatalf("expected non-loopback bind to be unconditionally rejected even with env var set")
	}
}

func TestSecurity_LegitimateSameOriginRequestSucceeds(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for legitimate authenticated request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_ForeignOriginReadRejected(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "https://malicious.evil")
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for foreign Origin read, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_ForeignOriginMutatingRejected(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "https://malicious.evil")
	req.Header.Set(api.CSRFHeaderName, secManager.CSRFToken())
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for foreign Origin mutating request, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_MutatingRequestWithoutCSRFOrSessionRejected(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	// 1. Without session -> 401
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req1.Host = "127.0.0.1:8080"
	req1.Header.Set(api.CSRFHeaderName, secManager.CSRFToken())
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without session, got %d", rec1.Code)
	}

	// 2. With session but without CSRF -> 403
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req2.Host = "127.0.0.1:8080"
	req2.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden without CSRF header, got %d", rec2.Code)
	}

	// 3. With session and invalid CSRF -> 403
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", nil)
	req3.Host = "127.0.0.1:8080"
	req3.Header.Set(api.CSRFHeaderName, "invalid-csrf-value")
	req3.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden with wrong CSRF header, got %d", rec3.Code)
	}
}

func TestSecurity_InvalidCredentialOrSessionRejected(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	// Invalid cookie
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req1.Host = "127.0.0.1:8080"
	req1.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: "invalid-session-token-value"})
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for invalid session cookie, got %d", rec1.Code)
	}
}

func TestSecurity_BearerTokenNotAccepted(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	// Bearer header without cookie must be rejected (pure cookie auth invariant)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Authorization", "Bearer "+secManager.SessionToken())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when attempting Bearer token auth, got %d", rec.Code)
	}
}

func TestSecurity_SSEWithoutValidAuthFails(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for SSE without auth, got %d", rec.Code)
	}
}

func TestSecurity_SSEWithValidAuthWorks(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	req.Host = "127.0.0.1:8080"
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(rec, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("expected text/event-stream content type, got %q", rec.Header().Get("Content-Type"))
	}
}

func TestSecurity_StateSyncSnapshotRequiresAuth(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	// Unauthenticated -> 401
	reqUnauth := httptest.NewRequest(http.MethodGet, "/api/v1/sync/snapshot", nil)
	reqUnauth.Host = "127.0.0.1:8080"
	recUnauth := httptest.NewRecorder()
	router.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for snapshot without auth, got %d", recUnauth.Code)
	}

	// Authenticated -> 200
	reqAuth := httptest.NewRequest(http.MethodGet, "/api/v1/sync/snapshot", nil)
	reqAuth.Host = "127.0.0.1:8080"
	reqAuth.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})
	recAuth := httptest.NewRecorder()
	router.ServeHTTP(recAuth, reqAuth)
	if recAuth.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for snapshot with auth, got %d: %s", recAuth.Code, recAuth.Body.String())
	}
}

func TestSecurity_MaliciousHostOrDNSRebindingRejected(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	for _, hostileHost := range []string{"evil-domain.com", "evil.com:8080", "192.168.1.1:8080", "rebind.attacker.net"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
		req.Host = hostileHost
		req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for hostile Host %q, got %d: %s", hostileHost, rec.Code, rec.Body.String())
		}
	}
}

func TestSecurity_CORSWildcardNotEmitted(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	// 1. Foreign origin does NOT receive Access-Control-Allow-Origin
	reqForeign := httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil)
	reqForeign.Host = "127.0.0.1:8080"
	reqForeign.Header.Set("Origin", "https://evil.com")
	reqForeign.Header.Set("Access-Control-Request-Method", "POST")
	recForeign := httptest.NewRecorder()
	router.ServeHTTP(recForeign, reqForeign)

	if allowOrigin := recForeign.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "" {
		t.Fatalf("SECURITY VIOLATION: foreign origin received Access-Control-Allow-Origin: %q", allowOrigin)
	}

	// 2. Loopback origin receives explicit origin, never wildcard *
	reqLocal := httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil)
	reqLocal.Host = "127.0.0.1:8080"
	reqLocal.Header.Set("Origin", "http://127.0.0.1:8080")
	reqLocal.Header.Set("Access-Control-Request-Method", "GET")
	recLocal := httptest.NewRecorder()
	router.ServeHTTP(recLocal, reqLocal)

	allowOrigin := recLocal.Header().Get("Access-Control-Allow-Origin")
	if allowOrigin == "*" {
		t.Fatalf("SECURITY VIOLATION: CORS emitted wildcard *")
	}
	if allowOrigin != "http://127.0.0.1:8080" {
		t.Fatalf("expected Access-Control-Allow-Origin: http://127.0.0.1:8080, got %q", allowOrigin)
	}
}

func TestSecurity_PNAPreflightDoesNotGrantArbitraryOrigins(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodOptions, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "https://malicious.com")
	req.Header.Set("Access-Control-Request-Private-Network", "true")
	req.Header.Set("Access-Control-Request-Method", "GET")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if pna := rec.Header().Get("Access-Control-Allow-Private-Network"); pna == "true" {
		t.Fatalf("SECURITY VIOLATION: arbitrary foreign origin granted Access-Control-Allow-Private-Network: true")
	}
}

func TestSecurity_SensitiveTokenNotAcceptedFromURL(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	// Attempting to authenticate via URL query string parameter
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?token="+secManager.SessionToken(), nil)
	req.Host = "127.0.0.1:8080"

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when credential is provided via URL query parameter, got %d", rec.Code)
	}
}

func TestSecurity_SessionBootstrapSetsCookiesAndDoesNotExposeSessionSecret(t *testing.T) {
	router, secManager, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.Host = "127.0.0.1:8080"

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for session bootstrap, got %d: %s", rec.Code, rec.Body.String())
	}

	var rawMap map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&rawMap); err != nil {
		t.Fatalf("failed to decode bootstrap response: %v", err)
	}

	// 1. Invariant: Status and CSRFToken must be returned
	if rawMap["status"] != "ok" {
		t.Fatalf("expected status: ok, got: %v", rawMap["status"])
	}
	if rawMap["csrfToken"] != secManager.CSRFToken() {
		t.Fatalf("expected csrfToken: %q, got: %v", secManager.CSRFToken(), rawMap["csrfToken"])
	}

	// 2. Invariant: sessionToken / token / bearer credential MUST NOT be in response body!
	if _, exists := rawMap["token"]; exists {
		t.Fatalf("SECURITY VIOLATION: sessionToken exposed in JSON response body as 'token'")
	}
	if _, exists := rawMap["sessionToken"]; exists {
		t.Fatalf("SECURITY VIOLATION: sessionToken exposed in JSON response body as 'sessionToken'")
	}

	// 3. Invariant: Cookies must be set with correct properties
	cookies := rec.Result().Cookies()
	var sessionCookie, csrfCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == api.SessionCookieName {
			sessionCookie = c
		}
		if c.Name == api.CSRFCookieName {
			csrfCookie = c
		}
	}

	if sessionCookie == nil || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie invalid: %+v", sessionCookie)
	}
	if sessionCookie.Value != secManager.SessionToken() {
		t.Fatalf("session cookie value mismatch: expected %q, got %q", secManager.SessionToken(), sessionCookie.Value)
	}
	if csrfCookie == nil || csrfCookie.HttpOnly || csrfCookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("csrf cookie invalid: %+v", csrfCookie)
	}
	if csrfCookie.Value != secManager.CSRFToken() {
		t.Fatalf("csrf cookie value mismatch: expected %q, got %q", secManager.CSRFToken(), csrfCookie.Value)
	}
}

func TestSecurity_SessionBootstrapForeignOriginRejected(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "https://evil.com")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for session bootstrap from foreign Origin, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_SessionBootstrapMaliciousHostRejected(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.Host = "evil.com:8080"

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for session bootstrap with malicious Host, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_ProductionRejectsDevOrigin(t *testing.T) {
	// Production default (DevMode: false)
	router, secManager, _ := setupSecurityTestRouterWithDevMode(t, false, false)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:5173")
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for development origin http://localhost:5173 in production mode, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSecurity_DevModeAllowsDevOrigin(t *testing.T) {
	// Explicit development mode (DevMode: true)
	router, secManager, _ := setupSecurityTestRouterWithDevMode(t, false, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://localhost:5173")
	req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for development origin http://localhost:5173 in dev mode, got %d: %s", rec.Code, rec.Body.String())
	}
	if allowOrigin := rec.Header().Get("Access-Control-Allow-Origin"); allowOrigin != "http://localhost:5173" {
		t.Fatalf("expected Access-Control-Allow-Origin: http://localhost:5173, got %q", allowOrigin)
	}
}

func TestSecurity_ArbitraryLocalhostPortRejected(t *testing.T) {
	// Even in DevMode, arbitrary localhost ports must NOT be trusted
	router, secManager, _ := setupSecurityTestRouterWithDevMode(t, false, true)

	for _, arbitraryOrigin := range []string{"http://localhost:9999", "http://127.0.0.1:3000", "http://localhost:8000"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
		req.Host = "127.0.0.1:8080"
		req.Header.Set("Origin", arbitraryOrigin)
		req.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager.SessionToken()})

		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for arbitrary origin %q, got %d: %s", arbitraryOrigin, rec.Code, rec.Body.String())
		}
	}
}

func TestSecurity_BackendRestartStaleCookieRecovers(t *testing.T) {
	// 1. Initial backend instance
	router1, secManager1, cfg := setupSecurityTestRouter(t, false)

	// Legitimate client on instance 1
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req1.Host = "127.0.0.1:8080"
	req1.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager1.SessionToken()})
	rec1 := httptest.NewRecorder()
	router1.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("instance 1 request failed: %d", rec1.Code)
	}

	// 2. Backend restarts: fresh SecurityManager created with new random sessionToken
	secManager2 := api.NewSecurityManager(cfg)
	// Build router for restarted instance
	tempDir := t.TempDir()
	db, _ := database.New(filepath.Join(tempDir, "test2.db"))
	defer db.Close()
	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	settingsSvc := settings.NewSettingsService(settingsRepo, cfg.DownloadDir, cfg.DataDir)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), cfg.DownloadDir, cfg.DataDir)
	bus := events.NewInMemoryBus()
	mgr := job.NewManager(jobRepo, engine.NewRegistry(), bus, cfg.DownloadDir, nil, cfg.DataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)
	sseHandler := events.NewSSEHandler(bus)
	sseHandler.SetCursorProvider(mgr)
	router2 := api.NewRouter(cfg, mgr, sseHandler, settingsSvc, secManager2, catRepo)

	// 3. Client sends old session cookie to restarted backend -> receives 401
	reqStale := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	reqStale.Host = "127.0.0.1:8080"
	reqStale.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager1.SessionToken()})
	recStale := httptest.NewRecorder()
	router2.ServeHTTP(recStale, reqStale)
	if recStale.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for stale session cookie after restart, got %d", recStale.Code)
	}

	// 4. Client re-bootstraps session via GET /api/v1/auth/session on instance 2
	reqBootstrap := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	reqBootstrap.Host = "127.0.0.1:8080"
	recBootstrap := httptest.NewRecorder()
	router2.ServeHTTP(recBootstrap, reqBootstrap)
	if recBootstrap.Code != http.StatusOK {
		t.Fatalf("re-bootstrap failed: %d", recBootstrap.Code)
	}

	// Extract new session cookie
	var newSessionCookie *http.Cookie
	for _, c := range recBootstrap.Result().Cookies() {
		if c.Name == api.SessionCookieName {
			newSessionCookie = c
		}
	}
	if newSessionCookie == nil || newSessionCookie.Value != secManager2.SessionToken() {
		t.Fatalf("re-bootstrap did not provide valid new session cookie")
	}

	// 5. Retried request with new cookie succeeds
	reqRetry := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	reqRetry.Host = "127.0.0.1:8080"
	reqRetry.AddCookie(newSessionCookie)
	recRetry := httptest.NewRecorder()
	router2.ServeHTTP(recRetry, reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("retried request with re-bootstrapped cookie failed: %d", recRetry.Code)
	}
}

func TestSecurity_HostValidationDoesNotAuthorizeLANClient(t *testing.T) {
	router, _, _ := setupSecurityTestRouter(t, false)

	// A hostile or remote client sends Host: 127.0.0.1:8080 but has no valid session cookie
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Must fail with 401 Unauthorized: Host validation alone provides zero authorization
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when Host header matches but session cookie is absent, got %d", rec.Code)
	}
}

func TestSecurity_BackendRestartSSEStaleCookieRecovers(t *testing.T) {
	// 1. Initial backend instance
	router1, secManager1, cfg := setupSecurityTestRouter(t, false)

	// Legitimate SSE client on instance 1
	ctx1, cancel1 := context.WithCancel(context.Background())
	req1 := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx1)
	req1.Host = "127.0.0.1:8080"
	req1.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager1.SessionToken()})
	rec1 := httptest.NewRecorder()
	done1 := make(chan struct{})
	go func() {
		router1.ServeHTTP(rec1, req1)
		close(done1)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel1()
	<-done1
	if rec1.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("instance 1 SSE connection failed: %q", rec1.Header().Get("Content-Type"))
	}

	// 2. Backend restarts: fresh SecurityManager created with new random sessionToken
	secManager2 := api.NewSecurityManager(cfg)
	tempDir := t.TempDir()
	db, _ := database.New(filepath.Join(tempDir, "test-sse-restart.db"))
	defer db.Close()
	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())
	settingsSvc := settings.NewSettingsService(settingsRepo, cfg.DownloadDir, cfg.DataDir)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), cfg.DownloadDir, cfg.DataDir)
	bus := events.NewInMemoryBus()
	mgr := job.NewManager(jobRepo, engine.NewRegistry(), bus, cfg.DownloadDir, nil, cfg.DataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)
	sseHandler := events.NewSSEHandler(bus)
	sseHandler.SetCursorProvider(mgr)
	router2 := api.NewRouter(cfg, mgr, sseHandler, settingsSvc, secManager2, catRepo)

	// 3. Stale SSE connection attempt to restarted backend -> 401 Unauthorized
	reqStale := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	reqStale.Host = "127.0.0.1:8080"
	reqStale.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: secManager1.SessionToken()})
	recStale := httptest.NewRecorder()
	router2.ServeHTTP(recStale, reqStale)
	if recStale.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for stale SSE cookie after restart, got %d", recStale.Code)
	}

	// 4. Client re-bootstraps session via GET /api/v1/auth/session on instance 2
	reqBootstrap := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	reqBootstrap.Host = "127.0.0.1:8080"
	recBootstrap := httptest.NewRecorder()
	router2.ServeHTTP(recBootstrap, reqBootstrap)
	if recBootstrap.Code != http.StatusOK {
		t.Fatalf("re-bootstrap failed: %d", recBootstrap.Code)
	}

	var newSessionCookie *http.Cookie
	for _, c := range recBootstrap.Result().Cookies() {
		if c.Name == api.SessionCookieName {
			newSessionCookie = c
		}
	}
	if newSessionCookie == nil || newSessionCookie.Value != secManager2.SessionToken() {
		t.Fatalf("re-bootstrap did not provide valid new session cookie")
	}

	// 5. Retried SSE connection with re-bootstrapped cookie succeeds
	ctx2, cancel2 := context.WithCancel(context.Background())
	reqRetry := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx2)
	reqRetry.Host = "127.0.0.1:8080"
	reqRetry.AddCookie(newSessionCookie)
	recRetry := httptest.NewRecorder()
	done2 := make(chan struct{})
	go func() {
		router2.ServeHTTP(recRetry, reqRetry)
		close(done2)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel2()
	<-done2

	if recRetry.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("retried SSE connection with re-bootstrapped cookie failed: %q", recRetry.Header().Get("Content-Type"))
	}
}
