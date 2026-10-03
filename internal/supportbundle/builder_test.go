package supportbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"downloader/internal/buildinfo"
)

type mockSnapshotProvider struct {
	snapshot CoreSnapshot
	err      error
}

func (m *mockSnapshotProvider) Snapshot(ctx context.Context) (CoreSnapshot, error) {
	if m.err != nil {
		return CoreSnapshot{}, m.err
	}
	return m.snapshot, nil
}

func sampleCoreSnapshot() CoreSnapshot {
	return CoreSnapshot{
		Settings: SafeSettingsDTO{
			Queue: SafeQueueSettings{
				MaxConcurrentDownloads:          3,
				EffectiveMaxConcurrentDownloads: 3,
				EnvironmentOverride:             false,
			},
			Storage: SafeStorageSettings{
				MinimumFreeSpaceBytes: 1073741824,
				DefaultConflictPolicy: "rename",
				OverridesPresent:      false,
			},
			Network: SafeNetworkSettings{
				GlobalDownloadLimitBytesPerSecond: 0,
				Proxy: SafeProxySettings{
					Mode:                  "disabled",
					Protocol:              "http",
					Port:                  0,
					CredentialsConfigured: false,
				},
				HasCustomUserAgent:      false,
				CustomHeaderCount:       0,
				RetryMaxAttempts:        3,
				RetryWaitSeconds:        5,
				ConnectTimeoutSeconds:   30,
				RequestTimeoutSeconds:   60,
				MaxConnectionsPerServer: 8,
				Split:                   4,
			},
			Torrent: SafeTorrentSettings{
				DownloadLimitBytesPerSecond:            0,
				UploadLimitBytesPerSecond:              0,
				SeedingMode:                            "ratio",
				ApplyTrackerSubscriptionsToNewTorrents: true,
				ManageQBitGlobalNetworkSettings:        false,
			},
		},
		Tools: []SafeToolDTO{
			{
				Name:               "yt-dlp",
				Version:            "2024.08.06",
				PlatformArch:       "windows_amd64",
				OwnershipMode:      "managed",
				VerificationStatus: "version_probed",
				Status:             "active",
				Provenance:         "managed",
				ProbedAt:           time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
			},
			{
				Name:               "ffmpeg",
				Version:            "6.1.1",
				PlatformArch:       "windows_amd64",
				OwnershipMode:      "external_user",
				VerificationStatus: "version_probed",
				Status:             "active",
				Provenance:         "system_path",
				ProbedAt:           time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
			},
		},
		Engines: []SafeEngineDTO{
			{
				Name:             "aria2",
				Registered:       true,
				OwnershipMode:    "compatibility",
				CachedToolStatus: "active",
			},
			{
				Name:             "qbittorrent",
				Registered:       true,
				OwnershipMode:    "external_user",
				CachedToolStatus: "active",
			},
			{
				Name:             "ytdlp",
				Registered:       true,
				OwnershipMode:    "managed",
				CachedToolStatus: "active",
			},
		},
		Scheduler: SafeSchedulerDTO{
			Governor: SafeGovernorDTO{
				GlobalTransferLimit: 3,
				ActiveTransfers:     1,
				ActiveLeases:        1,
				EngineLimits: map[string]int{
					"aria2":       3,
					"qbittorrent": 3,
					"ytdlp":       2,
				},
				ActivePerEngine: map[string]int{
					"aria2": 1,
					"ytdlp": 0,
				},
			},
			Queue: SafeQueueDTO{
				QueuedJobsCount: 2,
			},
			Processes: SafeProcessDTO{
				ActiveSupervisedCount: 1,
				CountsByTool: map[string]int{
					"yt-dlp": 1,
				},
				CountsByPurpose: map[string]int{
					"download": 1,
				},
				CountsByState: map[string]int{
					"running": 1,
				},
			},
		},
		Recovery: SafeRecoveryDTO{
			ReconciledFinalizations:       1,
			ReattachedTransfers:           1,
			RestartedMetadataAcquisitions: 0,
			InterruptedMediaJobs:          0,
			IssueCountsByKind: map[string]int{
				"interrupted_media": 0,
			},
		},
		DatabaseSummary: SafeDatabaseSummaryDTO{
			MigrationMarkers: map[string]bool{
				"v07NetworkControls": true,
				"v08Fnd1":            true,
			},
			TablesPresent: map[string]bool{
				"jobs":                true,
				"appSettings":         true,
				"jobQueue":            true,
				"jobExecutions":       true,
				"finalizationRecords": true,
			},
			PendingFinalizationsCount: 0,
			StateSyncCursor:           42,
			JobCountsByStatus: map[string]int{
				"downloading": 1,
				"queued":      2,
				"completed":   5,
			},
			JobCountsByEngine: map[string]int{
				"aria2":       3,
				"qbittorrent": 2,
				"ytdlp":       3,
			},
		},
	}
}

