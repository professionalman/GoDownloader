package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

// fakeEngine is a test double for Engine.
type fakeEngine struct {
	startFunc  func(ctx context.Context, j *Job, dir string) (string, error)
	pauseFunc  func(ctx context.Context, j *Job) error
	resumeFunc func(ctx context.Context, j *Job) error
	cancelFunc func(ctx context.Context, j *Job) error
	statusFunc func(ctx context.Context, j *Job) (*EngineStatus, error)
}

func (f *fakeEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{Pause: true, Resume: true, Cancel: true, Retry: true}
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

func (f *fakeEngine) Get(name string) (IEngine, bool) {
	return f, true
}

func (f *fakeEngine) Detect(url string) string {
	if strings.HasPrefix(url, "magnet:") || strings.HasPrefix(url, "torrent://") {
		return "qbittorrent"
	}
	return "aria2"
}

type fakeTorrentEngine struct {
	*fakeEngine
	isStopped          bool
	addMagnetFunc      func(magnet string) (string, error)
	addTorrentFileFunc func(path string) (string, error)
	getOwnershipFunc   func(hash string) (*TorrentOwnership, error)
	adoptTorrentFunc   func(hash, jobID string) error
	getFilesFunc       func(hash string) ([]TorrentFile, error)
	setPrioritiesFunc  func(hash string) error
	startDownloadFunc  func(hash string) error
	stopDownloadFunc   func(hash string) error
	removeTorrentFunc  func(hash string, deleteFiles bool) error
	getTorrentInfoFunc func(hash string) (*TorrentInfo, error)
	statusFunc         func(ctx context.Context, j *Job) (*EngineStatus, error)
}

func (f *fakeTorrentEngine) GetTorrentOwnership(ctx context.Context, infoHash string) (*TorrentOwnership, error) {
	if f.getOwnershipFunc != nil {
		return f.getOwnershipFunc(infoHash)
	}
	return nil, nil
}

func (f *fakeTorrentEngine) AdoptTorrent(ctx context.Context, infoHash, jobID string) error {
	if f.adoptTorrentFunc != nil {
		return f.adoptTorrentFunc(infoHash, jobID)
	}
	return nil
}

func (f *fakeTorrentEngine) GetRawState(ctx context.Context, infoHash string) (string, error) {
	if f.isStopped {
		return "pausedDL", nil
	}
	return "downloading", nil
}

func (f *fakeTorrentEngine) Cancel(ctx context.Context, j *Job) error {
	if f.cancelFunc != nil {
		return f.cancelFunc(ctx, j)
	}
	if f.removeTorrentFunc != nil && j.EngineID != "" {
		return f.removeTorrentFunc(j.EngineID, false)
	}
	return nil
}

func (f *fakeTorrentEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	if f.statusFunc != nil {
		return f.statusFunc(ctx, j)
	}
	if f.fakeEngine != nil && f.fakeEngine.statusFunc != nil {
		return f.fakeEngine.statusFunc(ctx, j)
	}
	raw := "downloading"
	if f.isStopped {
		raw = "pausedDL"
	}
	return &EngineStatus{Status: StatusDownloading, RawState: raw}, nil
}

func (f *fakeTorrentEngine) AddMagnet(ctx context.Context, magnet, savePath, jobID string) (string, error) {
	if f.addMagnetFunc != nil {
		return f.addMagnetFunc(magnet)
	}
	return "fake-hash", nil
}
func (f *fakeTorrentEngine) AddTorrentFile(ctx context.Context, filePath, savePath, jobID string) (string, error) {
	if f.addTorrentFileFunc != nil {
		return f.addTorrentFileFunc(filePath)
	}
	return "fake-hash", nil
}
func (f *fakeTorrentEngine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error) {
	if f.getFilesFunc != nil {
		return f.getFilesFunc(infoHash)
	}
	return []TorrentFile{
		{Index: 0, Path: "file1.bin", Size: 1024, Priority: PriorityNormal, Selected: true},
	}, nil
}
func (f *fakeTorrentEngine) SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error {
	if f.setPrioritiesFunc != nil {
		return f.setPrioritiesFunc(infoHash)
	}
	return nil
}
func (f *fakeTorrentEngine) StartDownload(ctx context.Context, infoHash string) error {
	f.isStopped = false
	if f.startDownloadFunc != nil {
		return f.startDownloadFunc(infoHash)
	}
	return nil
}
func (f *fakeTorrentEngine) StopDownload(ctx context.Context, infoHash string) error {
	f.isStopped = true
	if f.stopDownloadFunc != nil {
		return f.stopDownloadFunc(infoHash)
	}
	return nil
}
func (f *fakeTorrentEngine) RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error {
	if f.removeTorrentFunc != nil {
		return f.removeTorrentFunc(infoHash, deleteFiles)
	}
	return nil
}
func (f *fakeTorrentEngine) GetTorrentInfo(ctx context.Context, infoHash string) (*TorrentInfo, error) {
	if f.getTorrentInfoFunc != nil {
		return f.getTorrentInfoFunc(infoHash)
	}
	return &TorrentInfo{Name: "fake-torrent", TotalSize: 100}, nil
}
func (f *fakeTorrentEngine) HealthCheck(ctx context.Context) error {
	return nil
}

var (
	_ IEngine         = (*fakeEngine)(nil)
	_ ITorrentEngine  = (*fakeTorrentEngine)(nil)
	_ IEngineRegistry = (*fakeEngineRegistry)(nil)
	_ IJobRepository  = (*fakeJobRepository)(nil)
	_ IEventBus       = (*fakeEventBus)(nil)
)

type fakeEngineRegistry struct {
	engines map[string]IEngine
}

func (r *fakeEngineRegistry) Get(name string) (IEngine, bool) {
	e, ok := r.engines[name]
	return e, ok
}

func (r *fakeEngineRegistry) Detect(url string) string {
	if strings.HasPrefix(url, "magnet:") || strings.HasPrefix(url, "torrent://") {
		return "qbittorrent"
	}
	return "aria2"
}

type fakeJobRepository struct {
	mu        sync.Mutex
	jobs      map[string]*Job
	updateErr error
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
	if f.updateErr != nil {
		return f.updateErr
	}
	if _, exists := f.jobs[j.ID]; !exists {
		return fmt.Errorf("job not found")
	}
	jCopy := *j
	f.jobs[j.ID] = &jCopy
	return nil
}

func (f *fakeJobRepository) UpdateJobPriorityAndQueuePosition(ctx context.Context, jobID string, newPriority JobPriority, newPosition int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j, exists := f.jobs[jobID]
	if !exists {
		return fmt.Errorf("job not found")
	}
	j.Priority = newPriority
	j.UpdatedAt = time.Now()
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
	var list []Job
	for _, j := range f.jobs {
		if j.Status == StatusDownloading || j.Status == StatusQueued || j.Status == StatusAnalyzing || j.Status == StatusAwaitingSelection || j.Status == StatusSeeding {
			list = append(list, *j)
		}
	}
	return list, nil
}

func (f *fakeJobRepository) CountDownloading(ctx context.Context) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, j := range f.jobs {
		if j.Status == StatusDownloading {
			count++
		}
	}
	return count, nil
}

