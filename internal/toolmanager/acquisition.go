package toolmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"downloader/internal/job"
	"downloader/internal/runtimeguard"
)

// UsageCoordinator is the application-owned yt-dlp usage gate (Checkpoint 1 UsageGate via the ytdlp engine).
// Activation takes exclusive access so it can never overlap an analysis or download.
type UsageCoordinator interface {
	TryExclusive() (func(), error)
}

const (
	// managedProbeTimeout bounds the version probe of an already hash-verified managed binary.
	managedProbeTimeout = 20 * time.Second
	// candidateProbeTimeout bounds the first (self-extracting) run of a freshly authenticated candidate.
	candidateProbeTimeout = 60 * time.Second
)

// managedState holds managed yt-dlp acquisition and activation state embedded in Manager.
type managedState struct {
	ledger  job.IManagedToolLedger
	store   *managedStore
	release *releaseClient

	gateMu          sync.Mutex
	gate            UsageCoordinator
	restartRequired bool
	// maintenanceRelease is the held exclusive-gate release after a successful activation. It is never called:
	// new yt-dlp use stays blocked until the backend restarts and registers the newly selected binary.
	maintenanceRelease func()

	acquireMu  sync.Mutex
	acquiring  atomic.Bool
	diagMu     sync.Mutex
	diagnostic string

	platformSupportedFn func() bool
	nowFn               func() time.Time
	// trustRootFn returns the pinned release trust root; tests substitute a synthetic signer.
	trustRootFn func() (*trustRoot, error)
	// hashFileFn hashes managed binaries during verification; tests inject transient read failures.
	hashFileFn func(path string) (string, int64, error)
}

func (m *Manager) initManagedState(dataDir string, execRepo job.IExecutionRepository) {
	if ledger, ok := execRepo.(job.IManagedToolLedger); ok && ledger != nil {
		m.ledger = ledger
	}
	m.store = newManagedStore(dataDir)
	m.release = newReleaseClient(nil)
	m.platformSupportedFn = func() bool { return runtime.GOOS == "windows" && runtime.GOARCH == "amd64" }
	m.nowFn = time.Now
	m.trustRootFn = ytdlpTrustRoot
	m.hashFileFn = hashRegularFile
}

// SetUsageCoordinator wires the shared yt-dlp usage gate used to make activation exclusive.
func (m *Manager) SetUsageCoordinator(u UsageCoordinator) {
	m.gateMu.Lock()
	defer m.gateMu.Unlock()
	m.gate = u
}

// RestartRequired reports whether a managed activation committed in this process awaits a backend restart.
func (m *Manager) RestartRequired() bool {
	m.gateMu.Lock()
	defer m.gateMu.Unlock()
	return m.restartRequired
}

func (m *Manager) setDiagnostic(s string) {
	m.diagMu.Lock()
	m.diagnostic = s
	m.diagMu.Unlock()
}

func (m *Manager) managedDiagnostic() string {
	m.diagMu.Lock()
	defer m.diagMu.Unlock()
	return m.diagnostic
}

// managedVerifyResult distinguishes integrity failures (content cannot be trusted; LKG may be restored) from
// execution failures of verified content (possibly transient; the ledger is never changed for those).
type managedVerifyResult struct {
	info         *ToolInfo
	integrityErr error
	probeErr     error
}

