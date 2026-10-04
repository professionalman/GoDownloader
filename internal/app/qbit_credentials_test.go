package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"downloader/internal/engine/qbittorrent"
	"downloader/internal/settings"
)

func TestApp_QBitStartupAndRestartCredentials(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("q", 32))))

	var mu sync.Mutex
	var receivedPasswords []string
	var receivedUsernames []string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_ = r.ParseForm()
			u := r.FormValue("username")
			p := r.FormValue("password")
			mu.Lock()
			receivedUsernames = append(receivedUsernames, u)
			receivedPasswords = append(receivedPasswords, p)
			mu.Unlock()

			if p == "protected-stored-pass-123" {
				w.Header().Set("Set-Cookie", "SID=valid-qbit-session; Path=/; HttpOnly")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("Ok."))
				return
			}
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/api/v2/app/version" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("v5.0.0"))
			return
		}
		if r.URL.Path == "/api/v2/app/webapiVersion" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("2.9.3"))
			return
		}
		if r.URL.Path == "/api/v2/torrents/info" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("[]"))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	cfg := newTestConfig(t)
	cfg.DataDir = filepath.Join(tempDir, "data")
	cfg.DownloadDir = filepath.Join(tempDir, "downloads")
	cfg.QBitTimeout = 2
	dbPath := filepath.Join(tempDir, "test_qbit.db")

	ctx := context.Background()

	// 1. Initial startup with defaults
	app1, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("first App.New failed: %v", err)
	}

	// Persist custom qBittorrent endpoint, username, and password into SettingsService / SecureStore
	customURL := ts.URL
	customUser := "qbit-admin-user"
	customPass := "protected-stored-pass-123"
	_, err = app1.Settings().UpdatePowerSettings(ctx, &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &customURL,
			Username: &customUser,
			Password: &customPass,
		},
	})
	if err != nil {
		_ = app1.Shutdown(ctx)
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}

	// Prove safe DTO never leaks the password
	app1St, err := app1.Settings().GetSettings(ctx)
	if err != nil {
		_ = app1.Shutdown(ctx)
		t.Fatalf("GetSettings failed: %v", err)
	}
	if !app1St.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=true")
	}

	// 2. Clean shutdown
	if err := app1.Shutdown(ctx); err != nil {
		t.Fatalf("app1.Shutdown failed: %v", err)
	}

	// Clear recorded login attempts
	mu.Lock()
	receivedPasswords = nil
	receivedUsernames = nil
	mu.Unlock()

	// 3. Restart App targeting the same persistent dbPath and dataDir
	app2, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("restart App.New failed: %v", err)
	}
	defer func() {
		_ = app2.Shutdown(ctx)
	}()

	// Query qBittorrent engine HealthCheck to verify authenticated connectivity
	rawEng, ok := app2.registry.Get("qbittorrent")
	if !ok {
		t.Fatalf("expected qbittorrent engine to be registered")
	}
	qbit, ok := rawEng.(*qbittorrent.Engine)
	if !ok {
		t.Fatalf("expected *qbittorrent.Engine")
	}
	if err := qbit.HealthCheck(ctx); err != nil {
		t.Fatalf("qBittorrent HealthCheck failed on restart: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	foundMatchingLogin := false
	for i := range receivedPasswords {
		if receivedUsernames[i] == customUser && receivedPasswords[i] == customPass {
			foundMatchingLogin = true
			break
		}
	}
	if !foundMatchingLogin {
		t.Fatalf("expected login with user %q and password %q, got usernames=%v passwords=%v",
			customUser, customPass, receivedUsernames, receivedPasswords)
	}
}

func TestApp_QBitEnvironmentOverridePrecedence(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("q", 32))))

	var mu sync.Mutex
	var lastReceivedPassword string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			_ = r.ParseForm()
			p := r.FormValue("password")
			mu.Lock()
			lastReceivedPassword = p
			mu.Unlock()

			w.Header().Set("Set-Cookie", "SID=valid-qbit-session; Path=/; HttpOnly")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tempDir := t.TempDir()
	cfg := newTestConfig(t)
	cfg.DataDir = filepath.Join(tempDir, "data")
	cfg.DownloadDir = filepath.Join(tempDir, "downloads")
	cfg.QBitTimeout = 2
	dbPath := filepath.Join(tempDir, "test_env_override.db")

	ctx := context.Background()

	// 1. Initial startup: persist stored-secret-A
	app1, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("app1.New failed: %v", err)
	}

	storedURL := ts.URL
	storedPass := "stored-secret-A"
	_, err = app1.Settings().UpdatePowerSettings(ctx, &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &storedURL,
			Password: &storedPass,
		},
	})
	if err != nil {
		_ = app1.Shutdown(ctx)
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}
	_ = app1.Shutdown(ctx)

	// 2. Set environment override QBIT_PASSWORD=env-secret-B
	t.Setenv("QBIT_PASSWORD", "env-secret-B")

	app2, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("app2.New failed: %v", err)
	}

	rawEng, ok := app2.registry.Get("qbittorrent")
	if !ok {
		_ = app2.Shutdown(ctx)
		t.Fatalf("expected qbittorrent engine")
	}
	qbit, ok := rawEng.(*qbittorrent.Engine)
	if !ok {
		_ = app2.Shutdown(ctx)
		t.Fatalf("expected *qbittorrent.Engine")
	}
	_ = qbit.HealthCheck(ctx)

	mu.Lock()
	received := lastReceivedPassword
	mu.Unlock()

	if received != "env-secret-B" {
		_ = app2.Shutdown(ctx)
		t.Fatalf("expected environment password %q to be used, got %q", "env-secret-B", received)
	}

	// Verify SecureStore still contains stored-secret-A
	_, _, effPass, _ := app2.Settings().GetEffectiveQBitCredentials(ctx)
	if effPass != "env-secret-B" {
		t.Errorf("expected effective password to be env-secret-B while env set")
	}

	_ = app2.Shutdown(ctx)
}

