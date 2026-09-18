package process

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestHelperProcess is the subprocess helper executed by tests.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return
	}

	switch args[0] {
	case "sleep":
		time.Sleep(30 * time.Second)
	case "exit-code":
		if len(args) > 1 {
			code, _ := strconv.Atoi(args[1])
			os.Exit(code)
		}
		os.Exit(0)
	case "spawn-grandchild":
		// Spawns grandchild and prints grandchild's PID to stdout
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "sleep")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start grandchild: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("GRANDCHILD_PID:%d\n", cmd.Process.Pid)
		// Now sleep so child remains running alongside grandchild
		time.Sleep(30 * time.Second)
	case "early-grandchild-orphan":
		// Spawns grandchild, prints grandchild's PID to stdout, and exits immediately
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess", "--", "sleep")
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to start grandchild: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("GRANDCHILD_PID:%d\n", cmd.Process.Pid)
		os.Exit(0)
	}
}

func helperCmd(args ...string) *exec.Cmd {
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, args...)
	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")
	return cmd
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
		if err != nil {
			return false
		}
		defer windows.CloseHandle(h)
		event, err := windows.WaitForSingleObject(h, 0)
		if err != nil {
			return false
		}
		return event != windows.WAIT_OBJECT_0
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func waitProcessDeath(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !isProcessAlive(pid)
}

// ============================
// Test A: Owned simple child starts and exits normally
// ============================
func TestSupervisor_A_SimpleChildStartsAndExitsNormally(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("exit-code", "0")
	spec := ProcessSpec{
		ID:      "proc-a",
		Tool:    "test-tool",
		Purpose: "exit-normal",
	}

	proc, err := s.StartOwned(context.Background(), cmd, spec)
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	if proc.PID() <= 0 {
		t.Errorf("expected valid PID, got %d", proc.PID())
	}
	if proc.State() != StateRunning {
		t.Errorf("expected state running, got %s", proc.State())
	}

	err = proc.Wait()
	if err != nil {
		t.Fatalf("expected clean exit, got %v", err)
	}

	if proc.State() != StateExited {
		t.Errorf("expected state exited, got %s", proc.State())
	}
	if proc.WasTerminated() {
		t.Errorf("expected WasTerminated false")
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test B: Cancellation terminates child
// ============================
func TestSupervisor_B_CancellationTerminatesChild(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cmd := helperCmd("sleep")
	spec := ProcessSpec{
		ID:      "proc-b",
		Tool:    "test-tool",
		Purpose: "cancel-test",
	}

	proc, err := s.StartOwned(ctx, cmd, spec)
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	pid := proc.PID()
	if !isProcessAlive(pid) {
		t.Fatalf("expected child %d to be alive", pid)
	}

	// Cancel context -> triggers cmd.Cancel which invokes proc.Terminate()
	cancel()

	_ = proc.Wait()

	if !waitProcessDeath(pid, 2*time.Second) {
		t.Errorf("expected process %d to be dead after cancel", pid)
	}
	if !proc.WasTerminated() {
		t.Errorf("expected WasTerminated to be true")
	}
}

// ============================
// Test C: Owned child spawning grandchild: termination kills BOTH parent and descendant
// ============================
func TestSupervisor_C_GrandchildTreeTermination(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Job Object tree containment test")
	}

	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("spawn-grandchild")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}

	spec := ProcessSpec{
		ID:      "proc-c",
		Tool:    "test-tool",
		Purpose: "tree-test",
	}

	proc, err := s.StartOwned(context.Background(), cmd, spec)
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	childPID := proc.PID()

	// Read grandchild PID from stdout
	scanner := bufio.NewScanner(stdout)
	var grandchildPID int
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "GRANDCHILD_PID:") {
			pidStr := strings.TrimPrefix(line, "GRANDCHILD_PID:")
			grandchildPID, _ = strconv.Atoi(strings.TrimSpace(pidStr))
			break
		}
	}

	if grandchildPID <= 0 {
		t.Fatalf("failed to obtain grandchild PID")
	}

	// Verify both child and grandchild are running
	if !isProcessAlive(childPID) {
		t.Fatalf("expected child %d to be alive", childPID)
	}
	if !isProcessAlive(grandchildPID) {
		t.Fatalf("expected grandchild %d to be alive", grandchildPID)
	}

	// Terminate parent process tree
	if err := proc.Terminate(); err != nil {
		t.Fatalf("Terminate failed: %v", err)
	}

	_ = proc.Wait()

	// Verify BOTH child and grandchild are terminated
	if !waitProcessDeath(childPID, 2*time.Second) {
		t.Errorf("child %d still alive after tree termination", childPID)
	}
	if !waitProcessDeath(grandchildPID, 2*time.Second) {
		t.Errorf("grandchild %d still alive after tree termination", grandchildPID)
	}
}

