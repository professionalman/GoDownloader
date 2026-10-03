package app

import (
	"context"
	"strings"

	"downloader/internal/supportbundle"
	"downloader/internal/toolmanager"
)

// SupportSnapshotProvider implements supportbundle.SnapshotProvider by reading
// sanitized facts from App's owned subsystems without exposing App internals.
type SupportSnapshotProvider struct {
	app *App
}

// NewSupportSnapshotProvider constructs a new SupportSnapshotProvider wrapping the application runtime.
func NewSupportSnapshotProvider(a *App) *SupportSnapshotProvider {
	return &SupportSnapshotProvider{app: a}
}

// Snapshot gathers sanitized facts across all application subsystems.
// INVARIANT: Performs ZERO new network, RPC, or tool probe calls.
func (p *SupportSnapshotProvider) Snapshot(ctx context.Context) (supportbundle.CoreSnapshot, error) {
	if p.app == nil {
		return supportbundle.CoreSnapshot{}, nil
	}

	snapshot := supportbundle.CoreSnapshot{}

	// 1. Settings Snapshot
	if p.app.settingsService != nil {
		appSettings, err := p.app.settingsService.GetSettings(ctx)
		if err == nil && appSettings != nil {
			var customHeaderCount int
			if appSettings.Network.HTTPHeaders != nil {
				customHeaderCount = len(appSettings.Network.HTTPHeaders)
			}
			hasCustomUA := strings.TrimSpace(appSettings.Network.UserAgent) != ""

			snapshot.Settings = supportbundle.SafeSettingsDTO{
				Queue: supportbundle.SafeQueueSettings{
					MaxConcurrentDownloads:          appSettings.Queue.MaxConcurrentDownloads,
					EffectiveMaxConcurrentDownloads: appSettings.Queue.EffectiveMaxConcurrentDownloads,
					EnvironmentOverride:             appSettings.Queue.EnvironmentOverride,
				},
				Storage: supportbundle.SafeStorageSettings{
					MinimumFreeSpaceBytes: appSettings.Storage.MinimumFreeSpaceBytes,
					DefaultConflictPolicy: supportbundle.SanitizeConflictPolicy(appSettings.Storage.DefaultConflictPolicy),
					OverridesPresent:      appSettings.Storage.Overrides.DefaultDownloadDirectory || appSettings.Storage.Overrides.TemporaryDirectory || appSettings.Storage.Overrides.MinimumFreeSpaceBytes || appSettings.Storage.Overrides.DefaultConflictPolicy,
				},
				Network: supportbundle.SafeNetworkSettings{
					GlobalDownloadLimitBytesPerSecond: appSettings.Network.GlobalDownloadLimitBytesPerSecond,
					Proxy: supportbundle.SafeProxySettings{
						Mode:                  supportbundle.SanitizeProxyMode(string(appSettings.Network.Proxy.Mode)),
						Protocol:              supportbundle.SanitizeProxyProtocol(string(appSettings.Network.Proxy.Protocol)),
						Port:                  appSettings.Network.Proxy.Port,
						CredentialsConfigured: appSettings.Network.Proxy.HasPassword || appSettings.Network.Proxy.Username != "",
					},
					HasCustomUserAgent:      hasCustomUA,
					CustomHeaderCount:       customHeaderCount,
					RetryMaxAttempts:        appSettings.Network.RetryPolicy.MaxAttempts,
					RetryWaitSeconds:        appSettings.Network.RetryPolicy.RetryWaitSeconds,
					ConnectTimeoutSeconds:   appSettings.Network.TimeoutPolicy.ConnectTimeoutSeconds,
					RequestTimeoutSeconds:   appSettings.Network.TimeoutPolicy.RequestTimeoutSeconds,
					MaxConnectionsPerServer: appSettings.Network.DirectConnections.MaxConnectionsPerServer,
					Split:                   appSettings.Network.DirectConnections.Split,
				},
				Torrent: supportbundle.SafeTorrentSettings{
					DownloadLimitBytesPerSecond:            appSettings.Torrent.DownloadLimitBytesPerSecond,
					UploadLimitBytesPerSecond:              appSettings.Torrent.UploadLimitBytesPerSecond,
					SeedingMode:                            supportbundle.SanitizeSeedingMode(string(appSettings.Torrent.SeedingPolicy.Mode)),
					ApplyTrackerSubscriptionsToNewTorrents: appSettings.Torrent.ApplyTrackerSubscriptionsToNewTorrents,
					ManageQBitGlobalNetworkSettings:        appSettings.Torrent.ManageQBitGlobalNetworkSettings,
				},
			}
		}
	}

	// 2. Tools Snapshot (In-memory cached records via GetAll only!)
	var cachedTools []*toolmanager.ToolInfo
	if p.app.toolMgr != nil {
		toolMap := p.app.toolMgr.GetAll()
		for _, t := range toolMap {
			if t == nil {
				continue
			}
			cachedTools = append(cachedTools, t)
			snapshot.Tools = append(snapshot.Tools, supportbundle.SafeToolDTO{
				Name:               supportbundle.SanitizeToolName(t.Name),
				Version:            supportbundle.NormalizeToolVersion(t.Version),
				PlatformArch:       t.PlatformArch,
				OwnershipMode:      t.OwnershipMode,
				VerificationStatus: t.VerificationStatus,
				Status:             t.Status,
				Provenance:         t.Provenance,
				ProbedAt:           t.ProbedAt,
			})
		}
	}

	// Helper to find cached tool status
	getCachedToolStatus := func(toolName string) string {
		for _, t := range cachedTools {
			if t != nil && t.Name == toolName {
				return t.Status
			}
		}
		return "unknown"
	}

	// 3. Engines Snapshot (Registered status and cached tool health only, zero reachability probe)
	knownEngines := []struct {
		name          string
		toolName      string
		ownershipMode string
	}{
		{"aria2", "aria2", "compatibility"},
		{"qbittorrent", "qbittorrent", "external_user"},
		{"ytdlp", "yt-dlp", "managed"},
	}

	if p.app.registry != nil {
		for _, ke := range knownEngines {
			if _, ok := p.app.registry.Get(ke.name); ok {
				snapshot.Engines = append(snapshot.Engines, supportbundle.SafeEngineDTO{
					Name:             ke.name,
					Registered:       true,
					OwnershipMode:    ke.ownershipMode,
					CachedToolStatus: getCachedToolStatus(ke.toolName),
				})
			}
		}
	}

	// 4. Scheduler / Governor / Processes Snapshot
	if p.app.resourceGovernor != nil {
		govSnap := p.app.resourceGovernor.Snapshot()
		sanitizedEngineLimits := make(map[string]int)
		for eng, lim := range govSnap.EngineLimits {
			sanitizedEngineLimits[supportbundle.SanitizeEngineName(eng)] = lim
		}
		sanitizedActivePerEngine := make(map[string]int)
		for eng, count := range govSnap.ActivePerEngine {
			sanitizedActivePerEngine[supportbundle.SanitizeEngineName(eng)] = count
		}

		snapshot.Scheduler.Governor = supportbundle.SafeGovernorDTO{
			GlobalTransferLimit: govSnap.GlobalTransferLimit,
			ActiveTransfers:     govSnap.ActiveTransfers,
			ActiveLeases:        govSnap.ActiveLeases,
			EngineLimits:        sanitizedEngineLimits,
			ActivePerEngine:     sanitizedActivePerEngine,
		}
	}

	// Queued jobs count
	if p.app.db != nil && p.app.db.Conn() != nil {
		var qCount int
		_ = p.app.db.Conn().QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs WHERE status = 'queued'").Scan(&qCount)
		snapshot.Scheduler.Queue.QueuedJobsCount = qCount
	}

	// Processes aggregate (NO individual process rows, NO PIDs, NO JobIDs)
	if p.app.processSupervisor != nil {
		snapshot.Scheduler.Processes.ActiveSupervisedCount = p.app.processSupervisor.ActiveCount()
		countsByTool := make(map[string]int)
		countsByPurpose := make(map[string]int)
		countsByState := make(map[string]int)

		for _, proc := range p.app.processSupervisor.List() {
			if proc == nil {
				continue
			}
			toolKey := supportbundle.SanitizeToolName(proc.Tool)
			countsByTool[toolKey]++

			purposeKey := supportbundle.SanitizeProcessPurpose(proc.Purpose)
			countsByPurpose[purposeKey]++

			stateKey := supportbundle.SanitizeProcessState(string(proc.State))
			countsByState[stateKey]++
		}
		snapshot.Scheduler.Processes.CountsByTool = countsByTool
		snapshot.Scheduler.Processes.CountsByPurpose = countsByPurpose
		snapshot.Scheduler.Processes.CountsByState = countsByState
	}

	// 5. Recovery Snapshot (Aggregates only, zero JobIDs)
	if p.app.manager != nil {
		recSummary := p.app.manager.GetRecoverySummary()
		issueCounts := make(map[string]int)
		for _, issue := range recSummary.Issues {
			kind := supportbundle.SanitizeIssueKind(string(issue.Kind))
			issueCounts[kind]++
		}
		snapshot.Recovery = supportbundle.SafeRecoveryDTO{
			ReconciledFinalizations:       recSummary.ReconciledFinalizations,
			ReattachedTransfers:           recSummary.ReattachedTransfers,
			RestartedMetadataAcquisitions: recSummary.RestartedMetadataAcquisitions,
			InterruptedMediaJobs:          recSummary.InterruptedMediaJobs,
			IssueCountsByKind:             issueCounts,
		}
	}

	// 6. Database Summary Snapshot
	if p.app.db != nil && p.app.db.Conn() != nil {
		conn := p.app.db.Conn()

		// Migration markers
		markers := make(map[string]bool)
		var v07Migrated int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_settings WHERE key = 'v07_network_controls_migrated'").Scan(&v07Migrated); err == nil && v07Migrated > 0 {
			markers["v07NetworkControls"] = true
		} else {
			markers["v07NetworkControls"] = false
		}

		var v08Migrated int
		if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM app_settings WHERE key = 'v08_fnd1_migrated'").Scan(&v08Migrated); err == nil && v08Migrated > 0 {
			markers["v08Fnd1"] = true
		} else {
			markers["v08Fnd1"] = false
		}
		snapshot.DatabaseSummary.MigrationMarkers = markers

		// Tables present
		tables := make(map[string]bool)
		knownTables := map[string]string{
			"jobs":                 "jobs",
			"app_settings":         "appSettings",
			"job_queue":            "jobQueue",
			"job_executions":       "jobExecutions",
			"finalization_journal": "finalizationRecords",
		}
		for dbTable, fieldName := range knownTables {
			var exists int
			_ = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", dbTable).Scan(&exists)
			tables[fieldName] = exists > 0
		}
		snapshot.DatabaseSummary.TablesPresent = tables

		// Job counts by status (mapped to fixed status allowlist)
		statusCounts := make(map[string]int)
		rows, err := conn.QueryContext(ctx, "SELECT status, COUNT(*) FROM jobs GROUP BY status")
		if err == nil {
			for rows.Next() {
				var status string
				var count int
				if err := rows.Scan(&status, &count); err == nil {
					key := supportbundle.SanitizeJobStatus(status)
					statusCounts[key] += count
				}
			}
			_ = rows.Close()
		}
		snapshot.DatabaseSummary.JobCountsByStatus = statusCounts

		// Job counts by engine (mapped to fixed engine allowlist)
		engineCounts := make(map[string]int)
		rowsEng, err := conn.QueryContext(ctx, "SELECT engine, COUNT(*) FROM jobs GROUP BY engine")
		if err == nil {
			for rowsEng.Next() {
				var eng string
				var count int
				if err := rowsEng.Scan(&eng, &count); err == nil {
					key := supportbundle.SanitizeEngineName(eng)
					engineCounts[key] += count
				}
			}
			_ = rowsEng.Close()
		}
		snapshot.DatabaseSummary.JobCountsByEngine = engineCounts
	}

	// Pending finalizations count
	if p.app.execRepo != nil {
		pendingRecs, err := p.app.execRepo.GetPendingFinalizations(ctx)
		if err == nil {
			snapshot.DatabaseSummary.PendingFinalizationsCount = len(pendingRecs)
		}
		cursor, err := p.app.execRepo.GetEventCursor(ctx, "")
		if err == nil {
			snapshot.DatabaseSummary.StateSyncCursor = cursor
		}
	}

	return snapshot, nil
}

// Ensure interface compliance
var _ supportbundle.SnapshotProvider = (*SupportSnapshotProvider)(nil)
