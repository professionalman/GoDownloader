package job

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
)

type mockTorrentEngine struct {
	mu                   sync.Mutex
	addMagnetCalls       []string
	addFileCalls         []string
	stopCalls            []string
	removeCalls          []string
	infoToReturn         *TorrentInfo
	engineStatusToReturn *EngineStatus
	filesToReturn        []TorrentFile
	infoError            error
	filesError           error
	stoppedCount         int
	removeDeleteFiles    []bool
}

func (m *mockTorrentEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{FileSelection: true}
}
func (m *mockTorrentEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	return j.EngineID, nil
}
func (m *mockTorrentEngine) Pause(ctx context.Context, j *Job) error  { return nil }
func (m *mockTorrentEngine) Resume(ctx context.Context, j *Job) error { return nil }
func (m *mockTorrentEngine) Cancel(ctx context.Context, j *Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeCalls = append(m.removeCalls, j.EngineID)
	return nil
}
func (m *mockTorrentEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.engineStatusToReturn != nil {
		return m.engineStatusToReturn, nil
	}
	return &EngineStatus{Status: StatusDownloading}, nil
}
func (m *mockTorrentEngine) AddMagnet(ctx context.Context, magnet, savePath, jobID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addMagnetCalls = append(m.addMagnetCalls, magnet)
	hash, err := ExtractMagnetHash(magnet)
	if err != nil {
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	return hash, nil
}
func (m *mockTorrentEngine) AddTorrentFile(ctx context.Context, filePath, savePath, jobID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addFileCalls = append(m.addFileCalls, filePath)
	return "0123456789abcdef0123456789abcdef01234567", nil
}
func (m *mockTorrentEngine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.filesError != nil {
		return nil, m.filesError
	}
	return m.filesToReturn, nil
}
func (m *mockTorrentEngine) SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error {
	return nil
}
func (m *mockTorrentEngine) StartDownload(ctx context.Context, infoHash string) error { return nil }
func (m *mockTorrentEngine) StopDownload(ctx context.Context, infoHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stoppedCount++
	m.stopCalls = append(m.stopCalls, infoHash)
	return nil
}
func (m *mockTorrentEngine) RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeCalls = append(m.removeCalls, infoHash)
	m.removeDeleteFiles = append(m.removeDeleteFiles, deleteFiles)
	return nil
}
func (m *mockTorrentEngine) GetTorrentInfo(ctx context.Context, infoHash string) (*TorrentInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.infoError != nil {
		return nil, m.infoError
	}
	return m.infoToReturn, nil
}
func (m *mockTorrentEngine) HealthCheck(ctx context.Context) error { return nil }

type mockEngineRegistry struct {
	eng IEngine
}

func (r *mockEngineRegistry) Get(name string) (IEngine, bool) {
	if name == "qbittorrent" {
		return r.eng, true
	}
	return nil, false
}
func (r *mockEngineRegistry) Detect(url string) string { return "qbittorrent" }

func createTestManager(t *testing.T, mockEng *mockTorrentEngine) (*Manager, *fakeJobRepository, *fakeEventBus, *fakeTorrentRepository) {
	repo := newFakeJobRepository()
	bus := newFakeEventBus()
	reg := &mockEngineRegistry{eng: mockEng}
	torrentRepo := newFakeTorrentRepository(repo)
	mgr := NewManager(repo, reg, bus, t.TempDir(), torrentRepo)
	mgr.SetMetadataTimeoutSeconds(10)
	return mgr, repo, bus, torrentRepo
}

func TestTorrentMetadata_SuccessStopsTorrentAndEmitsSSE(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoToReturn: &TorrentInfo{
			Name:      "Ubuntu ISO 24.04",
			InfoHash:  "0123456789abcdef0123456789abcdef01234567",
			TotalSize: 5000000000,
		},
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "ubuntu-24.04-desktop-amd64.iso", Size: 5000000000, Selected: true, Priority: PriorityNormal},
		},
	}
	mgr, repo, bus, _ := createTestManager(t, mockEng)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Ubuntu"
	job, err := mgr.Create(ctx, magnet)
	if err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	var finalJob *Job
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusAwaitingSelection {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected job to reach AwaitingSelection state")
	}

	if finalJob.Name != "Ubuntu ISO 24.04" {
		t.Errorf("expected name 'Ubuntu ISO 24.04', got %s", finalJob.Name)
	}

	mockEng.mu.Lock()
	stopped := mockEng.stoppedCount
	mockEng.mu.Unlock()
	if stopped == 0 {
		t.Errorf("expected StopDownload to be called when metadata acquired, got 0 calls")
	}

	bus.mu.Lock()
	events := append([]Event(nil), bus.events...)
	bus.mu.Unlock()

	var updatedEvent *Event
	for _, e := range events {
		if e.Type == EventJobUpdated && e.Job.ID == job.ID {
			updatedEvent = &e
			break
		}
	}
	if updatedEvent == nil {
		t.Errorf("expected EventJobUpdated to be published on event bus")
	}
}

