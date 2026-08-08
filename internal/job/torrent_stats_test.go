package job

import (
	"context"
	"testing"

	"downloader/internal/networkpolicy"
)

func TestUpdateTorrentRuntimeStats_NilAndTypeSafety(t *testing.T) {
	// 1. Nil safety checks
	updateTorrentRuntimeStats(nil, &EngineStatus{})
	updateTorrentRuntimeStats(&Job{Type: TypeTorrent}, nil)

	// Non-torrent job should not instantiate TorrentInfo
	nonTorrent := &Job{Type: TypeMedia}
	updateTorrentRuntimeStats(nonTorrent, &EngineStatus{Uploaded: 100})
	if nonTorrent.TorrentInfo != nil {
		t.Fatal("expected TorrentInfo to remain nil for non-torrent job")
	}

	// 2. Instantiate TorrentInfo when nil for TypeTorrent
	j := &Job{Type: TypeTorrent}
	status := &EngineStatus{
		Uploaded:           104857600,
		UploadSpeed:        524288,
		Ratio:              0.25,
		Seeders:            4,
		Leechers:           7,
		SeedingTimeSeconds: 3600,
	}
	updateTorrentRuntimeStats(j, status)

	if j.TorrentInfo == nil {
		t.Fatal("expected TorrentInfo to be initialized")
	}
	if j.TorrentInfo.Uploaded != 104857600 || j.TorrentInfo.UploadSpeed != 524288 || j.TorrentInfo.Ratio != 0.25 ||
		j.TorrentInfo.Seeders != 4 || j.TorrentInfo.Leechers != 7 || j.TorrentInfo.SeedingTimeSeconds != 3600 {
		t.Fatalf("unexpected TorrentInfo values: %+v", j.TorrentInfo)
	}
}

func TestUpdateTorrentRuntimeStats_PreservesMetadataAndOverwritesWithZero(t *testing.T) {
	j := &Job{
		Type:       TypeTorrent,
		TotalBytes: 1400 * 1024 * 1024,
		TorrentInfo: &TorrentInfo{
			Name:               "Test Torrent",
			InfoHash:           "abcd1234efgh5678abcd1234efgh5678abcd1234",
			TotalSize:          2000 * 1024 * 1024,
			Uploaded:           500,
			UploadSpeed:        100,
			Ratio:              0.5,
			Seeders:            10,
			Leechers:           5,
			SeedingTimeSeconds: 100,
		},
	}

	// Status update with zero values (peers disconnected, speed dropped)
	statusZero := &EngineStatus{
		Uploaded:           600,
		UploadSpeed:        0,
		Ratio:              0.6,
		Seeders:            0,
		Leechers:           0,
		SeedingTimeSeconds: 120,
	}
	updateTorrentRuntimeStats(j, statusZero)

	// Metadata must remain intact
	if j.TorrentInfo.Name != "Test Torrent" || j.TorrentInfo.InfoHash != "abcd1234efgh5678abcd1234efgh5678abcd1234" || j.TorrentInfo.TotalSize != 2000*1024*1024 {
		t.Fatalf("metadata fields were modified: %+v", j.TorrentInfo)
	}
	if j.TotalBytes != 1400*1024*1024 {
		t.Fatalf("Job.TotalBytes was modified: %d", j.TotalBytes)
	}

	// Runtime fields must overwrite previous values with zeros
	if j.TorrentInfo.Seeders != 0 || j.TorrentInfo.Leechers != 0 || j.TorrentInfo.UploadSpeed != 0 {
		t.Fatalf("expected non-zero values to be replaced with 0: %+v", j.TorrentInfo)
	}
	if j.TorrentInfo.Uploaded != 600 || j.TorrentInfo.Ratio != 0.6 || j.TorrentInfo.SeedingTimeSeconds != 120 {
		t.Fatalf("unexpected updated values: %+v", j.TorrentInfo)
	}
}

