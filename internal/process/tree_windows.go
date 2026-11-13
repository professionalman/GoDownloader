//go:build windows

package process

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
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
	// Owned tools are console programs; the GUI-subsystem desktop host has no console for them to share, so run them
	// without a console window (their own console children inherit the hidden console).
	HideConsoleWindow(cmd)
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
			terminateJobAndWait(t.jobHandle, jobDrainTimeout)
			_ = windows.CloseHandle(t.jobHandle)
			t.jobHandle = 0
		}
	})
	return nil
}

// close releases the job after the root process exited. Descendants the root left running (for example the
// re-executed child of a PyInstaller onefile yt-dlp.exe) are terminated and awaited before the handle is closed,
// so when close returns no process of this tree remains — callers may then release the tool binary for updates.
// KILL_ON_JOB_CLOSE remains the backstop if the bounded wait expires.
func (t *windowsProcessTree) close() error {
	var closeErr error
	t.closeOnce.Do(func() {
		if t.jobHandle != 0 && t.jobHandle != windows.InvalidHandle {
			if jobActiveProcesses(t.jobHandle) > 0 {
				terminateJobAndWait(t.jobHandle, jobDrainTimeout)
			}
			closeErr = windows.CloseHandle(t.jobHandle)
			t.jobHandle = 0
		}
	})
	return closeErr
}

// jobDrainTimeout bounds how long tree teardown waits for terminated processes to leave the job.
const jobDrainTimeout = 5 * time.Second

// jobObjectBasicAccountingInformation mirrors JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type jobObjectBasicAccountingInformation struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

// jobActiveProcesses returns the number of live processes in the job, or 0 if it cannot be queried.
func jobActiveProcesses(job windows.Handle) uint32 {
	var info jobObjectBasicAccountingInformation
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil); err != nil {
		return 0
	}
	return info.ActiveProcesses
}

// maxJobProcessIDs bounds the job's process-ID list query (an analysis or download tree has a handful of members).
const maxJobProcessIDs = 256

// jobObjectBasicProcessIDList mirrors JOBOBJECT_BASIC_PROCESS_ID_LIST with a fixed-capacity ID array.
type jobObjectBasicProcessIDList struct {
	NumberOfAssignedProcesses uint32
	NumberOfProcessIdsInList  uint32
	ProcessIDList             [maxJobProcessIDs]uintptr
}

// openJobMembers opens SYNCHRONIZE handles to the processes currently in the job. Each handle is kept only if
// IsProcessInJob confirms membership, so a PID that exited and was reused by an unrelated process is never awaited.
// Nothing outside the job is touched: no process is enumerated system-wide and none is terminated here.
func openJobMembers(job windows.Handle) []windows.Handle {
	var list jobObjectBasicProcessIDList
	if err := windows.QueryInformationJobObject(job, windows.JobObjectBasicProcessIdList,
		uintptr(unsafe.Pointer(&list)), uint32(unsafe.Sizeof(list)), nil); err != nil && err != windows.ERROR_MORE_DATA {
		return nil
	}
	n := int(list.NumberOfProcessIdsInList)
	if n > maxJobProcessIDs {
		n = maxJobProcessIDs
	}
	handles := make([]windows.Handle, 0, n)
	for i := 0; i < n; i++ {
		h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(list.ProcessIDList[i]))
		if err != nil {
			continue // already gone
		}
		if in, err := isProcessInJob(h, job); err != nil || !in {
			_ = windows.CloseHandle(h)
			continue
		}
		handles = append(handles, h)
	}
	return handles
}

// terminateJobAndWait terminates every process in the job and waits, bounded by timeout, until each has actually
// exited (its process object is signaled) and the job reports no active process.
func terminateJobAndWait(job windows.Handle, timeout time.Duration) {
	members := openJobMembers(job)
	defer func() {
		for _, h := range members {
			_ = windows.CloseHandle(h)
		}
	}()
	_ = windows.TerminateJobObject(job, 1)
	deadline := time.Now().Add(timeout)
	for _, h := range members {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		_, _ = windows.WaitForSingleObject(h, uint32(remaining/time.Millisecond))
	}
	for jobActiveProcesses(job) > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
}