func (f *fakeJobRepository) ListPendingEngineCleanups(ctx context.Context) ([]Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var list []Job
	for _, j := range f.jobs {
		if j.Status == StatusCompleted && (j.Type == TypeTorrent || j.Engine == "qbittorrent") && j.EngineCleanupPending {
			list = append(list, *j)
		}
	}
	return list, nil
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

type fakeTorrentRepository struct {
	mu           sync.Mutex
	jobRepo      IJobRepository
	queueRepo    IQueueRepository
	torrentJobs  map[string]*TorrentJobRecord
	torrentFiles map[string][]TorrentFileRecord
	getActiveErr error
	getErr       error
	createErr    error
	updateErr    error
	finalizeErr  error
}

func newFakeTorrentRepository(jobRepo IJobRepository, qRepo ...IQueueRepository) *fakeTorrentRepository {
	var q IQueueRepository
	if len(qRepo) > 0 {
		q = qRepo[0]
	}
	return &fakeTorrentRepository{
		jobRepo:      jobRepo,
		queueRepo:    q,
		torrentJobs:  make(map[string]*TorrentJobRecord),
		torrentFiles: make(map[string][]TorrentFileRecord),
	}
}

func (f *fakeTorrentRepository) CreateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.torrentJobs[rec.JobID] = cloneTorrentRecord(rec)
	return nil
}

func (f *fakeTorrentRepository) CreateTorrentJobAtomic(ctx context.Context, j *Job, rec *TorrentJobRecord) error {
	if j == nil {
		return fmt.Errorf("job is required")
	}
	if rec == nil {
		return fmt.Errorf("torrent record is required")
	}
	if j.ID == "" {
		return fmt.Errorf("job ID is required")
	}
	if rec.JobID == "" {
		return fmt.Errorf("torrent record job ID is required")
	}
	if rec.JobID != j.ID {
		return fmt.Errorf("torrent record job ID (%s) does not match job ID (%s)", rec.JobID, j.ID)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	if f.jobRepo != nil {
		if err := f.jobRepo.Create(ctx, j); err != nil {
			return err
		}
	}
	f.torrentJobs[rec.JobID] = cloneTorrentRecord(rec)
	return nil
}

func (f *fakeTorrentRepository) GetTorrentJob(ctx context.Context, jobID string) (*TorrentJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	rec, ok := f.torrentJobs[jobID]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return rec, nil
}

func (f *fakeTorrentRepository) UpdateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return f.updateErr
	}
	f.torrentJobs[rec.JobID] = cloneTorrentRecord(rec)
	return nil
}

func (f *fakeTorrentRepository) DeleteTorrentJob(ctx context.Context, jobID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.torrentJobs, jobID)
	delete(f.torrentFiles, jobID)
	return nil
}

func (f *fakeTorrentRepository) GetTorrentJobByInfoHash(ctx context.Context, infoHash string) (*TorrentJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, rec := range f.torrentJobs {
		if rec.InfoHash == infoHash {
			return rec, nil
		}
	}
	return nil, nil
}

func (f *fakeTorrentRepository) GetActiveTorrentJobByInfoHash(ctx context.Context, infoHash string) (*TorrentJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getActiveErr != nil {
		return nil, f.getActiveErr
	}
	for _, rec := range f.torrentJobs {
		if rec.InfoHash == infoHash {
			if f.jobRepo != nil {
				j, _ := f.jobRepo.GetByID(ctx, rec.JobID)
				if j != nil && j.Status != StatusFailed && j.Status != StatusCancelled && j.Status != StatusCompleted {
					return rec, nil
				}
			} else {
				return rec, nil
			}
		}
	}
	return nil, nil
}

func (f *fakeTorrentRepository) FinalizeTorrent(ctx context.Context, j *Job, stopReason string) error {
	if f.finalizeErr != nil {
		return f.finalizeErr
	}
	if err := f.jobRepo.Update(ctx, j); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec := f.torrentJobs[j.ID]; rec != nil {
		rec.SeedAfterComplete = j.SeedAfterComplete
		rec.SeedingPolicy = cloneSeedingPolicy(j.SeedingPolicy)
		rec.SeedingStartedAt = cloneTimePointer(j.SeedingStartedAt)
		rec.SeedingStopReason = stopReason
		rec.SeedingReconcilePending = false
	}
	return nil
}

func (f *fakeTorrentRepository) SaveTorrentFiles(ctx context.Context, jobID string, files []TorrentFileRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrentFiles[jobID] = files
	return nil
}

func (f *fakeTorrentRepository) GetTorrentFiles(ctx context.Context, jobID string) ([]TorrentFileRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.torrentFiles[jobID], nil
}

func (f *fakeTorrentRepository) UpdateTorrentFileSelections(ctx context.Context, jobID string, selections []TorrentFileRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrentFiles[jobID] = selections
	return nil
}

func (f *fakeTorrentRepository) PersistTorrentSelectionAndEnqueue(ctx context.Context, j *Job, selections []TorrentFileRecord, rec *TorrentJobRecord, qe *QueueEntry) error {
	f.mu.Lock()
	if f.updateErr != nil {
		f.mu.Unlock()
		return f.updateErr
	}
	f.torrentFiles[j.ID] = selections
	if rec != nil {
		f.torrentJobs[j.ID] = cloneTorrentRecord(rec)
	}
	qRepo := f.queueRepo
	f.mu.Unlock()

	if f.jobRepo != nil {
		if err := f.jobRepo.Update(ctx, j); err != nil {
			return err
		}
	}
	if qe != nil && qRepo != nil {
		if err := qRepo.Enqueue(ctx, qe); err != nil {
			return err
		}
	}
	return nil
}

var _ ITorrentRepository = (*fakeTorrentRepository)(nil)

func setupManagerTest(t *testing.T) (*Manager, *fakeEngine, *fakeEventBus, func(), *fakeTorrentEngine) {
	t.Helper()
	tmpDir := t.TempDir()
	repo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(repo)
	fakeEng := &fakeEngine{}
	fakeTorrentEng := &fakeTorrentEngine{fakeEngine: fakeEng}

	registry := &fakeEngineRegistry{
		engines: map[string]IEngine{
			"aria2":       fakeEng,
			"qbittorrent": fakeTorrentEng,
		},
	}

	bus := newFakeEventBus()
	downloadDir := filepath.Join(tmpDir, "downloads")
	dataDir := filepath.Join(tmpDir, "data")
	os.MkdirAll(downloadDir, 0755)
	os.MkdirAll(dataDir, 0755)

	m := NewManager(repo, registry, bus, downloadDir, torrentRepo, dataDir)

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return m, fakeEng, bus, cleanup, fakeTorrentEng
}

func TestManager_Create(t *testing.T) {
	m, _, bus, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, fakeEng, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, fakeEng, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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
	m, _, _, cleanup, _ := setupManagerTest(t)
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

func TestManager_CreateMagnet(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		return "1234abcd", nil
	}
	fakeTorrent.getTorrentInfoFunc = func(hash string) (*TorrentInfo, error) {
		return &TorrentInfo{Name: "test.iso", TotalSize: 1024}, nil
	}

	j, err := m.Create(ctx, "magnet:?xt=urn:btih:1234abcd")
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if j.Type != TypeTorrent {
		t.Errorf("expected TypeTorrent, got %s", j.Type)
	}
	if j.Status != StatusAnalyzing {
		t.Errorf("expected StatusAnalyzing, got %s", j.Status)
	}

	// Wait a moment for background metadata acquisition to finish
	time.Sleep(1500 * time.Millisecond)

	updatedJ, _ := m.Get(ctx, j.ID)
	if updatedJ.Status != StatusAwaitingSelection {
		t.Errorf("expected StatusAwaitingSelection, got %s", updatedJ.Status)
	}
	if updatedJ.Name != "test.iso" {
		t.Errorf("expected test.iso, got %s", updatedJ.Name)
	}
	if updatedJ.EngineID != "1234abcd" {
		t.Errorf("expected 1234abcd, got %s", updatedJ.EngineID)
	}
}

