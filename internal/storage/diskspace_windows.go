//go:build windows

package storage

import (
	"fmt"

	"golang.org/x/sys/windows"
)

var _ IFreeSpaceProvider = (*OSFreeSpaceProvider)(nil)

// OSFreeSpaceProvider provides OS disk space statistics.
type OSFreeSpaceProvider struct{}

// NewOSFreeSpaceProvider creates a new OSFreeSpaceProvider.
func NewOSFreeSpaceProvider() *OSFreeSpaceProvider {
	return &OSFreeSpaceProvider{}
}

// FreeBytes returns the available free space in bytes for the specified path on Windows.
func (p *OSFreeSpaceProvider) FreeBytes(pathStr string) (int64, error) {
	if pathStr == "" {
		return 0, fmt.Errorf("empty path provided for free space check")
	}

	ptr, err := windows.UTF16PtrFromString(pathStr)
	if err != nil {
		return 0, fmt.Errorf("invalid path for windows: %w", err)
	}

	var freeBytesAvailable, totalBytes, totalFreeBytes uint64
	err = windows.GetDiskFreeSpaceEx(ptr, &freeBytesAvailable, &totalBytes, &totalFreeBytes)
	if err != nil {
		return 0, fmt.Errorf("GetDiskFreeSpaceEx failed for path %s: %w", pathStr, err)
	}

	return int64(freeBytesAvailable), nil
}
