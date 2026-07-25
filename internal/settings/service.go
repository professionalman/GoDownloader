package settings

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

const (
	KeyMaxConcurrentDownloads = "max_concurrent_downloads"
	DefaultMaxConcurrent      = 3
	MinMaxConcurrent          = 1
	MaxMaxConcurrent          = 20
)

// SettingsService manages application settings logic and environment overrides.
type SettingsService struct {
	repo ISettingsRepository
}

// NewSettingsService creates a new SettingsService.
func NewSettingsService(repo ISettingsRepository) *SettingsService {
	return &SettingsService{repo: repo}
}

// GetSettings retrieves the current AppSettings, considering persisted values and environment overrides.
func (s *SettingsService) GetSettings(ctx context.Context) (*AppSettings, error) {
	persistedMax := DefaultMaxConcurrent
	valStr, err := s.repo.Get(ctx, KeyMaxConcurrentDownloads)
	if err == nil && valStr != "" {
		if parsed, pErr := strconv.Atoi(valStr); pErr == nil && parsed >= MinMaxConcurrent && parsed <= MaxMaxConcurrent {
			persistedMax = parsed
		}
	}

	effectiveMax := persistedMax
	envOverride := false

	if envVal := os.Getenv("MAX_CONCURRENT_DOWNLOADS"); envVal != "" {
		if parsedEnv, pErr := strconv.Atoi(envVal); pErr == nil && parsedEnv >= MinMaxConcurrent && parsedEnv <= MaxMaxConcurrent {
			effectiveMax = parsedEnv
			envOverride = true
		}
	}

	return &AppSettings{
		Queue: QueueSettings{
			MaxConcurrentDownloads:          persistedMax,
			EffectiveMaxConcurrentDownloads: effectiveMax,
			EnvironmentOverride:             envOverride,
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
