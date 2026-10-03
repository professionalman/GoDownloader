package buildinfo

import (
	"testing"
)

func TestSanitizeRevision(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty", "", "unknown"},
		{"valid 7 chars", "6ab03f2", "6ab03f2"},
		{"valid 40 chars uppercase", "6AB03F2055FF7D18D14178D4B37ABE0542A75500", "6ab03f2055ff7d18d14178d4b37abe0542a75500"},
		{"invalid chars non-hex", "6ab03f2z", "unknown"},
		{"too short", "123456", "unknown"},
		{"path injection", "/path/to/repo/6ab03f2", "unknown"},
		{"command injection", "6ab03f2; rm -rf /", "unknown"},
		{"newlines", "6ab03f2\n", "6ab03f2"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeRevision(tc.input)
			if got != tc.expected {
				t.Errorf("sanitizeRevision(%q) = %q; want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestCurrent_Truthful(t *testing.T) {
	info := Current()
	if info.Version == "" {
		t.Errorf("expected non-empty Version")
	}
	// Verify that version does not guess unproven version string
	if info.Version == "0.9.1" {
		t.Errorf("Version must not be hardcoded to 0.9.1")
	}
	// Commit should be valid hex or unknown
	if info.Commit != "unknown" && !hexRevisionRegex.MatchString(info.Commit) && !info.Dirty {
		t.Errorf("Commit %q does not match expected format", info.Commit)
	}
}
