package settings_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/database"
	"downloader/internal/networkpolicy"
	"downloader/internal/securestore"
	"downloader/internal/settings"
)

func setupQBitTestServices(t *testing.T) (*settings.SettingsService, *database.DB, *securestore.Store) {
	t.Helper()
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	tempDir := t.TempDir()
	db, err := database.New(filepath.Join(tempDir, "settings.db"))
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	cipher, err := securestore.NewFromEnvironment()
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}
	secRepo := database.NewSQLiteSecretRepository(db)
	store := securestore.NewStore(secRepo, cipher)
	setRepo := database.NewSQLiteSettingsRepository(db)
	svc := settings.NewSettingsService(setRepo, tempDir, tempDir, store)
	return svc, db, store
}

func TestValidateQBitEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		rawURL    string
		expect    string
		expectErr bool
	}{
		{"valid loopback http", "http://127.0.0.1:8081", "http://127.0.0.1:8081", false},
		{"valid trailing slash normalization", "http://127.0.0.1:8081/", "http://127.0.0.1:8081", false},
		{"valid multiple trailing slashes", "http://localhost:8080///", "http://localhost:8080", false},
		{"valid remote https", "https://qb.example.com:443/api", "https://qb.example.com:443/api", false},
		{"valid remote https with path trailing slash", "https://qb.example.com:443/subpath/", "https://qb.example.com:443/subpath", false},
		{"valid reverse proxy subpath", "http://127.0.0.1:8081/qbittorrent", "http://127.0.0.1:8081/qbittorrent", false},
		{"valid reverse proxy subpath with trailing slash", "http://127.0.0.1:8081/qbittorrent/", "http://127.0.0.1:8081/qbittorrent", false},
		{"empty url", "", "", true},
		{"whitespace url", "   ", "", true},
		{"invalid scheme ftp", "ftp://127.0.0.1:8081", "", true},
		{"missing scheme", "127.0.0.1:8081", "", true},
		{"missing host", "http://", "", true},
		{"user credentials embedded", "http://admin:pass@127.0.0.1:8081", "", true},
		{"username only embedded", "http://admin@127.0.0.1:8081", "", true},
		{"query param token rejected", "http://host:8081/?token=secret", "", true},
		{"empty query param rejected", "http://host:8081/?", "", true},
		{"path with query rejected", "http://host:8081/path?x=y", "", true},
		{"url fragment rejected", "http://host:8081/#fragment", "", true},
		{"path with fragment rejected", "http://host:8081/subpath#frag", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := settings.ValidateQBitEndpoint(tt.rawURL)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.rawURL)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error for %q: %v", tt.rawURL, err)
				}
				if got != tt.expect {
					t.Fatalf("expected %q, got %q", tt.expect, got)
				}
			}
		})
	}
}

