package networkpolicy

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var headerToken = regexp.MustCompile(`^[!#$%&'*+\-.^_` + "`" + `|~0-9A-Za-z]+$`)

var forbiddenHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true,
	"connection": true, "proxy-connection": true, "upgrade": true,
	"trailer": true, "te": true,
}

var sensitiveHeaders = map[string]bool{
	"authorization": true, "proxy-authorization": true, "cookie": true,
	"set-cookie": true, "x-api-key": true, "x-auth-token": true,
}

func ValidateBandwidth(v int64) error {
	if v < 0 || v > MaxSafeInteger {
		return fmt.Errorf("bandwidth limit must be between 0 and %d bytes per second", MaxSafeInteger)
	}
	return nil
}

func ValidateProxy(p *ProxyPolicy) error {
	if p == nil {
		return nil
	}
	switch p.Mode {
	case ProxyModeDisabled, ProxyModeSystem:
		if p.Host != "" || p.Port != 0 || p.Username != "" {
			return fmt.Errorf("host, port, and username are only valid for custom proxy mode")
		}
	case ProxyModeCustom:
		if p.Protocol != ProxyProtocolHTTP && p.Protocol != ProxyProtocolHTTPS && p.Protocol != ProxyProtocolSOCKS5 {
			return fmt.Errorf("unsupported proxy protocol")
		}
		if strings.TrimSpace(p.Host) == "" {
			return fmt.Errorf("proxy host is required")
		}
		if p.Port < 1 || p.Port > 65535 {
			return fmt.Errorf("proxy port must be between 1 and 65535")
		}
		if len(p.Host) > 255 || len(p.Username) > 256 {
			return fmt.Errorf("proxy host or username is too long")
		}
		if hasControl(p.Host) || hasControl(p.Username) {
			return fmt.Errorf("proxy fields contain control characters")
		}
		u, err := url.Parse("proxy://" + p.Host)
		if err != nil || u.User != nil || u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("proxy host must not include credentials, path, query, or fragment")
		}
	default:
		return fmt.Errorf("invalid proxy mode")
	}
	if len(p.NoProxy) > 128 {
		return fmt.Errorf("noProxy cannot exceed 128 entries")
	}
	for _, entry := range p.NoProxy {
		if len(entry) > 255 || hasControl(entry) {
			return fmt.Errorf("invalid noProxy entry")
		}
	}
	return nil
}

func ValidateHeaders(headers []HTTPHeader) error {
	if len(headers) > 32 {
		return fmt.Errorf("custom headers cannot exceed 32 entries")
	}
	seen := make(map[string]bool, len(headers))
	for i := range headers {
		h := &headers[i]
		h.Name = strings.TrimSpace(h.Name)
		lower := strings.ToLower(h.Name)
		if h.Name == "" || len(h.Name) > 128 || !headerToken.MatchString(h.Name) {
			return fmt.Errorf("invalid HTTP header name")
		}
		if forbiddenHeaders[lower] {
			return fmt.Errorf("HTTP header %s is transport-owned", h.Name)
		}
		if seen[lower] {
			return fmt.Errorf("duplicate HTTP header %s", h.Name)
		}
		seen[lower] = true
		if len(h.Value) > 8192 || strings.ContainsAny(h.Value, "\r\n\x00") {
			return fmt.Errorf("invalid value for HTTP header %s", h.Name)
		}
		if sensitiveHeaders[lower] {
			h.Sensitive = true
		}
		if h.Value != "" {
			h.HasValue = true
		}
	}
	return nil
}

func ValidateRetry(p RetryPolicy) error {
	if p.MaxAttempts < 0 || p.MaxAttempts > 100 || p.RetryWaitSeconds < 0 || p.RetryWaitSeconds > 3600 {
		return fmt.Errorf("retry policy is outside the supported range")
	}
	return nil
}

func ValidateTimeout(p TimeoutPolicy) error {
	for _, v := range []int{p.ConnectTimeoutSeconds, p.RequestTimeoutSeconds} {
		if v < 0 || v > 86400 {
			return fmt.Errorf("timeout must be 0 or between 1 and 86400 seconds")
		}
	}
	return nil
}

