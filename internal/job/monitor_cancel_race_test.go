package job

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Monitor/cancellation composition. The progress monitor applies engine observations to a snapshot of an active job.
// Once a cancellation (or another terminal transition) is persisted, a stale observation must not change the
// persisted lifecycle state, re-insert the job into the active set, or announce it again. Interleavings are forced at
// fixed hook points; nothing depends on timing.

// running dispatches a queued aria2 job without contention and returns it as the monitor would snapshot it.
func (e *raceEnv) running(t *testing.T, id string) *Job {
	t.Helper()
	qj := e.queued(t, id, StatusQueued, QueueActionStart)
	e.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) { return "gid-" + j.ID, nil }
	if err := e.m.dispatchQueuedJob(context.Background(), qj); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return e.snapshot(t, id)
}

func (e *raceEnv) snapshot(t *testing.T, id string) *Job {
	t.Helper()
	snap := e.m.GetActiveJobs()[id]
	if snap == nil {
		t.Fatalf("job %s not active", id)
	}
	return snap
}

func (e *raceEnv) eventsAfterLast(id, eventType string) []string {
	e.bus.mu.Lock()
	defer e.bus.mu.Unlock()
	last := -1
	for i, ev := range e.bus.events {
		if ev.Job.ID == id && ev.Type == eventType {
			last = i
		}
	}
	var after []string
	if last < 0 {
		return []string{"<no " + eventType + ">"}
	}
	for _, ev := range e.bus.events[last+1:] {
		if ev.Job.ID == id {
			after = append(after, ev.Type)
		}
	}
	return after
}

func progress(pct float64) *EngineStatus {
	return &EngineStatus{Status: StatusDownloading, Progress: pct, CompletedBytes: int64(pct * 10), TotalBytes: 1000, SpeedBytesPerSecond: 100}
}

func assertCancelledAndQuiet(t *testing.T, env *raceEnv, id string) {
	t.Helper()
	if got := env.status(t, id); got != StatusCancelled {
		t.Fatalf("persisted status = %s, want CANCELLED (stale observation resurrected the job)", got)
	}
	if env.active(id) {
		t.Fatal("stale observation re-inserted the cancelled job into the active set")
	}
	if after := env.eventsAfterLast(id, EventJobCancelled); len(after) != 0 {
		t.Fatalf("events published after the cancellation: %v", after)
	}
	if env.leaseHeld(id) {
		t.Fatal("transfer lease still held after cancellation")
	}
}

// 1. Monitor snapshots DOWNLOADING, Cancel succeeds, then the monitor persists its stale observation.
func TestMonitorRace_StalePersistedTickAfterCancel(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-1")
	if _, err := env.m.Cancel(context.Background(), "mon-1"); err != nil {
		t.Fatal(err)
	}
	env.m.UpdateJobFromEngine(context.Background(), snap, progress(60), true)
	assertCancelledAndQuiet(t, env, "mon-1")
}

// 1b. Same with an ephemeral (non-persisted) progress tick: it must neither re-insert nor announce the job.
func TestMonitorRace_StaleEphemeralTickAfterCancel(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-1b")
	if _, err := env.m.Cancel(context.Background(), "mon-1b"); err != nil {
		t.Fatal(err)
	}
	env.m.UpdateJobFromEngine(context.Background(), snap, progress(61), false)
	assertCancelledAndQuiet(t, env, "mon-1b")
}

// 2. Progress update racing the cancellation: the monitor's tick arrives while Cancel is committing (inside Cancel's
// conditional write). It is applied only after Cancel's commit section and is then dropped.
func TestMonitorRace_TickDuringCancelCommit(t *testing.T) {
	for _, persistNow := range []bool{true, false} {
		env := newRaceEnv(t)
		snap := env.running(t, "mon-2")
		tickDone := make(chan struct{})
		var once sync.Once
		env.repo.beforeUpdateIfStatus = func(j *Job, expected JobStatus) {
			if j.ID == "mon-2" && j.Status == StatusCancelled {
				once.Do(func() {
					go func() {
						env.m.UpdateJobFromEngine(context.Background(), snap, progress(70), persistNow)
						close(tickDone)
					}()
				})
			}
		}
		if _, err := env.m.Cancel(context.Background(), "mon-2"); err != nil {
			t.Fatal(err)
		}
		select {
		case <-tickDone:
		case <-time.After(10 * time.Second):
			t.Fatal("monitor tick did not complete")
		}
		assertCancelledAndQuiet(t, env, "mon-2")
	}
}