// verifyManagedRecord re-establishes authenticity of a ledger record before use: the stored file name must be
// the content address inside managed storage, the file's SHA-256 must equal the authenticated digest, and only
// then is the binary executed for a bounded version probe, whose output must equal the recorded version.
func (m *Manager) verifyManagedRecord(ctx context.Context, rec *job.ManagedToolRecord) managedVerifyResult {
	root, err := m.trustRootFn()
	if err != nil {
		return managedVerifyResult{integrityErr: err}
	}
	if rec.SignerFingerprint != root.fingerprint {
		return managedVerifyResult{integrityErr: fmt.Errorf("record was authenticated by an untrusted key")}
	}
	path, err := m.store.resolveRecordPath(rec.FileName, rec.SHA256)
	if err != nil {
		return managedVerifyResult{integrityErr: err}
	}
	got, _, err := m.hashFileFn(path)
	if err != nil {
		// Only absence or a non-regular file proves the content is gone. Other read errors (for example an
		// antivirus sharing violation or access denied) are transient and must not quarantine a good binary.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errNotRegularFile) {
			return managedVerifyResult{integrityErr: fmt.Errorf("managed binary unavailable: %w", err)}
		}
		return managedVerifyResult{probeErr: fmt.Errorf("managed binary could not be read: %w", err)}
	}
	if got != rec.SHA256 {
		return managedVerifyResult{integrityErr: fmt.Errorf("managed binary hash mismatch")}
	}
	ver, err := m.probeVersionTimeout(ctx, managedProbeTimeout, path, "--version")
	if err != nil {
		return managedVerifyResult{probeErr: err}
	}
	if cmp, cerr := compareYtdlpVersions(ver, rec.Version); cerr != nil || cmp != 0 {
		return managedVerifyResult{integrityErr: fmt.Errorf("managed binary reports an unexpected version")}
	}
	return managedVerifyResult{info: &ToolInfo{
		Name:               ToolYtdlp,
		Version:            rec.Version,
		PlatformArch:       fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		OwnershipMode:      OwnershipManaged,
		ExecutablePath:     path,
		SHA256:             rec.SHA256,
		VerificationStatus: VerificationStatusAuthenticated,
		Status:             StatusActive,
		Provenance:         ProvenanceManagedRelease,
		ProbedAt:           time.Now().UTC(),
	}}
}

// resolveManagedYtdlp returns the authenticated managed yt-dlp, restoring the last-known-good record when the
// active record fails integrity verification. It returns nil when no usable authenticated binary exists, in
// which case resolution falls back to the legacy, configured, and PATH sources without any trust upgrade.
func (m *Manager) resolveManagedYtdlp(ctx context.Context) *ToolInfo {
	if m.ledger == nil {
		return nil
	}
	active, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusActive)
	if err != nil {
		log.Printf("toolmanager: read managed yt-dlp ledger failed: %v", err)
		m.setDiagnostic("The managed yt-dlp record could not be read.")
		return nil
	}
	lkg, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusLastKnownGood)
	if err != nil {
		log.Printf("toolmanager: read managed yt-dlp LKG failed: %v", err)
		lkg = nil
	}
	if active == nil {
		return nil
	}

	res := m.verifyManagedRecord(ctx, active)
	if res.info != nil {
		m.setDiagnostic("")
		return res.info
	}
	if res.probeErr != nil {
		log.Printf("toolmanager: authenticated managed yt-dlp %s failed to start: %v", active.Version, res.probeErr)
		m.setDiagnostic("The authenticated managed yt-dlp could not be started; using another available yt-dlp if present.")
		return nil
	}

	log.Printf("toolmanager: active managed yt-dlp %s failed integrity verification: %v", active.Version, res.integrityErr)
	if lkg != nil {
		lkgRes := m.verifyManagedRecord(ctx, lkg)
		if lkgRes.info != nil {
			if err := m.ledger.RestoreLastKnownGood(ctx, ToolYtdlp, active.SHA256, lkg.SHA256); err != nil {
				log.Printf("toolmanager: restore managed yt-dlp last-known-good failed: %v", err)
				m.setDiagnostic("The managed yt-dlp failed verification and its previous version could not be restored.")
				return nil
			}
			msg := fmt.Sprintf("The managed yt-dlp %s failed verification; restored the previous authenticated version %s.", active.Version, lkg.Version)
			log.Printf("toolmanager: %s", msg)
			m.setDiagnostic(msg)
			lkgRes.info.Diagnostic = msg
			return lkgRes.info
		}
		log.Printf("toolmanager: managed yt-dlp last-known-good %s also failed verification", lkg.Version)
	}
	m.setDiagnostic("The managed yt-dlp failed verification and no valid previous version is available. Reinstall it from Settings.")
	return nil
}

// AcquisitionResult is the public, path-free outcome of an install or update.
type AcquisitionResult struct {
	Tool            string `json:"tool"`
	Version         string `json:"version"`
	Ownership       string `json:"ownership"`
	Status          string `json:"status"`
	AlreadyCurrent  bool   `json:"alreadyCurrent"`
	RestartRequired bool   `json:"restartRequired"`
}