func TestUpdateJobFromEngine_PropagatesTorrentRuntimeStats(t *testing.T) {
	jobRepo := newFakeJobRepository()
	torrentRepo := newFakeTorrentRepository(jobRepo)
	bus := newFakeEventBus()
	eng := &regressionMockEngine{}
	reg := &fakeEngineRegistry{engines: map[string]IEngine{"qbittorrent": eng}}

	mgr := NewManager(jobRepo, reg, bus, t.TempDir(), torrentRepo)

	selectedSize := int64(1400 * 1024 * 1024)
	totalTorrentSize := int64(2000 * 1024 * 1024)

	j := &Job{
		ID:         "job-stats-1",
		Type:       TypeTorrent,
		Engine:     "qbittorrent",
		EngineID:   "hashstats1",
		Status:     StatusDownloading,
		TotalBytes: selectedSize,
		TorrentInfo: &TorrentInfo{
			Name:      "Ubuntu ISO",
			InfoHash:  "1234567890abcdef1234567890abcdef12345678",
			TotalSize: totalTorrentSize,
		},
	}
	_ = jobRepo.Create(context.Background(), j)
	mgr.addActive(j)

	sub := bus.Subscribe()

	status1 := &EngineStatus{
		Status:              StatusDownloading,
		CompletedBytes:      100 * 1024 * 1024,
		TotalBytes:          totalTorrentSize,
		SpeedBytesPerSecond: 10 * 1024 * 1024,
		UploadSpeed:         524288,
		Uploaded:            104857600,
		Ratio:               0.25,
		Seeders:             4,
		Leechers:            7,
		SeedingTimeSeconds:  0,
	}

	mgr.UpdateJobFromEngine(context.Background(), j, status1, false)

	// 1. Verify Job.TotalBytes is NOT overwritten by full torrent size
	if j.TotalBytes != selectedSize {
		t.Fatalf("expected TotalBytes to remain selected size %d, got %d", selectedSize, j.TotalBytes)
	}

	// 2. Verify TorrentInfo contains live runtime stats
	if j.TorrentInfo.Uploaded != 104857600 || j.TorrentInfo.UploadSpeed != 524288 ||
		j.TorrentInfo.Ratio != 0.25 || j.TorrentInfo.Seeders != 4 || j.TorrentInfo.Leechers != 7 {
		t.Fatalf("UpdateJobFromEngine failed to copy runtime stats to TorrentInfo: %+v", j.TorrentInfo)
	}

	// 3. Verify event published contains cloned TorrentInfo with stats
	select {
	case evt := <-sub:
		if evt.Type != EventJobUpdated {
			t.Fatalf("expected event %s, got %s", EventJobUpdated, evt.Type)
		}
		if evt.Job.TorrentInfo == nil || evt.Job.TorrentInfo.Uploaded != 104857600 || evt.Job.TorrentInfo.Seeders != 4 {
			t.Fatalf("published EventJobUpdated has invalid TorrentInfo: %+v", evt.Job.TorrentInfo)
		}
	default:
		t.Fatal("expected EventJobUpdated to be published")
	}

	// 4. Update status with non-zero -> zero transition
	status2 := &EngineStatus{
		Status:              StatusDownloading,
		CompletedBytes:      200 * 1024 * 1024,
		TotalBytes:          totalTorrentSize,
		SpeedBytesPerSecond: 0,
		UploadSpeed:         0,
		Uploaded:            104857600,
		Ratio:               0.25,
		Seeders:             0,
		Leechers:            1,
		SeedingTimeSeconds:  0,
	}
	mgr.UpdateJobFromEngine(context.Background(), j, status2, true)

	if j.TorrentInfo.Seeders != 0 || j.TorrentInfo.Leechers != 1 || j.TorrentInfo.UploadSpeed != 0 {
		t.Fatalf("expected zero transitions to update TorrentInfo: %+v", j.TorrentInfo)
	}

	// 5. Metadata preserved
	if j.TorrentInfo.Name != "Ubuntu ISO" || j.TorrentInfo.TotalSize != totalTorrentSize {
		t.Fatalf("metadata corrupted: %+v", j.TorrentInfo)
	}
}

func TestCloneJobSeedingState_DeepCopiesTorrentInfo(t *testing.T) {
	orig := &Job{
		ID:     "job-clone-test",
		Type:   TypeTorrent,
		Status: StatusDownloading,
		SeedingPolicy: networkpolicy.SeedingPolicy{
			Mode: networkpolicy.SeedingModeUnlimited,
		},
		TorrentInfo: &TorrentInfo{
			Name:        "Test File",
			Uploaded:    5000,
			UploadSpeed: 1000,
			Seeders:     8,
			Leechers:    3,
		},
	}

	cloned := cloneJobSeedingState(orig)

	if cloned.TorrentInfo == orig.TorrentInfo {
		t.Fatal("expected cloned.TorrentInfo to be a distinct pointer")
	}

	// Mutate cloned TorrentInfo
	cloned.TorrentInfo.Uploaded = 9999
	cloned.TorrentInfo.Seeders = 99

	// Orig TorrentInfo must remain untouched
	if orig.TorrentInfo.Uploaded != 5000 || orig.TorrentInfo.Seeders != 8 {
		t.Fatalf("mutating cloned TorrentInfo corrupted original: %+v", orig.TorrentInfo)
	}
}
