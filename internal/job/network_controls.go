package job

import (
	"context"
	"fmt"
	"strings"
	"time"

	"downloader/internal/networkpolicy"
)

type NetworkLimitUpdate struct {
	DownloadLimitBytesPerSecond *int64 `json:"downloadLimitBytesPerSecond,omitempty"`
	UploadLimitBytesPerSecond   *int64 `json:"uploadLimitBytesPerSecond,omitempty"`
}

func (m *Manager) CapabilityProfiles() map[string]networkpolicy.JobCapabilities {
	result := make(map[string]networkpolicy.JobCapabilities, 3)
	for kind, engineName := range map[string]string{
		TypeDownload: "aria2", TypeMedia: "ytdlp", TypeTorrent: "qbittorrent",
	} {
		if engine, ok := m.engines.Get(engineName); ok {
			result[kind] = networkpolicy.ProjectCapabilities(engine.Capabilities(), true)
		}
	}
	return result
}

func (m *Manager) ResolveCapabilities(sources []string) networkpolicy.JobCapabilities {
	items := make([]networkpolicy.JobCapabilities, 0, len(sources))
	for _, source := range sources {
		engineName := m.engines.Detect(source)
		if engine, ok := m.engines.Get(engineName); ok {
			items = append(items, networkpolicy.ProjectCapabilities(engine.Capabilities(), true))
		}
	}
	return networkpolicy.IntersectCapabilities(items)
}

func (m *Manager) JobCapabilities(ctx context.Context, id string) (networkpolicy.JobCapabilities, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return networkpolicy.JobCapabilities{}, err
	}
	engine, ok := m.engines.Get(j.Engine)
	if !ok {
		return networkpolicy.JobCapabilities{}, &AppError{Code: ErrEngineError, Message: "engine not available"}
	}
	mutable := j.Status == StatusQueued || j.Status == StatusDownloading || j.Status == StatusPaused || j.Status == StatusSeeding || j.Status == StatusAwaitingSelection
	result := networkpolicy.ProjectCapabilities(engine.Capabilities(), mutable)
	result.Pause.MutableNow = j.Status == StatusDownloading || j.Status == StatusSeeding
	result.Resume.MutableNow = j.Status == StatusPaused
	result.Retry.MutableNow = j.Status == StatusFailed
	return result, nil
}

func (m *Manager) runtimePolicyForJob(ctx context.Context, j *Job) (*networkpolicy.RuntimePolicy, error) {
	if runtime := j.RuntimeNetworkPolicy(); runtime != nil {
		copyRuntime := *runtime
		copyRuntime.Policy = j.NetworkPolicy
		return &copyRuntime, nil
	}
	if m.settings == nil {
		return &networkpolicy.RuntimePolicy{Policy: j.NetworkPolicy, HeaderValues: map[string]string{}}, nil
	}
	runtime, err := m.settings.RuntimePolicyForJob(ctx, j.ID, j.NetworkPolicy)
	if err != nil {
		return nil, &AppError{Code: ErrSecretStorageUnavailable, Message: err.Error()}
	}
	return runtime, nil
}

func (m *Manager) effectiveDownloadLimit(ctx context.Context, desired int64) int64 {
	if m.settings == nil {
		return desired
	}
	st, err := m.settings.GetSettings(ctx)
	if err != nil {
		return desired
	}
	return networkpolicy.EffectiveLimit(st.Network.GlobalDownloadLimitBytesPerSecond, desired)
}

func (m *Manager) prepareNetworkDispatch(ctx context.Context, j *Job, engine IEngine) error {
	runtime, err := m.runtimePolicyForJob(ctx, j)
	if err != nil {
		return err
	}
	effective := m.effectiveDownloadLimit(ctx, j.NetworkPolicy.DownloadLimitBytesPerSecond)
	runtime.Policy.DownloadLimitBytesPerSecond = effective
	j.EffectiveDownloadLimitBytesPerSecond = effective
	j.EffectiveUploadLimitBytesPerSecond = j.NetworkPolicy.UploadLimitBytesPerSecond
	j.SetRuntimeNetworkPolicy(runtime)

	if controller, ok := engine.(IGlobalDownloadLimitController); ok && m.settings != nil {
		st, settingsErr := m.settings.GetSettings(ctx)
		if settingsErr == nil {
			if err := m.applyCachedLimit("global:"+j.Engine, st.Network.GlobalDownloadLimitBytesPerSecond, func() error {
				return controller.SetGlobalDownloadLimit(ctx, st.Network.GlobalDownloadLimitBytesPerSecond)
			}); err != nil {
				return &AppError{Code: ErrNetworkSettingApplicationFailed, Message: "failed to apply global download limit"}
			}
		}
	}
	return nil
}

