package main

import "downloader/internal/buildinfo"

// GetAppVersion returns the version embedded in the running executable (the same build identity that
// --print-build-info and the support bundle report), so the UI never carries its own version string.
// Development builds report "(devel)".
func (s *DesktopService) GetAppVersion() string {
	return buildinfo.Current().Version
}
