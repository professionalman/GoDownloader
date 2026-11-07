package toolmanager

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// ytdlpWindowsAsset is the only executable asset GoDownloader acquires (Windows amd64 standalone build).
const ytdlpWindowsAsset = "yt-dlp.exe"

// manifestLine matches one sha256sum-format line: 64 hex digits, a separator (" " text or "*" binary mode),
// and a bare file name. File names may not contain path separators.
var manifestLine = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *]([A-Za-z0-9][A-Za-z0-9._+-]*)$`)

// parseSHA256Manifest extracts the digest for asset from an already authenticated SHA2-256SUMS manifest.
// The whole manifest must be well formed: any malformed line, traversal-like name, or oversized input
// rejects it, and the asset must appear exactly once. Matching is exact, never by substring.
func parseSHA256Manifest(manifest []byte, asset string) (string, error) {
	if len(manifest) == 0 || len(manifest) > maxManifestBytes {
		return "", fmt.Errorf("%w: manifest size %d outside accepted bounds", ErrInvalidRelease, len(manifest))
	}
	if bytes.IndexByte(manifest, 0) >= 0 {
		return "", fmt.Errorf("%w: manifest contains binary content", ErrInvalidRelease)
	}

	digest := ""
	matches := 0
	for i, raw := range strings.Split(string(manifest), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			continue
		}
		m := manifestLine.FindStringSubmatch(line)
		if m == nil {
			return "", fmt.Errorf("%w: malformed manifest line %d", ErrInvalidRelease, i+1)
		}
		name := m[2]
		if strings.Contains(name, "..") {
			return "", fmt.Errorf("%w: unsafe file name on manifest line %d", ErrInvalidRelease, i+1)
		}
		if name == asset {
			matches++
			digest = strings.ToLower(m[1])
		}
	}
	switch {
	case matches == 0:
		return "", fmt.Errorf("%w: manifest has no digest for %s", ErrInvalidRelease, asset)
	case matches > 1:
		return "", fmt.Errorf("%w: manifest lists %s more than once", ErrInvalidRelease, asset)
	}
	return digest, nil
}