func TestManager_StartTorrent(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		return "1234abcd", nil
	}
	fakeTorrent.getTorrentInfoFunc = func(hash string) (*TorrentInfo, error) {
		return &TorrentInfo{Name: "test.iso", TotalSize: 1024}, nil
	}

	j, _ := m.Create(ctx, "magnet:?xt=urn:btih:1234abcd")

	time.Sleep(1500 * time.Millisecond) // await analysis

	var prioritiesSet bool
	fakeTorrent.setPrioritiesFunc = func(hash string) error {
		prioritiesSet = true
		return nil
	}
	var started bool
	fakeTorrent.startDownloadFunc = func(hash string) error {
		started = true
		return nil
	}

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
	}

	startedJ, err := m.StartTorrent(ctx, j.ID, selections, true)
	if err != nil {
		t.Fatalf("StartTorrent failed: %v", err)
	}

	if startedJ.Status != StatusDownloading {
		t.Errorf("expected StatusDownloading, got %s", startedJ.Status)
	}
	if !prioritiesSet {
		t.Errorf("expected priorities to be set")
	}
	if !started {
		t.Errorf("expected download to be started")
	}
}

func TestManager_StartTorrent_Validation(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		return "1234abcd", nil
	}
	fakeTorrent.getTorrentInfoFunc = func(hash string) (*TorrentInfo, error) {
		return &TorrentInfo{Name: "test.iso", TotalSize: 1024}, nil
	}

	j, _ := m.Create(ctx, "magnet:?xt=urn:btih:1234abcd")
	time.Sleep(1500 * time.Millisecond) // await analysis

	// 1. Test zero selected files (all skip)
	allSkip := []TorrentFileSelection{{Index: 0, Priority: PrioritySkip}}
	_, err := m.StartTorrent(ctx, j.ID, allSkip, false)
	if err == nil {
		t.Error("expected error for zero selected files, got nil")
	}

	// 2. Test unknown file index
	unknownIndex := []TorrentFileSelection{{Index: 99, Priority: PriorityNormal}}
	_, err = m.StartTorrent(ctx, j.ID, unknownIndex, false)
	if err == nil {
		t.Error("expected error for unknown file index 99, got nil")
	}

	// 3. Test duplicate file index
	duplicateIndex := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 0, Priority: PriorityHigh},
	}
	_, err = m.StartTorrent(ctx, j.ID, duplicateIndex, false)
	if err == nil {
		t.Error("expected error for duplicate file index 0, got nil")
	}

	// 4. Test invalid priority string
	invalidPriority := []TorrentFileSelection{{Index: 0, Priority: TorrentFilePriority("super_high")}}
	_, err = m.StartTorrent(ctx, j.ID, invalidPriority, false)
	if err == nil {
		t.Error("expected error for invalid priority 'super_high', got nil")
	}
}

func TestManager_CreateTorrentJob_DuplicateHash(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		return "1234567890abcdef1234567890abcdef12345678", nil
	}

	magnet := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678"
	j1, err := m.Create(ctx, magnet)
	if err != nil {
		t.Fatalf("first Create failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	// Second create with same info hash should fail with duplicate hash error
	_, err = m.Create(ctx, magnet)
	if err == nil {
		t.Error("expected error creating duplicate torrent job, got nil")
	}
	_ = j1
}

func TestManager_StopSeeding(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j := &Job{
		ID:        "job-seeding-1",
		Status:    StatusSeeding,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "1234abcd",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.addActive(j)

	var stopCalled bool
	var removeCalled bool
	fakeTorrent.stopDownloadFunc = func(hash string) error {
		stopCalled = true
		return nil
	}
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}

	stoppedJ, err := m.StopSeeding(ctx, "job-seeding-1")
	if err != nil {
		t.Fatalf("StopSeeding failed: %v", err)
	}

	if stoppedJ.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted, got %s", stoppedJ.Status)
	}
	if !stopCalled {
		t.Error("expected StopDownload to be called")
	}
	if !removeCalled {
		t.Error("expected RemoveTorrent to be called")
	}
}

func TestManager_CreateTorrentFromFile_PersistedStorage(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
		return "abcd1234", nil
	}

	// Create a dummy temp torrent file
	tmpFile, err := os.CreateTemp("", "test-*.torrent")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.WriteString("dummy torrent content")
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	j, err := m.CreateTorrentFromFile(ctx, tmpPath)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile failed: %v", err)
	}

	if j.Type != TypeTorrent {
		t.Errorf("expected TypeTorrent, got %s", j.Type)
	}

	// Verify temp file was removed
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected temp file %s to be deleted", tmpPath)
	}

	// Verify persisted file exists in dataDir/torrents/
	persistedPath := filepath.Join(m.dataDir, "torrents", j.ID+".torrent")
	if _, err := os.Stat(persistedPath); os.IsNotExist(err) {
		t.Errorf("expected persisted file %s to exist", persistedPath)
	}
}

func TestManager_TorrentRetryWithPersistedFile(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var addedMu sync.Mutex
	var addedFilePath string
	fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
		addedMu.Lock()
		addedFilePath = path
		addedMu.Unlock()
		return "hash-retry-1", nil
	}

	// 1. Create .torrent job
	tmpFile, err := os.CreateTemp("", "test-retry-*.torrent")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.WriteString("dummy torrent payload for retry")
	tmpPath := tmpFile.Name()
	tmpFile.Close()

	j, err := m.CreateTorrentFromFile(ctx, tmpPath)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile failed: %v", err)
	}

	// 2. Wait for metadata acquisition
	time.Sleep(1500 * time.Millisecond)

	// 3. Assert TorrentJobRecord has TorrentFilePath set and file exists
	rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
	if err != nil || rec == nil {
		t.Fatalf("expected TorrentJobRecord to exist, got err %v", err)
	}
	if rec.TorrentFilePath == "" {
		t.Fatal("expected TorrentFilePath to be non-empty in TorrentJobRecord")
	}
	if _, err := os.Stat(rec.TorrentFilePath); os.IsNotExist(err) {
		t.Fatalf("expected persisted torrent file at %s to exist", rec.TorrentFilePath)
	}

	// 4. Simulate FAILED status
	j.Status = StatusFailed
	j.Error = "Simulated network failure"
	m.repo.Update(ctx, j)

	// 5. Call Retry()
	addedMu.Lock()
	addedFilePath = ""
	addedMu.Unlock()

	retriedJ, err := m.Retry(ctx, j.ID)
	if err != nil {
		t.Fatalf("Retry failed: %v", err)
	}
	if retriedJ.Status != StatusAnalyzing {
		t.Errorf("expected StatusAnalyzing after retry, got %s", retriedJ.Status)
	}

	// Wait for retry acquireTorrentMetadata
	time.Sleep(1500 * time.Millisecond)

	// 6. Verify AddTorrentFile received the persisted DATA_DIR path
	addedMu.Lock()
	gotPath := addedFilePath
	addedMu.Unlock()

	if gotPath != rec.TorrentFilePath {
		t.Errorf("expected AddTorrentFile to receive persisted path %s, got %s", rec.TorrentFilePath, gotPath)
	}
}

