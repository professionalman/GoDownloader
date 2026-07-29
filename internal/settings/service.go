package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"downloader/internal/networkpolicy"
	"downloader/internal/securestore"
)

const (
	KeyMaxConcurrentDownloads   = "max_concurrent_downloads"
	KeyDefaultDownloadDirectory = "default_download_directory"
	KeyTemporaryDirectory       = "temporary_directory"
	KeyMinimumFreeSpaceBytes    = "minimum_free_space_bytes"
	KeyDefaultConflictPolicy    = "default_conflict_policy"
	KeyV07PowerSettings         = "v07_power_settings"

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
	secrets             *securestore.Store
}

// NewSettingsService creates a new SettingsService.
func NewSettingsService(repo ISettingsRepository, initialDownloadDir, initialDataDir string, secretStore ...*securestore.Store) *SettingsService {
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

	s := &SettingsService{
		repo:                repo,
		fallbackDownloadDir: dDir,
		fallbackDataDir:     dataDir,
	}
	if len(secretStore) > 0 {
		s.secrets = secretStore[0]
	}
	return s
}

func defaultPowerSettings() (networkpolicy.NetworkSettings, networkpolicy.TorrentSettings) {
	return networkpolicy.NetworkSettings{
			Proxy:       networkpolicy.ProxyPolicy{Mode: networkpolicy.ProxyModeDisabled},
			HTTPHeaders: []networkpolicy.HTTPHeader{},
			DirectConnections: networkpolicy.DirectConnectionPolicy{
				Split: 5, MaxConnectionsPerServer: 1, MinSplitSizeBytes: 20 << 20,
			},
		}, networkpolicy.TorrentSettings{
			SeedingPolicy: networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone},
		}
}

type persistedPowerSettings struct {
	Network networkpolicy.NetworkSettings `json:"network"`
	Torrent networkpolicy.TorrentSettings `json:"torrent"`
}

