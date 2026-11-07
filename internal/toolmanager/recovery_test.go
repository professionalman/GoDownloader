package toolmanager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downloader/internal/job"
)

// installVersions installs each version in order, each as a fresh backend process.
func installVersions(t *testing.T, env *acquisitionEnv, versions ...string) map[string]string {
	t.Helper()
	digests := map[string]string{}
	for _, v := range versions {
		exe := env.knownVersion(v)
		digests[v] = sha256Hex(exe)
		env.rel.publishSigned(env.signer, v, exe)
		env.gate = &fakeGate{}
		if _, err := env.manager(t).UpdateYtdlp(context.Background()); err != nil {
			t.Fatalf("install %s: %v", v, err)
		}
	}
	return digests
}

func storePath(env *acquisitionEnv, digest string) string {
	return filepath.Join(env.dataDir, "bin", "yt-dlp", digest+".exe")
}

func TestRecovery_ActiveDeletedRestoresLKG(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01", "2026.10.26")
	if err := os.Remove(storePath(env, d["2026.10.26"])); err != nil {
		t.Fatal(err)
	}
	m := env.manager(t)
	info, err := m.Resolve(context.Background(), ToolYtdlp)
	if err != nil || info.SHA256 != d["2026.09.01"] || info.VerificationStatus != VerificationStatusAuthenticated {
		t.Fatalf("expected authenticated LKG 2026.09.01, got %+v (%v)", info, err)
	}
	if a := activeRecord(t, env); a.SHA256 != d["2026.09.01"] {
		t.Fatalf("ledger active should be the restored LKG, got %s", a.Version)
	}
	if lkgRecord(t, env) != nil {
		t.Fatal("LKG slot should be empty after promotion")
	}
	recs, _ := env.repo.ListManagedTools(context.Background(), ToolYtdlp)
	quarantined := false
	for _, r := range recs {
		if r.SHA256 == d["2026.10.26"] && r.Status == job.ManagedToolStatusQuarantined {
			quarantined = true
		}
	}
	if !quarantined {
		t.Fatal("failed active should be quarantined")
	}
	if !strings.Contains(m.YtdlpStatus(context.Background()).Diagnostic, "restored") {
		t.Fatal("expected a restoration diagnostic")
	}
}

