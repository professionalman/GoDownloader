package job

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGovernor_SingleResourceAdmission(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 3,
		EngineLimits: map[string]int{
			"aria2": 2,
		},
	}
	gov := NewResourceGovernor(cfg)

	// Admit transfer job
	lease, err := gov.TryAcquire("job-1", ResourceRequirements{Engine: "aria2"})
	if err != nil {
		t.Fatalf("expected acquire success, got: %v", err)
	}
	if lease == nil || lease.JobID != "job-1" {
		t.Fatalf("unexpected lease: %+v", lease)
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected 1 active transfer, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 1 {
		t.Errorf("expected 1 active aria2, got %d", snap.ActivePerEngine["aria2"])
	}

	// Release
	gov.Release("job-1")
	snap = gov.Snapshot()
	if snap.ActiveTransfers != 0 {
		t.Errorf("expected 0 active transfers after release, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 0 {
		t.Errorf("expected 0 active aria2 after release, got %d", snap.ActivePerEngine["aria2"])
	}
}

func TestGovernor_EngineLimitEnforcement(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 5,
		EngineLimits: map[string]int{
			"ytdlp": 2,
			"aria2": 3,
		},
	}
	gov := NewResourceGovernor(cfg)

	// Acquire 2 ytdlp jobs
	if _, err := gov.TryAcquire("yt-1", ResourceRequirements{Engine: "ytdlp"}); err != nil {
		t.Fatalf("acquire yt-1 failed: %v", err)
	}
	if _, err := gov.TryAcquire("yt-2", ResourceRequirements{Engine: "ytdlp"}); err != nil {
		t.Fatalf("acquire yt-2 failed: %v", err)
	}

	// 3rd ytdlp job should be rejected due to per-engine limit
	_, err := gov.TryAcquire("yt-3", ResourceRequirements{Engine: "ytdlp"})
	if !errors.Is(err, ErrResourceCapacityExceeded) || !strings.Contains(err.Error(), "engine \"ytdlp\" capacity full") {
		t.Fatalf("expected engine capacity full ErrResourceCapacityExceeded for yt-3, got: %v", err)
	}

	// But aria2 job can still be admitted because engine has capacity and global has capacity
	lease, err := gov.TryAcquire("aria-1", ResourceRequirements{Engine: "aria2"})
	if err != nil {
		t.Fatalf("expected aria-1 acquire success, got: %v", err)
	}
	if lease == nil {
		t.Fatal("expected non-nil lease for aria-1")
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 3 {
		t.Errorf("expected 3 active transfers, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["ytdlp"] != 2 {
		t.Errorf("expected 2 active ytdlp, got %d", snap.ActivePerEngine["ytdlp"])
	}
	if snap.ActivePerEngine["aria2"] != 1 {
		t.Errorf("expected 1 active aria2, got %d", snap.ActivePerEngine["aria2"])
	}
}

func TestGovernor_GlobalTransferLimitEnforcement(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 2,
		EngineLimits: map[string]int{
			"ytdlp":       2,
			"qbittorrent": 2,
		},
	}
	gov := NewResourceGovernor(cfg)

	if _, err := gov.TryAcquire("j1", ResourceRequirements{Engine: "ytdlp"}); err != nil {
		t.Fatalf("acquire j1 failed: %v", err)
	}
	if _, err := gov.TryAcquire("j2", ResourceRequirements{Engine: "qbittorrent"}); err != nil {
		t.Fatalf("acquire j2 failed: %v", err)
	}

	// Global limit is 2; 3rd job must fail with ErrResourceCapacityExceeded (global transfer capacity full)
	_, err := gov.TryAcquire("j3", ResourceRequirements{Engine: "qbittorrent"})
	if !errors.Is(err, ErrResourceCapacityExceeded) || !strings.Contains(err.Error(), "global transfer capacity full") {
		t.Fatalf("expected global transfer capacity full ErrResourceCapacityExceeded, got: %v", err)
	}
}

func TestGovernor_AtomicMultiResourceRejection(t *testing.T) {
	// If engine limit is exceeded, global transfer counter must NOT increment.
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 10,
		EngineLimits: map[string]int{
			"ytdlp": 1,
		},
	}
	gov := NewResourceGovernor(cfg)

	if _, err := gov.TryAcquire("j1", ResourceRequirements{Engine: "ytdlp"}); err != nil {
		t.Fatalf("acquire j1 failed: %v", err)
	}

	// Second ytdlp fails
	_, err := gov.TryAcquire("j2", ResourceRequirements{Engine: "ytdlp"})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded, got: %v", err)
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Errorf("expected active transfers to remain 1 after rejected acquire, got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["ytdlp"] != 1 {
		t.Errorf("expected active ytdlp to remain 1, got %d", snap.ActivePerEngine["ytdlp"])
	}
}

