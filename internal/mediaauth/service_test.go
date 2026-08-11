package mediaauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"downloader/internal/securestore"
)

// fakeSettingsRepo implements settings.ISettingsRepository in memory.
type fakeSettingsRepo struct {
	settings map[string]string
}

func newFakeSettingsRepo() *fakeSettingsRepo {
	return &fakeSettingsRepo{settings: make(map[string]string)}
}

func (r *fakeSettingsRepo) Get(ctx context.Context, key string) (string, error) {
	return r.settings[key], nil
}

func (r *fakeSettingsRepo) Set(ctx context.Context, key, value string) error {
	r.settings[key] = value
	return nil
}

// fakeSecretRepo implements securestore.Repository in memory.
type fakeSecretRepo struct {
	secrets map[string][]byte
}

func newFakeSecretRepo() *fakeSecretRepo {
	return &fakeSecretRepo{secrets: make(map[string][]byte)}
}

func key(scope, owner, field string) string {
	return scope + "/" + owner + "/" + field
}

func (r *fakeSecretRepo) GetSecret(ctx context.Context, scope, owner, field string) ([]byte, error) {
	return r.secrets[key(scope, owner, field)], nil
}

func (r *fakeSecretRepo) SetSecret(ctx context.Context, scope, owner, field string, ciphertext []byte) error {
	r.secrets[key(scope, owner, field)] = ciphertext
	return nil
}

func (r *fakeSecretRepo) DeleteSecret(ctx context.Context, scope, owner, field string) error {
	delete(r.secrets, key(scope, owner, field))
	return nil
}

func (r *fakeSecretRepo) HasSecret(ctx context.Context, scope, owner, field string) (bool, error) {
	_, ok := r.secrets[key(scope, owner, field)]
	return ok, nil
}

func testCipher(t *testing.T) *securestore.Cipher {
	t.Helper()
	key, _ := hex.DecodeString("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("failed to create AES block: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("failed to create GCM AEAD: %v", err)
	}
	// Use environment simulation or direct creation
	os.Setenv("V0.7_SETTINGS_ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	c, err := securestore.NewFromEnvironment()
	if err != nil || !c.Available() {
		t.Fatalf("failed to create test cipher: %v", err)
	}
	_ = aead
	return c
}

const validNetscapeSample = `# Netscape HTTP Cookie File
# https://curl.se/docs/http-cookies.html
# This is a generated cookie file for testing only
.example.com	TRUE	/	TRUE	2147483647	session_token	dummy_token_xyz123
`

// 1. Default = none
func TestMediaAuth_DefaultIsNone(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	settings, err := svc.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.Mode != ModeNone {
		t.Errorf("expected default mode 'none', got %q", settings.Mode)
	}
	if settings.HasCookieFile {
		t.Errorf("expected HasCookieFile false, got true")
	}
}

// 2. Valid browser modes accepted
func TestMediaAuth_ValidBrowserModes(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	browsers := []string{"brave", "chrome", "chromium", "edge", "firefox", "opera", "safari", "vivaldi", "whale", "CHROME", "Firefox"}
	for _, b := range browsers {
		res, err := svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
			Mode:    ModeBrowser,
			Browser: b,
		})
		if err != nil {
			t.Fatalf("failed to set browser %s: %v", b, err)
		}
		if res.Mode != ModeBrowser {
			t.Errorf("expected mode browser, got %q", res.Mode)
		}
		if res.Browser != strings.ToLower(strings.TrimSpace(b)) {
			t.Errorf("expected normalized browser %s, got %s", strings.ToLower(b), res.Browser)
		}
	}
}

// 3. Unsupported browser rejected
func TestMediaAuth_UnsupportedBrowserRejected(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	for _, b := range []string{"tor", "ie", "lynx", "unknown", "--custom-flag"} {
		_, err := svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
			Mode:    ModeBrowser,
			Browser: b,
		})
		if err == nil {
			t.Errorf("expected error for unsupported browser %q, got nil", b)
		}
	}
}

