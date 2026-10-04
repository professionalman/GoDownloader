//go:build !windows

package runtimeguard

import (
	"errors"
	"os"
	"syscall"
)

type guardImpl struct {
	file *os.File
}

func acquireLock(lockPath string) (guardImpl, error) {
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return guardImpl{}, err
	}

	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return guardImpl{}, ErrRuntimeAlreadyOwned
		}
		return guardImpl{}, err
	}

	return guardImpl{file: f}, nil
}

func (g guardImpl) close() error {
	if g.file != nil {
		_ = syscall.Flock(int(g.file.Fd()), syscall.LOCK_UN)
		return g.file.Close()
	}
	return nil
}