func TestGovernor_IdempotentRelease(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 3,
		EngineLimits: map[string]int{
			"aria2": 2,
		},
	}
	gov := NewResourceGovernor(cfg)

	if _, err := gov.TryAcquire("j1", ResourceRequirements{Engine: "aria2"}); err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// First release must return true
	first := gov.Release("j1")
	if !first {
		t.Fatal("expected first Release to return true")
	}
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 0 || snap.ActivePerEngine["aria2"] != 0 {
		t.Fatalf("expected 0 counters after first release, got transfers=%d, aria2=%d", snap.ActiveTransfers, snap.ActivePerEngine["aria2"])
	}

	// Duplicate release must return false and not decrement below 0
	second := gov.Release("j1")
	if second {
		t.Fatal("expected duplicate Release to return false")
	}

	// Unknown job release must return false
	unknown := gov.Release("unknown-job")
	if unknown {
		t.Fatal("expected unknown Release to return false")
	}

	snap = gov.Snapshot()
	if snap.ActiveTransfers < 0 || snap.ActivePerEngine["aria2"] < 0 {
		t.Fatalf("counters became negative: transfers=%d, aria2=%d", snap.ActiveTransfers, snap.ActivePerEngine["aria2"])
	}
	if snap.ActiveTransfers != 0 || snap.ActivePerEngine["aria2"] != 0 {
		t.Fatalf("expected 0 counters, got transfers=%d, aria2=%d", snap.ActiveTransfers, snap.ActivePerEngine["aria2"])
	}
}

func TestGovernor_ConcurrentAcquisitionSafety(t *testing.T) {
	limit := 5
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: limit,
		EngineLimits: map[string]int{
			"engine1": limit,
		},
	}
	gov := NewResourceGovernor(cfg)

	goroutines := 100
	var successCount int64
	var failCount int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		jobID := fmt.Sprintf("concurrent-job-%d", i)
		go func(id string) {
			defer wg.Done()
			_, err := gov.TryAcquire(id, ResourceRequirements{Engine: "engine1"})
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			} else {
				atomic.AddInt64(&failCount, 1)
			}
		}(jobID)
	}
	wg.Wait()

	if successCount != int64(limit) {
		t.Fatalf("expected exactly %d successful acquisitions, got %d", limit, successCount)
	}
	if failCount != int64(goroutines-limit) {
		t.Fatalf("expected exactly %d failed acquisitions, got %d", goroutines-limit, failCount)
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != limit {
		t.Fatalf("expected exactly %d active transfers, got %d", limit, snap.ActiveTransfers)
	}
}

func TestGovernor_StartupReconstruction(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 3,
		EngineLimits: map[string]int{
			"aria2":       2,
			"ytdlp":       2,
			"qbittorrent": 3,
		},
	}
	gov := NewResourceGovernor(cfg)

	jobs := []*Job{
		{ID: "j-down", Status: StatusDownloading, Engine: "aria2"},
		{ID: "j-proc", Status: StatusProcessing, Engine: "ytdlp"},
		{ID: "j-seed", Status: StatusSeeding, Engine: "qbittorrent"},
		{ID: "j-queued", Status: StatusQueued},
		{ID: "j-comp", Status: StatusCompleted},
	}

	for _, j := range jobs {
		gov.ReconstructJob(j)
	}

	snap := gov.Snapshot()
	// Both StatusDownloading and StatusProcessing are reconstructed as active transfers
	if snap.ActiveTransfers != 2 {
		t.Errorf("expected 2 active transfers (j-down and j-proc), got %d", snap.ActiveTransfers)
	}
	if snap.ActivePerEngine["aria2"] != 1 {
		t.Errorf("expected 1 active aria2, got %d", snap.ActivePerEngine["aria2"])
	}
	if snap.ActivePerEngine["ytdlp"] != 1 {
		t.Errorf("expected 1 active ytdlp, got %d", snap.ActivePerEngine["ytdlp"])
	}

	// Idempotent double reconstruction
	for _, j := range jobs {
		gov.ReconstructJob(j)
	}

	snap2 := gov.Snapshot()
	if snap2.ActiveTransfers != snap.ActiveTransfers ||
		snap2.ActiveLeases != snap.ActiveLeases {
		t.Fatalf("reconstruction was not idempotent: before=%+v, after=%+v", snap, snap2)
	}
}