func TestRecovery_ActiveHashMismatchRestoresLKGWithoutExecutingCorruptFile(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01", "2026.10.26")
	corrupt := []byte("corrupted by disk or tampering")
	env.recorder.versions[sha256Hex(corrupt)] = "2026.10.26" // would "work" if executed
	if err := os.WriteFile(storePath(env, d["2026.10.26"]), corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(env.recorder.executedHashes())
	info, _ := env.manager(t).Resolve(context.Background(), ToolYtdlp)
	if info == nil || info.SHA256 != d["2026.09.01"] {
		t.Fatalf("expected LKG restoration, got %+v", info)
	}
	for _, h := range env.recorder.executedHashes()[before:] {
		if h == sha256Hex(corrupt) {
			t.Fatal("corrupt active binary was executed")
		}
	}
}

func TestRecovery_BothInvalidFallsBackTruthfullyWithoutLedgerChange(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01", "2026.10.26")
	for _, v := range []string{"2026.09.01", "2026.10.26"} {
		if err := os.Remove(storePath(env, d[v])); err != nil {
			t.Fatal(err)
		}
	}
	// A genuine PATH yt-dlp exists.
	pathExe := filepath.Join(t.TempDir(), "yt-dlp.exe")
	pathBytes := []byte("system yt-dlp")
	_ = os.WriteFile(pathExe, pathBytes, 0o755)
	env.recorder.versions[sha256Hex(pathBytes)] = "2026.01.01"
	m := env.manager(t)
	m.lookPathFn = func(string) (string, error) { return pathExe, nil }

	info, err := m.Resolve(context.Background(), ToolYtdlp)
	if err != nil || info.Provenance != ProvenanceSystemPath || info.VerificationStatus != VerificationStatusVerified {
		t.Fatalf("expected truthful PATH fallback, got %+v (%v)", info, err)
	}
	if a := activeRecord(t, env); a.SHA256 != d["2026.10.26"] {
		t.Fatal("ledger must not change when no valid LKG exists")
	}
	st := m.YtdlpStatus(context.Background())
	if st.Authenticated || st.Ownership != StatusOwnershipSystem || !strings.Contains(st.Diagnostic, "no valid previous version") {
		t.Fatalf("status must not claim authenticity: %+v", st)
	}
}

func TestRecovery_BothInvalidNoFallbackIsUnavailable(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.10.26")
	_ = os.Remove(storePath(env, d["2026.10.26"]))
	info, err := env.manager(t).Resolve(context.Background(), ToolYtdlp)
	if err == nil || info.Available() {
		t.Fatalf("expected yt-dlp unavailable, got %+v", info)
	}
}

func TestRecovery_ProbeFailureOfVerifiedBinaryDoesNotRewriteLedger(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01", "2026.10.26")
	env.recorder.failAll = true // e.g. antivirus lock or transient start failure
	info, _ := env.manager(t).Resolve(context.Background(), ToolYtdlp)
	if info != nil && info.Available() {
		t.Fatalf("binary that cannot start must not be reported available: %+v", info)
	}
	if a := activeRecord(t, env); a.SHA256 != d["2026.10.26"] {
		t.Fatal("transient execution failure must not trigger LKG restoration")
	}
	if l := lkgRecord(t, env); l == nil || l.SHA256 != d["2026.09.01"] {
		t.Fatal("LKG must be untouched after a transient failure")
	}
}

func TestRecovery_CrashBeforeLedgerCommitKeepsOldActiveAndCleansOrphans(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01")
	// Simulate a crash after a verified candidate was promoted but before the ledger commit, plus a crash
	// mid-download that left a staging file.
	orphan := fakeExeBytes("2026.10.26")
	env.recorder.versions[sha256Hex(orphan)] = "2026.10.26"
	orphanPath := storePath(env, sha256Hex(orphan))
	_ = os.WriteFile(orphanPath, orphan, 0o644)
	partial := filepath.Join(env.dataDir, "bin", "yt-dlp", stagingPrefix+"123.exe")
	_ = os.WriteFile(partial, []byte("half"), 0o644)
	stale := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(partial, stale, stale) // left by a crash long ago
	freshPartial := filepath.Join(env.dataDir, "bin", "yt-dlp", stagingPrefix+"live.exe")
	_ = os.WriteFile(freshPartial, []byte("in progress elsewhere"), 0o644)
	unknown := filepath.Join(env.dataDir, "bin", "yt-dlp", "notes.txt")
	_ = os.WriteFile(unknown, []byte("user file"), 0o644)

	m := env.manager(t)
	m.CleanupManagedStore(context.Background())
	info, err := m.Resolve(context.Background(), ToolYtdlp)
	if err != nil || info.SHA256 != d["2026.09.01"] {
		t.Fatalf("old active must remain selected: %+v (%v)", info, err)
	}
	for _, h := range env.recorder.executedHashes() {
		if h == sha256Hex(orphan) {
			t.Fatal("orphan candidate was executed")
		}
	}
	// The orphan's ownership cannot be proved (it may belong to another database), so it is kept, never run.
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatal("orphan of unprovable ownership must be kept")
	}
	// No process holds the acquisition ownership lock, so every staging file is provably abandoned (crashed
	// acquisitions) and is removed regardless of its age. A live acquisition is covered by
	// TestAcquisition_CleanupNeverDeletesAnotherProcessesLiveStaging.
	for _, p := range []string{partial, freshPartial} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("abandoned staging file %s should be removed", filepath.Base(p))
		}
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal("unknown files must never be deleted")
	}
}

