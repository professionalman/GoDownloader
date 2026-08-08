package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
		return fmt.Errorf("%w: insufficient free space in %s (free: %d, required: %d, reserve: %d, remaining: %d)",
			storage.ErrInsufficientDiskSpace, destinationDir, m.freeBytes, req, reserve, rem)
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
	filesToReturn          []TorrentFile
	startDownloadCall      int
	stopDownloadCall       int
	setFilePrioritiesCalls int
	rawStateToReturn       string
	prioritiesSet          []TorrentFileSelection
	setFilePrioritiesErr   error
	stopDownloadErr        error
	getRawStateErr         error
	pauseErr               error
}

func (m *regressionMockEngine) Capabilities() networkpolicy.EngineCapabilities {
	return networkpolicy.EngineCapabilities{FileSelection: true}
}
func (m *regressionMockEngine) Start(ctx context.Context, j *Job, downloadDir string) (string, error) {
	return j.EngineID, nil
}
func (m *regressionMockEngine) Pause(ctx context.Context, j *Job) error {
	if m.pauseErr != nil {
		return m.pauseErr
	}
	return nil
}
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
func (m *regressionMockEngine) GetTorrentOwnership(ctx context.Context, infoHash string) (*TorrentOwnership, error) {
	return nil, nil
}
func (m *regressionMockEngine) AdoptTorrent(ctx context.Context, infoHash, jobID string) error {
	return nil
}
func (m *regressionMockEngine) GetFiles(ctx context.Context, infoHash string) ([]TorrentFile, error) {
	return m.filesToReturn, nil
}
func (m *regressionMockEngine) SetFilePriorities(ctx context.Context, infoHash string, selections []TorrentFileSelection) error {
	m.setFilePrioritiesCalls++
	if m.setFilePrioritiesErr != nil {
		return m.setFilePrioritiesErr
	}
	m.prioritiesSet = selections
	fileMap := make(map[int]*TorrentFile, len(m.filesToReturn))
	for i := range m.filesToReturn {
		fileMap[m.filesToReturn[i].Index] = &m.filesToReturn[i]
	}
	for _, s := range selections {
		if file, ok := fileMap[s.Index]; ok {
			file.Priority = s.Priority
			file.Selected = (s.Priority != PrioritySkip)
		}
	}
	return nil
}
func (m *regressionMockEngine) StartDownload(ctx context.Context, infoHash string) error {
	m.startDownloadCall++
	return nil
}
func (m *regressionMockEngine) StopDownload(ctx context.Context, infoHash string) error {
	m.stopDownloadCall++
	if m.stopDownloadErr != nil {
		return m.stopDownloadErr
	}
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
	if m.getRawStateErr != nil {
		return "", m.getRawStateErr
	}
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
	}

	// Verify the returned error is a proper *AppError with INSUFFICIENT_DISK_SPACE code
	var appErr *AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *AppError, got %T: %v", err, err)
	}
	if appErr.Code != ErrInsufficientDiskSpace {
		t.Fatalf("expected error code %s, got %s", ErrInsufficientDiskSpace, appErr.Code)
	}
	// Verify the message contains detailed disk space info
	if !strings.Contains(appErr.Message, "free:") || !strings.Contains(appErr.Message, "required:") || !strings.Contains(appErr.Message, "reserve:") || !strings.Contains(appErr.Message, "remaining:") {
		t.Fatalf("expected detailed disk space message, got: %s", appErr.Message)
	}
	t.Logf("StartTorrentWithPolicy error: %v", err)

	if eng.startDownloadCall != 0 {
		t.Fatalf("expected 0 StartDownload calls on preflight failure, got %d", eng.startDownloadCall)
	}

	// Verify SetFilePriorities was NOT called (preflight is before priorities)
	if eng.setFilePrioritiesCalls != 0 {
		t.Fatalf("expected 0 SetFilePriorities calls on preflight failure, got %d", eng.setFilePrioritiesCalls)
	}

	if storageSvc.lastPreflightTotal != 1400*1024*1024 {
		t.Fatalf("expected preflight to receive selected total (1.4 GB), got %d", storageSvc.lastPreflightTotal)
	}

	// Verify job remains in awaiting_selection
	updatedJob, _ := jobRepo.GetByID(context.Background(), j.ID)
	if updatedJob.Status != StatusAwaitingSelection {
		t.Fatalf("expected job to remain in awaiting_selection, got %s", updatedJob.Status)
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

func TestSetFilePrioritiesFailure_LeavesNoRunnableQueue(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)
	eng := &regressionMockEngine{
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "f0", Size: 700 * 1024 * 1024},
		},
		setFilePrioritiesErr: errors.New("qBittorrent connection error"),
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:        "job-prio-fail",
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
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err == nil {
		t.Fatal("expected StartTorrentWithPolicy to fail when SetFilePriorities returns error")
	}

	// Verify job remains StatusAwaitingSelection in DB
	durableJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if durableJ.Status != StatusAwaitingSelection {
		t.Fatalf("expected job status to remain StatusAwaitingSelection on priority failure, got %s", durableJ.Status)
	}

	// Verify no runnable queue entry exists
	qe, _ := queueRepo.Get(context.Background(), j.ID)
	if qe != nil {
		t.Fatalf("expected NO queue entry when SetFilePriorities fails, got %#v", qe)
	}
}