// ============================
// Test D: Supervisor shutdown terminates all owned process trees
// ============================
func TestSupervisor_D_ShutdownTerminatesAllTrees(t *testing.T) {
	s := NewSupervisor()

	cmd1 := helperCmd("sleep")
	cmd2 := helperCmd("sleep")

	proc1, err := s.StartOwned(context.Background(), cmd1, ProcessSpec{ID: "proc-d1", Tool: "t1"})
	if err != nil {
		t.Fatalf("start cmd1 failed: %v", err)
	}
	proc2, err := s.StartOwned(context.Background(), cmd2, ProcessSpec{ID: "proc-d2", Tool: "t2"})
	if err != nil {
		t.Fatalf("start cmd2 failed: %v", err)
	}

	pid1 := proc1.PID()
	pid2 := proc2.PID()

	if !isProcessAlive(pid1) || !isProcessAlive(pid2) {
		t.Fatalf("expected both processes to be alive")
	}

	// Background reaper goroutines to simulate active callers calling Wait
	go proc1.Wait()
	go proc2.Wait()

	// Shutdown supervisor
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()

	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if !waitProcessDeath(pid1, 2*time.Second) {
		t.Errorf("proc1 %d still alive after shutdown", pid1)
	}
	if !waitProcessDeath(pid2, 2*time.Second) {
		t.Errorf("proc2 %d still alive after shutdown", pid2)
	}

	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test E: One job/process termination does not affect another
// ============================
func TestSupervisor_E_ProcessIsolation(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd1 := helperCmd("sleep")
	cmd2 := helperCmd("sleep")

	proc1, err := s.StartOwned(context.Background(), cmd1, ProcessSpec{ID: "proc-e1", Tool: "t1"})
	if err != nil {
		t.Fatalf("start cmd1 failed: %v", err)
	}
	proc2, err := s.StartOwned(context.Background(), cmd2, ProcessSpec{ID: "proc-e2", Tool: "t2"})
	if err != nil {
		t.Fatalf("start cmd2 failed: %v", err)
	}

	pid1 := proc1.PID()
	pid2 := proc2.PID()

	// Terminate ONLY proc1
	if err := proc1.Terminate(); err != nil {
		t.Fatalf("terminate proc1 failed: %v", err)
	}
	_ = proc1.Wait()

	if !waitProcessDeath(pid1, 2*time.Second) {
		t.Errorf("proc1 %d should be dead", pid1)
	}

	// Verify proc2 remains alive and unaffected
	if !isProcessAlive(pid2) {
		t.Errorf("proc2 %d was killed when proc1 was terminated!", pid2)
	}

	// Clean up proc2
	_ = proc2.Terminate()
	_ = proc2.Wait()
}

// ============================
// Test F: Already-exited process termination is harmless/idempotent
// ============================
func TestSupervisor_F_AlreadyExitedTerminationIsIdempotent(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("exit-code", "0")
	proc, err := s.StartOwned(context.Background(), cmd, ProcessSpec{ID: "proc-f", Tool: "t"})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Wait for normal exit
	_ = proc.Wait()

	// Calling Terminate on already exited process must not error or panic
	for i := 0; i < 3; i++ {
		if err := proc.Terminate(); err != nil {
			t.Errorf("iteration %d: Terminate on exited process returned error: %v", i, err)
		}
	}

	// Subsequent calls to Wait return same memoized error
	if err := proc.Wait(); err != nil {
		t.Errorf("subsequent Wait returned error: %v", err)
	}
}

// ============================
// Test G: Concurrent terminate/wait does not panic/deadlock
// ============================
func TestSupervisor_G_ConcurrentTerminateAndWait(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("sleep")
	proc, err := s.StartOwned(context.Background(), cmd, ProcessSpec{ID: "proc-g", Tool: "t"})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = proc.Terminate()
		}()
		go func() {
			defer wg.Done()
			_ = proc.Wait()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded without deadlock
	case <-time.After(5 * time.Second):
		t.Fatalf("concurrent terminate/wait deadlocked")
	}
}