func (s *SettingsService) getPowerSettings(ctx context.Context) (networkpolicy.NetworkSettings, networkpolicy.TorrentSettings, map[string]bool, []ApplicationResult, error) {
	nw, torrent := defaultPowerSettings()
	if raw, err := s.repo.Get(ctx, KeyV07PowerSettings); err == nil && raw != "" {
		var persisted persistedPowerSettings
		if json.Unmarshal([]byte(raw), &persisted) == nil {
			nw = persisted.Network
			torrent = persisted.Torrent
		}
	}
	if nw.Proxy.Mode == "" {
		nw.Proxy.Mode = networkpolicy.ProxyModeDisabled
	}
	if nw.DirectConnections.Split == 0 {
		nw.DirectConnections = networkpolicy.DirectConnectionPolicy{Split: 5, MaxConnectionsPerServer: 1, MinSplitSizeBytes: 20 << 20}
	}
	if torrent.SeedingPolicy.Mode == "" {
		torrent.SeedingPolicy.Mode = networkpolicy.SeedingModeNone
	}

	overrides := map[string]bool{}
	var warnings []ApplicationResult
	warn := func(name, path string) {
		warnings = append(warnings, ApplicationResult{
			Target: path, Status: "ignored", Code: "INVALID_ENVIRONMENT_OVERRIDE",
			Message: name + " was ignored because it is invalid",
		})
	}
	applyInt64Env := func(name, path string, target *int64) {
		if raw := os.Getenv(name); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && networkpolicy.ValidateBandwidth(v) == nil {
				*target = v
				overrides[path] = true
			} else {
				warn(name, path)
			}
		}
	}
	applyInt64Env("GLOBAL_DOWNLOAD_LIMIT_BYTES_PER_SECOND", "network.globalDownloadLimitBytesPerSecond", &nw.GlobalDownloadLimitBytesPerSecond)
	applyInt64Env("DEFAULT_TORRENT_DOWNLOAD_LIMIT_BYTES_PER_SECOND", "torrent.downloadLimitBytesPerSecond", &torrent.DownloadLimitBytesPerSecond)
	applyInt64Env("DEFAULT_TORRENT_UPLOAD_LIMIT_BYTES_PER_SECOND", "torrent.uploadLimitBytesPerSecond", &torrent.UploadLimitBytesPerSecond)
	if raw := os.Getenv("DEFAULT_USER_AGENT"); raw != "" {
		nw.UserAgent = raw
		overrides["network.userAgent"] = true
	}
	if raw := os.Getenv("DEFAULT_PROXY_MODE"); raw != "" {
		candidate := nw.Proxy
		candidate.Mode = networkpolicy.ProxyMode(strings.ToLower(raw))
		if protocol := os.Getenv("DEFAULT_PROXY_PROTOCOL"); protocol != "" {
			candidate.Protocol = networkpolicy.ProxyProtocol(strings.ToLower(protocol))
		}
		candidate.Host = os.Getenv("DEFAULT_PROXY_HOST")
		candidate.Username = os.Getenv("DEFAULT_PROXY_USERNAME")
		if port, err := strconv.Atoi(os.Getenv("DEFAULT_PROXY_PORT")); err == nil {
			candidate.Port = port
		}
		if noProxy := os.Getenv("DEFAULT_NO_PROXY"); noProxy != "" {
			candidate.NoProxy = strings.Split(noProxy, ",")
		}
		if networkpolicy.ValidateProxy(&candidate) == nil {
			nw.Proxy = candidate
			overrides["network.proxy"] = true
		} else {
			warn("DEFAULT_PROXY_*", "network.proxy")
		}
	}
	if os.Getenv("DEFAULT_PROXY_PASSWORD") != "" {
		nw.Proxy.HasPassword = true
		nw.Proxy.SecretSource = "environment"
		overrides["network.proxyPassword"] = true
	} else if s.secrets != nil {
		has, _ := s.secrets.Has(ctx, "settings", "global", "proxy_password")
		nw.Proxy.HasPassword = has
		if has {
			nw.Proxy.SecretSource = "encrypted"
		}
	}
	applyIntEnv := func(name, path string, target *int, min, max int) {
		if raw := os.Getenv(name); raw != "" {
			if v, err := strconv.Atoi(raw); err == nil && v >= min && v <= max {
				*target = v
				overrides[path] = true
			} else {
				warn(name, path)
			}
		}
	}
	applyIntEnv("DEFAULT_MAX_ATTEMPTS", "network.retryPolicy.maxAttempts", &nw.RetryPolicy.MaxAttempts, 0, 100)
	applyIntEnv("DEFAULT_RETRY_WAIT_SECONDS", "network.retryPolicy.retryWaitSeconds", &nw.RetryPolicy.RetryWaitSeconds, 0, 3600)
	applyIntEnv("DEFAULT_CONNECT_TIMEOUT_SECONDS", "network.timeoutPolicy.connectTimeoutSeconds", &nw.TimeoutPolicy.ConnectTimeoutSeconds, 0, 86400)
	applyIntEnv("DEFAULT_REQUEST_TIMEOUT_SECONDS", "network.timeoutPolicy.requestTimeoutSeconds", &nw.TimeoutPolicy.RequestTimeoutSeconds, 0, 86400)
	applyIntEnv("DEFAULT_ARIA2_SPLIT", "network.directConnections.split", &nw.DirectConnections.Split, 1, 16)
	applyIntEnv("DEFAULT_ARIA2_MAX_CONNECTIONS_PER_SERVER", "network.directConnections.maxConnectionsPerServer", &nw.DirectConnections.MaxConnectionsPerServer, 1, 16)
	if raw := os.Getenv("DEFAULT_ARIA2_MIN_SPLIT_SIZE_BYTES"); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 1<<20 && v <= 1<<30 {
			nw.DirectConnections.MinSplitSizeBytes = v
			overrides["network.directConnections.minSplitSizeBytes"] = true
		} else {
			warn("DEFAULT_ARIA2_MIN_SPLIT_SIZE_BYTES", "network.directConnections.minSplitSizeBytes")
		}
	}
	if raw := os.Getenv("TRACKER_AUTO_APPLY"); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			torrent.ApplyTrackerSubscriptionsToNewTorrents = v
			overrides["torrent.applyTrackerSubscriptionsToNewTorrents"] = true
		} else {
			warn("TRACKER_AUTO_APPLY", "torrent.applyTrackerSubscriptionsToNewTorrents")
		}
	}
	if raw := os.Getenv("MANAGE_QBIT_GLOBAL_NETWORK_SETTINGS"); raw != "" {
		if v, err := strconv.ParseBool(raw); err == nil {
			torrent.ManageQBitGlobalNetworkSettings = v
			overrides["torrent.manageQBitGlobalNetworkSettings"] = true
		} else {
			warn("MANAGE_QBIT_GLOBAL_NETWORK_SETTINGS", "torrent.manageQBitGlobalNetworkSettings")
		}
	}
	if raw := os.Getenv("DEFAULT_SEEDING_MODE"); raw != "" {
		candidate := torrent.SeedingPolicy
		candidate.Mode = networkpolicy.SeedingMode(strings.ToLower(raw))
		if ratioRaw := os.Getenv("DEFAULT_SEED_RATIO"); ratioRaw != "" {
			if value, parseErr := strconv.ParseFloat(ratioRaw, 64); parseErr == nil {
				candidate.RatioLimit = &value
			}
		}
		if timeRaw := os.Getenv("DEFAULT_SEED_TIME_SECONDS"); timeRaw != "" {
			if value, parseErr := strconv.ParseInt(timeRaw, 10, 64); parseErr == nil {
				candidate.TimeLimitSeconds = &value
			}
		}
		if networkpolicy.ValidateSeeding(candidate) == nil {
			torrent.SeedingPolicy = candidate
			overrides["torrent.seedingPolicy"] = true
		} else {
			warn("DEFAULT_SEEDING_MODE/DEFAULT_SEED_*", "torrent.seedingPolicy")
		}
	}
	return nw, torrent, overrides, warnings, nil
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

	nw, torrent, overrides, applicationResults, err := s.getPowerSettings(ctx)
	if err != nil {
		return nil, err
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
		Network:            nw,
		Torrent:            torrent,
		Overrides:          overrides,
		ApplicationResults: applicationResults,
	}, nil
}

