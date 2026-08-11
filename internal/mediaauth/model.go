package mediaauth

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
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
// It strips UTF-8 BOM, removes leading blank lines, ensures the canonical Netscape header is the first physical line,
// preserves cookie entries (including #HttpOnly_ prefixes), and enforces strict 7-field Netscape row validation.
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

			var canonicalHeader string
			if strings.EqualFold(trimmed, "# Netscape HTTP Cookie File") {
				canonicalHeader = "# Netscape HTTP Cookie File"
			} else if strings.EqualFold(trimmed, "# HTTP Cookie File") {
				canonicalHeader = "# HTTP Cookie File"
			} else {
				return nil, errors.New("invalid cookie file format: must be a Netscape/Mozilla HTTP cookie file starting with '# Netscape HTTP Cookie File' or '# HTTP Cookie File'")
			}

			headerFound = true
			normalizedLines = append(normalizedLines, canonicalHeader)
			continue
		}

		// Header is found, process subsequent lines
		if trimmed == "" {
			normalizedLines = append(normalizedLines, "")
			continue
		}

		// Check if this line is a comment line (starts with # but NOT #HttpOnly_)
		if strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "#HttpOnly_") {
			normalizedLines = append(normalizedLines, line)
			continue
		}

		// Validate cookie data row (either non-comment or #HttpOnly_ prefixed)
		if err := validateCookieRow(line); err != nil {
			return nil, err
		}

		hasCookieEntry = true
		normalizedLines = append(normalizedLines, line)
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

// validateCookieRow validates that a line conforms to the standard 7 tab-delimited Netscape cookie row format.
// Format: domain \t includeSubdomains \t path \t secure \t expires \t name \t value
func validateCookieRow(line string) error {
	fields := strings.Split(line, "\t")
	if len(fields) != 7 {
		return fmt.Errorf("invalid cookie line: expected 7 tab-separated fields, got %d", len(fields))
	}

	domain := fields[0]
	if strings.HasPrefix(domain, "#HttpOnly_") {
		domain = strings.TrimPrefix(domain, "#HttpOnly_")
	}
	if strings.TrimSpace(domain) == "" {
		return errors.New("invalid cookie line: empty domain")
	}

	includeSub := strings.ToUpper(strings.TrimSpace(fields[1]))
	if includeSub != "TRUE" && includeSub != "FALSE" {
		return errors.New("invalid cookie line: includeSubdomains must be TRUE or FALSE")
	}

	path := strings.TrimSpace(fields[2])
	if path == "" {
		return errors.New("invalid cookie line: empty path")
	}

	secure := strings.ToUpper(strings.TrimSpace(fields[3]))
	if secure != "TRUE" && secure != "FALSE" {
		return errors.New("invalid cookie line: secure flag must be TRUE or FALSE")
	}

	expiresStr := strings.TrimSpace(fields[4])
	expires, err := strconv.ParseInt(expiresStr, 10, 64)
	if err != nil || expires < 0 {
		return errors.New("invalid cookie line: expires must be a valid non-negative integer")
	}

	name := strings.TrimSpace(fields[5])
	if name == "" {
		return errors.New("invalid cookie line: empty cookie name")
	}

	// fields[6] is the cookie value, which can be empty or non-empty
	return nil
}

// ValidateCookieFile validates that data is a valid Netscape/Mozilla format cookie file.
func ValidateCookieFile(data []byte) error {
	_, err := NormalizeCookieFile(data)
	return err
}