func ValidateDirect(p *DirectConnectionPolicy) error {
	if p == nil {
		return nil
	}
	if p.Split < 1 || p.Split > 16 || p.MaxConnectionsPerServer < 1 || p.MaxConnectionsPerServer > 16 {
		return fmt.Errorf("split and maximum connections per server must be between 1 and 16")
	}
	if p.MinSplitSizeBytes < 1<<20 || p.MinSplitSizeBytes > 1<<30 {
		return fmt.Errorf("minimum split size must be between 1 MiB and 1 GiB")
	}
	return nil
}

func ValidateNetworkPolicy(p *JobNetworkPolicy) error {
	if p == nil {
		return nil
	}
	if err := ValidateBandwidth(p.DownloadLimitBytesPerSecond); err != nil {
		return err
	}
	if err := ValidateBandwidth(p.UploadLimitBytesPerSecond); err != nil {
		return err
	}
	if err := ValidateProxy(&p.Proxy); err != nil {
		return err
	}
	if len(p.UserAgent) > 1024 || hasControl(p.UserAgent) {
		return fmt.Errorf("invalid User-Agent")
	}
	if err := ValidateHeaders(p.HTTPHeaders); err != nil {
		return err
	}
	if err := ValidateRetry(p.RetryPolicy); err != nil {
		return err
	}
	if err := ValidateTimeout(p.TimeoutPolicy); err != nil {
		return err
	}
	return ValidateDirect(p.DirectConnections)
}

func ValidateSeeding(p SeedingPolicy) error {
	validRatio := func(v *float64) bool {
		return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v > 0 && *v <= 1000
	}
	validTime := func(v *int64) bool { return v != nil && *v > 0 && *v <= 315360000 }
	switch p.Mode {
	case SeedingModeNone, SeedingModeUnlimited:
		if p.RatioLimit != nil || p.TimeLimitSeconds != nil {
			return fmt.Errorf("seeding mode %s does not accept thresholds", p.Mode)
		}
	case SeedingModeRatio:
		if !validRatio(p.RatioLimit) || p.TimeLimitSeconds != nil {
			return fmt.Errorf("ratio seeding requires one valid ratio threshold")
		}
	case SeedingModeDuration:
		if !validTime(p.TimeLimitSeconds) || p.RatioLimit != nil {
			return fmt.Errorf("duration seeding requires one valid duration threshold")
		}
	case SeedingModeRatioOrDuration:
		if !validRatio(p.RatioLimit) || !validTime(p.TimeLimitSeconds) {
			return fmt.Errorf("ratio_or_duration requires valid ratio and duration thresholds")
		}
	default:
		return fmt.Errorf("invalid seeding mode")
	}
	return nil
}

func ValidateTrackerURLs(values []string, max int) ([]string, error) {
	if len(values) > max {
		return nil, fmt.Errorf("too many tracker URLs")
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > 2048 || hasControl(raw) {
			return nil, fmt.Errorf("invalid tracker URL")
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("invalid tracker URL")
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "udp", "ws", "wss":
		default:
			return nil, fmt.Errorf("unsupported tracker URL scheme")
		}
		if !seen[raw] {
			seen[raw] = true
			result = append(result, raw)
		}
	}
	return result, nil
}

func ValidateTrackerSource(input TrackerSourceInput) error {
	if strings.TrimSpace(input.Name) == "" || len(input.Name) > 100 || hasControl(input.Name) {
		return fmt.Errorf("invalid tracker source name")
	}
	if input.RefreshIntervalSeconds < 900 || input.RefreshIntervalSeconds > 2592000 {
		return fmt.Errorf("refresh interval must be between 900 and 2592000 seconds")
	}
	u, err := url.Parse(strings.TrimSpace(input.URL))
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || hasControl(input.URL) {
		return fmt.Errorf("tracker source URL must be an HTTP(S) URL without credentials")
	}
	return nil
}

func IsSensitiveHeader(name string) bool {
	return sensitiveHeaders[strings.ToLower(strings.TrimSpace(name))]
}

func IsTerminalHTTPHeader(name string) bool {
	return forbiddenHeaders[strings.ToLower(strings.TrimSpace(name))]
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func EffectiveLimit(global, perJob int64) int64 {
	if global <= 0 {
		return perJob
	}
	if perJob <= 0 {
		return global
	}
	if global < perJob {
		return global
	}
	return perJob
}
