package job

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// lockedQueue makes fakeQueueRepo safe for concurrent tests and lets a test fail queue insertion.
type lockedQueue struct {
	*fakeQueueRepo
	mu         sync.Mutex
	enqueueErr error
}

func newLockedQueue() *lockedQueue {
	return &lockedQueue{fakeQueueRepo: &fakeQueueRepo{entries: make(map[string]*QueueEntry)}}
}

func (q *lockedQueue) Enqueue(ctx context.Context, entry *QueueEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.enqueueErr != nil {
		return q.enqueueErr
	}
	return q.fakeQueueRepo.Enqueue(ctx, entry)
}

func (q *lockedQueue) Get(ctx context.Context, jobID string) (*QueueEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fakeQueueRepo.Get(ctx, jobID)
}

func (q *lockedQueue) Delete(ctx context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fakeQueueRepo.Delete(ctx, jobID)
}

func (q *lockedQueue) NextPosition(ctx context.Context, priority JobPriority) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fakeQueueRepo.NextPosition(ctx, priority)
}

func (q *lockedQueue) has(jobID string) bool {
	e, _ := q.Get(context.Background(), jobID)
	return e != nil
}

// startCountingEngine records every download start so tests can prove no download began.
type startCountingEngine struct {
	dummyEngine
	starts atomic.Int32
}

func (e *startCountingEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	e.starts.Add(1)
	return j.ID, nil
}

type selectionHarness struct {
	mgr        *Manager
	repo       *fakeJobRepository
	queue      *lockedQueue
	engine     *startCountingEngine
	dispatches atomic.Int32
	bus        *fakeEventBus
}

// newSelectionHarness wires a Manager the way production does (queue repository + scheduler). The scheduler is
// never started, so any download start would have to come from SelectFormat/Cancel themselves.
// With withScheduler=false the legacy direct-start fallback is active, which would start a download for any
// selection that wins; it is used to prove a losing selection never reaches engine Start.
func newSelectionHarness(t *testing.T, withScheduler bool) *selectionHarness {
	t.Helper()
	h := &selectionHarness{
		repo:   newFakeJobRepository(),
		queue:  newLockedQueue(),
		engine: &startCountingEngine{dummyEngine: dummyEngine{available: true}},
		bus:    &fakeEventBus{},
	}
	h.repo.queue = h.queue
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": h.engine}}
	h.mgr = NewManager(h.repo, reg, h.bus, t.TempDir(), nil, t.TempDir())
	h.mgr.SetQueueRepository(h.queue)
	if withScheduler {
		h.mgr.SetScheduler(NewScheduler(h.repo, h.queue, nil, func(ctx context.Context, qj *QueuedJob) error {
			h.dispatches.Add(1)
			return nil
		}))
	}
	return h
}

func (h *selectionHarness) addAwaitingJob(t *testing.T, id string) {
	t.Helper()
	if err := h.repo.Create(context.Background(), &Job{
		ID:       id,
		Source:   "https://example.com/watch?v=" + id,
		Type:     TypeMedia,
		Engine:   "ytdlp",
		Status:   StatusAwaitingSelection,
		Priority: JobPriorityNormal,
		MediaInfo: &MediaInfo{
			Title:   "Video " + id,
			Formats: []MediaFormat{{FormatID: "18"}, {FormatID: "22"}},
		},
	}); err != nil {
		t.Fatalf("create job: %v", err)
	}
}

func (h *selectionHarness) job(t *testing.T, id string) *Job {
	t.Helper()
	j, err := h.repo.GetByID(context.Background(), id)
	if err != nil || j == nil {
		t.Fatalf("get job %s: %v", id, err)
	}
	return j
}

func (h *selectionHarness) guardsHeld() int {
	h.mgr.mu.RLock()
	defer h.mgr.mu.RUnlock()
	return len(h.mgr.inFlightSelections)
}