func sampleHostSnapshot() HostSnapshot {
	return HostSnapshot{
		CloseToTray:        true,
		AutostartEnabled:   false,
		DataRootKind:       "appdata",
		DataRootNormalized: "%APPDATA%\\GoDownloader",
	}
}

func fakeBuildInfo() buildinfo.Info {
	return buildinfo.Info{
		Version: "(devel)",
		Commit:  "6ab03f2055ff7d18d14178d4b37abe0542a75500",
		Dirty:   false,
	}
}

func TestSupportBundle_Success(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
	builder := NewBuilder(provider, fakeBuildInfo)

	res, err := builder.Build(context.Background(), destPath, sampleHostSnapshot())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if res.Status != "saved" {
		t.Errorf("status = %q; want 'saved'", res.Status)
	}
	if res.FileName != "support.zip" {
		t.Errorf("fileName = %q; want 'support.zip'", res.FileName)
	}

	// Verify file was written
	fi, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("failed to stat destination archive: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatalf("archive file is empty")
	}

	// Read and verify exact 9 entries
	r, err := zip.OpenReader(destPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	expectedEntries := map[string]bool{
		EntryManifest:        true,
		EntryRuntime:         true,
		EntrySettings:        true,
		EntryTools:           true,
		EntryEngines:         true,
		EntryScheduler:       true,
		EntryRecovery:        true,
		EntryDatabaseSummary: true,
		EntryReadme:          true,
	}

	if len(r.File) != len(expectedEntries) {
		t.Errorf("got %d files in archive; want %d", len(r.File), len(expectedEntries))
	}

	for _, f := range r.File {
		if !expectedEntries[f.Name] {
			t.Errorf("unexpected entry in archive: %q", f.Name)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open entry %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if len(data) == 0 {
			t.Errorf("entry %s is empty", f.Name)
		}
	}
}

func TestNormalizeToolVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "unknown"},
		{"6.1.1", "6.1.1"},
		{"v5.0.4", "5.0.4"},
		{"V2024.08.06", "2024.08.06"},
		{"2024.08.06", "2024.08.06"},
		{"1.37.0-beta", "1.37.0-beta"},
		{"ffmpeg version 6.1.1-system Copyright", "6.1.1-system"},
		{"ffmpeg version n7.0-3-g12345", "n7.0-3-g12345"},
		// Path injection attempts
		{"C:\\Users\\victim\\Downloads\\malicious", "unknown"},
		{"/usr/bin/evil", "unknown"},
		// URL injection attempts
		{"https://evil.com/token?secret=123", "unknown"},
		{"http://localhost:8080", "unknown"},
		// Script / command injection attempts
		{"1.0.0; rm -rf /", "unknown"},
		{"1.0.0 | cat /etc/passwd", "unknown"},
		// Query strings / secrets
		{"token=GD_SECRET_BEARER_7F92A1B", "unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := NormalizeToolVersion(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeToolVersion(%q) = %q; want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestSupportBundle_StructuralAllowlist(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
	builder := NewBuilder(provider, fakeBuildInfo)

	if _, err := builder.Build(context.Background(), destPath, sampleHostSnapshot()); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	r, err := zip.OpenReader(destPath)
	if err != nil {
		t.Fatalf("failed to open zip: %v", err)
	}
	defer r.Close()

	forbiddenKeys := []string{
		"password", "proxypassword", "username", "host", "url",
		"source", "destinationdir", "jobid", "args", "env",
		"diagnostic", "executablepath", "headers", "cookies", "authorization",
	}

	for _, f := range r.File {
		if f.Name == EntryReadme {
			continue // README.txt is human-readable markdown/text
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("failed to open %s: %v", f.Name, err)
		}
		var val any
		err = json.NewDecoder(rc).Decode(&val)
		rc.Close()
		if err != nil {
			t.Fatalf("failed to parse JSON in %s: %v", f.Name, err)
		}

		// Recursively check all JSON object keys
		var assertNoForbiddenKeys func(node any, path string)
		assertNoForbiddenKeys = func(node any, path string) {
			switch v := node.(type) {
			case map[string]any:
				for k, child := range v {
					lowerKey := strings.ToLower(k)
					for _, forbidden := range forbiddenKeys {
						if lowerKey == forbidden {
							t.Errorf("forbidden key %q found at %s in %s", k, path, f.Name)
						}
					}
					assertNoForbiddenKeys(child, path+"."+k)
				}
			case []any:
				for i, child := range v {
					assertNoForbiddenKeys(child, fmt.Sprintf("%s[%d]", path, i))
				}
			}
		}
		assertNoForbiddenKeys(val, "$")
	}
}

func TestSupportBundle_ExactSentinels(t *testing.T) {
	sentinels := []string{
		"GD_SECRET_BEARER_7F92A1B",
		"GD_SECRET_COOKIE_SESSION_992",
		"GD_SECRET_PROXY_PW_8819",
		"GD_PRIVATE_URL_771",
		"GD_PRIVATE_USER_NAME",
		"GD_PRIVATE_TRACKER_551",
		"GD_PRIVATE_MAGNET_1122",
		"GD_PRIVATE_FILE_884",
	}

	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	// Inject sentinels into snapshot fields where possible
	core := sampleCoreSnapshot()
	// Malicious/accidental attempts in raw models
	core.Settings.Storage.DefaultConflictPolicy = SanitizeConflictPolicy("GD_SECRET_BEARER_7F92A1B")
	core.Settings.Network.Proxy.Mode = SanitizeProxyMode("GD_SECRET_PROXY_PW_8819")
	core.Tools[0].Version = NormalizeToolVersion("https://private.host/?token=GD_SECRET_BEARER_7F92A1B&url=GD_PRIVATE_URL_771")
	core.Tools[1].Version = NormalizeToolVersion("C:\\Users\\GD_PRIVATE_USER_NAME\\Downloads\\GD_PRIVATE_FILE_884")
	core.Engines[0].Name = SanitizeEngineName("GD_PRIVATE_TRACKER_551")
	core.Scheduler.Processes.CountsByTool = map[string]int{
		SanitizeToolName("GD_PRIVATE_USER_NAME"): 1,
	}

	provider := &mockSnapshotProvider{snapshot: core}
	builder := NewBuilder(provider, fakeBuildInfo)

	if _, err := builder.Build(context.Background(), destPath, sampleHostSnapshot()); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	r, err := zip.OpenReader(destPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()

	// Scan every single byte of every file in the ZIP archive
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", f.Name, err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()

		for _, sentinel := range sentinels {
			if bytes.Contains(data, []byte(sentinel)) {
				t.Errorf("archive entry %s contains sentinel %q!", f.Name, sentinel)
			}
		}
	}
}

func TestSupportBundle_DatabaseExclusion(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
	builder := NewBuilder(provider, fakeBuildInfo)

	if _, err := builder.Build(context.Background(), destPath, sampleHostSnapshot()); err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	r, err := zip.OpenReader(destPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer r.Close()

	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, ".db") ||
			strings.HasSuffix(name, ".sqlite") ||
			strings.HasSuffix(name, "-wal") ||
			strings.HasSuffix(name, "-shm") ||
			strings.HasSuffix(name, ".bak") {
			t.Errorf("forbidden database entry found in archive: %q", f.Name)
		}
	}
}