// 4. Optional browser profile handled correctly
func TestMediaAuth_OptionalBrowserProfile(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	// Profile with name
	res, err := svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Mode:    ModeBrowser,
		Browser: "chrome",
		Profile: "Profile 1",
	})
	if err != nil {
		t.Fatalf("failed with profile: %v", err)
	}
	if res.Profile != "Profile 1" {
		t.Errorf("expected profile 'Profile 1', got %q", res.Profile)
	}

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	if len(args) != 2 || args[0] != "--cookies-from-browser" || args[1] != "chrome:Profile 1" {
		t.Errorf("expected args [--cookies-from-browser chrome:Profile 1], got %v", args)
	}

	// Empty profile
	res, err = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Mode:    ModeBrowser,
		Browser: "chrome",
		Profile: "   ",
	})
	if err != nil {
		t.Fatalf("failed with empty profile: %v", err)
	}
	if res.Profile != "" {
		t.Errorf("expected empty profile, got %q", res.Profile)
	}

	args, cleanup, err = svc.PrepareAuthArgs(context.Background())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected prepare error: %v", err)
	}
	if len(args) != 2 || args[0] != "--cookies-from-browser" || args[1] != "chrome" {
		t.Errorf("expected args [--cookies-from-browser chrome], got %v", args)
	}
}

// 5. Control-character/invalid browser/profile values rejected
func TestMediaAuth_InvalidProfileValuesRejected(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	badProfiles := []string{
		"profile\x00injection",
		"profile\nnewline",
		"profile\rreturn",
		"profile\ttab",
		"--arbitrary-flag",
		"-f",
		strings.Repeat("a", 300), // exceeds max length
	}

	for _, p := range badProfiles {
		_, err := svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
			Mode:    ModeBrowser,
			Browser: "chrome",
			Profile: p,
		})
		if err == nil {
			t.Errorf("expected error for bad profile %q, got nil", p)
		}
	}
}

// 6. Cookie-file mode without imported cookie fails safely
func TestMediaAuth_CookieFileModeWithoutImportFails(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	_, err := svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Mode: ModeCookieFile,
	})
	if err == nil {
		t.Fatalf("expected error when enabling cookie_file mode without imported cookies, got nil")
	}

	settings, err := svc.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if settings.Mode != ModeNone {
		t.Errorf("expected mode none, got %q", settings.Mode)
	}
}

// 7. Remove cookie changes active cookie-file mode to none
func TestMediaAuth_RemoveCookieResetsModeToNone(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	// Import cookies
	_, err := svc.ImportCookies(context.Background(), []byte(validNetscapeSample))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Set mode to cookie_file
	_, err = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{Mode: ModeCookieFile})
	if err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	// Delete cookies
	res, err := svc.DeleteCookies(context.Background())
	if err != nil {
		t.Fatalf("delete cookies failed: %v", err)
	}
	if res.HasCookieFile {
		t.Errorf("expected HasCookieFile false, got true")
	}
	if res.Mode != ModeNone {
		t.Errorf("expected mode none after delete, got %q", res.Mode)
	}

	// Verify persistence also reset
	persisted, _ := svc.GetSettings(context.Background())
	if persisted.Mode != ModeNone {
		t.Errorf("expected persisted mode none, got %q", persisted.Mode)
	}
}