func TestGovernor_ReconstructionAboveLimits(t *testing.T) {
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 2,
		EngineLimits: map[string]int{
			"aria2": 2,
		},
	}
	gov := NewResourceGovernor(cfg)

	// Reconstruct 3 active jobs when limit is 2
	jobs := []*Job{
		{ID: "r1", Status: StatusDownloading, Engine: "aria2"},
		{ID: "r2", Status: StatusDownloading, Engine: "aria2"},
		{ID: "r3", Status: StatusProcessing, Engine: "aria2"},
	}

	for _, j := range jobs {
		if !gov.ReconstructJob(j) {
			t.Fatalf("expected ReconstructJob to return true for %s", j.ID)
		}
	}

	// Snapshot reflects accurate usage of 3/2 without clamping or rejection
	snap := gov.Snapshot()
	if snap.ActiveTransfers != 3 {
		t.Fatalf("expected 3 active transfers, got %d", snap.ActiveTransfers)
	}
	if snap.GlobalTransferLimit != 2 {
		t.Fatalf("expected global limit 2, got %d", snap.GlobalTransferLimit)
	}

	// CanAdmit returns false
	canAdmit, reason := gov.CanAdmit(ResourceRequirements{Engine: "aria2"})
	if canAdmit {
		t.Fatal("expected CanAdmit to return false when usage (3) >= limit (2)")
	}
	if !strings.Contains(reason, "capacity full") {
		t.Fatalf("unexpected reason: %s", reason)
	}

	// TryAcquire fails with ErrResourceCapacityExceeded
	_, err := gov.TryAcquire("r4", ResourceRequirements{Engine: "aria2"})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded, got: %v", err)
	}

	// Release 1 job: usage becomes 2/2, still full
	gov.Release("r1")
	snap = gov.Snapshot()
	if snap.ActiveTransfers != 2 {
		t.Fatalf("expected 2 active transfers, got %d", snap.ActiveTransfers)
	}
	if canAdmit, _ := gov.CanAdmit(ResourceRequirements{Engine: "aria2"}); canAdmit {
		t.Fatal("expected CanAdmit false when usage (2) >= limit (2)")
	}

	// Release 2nd job: usage becomes 1/2, admission succeeds
	gov.Release("r2")
	snap = gov.Snapshot()
	if snap.ActiveTransfers != 1 {
		t.Fatalf("expected 1 active transfer, got %d", snap.ActiveTransfers)
	}
	lease, err := gov.TryAcquire("r4", ResourceRequirements{Engine: "aria2"})
	if err != nil {
		t.Fatalf("expected acquire success for r4 after capacity freed, got: %v", err)
	}
	if lease == nil || lease.JobID != "r4" {
		t.Fatalf("unexpected lease: %+v", lease)
	}
}

func TestGovernor_DynamicLimitLowering(t *testing.T) {
	dynLimit := 5
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 10,
	}
	gov := NewResourceGovernor(cfg, func() int {
		return dynLimit
	})

	// Acquire 3 jobs under dynLimit = 5
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("job-%d", i)
		if _, err := gov.TryAcquire(id, ResourceRequirements{Engine: "aria2"}); err != nil {
			t.Fatalf("failed to acquire %s: %v", id, err)
		}
	}

	snap := gov.Snapshot()
	if snap.ActiveTransfers != 3 || snap.GlobalTransferLimit != 5 {
		t.Fatalf("expected 3 transfers under limit 5, got %+v", snap)
	}

	// Dynamically lower limit to 2: existing 3 jobs remain active, usage is not clamped
	dynLimit = 2
	snap = gov.Snapshot()
	if snap.ActiveTransfers != 3 {
		t.Fatalf("expected active transfers to remain 3 after limit lowering, got %d", snap.ActiveTransfers)
	}
	if snap.GlobalTransferLimit != 2 {
		t.Fatalf("expected global limit 2, got %d", snap.GlobalTransferLimit)
	}

	// New admission is blocked
	if admit, _ := gov.CanAdmit(ResourceRequirements{Engine: "aria2"}); admit {
		t.Fatal("expected CanAdmit false when activeTransfers(3) >= dynLimit(2)")
	}
	_, err := gov.TryAcquire("job-4", ResourceRequirements{Engine: "aria2"})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded, got: %v", err)
	}

	// Release 1 job: active becomes 2, still at limit (2/2)
	gov.Release("job-1")
	if admit, _ := gov.CanAdmit(ResourceRequirements{Engine: "aria2"}); admit {
		t.Fatal("expected CanAdmit false when activeTransfers(2) >= dynLimit(2)")
	}

	// Release another: active becomes 1, now 1 < 2, admission succeeds
	gov.Release("job-2")
	lease, err := gov.TryAcquire("job-4", ResourceRequirements{Engine: "aria2"})
	if err != nil {
		t.Fatalf("expected acquire success for job-4, got: %v", err)
	}
	if lease == nil || lease.JobID != "job-4" {
		t.Fatalf("unexpected lease: %+v", lease)
	}
}

