package ytdlp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUsageGate_SharedExecutionCoexistence(t *testing.T) {
	gate := NewUsageGate()

	rel1, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("first AcquireUse failed: %v", err)
	}
	defer rel1()

	rel2, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("second AcquireUse failed: %v", err)
	}
	defer rel2()

	rel3, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("third AcquireUse failed: %v", err)
	}
	defer rel3()

	if !gate.InUse() {
		t.Errorf("expected gate.InUse() == true")
	}
	if gate.IsExclusive() {
		t.Errorf("expected gate.IsExclusive() == false")
	}
}

func TestUsageGate_ExclusiveMaintenanceRefusedWhileActive(t *testing.T) {
	gate := NewUsageGate()

	rel, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("AcquireUse failed: %v", err)
	}

	// TryExclusive should fail with ErrToolInUse
	_, err = gate.TryExclusive()
	if !errors.Is(err, ErrToolInUse) {
		t.Fatalf("expected ErrToolInUse, got: %v", err)
	}

	rel()

	// Once released, TryExclusive succeeds
	exRel, err := gate.TryExclusive()
	if err != nil {
		t.Fatalf("TryExclusive failed after release: %v", err)
	}
	if !gate.IsExclusive() {
		t.Errorf("expected IsExclusive() == true")
	}
	if !gate.InUse() {
		t.Errorf("expected InUse() == true while exclusive is held")
	}
	exRel()
	if gate.IsExclusive() {
		t.Errorf("expected IsExclusive() == false after release")
	}
	if gate.InUse() {
		t.Errorf("expected InUse() == false after exclusive release")
	}
}

func TestUsageGate_ExecutionRefusedWhileExclusiveHeld(t *testing.T) {
	gate := NewUsageGate()

	exRel, err := gate.TryExclusive()
	if err != nil {
		t.Fatalf("TryExclusive failed: %v", err)
	}

	// Normal AcquireUse should fail with ErrExclusiveMaintenance
	_, err = gate.AcquireUse()
	if !errors.Is(err, ErrExclusiveMaintenance) {
		t.Fatalf("expected ErrExclusiveMaintenance when exclusive held, got: %v", err)
	}

	// Another TryExclusive fails with ErrToolInUse
	_, err = gate.TryExclusive()
	if !errors.Is(err, ErrToolInUse) {
		t.Fatalf("expected ErrToolInUse on second TryExclusive, got: %v", err)
	}

	exRel()

	// Normal AcquireUse succeeds after exclusive released
	rel, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("AcquireUse failed after exclusive release: %v", err)
	}
	rel()
}

func TestUsageGate_IdempotentRelease(t *testing.T) {
	gate := NewUsageGate()

	rel, err := gate.AcquireUse()
	if err != nil {
		t.Fatalf("AcquireUse failed: %v", err)
	}

	// Call release multiple times
	rel()
	rel()
	rel()

	if gate.InUse() {
		t.Errorf("expected InUse() == false after idempotent releases")
	}

	// Same for exclusive release
	exRel, err := gate.TryExclusive()
	if err != nil {
		t.Fatalf("TryExclusive failed: %v", err)
	}
	exRel()
	exRel()
	exRel()

	if gate.InUse() || gate.IsExclusive() {
		t.Errorf("expected InUse() == false and IsExclusive() == false")
	}
}

func TestUsageGate_ConcurrentStress(t *testing.T) {
	gate := NewUsageGate()
	const workers = 20
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				rel, err := gate.AcquireUse()
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				time.Sleep(100 * time.Microsecond)
				rel()
			}
		}()
	}

	wg.Wait()

	if gate.InUse() {
		t.Errorf("expected InUse() == false after all workers done")
	}
}

func TestEngine_Available_NoUncontrolledExec(t *testing.T) {
	// 1. Empty path must report unavailable
	emptyEng := NewEngine("", "")
	if emptyEng.Available() {
		t.Errorf("expected empty path Available() == false")
	}

	// 2. Non-existent path must report unavailable (no false positive)
	nonExistentPath := filepath.Join(t.TempDir(), "nonexistent-yt-dlp.exe")
	nonExistentEng := NewEngine(nonExistentPath, "")
	if nonExistentEng.Available() {
		t.Errorf("expected non-existent path Available() == false")
	}

	// 3. Genuine existing file must report available without running exec.Command
	realDummy := filepath.Join(t.TempDir(), "real-yt-dlp.exe")
	if err := os.WriteFile(realDummy, []byte("dummy"), 0755); err != nil {
		t.Fatalf("failed to write dummy executable: %v", err)
	}
	realEng := NewEngine(realDummy, "")
	if !realEng.Available() {
		t.Errorf("expected real file Available() == true")
	}
}

func TestEngine_TryExclusiveCoordination(t *testing.T) {
	eng := NewEngine("C:\\dummy\\yt-dlp.exe", "")

	// TryExclusive should succeed when engine idle
	rel, err := eng.TryExclusive()
	if err != nil {
		t.Fatalf("eng.TryExclusive failed: %v", err)
	}
	if !eng.InUse() {
		t.Errorf("expected eng.InUse() == true")
	}

	// Subsequent TryExclusive fails
	_, err = eng.TryExclusive()
	if !errors.Is(err, ErrToolInUse) {
		t.Fatalf("expected ErrToolInUse, got: %v", err)
	}

	rel()
	if eng.InUse() {
		t.Errorf("expected eng.InUse() == false after release")
	}
}

func TestEngine_AnalyzeWithPolicy_RefusesWhenExclusiveHeld(t *testing.T) {
	eng := NewEngine("C:\\dummy\\yt-dlp.exe", "")

	rel, err := eng.TryExclusive()
	if err != nil {
		t.Fatalf("TryExclusive failed: %v", err)
	}
	defer rel()

	// AnalyzeWithPolicy must refuse immediately with ErrExclusiveMaintenance because exclusive is held
	ctx := context.Background()
	_, err = eng.AnalyzeWithPolicy(ctx, "https://youtube.com/watch?v=123", nil)
	if !errors.Is(err, ErrExclusiveMaintenance) {
		t.Fatalf("expected ErrExclusiveMaintenance for AnalyzeWithPolicy during exclusive, got: %v", err)
	}
}
