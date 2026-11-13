package main

import (
	"context"

	"downloader/internal/toolmanager"
)

// =========================================================================
// MANAGED TOOLS DOMAIN (native IPC; no localhost transport)
// =========================================================================

func toolIPCError(err error) error {
	code := toolmanager.AcquisitionErrorCode(err)
	return makeIPCError(code, toolmanager.AcquisitionErrorMessage(code))
}

func (s *DesktopService) toolManager() (*toolmanager.Manager, error) {
	if s.app == nil || s.app.ToolManager() == nil {
		return nil, makeIPCError("TOOLS_UNAVAILABLE", "managed tool service is not available")
	}
	return s.app.ToolManager(), nil
}

// GetYtdlpToolStatus returns the path-free managed yt-dlp status.
func (s *DesktopService) GetYtdlpToolStatus() (*toolmanager.ManagedToolStatus, error) {
	tm, err := s.toolManager()
	if err != nil {
		return nil, err
	}
	st := tm.YtdlpStatus(context.Background())
	return &st, nil
}

// GetMediaDependencyStatus returns the path-free status of yt-dlp, FFmpeg, FFprobe and the JavaScript runtime with
// per-workflow readiness. Read-only: nothing is installed or changed.
func (s *DesktopService) GetMediaDependencyStatus() (*toolmanager.MediaDependencyStatus, error) {
	tm, err := s.toolManager()
	if err != nil {
		return nil, err
	}
	st := tm.MediaDependencies(context.Background())
	return &st, nil
}

// InstallYtdlp installs the latest authenticated stable yt-dlp. Takes no parameters by design.
func (s *DesktopService) InstallYtdlp() (*toolmanager.AcquisitionResult, error) {
	tm, err := s.toolManager()
	if err != nil {
		return nil, err
	}
	res, err := tm.InstallYtdlp(context.Background())
	if err != nil {
		return nil, toolIPCError(err)
	}
	return res, nil
}

// UpdateYtdlp updates the managed yt-dlp to the latest authenticated stable release.
func (s *DesktopService) UpdateYtdlp() (*toolmanager.AcquisitionResult, error) {
	tm, err := s.toolManager()
	if err != nil {
		return nil, err
	}
	res, err := tm.UpdateYtdlp(context.Background())
	if err != nil {
		return nil, toolIPCError(err)
	}
	return res, nil
}

// CheckYtdlpUpdate reports whether a newer stable yt-dlp release exists.
func (s *DesktopService) CheckYtdlpUpdate() (*toolmanager.UpdateCheckResult, error) {
	tm, err := s.toolManager()
	if err != nil {
		return nil, err
	}
	res, err := tm.CheckYtdlpUpdate(context.Background())
	if err != nil {
		return nil, toolIPCError(err)
	}
	return res, nil
}
