package job

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// pendingAnalyzerEngine is a yt-dlp stand-in whose gate is held by an activated update awaiting restart: it
// reports MaintenancePending and refuses execution the way the real engine's AcquireUse does.
type pendingAnalyzerEngine struct {
	dummyEngine
	pending atomic.Bool
	starts  atomic.Int32
}

func (e *pendingAnalyzerEngine) MaintenancePending() bool { return e.pending.Load() }

func (e *pendingAnalyzerEngine) Start(ctx context.Context, j *Job, dir string) (string, error) {
	e.starts.Add(1)
	if e.pending.Load() {
		return "", errors.New("cannot start media download: tool is undergoing exclusive maintenance")
	}
	return j.ID, nil
}

func (e *pendingAnalyzerEngine) Analyze(ctx context.Context, url string) (*MediaInfo, error) {
	if e.pending.Load() {
		return nil, errors.New("cannot start media analysis: tool is undergoing exclusive maintenance")
	}
	return &MediaInfo{Title: "ok", Formats: []MediaFormat{{FormatID: "18"}}}, nil
}

// jobQueue returns runnable entries with their jobs populated (engine name included), like SQLite does.
type jobQueue struct {
	*fakeQueueRepo
	repo *fakeJobRepository
}

func (q *jobQueue) ListRunnable(ctx context.Context, evalTime ...time.Time) ([]QueuedJob, error) {
	list, _ := q.fakeQueueRepo.ListRunnable(ctx, evalTime...)
	for i := range list {
		if j, _ := q.repo.GetByID(ctx, list[i].JobID); j != nil {
			list[i].Job = *j
		}
	}
	return list, nil
}

func queuedMediaJob(t *testing.T, repo *fakeJobRepository, q *fakeQueueRepo, id string) {
	t.Helper()
	_ = repo.Create(context.Background(), &Job{ID: id, Type: TypeMedia, Engine: "ytdlp", Status: StatusQueued,
		MediaInfo: &MediaInfo{Formats: []MediaFormat{{FormatID: "18"}}, SelectedFmt: "18"}})
	_ = q.Enqueue(context.Background(), &QueueEntry{JobID: id, Action: QueueActionStart, Position: 1, EnqueuedAt: time.Now(), UpdatedAt: time.Now()})
}

// Regression (finding 2): queued media downloads must stay queued, not be failed, while an activated yt-dlp
// update awaits restart; after the gate reopens (restart) they dispatch normally.
func TestMaintenance_SchedulerLeavesQueuedMediaQueued(t *testing.T) {
	repo := newFakeJobRepository()
	fq := &fakeQueueRepo{entries: map[string]*QueueEntry{}}
	q := &jobQueue{fakeQueueRepo: fq, repo: repo}
	eng := &pendingAnalyzerEngine{dummyEngine: dummyEngine{available: true}}
	eng.pending.Store(true)
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": eng}}
	queuedMediaJob(t, repo, fq, "q1")

	var dispatches atomic.Int32
	s := NewScheduler(repo, q, func(context.Context) int { return 3 }, func(ctx context.Context, qj *QueuedJob) error {
		// Like production dispatch: the job leaves QUEUED and its queue entry is consumed.
		dispatches.Add(1)
		if j, _ := repo.GetByID(ctx, qj.JobID); j != nil {
			j.Status = StatusDownloading
			_ = repo.Update(ctx, j)
		}
		return fq.Delete(ctx, qj.JobID)
	})
	s.SetEngineRegistry(reg)
	gov := NewResourceGovernor(DefaultResourceGovernorConfig())
	s.SetResourceGovernor(gov)
	s.ctx = context.Background()

	s.schedule()
	if n := dispatches.Load(); n != 0 {
		t.Fatalf("media job dispatched %d time(s) while restart is pending", n)
	}
	if gov.GetLease("q1") != nil {
		t.Fatal("no capacity may be held for a job that cannot start")
	}
	if j, _ := repo.GetByID(context.Background(), "q1"); j.Status != StatusQueued {
		t.Fatalf("queued job status changed to %s", j.Status)
	}
	if e, _ := fq.Get(context.Background(), "q1"); e == nil {
		t.Fatal("queue entry removed")
	}

	eng.pending.Store(false) // backend restarted: gate open
	s.schedule()
	if n := dispatches.Load(); n != 1 {
		t.Fatalf("expected dispatch after restart, got %d", n)
	}
}