func TestQBit_SecureStorePasswordPersistenceAndSafeDTO(t *testing.T) {
	svc, db, store := setupQBitTestServices(t)
	defer db.Close()
	ctx := context.Background()

	// 1. Initial defaults
	initSt, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if initSt.QBittorrent.URL != settings.DefaultQBitURL {
		t.Errorf("expected default URL %q, got %q", settings.DefaultQBitURL, initSt.QBittorrent.URL)
	}
	if initSt.QBittorrent.Username != settings.DefaultQBitUsername {
		t.Errorf("expected default username %q, got %q", settings.DefaultQBitUsername, initSt.QBittorrent.Username)
	}
	if initSt.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=false initially")
	}

	// 2. Persist URL, Username, and Password
	urlVal := "http://192.168.1.50:8085/"
	userVal := "qbitadmin"
	passVal := "my-super-secret-password-123"

	req := &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &urlVal,
			Username: &userVal,
			Password: &passVal,
		},
	}

	if err := svc.ValidateUpdate(ctx, req); err != nil {
		t.Fatalf("ValidateUpdate failed: %v", err)
	}

	resSt, err := svc.UpdatePowerSettings(ctx, req)
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}

	// Safe DTO checks:
	// Normalized trailing slash:
	expectedURL := "http://192.168.1.50:8085"
	if resSt.QBittorrent.URL != expectedURL {
		t.Errorf("expected URL %q, got %q", expectedURL, resSt.QBittorrent.URL)
	}
	if resSt.QBittorrent.Username != "qbitadmin" {
		t.Errorf("expected username %q, got %q", "qbitadmin", resSt.QBittorrent.Username)
	}
	if !resSt.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=true after setting password")
	}

	// Prove DTO JSON never contains plaintext password
	encoded, err := json.Marshal(resSt)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if strings.Contains(string(encoded), passVal) {
		t.Fatalf("CRITICAL SECURITY DEFECT: plaintext password leaked in AppSettings DTO JSON: %s", string(encoded))
	}

	// Prove plaintext password is NOT in app_settings table
	var rawSettingsVal string
	_ = db.Conn().QueryRowContext(ctx, "SELECT value FROM app_settings WHERE key = 'qbittorrent_password'").Scan(&rawSettingsVal)
	if rawSettingsVal != "" {
		t.Fatalf("password must NOT be persisted in app_settings table, found: %q", rawSettingsVal)
	}

	// Prove password exists in SecureStore secret_records as ciphertext
	hasSec, err := store.Has(ctx, settings.SecretScopeSettings, settings.SecretOwnerGlobal, settings.SecretFieldQBitPass)
	if err != nil || !hasSec {
		t.Fatalf("expected password in SecureStore, err=%v, has=%v", err, hasSec)
	}
	var ciphertext string
	rowErr := db.Conn().QueryRowContext(ctx, "SELECT ciphertext FROM encrypted_secrets WHERE scope = ? AND owner_id = ? AND field_name = ?",
		settings.SecretScopeSettings, settings.SecretOwnerGlobal, settings.SecretFieldQBitPass).Scan(&ciphertext)
	if rowErr != nil {
		t.Fatalf("failed to query encrypted_secrets: %v", rowErr)
	}
	if strings.Contains(ciphertext, passVal) {
		t.Fatalf("CRITICAL: ciphertext in secret_records contains plaintext password: %q", ciphertext)
	}

	// Retrieve effective credentials:
	effURL, effUser, effPass, err := svc.GetEffectiveQBitCredentials(ctx)
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials failed: %v", err)
	}
	if effURL != expectedURL || effUser != userVal || effPass != passVal {
		t.Fatalf("mismatched effective credentials: got url=%q user=%q pass=%q", effURL, effUser, effPass)
	}

	// 3. Update without password -> password preserved
	newURL := "http://192.168.1.60:8085"
	reqPreserve := &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL: &newURL,
		},
	}
	resPreserve, err := svc.UpdatePowerSettings(ctx, reqPreserve)
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}
	if !resPreserve.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=true when omitted")
	}
	_, _, effPassAfterPreserve, _ := svc.GetEffectiveQBitCredentials(ctx)
	if effPassAfterPreserve != passVal {
		t.Fatalf("password was mutated unexpectedly when omitted")
	}

	// 4. Explicit password replacement
	newPass := "replaced-password-456"
	reqReplace := &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			Password: &newPass,
		},
	}
	_, err = svc.UpdatePowerSettings(ctx, reqReplace)
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}
	_, _, effPassReplaced, _ := svc.GetEffectiveQBitCredentials(ctx)
	if effPassReplaced != newPass {
		t.Fatalf("expected replaced password %q, got %q", newPass, effPassReplaced)
	}

	// 5. Clear password
	reqClear := &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			ClearPassword: true,
		},
	}
	resClear, err := svc.UpdatePowerSettings(ctx, reqClear)
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}
	if resClear.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=false after clear")
	}
	hasSecAfterClear, _ := store.Has(ctx, settings.SecretScopeSettings, settings.SecretOwnerGlobal, settings.SecretFieldQBitPass)
	if hasSecAfterClear {
		t.Fatalf("expected secret deleted from SecureStore")
	}
	_, _, effPassCleared, _ := svc.GetEffectiveQBitCredentials(ctx)
	if effPassCleared != "" {
		t.Fatalf("expected empty password after clear, got %q", effPassCleared)
	}
}