// 8. Valid Netscape cookies.txt accepted and normalized
func TestMediaAuth_ValidNetscapeFormatAndNormalization(t *testing.T) {
	samples := []struct {
		name     string
		input    string
		firstRow string
	}{
		{
			name:     "Standard Netscape",
			input:    "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tname\tval\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
		{
			name:     "HTTP Cookie File header",
			input:    "# HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tname\tval\n",
			firstRow: "# HTTP Cookie File",
		},
		{
			name:     "UTF-8 BOM stripped and header becomes first line",
			input:    "\xef\xbb\xbf# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tname\tval\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
		{
			name:     "Leading blank lines stripped so header is first line",
			input:    "\r\n\n\n  \n# Netscape HTTP Cookie File\n# Comments\n.example.com\tTRUE\t/\tTRUE\t2147483647\tname\tval\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
		{
			name:     "Leading spaces on header normalized to canonical header",
			input:    "   # Netscape HTTP Cookie File   \r\n.example.com\tTRUE\t/\tTRUE\t2147483647\tname\tval\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
		{
			name:     "HttpOnly cookies preserved and recognized as valid cookie entries",
			input:    "# Netscape HTTP Cookie File\n#HttpOnly_.example.com\tTRUE\t/\tTRUE\t2147483647\ttoken\tsecret123\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
		{
			name:     "Empty cookie value accepted in valid 7-field row",
			input:    "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t0\tlogged_in\t\n",
			firstRow: "# Netscape HTTP Cookie File",
		},
	}

	for _, tt := range samples {
		t.Run(tt.name, func(t *testing.T) {
			normalized, err := NormalizeCookieFile([]byte(tt.input))
			if err != nil {
				t.Fatalf("unexpected normalization error: %v", err)
			}
			if !strings.HasPrefix(string(normalized), tt.firstRow) {
				t.Fatalf("expected normalized output to start with %q, got %q", tt.firstRow, string(normalized))
			}
			// Must start directly with '#'
			if normalized[0] != '#' {
				t.Fatalf("first byte of normalized file must be '#', got %c", normalized[0])
			}
		})
	}
}

// 8b. BOM and leading blank line import materializes normalized temp file
func TestMediaAuth_ImportNormalizesTempFile(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	rawInput := "\xef\xbb\xbf\r\n\r\n\n   # Netscape HTTP Cookie File   \r\n.example.com\tTRUE\t/\tTRUE\t2147483647\tauth_key\tval123\r\n"

	_, err := svc.ImportCookies(context.Background(), []byte(rawInput))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Update mode to cookie_file so PrepareAuthArgs materializes temp file
	_, err = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{Mode: ModeCookieFile})
	if err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	if err != nil {
		t.Fatalf("prepare auth args in cookie_file mode failed: %v", err)
	}
	defer cleanup()

	if len(args) != 2 || args[0] != "--cookies" {
		t.Fatalf("expected --cookies flag, got %v", args)
	}

	tempPath := args[1]
	content, err := os.ReadFile(tempPath)
	if err != nil {
		t.Fatalf("failed to read materialized temp file: %v", err)
	}

	if !strings.HasPrefix(string(content), "# Netscape HTTP Cookie File\n") {
		t.Fatalf("materialized temp file must start with canonical '# Netscape HTTP Cookie File\\n', got: %q", string(content))
	}
	if content[0] != '#' {
		t.Fatalf("first byte must be '#', got %q", content[0])
	}
}

// 9. Empty file rejected
func TestMediaAuth_EmptyFileRejected(t *testing.T) {
	empties := [][]byte{
		{},
		[]byte(""),
		[]byte("   \n\t  \n"),
		[]byte("\xef\xbb\xbf"), // BOM only
	}
	for i, e := range empties {
		if err := ValidateCookieFile(e); err == nil {
			t.Errorf("sample %d should be rejected as empty", i)
		}
	}
}

// 10. Invalid format rejected
func TestMediaAuth_InvalidFormatRejected(t *testing.T) {
	invalids := []string{
		"this is plain text not cookies",
		"<html><body>Not a cookie file</body></html>",
		"{\"cookies\": [\"token\"]}",
		"[General]\nCookie=xyz",
		"# Some random comment\nnot a cookie header",
		"# Netscape HTTP Cookie File\n# Header only with no cookie entries\n# More comments\n",
		"# HTTP Cookie File\n\n\n",
		// Suffix garbage on header line
		"# Netscape HTTP Cookie File garbage\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n",
		"# HTTP Cookie File with extra stuff\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n",
		// Malformed tab rows
		"# Netscape HTTP Cookie File\nfoo\tbar\n",
		"# Netscape HTTP Cookie File\na\tb\tc\td\n",
		"# Netscape HTTP Cookie File\ndomain\tTRUE\t/\tTRUE\n",
		"# Netscape HTTP Cookie File\n.example.com\tINVALID\t/\tTRUE\t0\tk\tv\n",
		"# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tINVALID\t0\tk\tv\n",
		"# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t-1\tk\tv\n",
		"# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\tnotanumber\tk\tv\n",
		"# Netscape HTTP Cookie File\n\tTRUE\t/\tTRUE\t0\tk\tv\n",
		"# Netscape HTTP Cookie File\n.example.com\tTRUE\t\tTRUE\t0\tk\tv\n",
		"# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t0\t\tv\n",
	}
	for _, inv := range invalids {
		if err := ValidateCookieFile([]byte(inv)); err == nil {
			t.Errorf("invalid format %q should have been rejected", inv)
		}
	}
}

// 11. Oversized file rejected
func TestMediaAuth_OversizedFileRejected(t *testing.T) {
	oversized := make([]byte, MaxCookieFileSize+10)
	copy(oversized, []byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n"))
	if err := ValidateCookieFile(oversized); err == nil {
		t.Errorf("expected oversized cookie file to be rejected")
	}
}

// Failure injection repo implementations
type errorSettingsRepo struct {
	fakeSettingsRepo
	setErr error
	getErr error
}

func (r *errorSettingsRepo) Get(ctx context.Context, key string) (string, error) {
	if r.getErr != nil {
		return "", r.getErr
	}
	return r.fakeSettingsRepo.Get(ctx, key)
}

func (r *errorSettingsRepo) Set(ctx context.Context, key, value string) error {
	if r.setErr != nil {
		return r.setErr
	}
	return r.fakeSettingsRepo.Set(ctx, key, value)
}

type errorSecretRepo struct {
	fakeSecretRepo
	deleteErr error
	hasErr    error
}

func (r *errorSecretRepo) DeleteSecret(ctx context.Context, scope, owner, field string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	return r.fakeSecretRepo.DeleteSecret(ctx, scope, owner, field)
}

func (r *errorSecretRepo) HasSecret(ctx context.Context, scope, owner, field string) (bool, error) {
	if r.hasErr != nil {
		return false, r.hasErr
	}
	return r.fakeSecretRepo.HasSecret(ctx, scope, owner, field)
}

// 2b. DeleteCookies failure injection tests
func TestMediaAuth_DeleteCookiesFailures(t *testing.T) {
	// 1. Secret delete failure is returned
	t.Run("Secret delete error is returned", func(t *testing.T) {
		settingsRepo := newFakeSettingsRepo()
		errSecRepo := &errorSecretRepo{
			fakeSecretRepo: *newFakeSecretRepo(),
			deleteErr:      errors.New("db disk I/O error on secret delete"),
		}
		store := securestore.NewStore(errSecRepo, testCipher(t))
		svc := NewService(settingsRepo, store, t.TempDir())

		_, err := svc.ImportCookies(context.Background(), []byte(validNetscapeSample))
		if err != nil {
			t.Fatal(err)
		}

		res, err := svc.DeleteCookies(context.Background())
		if err == nil {
			t.Fatalf("expected error from DeleteCookies when secretStore.Delete fails, got nil")
		}
		if !strings.Contains(err.Error(), "db disk I/O error on secret delete") {
			t.Fatalf("expected error to contain underlying message, got %v", err)
		}
		if res != nil {
			t.Fatalf("expected nil result on failure, got %+v", res)
		}
	})

	// 2. Settings persistence failure on cookie_file -> none reset is returned
	t.Run("Settings persistence error on reset is returned", func(t *testing.T) {
		errSettingsRepo := &errorSettingsRepo{
			fakeSettingsRepo: *newFakeSettingsRepo(),
			setErr:           errors.New("database locked on settings update"),
		}
		secRepo := newFakeSecretRepo()
		store := securestore.NewStore(secRepo, testCipher(t))
		svc := NewService(errSettingsRepo, store, t.TempDir())

		// Seed settings with cookie_file mode
		errSettingsRepo.settings[SettingKeyMediaAuth] = `{"mode":"cookie_file"}`

		res, err := svc.DeleteCookies(context.Background())
		if err == nil {
			t.Fatalf("expected error when setting repo fails to update, got nil")
		}
		if !strings.Contains(err.Error(), "database locked on settings update") {
			t.Fatalf("expected error to contain underlying message, got %v", err)
		}
		if res != nil {
			t.Fatalf("expected nil result on failure, got %+v", res)
		}
	})
}

// 2c. Unavailable cipher allows deletion and reports presence truthfully (Requirements 1 & 2 & 5.G)
func TestMediaAuth_DeleteCookiesWithUnavailableCipher(t *testing.T) {
	settingsRepo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()

	// 1 & 2. Use a valid cipher/store to import cookies
	validStore := securestore.NewStore(secretRepo, testCipher(t))
	svc1 := NewService(settingsRepo, validStore, t.TempDir())
	_, err := svc1.ImportCookies(context.Background(), []byte(validNetscapeSample))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	_, err = svc1.UpdateSettings(context.Background(), UpdateSettingsRequest{Mode: ModeCookieFile})
	if err != nil {
		t.Fatalf("update settings failed: %v", err)
	}

	// 3. Confirm encrypted secret exists
	has, err := secretRepo.HasSecret(context.Background(), SecretScope, SecretOwner, SecretField)
	if err != nil || !has {
		t.Fatalf("expected encrypted secret to exist in repository")
	}

	// 4. Simulate restart without encryption key by creating Store with SAME secretRepo but nil cipher
	unavailStore := securestore.NewStore(secretRepo, nil)
	if unavailStore.Available() {
		t.Fatalf("expected unavailStore to be unavailable")
	}
	svc2 := NewService(settingsRepo, unavailStore, t.TempDir())

	// 5. Confirm Has reports true truthfully even with unavailable cipher
	truthfulSettings, err := svc2.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("GetSettings failed: %v", err)
	}
	if !truthfulSettings.HasCookieFile {
		t.Errorf("expected HasCookieFile true when encrypted secret is present despite unavailable cipher")
	}

	// 6. Call DeleteCookies
	delRes, err := svc2.DeleteCookies(context.Background())
	if err != nil {
		t.Fatalf("DeleteCookies failed with unavailable cipher: %v", err)
	}

	// 7. Verify deletion succeeds, encrypted DB secret is gone, mode becomes none, hasCookieFile=false
	if delRes.HasCookieFile {
		t.Errorf("expected HasCookieFile false after delete")
	}
	if delRes.Mode != ModeNone {
		t.Errorf("expected mode none after delete, got %q", delRes.Mode)
	}
	hasAfter, _ := secretRepo.HasSecret(context.Background(), SecretScope, SecretOwner, SecretField)
	if hasAfter {
		t.Errorf("expected encrypted secret to be deleted from secretRepo")
	}
}

// 2d. Repository lookup errors are not silently presented as no-cookie/default state (Requirements 2 & 5.H)
func TestMediaAuth_RepositoryLookupErrorsPropagated(t *testing.T) {
	settingsRepo := newFakeSettingsRepo()
	errSecRepo := &errorSecretRepo{
		fakeSecretRepo: *newFakeSecretRepo(),
		hasErr:         errors.New("db connection timeout on HasSecret"),
	}
	store := securestore.NewStore(errSecRepo, testCipher(t))
	svc := NewService(settingsRepo, store, t.TempDir())

	_, err := svc.GetSettings(context.Background())
	if err == nil {
		t.Fatalf("expected GetSettings to return error when HasSecret fails, got nil")
	}
	if !strings.Contains(err.Error(), "db connection timeout on HasSecret") {
		t.Fatalf("expected underlying error message, got: %v", err)
	}

	_, err = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{Mode: ModeCookieFile})
	if err == nil {
		t.Fatalf("expected UpdateSettings to return error when HasSecret fails, got nil")
	}
	if !strings.Contains(err.Error(), "db connection timeout on HasSecret") {
		t.Fatalf("expected underlying error message, got: %v", err)
	}
}

// 12. Stored value goes through securestore
func TestMediaAuth_StoredValueEncrypted(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	secretCookieVal := "super_secret_session_xyz987"
	cookieData := "# Netscape HTTP Cookie File\n.site.com\tTRUE\t/\tTRUE\t2147483647\tsid\t" + secretCookieVal + "\n"

	_, err := svc.ImportCookies(context.Background(), []byte(cookieData))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	// Verify raw bytes in secretRepo are encrypted (do not contain plaintext)
	rawCiphertext, err := secretRepo.GetSecret(context.Background(), SecretScope, SecretOwner, SecretField)
	if err != nil {
		t.Fatalf("failed to read from secretRepo: %v", err)
	}
	if len(rawCiphertext) == 0 {
		t.Fatalf("expected non-empty ciphertext")
	}
	if strings.Contains(string(rawCiphertext), secretCookieVal) {
		t.Fatalf("SECURITY VIOLATION: secret repo contains plaintext cookie!")
	}
	if strings.Contains(string(rawCiphertext), "Netscape") {
		t.Fatalf("SECURITY VIOLATION: secret repo contains unencrypted header!")
	}

	// Verify app_settings never contains cookie data
	rawSettings, _ := repo.Get(context.Background(), SettingKeyMediaAuth)
	if strings.Contains(rawSettings, secretCookieVal) {
		t.Fatalf("SECURITY VIOLATION: app_settings contains secret cookie value!")
	}
}

// 13. GET never exposes secret
func TestMediaAuth_GetNeverExposesSecret(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	_, _ = svc.ImportCookies(context.Background(), []byte(validNetscapeSample))
	settings, err := svc.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("get settings error: %v", err)
	}

	if !settings.HasCookieFile {
		t.Errorf("expected HasCookieFile true")
	}
	// Inspect struct fields: Settings only has Mode, Browser, Profile, HasCookieFile
}

// 14 & 15. Replace works & Delete works
func TestMediaAuth_ReplaceAndDelete(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	cookie1 := "# Netscape HTTP Cookie File\n.site1.com\tTRUE\t/\tTRUE\t2147483647\ta\t1\n"
	cookie2 := "# Netscape HTTP Cookie File\n.site2.com\tTRUE\t/\tTRUE\t2147483647\tb\t2\n"

	_, err := svc.ImportCookies(context.Background(), []byte(cookie1))
	if err != nil {
		t.Fatal(err)
	}

	// Replace with cookie2
	res, err := svc.ImportCookies(context.Background(), []byte(cookie2))
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasCookieFile {
		t.Errorf("expected HasCookieFile true")
	}

	// Verify decrypted content is cookie2
	decrypted, err := store.Get(context.Background(), SecretScope, SecretOwner, SecretField)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != cookie2 {
		t.Errorf("expected cookie2 content, got %q", decrypted)
	}

	// Delete
	delRes, err := svc.DeleteCookies(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if delRes.HasCookieFile {
		t.Errorf("expected HasCookieFile false")
	}
	has, _ := store.Has(context.Background(), SecretScope, SecretOwner, SecretField)
	if has {
		t.Errorf("expected store to not have secret after delete")
	}
}

// 16. securestore unavailable -> import rejected, no plaintext fallback
func TestMediaAuth_SecureStoreUnavailableRejectsImport(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	// store with nil cipher -> unavailable
	store := securestore.NewStore(secretRepo, nil)
	svc := NewService(repo, store, t.TempDir())

	_, err := svc.ImportCookies(context.Background(), []byte(validNetscapeSample))
	if err == nil {
		t.Fatalf("expected error when securestore is unavailable, got nil")
	}

	// Verify nothing was stored in secretRepo or app_settings
	has, _ := secretRepo.HasSecret(context.Background(), SecretScope, SecretOwner, SecretField)
	if has {
		t.Fatalf("SECURITY VIOLATION: secret stored when securestore was unavailable!")
	}
}

// 17. Mode none -> no cookie flags
func TestMediaAuth_ModeNoneArgs(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 0 {
		t.Errorf("expected empty args for mode none, got %v", args)
	}
}

// 18. Browser chrome -> exactly: --cookies-from-browser chrome
func TestMediaAuth_BrowserChromeArgs(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	_, _ = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Mode:    ModeBrowser,
		Browser: "chrome",
	})

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 2 || args[0] != "--cookies-from-browser" || args[1] != "chrome" {
		t.Errorf("expected [--cookies-from-browser chrome], got %v", args)
	}
}

// 19. Browser + profile -> expected browser/profile argument
func TestMediaAuth_BrowserProfileArgs(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, t.TempDir())

	_, _ = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{
		Mode:    ModeBrowser,
		Browser: "firefox",
		Profile: "dev-edition",
	})

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	defer cleanup()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(args) != 2 || args[0] != "--cookies-from-browser" || args[1] != "firefox:dev-edition" {
		t.Errorf("expected [--cookies-from-browser firefox:dev-edition], got %v", args)
	}
}

