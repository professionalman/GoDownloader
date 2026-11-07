package toolmanager

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"downloader/internal/job"
	"downloader/internal/runtimeguard"
)

func activeRecord(t *testing.T, env *acquisitionEnv) *job.ManagedToolRecord {
	t.Helper()
	rec, err := env.repo.GetManagedTool(context.Background(), ToolYtdlp, job.ManagedToolStatusActive)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func lkgRecord(t *testing.T, env *acquisitionEnv) *job.ManagedToolRecord {
	t.Helper()
	rec, err := env.repo.GetManagedTool(context.Background(), ToolYtdlp, job.ManagedToolStatusLastKnownGood)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestAcquisition_FirstInstallActivatesAuthenticatedBinary(t *testing.T) {
	env := newAcquisitionEnv(t)
	exe := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", exe)
	m := env.manager(t)

	res, err := m.InstallYtdlp(context.Background())
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if res.Version != "2026.10.26" || !res.RestartRequired || res.AlreadyCurrent || res.Ownership != OwnershipManaged {
		t.Fatalf("unexpected result: %+v", res)
	}
	digest := sha256Hex(exe)
	rec := activeRecord(t, env)
	if rec == nil || rec.SHA256 != digest || rec.Version != "2026.10.26" || rec.FileName != digest+".exe" ||
		rec.SignerFingerprint != env.signer.root.fingerprint {
		t.Fatalf("ledger active record wrong: %+v", rec)
	}
	data, err := os.ReadFile(filepath.Join(env.dataDir, "bin", "yt-dlp", digest+".exe"))
	if err != nil || !bytes.Equal(data, exe) {
		t.Fatalf("content-addressed binary missing or wrong: %v", err)
	}
	for _, name := range storeFiles(t, env.dataDir) {
		if strings.HasPrefix(name, stagingPrefix) {
			t.Fatalf("staging file left behind: %s", name)
		}
	}
	env.assertOnlyAuthenticatedExecuted(t, digest)
	if len(env.recorder.executedHashes()) == 0 {
		t.Fatal("candidate was never version-probed")
	}
	// Gate held until restart; restart required; further acquisitions refused.
	if exclusive, releases := env.gate.state(); !exclusive || releases != 0 {
		t.Fatalf("gate should stay exclusive until restart: exclusive=%v releases=%d", exclusive, releases)
	}
	if !m.RestartRequired() {
		t.Fatal("restart should be required")
	}
	if _, err := m.UpdateYtdlp(context.Background()); !errors.Is(err, ErrRestartRequired) {
		t.Fatalf("expected ErrRestartRequired before restart, got %v", err)
	}
	if env.rel.hit("exe") != 1 {
		t.Fatalf("executable downloaded %d times", env.rel.hit("exe"))
	}
}

func TestAcquisition_RestartResolvesAuthenticatedManagedBinary(t *testing.T) {
	env := newAcquisitionEnv(t)
	exe := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", exe)
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}

	restarted := env.manager(t) // new backend process, same data and ledger
	info, err := restarted.Resolve(context.Background(), ToolYtdlp)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if info.VerificationStatus != VerificationStatusAuthenticated || info.Provenance != ProvenanceManagedRelease ||
		info.OwnershipMode != OwnershipManaged || !info.Available() || info.SHA256 != sha256Hex(exe) {
		t.Fatalf("restart lost authenticated provenance: %+v", info)
	}
	if filepath.Dir(info.ExecutablePath) != filepath.Join(env.dataDir, "bin", "yt-dlp") {
		t.Fatalf("executable outside managed store: %s", info.ExecutablePath)
	}
	if restarted.RestartRequired() {
		t.Fatal("restart-required must not persist across restarts")
	}
	st := restarted.YtdlpStatus(context.Background())
	if !st.Authenticated || st.Ownership != StatusOwnershipManaged || st.CanInstall || !st.CanUpdate {
		t.Fatalf("status after restart: %+v", st)
	}
	env.assertOnlyAuthenticatedExecuted(t, sha256Hex(exe))
}

func TestAcquisition_AlreadyCurrentIsIdempotentAndKeepsGateOpen(t *testing.T) {
	env := newAcquisitionEnv(t)
	exe := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", exe)
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.gate = &fakeGate{}
	m := env.manager(t)
	res, err := m.UpdateYtdlp(context.Background())
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !res.AlreadyCurrent || res.RestartRequired || m.RestartRequired() {
		t.Fatalf("expected already-current without restart: %+v", res)
	}
	if exclusive, _ := env.gate.state(); exclusive {
		t.Fatal("already-current must not close the usage gate")
	}
	if env.rel.hit("exe") != 1 || env.rel.hit("manifest") != 1 {
		t.Fatalf("already-current re-downloaded assets: exe=%d manifest=%d", env.rel.hit("exe"), env.rel.hit("manifest"))
	}
}

func TestAcquisition_UpdateKeepsPreviousAsLKGAndBoundsHistory(t *testing.T) {
	env := newAcquisitionEnv(t)
	versions := []string{"2026.08.01", "2026.09.01", "2026.10.26"}
	var digests []string
	for _, v := range versions {
		exe := env.knownVersion(v)
		digests = append(digests, sha256Hex(exe))
		env.rel.publishSigned(env.signer, v, exe)
		env.gate = &fakeGate{}
		if _, err := env.manager(t).UpdateYtdlp(context.Background()); err != nil {
			t.Fatalf("update to %s: %v", v, err)
		}
	}
	if a := activeRecord(t, env); a.SHA256 != digests[2] {
		t.Fatalf("active %s, want %s", a.Version, versions[2])
	}
	if l := lkgRecord(t, env); l == nil || l.SHA256 != digests[1] {
		t.Fatalf("LKG should be the previous active %s, got %+v", versions[1], l)
	}
	files := strings.Join(storeFiles(t, env.dataDir), ",")
	if !strings.Contains(files, digests[2]) || !strings.Contains(files, digests[1]) {
		t.Fatalf("active and LKG binaries must be retained: %s", files)
	}
	if strings.Contains(files, digests[0]) {
		t.Fatalf("obsolete binary of %s should be garbage-collected: %s", versions[0], files)
	}
}

func TestAcquisition_DowngradeRefused(t *testing.T) {
	env := newAcquisitionEnv(t)
	newer := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", newer)
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	older := env.knownVersion("2026.09.01")
	env.rel.publishSigned(env.signer, "2026.09.01", older)
	env.gate = &fakeGate{}
	if _, err := env.manager(t).UpdateYtdlp(context.Background()); !errors.Is(err, ErrDowngradeRefused) {
		t.Fatalf("expected ErrDowngradeRefused, got %v", err)
	}
	if activeRecord(t, env).SHA256 != sha256Hex(newer) {
		t.Fatal("downgrade changed the active record")
	}
	if env.rel.hit("exe") != 1 {
		t.Fatal("older executable must not be downloaded")
	}
}

func TestAcquisition_HashMismatchNeverExecutes(t *testing.T) {
	env := newAcquisitionEnv(t)
	signedExe := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", signedExe)
	// The CDN serves different bytes than the signed manifest authenticates (also registered as runnable,
	// so only the hash check can stop it).
	tampered := env.knownVersion("2026.10.26-tampered")
	env.rel.exeServe = func(w http.ResponseWriter) { _, _ = w.Write(tampered) }
	env.rel.metaMutator = func(meta map[string]any) {
		meta["assets"].([]any)[0].(map[string]any)["size"] = len(tampered)
	}
	m := env.manager(t)
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrHashMismatch) {
		t.Fatalf("expected ErrHashMismatch, got %v", err)
	}
	if n := len(env.recorder.executedHashes()); n != 0 {
		t.Fatalf("unauthenticated candidate executed %d time(s)", n)
	}
	if activeRecord(t, env) != nil || len(storeFiles(t, env.dataDir)) != 0 {
		t.Fatalf("failed install left ledger or files: %v", storeFiles(t, env.dataDir))
	}
	if exclusive, _ := env.gate.state(); exclusive || m.RestartRequired() {
		t.Fatal("failed install must not close the gate or require restart")
	}
}