// ============================
// Test H: Failed Job Object assignment returns explicit error
// ============================
func TestSupervisor_H_FailedAssignmentReturnsExplicitError(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	// Inject a mock tree that fails on attach
	s.treeFactory = func() (processTree, error) {
		return &mockFailingTree{}, nil
	}

	cmd := helperCmd("sleep")
	spec := ProcessSpec{ID: "proc-h", Tool: "t"}
	proc, err := s.StartOwned(context.Background(), cmd, spec)

	if err == nil {
		if proc != nil {
			_ = proc.Terminate()
		}
		t.Fatalf("expected error on failed attachment, got nil")
	}
	if !errors.Is(err, ErrProcessTreeAttach) {
		t.Errorf("expected ErrProcessTreeAttach, got %v", err)
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected 0 active processes on failed attach, got %d", s.ActiveCount())
	}
}

type mockFailingTree struct{}

func (m *mockFailingTree) setup(cmd *exec.Cmd) error { return nil }
func (m *mockFailingTree) attach(ctx context.Context, cmd *exec.Cmd) error {
	return errors.New("simulated access denied in Job Object assignment")
}
func (m *mockFailingTree) terminate() error { return nil }
func (m *mockFailingTree) close() error     { return nil }

// ============================
// Test I: Closing supervisor/job handle provides expected kill-on-close behavior
// ============================
func TestSupervisor_I_KillOnJobClose(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Job Object KILL_ON_JOB_CLOSE test")
	}

	// Create a standalone Job Object with KILL_ON_JOB_CLOSE
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatalf("CreateJobObject failed: %v", err)
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
		t.Fatalf("SetInformationJobObject failed: %v", err)
	}

	cmd := helperCmd("sleep")
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(job)
		t.Fatalf("cmd.Start failed: %v", err)
	}

	pid := cmd.Process.Pid

	hProc, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = windows.CloseHandle(job)
		t.Fatalf("OpenProcess failed: %v", err)
	}

	if err := windows.AssignProcessToJobObject(job, hProc); err != nil {
		windows.CloseHandle(hProc)
		_ = cmd.Process.Kill()
		_ = windows.CloseHandle(job)
		t.Fatalf("AssignProcessToJobObject failed: %v", err)
	}
	windows.CloseHandle(hProc)

	if !isProcessAlive(pid) {
		t.Fatalf("expected process %d to be alive", pid)
	}

	// Close the job object handle: OS must terminate all assigned processes!
	if err := windows.CloseHandle(job); err != nil {
		t.Fatalf("CloseHandle failed: %v", err)
	}

	if !waitProcessDeath(pid, 2*time.Second) {
		_ = cmd.Process.Kill()
		t.Errorf("process %d was not terminated upon closing Job Object handle", pid)
	}
}

// ============================
// Test J & K: Ownership Safety Test (Section 33)
// Start one process THROUGH ProcessSupervisor.
// Start another equivalent process OUTSIDE ProcessSupervisor.
// TerminateAll.
// Supervised process dies; external process remains alive!
// ============================
func TestSupervisor_J_K_OwnershipSafetyTest(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	// 1. Start supervised process
	cmdSupervised := helperCmd("sleep")
	procSupervised, err := s.StartOwned(context.Background(), cmdSupervised, ProcessSpec{
		ID:      "supervised-1",
		Tool:    "test-tool",
		Purpose: "ownership-test",
	})
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}
	supervisedPID := procSupervised.PID()

	// 2. Start external process OUTSIDE ProcessSupervisor
	cmdExternal := helperCmd("sleep")
	if err := cmdExternal.Start(); err != nil {
		t.Fatalf("cmdExternal.Start failed: %v", err)
	}
	externalPID := cmdExternal.Process.Pid
	defer func() {
		// Clean external test process explicitly
		_ = cmdExternal.Process.Kill()
		_ = cmdExternal.Wait()
	}()

	// Verify both are running
	if !isProcessAlive(supervisedPID) {
		t.Fatalf("expected supervised process %d to be alive", supervisedPID)
	}
	if !isProcessAlive(externalPID) {
		t.Fatalf("expected external process %d to be alive", externalPID)
	}

	// 3. TerminateAll on ProcessSupervisor
	if err := s.TerminateAll(); err != nil {
		t.Fatalf("TerminateAll failed: %v", err)
	}
	_ = procSupervised.Wait()

	// 4. Required invariant:
	// Supervised process MUST die
	if !waitProcessDeath(supervisedPID, 2*time.Second) {
		t.Errorf("supervised process %d should have been terminated", supervisedPID)
	}

	// External process MUST remain alive
	if !isProcessAlive(externalPID) {
		t.Errorf("VIOLATION: external process %d was terminated by ProcessSupervisor!", externalPID)
	}
}

