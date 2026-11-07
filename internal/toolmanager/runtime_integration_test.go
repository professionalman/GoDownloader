package toolmanager

import (
	"context"
	"errors"
	"testing"

	"downloader/internal/engine/ytdlp"
	"downloader/internal/job"
)

// Activation coordinates through the real Checkpoint 1 UsageGate owned by the ytdlp engine.
func TestRuntime_ActivationRefusedWhileEngineInUse(t *testing.T) {
	for _, use := range []string{"analysis", "download"} {
		t.Run(use, func(t *testing.T) {
			env := newAcquisitionEnv(t)
			env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
			eng := ytdlp.NewEngine("", "")
			m := env.manager(t)
			m.SetUsageCoordinator(eng)

			// Analyses and downloads both hold shared use for the life of the yt-dlp process.
			release, err := eng.AcquireUse()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrToolInUse) {
				t.Fatalf("expected ErrToolInUse during %s, got %v", use, err)
			}
			if eng.MaintenancePending() || m.RestartRequired() {
				t.Fatal("refused activation must leave the gate open")
			}
			if rec, _ := env.repo.GetManagedTool(context.Background(), ToolYtdlp, job.ManagedToolStatusActive); rec != nil {
				t.Fatal("refused activation changed the ledger")
			}
			release()

			// Once idle, activation succeeds and the gate stays closed until restart.
			if _, err := m.InstallYtdlp(context.Background()); err != nil {
				t.Fatalf("install when idle: %v", err)
			}
			if !eng.MaintenancePending() {
				t.Fatal("gate should be held after activation")
			}
			if _, err := eng.AcquireUse(); !errors.Is(err, ytdlp.ErrExclusiveMaintenance) {
				t.Fatalf("no yt-dlp execution may start after activation, got %v", err)
			}
			if _, err := eng.Start(context.Background(), &job.Job{ID: "j1", Source: "https://example.com/v", Type: job.TypeMedia}, t.TempDir()); err == nil {
				t.Fatal("engine Start must be refused while restart is pending")
			}
		})
	}
}

func TestRuntime_AlreadyCurrentDoesNotBlockEngine(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	eng := ytdlp.NewEngine("", "")
	m := env.manager(t)
	m.SetUsageCoordinator(eng)
	res, err := m.UpdateYtdlp(context.Background())
	if err != nil || !res.AlreadyCurrent {
		t.Fatalf("expected already-current: %+v %v", res, err)
	}
	if eng.MaintenancePending() {
		t.Fatal("already-current must not block yt-dlp use")
	}
	release, err := eng.AcquireUse()
	if err != nil {
		t.Fatalf("yt-dlp use should remain available: %v", err)
	}
	release()
}

func TestRuntime_FailedActivationReopensRealGate(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	eng := ytdlp.NewEngine("", "")
	m := env.manager(t)
	m.SetUsageCoordinator(eng)
	m.ledger = &failingLedger{IManagedToolLedger: env.repo, commitErr: errors.New("injected")}
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrActivationFailed) {
		t.Fatalf("expected ErrActivationFailed, got %v", err)
	}
	if eng.MaintenancePending() {
		t.Fatal("failed activation must reopen the shared gate")
	}
	release, err := eng.AcquireUse()
	if err != nil {
		t.Fatalf("yt-dlp use must be possible after failed activation: %v", err)
	}
	release()
}