func TestAcquisition_SignatureFailuresStopBeforeExecutableDownload(t *testing.T) {
	cases := map[string]func(env *acquisitionEnv){
		"tampered manifest": func(env *acquisitionEnv) {
			env.rel.mu.Lock()
			env.rel.manifest = bytes.Replace(env.rel.manifest, []byte("yt-dlp.exe"), []byte("yt-dlp.exf"), 1)
			env.rel.mu.Unlock()
		},
		"wrong signer": func(env *acquisitionEnv) {
			impostor := newTestSigner(env.rel.t)
			env.rel.mu.Lock()
			env.rel.sig = impostor.signFn(env.rel.manifest)
			env.rel.mu.Unlock()
		},
		"garbage signature": func(env *acquisitionEnv) {
			env.rel.mu.Lock()
			env.rel.sig = bytes.Repeat([]byte{0x89}, 40)
			env.rel.mu.Unlock()
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			env := newAcquisitionEnv(t)
			env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
			mutate(env)
			env.rel.metaMutator = func(meta map[string]any) {
				env.rel.mu.Lock()
				defer env.rel.mu.Unlock()
				assets := meta["assets"].([]any)
				assets[1].(map[string]any)["size"] = len(env.rel.manifest)
				assets[2].(map[string]any)["size"] = len(env.rel.sig)
			}
			_, err := env.manager(t).InstallYtdlp(context.Background())
			if !errors.Is(err, ErrInvalidSignature) && !errors.Is(err, ErrUntrustedSigningKey) {
				t.Fatalf("expected signature rejection, got %v", err)
			}
			if name == "wrong signer" && !errors.Is(err, ErrUntrustedSigningKey) {
				t.Fatalf("wrong signer should be ErrUntrustedSigningKey, got %v", err)
			}
			if env.rel.hit("exe") != 0 || len(env.recorder.executedHashes()) != 0 || activeRecord(t, env) != nil {
				t.Fatal("signature failure must stop before downloading or executing the executable")
			}
		})
	}
}

