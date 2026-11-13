package buildinfo

import (
	"runtime"
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

func withInjected(t *testing.T, version, commit, modified, configuration string) {
	t.Helper()
	ov, oc, om, ocfg := Version, Commit, Modified, Configuration
	Version, Commit, Modified, Configuration = version, commit, modified, configuration
	t.Cleanup(func() { Version, Commit, Modified, Configuration = ov, oc, om, ocfg })
}

// Release builds inject the source commit, its working-tree state and the build configuration via -ldflags.
func TestCurrent_InjectedReleaseProvenance(t *testing.T) {
	withInjected(t, "0.9.1", "E50F7EAB106147015AE945DA4E6CE733D7AABCE1", "false", "production")
	info := Current()
	if info.Version != "0.9.1" || info.Commit != "e50f7eab106147015ae945da4e6ce733d7aabce1" || info.Dirty {
		t.Fatalf("unexpected identity: %+v", info)
	}
	if info.CommitSource != "injected" || info.Configuration != "production" {
		t.Fatalf("unexpected provenance: %+v", info)
	}
	if info.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("platform = %q", info.Platform)
	}
}

func TestCurrent_InjectedModifiedTreeIsMarkedDirty(t *testing.T) {
	withInjected(t, "0.9.1", "e50f7eab106147015ae945da4e6ce733d7aabce1", "true", "production")
	info := Current()
	if !info.Dirty || info.Commit != "e50f7eab106147015ae945da4e6ce733d7aabce1-dirty" {
		t.Fatalf("modified build not marked dirty: %+v", info)
	}
}

func TestCurrent_InvalidInjectedCommitIsUnknown(t *testing.T) {
	withInjected(t, "0.9.1", "$(git rev-parse HEAD)", "true", "")
	info := Current()
	if info.Commit != "unknown" || info.Dirty || info.CommitSource != "none" {
		t.Fatalf("invalid commit must not be reported: %+v", info)
	}
	if info.Configuration != "development" {
		t.Fatalf("empty configuration must report development, got %q", info.Configuration)
	}
}