func TestStartTorrent_GetTorrentJobFailureAborts(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	torrentRepo.getErr = errors.New("db get error")

	eng := &regressionMockEngine{
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "f0", Size: 700 * 1024 * 1024},
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:        "job-getrec-fail",
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hashgr",
		Status:    StatusAwaitingSelection,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err == nil {
		t.Fatal("expected StartTorrentWithPolicy to fail when GetTorrentJob returns error")
	}

	durableJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if durableJ.Status != StatusAwaitingSelection {
		t.Fatalf("expected job status = StatusAwaitingSelection, got %s", durableJ.Status)
	}
}

func TestStartTorrent_NextPositionFailureAborts(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry), nextPosErr: errors.New("queue error")}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	eng := &regressionMockEngine{
		filesToReturn: []TorrentFile{
			{Index: 0, Path: "f0", Size: 700 * 1024 * 1024},
		},
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:        "job-pos-fail",
		Type:      TypeTorrent,
		Engine:    "qbittorrent",
		EngineID:  "hashpos",
		Status:    StatusAwaitingSelection,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	_ = jobRepo.Create(context.Background(), j)

	selections := []TorrentFileSelection{
		{Index: 0, Priority: PriorityNormal},
	}

	_, err := mgr.StartTorrentWithPolicy(context.Background(), j.ID, selections, networkpolicy.SeedingPolicy{Mode: networkpolicy.SeedingModeNone})
	if err == nil {
		t.Fatal("expected StartTorrentWithPolicy to fail when NextPosition returns error")
	}

	durableJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if durableJ.Status != StatusAwaitingSelection {
		t.Fatalf("expected job status = StatusAwaitingSelection, got %s", durableJ.Status)
	}
}

func TestDispatchQueuedJob_MissingSelectionRecordsAborts(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:         "job-no-selections",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashnosel",
		Status:     StatusQueued,
		TotalBytes: 1400 * 1024 * 1024,
	}
	_ = jobRepo.Create(context.Background(), j)
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: j.ID, InfoHash: j.EngineID})
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}

	err := mgr.DispatchQueuedJob(context.Background(), qj)
	if err == nil {
		t.Fatal("expected DispatchQueuedJob to fail when torrent selection records are missing")
	}

	updatedJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if updatedJ.Status != StatusFailed {
		t.Fatalf("expected job status = failed, got %s", updatedJ.Status)
	}
}