func TestQBit_EnvironmentPrecedenceAndNeverPersisted(t *testing.T) {
	svc, db, store := setupQBitTestServices(t)
	defer db.Close()
	ctx := context.Background()

	// Persist stored-secret-A
	storedURL := "http://persisted-host:8081"
	storedUser := "persisted-user"
	storedPass := "stored-secret-A"
	_, err := svc.UpdatePowerSettings(ctx, &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &storedURL,
			Username: &storedUser,
			Password: &storedPass,
		},
	})
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}

	// Verify before env override
	st, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if st.QBittorrent.URL != storedURL || st.QBittorrent.Username != storedUser || !st.QBittorrent.HasPassword {
		t.Fatalf("persisted settings mismatch: %+v", st.QBittorrent)
	}
	if st.QBittorrent.URLEnvironmentOverride || st.QBittorrent.UsernameEnvironmentOverride || st.QBittorrent.PasswordEnvironmentOverride {
		t.Fatalf("unexpected override flags: %+v", st.QBittorrent)
	}

	// Set environment overrides
	t.Setenv("QBIT_URL", "http://env-host:9999")
	t.Setenv("QBIT_USERNAME", "env-user")
	t.Setenv("QBIT_PASSWORD", "env-secret-B")

	// GetSettings should reflect overrides
	stEnv, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings with env failed: %v", err)
	}
	if stEnv.QBittorrent.URL != "http://env-host:9999" {
		t.Errorf("expected env URL, got %q", stEnv.QBittorrent.URL)
	}
	if stEnv.QBittorrent.Username != "env-user" {
		t.Errorf("expected env username, got %q", stEnv.QBittorrent.Username)
	}
	if !stEnv.QBittorrent.HasPassword {
		t.Errorf("expected hasPassword=true with env password")
	}
	if !stEnv.QBittorrent.URLEnvironmentOverride || !stEnv.QBittorrent.UsernameEnvironmentOverride || !stEnv.QBittorrent.PasswordEnvironmentOverride {
		t.Fatalf("expected all override flags to be true: %+v", stEnv.QBittorrent)
	}

	// Effective credentials must use environment
	effURL, effUser, effPass, err := svc.GetEffectiveQBitCredentials(ctx)
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials failed: %v", err)
	}
	if effURL != "http://env-host:9999" || effUser != "env-user" || effPass != "env-secret-B" {
		t.Fatalf("expected environment values: got url=%q user=%q pass=%q", effURL, effUser, effPass)
	}

	// CRITICAL REQUIREMENT: Environment password must NEVER be persisted into SecureStore!
	persistedPassInStore, err := store.Get(ctx, settings.SecretScopeSettings, settings.SecretOwnerGlobal, settings.SecretFieldQBitPass)
	if err != nil {
		t.Fatalf("failed to read from store: %v", err)
	}
	if persistedPassInStore != storedPass {
		t.Fatalf("CRITICAL DEFECT: environment password leaked into SecureStore! Expected %q, found %q", storedPass, persistedPassInStore)
	}
}

func TestQBit_SharedModeGlobalNetworkSettingsUnavailable(t *testing.T) {
	svc, db, _ := setupQBitTestServices(t)
	defer db.Close()
	ctx := context.Background()

	// 1. GetSettings always reports false in ExternalUserManaged mode
	st, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if st.Torrent.ManageQBitGlobalNetworkSettings {
		t.Fatalf("expected ManageQBitGlobalNetworkSettings to be false")
	}

	// 2. Even with environment variable set to true, effective behavior must be false
	t.Setenv("MANAGE_QBIT_GLOBAL_NETWORK_SETTINGS", "true")
	stEnv, err := svc.GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if stEnv.Torrent.ManageQBitGlobalNetworkSettings {
		t.Fatalf("expected ManageQBitGlobalNetworkSettings to be false even with env=true")
	}

	// 3. ValidateUpdate rejects attempts to enable it
	enableTrue := true
	req := &settings.UpdateSettingsRequest{
		Torrent: &struct {
			DownloadLimitBytesPerSecond            *int64                       `json:"downloadLimitBytesPerSecond,omitempty"`
			UploadLimitBytesPerSecond              *int64                       `json:"uploadLimitBytesPerSecond,omitempty"`
			SeedingPolicy                          *networkpolicy.SeedingPolicy `json:"seedingPolicy,omitempty"`
			ApplyTrackerSubscriptionsToNewTorrents *bool                        `json:"applyTrackerSubscriptionsToNewTorrents,omitempty"`
			ManageQBitGlobalNetworkSettings        *bool                        `json:"manageQBitGlobalNetworkSettings,omitempty"`
		}{
			ManageQBitGlobalNetworkSettings: &enableTrue,
		},
	}
	valErr := svc.ValidateUpdate(ctx, req)
	if valErr == nil {
		t.Fatalf("expected ValidateUpdate to reject enabling ManageQBitGlobalNetworkSettings, got nil")
	}
	if !strings.Contains(valErr.Error(), "unavailable while using a shared, user-managed qBittorrent instance") {
		t.Fatalf("unexpected validation error message: %v", valErr)
	}
}

