package mediaauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"downloader/internal/securestore"
	"downloader/internal/settings"
)

const (
	SettingKeyMediaAuth = "media_auth_settings"

	SecretScope = "settings"
	SecretOwner = "global"
	SecretField = "media_cookies"

	TempCookiePrefix = "godownloader_cookie_"
	TempCookieSuffix = ".txt"
)

var (
	ErrSecretStorageUnavailable = errors.New("secret storage unavailable")
	ErrNoCookieFile             = errors.New("cannot enable cookie file mode without imported cookies")
	ErrInvalidMode              = errors.New("invalid media authentication mode: must be none, browser, or cookie_file")
)

// Service manages media authentication settings and ephemeral yt-dlp cookie credentials.
type Service struct {
	repo        settings.ISettingsRepository
	secretStore *securestore.Store
	tempAuthDir string
	mu          sync.RWMutex
}

// NewService creates a new media authentication service.
func NewService(repo settings.ISettingsRepository, secretStore *securestore.Store, tempAuthDir string) *Service {
	if tempAuthDir == "" {
		tempAuthDir = filepath.Join(".", "data", "tmp", "auth")
	}
	absAuthDir, err := filepath.Abs(tempAuthDir)
	if err == nil {
		tempAuthDir = absAuthDir
	}
	return &Service{
		repo:        repo,
		secretStore: secretStore,
		tempAuthDir: tempAuthDir,
	}
}

// TempAuthDir returns the directory used for ephemeral cookie files.
func (s *Service) TempAuthDir() string {
	return s.tempAuthDir
}

// GetSettings retrieves the current media authentication settings.
func (s *Service) GetSettings(ctx context.Context) (*Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	hasCookieFile := false
	if s.secretStore != nil && s.secretStore.Available() {
		has, err := s.secretStore.Has(ctx, SecretScope, SecretOwner, SecretField)
		if err == nil && has {
			hasCookieFile = true
		}
	}

	res := &Settings{
		Mode:          ModeNone,
		HasCookieFile: hasCookieFile,
	}

	if s.repo == nil {
		return res, nil
	}

	raw, err := s.repo.Get(ctx, SettingKeyMediaAuth)
	if err != nil || strings.TrimSpace(raw) == "" {
		return res, nil
	}

	var stored StoredSettings
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		log.Printf("mediaauth: failed to unmarshal stored settings: %v", err)
		return res, nil
	}

	switch stored.Mode {
	case ModeBrowser:
		res.Mode = ModeBrowser
		res.Browser = stored.Browser
		res.Profile = stored.Profile
	case ModeCookieFile:
		if hasCookieFile {
			res.Mode = ModeCookieFile
		} else {
			res.Mode = ModeNone
		}
	default:
		res.Mode = ModeNone
	}

	return res, nil
}

// UpdateSettings updates the non-secret media authentication configuration.
func (s *Service) UpdateSettings(ctx context.Context, req UpdateSettingsRequest) (*Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.repo == nil {
		return nil, errors.New("settings repository not available")
	}

	var stored StoredSettings

	switch req.Mode {
	case ModeNone, "":
		stored.Mode = ModeNone

	case ModeBrowser:
		browser, err := ValidateBrowser(req.Browser)
		if err != nil {
			return nil, err
		}
		profile, err := ValidateProfile(req.Profile)
		if err != nil {
			return nil, err
		}
		stored.Mode = ModeBrowser
		stored.Browser = browser
		stored.Profile = profile

	case ModeCookieFile:
		hasCookieFile := false
		if s.secretStore != nil && s.secretStore.Available() {
			has, err := s.secretStore.Has(ctx, SecretScope, SecretOwner, SecretField)
			if err == nil && has {
				hasCookieFile = true
			}
		}
		if !hasCookieFile {
			return nil, ErrNoCookieFile
		}
		stored.Mode = ModeCookieFile

	default:
		return nil, ErrInvalidMode
	}

	data, err := json.Marshal(stored)
	if err != nil {
		return nil, fmt.Errorf("failed to encode media auth settings: %w", err)
	}

	if err := s.repo.Set(ctx, SettingKeyMediaAuth, string(data)); err != nil {
		return nil, fmt.Errorf("failed to persist media auth settings: %w", err)
	}

	hasCookieFile := false
	if s.secretStore != nil && s.secretStore.Available() {
		has, _ := s.secretStore.Has(ctx, SecretScope, SecretOwner, SecretField)
		hasCookieFile = has
	}

	return &Settings{
		Mode:          stored.Mode,
		Browser:       stored.Browser,
		Profile:       stored.Profile,
		HasCookieFile: hasCookieFile,
	}, nil
}

// ImportCookies validates and encrypts uploaded cookie data into secure storage.
func (s *Service) ImportCookies(ctx context.Context, cookieData []byte) (*Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.secretStore == nil || !s.secretStore.Available() {
		return nil, ErrSecretStorageUnavailable
	}

	if err := ValidateCookieFile(cookieData); err != nil {
		return nil, err
	}

	if err := s.secretStore.Put(ctx, SecretScope, SecretOwner, SecretField, string(cookieData)); err != nil {
		return nil, fmt.Errorf("failed to encrypt and store cookie data: %w", err)
	}

	// Read current settings to return accurate status
	var stored StoredSettings
	if s.repo != nil {
		if raw, err := s.repo.Get(ctx, SettingKeyMediaAuth); err == nil && strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &stored)
		}
	}

	mode := stored.Mode
	if mode == "" {
		mode = ModeNone
	}

	return &Settings{
		Mode:          mode,
		Browser:       stored.Browser,
		Profile:       stored.Profile,
		HasCookieFile: true,
	}, nil
}