func (m *Manager) applyCachedLimit(key string, value int64, apply func() error) error {
	m.mu.RLock()
	last, exists := m.appliedLimits[key]
	m.mu.RUnlock()
	if exists && last == value {
		return nil
	}
	if err := apply(); err != nil {
		return err
	}
	m.mu.Lock()
	m.appliedLimits[key] = value
	m.mu.Unlock()
	return nil
}

func (m *Manager) applyJobLimits(ctx context.Context, j *Job, engine IEngine, force bool) error {
	if controller, ok := engine.(IDownloadLimitController); ok && j.EngineID != "" {
		key := "download:" + j.Engine + ":" + j.ID
		if force {
			m.mu.Lock()
			delete(m.appliedLimits, key)
			m.mu.Unlock()
		}
		if err := m.applyCachedLimit(key, j.EffectiveDownloadLimitBytesPerSecond, func() error {
			return controller.SetDownloadLimit(ctx, j, j.EffectiveDownloadLimitBytesPerSecond)
		}); err != nil {
			return err
		}
	}
	if controller, ok := engine.(IUploadLimitController); ok && j.EngineID != "" {
		key := "upload:" + j.Engine + ":" + j.ID
		if force {
			m.mu.Lock()
			delete(m.appliedLimits, key)
			m.mu.Unlock()
		}
		if err := m.applyCachedLimit(key, j.EffectiveUploadLimitBytesPerSecond, func() error {
			return controller.SetUploadLimit(ctx, j, j.EffectiveUploadLimitBytesPerSecond)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) UpdateNetworkLimits(ctx context.Context, id string, update NetworkLimitUpdate) (*Job, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	if update.DownloadLimitBytesPerSecond == nil && update.UploadLimitBytesPerSecond == nil {
		return nil, &AppError{Code: ErrInvalidNetworkPolicy, Message: "at least one limit is required"}
	}
	old := *j
	if update.DownloadLimitBytesPerSecond != nil {
		if err := networkpolicy.ValidateBandwidth(*update.DownloadLimitBytesPerSecond); err != nil {
			return nil, &AppError{Code: ErrInvalidNetworkPolicy, Message: err.Error()}
		}
		j.NetworkPolicy.DownloadLimitBytesPerSecond = *update.DownloadLimitBytesPerSecond
	}
	if update.UploadLimitBytesPerSecond != nil {
		if err := networkpolicy.ValidateBandwidth(*update.UploadLimitBytesPerSecond); err != nil {
			return nil, &AppError{Code: ErrInvalidNetworkPolicy, Message: err.Error()}
		}
		j.NetworkPolicy.UploadLimitBytesPerSecond = *update.UploadLimitBytesPerSecond
	}
	engine, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "engine not available"}
	}
	capabilities := engine.Capabilities()
	if update.DownloadLimitBytesPerSecond != nil && !capabilities.PerJobDownloadLimit {
		return nil, &AppError{Code: ErrCapabilityNotSupported, Message: "per-job download limits are unsupported"}
	}
	if update.UploadLimitBytesPerSecond != nil && !capabilities.PerJobUploadLimit {
		return nil, &AppError{Code: ErrCapabilityNotSupported, Message: "per-job upload limits are unsupported"}
	}
	active := j.EngineID != "" && (j.Status == StatusDownloading || j.Status == StatusPaused || j.Status == StatusSeeding)
	if active && (capabilities.StartupOnly["downloadLimit"] || capabilities.StartupOnly["uploadLimit"]) {
		return nil, &AppError{Code: ErrCapabilityNotSupported, Message: "this limit is startup-only for the selected job"}
	}
	j.EffectiveDownloadLimitBytesPerSecond = m.effectiveDownloadLimit(ctx, j.NetworkPolicy.DownloadLimitBytesPerSecond)
	j.EffectiveUploadLimitBytesPerSecond = j.NetworkPolicy.UploadLimitBytesPerSecond

	if active {
		if err := m.applyJobLimits(ctx, j, engine, true); err != nil {
			return nil, &AppError{Code: ErrNetworkSettingApplicationFailed, Message: "failed to apply network limit"}
		}
	}
	j.NetworkReconcilePending = false
	j.UpdatedAt = time.Now()
	if err := m.repo.Update(ctx, j); err != nil {
		if active {
			if rollbackErr := m.applyJobLimits(ctx, &old, engine, true); rollbackErr != nil {
				return nil, &AppError{Code: ErrNetworkSettingStateAmbiguous, Message: "persistence and external rollback both failed"}
			}
		}
		return nil, &AppError{Code: ErrInternalError, Message: "failed to persist network policy"}
	}
	m.bus.Publish(Event{Type: EventJobNetworkUpdated, Job: *j, Data: map[string]any{
		"jobId": id, "networkPolicy": j.NetworkPolicy,
		"effectiveDownloadLimitBytesPerSecond": j.EffectiveDownloadLimitBytesPerSecond,
		"effectiveUploadLimitBytesPerSecond":   j.EffectiveUploadLimitBytesPerSecond,
	}})
	return j, nil
}

func (m *Manager) ReconcileNetworkPolicies(ctx context.Context) {
	m.reconcileManagedTorrentProxy(ctx)
	jobs, err := m.repo.List(ctx)
	if err != nil {
		return
	}
	globalApplied := make(map[string]bool)
	for i := range jobs {
		j := &jobs[i]
		if j.Status != StatusDownloading && j.Status != StatusPaused && j.Status != StatusSeeding {
			continue
		}
		engine, ok := m.engines.Get(j.Engine)
		if !ok {
			continue
		}
		if !globalApplied[j.Engine] {
			_ = m.prepareNetworkDispatch(ctx, j, engine)
			globalApplied[j.Engine] = true
		} else {
			j.EffectiveDownloadLimitBytesPerSecond = m.effectiveDownloadLimit(ctx, j.NetworkPolicy.DownloadLimitBytesPerSecond)
			j.EffectiveUploadLimitBytesPerSecond = j.NetworkPolicy.UploadLimitBytesPerSecond
		}
		if engine.Capabilities().StartupOnly["downloadLimit"] {
			continue
		}
		if err := m.applyJobLimits(ctx, j, engine, false); err != nil {
			j.NetworkReconcilePending = true
		} else {
			j.NetworkReconcilePending = false
		}
		j.UpdatedAt = time.Now()
		_ = m.repo.Update(ctx, j)
	}
}

func (m *Manager) reconcileManagedTorrentProxy(ctx context.Context) {
	if m.settings == nil {
		return
	}
	settings, err := m.settings.GetSettings(ctx)
	if err != nil || !settings.Torrent.ManageQBitGlobalNetworkSettings {
		return
	}
	engine, ok := m.engines.Get("qbittorrent")
	if !ok {
		return
	}
	controller, ok := engine.(IManagedTorrentProxyController)
	if !ok {
		return
	}
	ownership, err := controller.ListTorrentOwnership(ctx)
	if err != nil {
		return
	}
	jobs, err := m.repo.List(ctx)
	if err != nil {
		return
	}
	owned := make(map[string]string, len(jobs))
	for i := range jobs {
		if jobs[i].Type == TypeTorrent && jobs[i].EngineID != "" {
			owned[strings.ToLower(jobs[i].EngineID)] = jobs[i].ID
		}
	}
	for _, torrent := range ownership {
		jobID, exists := owned[strings.ToLower(torrent.Hash)]
		if !exists || torrent.Category != "godownloader" || !containsString(torrent.Tags, jobID) {
			return
		}
	}
	runtime, err := m.settings.RuntimeGlobalNetworkPolicy(ctx)
	if err != nil {
		return
	}
	_ = controller.ApplyManagedProxy(ctx, runtime)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (m *Manager) runNetworkReconciler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.ReconcileNetworkPolicies(ctx)
		}
	}
}

func normalizeSourceList(source any) ([]string, error) {
	switch value := source.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("source is required")
		}
		return []string{value}, nil
	case []string:
		if len(value) == 0 {
			return nil, fmt.Errorf("at least one source is required")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("source must be a string or string array")
	}
}