func TestQBit_CanonicalDefaultAndPrecedence(t *testing.T) {
	svc, db, store := setupQBitTestServices(t)
	defer db.Close()
	ctx := context.Background()

	// 1. No settings persisted, no QBIT_* env -> must equal canonical fallback
	effURL, effUser, effPass, err := svc.GetEffectiveQBitCredentials(ctx)
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials failed: %v", err)
	}
	if effURL != "http://127.0.0.1:8081" {
		t.Errorf("expected canonical default URL http://127.0.0.1:8081, got %q", effURL)
	}
	if effUser != "admin" {
		t.Errorf("expected canonical default username admin, got %q", effUser)
	}
	if effPass != "" {
		t.Errorf("expected empty default password, got %q", effPass)
	}

	// 2. Persisted settings take precedence over default
	persistedURL := "http://127.0.0.1:18081"
	persistedUser := "user1"
	persistedPass := "secret1"
	_, err = svc.UpdatePowerSettings(ctx, &settings.UpdateSettingsRequest{
		QBittorrent: &struct {
			URL           *string `json:"url,omitempty"`
			Username      *string `json:"username,omitempty"`
			Password      *string `json:"password,omitempty"`
			ClearPassword bool    `json:"clearPassword,omitempty"`
		}{
			URL:      &persistedURL,
			Username: &persistedUser,
			Password: &persistedPass,
		},
	})
	if err != nil {
		t.Fatalf("UpdatePowerSettings failed: %v", err)
	}

	effURL, effUser, effPass, err = svc.GetEffectiveQBitCredentials(ctx)
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials failed: %v", err)
	}
	if effURL != "http://127.0.0.1:18081" || effUser != "user1" || effPass != "secret1" {
		t.Fatalf("expected persisted settings (18081/user1/secret1), got: url=%q user=%q pass=%q", effURL, effUser, effPass)
	}

	// 3. Environment overrides take precedence over persisted settings
	t.Setenv("QBIT_URL", "http://127.0.0.1:9999")
	t.Setenv("QBIT_USERNAME", "test")
	t.Setenv("QBIT_PASSWORD", "secret")

	effURLEnv, effUserEnv, effPassEnv, err := svc.GetEffectiveQBitCredentials(ctx)
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials with env failed: %v", err)
	}
	if effURLEnv != "http://127.0.0.1:9999" || effUserEnv != "test" || effPassEnv != "secret" {
		t.Fatalf("expected env overrides (9999/test/secret), got: url=%q user=%q pass=%q", effURLEnv, effUserEnv, effPassEnv)
	}

	// 4. Stored settings in database/SecureStore remain unchanged
	storedPassInStore, err := store.Get(ctx, settings.SecretScopeSettings, settings.SecretOwnerGlobal, settings.SecretFieldQBitPass)
	if err != nil {
		t.Fatalf("failed to read from store: %v", err)
	}
	if storedPassInStore != "secret1" {
		t.Fatalf("expected stored password in SecureStore to remain secret1, got %q", storedPassInStore)
	}
}