func TestManager_DuplicateInfoHashCombinations(t *testing.T) {
	t.Run("Test A: same magnet twice", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return "1111111111abcdef1234567890abcdef12345678", nil
		}

		magnet := "magnet:?xt=urn:btih:1111111111abcdef1234567890abcdef12345678"
		j1, err := m.Create(ctx, magnet)
		if err != nil {
			t.Fatalf("first magnet create failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		gotJ1, _ := m.repo.GetByID(ctx, j1.ID)
		if gotJ1.Status != StatusAwaitingSelection {
			t.Fatalf("expected Job A to be StatusAwaitingSelection, got %s", gotJ1.Status)
		}

		// Attempt duplicate magnet creation
		_, err = m.Create(ctx, magnet)
		if err == nil {
			t.Error("expected second magnet creation to fail pre-check with error")
		}

		// Assert Job A is untouched and RemoveTorrent was NOT called
		gotJ1After, _ := m.repo.GetByID(ctx, j1.ID)
		if gotJ1After.Status != StatusAwaitingSelection {
			t.Errorf("expected Job A to remain StatusAwaitingSelection, got %s", gotJ1After.Status)
		}
		if removeCalled {
			t.Error("RemoveTorrent must NOT be called when duplicate is rejected")
		}
	})

	t.Run("Test B: same torrent file twice", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}
		fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
			return "1234567890abcdef1234567890abcdef12345678", nil
		}

		tmp1, _ := os.CreateTemp("", "dup1-*.torrent")
		tmp1.WriteString("content1")
		tmpPath1 := tmp1.Name()
		tmp1.Close()

		j1, err := m.CreateTorrentFromFile(ctx, tmpPath1)
		if err != nil {
			t.Fatalf("first CreateTorrentFromFile failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond) // await analysis & metadata save

		tmp2, _ := os.CreateTemp("", "dup2-*.torrent")
		tmp2.WriteString("content2")
		tmpPath2 := tmp2.Name()
		tmp2.Close()

		j2, err := m.CreateTorrentFromFile(ctx, tmpPath2)
		if err != nil {
			t.Fatalf("second CreateTorrentFromFile failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond) // await analysis

		gotJ1, _ := m.repo.GetByID(ctx, j1.ID)
		if gotJ1.Status != StatusAwaitingSelection {
			t.Errorf("expected Job A to remain StatusAwaitingSelection, got %s", gotJ1.Status)
		}

		gotJ2, _ := m.repo.GetByID(ctx, j2.ID)
		if gotJ2.Status != StatusFailed {
			t.Errorf("expected duplicate torrent file job to be marked StatusFailed, got %s", gotJ2.Status)
		}
		if removeCalled {
			t.Error("RemoveTorrent must NOT be called when duplicate torrent file is rejected")
		}
	})

	t.Run("Test C: magnet then equivalent torrent file", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return "1234567890abcdef1234567890abcdef12345678", nil
		}
		fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
			return "1234567890abcdef1234567890abcdef12345678", nil
		}

		magnet := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678"
		j1, err := m.Create(ctx, magnet)
		if err != nil {
			t.Fatalf("Create magnet failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		tmp, _ := os.CreateTemp("", "dup-*.torrent")
		tmp.WriteString("content")
		tmpPath := tmp.Name()
		tmp.Close()

		j2, err := m.CreateTorrentFromFile(ctx, tmpPath)
		if err != nil {
			t.Fatalf("CreateTorrentFromFile failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		gotJ1, _ := m.repo.GetByID(ctx, j1.ID)
		if gotJ1.Status != StatusAwaitingSelection {
			t.Errorf("expected Job A (magnet) to remain StatusAwaitingSelection, got %s", gotJ1.Status)
		}

		gotJ2, _ := m.repo.GetByID(ctx, j2.ID)
		if gotJ2.Status != StatusFailed {
			t.Errorf("expected duplicate torrent file after magnet to be marked StatusFailed, got %s", gotJ2.Status)
		}
		if removeCalled {
			t.Error("RemoveTorrent must NOT be called when duplicate torrent file is rejected")
		}
	})

	t.Run("Test D: torrent file then equivalent magnet", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}
		fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
			return "4444444444abcdef1234567890abcdef12345678", nil
		}
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return "4444444444abcdef1234567890abcdef12345678", nil
		}

		tmp, _ := os.CreateTemp("", "dup-*.torrent")
		tmp.WriteString("content")
		tmpPath := tmp.Name()
		tmp.Close()

		j1, err := m.CreateTorrentFromFile(ctx, tmpPath)
		if err != nil {
			t.Fatalf("CreateTorrentFromFile failed: %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		magnet := "magnet:?xt=urn:btih:4444444444abcdef1234567890abcdef12345678"
		_, err = m.Create(ctx, magnet)
		if err == nil {
			t.Error("expected magnet creation after torrent file to fail pre-check with error")
		}

		gotJ1, _ := m.repo.GetByID(ctx, j1.ID)
		if gotJ1.Status != StatusAwaitingSelection {
			t.Errorf("expected Job A (torrent file) to remain StatusAwaitingSelection, got %s", gotJ1.Status)
		}
		if removeCalled {
			t.Error("RemoveTorrent must NOT be called when duplicate magnet is rejected")
		}
	})

	t.Run("Test E: failed historical + active current Job", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		hash := "5555555555abcdef1234567890abcdef12345678"
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return hash, nil
		}

		magnet := "magnet:?xt=urn:btih:" + hash

		// Historical failed job A
		jA, _ := m.Create(ctx, magnet)
		time.Sleep(1500 * time.Millisecond)
		jA.Status = StatusFailed
		m.repo.Update(ctx, jA)

		// Active current job B
		jB, _ := m.Create(ctx, magnet)
		time.Sleep(1500 * time.Millisecond)
		gotJB, _ := m.repo.GetByID(ctx, jB.ID)
		if gotJB.Status != StatusAwaitingSelection {
			t.Fatalf("expected Job B to be StatusAwaitingSelection, got %s", gotJB.Status)
		}

		// Attempt Job C with same hash -> must be rejected
		_, err := m.Create(ctx, magnet)
		if err == nil {
			t.Error("expected Job C to be rejected because Job B is active")
		}

		gotJBAfter, _ := m.repo.GetByID(ctx, jB.ID)
		if gotJBAfter.Status != StatusAwaitingSelection {
			t.Errorf("expected Job B to remain StatusAwaitingSelection, got %s", gotJBAfter.Status)
		}
	})

	t.Run("Test F & G: failed or completed historical job allows re-adding", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		hash := "6666666666abcdef1234567890abcdef12345678"
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return hash, nil
		}

		magnet := "magnet:?xt=urn:btih:" + hash
		j1, _ := m.Create(ctx, magnet)
		time.Sleep(1500 * time.Millisecond)

		// Mark j1 as StatusCompleted
		j1.Status = StatusCompleted
		m.repo.Update(ctx, j1)

		// Now creating second job with same magnet should succeed
		j2, err := m.Create(ctx, magnet)
		if err != nil {
			t.Fatalf("expected creating magnet after historical completion to succeed, got %v", err)
		}
		time.Sleep(1500 * time.Millisecond)

		gotJ2, _ := m.repo.GetByID(ctx, j2.ID)
		if gotJ2.Status != StatusAwaitingSelection {
			t.Errorf("expected re-added job to reach StatusAwaitingSelection, got %s", gotJ2.Status)
		}
	})
}