// 3. A stale progress observation after COMPLETED must not reopen the job.
func TestMonitorRace_StaleTickAfterCompleted(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-3")
	fresh := env.snapshot(t, "mon-3")
	env.m.UpdateJobFromEngine(context.Background(), fresh, &EngineStatus{Status: StatusCompleted, Progress: 100, CompletedBytes: 1000, TotalBytes: 1000}, true)
	if env.status(t, "mon-3") != StatusCompleted {
		t.Fatalf("completion not persisted: %s", env.status(t, "mon-3"))
	}
	env.m.UpdateJobFromEngine(context.Background(), snap, progress(40), true)
	if got := env.status(t, "mon-3"); got != StatusCompleted {
		t.Fatalf("stale tick overwrote COMPLETED with %s", got)
	}
	if env.active("mon-3") {
		t.Fatal("completed job re-inserted into the active set")
	}
	if after := env.eventsAfterLast("mon-3", EventJobCompleted); len(after) != 0 {
		t.Fatalf("events after completion: %v", after)
	}
}

// 4. A stale observation after FAILED must not reopen the job, nor turn it into another terminal state.
func TestMonitorRace_StaleTickAfterFailed(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-4")
	env.m.UpdateJobFromEngine(context.Background(), env.snapshot(t, "mon-4"), &EngineStatus{Status: StatusFailed, Error: "boom"}, true)
	if env.status(t, "mon-4") != StatusFailed {
		t.Fatalf("failure not persisted: %s", env.status(t, "mon-4"))
	}
	env.m.UpdateJobFromEngine(context.Background(), snap, progress(40), true)
	env.m.UpdateJobFromEngine(context.Background(), snap, &EngineStatus{Status: StatusCompleted, Progress: 100}, true)
	if got := env.status(t, "mon-4"); got != StatusFailed {
		t.Fatalf("stale observation overwrote FAILED with %s", got)
	}
	if env.active("mon-4") {
		t.Fatal("failed job re-inserted into the active set")
	}
}

// 5. Dispatch racing cancellation and monitor persistence: while the engine is starting, Cancel commits and a stale
// DOWNLOADING observation for the job is applied. The cancellation stands, the started engine is stopped, and the
// lease is released only afterwards.
func TestMonitorRace_DispatchCancelAndMonitorCompose(t *testing.T) {
	env := newRaceEnv(t)
	qj := env.queued(t, "mon-5", StatusQueued, QueueActionStart)
	var leaseHeldDuringStart bool
	env.eng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		if _, err := env.m.Cancel(ctx, j.ID); err != nil {
			t.Errorf("cancel: %v", err)
		}
		stale := *j
		stale.Status = StatusDownloading
		stale.EngineID = "gid-mon-5"
		env.m.UpdateJobFromEngine(ctx, &stale, progress(10), true)
		leaseHeldDuringStart = env.leaseHeld(j.ID)
		return "gid-mon-5", nil
	}
	assertHandled(t, env.m.dispatchQueuedJob(context.Background(), qj))
	if !leaseHeldDuringStart {
		t.Fatal("lease released while the engine was still starting")
	}
	assertCancelledAndQuiet(t, env, "mon-5")
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-mon-5" {
		t.Fatalf("started engine not stopped: %v", c)
	}
}

// 6–9. The real monitor tick keeps polling an active engine while it is being cancelled: Cancel commits between the
// monitor's snapshot and its write-back (inside the engine status call). The engine is cancelled while the lease is
// still held, the cancellation stands, nothing is re-inserted or re-announced, and later ticks no longer see the job.
func TestMonitorRace_RealMonitorTickDuringCancel(t *testing.T) {
	env := newRaceEnv(t)
	env.running(t, "mon-6")
	var leaseHeldAtEngineCancel bool
	env.eng.cancelFunc = func(ctx context.Context, j *Job) error {
		leaseHeldAtEngineCancel = env.leaseHeld(j.ID)
		env.mu.Lock()
		env.cancelled = append(env.cancelled, j.EngineID)
		env.mu.Unlock()
		return nil
	}
	polls := 0
	var once sync.Once
	env.eng.statusFunc = func(ctx context.Context, j *Job) (*EngineStatus, error) {
		polls++
		once.Do(func() {
			if _, err := env.m.Cancel(ctx, j.ID); err != nil {
				t.Errorf("cancel: %v", err)
			}
		})
		return progress(80), nil
	}
	mon := NewMonitor(env.m, time.Hour)
	mon.tick(context.Background()) // snapshot DOWNLOADING → status (Cancel commits) → stale write-back
	if !leaseHeldAtEngineCancel {
		t.Fatal("lease released before the engine was cancelled")
	}
	if c := env.engineCancels(); len(c) != 1 || c[0] != "gid-mon-6" {
		t.Fatalf("engine not cancelled exactly once: %v", c)
	}
	assertCancelledAndQuiet(t, env, "mon-6")
	before := polls
	mon.tick(context.Background())
	if polls != before {
		t.Fatal("monitor still polls the cancelled job's engine (untracked execution)")
	}
}