func TestSupportBundle_BuilderFailures(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		destPath := filepath.Join(tempDir, "cancel.zip")
		provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
		builder := NewBuilder(provider, fakeBuildInfo)

		_, err := builder.Build(ctx, destPath, sampleHostSnapshot())
		if err == nil {
			t.Errorf("expected error on cancelled context")
		}

		// Verify no residual temp files
		files, _ := os.ReadDir(tempDir)
		for _, f := range files {
			if strings.HasPrefix(f.Name(), ".godownloader-support-") {
				t.Errorf("residual temporary file found: %s", f.Name())
			}
		}
	})

	t.Run("empty destination path", func(t *testing.T) {
		provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
		builder := NewBuilder(provider, fakeBuildInfo)

		_, err := builder.Build(context.Background(), "", sampleHostSnapshot())
		if err == nil {
			t.Errorf("expected error on empty destination path")
		}
	})

	t.Run("provider error", func(t *testing.T) {
		destPath := filepath.Join(tempDir, "err.zip")
		provider := &mockSnapshotProvider{err: fmt.Errorf("core error")}
		builder := NewBuilder(provider, fakeBuildInfo)

		_, err := builder.Build(context.Background(), destPath, sampleHostSnapshot())
		if err == nil {
			t.Errorf("expected error on provider failure")
		}

		// Verify no residual temp files
		files, _ := os.ReadDir(tempDir)
		for _, f := range files {
			if strings.HasPrefix(f.Name(), ".godownloader-support-") {
				t.Errorf("residual temporary file found: %s", f.Name())
			}
		}
	})
}