func TestManager_UpdateJobFromEngine_SeedingPolicy(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalled bool
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}

	// 1. seedAfterComplete = false + engine StatusSeeding -> COMPLETED + RemoveTorrent called
	j1 := &Job{
		ID:                "job-seed-false",
		Status:            StatusDownloading,
		Type:              TypeTorrent,
		Engine:            "qbittorrent",
		EngineID:          "hash1",
		SeedAfterComplete: false,
	}
	m.repo.Create(ctx, j1)
	m.addActive(j1)

	statusSeeding := &EngineStatus{
		Status:      StatusSeeding,
		UploadSpeed: 500,
	}
	m.UpdateJobFromEngine(ctx, j1, statusSeeding, true)

	if j1.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted for seedAfterComplete=false, got %s", j1.Status)
	}
	if !removeCalled {
		t.Error("expected RemoveTorrent to be called for seedAfterComplete=false")
	}

	// 2. seedAfterComplete = true + engine StatusSeeding -> SEEDING + torrent remains
	removeCalled = false
	j2 := &Job{
		ID:                "job-seed-true",
		Status:            StatusDownloading,
		Type:              TypeTorrent,
		Engine:            "qbittorrent",
		EngineID:          "hash2",
		SeedAfterComplete: true,
	}
	m.repo.Create(ctx, j2)
	m.addActive(j2)

	m.UpdateJobFromEngine(ctx, j2, statusSeeding, true)

	if j2.Status != StatusSeeding {
		t.Errorf("expected StatusSeeding for seedAfterComplete=true, got %s", j2.Status)
	}
	if removeCalled {
		t.Error("RemoveTorrent should NOT be called for seedAfterComplete=true")
	}
}

func TestManager_StopSeeding_ErrorHandling(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j := &Job{
		ID:        "job-seeding-fail",
		Status:    StatusSeeding,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash-fail",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.addActive(j)

	// Simulate StopDownload failure
	fakeTorrent.stopDownloadFunc = func(hash string) error {
		return fmt.Errorf("rpc error")
	}

	_, err := m.StopSeeding(ctx, j.ID)
	if err == nil {
		t.Error("expected error from StopSeeding when StopDownload fails, got nil")
	}

	got, _ := m.Get(ctx, j.ID)
	if got.Status != StatusSeeding {
		t.Errorf("expected job status to remain SEEDING after failure, got %s", got.Status)
	}
}

func TestManager_UpdateJobFromEngine_RemoveTorrentFailure(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		return fmt.Errorf("daemon connection reset")
	}

	j := &Job{
		ID:                "job-remove-fail",
		Status:            StatusDownloading,
		Type:              TypeTorrent,
		Engine:            "qbittorrent",
		EngineID:          "hash-fail-1",
		SeedAfterComplete: false,
	}
	m.repo.Create(ctx, j)
	m.addActive(j)

	statusSeeding := &EngineStatus{
		Status:      StatusSeeding,
		UploadSpeed: 100,
	}
	m.UpdateJobFromEngine(ctx, j, statusSeeding, true)

	got, _ := m.Get(ctx, "job-remove-fail")
	if got.Status != StatusCompleted {
		t.Errorf("expected StatusCompleted when RemoveTorrent fails, got %s", got.Status)
	}
	if !got.EngineCleanupPending {
		t.Error("expected EngineCleanupPending == true when RemoveTorrent fails")
	}
}

func TestManager_DuplicateTorrentCanBeRetried(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalled bool
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}
	hash := "7777777777abcdef1234567890abcdef12345678"
	fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
		return hash, nil
	}

	tmp1, _ := os.CreateTemp("", "retry1-*.torrent")
	tmp1.WriteString("content1")
	tmpPath1 := tmp1.Name()
	tmp1.Close()

	// 1. Create Job A from .torrent
	jA, err := m.CreateTorrentFromFile(ctx, tmpPath1)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile jA failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	gotJA, _ := m.repo.GetByID(ctx, jA.ID)
	if gotJA.Status != StatusAwaitingSelection {
		t.Fatalf("expected Job A to reach StatusAwaitingSelection, got %s", gotJA.Status)
	}

	// 2. Create Job B from equivalent .torrent (same hash)
	tmp2, _ := os.CreateTemp("", "retry2-*.torrent")
	tmp2.WriteString("content2")
	tmpPath2 := tmp2.Name()
	tmp2.Close()

	jB, err := m.CreateTorrentFromFile(ctx, tmpPath2)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile jB failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	// 3. Verify Job B becomes FAILED while Job A remains unchanged & RemoveTorrent NOT called
	gotJBAfterFail, _ := m.repo.GetByID(ctx, jB.ID)
	if gotJBAfterFail.Status != StatusFailed {
		t.Fatalf("expected duplicate Job B to be StatusFailed, got %s", gotJBAfterFail.Status)
	}
	if removeCalled {
		t.Error("RemoveTorrent must NOT be called when duplicate is rejected")
	}

	// Verify Job B has TorrentJobRecord and its persisted file exists
	recB, err := m.torrentRepo.GetTorrentJob(ctx, jB.ID)
	if err != nil || recB == nil {
		t.Fatalf("expected TorrentJobRecord for duplicate Job B, got err=%v rec=%v", err, recB)
	}
	if recB.TorrentFilePath == "" {
		t.Error("expected Job B to have persisted TorrentFilePath")
	}
	if _, err := os.Stat(recB.TorrentFilePath); os.IsNotExist(err) {
		t.Errorf("expected Job B's .torrent file at %s to exist on disk", recB.TorrentFilePath)
	}

	// 4. Mark Job A as terminal (COMPLETED)
	gotJA.Status = StatusCompleted
	m.repo.Update(ctx, gotJA)

	// 5. Retry Job B
	var testMu sync.Mutex
	var addTorrentFileReceivedPath string
	var addMagnetCalled bool
	fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
		testMu.Lock()
		addTorrentFileReceivedPath = path
		testMu.Unlock()
		return hash, nil
	}
	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		testMu.Lock()
		addMagnetCalled = true
		testMu.Unlock()
		return hash, nil
	}

	retriedJB, err := m.Retry(ctx, jB.ID)
	if err != nil {
		t.Fatalf("Retry Job B failed: %v", err)
	}
	if retriedJB.Status != StatusAnalyzing {
		t.Errorf("expected Job B status to transition to StatusAnalyzing on Retry, got %s", retriedJB.Status)
	}

	deadline := time.Now().Add(5 * time.Second)
	var gotJBFinal *Job
	for time.Now().Before(deadline) {
		gotJBFinal, _ = m.repo.GetByID(ctx, jB.ID)
		if gotJBFinal != nil && gotJBFinal.Status == StatusAwaitingSelection {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// 6. Verify AddTorrentFile received preserved path, AddMagnet was NOT called, Job B reaches StatusAwaitingSelection
	testMu.Lock()
	gotPath := addTorrentFileReceivedPath
	gotMagnetCalled := addMagnetCalled
	testMu.Unlock()

	if gotMagnetCalled {
		t.Error("Retry must NOT call AddMagnet for a .torrent job")
	}
	if gotPath != recB.TorrentFilePath {
		t.Errorf("expected AddTorrentFile to receive preserved path %s, got %s", recB.TorrentFilePath, gotPath)
	}

	if gotJBFinal == nil || gotJBFinal.Status != StatusAwaitingSelection {
		statusStr := ""
		if gotJBFinal != nil {
			statusStr = string(gotJBFinal.Status)
		}
		t.Errorf("expected retried Job B to reach StatusAwaitingSelection, got %s", statusStr)
	}
}

func TestManager_ActiveLookupFailure_MagnetPreCheck(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var addMagnetCalled bool
	fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
		addMagnetCalled = true
		return "hash123", nil
	}

	mockRepo, ok := m.torrentRepo.(*fakeTorrentRepository)
	if !ok {
		t.Fatal("expected fakeTorrentRepository")
	}
	mockRepo.getActiveErr = fmt.Errorf("db disk error")

	magnet := "magnet:?xt=urn:btih:8888888888abcdef1234567890abcdef12345678"
	_, err := m.Create(ctx, magnet)
	if err == nil {
		t.Error("expected magnet creation to fail when active lookup fails")
	}
	if addMagnetCalled {
		t.Error("AddMagnet must NOT be called when ownership lookup fails during pre-check")
	}
}

