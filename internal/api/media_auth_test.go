package api_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/api"
	"downloader/internal/config"
	"downloader/internal/database"
	"downloader/internal/engine"
	"downloader/internal/job"
	"downloader/internal/mediaauth"
	"downloader/internal/securestore"
	"downloader/internal/settings"
	"downloader/internal/storage"
)

func setupMediaAuthAPITestRouter(t *testing.T, withCipher bool) (http.Handler, *mediaauth.Service, *settings.SettingsService) {
	t.Helper()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")
	db, err := database.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	jobRepo := database.NewSQLiteJobRepository(db)
	queueRepo := database.NewSQLiteQueueRepository(db)
	settingsRepo := database.NewSQLiteSettingsRepository(db)
	secretRepo := database.NewSQLiteSecretRepository(db)
	catRepo := storage.NewSQLiteCategoryRepository(db.Conn())

	var cipherInstance *securestore.Cipher
	if withCipher {
		key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		block, _ := aes.NewCipher(key)
		aead, _ := cipher.NewGCM(block)
		os.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		c, err := securestore.NewFromEnvironment()
		if err == nil && c.Available() {
			cipherInstance = c
		}
		_ = aead
	}

	secretStore := securestore.NewStore(secretRepo, cipherInstance)

	downloadDir := filepath.Join(tempDir, "downloads")
	dataDir := filepath.Join(tempDir, "data")
	tempAuthDir := filepath.Join(dataDir, "tmp", "auth")

	settingsSvc := settings.NewSettingsService(settingsRepo, downloadDir, dataDir, secretStore)
	storageSvc := storage.NewStorageService(catRepo, settingsSvc, storage.NewOSFreeSpaceProvider(), downloadDir, dataDir)
	registry := engine.NewRegistry()
	mgr := job.NewManager(jobRepo, registry, nil, downloadDir, nil, dataDir)
	mgr.SetQueueRepository(queueRepo)
	mgr.SetSettingsService(settingsSvc)
	mgr.SetStorageService(storageSvc)

	mediaAuthSvc := mediaauth.NewService(settingsRepo, secretStore, tempAuthDir)

	cfg := &config.Config{
		DownloadDir: downloadDir,
		DataDir:     dataDir,
	}

	router := api.NewRouter(cfg, mgr, nil, settingsSvc, catRepo, mediaAuthSvc)
	return router, mediaAuthSvc, settingsSvc
}

func TestMediaAuthAPI_GetDefaults(t *testing.T) {
	router, _, _ := setupMediaAuthAPITestRouter(t, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media-auth", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res mediaauth.Settings
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}

	if res.Mode != mediaauth.ModeNone {
		t.Errorf("expected mode none, got %q", res.Mode)
	}
	if res.HasCookieFile {
		t.Errorf("expected hasCookieFile false")
	}
}

func TestMediaAuthAPI_UpdateBrowserMode(t *testing.T) {
	router, _, _ := setupMediaAuthAPITestRouter(t, true)

	// Valid browser
	req := httptest.NewRequest(http.MethodPut, "/api/v1/media-auth", bytes.NewBufferString(
		`{"mode":"browser","browser":"Chrome","profile":"Default"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var res mediaauth.Settings
	if err := json.NewDecoder(rec.Body).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.Mode != mediaauth.ModeBrowser || res.Browser != "chrome" || res.Profile != "Default" {
		t.Errorf("unexpected response: %+v", res)
	}

	// Invalid browser
	reqBad := httptest.NewRequest(http.MethodPut, "/api/v1/media-auth", bytes.NewBufferString(
		`{"mode":"browser","browser":"InternetExplorer"}`,
	))
	reqBad.Header.Set("Content-Type", "application/json")
	recBad := httptest.NewRecorder()
	router.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad browser, got %d", recBad.Code)
	}
}

func TestMediaAuthAPI_CookieFileModeWithoutCookiesRejected(t *testing.T) {
	router, _, _ := setupMediaAuthAPITestRouter(t, true)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/media-auth", bytes.NewBufferString(
		`{"mode":"cookie_file"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when enabling cookie_file mode without imported cookies, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMediaAuthAPI_ImportReplaceDeleteCookies(t *testing.T) {
	router, _, _ := setupMediaAuthAPITestRouter(t, true)

	secretSample := "SAMPLE_SECRET_COOKIE_TOKEN_ABC123"
	cookieContent := "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tauth_token\t" + secretSample + "\n"

	// 1. Import
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "cookies.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(part, cookieContent)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media-auth/cookies", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("import failed with status %d: %s", rec.Code, rec.Body.String())
	}

	// 33. Verify API response never contains sample cookie value
	respBody := rec.Body.String()
	if strings.Contains(respBody, secretSample) {
		t.Fatalf("SECURITY VIOLATION: API response contains sample cookie value: %s", respBody)
	}

	var importRes mediaauth.Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &importRes); err != nil {
		t.Fatal(err)
	}
	if !importRes.HasCookieFile {
		t.Errorf("expected hasCookieFile true after import")
	}

	// 2. Now switch mode to cookie_file
	reqPut := httptest.NewRequest(http.MethodPut, "/api/v1/media-auth", bytes.NewBufferString(`{"mode":"cookie_file"}`))
	reqPut.Header.Set("Content-Type", "application/json")
	recPut := httptest.NewRecorder()
	router.ServeHTTP(recPut, reqPut)
	if recPut.Code != http.StatusOK {
		t.Fatalf("failed to set cookie_file mode: %s", recPut.Body.String())
	}

	// 3. Delete cookies
	reqDel := httptest.NewRequest(http.MethodDelete, "/api/v1/media-auth/cookies", nil)
	recDel := httptest.NewRecorder()
	router.ServeHTTP(recDel, reqDel)
	if recDel.Code != http.StatusOK {
		t.Fatalf("delete failed with status %d: %s", recDel.Code, recDel.Body.String())
	}

	var delRes mediaauth.Settings
	if err := json.Unmarshal(recDel.Body.Bytes(), &delRes); err != nil {
		t.Fatal(err)
	}
	if delRes.HasCookieFile {
		t.Errorf("expected hasCookieFile false after delete")
	}
	if delRes.Mode != mediaauth.ModeNone {
		t.Errorf("expected mode to reset to none after cookie deletion, got %q", delRes.Mode)
	}
}

func TestMediaAuthAPI_ImportInvalidFileRejected(t *testing.T) {
	router, _, _ := setupMediaAuthAPITestRouter(t, true)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "bad.txt")
	_, _ = io.WriteString(part, "plain text not a cookie file")
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media-auth/cookies", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid cookie file, got %d: %s", rec.Code, rec.Body.String())
	}

	// 34. Verify error response does not expose file contents
	if strings.Contains(rec.Body.String(), "plain text not a cookie file") {
		t.Fatalf("SECURITY VIOLATION: error response leaked uploaded file content")
	}
}

func TestMediaAuthAPI_SecretStoreUnavailableRejectsImport(t *testing.T) {
	// Router without encryption key configured
	router, _, _ := setupMediaAuthAPITestRouter(t, false)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "cookies.txt")
	_, _ = io.WriteString(part, "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\ts\tv\n")
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media-auth/cookies", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when securestore is unavailable, got %d: %s", rec.Code, rec.Body.String())
	}
}