func (s *SettingsService) UpdatePowerSettings(ctx context.Context, req *UpdateSettingsRequest) (*AppSettings, error) {
	current, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	nw, torrent := current.Network, current.Torrent
	if req.Network != nil {
		v := req.Network
		if v.GlobalDownloadLimitBytesPerSecond != nil {
			nw.GlobalDownloadLimitBytesPerSecond = *v.GlobalDownloadLimitBytesPerSecond
		}
		if v.Proxy != nil {
			hasPassword := nw.Proxy.HasPassword
			secretSource := nw.Proxy.SecretSource
			nw.Proxy = *v.Proxy
			if !v.ClearProxyPassword && (v.ProxyPassword == nil || *v.ProxyPassword == "") {
				nw.Proxy.HasPassword = hasPassword
				nw.Proxy.SecretSource = secretSource
			}
		}
		if v.UserAgent != nil {
			nw.UserAgent = strings.TrimSpace(*v.UserAgent)
		}
		if v.HTTPHeaders != nil {
			nw.HTTPHeaders = append([]networkpolicy.HTTPHeader(nil), (*v.HTTPHeaders)...)
		}
		if v.RetryPolicy != nil {
			nw.RetryPolicy = *v.RetryPolicy
		}
		if v.TimeoutPolicy != nil {
			nw.TimeoutPolicy = *v.TimeoutPolicy
		}
		if v.DirectConnections != nil {
			nw.DirectConnections = *v.DirectConnections
		}
	}
	if req.Torrent != nil {
		v := req.Torrent
		if v.DownloadLimitBytesPerSecond != nil {
			torrent.DownloadLimitBytesPerSecond = *v.DownloadLimitBytesPerSecond
		}
		if v.UploadLimitBytesPerSecond != nil {
			torrent.UploadLimitBytesPerSecond = *v.UploadLimitBytesPerSecond
		}
		if v.SeedingPolicy != nil {
			torrent.SeedingPolicy = *v.SeedingPolicy
		}
		if v.ApplyTrackerSubscriptionsToNewTorrents != nil {
			torrent.ApplyTrackerSubscriptionsToNewTorrents = *v.ApplyTrackerSubscriptionsToNewTorrents
		}
		if v.ManageQBitGlobalNetworkSettings != nil {
			torrent.ManageQBitGlobalNetworkSettings = *v.ManageQBitGlobalNetworkSettings
		}
	}
	policy := networkpolicy.JobNetworkPolicy{
		DownloadLimitBytesPerSecond: nw.GlobalDownloadLimitBytesPerSecond,
		Proxy:                       nw.Proxy, UserAgent: nw.UserAgent, HTTPHeaders: nw.HTTPHeaders,
		RetryPolicy: nw.RetryPolicy, TimeoutPolicy: nw.TimeoutPolicy,
		DirectConnections: &nw.DirectConnections,
	}
	if err := networkpolicy.ValidateNetworkPolicy(&policy); err != nil {
		return nil, err
	}
	if err := networkpolicy.ValidateBandwidth(torrent.DownloadLimitBytesPerSecond); err != nil {
		return nil, err
	}
	if err := networkpolicy.ValidateBandwidth(torrent.UploadLimitBytesPerSecond); err != nil {
		return nil, err
	}
	if err := networkpolicy.ValidateSeeding(torrent.SeedingPolicy); err != nil {
		return nil, err
	}
	if torrent.ManageQBitGlobalNetworkSettings {
		if nw.Proxy.Mode == networkpolicy.ProxyModeSystem {
			return nil, fmt.Errorf("managed qBittorrent proxy does not support system proxy mode")
		}
		if nw.Proxy.Mode == networkpolicy.ProxyModeCustom && nw.Proxy.Protocol == networkpolicy.ProxyProtocolHTTPS {
			return nil, fmt.Errorf("managed qBittorrent proxy supports only HTTP and SOCKS5")
		}
	}

	if req.Network != nil {
		if req.Network.ClearProxyPassword {
			if s.secrets != nil {
				if err := s.secrets.Delete(ctx, "settings", "global", "proxy_password"); err != nil {
					return nil, err
				}
			}
			nw.Proxy.HasPassword = false
		} else if req.Network.ProxyPassword != nil && *req.Network.ProxyPassword != "" {
			if s.secrets == nil || !s.secrets.Available() {
				return nil, securestore.ErrUnavailable
			}
			if err := s.secrets.Put(ctx, "settings", "global", "proxy_password", *req.Network.ProxyPassword); err != nil {
				return nil, err
			}
			nw.Proxy.HasPassword = true
		}
		for i := range nw.HTTPHeaders {
			h := &nw.HTTPHeaders[i]
			field := "header:" + strings.ToLower(h.Name)
			if h.Sensitive {
				if h.ClearValue {
					if s.secrets != nil {
						if err := s.secrets.Delete(ctx, "settings", "global", field); err != nil {
							return nil, err
						}
					}
					h.HasValue = false
					h.Value = ""
					h.ClearValue = false
				} else if h.Value != "" {
					if s.secrets == nil || !s.secrets.Available() {
						return nil, securestore.ErrUnavailable
					}
					if err := s.secrets.Put(ctx, "settings", "global", field, h.Value); err != nil {
						return nil, err
					}
					h.HasValue = true
					h.Value = ""
				} else if s.secrets != nil {
					has, _ := s.secrets.Has(ctx, "settings", "global", field)
					h.HasValue = has
				}
			}
		}
	}
	data, err := json.Marshal(persistedPowerSettings{Network: nw, Torrent: torrent})
	if err != nil {
		return nil, err
	}
	if err := s.repo.Set(ctx, KeyV07PowerSettings, string(data)); err != nil {
		return nil, fmt.Errorf("persist V0.7 settings: %w", err)
	}
	return s.GetSettings(ctx)
}