// DeleteCookies removes stored encrypted cookies and resets cookie_file mode to none if active.
func (s *Service) DeleteCookies(ctx context.Context) (*Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.secretStore != nil {
		_ = s.secretStore.Delete(ctx, SecretScope, SecretOwner, SecretField)
	}

	var stored StoredSettings
	if s.repo != nil {
		if raw, err := s.repo.Get(ctx, SettingKeyMediaAuth); err == nil && strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &stored)
		}

		if stored.Mode == ModeCookieFile {
			stored.Mode = ModeNone
			data, err := json.Marshal(stored)
			if err == nil {
				_ = s.repo.Set(ctx, SettingKeyMediaAuth, string(data))
			}
		}
	}

	mode := stored.Mode
	if mode == "" {
		mode = ModeNone
	}

	return &Settings{
		Mode:          mode,
		Browser:       stored.Browser,
		Profile:       stored.Profile,
		HasCookieFile: false,
	}, nil
}

// PrepareAuthArgs resolves the active media auth settings and constructs runtime yt-dlp arguments.
// For ModeCookieFile, it materializes an ephemeral temporary file and returns a cleanup function to delete it.
func (s *Service) PrepareAuthArgs(ctx context.Context) ([]string, func(), error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, func() {}, fmt.Errorf("failed to load media auth settings: %w", err)
	}

	switch settings.Mode {
	case ModeBrowser:
		browser, err := ValidateBrowser(settings.Browser)
		if err != nil {
			return nil, func() {}, fmt.Errorf("invalid browser configuration: %w", err)
		}
		profile, err := ValidateProfile(settings.Profile)
		if err != nil {
			return nil, func() {}, fmt.Errorf("invalid browser profile: %w", err)
		}
		spec := browser
		if profile != "" {
			spec = fmt.Sprintf("%s:%s", browser, profile)
		}
		return []string{"--cookies-from-browser", spec}, func() {}, nil

	case ModeCookieFile:
		if !settings.HasCookieFile || s.secretStore == nil || !s.secretStore.Available() {
			return nil, func() {}, errors.New("imported cookies are unavailable")
		}

		cookieData, err := s.secretStore.Get(ctx, SecretScope, SecretOwner, SecretField)
		if err != nil {
			return nil, func() {}, fmt.Errorf("imported cookies could not be decrypted: %w", err)
		}
		if strings.TrimSpace(cookieData) == "" {
			return nil, func() {}, errors.New("imported cookies are unavailable")
		}

		tempPath, err := s.createTempCookieFile(cookieData)
		if err != nil {
			return nil, func() {}, fmt.Errorf("failed to prepare temporary cookie file: %w", err)
		}

		var once sync.Once
		cleanup := func() {
			once.Do(func() {
				_ = os.Remove(tempPath)
			})
		}

		return []string{"--cookies", tempPath}, cleanup, nil

	default: // ModeNone
		return nil, func() {}, nil
	}
}

// createTempCookieFile safely creates an ephemeral cookie file with restrictive permissions.
func (s *Service) createTempCookieFile(cookieData string) (string, error) {
	if err := os.MkdirAll(s.tempAuthDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create auth temp dir: %w", err)
	}

	randomSuffix := make([]byte, 16)
	if _, err := rand.Read(randomSuffix); err != nil {
		return "", fmt.Errorf("failed to generate random filename: %w", err)
	}
	filename := TempCookiePrefix + hex.EncodeToString(randomSuffix) + TempCookieSuffix
	tempPath := filepath.Join(s.tempAuthDir, filename)

	f, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary cookie file: %w", err)
	}

	if _, err := f.WriteString(cookieData); err != nil {
		_ = f.Close()
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("failed to write temporary cookie file: %w", err)
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tempPath)
		return "", fmt.Errorf("failed to close temporary cookie file: %w", err)
	}

	return tempPath, nil
}

// CleanupStaleTempFiles removes any leftover ephemeral cookie files in tempAuthDir.
// It strictly removes ONLY files matching TempCookiePrefix and TempCookieSuffix and never touches subdirectories or unrelated files.
func (s *Service) CleanupStaleTempFiles() error {
	if _, err := os.Stat(s.tempAuthDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(s.tempAuthDir)
	if err != nil {
		return fmt.Errorf("failed to read auth temp dir %s: %w", s.tempAuthDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue // Never touch directories
		}

		name := entry.Name()
		if strings.HasPrefix(name, TempCookiePrefix) && strings.HasSuffix(name, TempCookieSuffix) {
			fullPath := filepath.Join(s.tempAuthDir, name)
			if err := os.Remove(fullPath); err != nil {
				log.Printf("mediaauth: failed to remove stale cookie file %s: %v", fullPath, err)
			} else {
				log.Printf("mediaauth: cleaned up stale cookie file %s", fullPath)
			}
		}
	}

	return nil
}