func TestSupportBundle_ExistingDestination_Success(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	// 1. Create pre-existing destination with old content
	oldContent := []byte("OLD_BUNDLE_CONTENT")
	if err := os.WriteFile(destPath, oldContent, 0644); err != nil {
		t.Fatalf("failed to create existing destination: %v", err)
	}

	// 2. Build new support bundle to the same path
	provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
	builder := NewBuilder(provider, fakeBuildInfo)

	res, err := builder.Build(context.Background(), destPath, sampleHostSnapshot())
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if res.Status != "saved" {
		t.Errorf("expected status 'saved', got %q", res.Status)
	}

	// 3. Verify destination is a valid new ZIP and OLD_BUNDLE_CONTENT is gone
	newData, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read replaced destination: %v", err)
	}
	if bytes.Equal(newData, oldContent) {
		t.Fatal("destination was not overwritten; still contains OLD_BUNDLE_CONTENT")
	}

	r, err := zip.OpenReader(destPath)
	if err != nil {
		t.Fatalf("replaced destination is not a valid zip: %v", err)
	}
	if len(r.File) != 9 {
		t.Errorf("expected 9 entries in new zip, got %d", len(r.File))
	}
	r.Close()

	// 4. Verify no temporary files remain
	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".godownloader-support-") {
			t.Errorf("residual temp file found: %s", e.Name())
		}
	}
}

func TestSupportBundle_ExistingDestination_ReplacementFailurePreserved(t *testing.T) {
	tempDir := t.TempDir()
	destPath := filepath.Join(tempDir, "support.zip")

	// 1. Create pre-existing destination with old content
	oldContent := []byte("OLD_BUNDLE_CONTENT")
	if err := os.WriteFile(destPath, oldContent, 0644); err != nil {
		t.Fatalf("failed to create existing destination: %v", err)
	}

	// 2. Build with synthetic replacement error
	provider := &mockSnapshotProvider{snapshot: sampleCoreSnapshot()}
	builder := NewBuilder(provider, fakeBuildInfo)
	builder.SetReplaceFnForTesting(func(tempPath, destPath string) error {
		return fmt.Errorf("synthetic permission denied")
	})

	_, err := builder.Build(context.Background(), destPath, sampleHostSnapshot())
	if err == nil {
		t.Fatal("expected error on synthetic replacement failure")
	}

	// 3. Verify destination path did not leak in error message
	if strings.Contains(err.Error(), destPath) {
		t.Errorf("error leaked destination path: %v", err)
	}

	// 4. Verify existing destination STILL contains OLD_BUNDLE_CONTENT
	currData, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("failed to read destination after failed replacement: %v", err)
	}
	if !bytes.Equal(currData, oldContent) {
		t.Fatal("existing destination content was corrupted or deleted during failed replacement")
	}

	// 5. Verify temporary staged file was cleaned up
	entries, _ := os.ReadDir(tempDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".godownloader-support-") {
			t.Errorf("temporary file was not cleaned up on failure: %s", e.Name())
		}
	}
}

