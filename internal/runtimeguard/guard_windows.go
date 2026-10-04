//go:build windows

package runtimeguard

import (
	"errors"

	"golang.org/x/sys/windows"
)

type guardImpl struct {
	handle windows.Handle
}

func acquireLock(lockPath string) (guardImpl, error) {
	pathPtr, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		return guardImpl{}, err
	}

	// OPEN_ALWAYS: opens the file if it exists, or creates it if it does not.
	// shareMode = 0: EXCLUSIVE ownership. No other process may open the file for read/write/delete.
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return guardImpl{}, ErrRuntimeAlreadyOwned
		}
		return guardImpl{}, err
	}

	return guardImpl{handle: handle}, nil
}

func (g guardImpl) close() error {
	if g.handle != windows.InvalidHandle && g.handle != 0 {
		return windows.CloseHandle(g.handle)
	}
	return nil
}