func TestManager_ActiveLookupFailure_PostAdd(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalled bool
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalled = true
		return nil
	}
	hash := "9999999999abcdef1234567890abcdef12345678"
	fakeTorrent.addTorrentFileFunc = func(path string) (string, error) {
		return hash, nil
	}

	tmp, _ := os.CreateTemp("", "failpost-*.torrent")
	tmp.WriteString("content")
	tmpPath := tmp.Name()
	tmp.Close()

	mockRepo := m.torrentRepo.(*fakeTorrentRepository)
	mockRepo.getActiveErr = fmt.Errorf("db connection timeout")

	j, err := m.CreateTorrentFromFile(ctx, tmpPath)
	if err != nil {
		t.Fatalf("CreateTorrentFromFile failed: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	gotJ, _ := m.repo.GetByID(ctx, j.ID)
	if gotJ.Status != StatusFailed {
		t.Errorf("expected job to be StatusFailed when post-add ownership lookup fails, got %s", gotJ.Status)
	}
	if gotJ.Error == "" || !strings.Contains(gotJ.Error, "failed to verify torrent ownership") {
		t.Errorf("expected actionable error message, got %q", gotJ.Error)
	}
	if removeCalled {
		t.Error("RemoveTorrent must NOT be called when ownership cannot be verified")
	}

	rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID)
	if err != nil || rec == nil {
		t.Errorf("expected TorrentJobRecord to be saved on post-add lookup failure, got err=%v rec=%v", err, rec)
	}
}

func TestExtractMagnetHash(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
		wantErr  bool
	}{
		{
			name:     "40-char lowercase hex",
			input:    "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&dn=test",
			expected: "1234567890abcdef1234567890abcdef12345678",
			wantErr:  false,
		},
		{
			name:     "40-char uppercase hex",
			input:    "magnet:?xt=URN:BTIH:1234567890ABCDEF1234567890ABCDEF12345678&dn=test",
			expected: "1234567890abcdef1234567890abcdef12345678",
			wantErr:  false,
		},
		{
			name:     "32-char Base32",
			input:    "magnet:?xt=urn:btih:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&dn=test",
			expected: "0000000000000000000000000000000000000000",
			wantErr:  false,
		},
		{
			name:    "invalid 40-char non-hex",
			input:   "magnet:?xt=urn:btih:1234567890GBCDEF1234567890ABCDEF12345678",
			wantErr: true,
		},
		{
			name:    "invalid base32 chars",
			input:   "magnet:?xt=urn:btih:11111111111111111111111111111111",
			wantErr: true,
		},
		{
			name:    "missing btih",
			input:   "magnet:?xt=urn:other:1234",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractMagnetHash(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExtractMagnetHash(%q) err = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if got != tt.expected {
				t.Errorf("ExtractMagnetHash(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestManager_CancelEngineFailure(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	j := &Job{
		ID:        "job_cancel_fail",
		Source:    "https://example.com/file.zip",
		Status:    StatusDownloading,
		Engine:    "qbittorrent",
		EngineID:  "hash123",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)
	m.addActive(j)

	fakeTorrent.cancelFunc = func(ctx context.Context, j *Job) error {
		return fmt.Errorf("qBittorrent connection reset")
	}

	_, err := m.Cancel(ctx, j.ID)
	if err == nil {
		t.Fatal("expected Cancel to return error when engine cancel fails")
	}

	gotJ, _ := m.repo.GetByID(ctx, j.ID)
	if gotJ.Status == StatusCancelled {
		t.Errorf("job must NOT be marked StatusCancelled when engine cancel fails, got status %s", gotJ.Status)
	}
}

func TestManager_TorrentRetry_FailSafely(t *testing.T) {
	t.Run("GetTorrentJob error", func(t *testing.T) {
		m, _, _, cleanup, _ := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		j := &Job{
			ID:        "job_retry_err",
			Source:    "torrent://local/job_retry_err.torrent",
			Type:      TypeTorrent,
			Status:    StatusFailed,
			Engine:    "qbittorrent",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		m.repo.Create(ctx, j)

		mockRepo := m.torrentRepo.(*fakeTorrentRepository)
		mockRepo.getErr = fmt.Errorf("db connection closed")

		_, err := m.Retry(ctx, j.ID)
		if err == nil {
			t.Error("expected Retry to fail when GetTorrentJob returns DB error")
		}
	})

	t.Run("Record missing for torrent source", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var addMagnetCalled bool
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			addMagnetCalled = true
			return "hash", nil
		}

		j := &Job{
			ID:        "job_no_rec",
			Source:    "torrent://local/job_no_rec.torrent",
			Type:      TypeTorrent,
			Status:    StatusFailed,
			Engine:    "qbittorrent",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		m.repo.Create(ctx, j)

		_, err := m.Retry(ctx, j.ID)
		if err == nil {
			t.Error("expected Retry to fail when TorrentJobRecord is missing")
		}
		if addMagnetCalled {
			t.Error("AddMagnet must NOT be called on retry when record is missing")
		}
	})

	t.Run("File missing on disk for torrent source", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var addMagnetCalled bool
		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			addMagnetCalled = true
			return "hash", nil
		}

		j := &Job{
			ID:        "job_file_del",
			Source:    "torrent://local/job_file_del.torrent",
			Type:      TypeTorrent,
			Status:    StatusFailed,
			Engine:    "qbittorrent",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		m.repo.Create(ctx, j)
		m.torrentRepo.CreateTorrentJob(ctx, &TorrentJobRecord{
			JobID:           j.ID,
			TorrentFilePath: "C:/non_existent_dir/missing.torrent",
		})

		_, err := m.Retry(ctx, j.ID)
		if err == nil {
			t.Error("expected Retry to fail when metainfo file is missing from disk")
		}
		if addMagnetCalled {
			t.Error("AddMagnet must NOT be called on retry when file is missing from disk")
		}
	})
}

func TestManager_SeedPolicyGuard(t *testing.T) {
	t.Run("Seed=true + already SEEDING -> RemoveTorrent NOT called", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}

		j := &Job{
			ID:                "job_seed_guard",
			Type:              TypeTorrent,
			Status:            StatusSeeding,
			SeedAfterComplete: true,
			Engine:            "qbittorrent",
			EngineID:          "hash_seed_guard",
		}
		m.repo.Create(ctx, j)

		status := &EngineStatus{
			Status:   StatusCompleted,
			Progress: 100,
		}

		m.UpdateJobFromEngine(ctx, j, status, true)
		if removeCalled {
			t.Error("RemoveTorrent must NOT be called when SeedAfterComplete is true")
		}
		gotJ, _ := m.repo.GetByID(ctx, j.ID)
		if gotJ.Status != StatusSeeding {
			t.Errorf("expected job to remain StatusSeeding, got %s", gotJ.Status)
		}
	})

	t.Run("Seed=false + completion -> RemoveTorrent called -> COMPLETED", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalled bool
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalled = true
			return nil
		}

		j := &Job{
			ID:                "job_no_seed",
			Type:              TypeTorrent,
			Status:            StatusDownloading,
			SeedAfterComplete: false,
			Engine:            "qbittorrent",
			EngineID:          "hash_no_seed",
		}
		m.repo.Create(ctx, j)

		status := &EngineStatus{
			Status:   StatusCompleted,
			Progress: 100,
		}

		m.UpdateJobFromEngine(ctx, j, status, true)
		if !removeCalled {
			t.Error("RemoveTorrent MUST be called when SeedAfterComplete is false on completion")
		}
		gotJ, _ := m.repo.GetByID(ctx, j.ID)
		if gotJ.Status != StatusCompleted {
			t.Errorf("expected job to transition to StatusCompleted, got %s", gotJ.Status)
		}
	})
}

