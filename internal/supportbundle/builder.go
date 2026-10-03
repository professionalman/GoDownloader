package supportbundle

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"downloader/internal/buildinfo"
)

// Maximum allowed total uncompressed payload bytes across all entries (1 MiB).
const maxTotalUncompressedBytes = 1024 * 1024

// Hard-coded list of exactly nine fixed entry names.
const (
	EntryManifest        = "manifest.json"
	EntryRuntime         = "runtime.json"
	EntrySettings        = "settings.json"
	EntryTools           = "tools.json"
	EntryEngines         = "engines.json"
	EntryScheduler       = "scheduler.json"
	EntryRecovery        = "recovery.json"
	EntryDatabaseSummary = "database-summary.json"
	EntryReadme          = "README.txt"
)

// Builder coordinates diagnostic snapshot collection and staged archive creation.
type Builder struct {
	provider     SnapshotProvider
	infoProvider func() buildinfo.Info
	replaceFn    func(tempPath, destPath string) error
}

// SetReplaceFnForTesting allows unit tests to inject replacement failures without modifying filesystem semantics.
func (b *Builder) SetReplaceFnForTesting(fn func(tempPath, destPath string) error) {
	b.replaceFn = fn
}

// NewBuilder constructs a Builder with the specified snapshot provider.
func NewBuilder(provider SnapshotProvider, infoProvider ...func() buildinfo.Info) *Builder {
	ip := buildinfo.Current
	if len(infoProvider) > 0 && infoProvider[0] != nil {
		ip = infoProvider[0]
	}
	return &Builder{
		provider:     provider,
		infoProvider: ip,
	}
}