// ============================
// Test L: ActiveCount, List, Get, and registration lifecycle
// ============================
func TestSupervisor_L_RegistryLifecycle(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("sleep")
	proc, err := s.StartOwned(context.Background(), cmd, ProcessSpec{
		ID:      "proc-l",
		JobID:   "job-123",
		Tool:    "yt-dlp",
		Purpose: "download",
	})
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	if s.ActiveCount() != 1 {
		t.Errorf("expected active count 1, got %d", s.ActiveCount())
	}

	info := s.Get("proc-l")
	if info == nil {
		t.Fatalf("expected process info, got nil")
	}
	if info.JobID != "job-123" || info.Tool != "yt-dlp" || info.Purpose != "download" {
		t.Errorf("info mismatch: %+v", info)
	}
	if info.State != StateRunning {
		t.Errorf("expected state running, got %s", info.State)
	}

	list := s.List()
	if len(list) != 1 || list[0].ID != "proc-l" {
		t.Errorf("unexpected list: %+v", list)
	}

	// Duplicate ID rejected
	cmdDup := helperCmd("sleep")
	_, err = s.StartOwned(context.Background(), cmdDup, ProcessSpec{ID: "proc-l"})
	if !errors.Is(err, ErrProcessAlreadyRunning) {
		t.Errorf("expected ErrProcessAlreadyRunning, got %v", err)
	}

	// Terminate and verify unregistration
	_ = proc.Terminate()
	_ = proc.Wait()

	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0 after exit, got %d", s.ActiveCount())
	}
	if s.Get("proc-l") != nil {
		t.Errorf("expected Get to return nil after unregistration")
	}
}

// ============================
// Test M: Context cancellation triggers tree termination cleanly
// ============================
func TestSupervisor_M_ContextCancellationTreeKill(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())

	cmd := helperCmd("sleep")
	proc, err := s.StartOwned(ctx, cmd, ProcessSpec{ID: "proc-m", Tool: "test"})
	if err != nil {
		t.Fatalf("start failed: %v", err)
	}
	pid := proc.PID()

	// Cancel context
	cancel()

	_ = proc.Wait()

	if !waitProcessDeath(pid, 2*time.Second) {
		t.Errorf("process %d was not terminated on context cancellation", pid)
	}
	if !proc.WasTerminated() {
		t.Errorf("expected WasTerminated to be true")
	}
}