func TestTorrentMetadata_PausedDLDiagnosisOnTimeout(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoToReturn: &TorrentInfo{
			Name:     "0123456789abcdef0123456789abcdef01234567",
			InfoHash: "0123456789abcdef0123456789abcdef01234567",
		},
		engineStatusToReturn: &EngineStatus{Status: StatusPaused},
		filesToReturn:        nil,
	}
	mgr, repo, _, torrentRepo := createTestManager(t, mockEng)
	mgr.SetMetadataTimeoutSeconds(1)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, err := mgr.Create(ctx, magnet)
	if err != nil {
		t.Fatalf("failed to create job: %v", err)
	}

	var finalJob *Job
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusFailed {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected job to reach Failed state")
	}

	if finalJob.Error != "metadata timed out while torrent was paused" {
		t.Errorf("expected error 'metadata timed out while torrent was paused', got '%s'", finalJob.Error)
	}

	rec, err := torrentRepo.GetTorrentJob(ctx, job.ID)
	if err != nil || rec == nil {
		t.Fatalf("expected torrent record to be preserved in torrentRepo")
	}
	if rec.InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("expected preserved infoHash, got %s", rec.InfoHash)
	}

	mockEng.mu.Lock()
	removes := len(mockEng.removeCalls)
	deleteFiles := mockEng.removeDeleteFiles
	mockEng.mu.Unlock()

	if removes == 0 {
		t.Errorf("expected RemoveTorrent to be called on timeout")
	}
	if len(deleteFiles) > 0 && deleteFiles[0] != false {
		t.Errorf("expected RemoveTorrent deleteFiles = false, got true")
	}
}

func TestTorrentMetadata_ZeroPeersTimeoutDiagnosis(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoToReturn: &TorrentInfo{
			Name:     "0123456789abcdef0123456789abcdef01234567",
			InfoHash: "0123456789abcdef0123456789abcdef01234567",
			Seeders:  0,
			Leechers: 0,
		},
		engineStatusToReturn: &EngineStatus{Status: StatusDownloading},
	}
	mgr, repo, _, _ := createTestManager(t, mockEng)
	mgr.SetMetadataTimeoutSeconds(1)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, _ := mgr.Create(ctx, magnet)

	var finalJob *Job
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusFailed {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected job to reach Failed state")
	}

	if finalJob.Error != "metadata timed out with zero connected peers (0 seeds, 0 leechers)" {
		t.Errorf("expected zero peers error, got '%s'", finalJob.Error)
	}
}

func TestTorrentMetadata_ErrorStateFailsImmediately(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoToReturn: &TorrentInfo{
			Name:     "0123456789abcdef0123456789abcdef01234567",
			InfoHash: "0123456789abcdef0123456789abcdef01234567",
		},
		engineStatusToReturn: &EngineStatus{Status: StatusFailed},
	}
	mgr, repo, _, _ := createTestManager(t, mockEng)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, _ := mgr.Create(ctx, magnet)

	var finalJob *Job
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusFailed {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected job to fail immediately on error state")
	}
	if finalJob.Error == "" {
		t.Errorf("expected non-empty error message")
	}
}

func TestTorrentMetadata_DisappearingTorrentFailsTruthfully(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoError: errors.New("torrent not found"),
	}
	mgr, repo, _, _ := createTestManager(t, mockEng)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, _ := mgr.Create(ctx, magnet)

	var finalJob *Job
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusFailed {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected job to fail when torrent disappears")
	}
	if finalJob.Error != "torrent disappeared from qBittorrent" {
		t.Errorf("expected 'torrent disappeared from qBittorrent', got '%s'", finalJob.Error)
	}
}

func TestTorrentMetadata_RetryRestartsAcquisition(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoError: errors.New("torrent not found"),
	}
	mgr, repo, _, _ := createTestManager(t, mockEng)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, _ := mgr.Create(ctx, magnet)

	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusFailed {
			break
		}
	}

	// Update mock engine to return valid info on retry
	mockEng.mu.Lock()
	mockEng.infoError = nil
	mockEng.infoToReturn = &TorrentInfo{
		Name:      "Retried Torrent",
		InfoHash:  "0123456789abcdef0123456789abcdef01234567",
		TotalSize: 10000,
	}
	mockEng.filesToReturn = []TorrentFile{
		{Index: 0, Path: "file.txt", Size: 10000, Selected: true},
	}
	mockEng.mu.Unlock()

	retriedJob, err := mgr.Retry(ctx, job.ID)
	if err != nil {
		t.Fatalf("expected Retry to succeed, got %v", err)
	}

	if retriedJob.Status != StatusAnalyzing {
		t.Errorf("expected status Analyzing on retry, got %s", retriedJob.Status)
	}

	var finalJob *Job
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		j, _ := repo.GetByID(ctx, job.ID)
		if j != nil && j.Status == StatusAwaitingSelection {
			finalJob = j
			break
		}
	}

	if finalJob == nil {
		t.Fatalf("expected retried job to reach AwaitingSelection")
	}
}

func TestTorrentMetadata_CancelRemovesMetadataTorrent(t *testing.T) {
	mockEng := &mockTorrentEngine{
		infoToReturn: &TorrentInfo{
			Name:     "0123456789abcdef0123456789abcdef01234567",
			InfoHash: "0123456789abcdef0123456789abcdef01234567",
		},
		engineStatusToReturn: &EngineStatus{Status: StatusAnalyzing},
	}
	mgr, repo, _, _ := createTestManager(t, mockEng)

	ctx := context.Background()
	magnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	job, _ := mgr.Create(ctx, magnet)

	time.Sleep(100 * time.Millisecond)

	cancelledJob, err := mgr.Cancel(ctx, job.ID)
	if err != nil {
		t.Fatalf("failed to cancel job: %v", err)
	}

	if cancelledJob.Status != StatusCancelled {
		t.Errorf("expected status Cancelled, got %s", cancelledJob.Status)
	}

	_ = repo
}