// ValidateUpdate checks the complete settings request before any scope is persisted.
func (s *SettingsService) ValidateUpdate(ctx context.Context, req *UpdateSettingsRequest) error {
	if req == nil {
		return nil
	}
	if req.Queue != nil && (req.Queue.MaxConcurrentDownloads < MinMaxConcurrent || req.Queue.MaxConcurrentDownloads > MaxMaxConcurrent) {
		return fmt.Errorf("maxConcurrentDownloads must be between %d and %d", MinMaxConcurrent, MaxMaxConcurrent)
	}
	if req.Storage != nil {
		storage := req.Storage
		for name, value := range map[string]*string{
			"defaultDownloadDirectory": storage.DefaultDownloadDirectory,
			"temporaryDirectory":       storage.TemporaryDirectory,
		} {
			if value != nil {
				if strings.TrimSpace(*value) == "" {
					return fmt.Errorf("%s cannot be empty", name)
				}
				if _, err := filepath.Abs(filepath.Clean(*value)); err != nil {
					return fmt.Errorf("invalid %s path", name)
				}
			}
		}
		if storage.MinimumFreeSpaceBytes != nil && *storage.MinimumFreeSpaceBytes < 0 {
			return fmt.Errorf("minimumFreeSpaceBytes cannot be negative")
		}
		if storage.DefaultConflictPolicy != nil {
			policy := strings.ToLower(strings.TrimSpace(*storage.DefaultConflictPolicy))
			if policy != "rename" && policy != "overwrite" && policy != "fail" {
				return fmt.Errorf("defaultConflictPolicy must be rename, overwrite, or fail")
			}
		}
	}
	if req.Network == nil && req.Torrent == nil {
		return nil
	}
	current, err := s.GetSettings(ctx)
	if err != nil {
		return err
	}
	nw, torrent := current.Network, current.Torrent
	if req.Network != nil {
		value := req.Network
		if value.GlobalDownloadLimitBytesPerSecond != nil {
			nw.GlobalDownloadLimitBytesPerSecond = *value.GlobalDownloadLimitBytesPerSecond
		}
		if value.Proxy != nil {
			nw.Proxy = *value.Proxy
		}
		if value.UserAgent != nil {
			nw.UserAgent = strings.TrimSpace(*value.UserAgent)
		}
		if value.HTTPHeaders != nil {
			nw.HTTPHeaders = append([]networkpolicy.HTTPHeader(nil), (*value.HTTPHeaders)...)
		}
		if value.RetryPolicy != nil {
			nw.RetryPolicy = *value.RetryPolicy
		}
		if value.TimeoutPolicy != nil {
			nw.TimeoutPolicy = *value.TimeoutPolicy
		}
		if value.DirectConnections != nil {
			nw.DirectConnections = *value.DirectConnections
		}
		if value.ProxyPassword != nil && *value.ProxyPassword != "" && (s.secrets == nil || !s.secrets.Available()) {
			return securestore.ErrUnavailable
		}
		for _, header := range nw.HTTPHeaders {
			if networkpolicy.IsSensitiveHeader(header.Name) && header.Value != "" && (s.secrets == nil || !s.secrets.Available()) {
				return securestore.ErrUnavailable
			}
		}
	}
	if req.Torrent != nil {
		value := req.Torrent
		if value.DownloadLimitBytesPerSecond != nil {
			torrent.DownloadLimitBytesPerSecond = *value.DownloadLimitBytesPerSecond
		}
		if value.UploadLimitBytesPerSecond != nil {
			torrent.UploadLimitBytesPerSecond = *value.UploadLimitBytesPerSecond
		}
		if value.SeedingPolicy != nil {
			torrent.SeedingPolicy = *value.SeedingPolicy
		}
		if value.ManageQBitGlobalNetworkSettings != nil {
			torrent.ManageQBitGlobalNetworkSettings = *value.ManageQBitGlobalNetworkSettings
		}
	}
	policy := networkpolicy.JobNetworkPolicy{
		DownloadLimitBytesPerSecond: nw.GlobalDownloadLimitBytesPerSecond,
		Proxy:                       nw.Proxy, UserAgent: nw.UserAgent, HTTPHeaders: nw.HTTPHeaders,
		RetryPolicy: nw.RetryPolicy, TimeoutPolicy: nw.TimeoutPolicy,
		DirectConnections: &nw.DirectConnections,
	}
	if err := networkpolicy.ValidateNetworkPolicy(&policy); err != nil {
		return err
	}
	if err := networkpolicy.ValidateBandwidth(torrent.DownloadLimitBytesPerSecond); err != nil {
		return err
	}
	if err := networkpolicy.ValidateBandwidth(torrent.UploadLimitBytesPerSecond); err != nil {
		return err
	}
	if err := networkpolicy.ValidateSeeding(torrent.SeedingPolicy); err != nil {
		return err
	}
	if torrent.ManageQBitGlobalNetworkSettings &&
		(nw.Proxy.Mode == networkpolicy.ProxyModeSystem ||
			(nw.Proxy.Mode == networkpolicy.ProxyModeCustom && nw.Proxy.Protocol == networkpolicy.ProxyProtocolHTTPS)) {
		return fmt.Errorf("managed qBittorrent proxy supports only disabled, HTTP, or SOCKS5")
	}
	return nil
}

