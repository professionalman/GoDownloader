package main

import (
	"testing"

	"downloader/internal/buildinfo"
)

// The UI version comes from the executable's embedded build identity: whatever version is injected at link time
// (-X downloader/internal/buildinfo.Version=…) is what the desktop service reports.
func TestGetAppVersion_ReportsEmbeddedBuildVersion(t *testing.T) {
	orig := buildinfo.Version
	t.Cleanup(func() { buildinfo.Version = orig })

	svc := &DesktopService{}
	for _, injected := range []string{"0.9.1", "1.2.3"} {
		buildinfo.Version = injected
		if got := svc.GetAppVersion(); got != injected {
			t.Fatalf("GetAppVersion() = %q, want the injected build version %q", got, injected)
		}
		if got, want := svc.GetAppVersion(), buildinfo.Current().Version; got != want {
			t.Fatalf("GetAppVersion() = %q, differs from buildinfo.Current().Version %q", got, want)
		}
	}
}
