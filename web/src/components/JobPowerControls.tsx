import React, { useEffect, useState } from 'react';
import type { Job, JobCapabilities, SeedingMode, SeedingPolicy } from '../types';
import { addTorrentTrackers, getJobCapabilities, updateJobNetwork, updateSeedingPolicy } from '../api';

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.max(0, Math.round(seconds))}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return minutes ? `${hours}h ${minutes}m` : `${hours}h`;
}

function formatSpeed(bytesPerSecond: number): string {
  if (!bytesPerSecond) return '0 B/s';
  const units = ['B/s', 'KiB/s', 'MiB/s', 'GiB/s'];
  let value = bytesPerSecond;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function policyLabel(mode: SeedingMode): string {
  switch (mode) {
    case 'unlimited': return 'Unlimited';
    case 'ratio': return 'Stop at ratio';
    case 'duration': return 'Stop after active seeding time';
    case 'ratio_or_duration': return 'Stop at ratio or duration';
    default: return 'Do not seed';
  }
}

interface JobPowerControlsProps {
  job: Job;
  capabilities?: JobCapabilities | null;
  onUpdated?: (job: Job) => void;
}

export const JobPowerControls: React.FC<JobPowerControlsProps> = ({
  job,
  capabilities: passedCapabilities,
  onUpdated,
}) => {
  const [localCapabilities, setLocalCapabilities] = useState<JobCapabilities | null>(passedCapabilities ?? null);
  const [download, setDownload] = useState(job.networkPolicy?.downloadLimitBytesPerSecond ?? 0);
  const [upload, setUpload] = useState(job.networkPolicy?.uploadLimitBytesPerSecond ?? 0);
  const [trackers, setTrackers] = useState('');
  const [seedingMode, setSeedingMode] = useState<SeedingMode>(job.seedingPolicy?.mode ?? 'none');
  const [ratio, setRatio] = useState(job.seedingPolicy?.ratioLimit ?? 1);
  const [hours, setHours] = useState((job.seedingPolicy?.timeLimitSeconds ?? 86400) / 3600);
  const [message, setMessage] = useState('');

  useEffect(() => {
    if (passedCapabilities !== undefined) {
      setLocalCapabilities(passedCapabilities);
    } else {
      getJobCapabilities(job.id).then(setLocalCapabilities).catch(() => setLocalCapabilities(null));
    }
  }, [job.id, passedCapabilities]);

  const capabilities = localCapabilities;

  if (!capabilities) return null;
  const hasControls = capabilities.downloadLimit.supported || capabilities.uploadLimit.supported || capabilities.trackers.supported || capabilities.seedingPolicy.supported;
  if (!hasControls) return null;

  const saveLimits = async () => {
    const payload: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number } = {};
    if (capabilities.downloadLimit.mutableNow) payload.downloadLimitBytesPerSecond = download;
    if (capabilities.uploadLimit.mutableNow) payload.uploadLimitBytesPerSecond = upload;
    const updated = await updateJobNetwork(job.id, payload);
    onUpdated?.(updated);
    setMessage('Limits updated.');
  };

  const saveSeeding = async () => {
    const policy: SeedingPolicy = { mode: seedingMode };
    if (seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') policy.ratioLimit = ratio;
    if (seedingMode === 'duration' || seedingMode === 'ratio_or_duration') policy.timeLimitSeconds = Math.round(hours * 3600);
    const updated = await updateSeedingPolicy(job.id, policy);
    onUpdated?.(updated);
    setMessage('Seeding policy updated.');
  };

  return (
    <details className="job-power-controls" open={job.status === 'seeding' ? true : undefined}>
      <summary>Network & torrent controls</summary>
      <div className="power-grid">
        {capabilities.downloadLimit.supported && <label>Download bytes/s<input type="number" min="0" value={download} disabled={!capabilities.downloadLimit.mutableNow} onChange={(e) => setDownload(Number(e.target.value))} /></label>}
        {capabilities.uploadLimit.supported && <label>Upload bytes/s<input type="number" min="0" value={upload} disabled={!capabilities.uploadLimit.mutableNow} onChange={(e) => setUpload(Number(e.target.value))} /></label>}
        {(capabilities.downloadLimit.mutableNow || capabilities.uploadLimit.mutableNow) && <button type="button" className="btn btn-sm btn-secondary" onClick={() => saveLimits().catch((error) => setMessage(error.message))}>Apply limits</button>}
        {capabilities.trackers.mutableNow && <label style={{ gridColumn: '1 / -1' }}>Add trackers<textarea value={trackers} onChange={(e) => setTrackers(e.target.value)} placeholder="One tracker URL per line" /><button type="button" className="btn btn-sm btn-secondary" onClick={() => addTorrentTrackers(job.id, trackers.split('\n').map((value) => value.trim()).filter(Boolean)).then(() => { setTrackers(''); setMessage('Trackers added.'); }).catch((error) => setMessage(error.message))}>Add missing trackers</button></label>}
        {capabilities.seedingPolicy.mutableNow && <>
          <label>Seeding policy<select value={seedingMode} onChange={(e) => setSeedingMode(e.target.value as SeedingMode)}><option value="none">None</option><option value="unlimited">Unlimited</option><option value="ratio">Ratio</option><option value="duration">Duration</option><option value="ratio_or_duration">Ratio or duration</option></select></label>
          {(seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') && <label>Ratio<input type="number" min="0.01" max="1000" value={ratio} onChange={(e) => setRatio(Number(e.target.value))} /></label>}
          {(seedingMode === 'duration' || seedingMode === 'ratio_or_duration') && <label>Active hours<input type="number" min="0.01" value={hours} onChange={(e) => setHours(Number(e.target.value))} /></label>}
          <button type="button" className="btn btn-sm btn-secondary" onClick={() => saveSeeding().catch((error) => setMessage(error.message))}>Apply seeding policy</button>
        </>}
      </div>
      <div className="setting-hint">Effective ↓ {job.effectiveDownloadLimitBytesPerSecond || 'unlimited'} B/s · ↑ {job.effectiveUploadLimitBytesPerSecond || 'unlimited'} B/s{job.networkReconcilePending ? ' · reconciliation pending' : ''}</div>
      {job.status === 'seeding' && <div className="active-seeding-summary" aria-label="Active seeding status">
        <div>Ratio: {job.torrentInfo?.ratio?.toFixed(2) ?? '0.00'}{job.seedingPolicy?.ratioLimit != null ? ` / ${job.seedingPolicy.ratioLimit.toFixed(2)}` : ''}</div>
        <div>Seeding time: {formatDuration(job.torrentInfo?.seedingTimeSeconds ?? 0)}{job.seedingPolicy?.timeLimitSeconds != null ? ` / ${formatDuration(job.seedingPolicy.timeLimitSeconds)}` : ''}</div>
        <div>Upload speed: {formatSpeed(job.torrentInfo?.uploadSpeed ?? 0)}</div>
        <div>Seeders: {job.torrentInfo?.seeders ?? 0} · Leechers: {job.torrentInfo?.leechers ?? 0}</div>
        <div>Policy: {policyLabel(job.seedingPolicy?.mode ?? 'none')}</div>
      </div>}
      {message && <div className="setting-notice">{message}</div>}
    </details>
  );
};
