//go:build !windows

package process

import (
	"context"
	"os/exec"
	"syscall"
)

type unixProcessTree struct {
	cmd *exec.Cmd
}

func newProcessTree() (processTree, error) {
	return &unixProcessTree{}, nil
}

func (t *unixProcessTree) setup(cmd *exec.Cmd) error {
	t.cmd = cmd
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return nil
}

func (t *unixProcessTree) attach(ctx context.Context, cmd *exec.Cmd) error {
	t.cmd = cmd
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func (t *unixProcessTree) terminate() error {
	if t.cmd != nil && t.cmd.Process != nil && t.cmd.Process.Pid > 0 {
		_ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL)
	}
	return nil
}

func (t *unixProcessTree) close() error {
	return nil
}
