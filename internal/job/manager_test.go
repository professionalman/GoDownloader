package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeEngine is a test double for Engine.
type fakeEngine struct {
	startFunc  func(ctx context.Context, j *Job, dir string) (string, error)
	pauseFunc  func(ctx context.Context, j *Job) error
	resumeFunc func(ctx context.Context, j *Job) error
	cancelFunc func(ctx context.Context, j *Job) error
	statusFunc func(ctx context.Context, j *Job) (*EngineStatus, error)
}

func (f *fakeEngine) Start(ctx context.Context, j *Job, dir string) (string, error) {
	if f.startFunc != nil {
		return f.startFunc(ctx, j, dir)
	}
	return "fake-gid-123", nil
}

func (f *fakeEngine) Pause(ctx context.Context, j *Job) error {
	if f.pauseFunc != nil {
		return f.pauseFunc(ctx, j)
	}
	return nil
}

func (f *fakeEngine) Resume(ctx context.Context, j *Job) error {
	if f.resumeFunc != nil {
		return f.resumeFunc(ctx, j)
	}
	return nil
}

func (f *fakeEngine) Cancel(ctx context.Context, j *Job) error {
	if f.cancelFunc != nil {
		return f.cancelFunc(ctx, j)
	}
	return nil
}

func (f *fakeEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	if f.statusFunc != nil {
		return f.statusFunc(ctx, j)
	}
	return &EngineStatus{Status: StatusDownloading}, nil
}

func (f *fakeEngine) Get(name string) (Engine, bool) {
	return f, true
}

func (f *fakeEngine) Detect(url string) string {
	return "aria2"
}

type fakeJobRepository struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func newFakeJobRepository() *fakeJobRepository {
	return &fakeJobRepository{jobs: make(map[string]*Job)}
}

func (f *fakeJobRepository) Create(ctx context.Context, j *Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	jCopy := *j
	f.jobs[j.ID] = &jCopy
	return nil
}

func (f *fakeJobRepository) Update(ctx context.Context, j *Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.jobs[j.ID]; !exists {
		return fmt.Errorf("job not found")
	}
	jCopy := *j
	f.jobs[j.ID] = &jCopy
	return nil
}

func (f *fakeJobRepository) GetByID(ctx context.Context, id string) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, exists := f.jobs[id]
	if !exists {
		return nil, nil
	}
	jCopy := *j
	return &jCopy, nil
}

func (f *fakeJobRepository) List(ctx context.Context) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := make([]Job, 0, len(f.jobs))
	for _, j := range f.jobs {
		res = append(res, *j)
	}
	return res, nil
}

func (f *fakeJobRepository) ListRecoverable(ctx context.Context) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	res := make([]Job, 0)
	for _, j := range f.jobs {
		if IsRecoverable(j.Status) {
			res = append(res, *j)
		}
	}
	return res, nil
}

type fakeEventBus struct {
	mu     sync.Mutex
	events []Event
	ch     chan Event
}

func newFakeEventBus() *fakeEventBus {
	return &fakeEventBus{ch: make(chan Event, 64)}
}

func (f *fakeEventBus) Publish(e Event) {
	f.mu.Lock()
	f.events = append(f.events, e)
	f.mu.Unlock()
	select {
	case f.ch <- e:
	default:
	}
}

func (f *fakeEventBus) Subscribe() <-chan Event {
	return f.ch
}

func (f *fakeEventBus) Unsubscribe(ch <-chan Event) {
}

func setupManagerTest(t *testing.T) (*Manager, *fakeEngine, *fakeEventBus, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	repo := newFakeJobRepository()
	fakeEng := &fakeEngine{}
	bus := newFakeEventBus()
	downloadDir := filepath.Join(tmpDir, "downloads")
	os.MkdirAll(downloadDir, 0755)

	m := NewManager(repo, fakeEng, bus, downloadDir)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return m, fakeEng, bus, cleanup
}

