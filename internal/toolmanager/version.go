package toolmanager

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ytdlpVersionPattern accepts stable yt-dlp versions: YYYY.MM.DD with an optional numeric patch component.
var ytdlpVersionPattern = regexp.MustCompile(`^(\d{4})\.(\d{2})\.(\d{2})(?:\.(\d{1,6}))?$`)

// ytdlpVersion is a parsed stable yt-dlp version compared component-wise (a missing patch is 0).
type ytdlpVersion [4]int

func parseYtdlpVersion(s string) (ytdlpVersion, error) {
	var v ytdlpVersion
	m := ytdlpVersionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return v, fmt.Errorf("%w: %q is not a stable yt-dlp version", ErrInvalidRelease, s)
	}
	for i := 1; i <= 4; i++ {
		if m[i] == "" {
			continue
		}
		n, err := strconv.Atoi(m[i])
		if err != nil {
			return v, fmt.Errorf("%w: %q is not a stable yt-dlp version", ErrInvalidRelease, s)
		}
		v[i-1] = n
	}
	if v[1] < 1 || v[1] > 12 || v[2] < 1 || v[2] > 31 {
		return v, fmt.Errorf("%w: %q has an invalid date component", ErrInvalidRelease, s)
	}
	return v, nil
}

// compare returns -1, 0, or 1 as v is older than, equal to, or newer than o.
func (v ytdlpVersion) compare(o ytdlpVersion) int {
	for i := range v {
		switch {
		case v[i] < o[i]:
			return -1
		case v[i] > o[i]:
			return 1
		}
	}
	return 0
}

// compareYtdlpVersions compares two version strings; both must parse.
func compareYtdlpVersions(a, b string) (int, error) {
	va, err := parseYtdlpVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseYtdlpVersion(b)
	if err != nil {
		return 0, err
	}
	return va.compare(vb), nil
}