// ============================
// Test N: Adversarial early spawn containment
// Proves that when a child process immediately spawns a grandchild at startup,
// the grandchild is contained inside the Job Object / process group and cannot escape.
// ============================
func TestProcessSupervisor_AdversarialEarlySpawn(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	// Test 1: Immediate early-grandchild while parent remains running
	t.Run("ConcurrentGrandchild", func(t *testing.T) {
		cmd := helperCmd("spawn-grandchild")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe failed: %v", err)
		}

		spec := ProcessSpec{
			ID:      "adv-early-spawn",
			Tool:    "test-tool",
			Purpose: "adversarial-spawn",
		}

		proc, err := s.StartOwned(context.Background(), cmd, spec)
		if err != nil {
			t.Fatalf("StartOwned failed: %v", err)
		}

		childPID := proc.PID()

		// Read grandchild PID
		scanner := bufio.NewScanner(stdout)
		var grandchildPID int
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "GRANDCHILD_PID:") {
				pidStr := strings.TrimPrefix(line, "GRANDCHILD_PID:")
				grandchildPID, _ = strconv.Atoi(strings.TrimSpace(pidStr))
				break
			}
		}

		if grandchildPID <= 0 {
			t.Fatalf("failed to obtain grandchild PID")
		}

		if !isProcessAlive(childPID) {
			t.Fatalf("expected child %d to be alive", childPID)
		}
		if !isProcessAlive(grandchildPID) {
			t.Fatalf("expected grandchild %d to be alive", grandchildPID)
		}

		// Terminate parent tree
		if err := proc.Terminate(); err != nil {
			t.Fatalf("Terminate failed: %v", err)
		}
		_ = proc.Wait()

		// Both must be dead
		if !waitProcessDeath(childPID, 2*time.Second) {
			t.Errorf("child %d escaped termination", childPID)
		}
		if !waitProcessDeath(grandchildPID, 2*time.Second) {
			t.Errorf("grandchild %d escaped termination", grandchildPID)
		}
	})

	// Test 2: Child immediately spawns grandchild and exits, leaving orphaned grandchild
	t.Run("OrphanedGrandchild", func(t *testing.T) {
		cmd := helperCmd("early-grandchild-orphan")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe failed: %v", err)
		}

		spec := ProcessSpec{
			ID:      "adv-orphan-spawn",
			Tool:    "test-tool",
			Purpose: "adversarial-orphan",
		}

		proc, err := s.StartOwned(context.Background(), cmd, spec)
		if err != nil {
			t.Fatalf("StartOwned failed: %v", err)
		}

		childPID := proc.PID()

		scanner := bufio.NewScanner(stdout)
		var grandchildPID int
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "GRANDCHILD_PID:") {
				pidStr := strings.TrimPrefix(line, "GRANDCHILD_PID:")
				grandchildPID, _ = strconv.Atoi(strings.TrimSpace(pidStr))
				break
			}
		}

		if grandchildPID <= 0 {
			t.Fatalf("failed to obtain grandchild PID")
		}

		// Parent exits on its own.
		// When proc.Wait() completes, it closes the Job Object handle, which triggers
		// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, ensuring even orphaned grandchildren
		// cannot outlive the managed process lifecycle!
		_ = proc.Wait()
		if !waitProcessDeath(childPID, 2*time.Second) {
			t.Fatalf("expected child %d to have exited", childPID)
		}

		// Grandchild MUST be terminated by KILL_ON_JOB_CLOSE and cannot escape
		if !waitProcessDeath(grandchildPID, 2*time.Second) {
			t.Errorf("orphaned grandchild %d escaped Job Object containment!", grandchildPID)
		}
	})
}

// ============================
// Test O: Containment failure cleanup
// Simulates a failure in tree attachment and verifies:
// 1. ErrProcessTreeAttach is returned
// 2. Child process is killed immediately
// 3. Child process is reaped (no zombie)
// 4. Registry active count is 0 and Get returns nil
// ============================
func TestProcessSupervisor_ContainmentFailureCleanup(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	// Force attach failure via mock tree
	s.treeFactory = func() (processTree, error) {
		return &mockFailingTree{}, nil
	}

	cmd := helperCmd("sleep")
	spec := ProcessSpec{
		ID:      "proc-contain-fail",
		Tool:    "yt-dlp",
		Purpose: "download",
	}

	proc, err := s.StartOwned(context.Background(), cmd, spec)
	if err == nil {
		if proc != nil {
			_ = proc.Terminate()
		}
		t.Fatalf("expected ErrProcessTreeAttach, got nil")
	}

	if !errors.Is(err, ErrProcessTreeAttach) {
		t.Errorf("expected ErrProcessTreeAttach, got %v", err)
	}

	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
	if s.Get("proc-contain-fail") != nil {
		t.Errorf("expected Get to return nil")
	}

	// Verify the spawned process was killed and reaped immediately
	if cmd.Process != nil && isProcessAlive(cmd.Process.Pid) {
		t.Errorf("process %d was not killed and reaped upon attach failure", cmd.Process.Pid)
	}
}

