package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	KeyMaxConcurrentDownloads   = "max_concurrent_downloads"
	KeyDefaultDownloadDirectory = "default_download_directory"
	KeyTemporaryDirectory       = "temporary_directory"
	KeyMinimumFreeSpaceBytes    = "minimum_free_space_bytes"
	KeyDefaultConflictPolicy    = "default_conflict_policy"

	DefaultMaxConcurrent  = 3
	MinMaxConcurrent      = 1
	MaxMaxConcurrent      = 20
	DefaultMinFreeSpace   = 1073741824 // 1 GiB
	DefaultConflictPolicy = "rename"
	FallbackDownloadDir   = "./downloads"
	FallbackDataDir       = "./data"
)

// SettingsService manages application settings logic and environment overrides.
type SettingsService struct {
	repo                ISettingsRepository
	fallbackDownloadDir string
	fallbackDataDir     string
}

// NewSettingsService creates a new SettingsService.
func NewSettingsService(repo ISettingsRepository, initialDownloadDir, initialDataDir string) *SettingsService {
	dDir := initialDownloadDir
	if dDir == "" {
		dDir = FallbackDownloadDir
	}
	absDDir, err := filepath.Abs(dDir)
	if err == nil {
		dDir = absDDir
	}

	dataDir := initialDataDir
	if dataDir == "" {
		dataDir = FallbackDataDir
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err == nil {
		dataDir = absDataDir
	}

	return &SettingsService{
		repo:                repo,
		fallbackDownloadDir: dDir,
		fallbackDataDir:     dataDir,
	}
}

// GetSettings retrieves the current AppSettings, considering persisted values and environment overrides.
func (s *SettingsService) GetSettings(ctx context.Context) (*AppSettings, error) {
	// Queue settings
	persistedMax := DefaultMaxConcurrent
	if valStr, err := s.repo.Get(ctx, KeyMaxConcurrentDownloads); err == nil && valStr != "" {
		if parsed, pErr := strconv.Atoi(valStr); pErr == nil && parsed >= MinMaxConcurrent && parsed <= MaxMaxConcurrent {
			persistedMax = parsed
		}
	}
	effectiveMax := persistedMax
	queueEnvOverride := false
	if envVal := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); envVal != "" {
		if parsedEnv, pErr := strconv.Atoi(envVal); pErr == nil && parsedEnv >= MinMaxConcurrent && parsedEnv <= MaxMaxConcurrent {
			effectiveMax = parsedEnv
			queueEnvOverride = true
		}
	}

	// Storage settings defaults
	defaultDownloadDir := s.fallbackDownloadDir
	if valStr, err := s.repo.Get(ctx, KeyDefaultDownloadDirectory); err == nil && valStr != "" {
		defaultDownloadDir = valStr
	}

	tempDir := filepath.Join(s.fallbackDataDir, "tmp")
	if valStr, err := s.repo.Get(ctx, KeyTemporaryDirectory); err == nil && valStr != "" {
		tempDir = valStr
	}

	minFreeSpace := int64(DefaultMinFreeSpace)
	if valStr, err := s.repo.Get(ctx, KeyMinimumFreeSpaceBytes); err == nil && valStr != "" {
		if parsed, pErr := strconv.ParseInt(valStr, 10, 64); pErr == nil && parsed >= 0 {
			minFreeSpace = parsed
		}
	}

	defaultConflictPolicy := DefaultConflictPolicy
	if valStr, err := s.repo.Get(ctx, KeyDefaultConflictPolicy); err == nil && valStr != "" {
		defaultConflictPolicy = valStr
	}

	// Environment overrides for storage
	effectiveDefaultDownloadDir := defaultDownloadDir
	overrideDefaultDir := false
	if envVal := os.Getenv("DOWNLOAD_DIR"); envVal != "" {
		absEnv, err := filepath.Abs(envVal)
		if err == nil {
			effectiveDefaultDownloadDir = absEnv
			overrideDefaultDir = true
		}
	}

	effectiveTempDir := tempDir
	overrideTempDir := false
	if envVal := os.Getenv("TEMP_DIR"); envVal != "" {
		absEnv, err := filepath.Abs(envVal)
		if err == nil {
			effectiveTempDir = absEnv
			overrideTempDir = true
		}
	}

	effectiveMinFreeSpace := minFreeSpace
	overrideMinFreeSpace := false
	if envVal := os.Getenv("MIN_FREE_SPACE_BYTES"); envVal != "" {
		if parsedEnv, pErr := strconv.ParseInt(envVal, 10, 64); pErr == nil && parsedEnv >= 0 {
			effectiveMinFreeSpace = parsedEnv
			overrideMinFreeSpace = true
		}
	}

	effectiveDefaultConflictPolicy := defaultConflictPolicy
	overrideConflictPolicy := false
	if envVal := os.Getenv("DEFAULT_CONFLICT_POLICY"); envVal != "" {
		lowerEnv := strings.ToLower(envVal)
		if lowerEnv == "rename" || lowerEnv == "overwrite" || lowerEnv == "fail" {
			effectiveDefaultConflictPolicy = lowerEnv
			overrideConflictPolicy = true
		}
	}

	return &AppSettings{
		Queue: QueueSettings{
			MaxConcurrentDownloads:          persistedMax,
			EffectiveMaxConcurrentDownloads: effectiveMax,
			EnvironmentOverride:             queueEnvOverride,
		},
		Storage: StorageSettings{
			DefaultDownloadDirectory:          defaultDownloadDir,
			EffectiveDefaultDownloadDirectory: effectiveDefaultDownloadDir,
			TemporaryDirectory:                tempDir,
			EffectiveTemporaryDirectory:       effectiveTempDir,
			MinimumFreeSpaceBytes:             minFreeSpace,
			EffectiveMinimumFreeSpaceBytes:    effectiveMinFreeSpace,
			DefaultConflictPolicy:             defaultConflictPolicy,
			EffectiveDefaultConflictPolicy:    effectiveDefaultConflictPolicy,
			Overrides: StorageOverrides{
				DefaultDownloadDirectory: overrideDefaultDir,
				TemporaryDirectory:       overrideTempDir,
				MinimumFreeSpaceBytes:    overrideMinFreeSpace,
				DefaultConflictPolicy:    overrideConflictPolicy,
			},
		},
	}, nil
}