// Build compiles the diagnostic support bundle and atomically writes it to destinationPath.
func (b *Builder) Build(ctx context.Context, destinationPath string, host HostSnapshot) (SupportBundleExportResult, error) {
	if destinationPath == "" {
		return SupportBundleExportResult{}, errors.New("destination path cannot be empty")
	}

	if err := ctx.Err(); err != nil {
		return SupportBundleExportResult{}, err
	}

	core, err := b.provider.Snapshot(ctx)
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("failed to collect core snapshot: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return SupportBundleExportResult{}, err
	}

	bInfo := b.infoProvider()
	genTime := time.Now().UTC()

	// 1. Prepare exact payload bytes in memory
	manifestBytes, err := json.MarshalIndent(ManifestDTO{
		BundleSchemaVersion:  1,
		PrivacyPolicyVersion: 1,
		GeneratedAt:          genTime.Format(time.RFC3339),
		AppVersion:           bInfo.Version,
		Platform:             runtime.GOOS,
		Architecture:         runtime.GOARCH,
		Commit:               bInfo.Commit,
	}, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal manifest: %w", err)
	}

	runtimeBytes, err := json.MarshalIndent(RuntimeDTO{
		AppVersion:         bInfo.Version,
		Commit:             bInfo.Commit,
		GoVersion:          runtime.Version(),
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		DataRootKind:       host.DataRootKind,
		DataRootNormalized: host.DataRootNormalized,
		CloseToTray:        host.CloseToTray,
		AutostartEnabled:   host.AutostartEnabled,
	}, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal runtime: %w", err)
	}

	settingsBytes, err := json.MarshalIndent(core.Settings, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal settings: %w", err)
	}

	toolsBytes, err := json.MarshalIndent(SafeToolsDTO{Tools: core.Tools}, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal tools: %w", err)
	}

	enginesBytes, err := json.MarshalIndent(SafeEnginesDTO{Engines: core.Engines}, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal engines: %w", err)
	}

	schedulerBytes, err := json.MarshalIndent(core.Scheduler, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal scheduler: %w", err)
	}

	recoveryBytes, err := json.MarshalIndent(core.Recovery, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal recovery: %w", err)
	}

	dbSummaryBytes, err := json.MarshalIndent(core.DatabaseSummary, "", "  ")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("marshal database-summary: %w", err)
	}

	readmeText := fmt.Sprintf(`GoDownloader Diagnostic Support Bundle
======================================
Application Version: %s
Generated At:        %s
Platform:            %s/%s

This archive contains sanitized diagnostic snapshots to assist in troubleshooting:
- manifest.json: Bundle metadata and contract versions
- runtime.json: Runtime environment, desktop lifecycle, and data-root facts
- settings.json: Sanitized concurrency, storage, and network limits
- tools.json: External tool verification statuses and normalized versions
- engines.json: Registered download engine statuses
- scheduler.json: Resource governor limits and child process count aggregates
- recovery.json: Startup recovery summary counters and issue kind counts
- database-summary.json: Database migration markers and job aggregate counts

PRIVACY GUARANTEE:
This archive DOES NOT contain:
- Download URLs, torrent magnet URIs, or tracker endpoints
- Downloaded file names, content, or custom folder paths
- Passwords, credentials, auth tokens, or session cookies
- Raw SQLite database files, WAL/SHM files, or database backups
- Application logs or console transcripts
`, bInfo.Version, genTime.Format(time.RFC3339), runtime.GOOS, runtime.GOARCH)
	readmeBytes := []byte(readmeText)

	// 2. Compute total uncompressed bytes and enforce hard cap
	entries := []struct {
		name string
		data []byte
	}{
		{EntryManifest, manifestBytes},
		{EntryRuntime, runtimeBytes},
		{EntrySettings, settingsBytes},
		{EntryTools, toolsBytes},
		{EntryEngines, enginesBytes},
		{EntryScheduler, schedulerBytes},
		{EntryRecovery, recoveryBytes},
		{EntryDatabaseSummary, dbSummaryBytes},
		{EntryReadme, readmeBytes},
	}

	totalUncompressed := 0
	for _, entry := range entries {
		totalUncompressed += len(entry.data)
	}

	if totalUncompressed > maxTotalUncompressedBytes {
		return SupportBundleExportResult{}, fmt.Errorf("total uncompressed payload size (%d bytes) exceeds hard cap of %d bytes", totalUncompressed, maxTotalUncompressedBytes)
	}

	// 3. Atomically write to sibling temporary file in the destination directory
	destDir := filepath.Dir(destinationPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("ensure destination directory: %w", err)
	}

	tempFile, err := os.CreateTemp(destDir, ".godownloader-support-*.tmp")
	if err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("create temporary archive: %w", err)
	}
	tempPath := tempFile.Name()

	success := false
	defer func() {
		if !success {
			_ = os.Remove(tempPath)
		}
	}()

	zipWriter := zip.NewWriter(tempFile)
	for _, entry := range entries {
		w, err := zipWriter.Create(entry.name)
		if err != nil {
			_ = zipWriter.Close()
			_ = tempFile.Close()
			return SupportBundleExportResult{}, fmt.Errorf("create zip entry %s: %w", entry.name, err)
		}
		if _, err := w.Write(entry.data); err != nil {
			_ = zipWriter.Close()
			_ = tempFile.Close()
			return SupportBundleExportResult{}, fmt.Errorf("write zip entry %s: %w", entry.name, err)
		}
	}

	if err := zipWriter.Close(); err != nil {
		_ = tempFile.Close()
		return SupportBundleExportResult{}, fmt.Errorf("finalize zip writer: %w", err)
	}

	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return SupportBundleExportResult{}, fmt.Errorf("sync temp file: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return SupportBundleExportResult{}, fmt.Errorf("close temp file: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return SupportBundleExportResult{}, err
	}

	// 4. Staged same-filesystem replacement to final destination
	replace := b.replaceFn
	if replace == nil {
		replace = os.Rename
	}
	if err := replace(tempPath, destinationPath); err != nil {
		return SupportBundleExportResult{}, errors.New("finalize archive to destination: replacement failed")
	}

	success = true
	return SupportBundleExportResult{
		Status:   "saved",
		FileName: filepath.Base(destinationPath),
	}, nil
}