// assertCancelledClean verifies the single valid post-cancellation state: CANCELLED, no runnable queue entry,
// no download started, no dispatch, and no leaked selection guard.
func (h *selectionHarness) assertCancelledClean(t *testing.T, id string) {
	t.Helper()
	if got := h.job(t, id).Status; got != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", got)
	}
	if h.queue.has(id) {
		t.Fatal("orphan queue entry remains for cancelled job")
	}
	if n := h.engine.starts.Load(); n != 0 {
		t.Fatalf("download started %d time(s) for cancelled job", n)
	}
	if n := h.dispatches.Load(); n != 0 {
		t.Fatalf("scheduler dispatched %d time(s) for cancelled job", n)
	}
	if n := h.guardsHeld(); n != 0 {
		t.Fatalf("selection guard leaked (%d held)", n)
	}
}

func requireAppErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("expected AppError %s, got %v", code, err)
	}
}

// Case A, interleaving 1: Cancel completes before SelectFormat reads the job.
func TestSelectCancel_CancelWinsBeforeSelection(t *testing.T) {
	h := newSelectionHarness(t, false)
	h.addAwaitingJob(t, "a1")
	ctx := context.Background()

	if _, err := h.mgr.Cancel(ctx, "a1"); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	_, err := h.mgr.SelectFormat(ctx, "a1", "18")
	requireAppErrorCode(t, err, ErrInvalidJobState)

	h.assertCancelledClean(t, "a1")
	if fmtID := h.job(t, "a1").MediaInfo.SelectedFmt; fmtID != "" {
		t.Fatalf("selection recorded on cancelled job: %q", fmtID)
	}
}

// Case A, interleaving 2: SelectFormat has read and validated AWAITING_SELECTION, then Cancel commits
// immediately before the selection's final compare-and-set. The selection must lose and start nothing.
func TestSelectCancel_CancelDuringFinalTransition(t *testing.T) {
	h := newSelectionHarness(t, false) // fallback would start a download if the selection wrongly won
	h.addAwaitingJob(t, "a2")
	ctx := context.Background()

	var cancelErr error
	h.repo.beforeSelectMediaFormat = func() {
		h.repo.beforeSelectMediaFormat = nil
		_, cancelErr = h.mgr.Cancel(ctx, "a2")
	}

	_, err := h.mgr.SelectFormat(ctx, "a2", "18")
	if cancelErr != nil {
		t.Fatalf("interleaved Cancel failed: %v", cancelErr)
	}
	requireAppErrorCode(t, err, ErrInvalidJobState)

	h.assertCancelledClean(t, "a2")
	if fmtID := h.job(t, "a2").MediaInfo.SelectedFmt; fmtID != "" {
		t.Fatalf("selection recorded on cancelled job: %q", fmtID)
	}
	if countEvents(h.bus, EventJobCancelled, "a2") != 1 {
		t.Fatal("expected exactly one job.cancelled event")
	}
}

// Case B: selection commits first (QUEUED + queue entry + selected format), then Cancel removes both.
func TestSelectCancel_SelectionWinsThenCancel(t *testing.T) {
	h := newSelectionHarness(t, true)
	h.addAwaitingJob(t, "b1")
	ctx := context.Background()

	selected, err := h.mgr.SelectFormat(ctx, "b1", "22")
	if err != nil {
		t.Fatalf("SelectFormat failed: %v", err)
	}
	if selected.Status != StatusQueued {
		t.Fatalf("expected QUEUED, got %s", selected.Status)
	}
	persisted := h.job(t, "b1")
	if persisted.Status != StatusQueued || persisted.MediaInfo.SelectedFmt != "22" {
		t.Fatalf("selection not durably recorded: status=%s fmt=%q", persisted.Status, persisted.MediaInfo.SelectedFmt)
	}
	if !h.queue.has("b1") {
		t.Fatal("expected queue entry after selection")
	}

	cancelled, err := h.mgr.Cancel(ctx, "b1")
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", cancelled.Status)
	}
	h.assertCancelledClean(t, "b1")
}