func TestAcquisition_CandidateVersionMustMatchReleaseTag(t *testing.T) {
	env := newAcquisitionEnv(t)
	exe := fakeExeBytes("2026.10.26")
	env.recorder.versions[sha256Hex(exe)] = "2026.10.25" // authentic bytes, unexpected self-reported version
	env.rel.publishSigned(env.signer, "2026.10.26", exe)
	if _, err := env.manager(t).InstallYtdlp(context.Background()); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("expected ErrInvalidRelease, got %v", err)
	}
	if activeRecord(t, env) != nil {
		t.Fatal("mismatched version activated")
	}
}

func TestAcquisition_ToolInUseRefusesActivationAndKeepsLedger(t *testing.T) {
	env := newAcquisitionEnv(t)
	exe := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", exe)
	env.gate.inUse = true // an analysis or download holds shared use
	m := env.manager(t)
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrToolInUse) {
		t.Fatalf("expected ErrToolInUse, got %v", err)
	}
	if activeRecord(t, env) != nil || m.RestartRequired() {
		t.Fatal("in-use refusal must not activate or require restart")
	}
	// The verified candidate is an orphan unknown to the ledger. Cleanup keeps it (its ownership cannot be proved;
	// another database may share the data directory), and it is never executed outside acquisition.
	if len(storeFiles(t, env.dataDir)) != 1 {
		t.Fatalf("expected one orphaned verified candidate, got %v", storeFiles(t, env.dataDir))
	}
	env.manager(t).CleanupManagedStore(context.Background())
	if files := storeFiles(t, env.dataDir); len(files) != 1 {
		t.Fatalf("orphan of unprovable ownership must be kept: %v", files)
	}
	// Once idle, the retry reuses the verified orphan (re-hashed) rather than accumulating copies.
	env.gate.inUse = false
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatalf("retry after in-use refusal: %v", err)
	}
	if files := storeFiles(t, env.dataDir); len(files) != 1 {
		t.Fatalf("retry should reuse the orphan, got %v", files)
	}
}