func TestRecovery_LedgerRecordPointingOutsideStoreIsRejected(t *testing.T) {
	env := newAcquisitionEnv(t)
	outside := filepath.Join(t.TempDir(), "evil.exe")
	evil := []byte("evil")
	_ = os.WriteFile(outside, evil, 0o755)
	env.recorder.versions[sha256Hex(evil)] = "2026.10.26"
	sha := sha256Hex(evil)
	for _, name := range []string{`..\..\evil.exe`, outside, sha + ".exe.lnk"} {
		rec := &job.ManagedToolRecord{Name: ToolYtdlp, SHA256: sha, Version: "2026.10.26", FileName: name,
			SignerFingerprint: env.signer.root.fingerprint}
		res := env.manager(t).verifyManagedRecord(context.Background(), rec)
		if res.info != nil || res.integrityErr == nil {
			t.Fatalf("record with file name %q must fail integrity", name)
		}
	}
	for _, h := range env.recorder.executedHashes() {
		if h == sha {
			t.Fatal("file outside managed storage was executed")
		}
	}
}

func TestRecovery_RecordFromUntrustedKeyRejected(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.10.26")
	m := env.manager(t)
	other := newTestSigner(t)
	m.trustRootFn = func() (*trustRoot, error) { return other.root, nil } // pinned key changed
	if info, _ := m.Resolve(context.Background(), ToolYtdlp); info != nil && info.SHA256 == d["2026.10.26"] {
		t.Fatal("record authenticated by a different key must not be trusted")
	}
}

func TestRecovery_LegacyPathPreservedAndNeverAuthenticated(t *testing.T) {
	env := newAcquisitionEnv(t)
	legacy := filepath.Join(env.dataDir, "bin", "yt-dlp.exe")
	_ = os.MkdirAll(filepath.Dir(legacy), 0o755)
	legacyBytes := []byte("manually placed yt-dlp")
	_ = os.WriteFile(legacy, legacyBytes, 0o755)
	env.recorder.versions[sha256Hex(legacyBytes)] = "2025.01.01"

	info, err := env.manager(t).Resolve(context.Background(), ToolYtdlp)
	if err != nil || info.Provenance != ProvenanceManagedLegacy || info.VerificationStatus != VerificationStatusVerified {
		t.Fatalf("legacy binary must be version-probed only: %+v (%v)", info, err)
	}
	installVersions(t, env, "2026.10.26")
	m := env.manager(t)
	m.CleanupManagedStore(context.Background())
	info, _ = m.Resolve(context.Background(), ToolYtdlp)
	if info.Provenance != ProvenanceManagedRelease {
		t.Fatal("authenticated managed release should take precedence after restart")
	}
	data, err := os.ReadFile(legacy)
	if err != nil || string(data) != string(legacyBytes) {
		t.Fatal("legacy managed-path binary must remain untouched")
	}
}

func TestRecovery_ExternalConfiguredBinaryUntouchedByInstall(t *testing.T) {
	env := newAcquisitionEnv(t)
	cfgExe := filepath.Join(t.TempDir(), "my-yt-dlp.exe")
	cfgBytes := []byte("user configured yt-dlp")
	_ = os.WriteFile(cfgExe, cfgBytes, 0o755)
	env.recorder.versions[sha256Hex(cfgBytes)] = "2026.01.01"
	m := env.manager(t)
	m.RegisterConfig(ToolYtdlp, cfgExe)
	if info, _ := m.Resolve(context.Background(), ToolYtdlp); info.Provenance != ProvenanceUserConfigured {
		t.Fatalf("expected configured yt-dlp, got %+v", info)
	}
	installVersions(t, env, "2026.10.26")
	if data, _ := os.ReadFile(cfgExe); string(data) != string(cfgBytes) {
		t.Fatal("user-configured executable was modified")
	}
}