// ============================
// Test P: Context cancellation kills entire process tree
// Tests both CommandContext and supervisor context watcher
// ============================
func TestProcessSupervisor_ContextCancellationTreeKill(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	t.Run("CommandContextWithTree", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, "spawn-grandchild")
		cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatalf("StdoutPipe failed: %v", err)
		}

		proc, err := s.StartOwned(ctx, cmd, ProcessSpec{ID: "ctx-tree-1", Tool: "test"})
		if err != nil {
			t.Fatalf("StartOwned failed: %v", err)
		}

		childPID := proc.PID()
		scanner := bufio.NewScanner(stdout)
		var grandchildPID int
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "GRANDCHILD_PID:") {
				pidStr := strings.TrimPrefix(line, "GRANDCHILD_PID:")
				grandchildPID, _ = strconv.Atoi(strings.TrimSpace(pidStr))
				break
			}
		}

		if grandchildPID <= 0 {
			t.Fatalf("failed to obtain grandchild PID")
		}

		// Cancel context
		cancel()

		_ = proc.Wait()

		if !waitProcessDeath(childPID, 2*time.Second) {
			t.Errorf("child %d alive after context cancellation", childPID)
		}
		if !waitProcessDeath(grandchildPID, 2*time.Second) {
			t.Errorf("grandchild %d alive after context cancellation", grandchildPID)
		}
	})
}

// ============================
// Test Q: No double-kill race under heavy concurrent termination
// ============================
func TestProcessSupervisor_NoDoubleKillRace(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	cmd := helperCmd("sleep")
	proc, err := s.StartOwned(context.Background(), cmd, ProcessSpec{ID: "proc-race", Tool: "t"})
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	const callers = 20
	var wg sync.WaitGroup
	wg.Add(callers * 4)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			_ = proc.Terminate()
		}()
		go func() {
			defer wg.Done()
			_ = proc.Wait()
		}()
		go func() {
			defer wg.Done()
			_ = s.Terminate("proc-race")
		}()
		go func() {
			defer wg.Done()
			_ = s.TerminateAll()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly without deadlock or race panic
	case <-time.After(5 * time.Second):
		t.Fatalf("concurrent termination deadlocked")
	}

	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test R: Supervised command does not require exec.CommandContext