// UpdateCheckResult reports stable update availability without changing anything.
type UpdateCheckResult struct {
	Tool            string `json:"tool"`
	CurrentVersion  string `json:"currentVersion,omitempty"`
	LatestVersion   string `json:"latestVersion"`
	Managed         bool   `json:"managed"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// InstallYtdlp installs the latest authenticated stable yt-dlp as the managed binary.
func (m *Manager) InstallYtdlp(ctx context.Context) (*AcquisitionResult, error) {
	return m.acquireYtdlp(ctx)
}

// UpdateYtdlp updates the managed yt-dlp to the latest authenticated stable release; older releases are refused.
func (m *Manager) UpdateYtdlp(ctx context.Context) (*AcquisitionResult, error) {
	return m.acquireYtdlp(ctx)
}

// CheckYtdlpUpdate reports whether a newer stable release than the managed yt-dlp exists. It does not
// download executables or change state.
func (m *Manager) CheckYtdlpUpdate(ctx context.Context) (*UpdateCheckResult, error) {
	if !m.platformSupportedFn() {
		return nil, ErrUnsupportedPlatform
	}
	rel, err := m.release.latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	out := &UpdateCheckResult{Tool: ToolYtdlp, LatestVersion: rel.Tag}
	if m.ledger == nil {
		out.UpdateAvailable = true
		return out, nil
	}
	active, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusActive)
	if err != nil {
		return nil, fmt.Errorf("%w: read managed ledger", ErrActivationFailed)
	}
	if active == nil {
		out.UpdateAvailable = true
		return out, nil
	}
	out.Managed = true
	out.CurrentVersion = active.Version
	cmp, err := compareYtdlpVersions(rel.Tag, active.Version)
	if err != nil {
		return nil, err
	}
	out.UpdateAvailable = cmp > 0
	return out, nil
}

// acquireYtdlp runs the authenticated acquisition chain:
//
//	pinned key -> SHA2-256SUMS + .sig -> detached signature verified -> exact yt-dlp.exe digest ->
//	stream executable to staging -> SHA-256 verified -> only then bounded --version probe (must equal tag) ->
//	immutable content-addressed promotion -> exclusive usage gate -> transactional ledger activation.
//
// Nothing is executed before its digest matches the signature-authenticated manifest.
func (m *Manager) acquireYtdlp(ctx context.Context) (*AcquisitionResult, error) {
	if !m.platformSupportedFn() {
		return nil, ErrUnsupportedPlatform
	}
	if m.RestartRequired() {
		return nil, ErrRestartRequired
	}
	if !m.acquireMu.TryLock() {
		return nil, ErrAcquisitionInProgress
	}
	defer m.acquireMu.Unlock()
	m.acquiring.Store(true)
	defer m.acquiring.Store(false)

	if m.ledger == nil {
		return nil, fmt.Errorf("%w: managed tool ledger is unavailable", ErrActivationFailed)
	}
	// Cross-process ownership: held from before any staging file exists until cleanup completes, so no other
	// process's cleanup can treat this acquisition's files as abandoned.
	owner, err := m.store.acquireOwnership()
	if err != nil {
		if errors.Is(err, runtimeguard.ErrRuntimeAlreadyOwned) {
			return nil, ErrAcquisitionInProgress
		}
		return nil, fmt.Errorf("%w: %v", ErrActivationFailed, err)
	}
	defer owner.Close()

	root, err := m.trustRootFn()
	if err != nil {
		return nil, err
	}

	current, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusActive)
	if err != nil {
		return nil, fmt.Errorf("%w: read managed ledger", ErrActivationFailed)
	}
	// Judge the installed binary by integrity, not by a probe that may fail transiently: an intact file of the
	// release's version is already current, and re-downloading it would only block media work until restart.
	currentIntact := false
	if current != nil {
		res := m.verifyManagedRecord(ctx, current)
		currentIntact = res.integrityErr == nil
	}

	rel, err := m.release.latestRelease(ctx)
	if err != nil {
		return nil, err
	}
	if current != nil {
		cmp, err := compareYtdlpVersions(rel.Tag, current.Version)
		if err != nil {
			return nil, err
		}
		if cmp < 0 {
			return nil, ErrDowngradeRefused
		}
		if cmp == 0 && currentIntact {
			return &AcquisitionResult{Tool: ToolYtdlp, Version: current.Version, Ownership: OwnershipManaged, Status: StatusActive, AlreadyCurrent: true}, nil
		}
	}

	manifest, err := m.release.fetchSmallAsset(ctx, rel.Manifest, maxManifestBytes)
	if err != nil {
		return nil, err
	}
	signature, err := m.release.fetchSmallAsset(ctx, rel.Signature, maxSignatureBytes)
	if err != nil {
		return nil, err
	}
	if err := root.verifyDetachedSignature(manifest, signature); err != nil {
		return nil, err
	}
	digest, err := parseSHA256Manifest(manifest, ytdlpWindowsAsset)
	if err != nil {
		return nil, err
	}

	finalPath, reused, err := m.stageAndVerify(ctx, rel, digest)
	if err != nil {
		return nil, err
	}
	if current != nil && current.SHA256 == digest && reused {
		// The selected binary is intact after all (e.g. an earlier read error was transient): nothing changed,
		// so the usage gate is not taken and no restart is needed.
		return &AcquisitionResult{Tool: ToolYtdlp, Version: current.Version, Ownership: OwnershipManaged, Status: StatusActive, AlreadyCurrent: true}, nil
	}

	newRec := &job.ManagedToolRecord{
		Name:              ToolYtdlp,
		SHA256:            digest,
		Version:           rel.Tag,
		ReleaseTag:        rel.Tag,
		FileName:          digest + ".exe",
		SignerFingerprint: root.fingerprint,
		AuthenticatedAt:   m.nowFn().UTC(),
	}
	if err := m.activate(ctx, newRec, current, finalPath); err != nil {
		return nil, err
	}
	m.collectManagedGarbage(ctx, true)
	return &AcquisitionResult{Tool: ToolYtdlp, Version: rel.Tag, Ownership: OwnershipManaged, Status: "restart_required", RestartRequired: true}, nil
}

// stageAndVerify streams the executable into a staging file inside managed storage, flushes it, verifies its
// SHA-256 against the authenticated digest (while streaming and again from disk), and only then runs the
// bounded version probe. The verified file is promoted to its immutable content-addressed name.
func (m *Manager) stageAndVerify(ctx context.Context, rel *ytdlpRelease, digest string) (path string, reused bool, err error) {
	f, err := m.store.createStaging()
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrActivationFailed, err)
	}
	stagingPath := f.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(stagingPath)
		}
	}()

	h := sha256.New()
	_, copyErr := m.release.streamAsset(ctx, rel.Exe, io.MultiWriter(f, h), maxExecutableBytes)
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return "", false, copyErr
	}
	if syncErr != nil || closeErr != nil {
		return "", false, fmt.Errorf("%w: flush staged executable", ErrActivationFailed)
	}
	if hex.EncodeToString(h.Sum(nil)) != digest {
		return "", false, ErrHashMismatch
	}
	onDisk, _, err := hashRegularFile(stagingPath)
	if err != nil || onDisk != digest {
		return "", false, ErrHashMismatch
	}

	// Authenticated content only beyond this point.
	ver, err := m.probeVersionTimeout(ctx, candidateProbeTimeout, stagingPath, "--version")
	if err != nil {
		return "", false, fmt.Errorf("%w: authenticated candidate failed to start: %v", ErrInvalidRelease, err)
	}
	if cmp, cerr := compareYtdlpVersions(ver, rel.Tag); cerr != nil || cmp != 0 {
		return "", false, fmt.Errorf("%w: candidate version does not match release %s", ErrInvalidRelease, rel.Tag)
	}

	finalPath, reused, err := m.store.promote(stagingPath, digest)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrActivationFailed, err)
	}
	keep = true
	if got, _, err := hashRegularFile(finalPath); err != nil || got != digest {
		return "", false, ErrHashMismatch
	}
	return finalPath, reused, nil
}

// activate takes the shared usage gate exclusively, revalidates that the ledger still matches the snapshot the
// update was decided against, and commits the activation transactionally. On any failure the gate reopens.
// On success the gate stays closed until restart so the running process never mixes binaries.
func (m *Manager) activate(ctx context.Context, newRec, previous *job.ManagedToolRecord, finalPath string) error {
	m.gateMu.Lock()
	gate := m.gate
	m.gateMu.Unlock()

	release := func() {}
	if gate != nil {
		r, err := gate.TryExclusive()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrToolInUse, err)
		}
		release = r
	}

	committed := false
	defer func() {
		if !committed {
			release()
		}
	}()

	latest, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusActive)
	if err != nil {
		return fmt.Errorf("%w: revalidate managed ledger", ErrActivationFailed)
	}
	if (latest == nil) != (previous == nil) || (latest != nil && latest.SHA256 != previous.SHA256) {
		return fmt.Errorf("%w: managed ledger changed during update", ErrActivationFailed)
	}
	if got, _, err := hashRegularFile(finalPath); err != nil || got != newRec.SHA256 {
		return ErrHashMismatch
	}

	if previous == nil || previous.SHA256 != newRec.SHA256 {
		if err := m.ledger.CommitToolActivation(ctx, newRec, previous); err != nil {
			return fmt.Errorf("%w: %v", ErrActivationFailed, err)
		}
	}

	committed = true
	m.gateMu.Lock()
	m.restartRequired = true
	m.maintenanceRelease = release
	m.gateMu.Unlock()
	m.setDiagnostic(fmt.Sprintf("yt-dlp %s is installed and authenticated. Quit GoDownloader completely and start it again to use it (closing the window to the tray does not restart it).", newRec.Version))
	return nil
}

// CleanupManagedStore removes managed-store files whose obsolescence is provable. Staging and quarantine files
// are only removed while holding the cross-process acquisition ownership lock; if another process is acquiring,
// they are kept. If the ledger cannot be read, nothing is deleted.
func (m *Manager) CleanupManagedStore(ctx context.Context) {
	if !m.acquireMu.TryLock() {
		return
	}
	defer m.acquireMu.Unlock()
	// Nothing to clean, and nothing should be created, when no managed store exists yet.
	if _, err := os.Lstat(m.store.root); os.IsNotExist(err) {
		return
	}
	scratchOwned := false
	if owner, err := m.store.acquireOwnership(); err == nil {
		defer owner.Close()
		scratchOwned = true
	} else {
		log.Printf("toolmanager: managed store scratch cleanup deferred: %v", err)
	}
	m.collectManagedGarbage(ctx, scratchOwned)
}

// collectManagedGarbage removes obsolete managed files. scratchOwned must be true only while the caller holds
// the acquisition ownership lock.
func (m *Manager) collectManagedGarbage(ctx context.Context, scratchOwned bool) {
	if m.ledger == nil {
		return
	}
	// Only binaries this ledger itself selected and later retired or quarantined are provably obsolete. Files
	// unknown to this ledger may belong to another database sharing the data directory, so they are kept.
	records, err := m.ledger.ListManagedTools(ctx, ToolYtdlp)
	if err != nil {
		log.Printf("toolmanager: managed store cleanup deferred: ledger unreadable: %v", err)
		return
	}
	keep := map[string]bool{}
	retired := map[string]bool{}
	for _, rec := range records {
		switch rec.Status {
		case job.ManagedToolStatusActive, job.ManagedToolStatusLastKnownGood:
			keep[rec.FileName] = true
		case job.ManagedToolStatusRetired, job.ManagedToolStatusQuarantined:
			retired[rec.FileName] = true
		}
	}
	removed, deferred := m.store.collectGarbage(retired, keep, m.nowFn(), scratchOwned)
	if len(removed) > 0 {
		log.Printf("toolmanager: removed %d obsolete managed yt-dlp file(s)", len(removed))
	}
	if len(deferred) > 0 {
		log.Printf("toolmanager: deferred removal of %d managed yt-dlp file(s) still in use", len(deferred))
	}
}

// ManagedToolStatus is the public, path-free yt-dlp status used by the Settings UI.
type ManagedToolStatus struct {
	Tool               string `json:"tool"`
	Available          bool   `json:"available"`
	Version            string `json:"version,omitempty"`
	Ownership          string `json:"ownership"`
	VerificationStatus string `json:"verificationStatus,omitempty"`
	Authenticated      bool   `json:"authenticated"`
	ManagedVersion     string `json:"managedVersion,omitempty"`
	RestartRequired    bool   `json:"restartRequired"`
	Busy               bool   `json:"busy"`
	PlatformSupported  bool   `json:"platformSupported"`
	CanInstall         bool   `json:"canInstall"`
	CanUpdate          bool   `json:"canUpdate"`
	Diagnostic         string `json:"diagnostic,omitempty"`
}

// Public ownership labels for ManagedToolStatus.
const (
	StatusOwnershipManaged = "managed"
	StatusOwnershipLegacy  = "legacy_managed_directory"
	StatusOwnershipUser    = "user_configured"
	StatusOwnershipSystem  = "system_path"
	StatusOwnershipNone    = "none"
)

// YtdlpStatus reports the yt-dlp the running backend resolved plus managed ledger and acquisition state.
func (m *Manager) YtdlpStatus(ctx context.Context) ManagedToolStatus {
	st := ManagedToolStatus{
		Tool:              ToolYtdlp,
		Ownership:         StatusOwnershipNone,
		RestartRequired:   m.RestartRequired(),
		Busy:              m.acquiring.Load(),
		PlatformSupported: m.platformSupportedFn(),
		Diagnostic:        m.managedDiagnostic(),
	}
	if info := m.Get(ToolYtdlp); info != nil && info.Available() {
		st.Available = true
		st.Version = info.Version
		st.VerificationStatus = info.VerificationStatus
		st.Authenticated = info.VerificationStatus == VerificationStatusAuthenticated
		switch info.Provenance {
		case ProvenanceManagedRelease:
			st.Ownership = StatusOwnershipManaged
		case ProvenanceManagedLegacy, ProvenanceManaged:
			st.Ownership = StatusOwnershipLegacy
		case ProvenanceUserConfigured:
			st.Ownership = StatusOwnershipUser
		case ProvenanceSystemPath:
			st.Ownership = StatusOwnershipSystem
		}
	}
	hasManaged := false
	if m.ledger != nil {
		if active, err := m.ledger.GetManagedTool(ctx, ToolYtdlp, job.ManagedToolStatusActive); err == nil && active != nil {
			hasManaged = true
			st.ManagedVersion = active.Version
		}
	}
	idle := st.PlatformSupported && !st.RestartRequired && !st.Busy && m.ledger != nil
	st.CanInstall = idle && !hasManaged
	st.CanUpdate = idle && hasManaged
	if st.Diagnostic == "" && !st.Available {
		st.Diagnostic = "yt-dlp is not installed. Install the managed yt-dlp, or configure your own yt-dlp, to enable media downloads."
	}
	return st
}

// AcquisitionErrorCode maps acquisition errors to stable public codes for transports.
func AcquisitionErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrUnsupportedPlatform):
		return "UNSUPPORTED_PLATFORM"
	case errors.Is(err, ErrRestartRequired):
		return "RESTART_REQUIRED"
	case errors.Is(err, ErrAcquisitionInProgress):
		return "ACQUISITION_IN_PROGRESS"
	case errors.Is(err, ErrToolInUse):
		return "TOOL_IN_USE"
	case errors.Is(err, ErrUntrustedSigningKey):
		return "UNTRUSTED_SIGNING_KEY"
	case errors.Is(err, ErrInvalidSignature):
		return "INVALID_SIGNATURE"
	case errors.Is(err, ErrHashMismatch):
		return "HASH_MISMATCH"
	case errors.Is(err, ErrDowngradeRefused):
		return "DOWNGRADE_REFUSED"
	case errors.Is(err, ErrInvalidRelease):
		return "INVALID_RELEASE"
	case errors.Is(err, ErrAcquisitionNetwork):
		return "NETWORK_FAILURE"
	case errors.Is(err, ErrActivationFailed):
		return "ACTIVATION_FAILED"
	default:
		return "ACTIVATION_FAILED"
	}
}

// AcquisitionErrorMessage returns a safe, path- and URL-free message for a public error code.
func AcquisitionErrorMessage(code string) string {
	switch code {
	case "UNSUPPORTED_PLATFORM":
		return "Managed yt-dlp installation is only available on Windows (64-bit)."
	case "RESTART_REQUIRED":
		return "yt-dlp was updated. Quit GoDownloader completely and start it again to use it (closing the window to the tray does not restart it)."
	case "ACQUISITION_IN_PROGRESS":
		return "A yt-dlp installation or update is already running."
	case "TOOL_IN_USE":
		return "yt-dlp is in use by an active analysis or download. Try again when media jobs are idle."
	case "UNTRUSTED_SIGNING_KEY":
		return "The release was not signed by the trusted yt-dlp signing key. Nothing was installed."
	case "INVALID_SIGNATURE":
		return "The release signature could not be verified. Nothing was installed."
	case "HASH_MISMATCH":
		return "The downloaded yt-dlp did not match its signed checksum. Nothing was installed."
	case "DOWNGRADE_REFUSED":
		return "The available release is older than the installed managed yt-dlp."
	case "INVALID_RELEASE":
		return "The yt-dlp release information was invalid. Nothing was installed."
	case "NETWORK_FAILURE":
		return "Could not download yt-dlp from GitHub. Check your connection and try again."
	default:
		return "yt-dlp could not be installed. The previous version, if any, is still selected."
	}
}