func TestProcessToolTaxonomy(t *testing.T) {
	// 1. Expected safe tool keys
	tests := []struct {
		input    string
		expected string
	}{
		{"yt-dlp", "yt-dlp"},
		{"YT-DLP", "yt-dlp"},
		{"ffmpeg", "ffmpeg"},
		{"aria2", "aria2"},
		{"qbittorrent", "qbittorrent"},
		// Unknown / malicious inputs
		{"ytdlp", "other"},
		{"curl", "other"},
		{"powershell.exe", "other"},
		{"../../evil", "other"},
		{"unknown_tool_token", "other"},
		{"", "other"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := SanitizeToolName(tc.input)
			if got != tc.expected {
				t.Errorf("SanitizeToolName(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}

	// 2. Verify aggregation behavior
	countsByTool := make(map[string]int)

	// Legitimate yt-dlp tool
	toolKey := SanitizeToolName("yt-dlp")
	countsByTool[toolKey]++
	if countsByTool["yt-dlp"] != 1 {
		t.Errorf("expected countsByTool['yt-dlp'] == 1, got %d", countsByTool["yt-dlp"])
	}
	if countsByTool["other"] != 0 {
		t.Errorf("expected countsByTool['other'] == 0, got %d", countsByTool["other"])
	}

	// Malicious unknown tool
	maliciousKey := SanitizeToolName("malicious_tool_binary")
	countsByTool[maliciousKey]++
	if countsByTool["other"] != 1 {
		t.Errorf("expected countsByTool['other'] == 1, got %d", countsByTool["other"])
	}
	if _, exists := countsByTool["malicious_tool_binary"]; exists {
		t.Errorf("raw malicious tool string became JSON map key")
	}
}

func TestRecoveryIssueTaxonomy(t *testing.T) {
	knownKinds := []string{
		"interrupted_media",
		"finalization_failed",
		"engine_unavailable",
		"external_state_unrecoverable",
		"torrent_metadata_unrecoverable",
	}

	for _, kind := range knownKinds {
		t.Run("known_"+kind, func(t *testing.T) {
			got := SanitizeIssueKind(kind)
			if got != kind {
				t.Errorf("SanitizeIssueKind(%q) = %q, want %q", kind, got, kind)
			}
		})
	}

	unknownInputs := []string{
		"torrent_metainfo_missing", // Obsolete key
		"random_future_kind",
		"unknown_db_issue",
		"__proto__",
		"",
	}

	for _, input := range unknownInputs {
		t.Run("unknown_"+input, func(t *testing.T) {
			got := SanitizeIssueKind(input)
			if got != "other" {
				t.Errorf("SanitizeIssueKind(%q) = %q, want 'other'", input, got)
			}
		})
	}

	// Verify aggregation map guarantees
	issueCounts := make(map[string]int)
	for _, k := range knownKinds {
		issueCounts[SanitizeIssueKind(k)]++
	}
	issueCounts[SanitizeIssueKind("torrent_metainfo_missing")]++

	for _, k := range knownKinds {
		if issueCounts[k] != 1 {
			t.Errorf("expected issueCounts[%q] == 1, got %d", k, issueCounts[k])
		}
	}
	if issueCounts["other"] != 1 {
		t.Errorf("expected issueCounts['other'] == 1, got %d", issueCounts["other"])
	}
	if _, exists := issueCounts["torrent_metainfo_missing"]; exists {
		t.Error("obsolete/unknown kind became JSON map key")
	}
}
