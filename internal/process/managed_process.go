package process

import (
	"context"
	"os/exec"
	"sync"
	"time"
)

type processTree interface {
	setup(cmd *exec.Cmd) error
	attach(ctx context.Context, cmd *exec.Cmd) error
	terminate() error
	close() error
}

// ManagedProcess wraps an OS command with process-tree lifecycle management.
type ManagedProcess struct {
	spec       ProcessSpec
	cmd        *exec.Cmd
	tree       processTree
	startedAt  time.Time
	exitedAt   *time.Time
	exitCode   int
	terminated bool
	state      ProcessState

	waitErr  error
	waitOnce sync.Once
	termOnce sync.Once
	doneChan chan struct{}
	mu       sync.RWMutex

	onExit func(id string)
}

func newManagedProcess(spec ProcessSpec, cmd *exec.Cmd, tree processTree, onExit func(id string)) *ManagedProcess {
	return &ManagedProcess{
		spec:      spec,
		cmd:       cmd,
		tree:      tree,
		startedAt: time.Now(),
		state:     StateRunning,
		doneChan:  make(chan struct{}),
		onExit:    onExit,
	}
}

// ID returns the unique process identifier.
func (p *ManagedProcess) ID() string {
	return p.spec.ID
}

// JobID returns the associated Job ID if any.
func (p *ManagedProcess) JobID() string {
	return p.spec.JobID
}

// Tool returns the tool name.
func (p *ManagedProcess) Tool() string {
	return p.spec.Tool
}

// Purpose returns the process purpose.
func (p *ManagedProcess) Purpose() string {
	return p.spec.Purpose
}

// PID returns the OS process ID. Returns 0 if not started.
func (p *ManagedProcess) PID() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Cmd returns the underlying exec.Cmd.
func (p *ManagedProcess) Cmd() *exec.Cmd {
	return p.cmd
}

// State returns the current lifecycle state.
func (p *ManagedProcess) State() ProcessState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

// WasTerminated returns true if the process was stopped via Terminate or supervisor shutdown.
func (p *ManagedProcess) WasTerminated() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.terminated
}

// Info returns a snapshot of process state.
func (p *ManagedProcess) Info() *ProcessInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var exitCopy *time.Time
	if p.exitedAt != nil {
		t := *p.exitedAt
		exitCopy = &t
	}

	return &ProcessInfo{
		ID:         p.spec.ID,
		JobID:      p.spec.JobID,
		Tool:       p.spec.Tool,
		Purpose:    p.spec.Purpose,
		PID:        p.PID(),
		State:      p.state,
		ExitCode:   p.exitCode,
		Terminated: p.terminated,
		StartedAt:  p.startedAt,
		ExitedAt:   exitCopy,
	}
}

// Terminate stops the entire process tree (parent and all descendants).
// It is idempotent and concurrency-safe.
func (p *ManagedProcess) Terminate() error {
	p.termOnce.Do(func() {
		p.mu.Lock()
		p.terminated = true
		p.mu.Unlock()

		if p.tree != nil {
			_ = p.tree.terminate()
		}
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
	return nil
}

// Wait waits for the process to exit, cleans up tree handles, and unregisters it.
// It is idempotent and concurrency-safe; subsequent calls return the memoized error.
func (p *ManagedProcess) Wait() error {
	p.waitOnce.Do(func() {
		if p.cmd != nil {
			p.waitErr = p.cmd.Wait()
		}

		p.mu.Lock()
		now := time.Now()
		p.exitedAt = &now
		if p.cmd != nil && p.cmd.ProcessState != nil {
			p.exitCode = p.cmd.ProcessState.ExitCode()
		}
		if p.terminated {
			p.state = StateTerminated
		} else {
			p.state = StateExited
		}
		p.mu.Unlock()

		if p.tree != nil {
			_ = p.tree.close()
		}

		close(p.doneChan)

		if p.onExit != nil {
			p.onExit(p.spec.ID)
		}
	})
	return p.waitErr
}