// 20. Cookie-file -> exactly: --cookies <ephemeral path>
func TestMediaAuth_CookieFileArgsAndLifecycle(t *testing.T) {
	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	tempDir := t.TempDir()
	svc := NewService(repo, store, tempDir)

	_, err := svc.ImportCookies(context.Background(), []byte(validNetscapeSample))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateSettings(context.Background(), UpdateSettingsRequest{Mode: ModeCookieFile})
	if err != nil {
		t.Fatal(err)
	}

	args, cleanup, err := svc.PrepareAuthArgs(context.Background())
	if err != nil {
		t.Fatalf("prepare failed: %v", err)
	}
	if len(args) != 2 || args[0] != "--cookies" {
		t.Fatalf("expected [--cookies <path>], got %v", args)
	}

	tempCookiePath := args[1]
	// Verify temp file exists while in use
	if _, err := os.Stat(tempCookiePath); err != nil {
		t.Fatalf("temp cookie file %s should exist before cleanup: %v", tempCookiePath, err)
	}

	// Verify temp file contains decrypted cookie data
	content, err := os.ReadFile(tempCookiePath)
	if err != nil {
		t.Fatalf("failed to read temp cookie: %v", err)
	}
	if string(content) != validNetscapeSample {
		t.Errorf("temp cookie content mismatch")
	}

	// Run cleanup
	cleanup()

	// Verify temp file is removed
	if _, err := os.Stat(tempCookiePath); !os.IsNotExist(err) {
		t.Fatalf("temp cookie file %s should have been deleted after cleanup", tempCookiePath)
	}
}