func TestManager_Create(t *testing.T) {
	m, _, bus, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	// Subscribe to events
	ch := bus.Subscribe()
	defer bus.Unsubscribe(ch)

	j, err := m.Create(ctx, "https://example.com/file.zip")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if j.Status != StatusDownloading {
		t.Errorf("expected downloading, got %s", j.Status)
	}
	if j.Name != "file.zip" {
		t.Errorf("expected name file.zip, got %s", j.Name)
	}
	if j.EngineID != "fake-gid-123" {
		t.Errorf("expected engineID fake-gid-123, got %s", j.EngineID)
	}

	// Verify event was published
	select {
	case evt := <-ch:
		if evt.Type != EventJobCreated {
			t.Errorf("expected event type %s, got %s", EventJobCreated, evt.Type)
		}
	case <-time.After(time.Second):
		t.Error("timed out waiting for event")
	}
}

func TestManager_Create_InvalidURL(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	_, err := m.Create(ctx, "not-a-url")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}

	_, err = m.Create(ctx, "ftp://example.com/file.zip")
	if err == nil {
		t.Fatal("expected error for FTP URL")
	}
}

func TestManager_Pause(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, _ := m.Create(ctx, "https://example.com/file.zip")

	paused, err := m.Pause(ctx, j.ID)
	if err != nil {
		t.Fatalf("Pause failed: %v", err)
	}
	if paused.Status != StatusPaused {
		t.Errorf("expected paused, got %s", paused.Status)
	}
}

func TestManager_Resume(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, _ := m.Create(ctx, "https://example.com/file.zip")
	m.Pause(ctx, j.ID)

	resumed, err := m.Resume(ctx, j.ID)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}
	if resumed.Status != StatusDownloading {
		t.Errorf("expected downloading, got %s", resumed.Status)
	}
}

func TestManager_Cancel(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, _ := m.Create(ctx, "https://example.com/file.zip")

	cancelled, err := m.Cancel(ctx, j.ID)
	if err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}
	if cancelled.Status != StatusCancelled {
		t.Errorf("expected cancelled, got %s", cancelled.Status)
	}
}

func TestManager_Retry(t *testing.T) {
	m, fakeEng, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	// Create a job that fails at engine start
	callCount := 0
	fakeEng.startFunc = func(ctx context.Context, j *Job, dir string) (string, error) {
		callCount++
		if callCount == 1 {
			return "", fmt.Errorf("connection refused")
		}
		return "new-gid-456", nil
	}

	j, _ := m.Create(ctx, "https://example.com/file.zip")
	if j.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", j.Status)
	}

	// Retry
	retried, err := m.Retry(ctx, j.ID)
	if err != nil {
		t.Fatalf("Retry failed: %v", err)
	}
	if retried.Status != StatusDownloading {
		t.Errorf("expected downloading after retry, got %s", retried.Status)
	}
	if retried.EngineID != "new-gid-456" {
		t.Errorf("expected new engine ID, got %s", retried.EngineID)
	}
	if retried.Error != "" {
		t.Errorf("expected error to be cleared, got %s", retried.Error)
	}
	if retried.ID != j.ID {
		t.Errorf("expected same job ID, got different")
	}
}

func TestManager_InvalidStateTransition(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, _ := m.Create(ctx, "https://example.com/file.zip")
	m.Cancel(ctx, j.ID)

	// Try to pause a cancelled job
	_, err := m.Pause(ctx, j.ID)
	if err == nil {
		t.Error("expected error when pausing cancelled job")
	}

	// Try to resume a cancelled job
	_, err = m.Resume(ctx, j.ID)
	if err == nil {
		t.Error("expected error when resuming cancelled job")
	}
}

func TestManager_EngineFailure(t *testing.T) {
	m, fakeEng, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, _ := m.Create(ctx, "https://example.com/file.zip")

	// Make pause fail
	fakeEng.pauseFunc = func(ctx context.Context, j *Job) error {
		return fmt.Errorf("engine unavailable")
	}

	_, err := m.Pause(ctx, j.ID)
	if err == nil {
		t.Error("expected error when engine fails")
	}
}

func TestManager_List(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	m.Create(ctx, "https://example.com/a.zip")
	m.Create(ctx, "https://example.com/b.zip")

	jobs, err := m.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("expected 2 jobs, got %d", len(jobs))
	}
}

func TestManager_Get_NotFound(t *testing.T) {
	m, _, _, cleanup := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j, err := m.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if j != nil {
		t.Errorf("expected nil, got %v", j)
	}
}