// 10. Legitimate observations still persist and announce: progress, processing, completion.
func TestMonitorRace_LegitimateUpdatesPersist(t *testing.T) {
	env := newRaceEnv(t)
	env.running(t, "mon-10")
	ctx := context.Background()

	env.m.UpdateJobFromEngine(ctx, env.snapshot(t, "mon-10"), progress(30), true)
	j, _ := env.repo.GetByID(ctx, "mon-10")
	if j.Status != StatusDownloading || j.Progress != 30 || j.CompletedBytes != 300 {
		t.Fatalf("progress not persisted: %+v", j)
	}
	env.m.UpdateJobFromEngine(ctx, env.snapshot(t, "mon-10"), progress(35), false)
	if got := env.snapshot(t, "mon-10"); got.Progress != 35 {
		t.Fatalf("ephemeral progress not applied to the active job: %v", got.Progress)
	}

	env.m.UpdateJobFromEngine(ctx, env.snapshot(t, "mon-10"), &EngineStatus{Status: StatusProcessing, Progress: 99}, true)
	if env.status(t, "mon-10") != StatusProcessing || env.snapshot(t, "mon-10").Status != StatusProcessing {
		t.Fatal("PROCESSING not persisted / active entry not refreshed")
	}

	env.m.UpdateJobFromEngine(ctx, env.snapshot(t, "mon-10"), &EngineStatus{Status: StatusCompleted, Progress: 100, CompletedBytes: 1000, TotalBytes: 1000}, true)
	if env.status(t, "mon-10") != StatusCompleted || env.active("mon-10") || env.leaseHeld("mon-10") {
		t.Fatalf("completion not applied: status=%s active=%v lease=%v", env.status(t, "mon-10"), env.active("mon-10"), env.leaseHeld("mon-10"))
	}
	if ev := env.lastJobEvent("mon-10"); ev != EventJobCompleted {
		t.Fatalf("last event = %s, want %s", ev, EventJobCompleted)
	}
}

// A snapshot that lags the persisted row by an engine-observable transition (DOWNLOADING snapshot, PROCESSING row,
// e.g. written by an earlier tick) still applies, so completion is never lost to an out-of-date in-memory copy.
func TestMonitorRace_LaggingSnapshotStillCompletes(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-11")
	row, _ := env.repo.GetByID(context.Background(), "mon-11")
	row.Status = StatusProcessing
	_ = env.repo.Update(context.Background(), row)
	env.m.UpdateJobFromEngine(context.Background(), snap, &EngineStatus{Status: StatusCompleted, Progress: 100, CompletedBytes: 1000, TotalBytes: 1000}, true)
	if got := env.status(t, "mon-11"); got != StatusCompleted {
		t.Fatalf("completion lost: %s", got)
	}
}

// The monitor's failure-count path (engine unreachable) must not overwrite a cancellation committed after its
// terminal-state check.
func TestMonitorRace_RecordFailureDoesNotOverwriteCancel(t *testing.T) {
	env := newRaceEnv(t)
	snap := env.running(t, "mon-12")
	mon := NewMonitor(env.m, time.Hour)
	mon.consecutiveFailures["mon-12"] = maxConsecutiveFailures - 1
	var once sync.Once
	env.repo.beforeUpdateIfStatus = func(j *Job, expected JobStatus) {
		if j.ID == "mon-12" && j.Status == StatusFailed {
			once.Do(func() {
				// A cancellation is persisted between the monitor's terminal-state check and its FAILED write.
				row, _ := env.repo.GetByID(context.Background(), "mon-12")
				row.Status = StatusCancelled
				_ = env.repo.Update(context.Background(), row)
			})
		}
	}
	mon.recordFailure(context.Background(), snap, "status query failed")
	if got := env.status(t, "mon-12"); got != StatusCancelled {
		t.Fatalf("monitor failure path overwrote CANCELLED with %s", got)
	}
	if env.lastJobEvent("mon-12") == EventJobFailed {
		t.Fatal("FAILED announced for a cancelled job")
	}
}
