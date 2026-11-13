//go:build windows

package process

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// TestConsoleHelperProcess reports whether the process is attached to a console (GetConsoleCP is 0 without one).
func TestConsoleHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CONSOLE_HELPER") != "1" {
		return
	}
	cp, _ := windows.GetConsoleCP()
	fmt.Printf("CONSOLE_CP:%d\n", cp)
	os.Exit(0)
}

func TestHideConsoleWindow_AddsFlagAndPreservesExistingFlags(t *testing.T) {
	cmd := exec.Command("cmd")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	HideConsoleWindow(cmd)
	if f := cmd.SysProcAttr.CreationFlags; f&windows.CREATE_NO_WINDOW == 0 || f&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("creation flags = %#x; want CREATE_NO_WINDOW added and CREATE_NEW_PROCESS_GROUP kept", f)
	}

	bare := exec.Command("cmd")
	HideConsoleWindow(bare)
	if bare.SysProcAttr == nil || bare.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("CREATE_NO_WINDOW not set on a command without SysProcAttr")
	}
	HideConsoleWindow(nil) // must not panic
}

// Owned processes are created suspended (Job Object containment) and without a console window.
func TestProcessTreeSetup_SuspendedWithoutConsoleWindow(t *testing.T) {
	tree := &windowsProcessTree{}
	cmd := exec.Command("cmd")
	if err := tree.setup(cmd); err != nil {
		t.Fatal(err)
	}
	f := cmd.SysProcAttr.CreationFlags
	if f&windows.CREATE_SUSPENDED == 0 {
		t.Fatalf("creation flags = %#x; CREATE_SUSPENDED missing", f)
	}
	if f&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("creation flags = %#x; CREATE_NO_WINDOW missing: a GUI-subsystem host would open a console window per tool", f)
	}
}

// End to end through the supervisor: the owned child starts (suspended, contained, resumed), runs to completion and
// still has a console of its own — hidden, so console programs it starts inherit it instead of opening windows.
func TestStartOwned_ChildRunsWithHiddenConsole(t *testing.T) {
	sup := NewSupervisor()
	defer sup.Shutdown(context.Background())

	var out strings.Builder
	cmd := exec.Command(os.Args[0], "-test.run=^TestConsoleHelperProcess$")
	cmd.Env = append(os.Environ(), "GO_WANT_CONSOLE_HELPER=1")
	cmd.Stdout = &out
	proc, err := sup.StartOwned(context.Background(), cmd, ProcessSpec{ID: "console-flags", Tool: "test", Purpose: "console"})
	if err != nil {
		t.Fatalf("StartOwned: %v", err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatalf("child failed: %v (output %q)", err, out.String())
	}
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("owned child was not started with CREATE_NO_WINDOW")
	}
	if !strings.Contains(out.String(), "CONSOLE_CP:") || strings.Contains(out.String(), "CONSOLE_CP:0\n") {
		t.Fatalf("child has no console (output %q); its console children would each open a window", out.String())
	}
}
