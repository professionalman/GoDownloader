package job

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"downloader/internal/networkpolicy"
)

func (m *Manager) AddTorrentTrackers(ctx context.Context, id string, values []string) ([]networkpolicy.Tracker, error) {
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.Type != TypeTorrent || j.EngineID == "" {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "torrent metadata is not available"}
	}
	trackers, err := networkpolicy.ValidateTrackerURLs(values, 256)
	if err != nil {
		return nil, &AppError{Code: ErrInvalidTrackerURL, Message: err.Error()}
	}
	return m.addValidatedTorrentTrackers(ctx, j, trackers)
}

func (m *Manager) applyTorrentTrackerSnapshot(ctx context.Context, j *Job) ([]networkpolicy.Tracker, error) {
	trackers, err := networkpolicy.ValidateTrackerURLs(j.CustomTrackers, 10000)
	if err != nil {
		return nil, &AppError{Code: ErrInvalidTrackerURL, Message: err.Error()}
	}
	return m.addValidatedTorrentTrackers(ctx, j, trackers)
}

func (m *Manager) addValidatedTorrentTrackers(ctx context.Context, j *Job, trackers []string) ([]networkpolicy.Tracker, error) {
	engine, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "torrent engine not available"}
	}
	controller, ok := engine.(ITrackerController)
	if !ok {
		return nil, &AppError{Code: ErrCapabilityNotSupported, Message: "trackers are unsupported"}
	}
	private, err := controller.GetTorrentPrivacy(ctx, j)
	if err != nil {
		return nil, &AppError{Code: ErrNetworkSettingApplicationFailed, Message: "torrent privacy could not be verified"}
	}
	if private {
		return nil, &AppError{Code: ErrPrivateTorrentTrackerRejected, Message: "custom trackers are disabled for private torrents"}
	}
	existing, err := controller.GetTrackers(ctx, j)
	if err != nil {
		return nil, &AppError{Code: ErrNetworkSettingApplicationFailed, Message: "failed to read existing trackers"}
	}
	seen := make(map[string]bool, len(existing))
	for _, tracker := range existing {
		seen[strings.TrimSpace(tracker.URL)] = true
	}
	missing := make([]string, 0, len(trackers))
	for _, tracker := range trackers {
		if !seen[tracker] {
			seen[tracker] = true
			missing = append(missing, tracker)
		}
	}
	if len(missing) > 0 {
		if err := controller.AddTrackers(ctx, j, missing); err != nil {
			return nil, &AppError{Code: ErrNetworkSettingApplicationFailed, Message: "failed to add trackers"}
		}
	}
	return controller.GetTrackers(ctx, j)
}