// Cancel observes AWAITING_SELECTION, but the selection (job transition + queue insertion) commits before
// Cancel's compare-and-set. Cancel must lose the CAS, re-evaluate as a QUEUED cancel, and remove the entry.
func TestSelectCancel_CancelRacesQueueInsertion(t *testing.T) {
	h := newSelectionHarness(t, true)
	h.addAwaitingJob(t, "c1")
	ctx := context.Background()

	var selectErr error
	var queuedBeforeCancel bool
	h.repo.beforeCancelAwaitingSelection = func() {
		h.repo.beforeCancelAwaitingSelection = nil
		_, selectErr = h.mgr.SelectFormat(ctx, "c1", "18")
		queuedBeforeCancel = h.queue.has("c1")
	}

	cancelled, err := h.mgr.Cancel(ctx, "c1")
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if selectErr != nil {
		t.Fatalf("interleaved SelectFormat failed: %v", selectErr)
	}
	if !queuedBeforeCancel {
		t.Fatal("test precondition: selection should have inserted a queue entry before Cancel's CAS")
	}
	if cancelled.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", cancelled.Status)
	}
	h.assertCancelledClean(t, "c1")
}

// Queue insertion failure inside the selection transaction leaves no half-selected job and no queue entry,
// and a later cancellation still succeeds.
func TestSelectCancel_QueueInsertionFailureLeavesNoHalfSelection(t *testing.T) {
	h := newSelectionHarness(t, true)
	h.addAwaitingJob(t, "d1")
	ctx := context.Background()
	h.queue.enqueueErr = errors.New("simulated queue insert failure")

	_, err := h.mgr.SelectFormat(ctx, "d1", "18")
	requireAppErrorCode(t, err, ErrInternalError)

	j := h.job(t, "d1")
	if j.Status != StatusAwaitingSelection || j.MediaInfo.SelectedFmt != "" {
		t.Fatalf("half-selected state persisted: status=%s fmt=%q", j.Status, j.MediaInfo.SelectedFmt)
	}
	if h.queue.has("d1") || h.guardsHeld() != 0 {
		t.Fatal("queue entry or selection guard leaked after failed selection")
	}

	h.queue.enqueueErr = nil
	if _, err := h.mgr.Cancel(ctx, "d1"); err != nil {
		t.Fatalf("Cancel after failed selection: %v", err)
	}
	h.assertCancelledClean(t, "d1")
}

// A failed selection transaction (DB error) leaves AWAITING_SELECTION intact; a failed cancel transaction
// leaves the job uncancelled rather than half-cancelled.
func TestSelectCancel_PersistenceFailures(t *testing.T) {
	h := newSelectionHarness(t, true)
	h.addAwaitingJob(t, "e1")
	ctx := context.Background()

	h.repo.selectErr = errors.New("simulated DB failure")
	_, err := h.mgr.SelectFormat(ctx, "e1", "18")
	requireAppErrorCode(t, err, ErrInternalError)
	j := h.job(t, "e1")
	if j.Status != StatusAwaitingSelection || j.MediaInfo.SelectedFmt != "" || h.queue.has("e1") || h.guardsHeld() != 0 {
		t.Fatalf("DB failure left inconsistent state: status=%s fmt=%q queued=%v guards=%d",
			j.Status, j.MediaInfo.SelectedFmt, h.queue.has("e1"), h.guardsHeld())
	}
	h.repo.selectErr = nil

	h.repo.updateErr = errors.New("simulated DB failure")
	_, err = h.mgr.Cancel(ctx, "e1")
	requireAppErrorCode(t, err, ErrInternalError)
	if got := h.job(t, "e1").Status; got != StatusAwaitingSelection {
		t.Fatalf("failed cancel changed status to %s", got)
	}
	if countEvents(h.bus, EventJobCancelled, "e1") != 0 {
		t.Fatal("failed cancel must not publish job.cancelled")
	}
	h.repo.updateErr = nil

	if _, err := h.mgr.Cancel(ctx, "e1"); err != nil {
		t.Fatalf("Cancel after recovery: %v", err)
	}
	h.assertCancelledClean(t, "e1")
}

