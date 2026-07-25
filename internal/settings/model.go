package settings

import "context"

// ISettingsRepository defines the persistence interface for application settings.
type ISettingsRepository interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// QueueSettings represents the queue configuration settings.
type QueueSettings struct {
	MaxConcurrentDownloads          int  `json:"maxConcurrentDownloads"`
	EffectiveMaxConcurrentDownloads int  `json:"effectiveMaxConcurrentDownloads"`
	EnvironmentOverride             bool `json:"environmentOverride"`
}

// AppSettings represents user-configurable application settings.
type AppSettings struct {
	Queue QueueSettings `json:"queue"`
}

// UpdateQueueSettingsRequest represents the request body for PUT /settings.
type UpdateQueueSettingsRequest struct {
	Queue struct {
		MaxConcurrentDownloads int `json:"maxConcurrentDownloads"`
	} `json:"queue"`
}