func (m *Manager) UpdateSeedingPolicy(ctx context.Context, id string, policy networkpolicy.SeedingPolicy) (*Job, error) {
	if err := networkpolicy.ValidateSeeding(policy); err != nil {
		return nil, &AppError{Code: ErrInvalidSeedingPolicy, Message: err.Error()}
	}
	policy = cloneSeedingPolicy(policy)
	j, err := m.getJobOrError(ctx, id)
	if err != nil {
		return nil, err
	}
	if j.Type != TypeTorrent {
		return nil, &AppError{Code: ErrInvalidJobState, Message: "seeding policy is only available for torrents"}
	}
	switch j.Status {
	case StatusAwaitingSelection, StatusQueued, StatusDownloading, StatusPaused, StatusSeeding:
	default:
		return nil, &AppError{Code: ErrInvalidJobState, Message: fmt.Sprintf("cannot change seeding policy for a %s job", j.Status)}
	}
	if m.torrentRepo == nil {
		return nil, &AppError{Code: ErrInternalError, Message: "torrent policy record is unavailable"}
	}
	rec, err := m.torrentRepo.GetTorrentJob(ctx, id)
	if err != nil || rec == nil {
		return nil, &AppError{Code: ErrInternalError, Message: "torrent policy record is unavailable"}
	}
	rec = cloneTorrentRecord(rec)
	oldPolicy := cloneSeedingPolicy(rec.SeedingPolicy)
	oldSeedAfterComplete := rec.SeedAfterComplete
	synchronizeJobSeedingState(j, rec)
	j.SeedingPolicy = cloneSeedingPolicy(oldPolicy)
	j.SeedAfterComplete = oldSeedAfterComplete

	if j.Status == StatusSeeding && policy.Mode == networkpolicy.SeedingModeNone {
		stopped, stopErr := m.stopSeedingWithReason(ctx, j, "policy_none")
		if stopErr != nil {
			var finalizeErr *TorrentFinalizeError
			if errors.As(stopErr, &finalizeErr) {
				if finalizeErr.Kind == TorrentFinalizeCleanupFailure {
					return j, &AppError{Code: ErrSeedingPolicyApplicationFailed, Message: "torrent completed but daemon cleanup is pending"}
				}
				return nil, &AppError{Code: ErrInternalError, Message: "failed to persist completed torrent state"}
			}
			if !stopped {
				return nil, &AppError{Code: ErrSeedingPolicyApplicationFailed, Message: "failed to stop seeding"}
			}
			return nil, &AppError{Code: ErrInternalError, Message: "failed to finalize torrent"}
		}
		m.publishSeedingPolicyUpdated(id, j)
		return j, nil
	}
	engine, ok := m.engines.Get(j.Engine)
	if !ok {
		return nil, &AppError{Code: ErrEngineError, Message: "torrent engine not available"}
	}
	controller, ok := engine.(ISeedingPolicyController)
	if !ok {
		return nil, &AppError{Code: ErrCapabilityNotSupported, Message: "seeding policies are unsupported"}
	}
	if j.EngineID != "" {
		if err := controller.ApplySeedingPolicy(ctx, j, policy); err != nil {
			return nil, &AppError{Code: ErrSeedingPolicyApplicationFailed, Message: "failed to apply seeding policy"}
		}
	}
	rec.SeedingPolicy = cloneSeedingPolicy(policy)
	rec.SeedAfterComplete = policy.Mode != networkpolicy.SeedingModeNone
	if err := m.torrentRepo.UpdateTorrentJob(ctx, rec); err != nil {
		if j.EngineID != "" {
			if rollbackErr := controller.ApplySeedingPolicy(ctx, j, oldPolicy); rollbackErr != nil {
				return nil, &AppError{Code: ErrSeedingPolicyStateAmbiguous, Message: "seeding persistence and rollback both failed"}
			}
		}
		return nil, &AppError{Code: ErrInternalError, Message: "failed to persist seeding policy"}
	}
	synchronizeJobSeedingState(j, rec)
	j.UpdatedAt = time.Now()
	m.updateActiveJobSeedingState(id, rec)
	m.publishSeedingPolicyUpdated(id, j)
	return j, nil
}

func (m *Manager) publishSeedingPolicyUpdated(id string, j *Job) {
	eventJob := cloneJobSeedingState(j)
	m.bus.Publish(Event{Type: EventJobSeedingPolicyUpdated, Job: eventJob, Data: map[string]any{
		"jobId": id, "seedingPolicy": cloneSeedingPolicy(eventJob.SeedingPolicy),
	}})
}

func seedingThresholdReason(policy networkpolicy.SeedingPolicy, ratio float64, seconds int64) string {
	ratioReached := policy.RatioLimit != nil && ratio >= *policy.RatioLimit
	timeReached := policy.TimeLimitSeconds != nil && seconds >= *policy.TimeLimitSeconds
	switch policy.Mode {
	case networkpolicy.SeedingModeRatio:
		if ratioReached {
			return "ratio"
		}
	case networkpolicy.SeedingModeDuration:
		if timeReached {
			return "duration"
		}
	case networkpolicy.SeedingModeRatioOrDuration:
		if ratioReached {
			return "ratio"
		}
		if timeReached {
			return "duration"
		}
	}
	return ""
}

