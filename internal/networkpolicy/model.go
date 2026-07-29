package networkpolicy

import "time"

const MaxSafeInteger int64 = 9007199254740991

type ProxyMode string

const (
	ProxyModeDisabled ProxyMode = "disabled"
	ProxyModeSystem   ProxyMode = "system"
	ProxyModeCustom   ProxyMode = "custom"
)

type ProxyProtocol string

const (
	ProxyProtocolHTTP   ProxyProtocol = "http"
	ProxyProtocolHTTPS  ProxyProtocol = "https"
	ProxyProtocolSOCKS5 ProxyProtocol = "socks5"
)

type ProxyPolicy struct {
	Mode         ProxyMode     `json:"mode"`
	Protocol     ProxyProtocol `json:"protocol,omitempty"`
	Host         string        `json:"host,omitempty"`
	Port         int           `json:"port,omitempty"`
	Username     string        `json:"username,omitempty"`
	HasPassword  bool          `json:"hasPassword,omitempty"`
	SecretSource string        `json:"secretSource,omitempty"`
	NoProxy      []string      `json:"noProxy,omitempty"`
}

type HTTPHeader struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	HasValue   bool   `json:"hasValue,omitempty"`
	Sensitive  bool   `json:"sensitive,omitempty"`
	ClearValue bool   `json:"clearValue,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts      int `json:"maxAttempts"`
	RetryWaitSeconds int `json:"retryWaitSeconds"`
}

type TimeoutPolicy struct {
	ConnectTimeoutSeconds int `json:"connectTimeoutSeconds"`
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds"`
}

type DirectConnectionPolicy struct {
	Split                   int   `json:"split"`
	MaxConnectionsPerServer int   `json:"maxConnectionsPerServer"`
	MinSplitSizeBytes       int64 `json:"minSplitSizeBytes"`
}

type JobNetworkPolicy struct {
	DownloadLimitBytesPerSecond int64                   `json:"downloadLimitBytesPerSecond"`
	UploadLimitBytesPerSecond   int64                   `json:"uploadLimitBytesPerSecond,omitempty"`
	Proxy                       ProxyPolicy             `json:"proxy"`
	UserAgent                   string                  `json:"userAgent,omitempty"`
	HTTPHeaders                 []HTTPHeader            `json:"httpHeaders,omitempty"`
	RetryPolicy                 RetryPolicy             `json:"retryPolicy"`
	TimeoutPolicy               TimeoutPolicy           `json:"timeoutPolicy"`
	DirectConnections           *DirectConnectionPolicy `json:"directConnections,omitempty"`
}

// JobNetworkPolicyOverride is used at creation time. Nil fields inherit defaults.
// A non-nil bandwidth value of zero explicitly means unlimited.
type JobNetworkPolicyOverride struct {
	DownloadLimitBytesPerSecond *int64                  `json:"downloadLimitBytesPerSecond,omitempty"`
	UploadLimitBytesPerSecond   *int64                  `json:"uploadLimitBytesPerSecond,omitempty"`
	Proxy                       *ProxyPolicy            `json:"proxy,omitempty"`
	ProxyPassword               *string                 `json:"proxyPassword,omitempty"`
	ClearProxyPassword          bool                    `json:"clearProxyPassword,omitempty"`
	UserAgent                   *string                 `json:"userAgent,omitempty"`
	HTTPHeaders                 *[]HTTPHeader           `json:"httpHeaders,omitempty"`
	RetryPolicy                 *RetryPolicy            `json:"retryPolicy,omitempty"`
	TimeoutPolicy               *TimeoutPolicy          `json:"timeoutPolicy,omitempty"`
	DirectConnections           *DirectConnectionPolicy `json:"directConnections,omitempty"`
}

type SeedingMode string

const (
	SeedingModeNone            SeedingMode = "none"
	SeedingModeUnlimited       SeedingMode = "unlimited"
	SeedingModeRatio           SeedingMode = "ratio"
	SeedingModeDuration        SeedingMode = "duration"
	SeedingModeRatioOrDuration SeedingMode = "ratio_or_duration"
)

type SeedingPolicy struct {
	Mode             SeedingMode `json:"mode"`
	RatioLimit       *float64    `json:"ratioLimit,omitempty"`
	TimeLimitSeconds *int64      `json:"timeLimitSeconds,omitempty"`
}

type Tracker struct {
	URL string `json:"url"`
}

