package toolmanager

import (
	"errors"
	"strings"
	"testing"
)

func digestOf(c string) string { return strings.Repeat(c, 64) }

func TestManifest_ExactDigestExtraction(t *testing.T) {
	m := []byte(digestOf("a") + "  yt-dlp\n" +
		digestOf("b") + "  yt-dlp.exe\n" +
		digestOf("c") + "  yt-dlp_x86.exe\n" +
		digestOf("d") + " *yt-dlp.exe.zip\n")
	got, err := parseSHA256Manifest(m, ytdlpWindowsAsset)
	if err != nil {
		t.Fatal(err)
	}
	if got != digestOf("b") {
		t.Fatalf("got %s, want exact yt-dlp.exe digest", got)
	}
}

func TestManifest_UppercaseDigestNormalised(t *testing.T) {
	got, err := parseSHA256Manifest([]byte(strings.ToUpper(digestOf("e"))+"  yt-dlp.exe\r\n"), ytdlpWindowsAsset)
	if err != nil || got != digestOf("e") {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestManifest_Rejections(t *testing.T) {
	cases := map[string]string{
		"missing asset":    digestOf("a") + "  yt-dlp\n",
		"duplicate asset":  digestOf("a") + "  yt-dlp.exe\n" + digestOf("b") + "  yt-dlp.exe\n",
		"short digest":     strings.Repeat("a", 63) + "  yt-dlp.exe\n",
		"long digest":      strings.Repeat("a", 65) + "  yt-dlp.exe\n",
		"non-hex digest":   strings.Repeat("g", 64) + "  yt-dlp.exe\n",
		"path traversal":   digestOf("a") + "  ../yt-dlp.exe\n",
		"dot-dot name":     digestOf("a") + "  yt-dlp..exe\n",
		"path separator":   digestOf("a") + "  dir/yt-dlp.exe\n",
		"windows path":     digestOf("a") + `  dir\yt-dlp.exe` + "\n",
		"unsupported line": "# comment\n" + digestOf("a") + "  yt-dlp.exe\n",
		"single space sep": digestOf("a") + " yt-dlp.exe\n",
		"trailing junk":    digestOf("a") + "  yt-dlp.exe extra\n",
		"binary content":   digestOf("a") + "  yt-dlp.exe\n\x00",
		"empty":            "",
	}
	for name, manifest := range cases {
		if _, err := parseSHA256Manifest([]byte(manifest), ytdlpWindowsAsset); !errors.Is(err, ErrInvalidRelease) {
			t.Errorf("%s: expected ErrInvalidRelease, got %v", name, err)
		}
	}
	oversized := []byte(strings.Repeat(digestOf("a")+"  filler.bin\n", maxManifestBytes/70+2))
	if _, err := parseSHA256Manifest(oversized, ytdlpWindowsAsset); !errors.Is(err, ErrInvalidRelease) {
		t.Errorf("oversized manifest: %v", err)
	}
}

func TestVersion_ComponentWiseComparison(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026.10.26", "2026.10.26", 0},
		{"2026.10.26", "2026.10.26.0", 0},
		{"2026.10.26.1", "2026.10.26", 1},
		{"2026.10.26", "2026.10.27", -1},
		{"2026.11.01", "2026.10.31", 1},
		{"2027.01.01", "2026.12.31", 1},
		{"2026.10.26.2", "2026.10.26.10", -1}, // numeric, not lexical
		{"2026.09.30", "2026.10.01", -1},
	}
	for _, tc := range cases {
		got, err := compareYtdlpVersions(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Errorf("compare(%s, %s) = %d, %v; want %d", tc.a, tc.b, got, err, tc.want)
		}
	}
}

func TestVersion_RejectsNonStableVersions(t *testing.T) {
	for _, v := range []string{"", "2026.10", "2026.13.01", "2026.00.10", "2026.10.32", "26.10.26",
		"2026.10.26-nightly", "2026.10.26.1.2", "v2026.10.26", "2026.10.26.abc", "nightly@2026.10.26"} {
		if _, err := parseYtdlpVersion(v); !errors.Is(err, ErrInvalidRelease) {
			t.Errorf("%q: expected ErrInvalidRelease, got %v", v, err)
		}
	}
	if _, err := parseYtdlpVersion(" 2026.10.26\r\n"); err != nil {
		t.Errorf("probe output with whitespace should parse: %v", err)
	}
}