func (m *Manager) enterSeeding(ctx context.Context, j *Job, status *EngineStatus) bool {
	if j.SeedingPolicy.Mode == "" {
		if j.SeedAfterComplete {
			j.SeedingPolicy.Mode = networkpolicy.SeedingModeUnlimited
		} else {
			j.SeedingPolicy.Mode = networkpolicy.SeedingModeNone
		}
	}
	if j.SeedingPolicy.Mode == networkpolicy.SeedingModeNone {
		_ = m.finalizeCompletedTorrentWithReason(ctx, j, "policy_none")
		return true
	}
	if reason := seedingThresholdReason(j.SeedingPolicy, status.Ratio, status.SeedingTimeSeconds); reason != "" {
		if engine, ok := m.engines.Get(j.Engine); ok {
			if torrentEngine, torrentOK := engine.(ITorrentEngine); torrentOK {
				_ = torrentEngine.StopDownload(ctx, j.EngineID)
			}
		}
		_ = m.finalizeCompletedTorrentWithReason(ctx, j, reason)
		return true
	}
	if j.SeedingStartedAt == nil {
		now := time.Now()
		j.SeedingStartedAt = &now
		if m.torrentRepo != nil {
			if rec, err := m.torrentRepo.GetTorrentJob(ctx, j.ID); err == nil && rec != nil {
				rec = cloneTorrentRecord(rec)
				rec.SeedingStartedAt = &now
				_ = m.torrentRepo.UpdateTorrentJob(ctx, rec)
			}
		}
	}
	j.Status = StatusSeeding
	j.Progress = 100
	j.SpeedBytesPerSecond = status.UploadSpeed
	j.ETASeconds = 0
	j.UpdatedAt = time.Now()
	if j.TorrentInfo != nil {
		j.TorrentInfo.UploadSpeed = status.UploadSpeed
		j.TorrentInfo.Uploaded = status.Uploaded
		j.TorrentInfo.Ratio = status.Ratio
		j.TorrentInfo.Seeders = status.Seeders
		j.TorrentInfo.Leechers = status.Leechers
		j.TorrentInfo.SeedingTimeSeconds = status.SeedingTimeSeconds
	}
	return false
}

func cloneTorrentRecord(record *TorrentJobRecord) *TorrentJobRecord {
	if record == nil {
		return nil
	}
	copyRecord := *record
	copyRecord.SeedingPolicy = cloneSeedingPolicy(record.SeedingPolicy)
	copyRecord.SeedingStartedAt = cloneTimePointer(record.SeedingStartedAt)
	copyRecord.CustomTrackers = append([]string(nil), record.CustomTrackers...)
	return &copyRecord
}

func cloneSeedingPolicy(policy networkpolicy.SeedingPolicy) networkpolicy.SeedingPolicy {
	copyPolicy := policy
	if policy.RatioLimit != nil {
		ratio := *policy.RatioLimit
		copyPolicy.RatioLimit = &ratio
	}
	if policy.TimeLimitSeconds != nil {
		duration := *policy.TimeLimitSeconds
		copyPolicy.TimeLimitSeconds = &duration
	}
	return copyPolicy
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func synchronizeJobSeedingState(j *Job, record *TorrentJobRecord) {
	j.SeedingPolicy = cloneSeedingPolicy(record.SeedingPolicy)
	j.SeedAfterComplete = record.SeedAfterComplete
	j.SeedingStartedAt = cloneTimePointer(record.SeedingStartedAt)
	j.SeedingStopReason = record.SeedingStopReason
}

func cloneJobSeedingState(j *Job) Job {
	copyJob := *j
	copyJob.SeedingPolicy = cloneSeedingPolicy(j.SeedingPolicy)
	copyJob.SeedingStartedAt = cloneTimePointer(j.SeedingStartedAt)
	return copyJob
}

func unsupportedTorrentControl(name string) error {
	return &AppError{Code: ErrCapabilityNotSupported, Message: fmt.Sprintf("%s is unsupported", name)}
}
