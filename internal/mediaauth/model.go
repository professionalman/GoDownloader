package mediaauth

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// Mode represents the media authentication mode.
type Mode string

const (
	ModeNone       Mode = "none"
	ModeBrowser    Mode = "browser"
	ModeCookieFile Mode = "cookie_file"
)

// MaxCookieFileSize is the maximum allowed size for imported cookie files (10 MiB).
const MaxCookieFileSize = 10 * 1024 * 1024

// MaxProfileLength is the maximum allowed length for browser profile names.
const MaxProfileLength = 255

// AllowedBrowsers is the strict allowlist of supported browser identifiers for yt-dlp.
var AllowedBrowsers = map[string]string{
	"brave":    "Brave",
	"chrome":   "Google Chrome",
	"chromium": "Chromium",
	"edge":     "Microsoft Edge",
	"firefox":  "Mozilla Firefox",
	"opera":    "Opera",
	"safari":   "Safari",
	"vivaldi":  "Vivaldi",
	"whale":    "Naver Whale",
}

// Settings represents the public media authentication configuration.
type Settings struct {
	Mode          Mode   `json:"mode"`
	Browser       string `json:"browser,omitempty"`
	Profile       string `json:"profile,omitempty"`
	HasCookieFile bool   `json:"hasCookieFile"`
}

// UpdateSettingsRequest represents the strict request body for updating media auth configuration.
type UpdateSettingsRequest struct {
	Mode    Mode   `json:"mode"`
	Browser string `json:"browser,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// StoredSettings is the internal representation persisted in app_settings (never contains secrets).
type StoredSettings struct {
	Mode    Mode   `json:"mode"`
	Browser string `json:"browser,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// ValidateBrowser checks if the given browser is in the allowed list.
func ValidateBrowser(browser string) (string, error) {
	b := strings.ToLower(strings.TrimSpace(browser))
	if b == "" {
		return "", errors.New("browser is required for browser session mode")
	}
	if _, ok := AllowedBrowsers[b]; !ok {
		return "", fmt.Errorf("unsupported browser %q: must be one of brave, chrome, chromium, edge, firefox, opera, safari, vivaldi, whale", b)
	}
	return b, nil
}

// ValidateProfile checks if the browser profile is valid and safe.
func ValidateProfile(profile string) (string, error) {
	p := strings.TrimSpace(profile)
	if p == "" {
		return "", nil
	}
	if len(p) > MaxProfileLength {
		return "", fmt.Errorf("browser profile exceeds maximum length of %d characters", MaxProfileLength)
	}
	if strings.HasPrefix(p, "-") {
		return "", errors.New("browser profile cannot begin with a dash or flag prefix")
	}
	for _, r := range p {
		if unicode.IsControl(r) || r < 32 || r == 127 {
			return "", errors.New("browser profile contains invalid control characters")
		}
	}
	return p, nil
}

// NormalizeCookieFile validates and normalizes Netscape/Mozilla cookie file data.
// It strips UTF-8 BOM, removes leading blank lines, ensures the Netscape header is the first physical line,
// preserves cookie entries (including #HttpOnly_ prefixes), and ensures at least one valid cookie entry is present.
func NormalizeCookieFile(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("cookie file is empty")
	}
	if len(data) > MaxCookieFileSize {
		return nil, fmt.Errorf("cookie file exceeds maximum allowed size of %d bytes", MaxCookieFileSize)
	}

	// Strip optional UTF-8 BOM if present
	cleanData := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(cleanData)) == 0 {
		return nil, errors.New("cookie file is empty")
	}

	// Verify text content (reject binary data containing null bytes)
	if bytes.IndexByte(cleanData, 0) != -1 {
		return nil, errors.New("cookie file contains invalid binary data")
	}

	rawText := string(cleanData)
	rawLines := strings.Split(rawText, "\n")

	var normalizedLines []string
	headerFound := false
	hasCookieEntry := false

	for _, rawLine := range rawLines {
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)

		if !headerFound {
			// Skip leading empty lines before header
			if trimmed == "" {
				continue
			}

			lowerHeader := strings.ToLower(trimmed)
			if !strings.HasPrefix(lowerHeader, "# netscape http cookie file") &&
				!strings.HasPrefix(lowerHeader, "# http cookie file") {
				return nil, errors.New("invalid cookie file format: must be a Netscape/Mozilla HTTP cookie file starting with '# Netscape HTTP Cookie File' or '# HTTP Cookie File'")
			}

			headerFound = true
			normalizedLines = append(normalizedLines, line)
			continue
		}

		// Header is found, process subsequent lines
		normalizedLines = append(normalizedLines, line)

		if trimmed == "" {
			continue
		}

		// Check if this line is a valid cookie data row (either non-comment or #HttpOnly_ prefixed)
		if !strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "#HttpOnly_") {
			fields := strings.Split(trimmed, "\t")
			if len(fields) >= 4 || strings.Contains(trimmed, "\t") || len(strings.Fields(trimmed)) >= 4 {
				hasCookieEntry = true
			}
		}
	}

	if !headerFound {
		return nil, errors.New("cookie file contains no content")
	}

	if !hasCookieEntry {
		return nil, errors.New("cookie file contains no cookie entries")
	}

	normalized := strings.Join(normalizedLines, "\n")
	if !strings.HasSuffix(normalized, "\n") {
		normalized += "\n"
	}

	return []byte(normalized), nil
}

// ValidateCookieFile validates that data is a valid Netscape/Mozilla format cookie file.
func ValidateCookieFile(data []byte) error {
	_, err := NormalizeCookieFile(data)
	return err
}