// The selection guard is released on every failure path.
func TestSelectCancel_GuardReleasedOnAllFailurePaths(t *testing.T) {
	h := newSelectionHarness(t, true)
	ctx := context.Background()
	h.addAwaitingJob(t, "g-ok")
	_ = h.repo.Create(ctx, &Job{ID: "g-download", Type: TypeDownload, Status: StatusQueued})
	_ = h.repo.Create(ctx, &Job{ID: "g-noinfo", Type: TypeMedia, Status: StatusAwaitingSelection})
	_ = h.repo.Create(ctx, &Job{ID: "g-analyzing", Type: TypeMedia, Status: StatusAnalyzing})

	cases := []struct {
		name  string
		id    string
		fmtID string
		subs  *SubtitleOptions
		setup func()
	}{
		{name: "not found", id: "missing", fmtID: "18"},
		{name: "not media", id: "g-download", fmtID: "18"},
		{name: "wrong status", id: "g-analyzing", fmtID: "18"},
		{name: "nil media info", id: "g-noinfo", fmtID: "18"},
		{name: "invalid format", id: "g-ok", fmtID: "999"},
		{name: "invalid subtitles", id: "g-ok", fmtID: "18", subs: &SubtitleOptions{Languages: []string{"en"}, Mode: "bogus"}},
		{name: "persistence error", id: "g-ok", fmtID: "18", setup: func() { h.repo.selectErr = errors.New("db down") }},
		{name: "lost compare-and-set", id: "g-ok", fmtID: "18", setup: func() {
			h.repo.selectErr = nil
			h.repo.beforeSelectMediaFormat = func() {
				h.repo.beforeSelectMediaFormat = nil
				_, _ = h.mgr.Cancel(ctx, "g-ok")
			}
		}},
	}
	for _, tc := range cases {
		if tc.setup != nil {
			tc.setup()
		}
		if _, err := h.mgr.SelectFormat(ctx, tc.id, tc.fmtID, tc.subs); err == nil {
			t.Fatalf("%s: expected SelectFormat to fail", tc.name)
		}
		if n := h.guardsHeld(); n != 0 {
			t.Fatalf("%s: selection guard leaked (%d held)", tc.name, n)
		}
	}
}

// Repeated true concurrency: one Cancel and several SelectFormat calls race on the same job each round.
// Whatever the interleaving, the only valid final state is CANCELLED with no queue entry and no download.
func TestSelectCancel_ConcurrentContention(t *testing.T) {
	for round := 0; round < 200; round++ {
		h := newSelectionHarness(t, true)
		id := fmt.Sprintf("r%d", round)
		h.addAwaitingJob(t, id)
		ctx := context.Background()

		const selectors = 4
		start := make(chan struct{})
		var wg sync.WaitGroup
		var selectWins atomic.Int32
		var cancelErr error

		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, cancelErr = h.mgr.Cancel(ctx, id)
		}()
		for i := 0; i < selectors; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, err := h.mgr.SelectFormat(ctx, id, "18"); err == nil {
					selectWins.Add(1)
				}
			}()
		}
		close(start)
		wg.Wait()

		if cancelErr != nil {
			t.Fatalf("round %d: Cancel failed: %v", round, cancelErr)
		}
		if n := selectWins.Load(); n > 1 {
			t.Fatalf("round %d: %d selections won; expected at most one", round, n)
		}
		h.assertCancelledClean(t, id)
		if countEvents(h.bus, EventJobCancelled, id) != 1 {
			t.Fatalf("round %d: expected exactly one job.cancelled event", round)
		}
	}
}

// Single-winner selection is enforced by the repository compare-and-set itself, not only the in-memory guard.
func TestSelectCancel_RepositoryCASAdmitsSingleSelection(t *testing.T) {
	h := newSelectionHarness(t, true)
	h.addAwaitingJob(t, "s1")
	ctx := context.Background()
	info := &MediaInfo{Formats: []MediaFormat{{FormatID: "18"}}, SelectedFmt: "18"}

	first, err := h.repo.SelectMediaFormat(ctx, "s1", info, &QueueEntry{Action: QueueActionStart})
	if err != nil || !first {
		t.Fatalf("first selection should win: won=%v err=%v", first, err)
	}
	second, err := h.repo.SelectMediaFormat(ctx, "s1", info, &QueueEntry{Action: QueueActionStart})
	if err != nil || second {
		t.Fatalf("second selection must lose the compare-and-set: won=%v err=%v", second, err)
	}
}