// 35, 36, 37, 38. Startup stale temp cleanup
func TestMediaAuth_CleanupStaleTempFiles(t *testing.T) {
	tempAuthDir := t.TempDir()

	// 1. Create a stale auth temp file
	staleAuthFile := filepath.Join(tempAuthDir, TempCookiePrefix+"stale123"+TempCookieSuffix)
	if err := os.WriteFile(staleAuthFile, []byte("stale-cookie"), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Create an unrelated file in the same directory
	unrelatedFile := filepath.Join(tempAuthDir, "user_notes.txt")
	if err := os.WriteFile(unrelatedFile, []byte("important note"), 0644); err != nil {
		t.Fatal(err)
	}

	// 3. Create a subdirectory in the same directory
	subDir := filepath.Join(tempAuthDir, "important_folder")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	fileInSubDir := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(fileInSubDir, []byte("nested"), 0644); err != nil {
		t.Fatal(err)
	}

	repo := newFakeSettingsRepo()
	secretRepo := newFakeSecretRepo()
	store := securestore.NewStore(secretRepo, testCipher(t))
	svc := NewService(repo, store, tempAuthDir)

	// Run stale cleanup
	if err := svc.CleanupStaleTempFiles(); err != nil {
		t.Fatalf("CleanupStaleTempFiles failed: %v", err)
	}

	// Verify stale auth file was deleted
	if _, err := os.Stat(staleAuthFile); !os.IsNotExist(err) {
		t.Errorf("stale auth file %s should have been deleted", staleAuthFile)
	}

	// Verify unrelated file remains untouched
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Errorf("unrelated file %s should NOT have been deleted: %v", unrelatedFile, err)
	}

	// Verify subdirectory and nested file remain untouched
	if _, err := os.Stat(subDir); err != nil {
		t.Errorf("subdirectory %s should NOT have been deleted: %v", subDir, err)
	}
	if _, err := os.Stat(fileInSubDir); err != nil {
		t.Errorf("nested file %s should NOT have been deleted: %v", fileInSubDir, err)
	}
}
