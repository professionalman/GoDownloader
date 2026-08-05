package job

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"downloader/internal/networkpolicy"
	"downloader/internal/storage"
)

type regressionMockStorage struct {
	freeBytes            int64
	lastPreflightTotal   int64
	lastPreflightComp    int64
	preflightCalls       int
	preflightErrToReturn error
}

func (m *regressionMockStorage) GetEffectiveDefaultDownloadDir(ctx context.Context) string {
	return "/downloads"
}

func (m *regressionMockStorage) ResolveDestination(ctx context.Context, categoryID, customDest string, policy storage.FilenameConflictPolicy, jobID string, isMedia bool) (*storage.StorageResolution, error) {
	return nil, nil
}

func (m *regressionMockStorage) Preflight(ctx context.Context, destinationDir, workDir string, totalBytes, completedBytes int64) error {
	m.preflightCalls++
	m.lastPreflightTotal = totalBytes
	m.lastPreflightComp = completedBytes
	if m.preflightErrToReturn != nil {
		return m.preflightErrToReturn
	}
	rem := totalBytes - completedBytes
	if rem < 0 {
		rem = 0
	}
	reserve := int64(104857600) // 100 MiB
	req := rem + reserve
	if m.freeBytes > 0 && m.freeBytes < req {
		return fmt.Errorf("insufficient free space in %s (free: %d, required: %d, reserve: %d, remaining: %d)",
			destinationDir, m.freeBytes, req, reserve, rem)
	}
	return nil
}

func (m *regressionMockStorage) PrepareWorkDir(ctx context.Context, jobID, workDir string) error {
	return nil
}

func (m *regressionMockStorage) FinalizeFile(ctx context.Context, srcPath, destinationDir string, policy storage.FilenameConflictPolicy) (string, error) {
	return srcPath, nil
}

func (m *regressionMockStorage) CleanupWorkDir(ctx context.Context, jobID, workDir string) error {
	return nil
}

func (m *regressionMockStorage) CleanupStaleWorkDirs(ctx context.Context, activeJobIDs map[string]bool) error {
	return nil
}

type regressionMockEngine struct {
	filesToReturn     []TorrentFile
	startDownloadCall int
	stopDownloadCall  int
	rawStateToReturn  string
	prioritiesSet     []TorrentFileSelection
}

func (m *regressionMockEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{FileSelection: true}
}
func (m *regressionMockEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	return j.EngineID, nil
}
func (m *regressionMockEngine) Pause(ctx context.Context, j *Job) error  { return nil }
func (m *regressionMockEngine) Resume(ctx context.Context, j *Job) error { return nil }
func (m *regressionMockEngine) Cancel(ctx context.Context, j *Job) error { return nil }
func (m *regressionMockEngine) Status(ctx context.Context, j *Job) (*EngineStatus, error) {
	state := m.rawStateToReturn
	if state == "" {
		state = "downloading"
	}
	return &EngineStatus{Status: StatusDownloading, RawState: state, TotalBytes: j.TotalBytes}, nil
}
func (m *regressionMockEngine) AddMagnet(ctx context.Context, magnet, savePath, jobID string) (string, error) {
	return "hash123", nil
}
func (m *regressionMockEngine) AddTorrentFile(ctx context.Context, filePath, savePath, jobID string) (string, error) {
	return "hash123", nil
}
func (m *regressionMockEngine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error) {
	return m.filesToReturn, nil
}
func (m *regressionMockEngine) SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error {
	m.prioritiesSet = selections
	return nil
}
func (m *regressionMockEngine) StartDownload(ctx context.Context, infoHash string) error {
	m.startDownloadCall++
	return nil
}
func (m *regressionMockEngine) StopDownload(ctx context.Context, infoHash string) error {
	m.stopDownloadCall++
	m.rawStateToReturn = "stoppedDL"
	return nil
}
func (m *regressionMockEngine) RemoveTorrent(ctx context.Context, infoHash string, deleteFiles bool) error {
	return nil
}
func (m *regressionMockEngine) GetTorrentInfo(ctx context.Context, infoHash string) (*TorrentInfo, error) {
	return &TorrentInfo{Name: "test", InfoHash: infoHash, TotalSize: 23192823398}, nil
}
func (m *regressionMockEngine) GetRawState(ctx context.Context, infoHash string) (string, error) {
	if m.rawStateToReturn == "" {
		return "downloading", nil
	}
	return m.rawStateToReturn, nil
}
func (m *regressionMockEngine) HealthCheck(ctx context.Context) error {
	return nil
}