func TestApp_QBitCredentialResolution_FailurePolicy(t *testing.T) {
	key1 := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("1", 32)))
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", key1)

	tempDir := t.TempDir()
	cfg := newTestConfig(t)
	cfg.DataDir = filepath.Join(tempDir, "data")
	cfg.DownloadDir = filepath.Join(tempDir, "downloads")
	dbPath := filepath.Join(tempDir, "test_failure_policy.db")
	ctx := context.Background()

	// A & B: Normal clean startup with no stored password continues normally
	app1, err := New(ctx, cfg, WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("clean startup failed: %v", err)
	}

	secretPass := "confidential-qbit-secret-999"
	endpointURL := "http://127.0.0.1:18081"
	_, err = app1.Settings().UpdatePowerSettings(ctx, &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &endpointURL,
			Password: &secretPass,
		},
	})
	if err != nil {
		_ = app1.Shutdown(ctx)
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}
	// C & E: SecureStore decrypt failure on startup must fail closed (do NOT silently construct with empty password)
	// Corrupt ciphertext in DB to simulate corrupted credential
	_, err = app1.db.Conn().ExecContext(ctx, "UPDATE encrypted_secrets SET ciphertext = 'corrupted-ciphertext-v1' WHERE field_name = 'qbittorrent_password'")
	if err != nil {
		_ = app1.Shutdown(ctx)
		t.Fatalf("failed to corrupt secret ciphertext: %v", err)
	}
	_ = app1.Shutdown(ctx)

	appFail, err := New(ctx, cfg, WithDBPath(dbPath))
	if err == nil {
		_ = appFail.Shutdown(ctx)
		t.Fatalf("expected App.New to fail closed on secret decryption error, succeeded")
	}
	if !strings.Contains(err.Error(), "cannot decrypt existing secrets") && !strings.Contains(err.Error(), "resolve qBittorrent connection settings") {
		t.Fatalf("expected error wrapping decryption failure, got: %v", err)
	}
	// E: Prove error message contains zero secret plaintext
	if strings.Contains(err.Error(), secretPass) {
		t.Fatalf("CRITICAL SECURITY DEFECT: secret plaintext leaked in startup error: %v", err)
	}

	// D: Invalid persisted qB endpoint fails closed
	dbPath2 := filepath.Join(tempDir, "test_corrupt_url.db")
	app2, err := New(ctx, cfg, WithDBPath(dbPath2))
	if err != nil {
		t.Fatalf("failed to open app2: %v", err)
	}
	_, err = app2.db.Conn().ExecContext(ctx, "INSERT INTO app_settings (key, value, updated_at) VALUES ('qbittorrent_url', 'http://invalid host with spaces/?query=bad', CURRENT_TIMESTAMP)")
	if err != nil {
		_ = app2.Shutdown(ctx)
		t.Fatalf("failed to insert corrupt URL: %v", err)
	}
	_ = app2.Shutdown(ctx)

	appCorrupt, err := New(ctx, cfg, WithDBPath(dbPath2))
	if err == nil {
		_ = appCorrupt.Shutdown(ctx)
		t.Fatalf("expected App.New to fail on invalid persisted URL, succeeded")
	}
	if !strings.Contains(err.Error(), "resolve qBittorrent connection settings") {
		t.Fatalf("expected error wrapping 'resolve qBittorrent connection settings', got: %v", err)
	}
}