// Regression (finding 1): a second database sharing the data directory (e.g. the desktop --diagnostic-smoke
// mode) must never delete binaries owned by another ledger during its startup cleanup.
func TestAcquisition_CleanupNeverDeletesAnotherLedgersBinaries(t *testing.T) {
	primary := newAcquisitionEnv(t)
	d := installVersions(t, primary, "2026.09.01", "2026.10.26")

	other := newAcquisitionEnv(t)
	other.dataDir = primary.dataDir // same data directory, separate database (fresh, empty ledger)
	other.recorder = primary.recorder
	other.manager(t).CleanupManagedStore(context.Background())

	for _, v := range []string{"2026.09.01", "2026.10.26"} {
		if _, err := os.Stat(storePath(primary, d[v])); err != nil {
			t.Fatalf("binary %s of the primary ledger was deleted by another database's cleanup", v)
		}
	}
	info, err := primary.manager(t).Resolve(context.Background(), ToolYtdlp)
	if err != nil || info.SHA256 != d["2026.10.26"] || info.VerificationStatus != VerificationStatusAuthenticated {
		t.Fatalf("primary lost its authenticated binary: %+v %v", info, err)
	}
}

// Regression (final safety check A): while another process holds the acquisition ownership lock (a live
// acquisition writing a staging file), cleanup must keep every staging file, however old it looks, and a
// second acquisition must be refused. Once the lock is free, abandoned staging files are removed.
func TestAcquisition_CleanupNeverDeletesAnotherProcessesLiveStaging(t *testing.T) {
	env := newAcquisitionEnv(t)
	installVersions(t, env, "2026.09.01")
	root := filepath.Join(env.dataDir, "bin", "yt-dlp")
	partial := filepath.Join(root, stagingPrefix+"live-elsewhere.exe")
	if err := os.WriteFile(partial, []byte("being written by another process"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-72 * time.Hour) // a stalled writer or clock jump: age proves nothing
	_ = os.Chtimes(partial, old, old)

	other, err := runtimeguard.Acquire(filepath.Join(root, "acquisition")) // the other process's live acquisition
	if err != nil {
		t.Fatalf("simulate other process lock: %v", err)
	}
	m := env.manager(t)
	m.CleanupManagedStore(context.Background())
	if _, err := os.Stat(partial); err != nil {
		t.Fatal("cleanup deleted a staging file owned by a live acquisition in another process")
	}
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	env.gate = &fakeGate{}
	if _, err := env.manager(t).UpdateYtdlp(context.Background()); !errors.Is(err, ErrAcquisitionInProgress) {
		t.Fatalf("expected ErrAcquisitionInProgress while another process acquires, got %v", err)
	}
	if env.rel.hit("exe") != 1 {
		t.Fatal("a concurrent acquisition must not download")
	}

	_ = other.Close() // the other process finished (or died; the OS releases its lock)
	env.manager(t).CleanupManagedStore(context.Background())
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("abandoned staging file should be removed once no acquisition owns the store")
	}
}

// Regression: startup cleanup on a data directory without a managed store must not create the store or its
// ownership lock file (found by the repository-write check: package-relative test data directories).
func TestAcquisition_StartupCleanupCreatesNothingWithoutStore(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.manager(t).CleanupManagedStore(context.Background())
	if _, err := os.Lstat(filepath.Join(env.dataDir, "bin", "yt-dlp")); !os.IsNotExist(err) {
		t.Fatalf("startup cleanup created the managed store (err=%v)", err)
	}
}

// Regression (finding 3): reinstalling a release whose binary is intact must not close the usage gate or require
// a restart, even when the probe fails transiently.
func TestAcquisition_IntactSameVersionIsAlreadyCurrentDespiteTransientProbe(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.gate = &fakeGate{}
	env.recorder.failAll = true // e.g. antivirus delaying the cold start
	m := env.manager(t)
	res, err := m.UpdateYtdlp(context.Background())
	if err != nil || !res.AlreadyCurrent || res.RestartRequired || m.RestartRequired() {
		t.Fatalf("expected already-current without restart: %+v %v", res, err)
	}
	if exclusive, _ := env.gate.state(); exclusive {
		t.Fatal("usage gate closed although nothing changed")
	}
	if env.rel.hit("exe") != 1 {
		t.Fatal("intact binary must not be re-downloaded")
	}
}

