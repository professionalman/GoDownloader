package ytdlp

import (
	"strings"
	"testing"
)

func TestMaintenancePendingTracksExclusiveGate(t *testing.T) {
	e := NewEngine("", "")
	if e.MaintenancePending() {
		t.Fatal("fresh engine reports maintenance")
	}
	release, err := e.TryExclusive()
	if err != nil {
		t.Fatal(err)
	}
	if !e.MaintenancePending() {
		t.Fatal("exclusive maintenance not reported")
	}
	if _, err := e.AcquireUse(); err == nil {
		t.Fatal("shared use allowed during maintenance")
	}
	release()
	if e.MaintenancePending() {
		t.Fatal("maintenance still reported after release")
	}
}

func TestJSRuntimeHint(t *testing.T) {
	hinted := []string{
		"WARNING: [youtube] No supported JavaScript runtime could be found.\nERROR: [youtube] abc: Requested format is not available",
		"ERROR: [youtube] abc: n challenge solving failed: Some formats may be missing",
		"ERROR: Signature solving failed",
		"ERROR: install yt-dlp-ejs or pass --remote-components",
	}
	for _, stderr := range hinted {
		msg := cleanError(stderr)
		if !strings.Contains(msg, "JavaScript runtime") || !strings.Contains(msg, "does not install") {
			t.Errorf("expected runtime hint for %q, got %q", stderr, msg)
		}
	}
	plain := "ERROR: [generic] Unsupported URL: https://example.com"
	if msg := cleanError(plain); strings.Contains(msg, "JavaScript") || msg != "[generic] Unsupported URL: https://example.com" {
		t.Errorf("unrelated error altered: %q", msg)
	}
}
