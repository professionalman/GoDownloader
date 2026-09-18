package process

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"sync"
	"time"
)

// Supervisor coordinates owned child process trees, their lifecycle, and cleanup.
type Supervisor struct {
	mu     sync.RWMutex
	procs  map[string]*ManagedProcess
	closed bool

	// treeFactory allows injecting mock processTree in tests
	treeFactory func() (processTree, error)
}

// NewSupervisor creates a new process supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		procs:       make(map[string]*ManagedProcess),
		treeFactory: newProcessTree,
	}
}

// StartOwned starts cmd and registers it as an application-owned supervised process tree.
// It assigns the root process to an OS process-tree controller (Windows Job Object with
// KILL_ON_JOB_CLOSE or Unix process group), ensuring child and descendant processes
// cannot be orphaned upon cancellation, exit, or application shutdown.
func (s *Supervisor) StartOwned(ctx context.Context, cmd *exec.Cmd, spec ProcessSpec) (*ManagedProcess, error) {
	if spec.ID == "" {
		return nil, fmt.Errorf("process spec ID cannot be empty")
	}

	// Phase A: If context is already cancelled, do not spawn an OS process at all.
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrSupervisorClosed
	}
	if _, exists := s.procs[spec.ID]; exists {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrProcessAlreadyRunning, spec.ID)
	}
	s.mu.Unlock()

	tree, err := s.treeFactory()
	if err != nil {
		return nil, fmt.Errorf("create process tree: %w", err)
	}

	if err := tree.setup(cmd); err != nil {
		_ = tree.close()
		return nil, fmt.Errorf("setup process tree: %w", err)
	}

	proc := newManagedProcess(spec, cmd, tree, s.removeProcess)

	// Ensure exactly one cancellation owner:
	// Clear cmd.Cancel before cmd.Start() so Go runtime's direct-child Process.Kill()
	// watcher is never launched. ProcessSupervisor is the sole owner of context cancellation.
	// This also guarantees zero post-Start mutation of Cmd lifecycle fields.
	cmd.Cancel = nil

	if err := cmd.Start(); err != nil {
		_ = tree.close()
		return nil, fmt.Errorf("start process %s: %w", spec.Tool, err)
	}

	// Phase B: Attach to tree (Windows Job Object assignment + containment verification + resume)
	if err := tree.attach(ctx, cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = tree.close()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrProcessTreeAttach, err)
	}

	// Phase C: If context was cancelled immediately after containment/resume, terminate cleanly
	if ctx != nil && ctx.Err() != nil {
		_ = proc.Terminate()
		_ = proc.Wait()
		return nil, ctx.Err()
	}

	s.mu.Lock()
	if s.closed {
		// Race with supervisor shutdown: terminate immediately
		s.mu.Unlock()
		_ = proc.Terminate()
		_ = proc.Wait()
		return nil, ErrSupervisorClosed
	}
	s.procs[spec.ID] = proc
	s.mu.Unlock()

	// Phase D: Sole cancellation watcher.
	// Watches ctx.Done() during normal execution and terminates the full process tree.
	// When the process exits normally, proc.doneChan unblocks and the goroutine exits cleanly.
	if ctx != nil && ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = proc.Terminate()
			case <-proc.doneChan:
			}
		}()
	}

	// Log started process with high-level metadata (omitting sensitive argv/credentials)
	log.Printf("process: started owned process %s [tool=%s, purpose=%s, pid=%d]", spec.ID, spec.Tool, spec.Purpose, cmd.Process.Pid)

	return proc, nil
}

// Terminate stops a specific managed process and its descendants.
func (s *Supervisor) Terminate(id string) error {
	s.mu.RLock()
	proc, exists := s.procs[id]
	s.mu.RUnlock()

	if !exists {
		return ErrProcessNotFound
	}
	return proc.Terminate()
}

// TerminateAll stops all currently active managed process trees.
func (s *Supervisor) TerminateAll() error {
	s.mu.RLock()
	procs := make([]*ManagedProcess, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.RUnlock()

	for _, p := range procs {
		_ = p.Terminate()
	}
	return nil
}

// Shutdown marks the supervisor closed, terminates all owned process trees,
// and boundedly waits for all processes to be reaped.
func (s *Supervisor) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	procs := make([]*ManagedProcess, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()

	// Terminate all process trees
	for _, p := range procs {
		_ = p.Terminate()
	}

	// Bounded wait for active processes to be reaped
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(5 * time.Second)
	}

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		s.mu.RLock()
		remaining := len(s.procs)
		s.mu.RUnlock()

		if remaining == 0 {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("shutdown timed out with %d active processes remaining", remaining)
			}
		}
	}
}

// Get returns process info for a managed process by ID, or nil if not found.
func (s *Supervisor) Get(id string) *ProcessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.procs[id]; ok {
		return p.Info()
	}
	return nil
}

// ActiveCount returns the number of currently active supervised processes.
func (s *Supervisor) ActiveCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.procs)
}

// List returns snapshots of all currently active managed processes.
func (s *Supervisor) List() []*ProcessInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]*ProcessInfo, 0, len(s.procs))
	for _, p := range s.procs {
		list = append(list, p.Info())
	}
	return list
}

func (s *Supervisor) removeProcess(id string) {
	s.mu.Lock()
	delete(s.procs, id)
	s.mu.Unlock()
}