type CapabilityState struct {
	Supported          bool     `json:"supported"`
	MutableNow         bool     `json:"mutableNow"`
	StartupOnly        bool     `json:"startupOnly,omitempty"`
	Reason             string   `json:"reason,omitempty"`
	SupportedProtocols []string `json:"supportedProtocols,omitempty"`
	SupportedFields    []string `json:"supportedFields,omitempty"`
}

type EngineCapabilities struct {
	Pause               bool            `json:"pause"`
	Resume              bool            `json:"resume"`
	Cancel              bool            `json:"cancel"`
	Retry               bool            `json:"retry"`
	GlobalDownloadLimit bool            `json:"globalDownloadLimit"`
	PerJobDownloadLimit bool            `json:"perJobDownloadLimit"`
	PerJobUploadLimit   bool            `json:"perJobUploadLimit"`
	Proxy               bool            `json:"proxy"`
	UserAgent           bool            `json:"userAgent"`
	CustomHeaders       bool            `json:"customHeaders"`
	RetryPolicy         bool            `json:"retryPolicy"`
	TimeoutPolicy       bool            `json:"timeoutPolicy"`
	Connections         bool            `json:"connections"`
	FileSelection       bool            `json:"fileSelection"`
	Trackers            bool            `json:"trackers"`
	SeedingPolicy       bool            `json:"seedingPolicy"`
	ProxyProtocols      []ProxyProtocol `json:"proxyProtocols,omitempty"`
	StartupOnly         map[string]bool `json:"startupOnly,omitempty"`
}

type JobCapabilities struct {
	Pause         CapabilityState `json:"pause"`
	Resume        CapabilityState `json:"resume"`
	Cancel        CapabilityState `json:"cancel"`
	Retry         CapabilityState `json:"retry"`
	DownloadLimit CapabilityState `json:"downloadLimit"`
	UploadLimit   CapabilityState `json:"uploadLimit"`
	Proxy         CapabilityState `json:"proxy"`
	UserAgent     CapabilityState `json:"userAgent"`
	CustomHeaders CapabilityState `json:"customHeaders"`
	RetryPolicy   CapabilityState `json:"retryPolicy"`
	TimeoutPolicy CapabilityState `json:"timeoutPolicy"`
	Connections   CapabilityState `json:"connections"`
	FileSelection CapabilityState `json:"fileSelection"`
	Trackers      CapabilityState `json:"trackers"`
	SeedingPolicy CapabilityState `json:"seedingPolicy"`
}

type NetworkSettings struct {
	GlobalDownloadLimitBytesPerSecond int64                  `json:"globalDownloadLimitBytesPerSecond"`
	Proxy                             ProxyPolicy            `json:"proxy"`
	UserAgent                         string                 `json:"userAgent"`
	HTTPHeaders                       []HTTPHeader           `json:"httpHeaders"`
	RetryPolicy                       RetryPolicy            `json:"retryPolicy"`
	TimeoutPolicy                     TimeoutPolicy          `json:"timeoutPolicy"`
	DirectConnections                 DirectConnectionPolicy `json:"directConnections"`
}

type TorrentSettings struct {
	DownloadLimitBytesPerSecond            int64         `json:"downloadLimitBytesPerSecond"`
	UploadLimitBytesPerSecond              int64         `json:"uploadLimitBytesPerSecond"`
	SeedingPolicy                          SeedingPolicy `json:"seedingPolicy"`
	ApplyTrackerSubscriptionsToNewTorrents bool          `json:"applyTrackerSubscriptionsToNewTorrents"`
	ManageQBitGlobalNetworkSettings        bool          `json:"manageQBitGlobalNetworkSettings"`
}

type RuntimePolicy struct {
	Policy        JobNetworkPolicy
	ProxyPassword string
	HeaderValues  map[string]string
}

type TrackerSource struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	URL                    string     `json:"url"`
	Enabled                bool       `json:"enabled"`
	RefreshIntervalSeconds int64      `json:"refreshIntervalSeconds"`
	LastCheckedAt          *time.Time `json:"lastCheckedAt,omitempty"`
	LastSuccessAt          *time.Time `json:"lastSuccessAt,omitempty"`
	ETag                   string     `json:"-"`
	LastModified           string     `json:"-"`
	LastError              string     `json:"lastError,omitempty"`
	TrackerCount           int        `json:"trackerCount"`
	CreatedAt              time.Time  `json:"createdAt"`
	UpdatedAt              time.Time  `json:"updatedAt"`
}

type TrackerSourceInput struct {
	Name                   string `json:"name"`
	URL                    string `json:"url"`
	Enabled                bool   `json:"enabled"`
	RefreshIntervalSeconds int64  `json:"refreshIntervalSeconds"`
}