func TestSelectedSizeCalculation_30Files(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{}

	files := make([]TorrentFile, 30)
	for i := 0; i < 30; i++ {
		files[i] = TorrentFile{
			Index:    i,
			Path:     fmt.Sprintf("file_%d.dat", i),
			Size:     700 * 1024 * 1024,
			Priority: PriorityNormal,
			Selected: true,
		}
	}
	eng.filesToReturn = files

	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:        "job-30files",
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hash30",
		Status:    StatusAwaitingSelection,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	selections := make([]TorrentFileSelection, 30)
	for i := 0; i < 30; i++ {
		prio := PrioritySkip
		if i == 0 || i == 1 {
			prio = PriorityHigh
		}
		selections[i] = TorrentFileSelection{Index: i, Priority: prio}
	}

	startedJ, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err != nil {
		t.Fatalf("expected StartTorrentWithPolicy to succeed, got %v", err)
	}

	expectedSelectedBytes := int64(2 * 700 * 1024 * 1024)
	if startedJ.TotalBytes != expectedSelectedBytes {
		t.Fatalf("expected TotalBytes = %d (~1.4 GiB), got %d", expectedSelectedBytes, startedJ.TotalBytes)
	}
}

func TestStartTorrent_ValidationRejections(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "f0", Size: 100},
			{Index: 1, Path: "f1", Size: 200},
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:        "job-val",
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hashval",
		Status:    StatusAwaitingSelection,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	// Duplicate index
	_, errDup := mgr.StartTorrentWithPolicy(context.Background(), j.ID, []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 0, Priority: PriorityNormal},
	}, networkpolicy.SeedingPolicy{})
	if errDup == nil {
		t.Fatal("expected duplicate index selection to be rejected")
	}

	// Unknown index
	_, errUnk := mgr.StartTorrentWithPolicy(context.Background(), j.ID, []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 99, Priority: PriorityNormal},
	}, networkpolicy.SeedingPolicy{})
	if errUnk == nil {
		t.Fatal("expected unknown index selection to be rejected")
	}

	// No selected files (all skip)
	_, errNone := mgr.StartTorrentWithPolicy(context.Background(), j.ID, []TorrentFileSelection{
		{Index: 0, Priority: PrioritySkip},
		{Index: 1, Priority: PrioritySkip},
	}, networkpolicy.SeedingPolicy{})
	if errNone == nil {
		t.Fatal("expected no files selected to be rejected")
	}
}

func TestDiskPreflightBeforeStart_FailureCausesZeroStartDownloadCalls(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "f0", Size: 700 * 1024 * 1024},
			{Index: 1, Path: "f1", Size: 700 * 1024 * 1024},
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	storageSvc := &regressionMockStorage{
		freeBytes: 100 * 1024 * 1024, // Free space 100 MB, required 1.4 GiB -> preflight fail
	}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetStorageService(storageSvc)

	j := &Job{
		ID:        "job-preflight-fail",
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hashpf",
		Status:    StatusAwaitingSelection,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
		{Index: 1, Priority: PriorityNormal},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err == nil {
		t.Fatal("expected StartTorrentWithPolicy to fail preflight due to insufficient space")
	} else {
		t.Logf("StartTorrentWithPolicy error: %v", err)
	}

	if eng.startDownloadCall != 0 {
		t.Fatalf("expected 0 StartDownload calls on preflight failure, got %d", eng.startDownloadCall)
	}

	if storageSvc.lastPreflightTotal != 1400*1024*1024 {
		t.Fatalf("expected preflight to receive selected total (1.4 GB), got %d", storageSvc.lastPreflightTotal)
	}
}

func TestPersistDispatchFailure_StopsExternalDownloadingEngine(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{
		rawStateToReturn: "downloading",
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:         "job-dispatch-fail",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashdf",
		Status:     StatusQueued,
		TotalBytes: 1400 * 1024 * 1024,
	}
	_ = jobRepo.Create(context.Background(), j)

	qj := &QueuedJob{
		JobID:  j.ID,
		Action: QueueActionStart,
	}

	dispatchErr := errors.New("simulated dispatch storage preflight error")
	_ = mgr.persistDispatchFailure(context.Background(), j, qj, StatusFailed, dispatchErr)

	if eng.stopDownloadCall == 0 {
		t.Fatal("expected persistDispatchFailure to explicitly call StopDownload on external engine")
	}

	updated, _ := jobRepo.GetByID(context.Background(), j.ID)
	if updated.Status != StatusFailed {
		t.Fatalf("expected job status = failed, got %s", updated.Status)
	}
}

func TestUpdateJobFromEngine_DoesNotRestoreFullSize(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}
	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	fullTorrentSize := int64(23192823398)
	selectedSize := int64(1400000000)

	j := &Job{
		ID:         "job-size-preserve",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashsize",
		Status:     StatusDownloading,
		TotalBytes: selectedSize,
		TorrentInfo: &TorrentInfo{
			TotalSize: fullTorrentSize,
		},
	}
	_ = jobRepo.Create(context.Background(), j)

	// Engine status returning full torrent size by mistake
	status := &EngineStatus{
		Status:     StatusDownloading,
		TotalBytes: fullTorrentSize,
	}

	mgr.UpdateJobFromEngine(context.Background(), j, status, true)

	if j.TotalBytes != selectedSize {
		t.Fatalf("expected TotalBytes to remain selected size %d, but was overwritten with %d", selectedSize, j.TotalBytes)
	}
}