// EffectiveMaxConcurrentDownloads returns the active capacity limit directly.
func (s *SettingsService) EffectiveMaxConcurrentDownloads(ctx context.Context) int {
	appSettings, err := s.GetSettings(ctx)
	if err != nil {
		return DefaultMaxConcurrent
	}
	return appSettings.Queue.EffectiveMaxConcurrentDownloads
}

// UpdateQueueSettings validates and persists user queue settings.
func (s *SettingsService) UpdateQueueSettings(ctx context.Context, maxConcurrent int) (*AppSettings, error) {
	if maxConcurrent < MinMaxConcurrent || maxConcurrent > MaxMaxConcurrent {
		return nil, fmt.Errorf("maxConcurrentDownloads must be between %d and %d", MinMaxConcurrent, MaxMaxConcurrent)
	}

	if err := s.repo.Set(ctx, KeyMaxConcurrentDownloads, strconv.Itoa(maxConcurrent)); err != nil {
		return nil, fmt.Errorf("persist maxConcurrentDownloads: %w", err)
	}

	return s.GetSettings(ctx)
}

// UpdateStorageSettings validates and persists storage settings.
func (s *SettingsService) UpdateStorageSettings(ctx context.Context, req *UpdateSettingsRequest) (*AppSettings, error) {
	if req == nil || req.Storage == nil {
		return s.GetSettings(ctx)
	}

	st := req.Storage

	if st.DefaultDownloadDirectory != nil {
		pathStr := strings.TrimSpace(*st.DefaultDownloadDirectory)
		if pathStr == "" {
			return nil, fmt.Errorf("defaultDownloadDirectory cannot be empty")
		}
		cleaned := filepath.Clean(pathStr)
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return nil, fmt.Errorf("invalid defaultDownloadDirectory path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("cannot create or access defaultDownloadDirectory %s: %w", absPath, err)
		}
		if err := s.repo.Set(ctx, KeyDefaultDownloadDirectory, absPath); err != nil {
			return nil, fmt.Errorf("persist defaultDownloadDirectory: %w", err)
		}
	}

	if st.TemporaryDirectory != nil {
		pathStr := strings.TrimSpace(*st.TemporaryDirectory)
		if pathStr == "" {
			return nil, fmt.Errorf("temporaryDirectory cannot be empty")
		}
		cleaned := filepath.Clean(pathStr)
		absPath, err := filepath.Abs(cleaned)
		if err != nil {
			return nil, fmt.Errorf("invalid temporaryDirectory path: %w", err)
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			return nil, fmt.Errorf("cannot create or access temporaryDirectory %s: %w", absPath, err)
		}
		if err := s.repo.Set(ctx, KeyTemporaryDirectory, absPath); err != nil {
			return nil, fmt.Errorf("persist temporaryDirectory: %w", err)
		}
	}

	if st.MinimumFreeSpaceBytes != nil {
		val := *st.MinimumFreeSpaceBytes
		if val < 0 {
			return nil, fmt.Errorf("minimumFreeSpaceBytes cannot be negative")
		}
		if err := s.repo.Set(ctx, KeyMinimumFreeSpaceBytes, strconv.FormatInt(val, 10)); err != nil {
			return nil, fmt.Errorf("persist minimumFreeSpaceBytes: %w", err)
		}
	}

	if st.DefaultConflictPolicy != nil {
		pol := strings.ToLower(strings.TrimSpace(*st.DefaultConflictPolicy))
		if pol != "rename" && pol != "overwrite" && pol != "fail" {
			return nil, fmt.Errorf("defaultConflictPolicy must be rename, overwrite, or fail")
		}
		if err := s.repo.Set(ctx, KeyDefaultConflictPolicy, pol); err != nil {
			return nil, fmt.Errorf("persist defaultConflictPolicy: %w", err)
		}
	}

	return s.GetSettings(ctx)
}