func TestManager_StopPreservesDaemonTorrent(t *testing.T) {
	m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
	defer cleanup()
	ctx := context.Background()

	var removeCalls int
	fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
		removeCalls++
		return nil
	}

	j := &Job{
		ID:        "job_stop_test",
		Source:    "magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
		Status:    StatusAnalyzing,
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "1111111111111111111111111111111111111111",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(ctx, j)

	var cancelCalled bool
	m.registerCancel(j.ID, func() {
		cancelCalled = true
	})

	// Perform backend shutdown
	m.Stop()

	if !cancelCalled {
		t.Error("expected Manager.Stop() to invoke local cancel callback")
	}
	if removeCalls != 0 {
		t.Errorf("RemoveTorrent must NOT be called on backend shutdown, got %d calls", removeCalls)
	}

	gotJ, _ := m.repo.GetByID(ctx, j.ID)
	if gotJ.Status != StatusAnalyzing {
		t.Errorf("expected job status to remain StatusAnalyzing on backend shutdown, got %s", gotJ.Status)
	}
}

func TestManager_CancelOrderingAndErrorHandling(t *testing.T) {
	t.Run("Engine cancel failure does not trigger local cancel or mark job cancelled", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var removeCalls int
		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			removeCalls++
			return fmt.Errorf("qBittorrent API error")
		}

		j := &Job{
			ID:        "job_order_fail",
			Source:    "magnet:?xt=urn:btih:2222222222222222222222222222222222222222",
			Status:    StatusDownloading,
			Type:      TypeTorrent,
			Engine:    "qbittorrent",
			EngineID:  "2222222222222222222222222222222222222222",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		m.repo.Create(ctx, j)
		m.addActive(j)

		var localCancelCalled bool
		m.registerCancel(j.ID, func() {
			localCancelCalled = true
		})

		_, err := m.Cancel(ctx, j.ID)
		if err == nil {
			t.Fatal("expected Cancel to fail when engine cancel returns error")
		}
		if localCancelCalled {
			t.Error("local cancel callback must NOT be triggered when engine cancel fails")
		}
		if removeCalls != 1 {
			t.Errorf("expected RemoveTorrent to be attempted exactly once, got %d", removeCalls)
		}

		gotJ, _ := m.repo.GetByID(ctx, j.ID)
		if gotJ.Status != StatusDownloading {
			t.Errorf("expected job status to remain StatusDownloading, got %s", gotJ.Status)
		}
	})

	t.Run("Successful cancel invokes engine cancel FIRST then local cancel", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		var sequence []string
		var mu sync.Mutex

		fakeTorrent.removeTorrentFunc = func(hash string, deleteFiles bool) error {
			mu.Lock()
			sequence = append(sequence, "engine")
			mu.Unlock()
			return nil
		}

		j := &Job{
			ID:        "job_order_succ",
			Source:    "magnet:?xt=urn:btih:3333333333333333333333333333333333333333",
			Status:    StatusDownloading,
			Type:      TypeTorrent,
			Engine:    "qbittorrent",
			EngineID:  "3333333333333333333333333333333333333333",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		m.repo.Create(ctx, j)
		m.addActive(j)

		m.registerCancel(j.ID, func() {
			mu.Lock()
			sequence = append(sequence, "local")
			mu.Unlock()
		})

		canceledJ, err := m.Cancel(ctx, j.ID)
		if err != nil {
			t.Fatalf("expected Cancel to succeed, got %v", err)
		}
		if canceledJ.Status != StatusCancelled {
			t.Errorf("expected status StatusCancelled, got %s", canceledJ.Status)
		}

		mu.Lock()
		gotSeq := append([]string(nil), sequence...)
		mu.Unlock()

		if len(gotSeq) != 2 || gotSeq[0] != "engine" || gotSeq[1] != "local" {
			t.Errorf("expected sequence [engine, local], got %v", gotSeq)
		}
	})
}

func TestManager_PauseActive_UpdateFailureReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	failRepo := &failingUpdateJobRepo{jobs: make(map[string]*Job)}
	fakeEng := &fakeEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"aria2": fakeEng}}
	bus := newFakeEventBus()

	m := NewManager(failRepo, reg, bus, tmpDir, nil)

	j := &Job{
		ID:        "job_pause_fail",
		Source:    "http://example.com/pause",
		Status:    StatusDownloading,
		Engine:    "aria2",
		EngineID:  "gid_123",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	failRepo.Create(context.Background(), j)
	m.addActive(j)

	_, err := m.Pause(context.Background(), j.ID)
	if err == nil {
		t.Fatal("expected Pause to return error when DB update fails, got nil")
	}

	events := bus.events
	for _, ev := range events {
		if ev.Type == EventJobUpdated && ev.Job.ID == j.ID && ev.Job.Status == StatusPaused {
			t.Errorf("expected EventJobUpdated(PAUSED) to NOT be published on DB failure")
		}
	}
}