func TestGovernor_DynamicLimitUpdates(t *testing.T) {
	dynLimit := 3
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: 10,
		EngineLimits: map[string]int{
			"engine": 10,
		},
	}
	gov := NewResourceGovernor(cfg, func() int {
		return dynLimit
	})

	// Admit 3 jobs under dynLimit = 3
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("dyn-job-%d", i)
		if _, err := gov.TryAcquire(id, ResourceRequirements{Engine: "engine"}); err != nil {
			t.Fatalf("failed to acquire %s: %v", id, err)
		}
	}

	// 4th job fails because dynLimit is 3
	_, err := gov.TryAcquire("dyn-job-4", ResourceRequirements{Engine: "engine"})
	if !errors.Is(err, ErrResourceCapacityExceeded) {
		t.Fatalf("expected ErrResourceCapacityExceeded, got: %v", err)
	}

	// Reduce dynamic limit to 2; existing 3 jobs remain active, but CanAdmit returns false
	dynLimit = 2
	if admit, _ := gov.CanAdmit(ResourceRequirements{Engine: "engine"}); admit {
		t.Fatal("expected CanAdmit false when activeTransfers(3) >= dynLimit(2)")
	}

	// Increase dynamic limit to 5; CanAdmit now returns true and job-4 can acquire
	dynLimit = 5
	if admit, _ := gov.CanAdmit(ResourceRequirements{Engine: "engine"}); !admit {
		t.Fatal("expected CanAdmit true when activeTransfers(3) < dynLimit(5)")
	}
	if _, err := gov.TryAcquire("dyn-job-4", ResourceRequirements{Engine: "engine"}); err != nil {
		t.Fatalf("expected acquire success for dyn-job-4 under dynLimit 5, got: %v", err)
	}
}

func TestGovernor_ConfigDefaultsAndEngineLimits(t *testing.T) {
	defaultCfg := DefaultResourceGovernorConfig()
	if defaultCfg.GlobalTransferLimit != DefaultGlobalTransferLimit {
		t.Errorf("expected default global limit %d, got %d", DefaultGlobalTransferLimit, defaultCfg.GlobalTransferLimit)
	}
	if defaultCfg.EngineLimits["ytdlp"] != DefaultYtdlpEngineLimit {
		t.Errorf("expected ytdlp engine limit %d, got %d", DefaultYtdlpEngineLimit, defaultCfg.EngineLimits["ytdlp"])
	}
	if _, ok := defaultCfg.EngineLimits["qbittorrent"]; ok {
		t.Errorf("expected qbittorrent not to be explicitly set in default EngineLimits")
	}
	if _, ok := defaultCfg.EngineLimits["aria2"]; ok {
		t.Errorf("expected aria2 not to be explicitly set in default EngineLimits")
	}

	gov := NewResourceGovernor(defaultCfg)
	// ytdlp has explicit engine limit of 2
	if limit := gov.effectiveEngineLimit("ytdlp"); limit != DefaultYtdlpEngineLimit {
		t.Errorf("expected effective ytdlp limit %d, got %d", DefaultYtdlpEngineLimit, limit)
	}
	// qbittorrent and aria2 default to being bounded by global limit (3)
	if limit := gov.effectiveEngineLimit("qbittorrent"); limit != DefaultGlobalTransferLimit {
		t.Errorf("expected effective qbittorrent limit %d, got %d", DefaultGlobalTransferLimit, limit)
	}
	if limit := gov.effectiveEngineLimit("aria2"); limit != DefaultGlobalTransferLimit {
		t.Errorf("expected effective aria2 limit %d, got %d", DefaultGlobalTransferLimit, limit)
	}
}

func TestGovernor_ConfigBoundaryLimits(t *testing.T) {
	defaultCfg := DefaultResourceGovernorConfig()

	// Zero and negative config limits must default to safe positive values
	cfg := ResourceGovernorConfig{
		GlobalTransferLimit: -1,
		EngineLimits: map[string]int{
			"ytdlp": -2,
			"aria2": 0,
		},
	}
	gov := NewResourceGovernor(cfg)

	snap := gov.Snapshot()
	if snap.GlobalTransferLimit != DefaultGlobalTransferLimit {
		t.Errorf("expected default global limit %d, got %d", DefaultGlobalTransferLimit, snap.GlobalTransferLimit)
	}
	if snap.EngineLimits["ytdlp"] != defaultCfg.EngineLimits["ytdlp"] {
		t.Errorf("expected default ytdlp limit %d, got %d", defaultCfg.EngineLimits["ytdlp"], snap.EngineLimits["ytdlp"])
	}
}
