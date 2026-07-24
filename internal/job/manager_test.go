package job

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	addMagnetFunc      func(magnet string) (string, error)
	addTorrentFileFunc func(path string) (string, error)
	getFilesFunc       func(hash string) ([]TorrentFile, error)
	setPrioritiesFunc  func(hash string) error
	startDownloadFunc  func(hash string) error
	stopDownloadFunc   func(hash string) error
	removeTorrentFunc  func(hash string, deleteFiles bool) error
	getTorrentInfoFunc func(hash string) (*TorrentInfo, error)
	statusFunc         func(ctx context.Context, j *Job) (*EngineStatus, error)
}

func (f *fakeTorrentEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	if f.statusFunc != nil {
		return f.statusFunc(ctx, j)
	}
	if f.fakeEngine != nil && f.fakeEngine.statusFunc != nil {
		return f.fakeEngine.statusFunc(ctx, j)
	}
	return &EngineStatus{Status: StatusDownloading}, nil
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
	if f.startDownloadFunc != nil {
		return f.startDownloadFunc(infoHash)
	}
	return nil
}
func (f *fakeTorrentEngine) StopDownload(ctx context.Context, infoHash string) error {
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

type fakeTorrentRepository struct {
	mu           sync.Mutex
	torrentJobs  map[string]*TorrentJobRecord
	torrentFiles map[string][]TorrentFileRecord
}

func newFakeTorrentRepository() *fakeTorrentRepository {
	return &fakeTorrentRepository{
		torrentJobs:  make(map[string]*TorrentJobRecord),
		torrentFiles: make(map[string][]TorrentFileRecord),
	}
}

func (f *fakeTorrentRepository) CreateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrentJobs[rec.JobID] = rec
	return nil
}

func (f *fakeTorrentRepository) GetTorrentJob(ctx context.Context, jobID string) (*TorrentJobRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.torrentJobs[jobID]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return rec, nil
}

func (f *fakeTorrentRepository) UpdateTorrentJob(ctx context.Context, rec *TorrentJobRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrentJobs[rec.JobID] = rec
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

var _ ITorrentRepository = (*fakeTorrentRepository)(nil)

func setupManagerTest(t *testing.T) (*Manager, *fakeEngine, *fakeEventBus, func(), *fakeTorrentEngine) {
	t.Helper()
	tmpDir := t.TempDir()
	repo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository()
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
	t.Run("same torrent file twice is rejected", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

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

		gotJ2, _ := m.repo.GetByID(ctx, j2.ID)
		if gotJ2.Status != StatusFailed {
			t.Errorf("expected duplicate torrent file job to be marked StatusFailed, got %s", gotJ2.Status)
		}
		_ = j1
	})

	t.Run("magnet first then equivalent torrent file is rejected", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

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

		gotJ2, _ := m.repo.GetByID(ctx, j2.ID)
		if gotJ2.Status != StatusFailed {
			t.Errorf("expected duplicate torrent file after magnet to be marked StatusFailed, got %s", gotJ2.Status)
		}
		_ = j1
	})

	t.Run("failed historical job allows re-adding same info hash", func(t *testing.T) {
		m, _, _, cleanup, fakeTorrent := setupManagerTest(t)
		defer cleanup()
		ctx := context.Background()

		fakeTorrent.addMagnetFunc = func(magnet string) (string, error) {
			return "1234567890abcdef1234567890abcdef12345678", nil
		}

		magnet := "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678"
		j1, _ := m.Create(ctx, magnet)
		time.Sleep(1500 * time.Millisecond)

		// Mark j1 as StatusFailed
		j1.Status = StatusFailed
		m.repo.Update(ctx, j1)

		// Now creating second job with same magnet should succeed
		j2, err := m.Create(ctx, magnet)
		if err != nil {
			t.Fatalf("expected creating magnet after historical failure to succeed, got %v", err)
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
	if got.Status != StatusFailed {
		t.Errorf("expected StatusFailed when RemoveTorrent fails, got %s", got.Status)
	}
	if got.Error == "" {
		t.Error("expected error message when RemoveTorrent fails")
	}
}