// Regression (finding 4): a transient read error on the active binary (sharing violation, access denied) must
// not quarantine it or rewrite the ledger.
func TestRecovery_TransientReadErrorDoesNotQuarantine(t *testing.T) {
	env := newAcquisitionEnv(t)
	d := installVersions(t, env, "2026.09.01", "2026.10.26")
	m := env.manager(t)
	// Only the active binary is momentarily unreadable; the LKG is fine, so a wrongly classified integrity
	// failure would visibly restore the LKG.
	m.hashFileFn = func(path string) (string, int64, error) {
		if strings.Contains(path, d["2026.10.26"]) {
			return "", 0, errors.New("The process cannot access the file because it is being used by another process.")
		}
		return hashRegularFile(path)
	}
	if info, _ := m.Resolve(context.Background(), ToolYtdlp); info != nil && info.Available() && info.VerificationStatus == VerificationStatusAuthenticated {
		t.Fatal("unreadable binary must not be reported authenticated")
	}
	if a := activeRecord(t, env); a.SHA256 != d["2026.10.26"] {
		t.Fatal("transient read error triggered LKG restoration")
	}
	if l := lkgRecord(t, env); l == nil || l.SHA256 != d["2026.09.01"] {
		t.Fatal("LKG changed after a transient read error")
	}
}

// failingLedger injects ledger failures and concurrent changes around activation.
type failingLedger struct {
	job.IManagedToolLedger
	commitErr  error
	afterReads int
	reads      int
	mutate     func()
}

func (f *failingLedger) GetManagedTool(ctx context.Context, name, status string) (*job.ManagedToolRecord, error) {
	f.reads++
	if f.mutate != nil && f.reads == f.afterReads {
		f.mutate()
	}
	return f.IManagedToolLedger.GetManagedTool(ctx, name, status)
}

func (f *failingLedger) CommitToolActivation(ctx context.Context, n, p *job.ManagedToolRecord) error {
	if f.commitErr != nil {
		return f.commitErr
	}
	return f.IManagedToolLedger.CommitToolActivation(ctx, n, p)
}

func TestAcquisition_LedgerFailureReopensGateAndPreservesActive(t *testing.T) {
	env := newAcquisitionEnv(t)
	first := env.knownVersion("2026.09.01")
	env.rel.publishSigned(env.signer, "2026.09.01", first)
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := env.knownVersion("2026.10.26")
	env.rel.publishSigned(env.signer, "2026.10.26", second)
	env.gate = &fakeGate{}
	m := env.manager(t)
	m.ledger = &failingLedger{IManagedToolLedger: env.repo, commitErr: errors.New("disk I/O error")}
	if _, err := m.UpdateYtdlp(context.Background()); !errors.Is(err, ErrActivationFailed) {
		t.Fatalf("expected ErrActivationFailed, got %v", err)
	}
	if exclusive, releases := env.gate.state(); exclusive || releases != 1 {
		t.Fatalf("gate must reopen after failed activation: exclusive=%v releases=%d", exclusive, releases)
	}
	if a := activeRecord(t, env); a.SHA256 != sha256Hex(first) || lkgRecord(t, env) != nil {
		t.Fatal("failed activation changed the ledger")
	}
	if _, err := os.Stat(filepath.Join(env.dataDir, "bin", "yt-dlp", sha256Hex(first)+".exe")); err != nil {
		t.Fatal("failed update removed the previously active binary")
	}
	if m.RestartRequired() {
		t.Fatal("failed activation must not require restart")
	}
}

func TestAcquisition_ConcurrentLedgerChangeAbortsActivation(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	m := env.manager(t)
	other := env.knownVersion("2026.10.20")
	otherSHA := sha256Hex(other)
	fl := &failingLedger{IManagedToolLedger: env.repo}
	// Between the update decision and the exclusive revalidation, another writer activates a binary.
	fl.afterReads = 2
	fl.mutate = func() {
		_ = env.repo.CommitToolActivation(context.Background(), &job.ManagedToolRecord{
			Name: ToolYtdlp, SHA256: otherSHA, Version: "2026.10.20", ReleaseTag: "2026.10.20",
			FileName: otherSHA + ".exe", SignerFingerprint: env.signer.root.fingerprint, AuthenticatedAt: m.nowFn(),
		}, nil)
	}
	m.ledger = fl
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrActivationFailed) {
		t.Fatalf("expected ErrActivationFailed on concurrent ledger change, got %v", err)
	}
	if exclusive, releases := env.gate.state(); exclusive || releases != 1 {
		t.Fatal("gate must reopen after aborted activation")
	}
}