// Regression (finding 2, race backstop): if activation happens after selection, dispatch leaves the job queued
// and unchanged instead of failing it on a refused engine start.
func TestMaintenance_DispatchBackstopKeepsJobQueued(t *testing.T) {
	repo := newFakeJobRepository()
	fq := &fakeQueueRepo{entries: map[string]*QueueEntry{}}
	eng := &pendingAnalyzerEngine{dummyEngine: dummyEngine{available: true}}
	eng.pending.Store(true)
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": eng}}
	mgr := NewManager(repo, reg, &fakeEventBus{}, t.TempDir(), nil, t.TempDir())
	mgr.SetQueueRepository(fq)
	queuedMediaJob(t, repo, fq, "q2")

	err := mgr.DispatchQueuedJob(context.Background(), &QueuedJob{JobID: "q2", Action: QueueActionStart})
	var handled *DispatchHandledError
	if !errors.As(err, &handled) {
		t.Fatalf("expected DispatchHandledError (no state change), got %v", err)
	}
	if eng.starts.Load() != 0 {
		t.Fatal("engine start attempted while restart is pending")
	}
	if j, _ := repo.GetByID(context.Background(), "q2"); j.Status != StatusQueued || j.Error != "" {
		t.Fatalf("job changed: status=%s error=%q", j.Status, j.Error)
	}
	if e, _ := fq.Get(context.Background(), "q2"); e == nil {
		t.Fatal("queue entry removed")
	}
}

// Regression (finding 2): an analysis refused by the held gate stays ANALYZING for startup recovery.
func TestMaintenance_AnalysisDeferredNotFailed(t *testing.T) {
	repo := newFakeJobRepository()
	eng := &pendingAnalyzerEngine{dummyEngine: dummyEngine{available: true}}
	eng.pending.Store(true)
	reg := &dummyRegistry{engines: map[string]IEngine{"ytdlp": eng}}
	bus := &fakeEventBus{}
	mgr := NewManager(repo, reg, bus, t.TempDir(), nil, t.TempDir())
	mgr.SetResourceGovernor(NewResourceGovernor(DefaultResourceGovernorConfig()))
	j := newAnalyzingJob("an1")
	_ = repo.Create(context.Background(), j)

	mgr.analyzeMedia(context.Background(), j.ID, j.Source)

	got, _ := repo.GetByID(context.Background(), "an1")
	if got.Status != StatusAnalyzing {
		t.Fatalf("expected ANALYZING to be preserved for recovery, got %s", got.Status)
	}
	if countEvents(bus, EventJobFailed, "an1") != 0 {
		t.Fatal("deferred analysis must not publish job.failed")
	}
}

func TestMaintenance_RetryRefusedWithTypedError(t *testing.T) {
	repo := newFakeJobRepository()
	eng := &pendingAnalyzerEngine{dummyEngine: dummyEngine{available: true}}
	eng.pending.Store(true)
	mgr := NewManager(repo, &dummyRegistry{engines: map[string]IEngine{"ytdlp": eng}}, &fakeEventBus{}, t.TempDir(), nil, t.TempDir())
	_ = repo.Create(context.Background(), &Job{ID: "f1", Type: TypeMedia, Engine: "ytdlp", Status: StatusFailed, Source: "https://example.com/v"})

	_, err := mgr.Retry(context.Background(), "f1")
	var appErr *AppError
	if !errors.As(err, &appErr) || appErr.Code != ErrYtDlpRestartRequired {
		t.Fatalf("expected %s, got %v", ErrYtDlpRestartRequired, err)
	}
	if j, _ := repo.GetByID(context.Background(), "f1"); j.Status != StatusFailed {
		t.Fatalf("refused retry changed status to %s", j.Status)
	}
}