func TestQBit_GetEffectiveQBitCredentials_ErrorsAndFallbacks(t *testing.T) {
	svc, db, _ := setupQBitTestServices(t)
	defer db.Close()
	ctx := context.Background()

	// 1. Invalid QBIT_URL env override returns error
	t.Setenv("QBIT_URL", "http://bad host with spaces/?bad=1")
	_, _, _, err := svc.GetEffectiveQBitCredentials(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid QBIT_URL environment override") {
		t.Fatalf("expected error for invalid QBIT_URL env, got %v", err)
	}
	t.Setenv("QBIT_URL", "")

	// 2. Invalid persisted URL returns error
	_, _ = db.Conn().ExecContext(ctx, "INSERT INTO app_settings (key, value, updated_at) VALUES ('qbittorrent_url', 'ftp://bad-scheme:8081', CURRENT_TIMESTAMP)")
	_, _, _, err = svc.GetEffectiveQBitCredentials(ctx)
	if err == nil || !strings.Contains(err.Error(), "invalid persisted qbittorrent_url") {
		t.Fatalf("expected error for invalid persisted qbittorrent_url, got %v", err)
	}

	// 3. Programmatic fallback when clean
	svcClean, dbClean, _ := setupQBitTestServices(t)
	defer dbClean.Close()
	fbURL, fbUser, fbPass, err := svcClean.GetEffectiveQBitCredentials(ctx, settings.QBitFallback{
		URL:      "http://127.0.0.1:19999",
		Username: "prog-user",
		Password: "prog-password",
	})
	if err != nil {
		t.Fatalf("GetEffectiveQBitCredentials with programmatic fallback failed: %v", err)
	}
	if fbURL != "http://127.0.0.1:19999" || fbUser != "prog-user" || fbPass != "prog-password" {
		t.Fatalf("expected programmatic fallback (19999/prog-user/prog-password), got url=%q user=%q pass=%q", fbURL, fbUser, fbPass)
	}
}

type faultySecretRepo struct {
	err error
}

func (f *faultySecretRepo) GetSecret(ctx context.Context, scope, owner, field string) ([]byte, error) {
	return nil, f.err
}
func (f *faultySecretRepo) SetSecret(ctx context.Context, scope, owner, field string, ciphertext []byte) error {
	return f.err
}
func (f *faultySecretRepo) DeleteSecret(ctx context.Context, scope, owner, field string) error {
	return f.err
}
func (f *faultySecretRepo) HasSecret(ctx context.Context, scope, owner, field string) (bool, error) {
	return false, f.err
}
func (f *faultySecretRepo) CountSecrets(ctx context.Context) (int, error) {
	return 0, f.err
}
func (f *faultySecretRepo) GetAllSecrets(ctx context.Context) ([]securestore.EncryptedRecord, error) {
	return nil, f.err
}

func TestQBit_SecureStoreHasError_Propagated(t *testing.T) {
	t.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	tempDir := t.TempDir()
	db, err := database.New(filepath.Join(tempDir, "settings.db"))
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	defer db.Close()

	cipher, err := securestore.NewFromEnvironment()
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}

	simulatedErr := errors.New("simulated securestore repository failure")
	store := securestore.NewStore(&faultySecretRepo{err: simulatedErr}, cipher)
	setRepo := database.NewSQLiteSettingsRepository(db)
	svc := settings.NewSettingsService(setRepo, tempDir, tempDir, store)
	ctx := context.Background()

	// 1. GetSettings must propagate HasSecret error and not silently treat as unconfigured
	_, err = svc.GetSettings(ctx)
	if err == nil {
		t.Fatalf("expected GetSettings to fail on HasSecret error, got nil")
	}
	if !strings.Contains(err.Error(), "check protected qbittorrent_password") {
		t.Fatalf("expected error wrapping check protected qbittorrent_password, got %v", err)
	}

	// 2. GetEffectiveQBitCredentials must propagate HasSecret error and not return empty password
	_, _, _, err = svc.GetEffectiveQBitCredentials(ctx)
	if err == nil {
		t.Fatalf("expected GetEffectiveQBitCredentials to fail on HasSecret error, got nil")
	}
	if !strings.Contains(err.Error(), "check protected qbittorrent_password") {
		t.Fatalf("expected error wrapping check protected qbittorrent_password, got %v", err)
	}
}