func TestAcquisition_SingleAcquisitionAtATime(t *testing.T) {
	env := newAcquisitionEnv(t)
	m := env.manager(t)
	m.acquireMu.Lock()
	defer m.acquireMu.Unlock()
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrAcquisitionInProgress) {
		t.Fatalf("expected ErrAcquisitionInProgress, got %v", err)
	}
}

func TestAcquisition_LogsNeverContainSignedCDNTokens(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(prev)

	// Success path through the redirect, then a failure path whose error involves the redirect URL.
	if _, err := env.manager(t).InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	env2 := newAcquisitionEnv(t)
	env2.rel.publishSigned(env2.signer, "2026.10.27", env2.knownVersion("2026.10.27"))
	m2 := env2.manager(t)
	m2.release.allowedHosts = map[string]bool{"127.0.0.1": true}
	env2.rel.exeServe = func(w http.ResponseWriter) {
		w.Header().Set("Content-Length", "999999")
		_, _ = w.Write([]byte("short"))
	}
	_, err := m2.InstallYtdlp(context.Background())
	if err == nil {
		t.Fatal("expected truncated download failure")
	}
	for _, s := range []string{logs.String(), err.Error()} {
		if strings.Contains(s, "SUPERSECRETTOKEN") || strings.Contains(s, "X-Amz-Signature") {
			t.Fatalf("signed CDN token leaked: %s", s)
		}
	}
	if len(env.rel.authSeen) != 0 || len(env2.rel.authSeen) != 0 {
		t.Fatal("authorization header sent to release hosts")
	}
}

func TestAcquisition_PlatformAndStatusGuards(t *testing.T) {
	env := newAcquisitionEnv(t)
	m := env.manager(t)
	m.platformSupportedFn = func() bool { return false }
	if _, err := m.InstallYtdlp(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("expected ErrUnsupportedPlatform, got %v", err)
	}
	if _, err := m.CheckYtdlpUpdate(context.Background()); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("check: expected ErrUnsupportedPlatform, got %v", err)
	}
	st := m.YtdlpStatus(context.Background())
	if st.CanInstall || st.CanUpdate || st.PlatformSupported {
		t.Fatalf("unsupported platform must not offer actions: %+v", st)
	}
}

func TestAcquisition_CheckUpdateReportsAvailability(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.09.01", env.knownVersion("2026.09.01"))
	m := env.manager(t)
	chk, err := m.CheckYtdlpUpdate(context.Background())
	if err != nil || !chk.UpdateAvailable || chk.Managed {
		t.Fatalf("check before install: %+v %v", chk, err)
	}
	if _, err := m.InstallYtdlp(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	chk, err = env.manager(t).CheckYtdlpUpdate(context.Background())
	if err != nil || !chk.UpdateAvailable || !chk.Managed || chk.CurrentVersion != "2026.09.01" || chk.LatestVersion != "2026.10.26" {
		t.Fatalf("check after install: %+v %v", chk, err)
	}
	if env.rel.hit("exe") != 1 {
		t.Fatal("update check must not download the executable")
	}
}

func TestAcquisition_ConcurrentInstallsSingleWinner(t *testing.T) {
	env := newAcquisitionEnv(t)
	env.rel.publishSigned(env.signer, "2026.10.26", env.knownVersion("2026.10.26"))
	m := env.manager(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := m.InstallYtdlp(context.Background()); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("expected exactly one successful install, got %d", successes)
	}
	recs, _ := env.repo.ListManagedTools(context.Background(), ToolYtdlp)
	if len(recs) != 1 {
		t.Fatalf("expected one ledger row, got %d", len(recs))
	}
}