// ============================
func TestProcessSupervisor_SupervisedCommandDoesNotRequireCommandContext(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	// Plain exec.Command without CommandContext
	cmd := helperCmd("sleep")
	spec := ProcessSpec{ID: "proc-no-cmd-ctx", Tool: "test"}

	proc, err := s.StartOwned(ctx, cmd, spec)
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	pid := proc.PID()
	if !isProcessAlive(pid) {
		t.Fatalf("expected process %d to be alive", pid)
	}

	// Cancel context: ProcessSupervisor's sole watcher terminates the tree
	cancel()
	_ = proc.Wait()

	if !waitProcessDeath(pid, 2*time.Second) {
		t.Errorf("process %d was not terminated upon context cancellation", pid)
	}
	if !proc.WasTerminated() {
		t.Errorf("expected WasTerminated to be true")
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test S: Already-cancelled context does not launch an OS process
// ============================
func TestProcessSupervisor_AlreadyCancelledContextDoesNotLaunchChild(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled

	cmd := helperCmd("sleep")
	spec := ProcessSpec{ID: "proc-pre-cancelled", Tool: "test"}

	proc, err := s.StartOwned(ctx, cmd, spec)
	if err == nil {
		if proc != nil {
			_ = proc.Terminate()
		}
		t.Fatalf("expected error on already-cancelled context, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// Verify that the command was NEVER started
	if cmd.Process != nil {
		t.Errorf("expected cmd.Process to be nil (never started), got PID %d", cmd.Process.Pid)
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test T: Cancellation during startup leaves zero active process
// ============================
func TestProcessSupervisor_CancellationDuringStartupLeavesZeroActiveProcess(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())

	// Custom tree whose attach cancels context while process is created suspended
	s.treeFactory = func() (processTree, error) {
		realTree, err := newProcessTree()
		if err != nil {
			return nil, err
		}
		return &cancellingTree{
			processTree: realTree,
			onAttach: func() {
				cancel() // Cancel while process is suspended before resumption
			},
		}, nil
	}

	cmd := helperCmd("sleep")
	spec := ProcessSpec{ID: "proc-cancel-startup", Tool: "test"}

	proc, err := s.StartOwned(ctx, cmd, spec)
	if err == nil {
		if proc != nil {
			_ = proc.Terminate()
		}
		t.Fatalf("expected cancellation error during startup, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Verify no process leaked
	if cmd.Process != nil && isProcessAlive(cmd.Process.Pid) {
		t.Errorf("process %d survived startup cancellation", cmd.Process.Pid)
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

type cancellingTree struct {
	processTree
	onAttach func()
}

func (c *cancellingTree) attach(ctx context.Context, cmd *exec.Cmd) error {
	if c.onAttach != nil {
		c.onAttach()
	}
	return c.processTree.attach(ctx, cmd)
}

// ============================
// Test U: Cancellation after resume kills full parent+grandchild tree
// ============================
func TestProcessSupervisor_CancellationAfterResumeKillsFullTree(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	// Plain exec.Command, not CommandContext
	cmd := helperCmd("spawn-grandchild")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe failed: %v", err)
	}

	spec := ProcessSpec{ID: "proc-cancel-tree", Tool: "test"}
	proc, err := s.StartOwned(ctx, cmd, spec)
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	childPID := proc.PID()
	scanner := bufio.NewScanner(stdout)
	var grandchildPID int
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "GRANDCHILD_PID:") {
			pidStr := strings.TrimPrefix(line, "GRANDCHILD_PID:")
			grandchildPID, _ = strconv.Atoi(strings.TrimSpace(pidStr))
			break
		}
	}

	if grandchildPID <= 0 {
		t.Fatalf("failed to read grandchild PID")
	}

	if !isProcessAlive(childPID) {
		t.Fatalf("expected child %d to be alive", childPID)
	}
	if !isProcessAlive(grandchildPID) {
		t.Fatalf("expected grandchild %d to be alive", grandchildPID)
	}

	// Cancel context after resume and full tree is active
	cancel()
	_ = proc.Wait()

	// Both child and grandchild MUST be terminated
	if !waitProcessDeath(childPID, 2*time.Second) {
		t.Errorf("child %d alive after context cancellation", childPID)
	}
	if !waitProcessDeath(grandchildPID, 2*time.Second) {
		t.Errorf("grandchild %d alive after context cancellation", grandchildPID)
	}
	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test V: Concurrent cancel + Wait remains safe
// ============================
func TestProcessSupervisor_ConcurrentCancelAndWait(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := helperCmd("sleep")
	proc, err := s.StartOwned(ctx, cmd, ProcessSpec{ID: "proc-conc-cancel", Tool: "t"})
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	const callers = 20
	var wg sync.WaitGroup
	wg.Add(callers * 3)

	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			cancel()
		}()
		go func() {
			defer wg.Done()
			_ = proc.Wait()
		}()
		go func() {
			defer wg.Done()
			_ = proc.Terminate()
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly without deadlock or race panic
	case <-time.After(5 * time.Second):
		t.Fatalf("concurrent cancel + wait deadlocked")
	}

	if s.ActiveCount() != 0 {
		t.Errorf("expected active count 0, got %d", s.ActiveCount())
	}
}

// ============================
// Test W: No duplicate cancellation watcher exists
// ============================
func TestProcessSupervisor_NoDuplicateCancellationWatcher(t *testing.T) {
	s := NewSupervisor()
	defer s.Shutdown(context.Background())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pass a command that had CommandContext set
	cmdArgs := append([]string{"-test.run=TestHelperProcess", "--"}, "exit-code", "0")
	cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1")

	// Prior to StartOwned, cmd.Cancel is non-nil (from CommandContext)
	if cmd.Cancel == nil {
		t.Fatalf("expected cmd.Cancel to be non-nil before StartOwned")
	}

	proc, err := s.StartOwned(ctx, cmd, ProcessSpec{ID: "proc-no-dup-watch", Tool: "t"})
	if err != nil {
		t.Fatalf("StartOwned failed: %v", err)
	}

	// StartOwned cleared cmd.Cancel before Start() so Go runtime watcher is NOT launched
	if cmd.Cancel != nil {
		t.Errorf("expected cmd.Cancel to be nil after StartOwned to suppress Go runtime watcher")
	}

	// Wait for process to exit normally
	err = proc.Wait()
	if err != nil {
		t.Fatalf("expected clean exit, got %v", err)
	}

	// Verify watcher goroutine exited cleanly upon process completion
	select {
	case <-proc.doneChan:
		// doneChan is closed, watcher terminated
	case <-time.After(1 * time.Second):
		t.Fatalf("doneChan was not closed after Wait()")
	}
}
