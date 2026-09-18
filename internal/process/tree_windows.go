//go:build windows

package process

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procIsProcessInJob = kernel32.NewProc("IsProcessInJob")
)

type windowsProcessTree struct {
	jobHandle windows.Handle
	closeOnce sync.Once
}

func newProcessTree() (processTree, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("set job object limit kill-on-close: %w", err)
	}

	return &windowsProcessTree{
		jobHandle: job,
	}, nil
}

func (t *windowsProcessTree) setup(cmd *exec.Cmd) error {
	if cmd == nil {
		return fmt.Errorf("cmd is nil")
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	// Create the process suspended so that zero instructions of application code
	// or child-spawning logic execute prior to Job Object containment.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	return nil
}

func (t *windowsProcessTree) attach(ctx context.Context, cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process is nil")
	}

	pid := uint32(cmd.Process.Pid)
	hProc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("open process handle: %w", err)
	}
	defer windows.CloseHandle(hProc)

	if err := windows.AssignProcessToJobObject(t.jobHandle, hProc); err != nil {
		return fmt.Errorf("assign process to job object: %w", err)
	}

	inJob, err := isProcessInJob(hProc, t.jobHandle)
	if err != nil {
		return fmt.Errorf("verify process in job: %w", err)
	}
	if !inJob {
		return fmt.Errorf("process %d not found in job object after assignment", pid)
	}

	// Phase B: If context was cancelled while the process was created suspended,
	// abort before resuming execution so zero application instructions execute.
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}

	if err := resumeProcess(pid); err != nil {
		return fmt.Errorf("resume suspended process %d: %w", pid, err)
	}

	return nil
}

func isProcessInJob(hProcess, hJob windows.Handle) (bool, error) {
	var result int32
	r1, _, err := procIsProcessInJob.Call(uintptr(hProcess), uintptr(hJob), uintptr(unsafe.Pointer(&result)))
	if r1 == 0 {
		return false, fmt.Errorf("IsProcessInJob call failed: %w", err)
	}
	return result != 0, nil
}

func resumeProcess(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("create thread snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)

	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	if err := windows.Thread32First(snap, &te); err != nil {
		return fmt.Errorf("thread32first: %w", err)
	}

	found := false
	for {
		if te.OwnerProcessID == pid {
			hThread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
			if err != nil {
				return fmt.Errorf("open thread %d: %w", te.ThreadID, err)
			}
			_, err = windows.ResumeThread(hThread)
			_ = windows.CloseHandle(hThread)
			if err != nil {
				return fmt.Errorf("resume thread %d: %w", te.ThreadID, err)
			}
			found = true
		}
		if err := windows.Thread32Next(snap, &te); err != nil {
			break
		}
	}
	if !found {
		return fmt.Errorf("no thread found for pid %d", pid)
	}
	return nil
}

func (t *windowsProcessTree) terminate() error {
	t.closeOnce.Do(func() {
		if t.jobHandle != 0 && t.jobHandle != windows.InvalidHandle {
			_ = windows.TerminateJobObject(t.jobHandle, 1)
			_ = windows.CloseHandle(t.jobHandle)
			t.jobHandle = 0
		}
	})
	return nil
}

func (t *windowsProcessTree) close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		if t.jobHandle != 0 && t.jobHandle != windows.InvalidHandle {
			closeErr = windows.CloseHandle(t.jobHandle)
			t.jobHandle = 0
		}
	})
	return closeErr
}