func TestManager_SetPriority_AtomicRollback(t *testing.T) {
	ctx := context.Background()
	j := &Job{
		ID:        "prio_rollback_job",
		Source:    "http://example.com/roll",
		Status:    StatusQueued,
		Priority:  JobPriorityNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	failRepo := &failingUpdateJobRepo{jobs: map[string]*Job{j.ID: j}}
	mockQueueRepo := &fakeQueueRepo{
		entries: map[string]*QueueEntry{
			j.ID: {JobID: j.ID, Position: 5, Action: QueueActionStart},
		},
	}

	m := NewManager(failRepo, &fakeEngineRegistry{}, newFakeEventBus(), t.TempDir(), nil)
	m.SetQueueRepository(mockQueueRepo)

	_, err := m.SetJobPriority(ctx, j.ID, JobPriorityHigh)
	if err == nil {
		t.Fatal("expected SetJobPriority to fail when atomic update fails")
	}

	gotJ, _ := failRepo.GetByID(ctx, j.ID)
	if gotJ.Priority != JobPriorityNormal {
		t.Errorf("expected priority to remain NORMAL on failure, got %s", gotJ.Priority)
	}
}

func TestManager_SetPriority_QueueReadFailure(t *testing.T) {
	ctx := context.Background()
	j := &Job{
		ID:        "prio_read_fail_job",
		Source:    "http://example.com/read_fail",
		Status:    StatusQueued,
		Priority:  JobPriorityNormal,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	mockJobRepo := newFakeJobRepository()
	mockJobRepo.Create(ctx, j)

	failQueueRepo := &fakeQueueRepo{getErr: errors.New("simulated queue read error")}

	m := NewManager(mockJobRepo, &fakeEngineRegistry{}, newFakeEventBus(), t.TempDir(), nil)
	m.SetQueueRepository(failQueueRepo)

	_, err := m.SetJobPriority(ctx, j.ID, JobPriorityHigh)
	if err == nil {
		t.Fatal("expected SetJobPriority to fail when queue read fails")
	}

	gotJ, _ := mockJobRepo.GetByID(ctx, j.ID)
	if gotJ.Priority != JobPriorityNormal {
		t.Errorf("expected priority to remain NORMAL on queue read failure, got %s", gotJ.Priority)
	}
}

type fakeQueueRepo struct {
	entries    map[string]*QueueEntry
	getErr     error
	nextPosErr error
}

func (f *fakeQueueRepo) Enqueue(ctx context.Context, entry *QueueEntry) error {
	if f.entries == nil {
		f.entries = make(map[string]*QueueEntry)
	}
	f.entries[entry.JobID] = entry
	return nil
}
func (f *fakeQueueRepo) Get(ctx context.Context, jobID string) (*QueueEntry, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.entries[jobID], nil
}
func (f *fakeQueueRepo) Delete(ctx context.Context, jobID string) error {
	delete(f.entries, jobID)
	return nil
}
func (f *fakeQueueRepo) NextRunnable(ctx context.Context) (*QueuedJob, error) {
	if f.entries == nil {
		return nil, nil
	}
	for jobID, entry := range f.entries {
		return &QueuedJob{
			JobID:      jobID,
			Action:     entry.Action,
			Position:   entry.Position,
			EnqueuedAt: entry.EnqueuedAt,
			UpdatedAt:  entry.UpdatedAt,
		}, nil
	}
	return nil, nil
}
func (f *fakeQueueRepo) List(ctx context.Context) ([]QueuedJob, error) {
	return nil, nil
}
func (f *fakeQueueRepo) NextPosition(ctx context.Context, priority JobPriority) (int64, error) {
	if f.nextPosErr != nil {
		return 0, f.nextPosErr
	}
	return 10, nil
}
func (f *fakeQueueRepo) Reorder(ctx context.Context, priority JobPriority, orderedJobIDs []string) error {
	return nil
}

type failingUpdateJobRepo struct {
	mu   sync.Mutex
	jobs map[string]*Job
}

func (f *failingUpdateJobRepo) Create(ctx context.Context, j *Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.jobs == nil {
		f.jobs = make(map[string]*Job)
	}
	f.jobs[j.ID] = j
	return nil
}
func (f *failingUpdateJobRepo) Update(ctx context.Context, j *Job) error {
	return errors.New("simulated DB update failure")
}
func (f *failingUpdateJobRepo) UpdateJobPriorityAndQueuePosition(ctx context.Context, jobID string, newPriority JobPriority, newPosition int64) error {
	return errors.New("simulated DB atomic update failure")
}
func (f *failingUpdateJobRepo) GetByID(ctx context.Context, id string) (*Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jobs[id], nil
}
func (f *failingUpdateJobRepo) List(ctx context.Context) ([]Job, error) {
	return nil, nil
}
func (f *failingUpdateJobRepo) ListRecoverable(ctx context.Context) ([]Job, error) {
	return nil, nil
}
func (f *failingUpdateJobRepo) CountDownloading(ctx context.Context) (int, error) {
	return 0, nil
}
func (f *failingUpdateJobRepo) ListPendingEngineCleanups(ctx context.Context) ([]Job, error) {
	return nil, nil
}

func TestEndToEnd_UploadedTorrent(t *testing.T) {
	m, _, _, cleanup, fakeTorrentEng := setupManagerTest(t)
	defer cleanup()

	addTorrentFileCalled := false
	fakeTorrentEng.addTorrentFileFunc = func(filePath string) (string, error) {
		addTorrentFileCalled = true
		return "hash999", nil
	}

	tempTorrent := filepath.Join(t.TempDir(), "input.torrent")
	if err := os.WriteFile(tempTorrent, []byte("d8:announce3:url7:filesizede"), 0644); err != nil {
		t.Fatalf("failed to create temp torrent file: %v", err)
	}

	j, err := m.CreateTorrentFromFileWithOptions(context.Background(), tempTorrent, CreateOptions{Priority: JobPriorityNormal})
	if err != nil {
		t.Fatalf("expected CreateTorrentFromFileWithOptions to succeed, got %v", err)
	}

	if j.Engine != "qbittorrent" {
		t.Errorf("expected engine qbittorrent, got %s", j.Engine)
	}
	if j.Type != TypeTorrent {
		t.Errorf("expected type torrent, got %s", j.Type)
	}
	if !strings.HasPrefix(j.Source, "torrent://") {
		t.Errorf("expected source starting with torrent://, got %s", j.Source)
	}

	deadline := time.Now().Add(5 * time.Second)
	var updatedJob *Job
	for time.Now().Before(deadline) {
		updatedJob, err = m.repo.GetByID(context.Background(), j.ID)
		if updatedJob != nil && updatedJob.EngineID == "hash999" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !addTorrentFileCalled {
		t.Errorf("expected AddTorrentFile to be invoked on fake qBittorrent engine")
	}
	if updatedJob == nil || updatedJob.EngineID != "hash999" {
		gotEngineID := ""
		if updatedJob != nil {
			gotEngineID = updatedJob.EngineID
		}
		t.Errorf("expected infoHash hash999 persisted on job, got %s", gotEngineID)
	}
}

func TestCreateTorrentFromFile_CleanupOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	repo := newFakeJobRepository()
	bus := newFakeEventBus()
	registry := &fakeEngineRegistry{
		engines: map[string]IEngine{
			"aria2": &fakeEngine{},
		},
	}
	m := NewManager(repo, registry, bus, tmpDir, newFakeTorrentRepository(repo), tmpDir)

	tempTorrent := filepath.Join(tmpDir, "input.torrent")
	if err := os.WriteFile(tempTorrent, []byte("d8:announce3:url7:filesizede"), 0644); err != nil {
		t.Fatalf("failed to create temp torrent file: %v", err)
	}

	_, err := m.CreateTorrentFromFileWithOptions(context.Background(), tempTorrent, CreateOptions{Priority: JobPriorityNormal})
	if err == nil {
		t.Fatalf("expected CreateTorrentFromFileWithOptions to fail when qbittorrent engine is missing")
	}

	torrentsDir := filepath.Join(m.dataDir, "torrents")
	entries, _ := os.ReadDir(torrentsDir)
	if len(entries) > 0 {
		t.Errorf("expected 0 leaked torrent files in %s, found %d", torrentsDir, len(entries))
	}
}

func TestManager_MediaFinalization_NeverPicksUnrelatedLargerFile(t *testing.T) {
	m, _, _, cleanup, _ := setupManagerTest(t)
	defer cleanup()

	workDir := filepath.Join(t.TempDir(), "work_job123")
	if err := os.MkdirAll(workDir, 0755); err != nil {
		t.Fatalf("failed to create workdir: %v", err)
	}

	unrelatedFile := filepath.Join(workDir, "huge_unrelated_video.mp4")
	if err := os.WriteFile(unrelatedFile, make([]byte, 100*1024), 0644); err != nil {
		t.Fatalf("failed to write dummy large file: %v", err)
	}

	j := &Job{
		ID:        "job123",
		Engine:    "ytdlp",
		Type:      TypeMedia,
		Status:    StatusDownloading,
		WorkDir:   workDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.repo.Create(context.Background(), j)

	status := &EngineStatus{
		Status:     StatusCompleted,
		OutputPath: "",
	}

	m.UpdateJobFromEngine(context.Background(), j, status, true)

	updatedJob, err := m.repo.GetByID(context.Background(), "job123")
	if err != nil {
		t.Fatalf("failed to fetch job: %v", err)
	}

	if updatedJob.Status != StatusFailed {
		t.Errorf("expected job to fail when OutputPath is missing, got %s", updatedJob.Status)
	}
	if updatedJob.FinalPath != "" {
		t.Errorf("expected empty FinalPath, got %s (unrelated file was wrongly finalized)", updatedJob.FinalPath)
	}
	if !strings.Contains(updatedJob.Error, "engine output path was not provided") {
		t.Errorf("expected diagnostic error message, got %s", updatedJob.Error)
	}
}
