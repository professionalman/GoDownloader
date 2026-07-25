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

// StorageOverrides indicates which storage settings have active environment overrides.
type StorageOverrides struct {
	DefaultDownloadDirectory bool `json:"defaultDownloadDirectory"`
	TemporaryDirectory       bool `json:"temporaryDirectory"`
	MinimumFreeSpaceBytes    bool `json:"minimumFreeSpaceBytes"`
	DefaultConflictPolicy    bool `json:"defaultConflictPolicy"`
}

// StorageSettings represents storage lifecycle configuration settings.
type StorageSettings struct {
	DefaultDownloadDirectory          string           `json:"defaultDownloadDirectory"`
	EffectiveDefaultDownloadDirectory string           `json:"effectiveDefaultDownloadDirectory"`
	TemporaryDirectory                string           `json:"temporaryDirectory"`
	EffectiveTemporaryDirectory       string           `json:"effectiveTemporaryDirectory"`
	MinimumFreeSpaceBytes             int64            `json:"minimumFreeSpaceBytes"`
	EffectiveMinimumFreeSpaceBytes    int64            `json:"effectiveMinimumFreeSpaceBytes"`
	DefaultConflictPolicy             string           `json:"defaultConflictPolicy"`
	EffectiveDefaultConflictPolicy    string           `json:"effectiveDefaultConflictPolicy"`
	Overrides                         StorageOverrides `json:"overrides"`
}

// AppSettings represents user-configurable application settings.
type AppSettings struct {
	Queue   QueueSettings   `json:"queue"`
	Storage StorageSettings `json:"storage"`
}

// UpdateQueueSettingsRequest represents the request body for updating queue settings.
type UpdateQueueSettingsRequest struct {
	Queue struct {
		MaxConcurrentDownloads int `json:"maxConcurrentDownloads"`
	} `json:"queue"`
}

// UpdateSettingsRequest represents the request body for PUT /settings.
type UpdateSettingsRequest struct {
	Queue *struct {
		MaxConcurrentDownloads int `json:"maxConcurrentDownloads"`
	} `json:"queue,omitempty"`
	Storage *struct {
		DefaultDownloadDirectory *string `json:"defaultDownloadDirectory,omitempty"`
		TemporaryDirectory       *string `json:"temporaryDirectory,omitempty"`
		MinimumFreeSpaceBytes    *int64  `json:"minimumFreeSpaceBytes,omitempty"`
		DefaultConflictPolicy    *string `json:"defaultConflictPolicy,omitempty"`
	} `json:"storage,omitempty"`
}