func TestDispatchQueuedJob_InconsistentSelectionRecordsAborts(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// Save inconsistent record: Selected=false but Priority=normal
	inconsistentFiles := []TorrentFileRecord{
		{JobID: "job-inconsistent", FileIndex: 0, Selected: false, Priority: "normal", Size: 700 * 1024 * 1024},
	}
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{JobID: "job-inconsistent", InfoHash: "hashincon"})
	_ = torrentRepo.SaveTorrentFiles(context.Background(), "job-inconsistent", inconsistentFiles)

	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:         "job-inconsistent",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashincon",
		Status:     StatusQueued,
		TotalBytes: 700 * 1024 * 1024,
	}
	_ = jobRepo.Create(context.Background(), j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}

	err := mgr.DispatchQueuedJob(context.Background(), qj)
	if err == nil {
		t.Fatal("expected DispatchQueuedJob to fail on inconsistent selection records")
	}

	updatedJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if updatedJ.Status != StatusFailed {
		t.Fatalf("expected job status = failed, got %s", updatedJ.Status)
	}
}

func TestPersistDispatchFailure_GetRawStateErrorMarksReconciliationPending(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{
		getRawStateErr: errors.New("engine RPC timeout"),
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:         "job-rawerr",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashrawerr",
		Status:     StatusQueued,
		TotalBytes: 1400 * 1024 * 1024,
	}
	_ = jobRepo.Create(context.Background(), j)

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.persistDispatchFailure(context.Background(), j, qj, StatusFailed, errors.New("preflight failed"))

	updatedJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if !updatedJ.NetworkReconcilePending {
		t.Fatal("expected NetworkReconcilePending = true when GetRawState returns error")
	}
}

func TestPersistDispatchFailure_StopDownloadErrorMarksReconciliationPending(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	eng := &regressionMockEngine{
		stopDownloadErr: errors.New("stop download failed"),
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)

	j := &Job{
		ID:         "job-stoperr",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashstoperr",
		Status:     StatusQueued,
		TotalBytes: 1400 * 1024 * 1024,
	}
	_ = jobRepo.Create(context.Background(), j)

	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}
	_ = mgr.persistDispatchFailure(context.Background(), j, qj, StatusFailed, errors.New("preflight failed"))

	updatedJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if !updatedJ.NetworkReconcilePending {
		t.Fatal("expected NetworkReconcilePending = true when StopDownload returns error")
	}
}

func TestDispatchQueuedJob_SuccessfulSelectedSizeCondition(t *testing.T) {
	jobRepo := newFakeJobRepository()
	queueRepo := &fakeQueueRepo{entries: make(map[string]*QueueEntry)}
	torrentRepo := newFakeTorrentRepository(jobRepo, queueRepo)

	// 2 selected files @ 700 MiB each (~1.4 GiB selected total)
	files := []TorrentFileRecord{
		{JobID: "job-repro-success", FileIndex: 0, Selected: true, Priority: "normal", Size: 700 * 1024 * 1024},
		{JobID: "job-repro-success", FileIndex: 1, Selected: true, Priority: "normal", Size: 700 * 1024 * 1024},
		{JobID: "job-repro-success", FileIndex: 2, Selected: false, Priority: "skip", Size: 20 * 1024 * 1024 * 1024}, // 20 GiB skipped file
	}
	_ = torrentRepo.CreateTorrentJob(context.Background(), &TorrentJobRecord{
		JobID:    "job-repro-success",
		InfoHash: "hashrepro",
		Name:     "repro",
	})
	_ = torrentRepo.SaveTorrentFiles(context.Background(), "job-repro-success", files)

	eng := &regressionMockEngine{
		rawStateToReturn: "stoppedDL",
	}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	// Free space: 14.9 GiB (16,000,000,000 bytes)
	// Full torrent: 21.4 GiB (22,870,000,000 bytes) -> free space < full torrent size!
	// Selected payload: 1.4 GiB (1,468,006,400 bytes) -> free space > selected + reserve!
	storageSvc := &regressionMockStorage{
		freeBytes: 16000000000,
	}

	mgr := NewManager(jobRepo, reg, newFakeEventBus(), t.TempDir(), torrentRepo)
	mgr.SetStorageService(storageSvc)
	mgr.SetQueueRepository(queueRepo)

	j := &Job{
		ID:         "job-repro-success",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashrepro",
		Status:     StatusQueued,
		TotalBytes: 1468006400,
		TorrentInfo: &TorrentInfo{
			TotalSize: 22870000000,
		},
	}
	_ = jobRepo.Create(context.Background(), j)
	qj := &QueuedJob{JobID: j.ID, Action: QueueActionStart}

	err := mgr.DispatchQueuedJob(context.Background(), qj)
	if err != nil {
		t.Fatalf("expected DispatchQueuedJob to succeed when free disk > selected payload + reserve, got %v", err)
	}

	updatedJ, _ := jobRepo.GetByID(context.Background(), j.ID)
	if updatedJ.Status != StatusDownloading {
		t.Fatalf("expected job status = downloading, got %s", updatedJ.Status)
	}
	if updatedJ.TotalBytes != 1468006400 {
		t.Fatalf("expected TotalBytes = 1.4 GiB (%d), got %d", 1468006400, updatedJ.TotalBytes)
	}
}