func (s *SettingsService) ResolveJobPolicy(ctx context.Context, jobID, jobType string, override *networkpolicy.JobNetworkPolicyOverride) (networkpolicy.JobNetworkPolicy, *networkpolicy.RuntimePolicy, error) {
	st, err := s.GetSettings(ctx)
	if err != nil {
		return networkpolicy.JobNetworkPolicy{}, nil, err
	}
	p := networkpolicy.JobNetworkPolicy{
		DownloadLimitBytesPerSecond: 0,
		Proxy:                       st.Network.Proxy, UserAgent: st.Network.UserAgent,
		HTTPHeaders: append([]networkpolicy.HTTPHeader(nil), st.Network.HTTPHeaders...),
		RetryPolicy: st.Network.RetryPolicy, TimeoutPolicy: st.Network.TimeoutPolicy,
	}
	if jobType == "torrent" {
		p.DownloadLimitBytesPerSecond = st.Torrent.DownloadLimitBytesPerSecond
		p.UploadLimitBytesPerSecond = st.Torrent.UploadLimitBytesPerSecond
		p.Proxy = networkpolicy.ProxyPolicy{Mode: networkpolicy.ProxyModeDisabled}
	} else if jobType == "download" {
		direct := st.Network.DirectConnections
		p.DirectConnections = &direct
	}
	runtime := &networkpolicy.RuntimePolicy{HeaderValues: map[string]string{}}
	if override != nil {
		if override.DownloadLimitBytesPerSecond != nil {
			p.DownloadLimitBytesPerSecond = *override.DownloadLimitBytesPerSecond
		}
		if override.UploadLimitBytesPerSecond != nil {
			p.UploadLimitBytesPerSecond = *override.UploadLimitBytesPerSecond
		}
		if override.Proxy != nil {
			p.Proxy = *override.Proxy
		}
		if override.UserAgent != nil {
			p.UserAgent = *override.UserAgent
		}
		if override.HTTPHeaders != nil {
			p.HTTPHeaders = append([]networkpolicy.HTTPHeader(nil), (*override.HTTPHeaders)...)
		}
		if override.RetryPolicy != nil {
			p.RetryPolicy = *override.RetryPolicy
		}
		if override.TimeoutPolicy != nil {
			p.TimeoutPolicy = *override.TimeoutPolicy
		}
		if override.DirectConnections != nil {
			d := *override.DirectConnections
			p.DirectConnections = &d
		}
	}
	if err := networkpolicy.ValidateNetworkPolicy(&p); err != nil {
		return p, nil, err
	}
	if jobType == "torrent" && override != nil &&
		(override.Proxy != nil || override.UserAgent != nil || override.HTTPHeaders != nil ||
			override.RetryPolicy != nil || override.TimeoutPolicy != nil || override.DirectConnections != nil) {
		return p, nil, fmt.Errorf("requested network control is not supported for torrent jobs")
	}
	if jobType != "torrent" && override != nil && override.UploadLimitBytesPerSecond != nil {
		return p, nil, fmt.Errorf("per-job upload limits are supported only for torrent jobs")
	}
	if jobType == "media" && override != nil && override.DirectConnections != nil {
		return p, nil, fmt.Errorf("connection split controls are unsupported for media jobs")
	}
	if jobType == "download" && p.Proxy.Mode == networkpolicy.ProxyModeCustom && p.Proxy.Protocol != networkpolicy.ProxyProtocolHTTP {
		return p, nil, fmt.Errorf("direct downloads support only custom HTTP proxies")
	}
	if p.Proxy.SecretSource == "environment" {
		runtime.ProxyPassword = os.Getenv("DEFAULT_PROXY_PASSWORD")
	} else if p.Proxy.HasPassword && s.secrets != nil {
		runtime.ProxyPassword, err = s.secrets.Get(ctx, "settings", "global", "proxy_password")
		if err != nil {
			return p, nil, err
		}
	}
	if override != nil && override.ProxyPassword != nil && *override.ProxyPassword != "" {
		if s.secrets == nil || !s.secrets.Available() {
			return p, nil, securestore.ErrUnavailable
		}
		runtime.ProxyPassword = *override.ProxyPassword
		p.Proxy.HasPassword = true
		p.Proxy.SecretSource = "encrypted"
		if err := s.secrets.Put(ctx, "job", jobID, "proxy_password", runtime.ProxyPassword); err != nil {
			return p, nil, err
		}
	} else if override != nil && override.ClearProxyPassword {
		runtime.ProxyPassword = ""
		p.Proxy.HasPassword = false
		p.Proxy.SecretSource = ""
		if s.secrets != nil {
			if err := s.secrets.Delete(ctx, "job", jobID, "proxy_password"); err != nil {
				return p, nil, err
			}
		}
	}
	for i := range p.HTTPHeaders {
		h := &p.HTTPHeaders[i]
		if !h.Sensitive {
			runtime.HeaderValues[strings.ToLower(h.Name)] = h.Value
			continue
		}
		if h.ClearValue {
			h.Value = ""
			h.HasValue = false
			h.ClearValue = false
			if s.secrets != nil {
				if err := s.secrets.Delete(ctx, "job", jobID, "header:"+strings.ToLower(h.Name)); err != nil {
					return p, nil, err
				}
			}
			continue
		}
		value := h.Value
		if value == "" && s.secrets != nil {
			value, _ = s.secrets.Get(ctx, "settings", "global", "header:"+strings.ToLower(h.Name))
		}
		if value != "" {
			if s.secrets == nil || !s.secrets.Available() {
				return p, nil, securestore.ErrUnavailable
			}
			if err := s.secrets.Put(ctx, "job", jobID, "header:"+strings.ToLower(h.Name), value); err != nil {
				return p, nil, err
			}
			runtime.HeaderValues[strings.ToLower(h.Name)] = value
			h.HasValue = true
			h.Value = ""
		}
	}
	runtime.Policy = p
	return p, runtime, nil
}

