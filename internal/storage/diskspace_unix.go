//go:build !windows

package storage

import (
	"fmt"

	"golang.org/x/sys/unix"
)

var _ IFreeSpaceProvider = (*OSFreeSpaceProvider)(nil)

// OSFreeSpaceProvider provides OS disk space statistics.
type OSFreeSpaceProvider struct{}

// NewOSFreeSpaceProvider creates a new OSFreeSpaceProvider.
func NewOSFreeSpaceProvider() *OSFreeSpaceProvider {
	return &OSFreeSpaceProvider{}
}

// FreeBytes returns the available free space in bytes for the specified path on Unix.
func (p *OSFreeSpaceProvider) FreeBytes(pathStr string) (int64, error) {
	if pathStr == "" {
		return 0, fmt.Errorf("empty path provided for free space check")
	}

	var stat unix.Statfs_t
	if err := unix.Statfs(pathStr, &stat); err != nil {
		return 0, fmt.Errorf("statfs failed for path %s: %w", pathStr, err)
	}

	// Available blocks * block size
	freeBytes := int64(stat.Bavail) * int64(stat.Bsize)
	return freeBytes, nil
}