func TestMapStorageError_AllSentinels(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		expectedCode    string
		expectedMessage string
	}{
		{
			name:         "ErrInsufficientDiskSpace",
			err:          fmt.Errorf("%w: insufficient free space (free: 100, required: 200, reserve: 50, remaining: 150)", storage.ErrInsufficientDiskSpace),
			expectedCode: ErrInsufficientDiskSpace,
		},
		{
			name:         "ErrInvalidStorageSelection",
			err:          fmt.Errorf("%w: bad selection", storage.ErrInvalidStorageSelection),
			expectedCode: ErrInvalidStorageSelection,
		},
		{
			name:         "ErrInvalidDestination",
			err:          fmt.Errorf("%w: bad dest", storage.ErrInvalidDestination),
			expectedCode: ErrInvalidDestination,
		},
		{
			name:         "ErrCategoryNotFound",
			err:          fmt.Errorf("%w: no such category", storage.ErrCategoryNotFound),
			expectedCode: ErrCategoryNotFound,
		},
		{
			name:         "ErrCategoryNameConflict",
			err:          fmt.Errorf("%w: name conflict", storage.ErrCategoryNameConflict),
			expectedCode: ErrCategoryNameConflict,
		},
		{
			name:         "ErrFileConflict",
			err:          fmt.Errorf("%w: file conflict", storage.ErrFileConflict),
			expectedCode: ErrFileConflict,
		},
		{
			name:         "ErrStorageError",
			err:          fmt.Errorf("%w: storage failure", storage.ErrStorageError),
			expectedCode: ErrStorageError,
		},
		{
			name:            "unknown error falls back to INTERNAL_ERROR and sanitizes message",
			err:             fmt.Errorf("something completely unexpected"),
			expectedCode:    ErrInternalError,
			expectedMessage: "an internal error occurred",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapped := mapStorageError(tt.err)
			appErr, ok := mapped.(*AppError)
			if !ok {
				t.Fatalf("expected *AppError, got %T", mapped)
			}
			if appErr.Code != tt.expectedCode {
				t.Fatalf("expected code %s, got %s", tt.expectedCode, appErr.Code)
			}
			expectedMsg := tt.expectedMessage
			if expectedMsg == "" {
				expectedMsg = tt.err.Error()
			}
			if appErr.Message != expectedMsg {
				t.Fatalf("expected message %q, got %q", expectedMsg, appErr.Message)
			}
		})
	}
}

func TestMapStorageError_Nil(t *testing.T) {
	result := mapStorageError(nil)
	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}