func (s *SettingsService) RuntimePolicyForJob(ctx context.Context, jobID string, p networkpolicy.JobNetworkPolicy) (*networkpolicy.RuntimePolicy, error) {
	runtime := &networkpolicy.RuntimePolicy{Policy: p, HeaderValues: map[string]string{}}
	if p.Proxy.HasPassword {
		if p.Proxy.SecretSource == "environment" {
			runtime.ProxyPassword = os.Getenv("DEFAULT_PROXY_PASSWORD")
		} else if s.secrets != nil {
			value, err := s.secrets.Get(ctx, "job", jobID, "proxy_password")
			if err != nil {
				return nil, err
			}
			runtime.ProxyPassword = value
		}
	}
	for _, h := range p.HTTPHeaders {
		if h.Sensitive {
			if s.secrets == nil {
				return nil, securestore.ErrUnavailable
			}
			value, err := s.secrets.Get(ctx, "job", jobID, "header:"+strings.ToLower(h.Name))
			if err != nil {
				return nil, err
			}
			runtime.HeaderValues[strings.ToLower(h.Name)] = value
		} else {
			runtime.HeaderValues[strings.ToLower(h.Name)] = h.Value
		}
	}
	return runtime, nil
}

func (s *SettingsService) RuntimeGlobalNetworkPolicy(ctx context.Context) (*networkpolicy.RuntimePolicy, error) {
	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	policy := networkpolicy.JobNetworkPolicy{
		Proxy: settings.Network.Proxy, UserAgent: settings.Network.UserAgent,
		HTTPHeaders: settings.Network.HTTPHeaders, RetryPolicy: settings.Network.RetryPolicy,
		TimeoutPolicy: settings.Network.TimeoutPolicy,
	}
	runtime := &networkpolicy.RuntimePolicy{Policy: policy, HeaderValues: map[string]string{}}
	if policy.Proxy.HasPassword {
		if policy.Proxy.SecretSource == "environment" {
			runtime.ProxyPassword = os.Getenv("DEFAULT_PROXY_PASSWORD")
		} else if s.secrets != nil {
			runtime.ProxyPassword, err = s.secrets.Get(ctx, "settings", "global", "proxy_password")
			if err != nil {
				return nil, err
			}
		}
	}
	return runtime, nil
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
