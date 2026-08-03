import React, { useEffect, useState } from 'react';
import type { Job, TorrentFile, TorrentFileSelection, TorrentFilePriority, SeedingMode, SeedingPolicy } from '../types';
import { getTorrentFiles } from '../api';

interface TorrentFileSelectorProps {
  job: Job;
  onStart: (jobId: string, files: TorrentFileSelection[], seedingPolicy: SeedingPolicy) => Promise<void>;
  onClose: () => void;
}

export function normalizeSeedingMode(value: unknown): SeedingMode {
  if (typeof value !== 'string') return 'none';
  switch (value) {
    case 'none':
    case 'unlimited':
    case 'ratio':
    case 'duration':
    case 'ratio_or_duration':
      return value;
    default:
      return 'none';
  }
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + sizes[i];
}

export const TorrentFileSelector: React.FC<TorrentFileSelectorProps> = ({ job, onStart, onClose }) => {
  const [files, setFiles] = useState<TorrentFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const [seedingMode, setSeedingMode] = useState<SeedingMode>(() => normalizeSeedingMode(job.seedingPolicy?.mode));
  const [ratioLimit, setRatioLimit] = useState(job.seedingPolicy?.ratioLimit ?? 1);
  const [durationHours, setDurationHours] = useState((job.seedingPolicy?.timeLimitSeconds ?? 86400) / 3600);

  useEffect(() => {
    getTorrentFiles(job.id)
      .then(f => {
        setFiles(f);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message || 'Failed to fetch torrent files');
        setLoading(false);
      });
  }, [job.id]);

  const toggleSelection = (index: number) => {
    setFiles(prev => prev.map(f => f.index === index ? { ...f, selected: !f.selected } : f));
  };

  const changePriority = (index: number, priority: TorrentFilePriority) => {
    setFiles(prev => prev.map(f => f.index === index ? { ...f, priority } : f));
  };

  const selectAll = (selected: boolean) => {
    setFiles(prev => prev.map(f => ({ ...f, selected })));
  };

  const handleStart = async () => {
    if (isSubmitting || selectedCount === 0) return;
    setIsSubmitting(true);
    setSubmitError('');

    const selection = files.map(f => ({
      index: f.index,
      priority: f.selected ? f.priority : ('skip' as TorrentFilePriority)
    }));

    const policy: SeedingPolicy = { mode: seedingMode };
    if (seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') {
      policy.ratioLimit = ratioLimit;
    }
    if (seedingMode === 'duration' || seedingMode === 'ratio_or_duration') {
      policy.timeLimitSeconds = Math.round(durationHours * 3600);
    }

    try {
      await onStart(job.id, selection, policy);
    } catch (err: unknown) {
      setSubmitError(err instanceof Error ? err.message : String(err));
      setIsSubmitting(false);
    }
  };

  const selectedCount = files.filter(f => f.selected).length;
  const selectedSize = files.filter(f => f.selected).reduce((acc, f) => acc + f.size, 0);
  const totalSize = files.reduce((acc, f) => acc + f.size, 0);

  return (
    <div className="modal-overlay">
      <div className="modal-content torrent-modal">
        <h2>Select Files for {job.name}</h2>
        {error && <div className="app-error">{error}</div>}
        {submitError && <div className="app-error">{submitError}</div>}
        
        {loading ? (
          <div style={{ textAlign: 'center', padding: '20px' }}>Loading files...</div>
        ) : (
          <>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '10px' }}>
              <div>
                <button className="btn btn-sm btn-secondary" onClick={() => selectAll(true)} style={{ marginRight: '8px' }} disabled={isSubmitting}>Select All</button>
                <button className="btn btn-sm btn-secondary" onClick={() => selectAll(false)} disabled={isSubmitting}>Deselect All</button>
              </div>
              <div>
                Selected: {selectedCount}/{files.length} ({formatBytes(selectedSize)} / {formatBytes(totalSize)})
              </div>
            </div>

            <div style={{ maxHeight: '400px', overflowY: 'auto' }}>
              <table className="format-table torrent-file-table">
                <thead>
                  <tr>
                    <th><input type="checkbox" onChange={(e) => selectAll(e.target.checked)} checked={selectedCount === files.length && files.length > 0} disabled={isSubmitting} /></th>
                    <th>File Path</th>
                    <th>Size</th>
                    <th>Priority</th>
                  </tr>
                </thead>
                <tbody>
                  {files.map(f => (
                    <tr key={f.index}>
                      <td>
                        <input type="checkbox" checked={f.selected} onChange={() => toggleSelection(f.index)} disabled={isSubmitting} />
                      </td>
                      <td style={{ wordBreak: 'break-all' }}>{f.path}</td>
                      <td>{formatBytes(f.size)}</td>
                      <td>
                        <select
                          value={f.priority}
                          onChange={(e) => changePriority(f.index, e.target.value as TorrentFilePriority)}
                          disabled={!f.selected || isSubmitting}
                          style={{ background: '#21262d', color: '#e1e4e8', border: '1px solid #30363d', borderRadius: '4px', padding: '2px 4px' }}
                        >
                          <option value="normal">Normal</option>
                          <option value="high">High</option>
                          <option value="maximum">Maximum</option>
                        </select>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            
            <div style={{ marginTop: '16px', display: 'grid', gap: '8px' }}>
              <label htmlFor="seeding-mode">Seeding policy</label>
              <select
                id="seeding-mode"
                value={seedingMode}
                onChange={(e) => setSeedingMode(normalizeSeedingMode(e.target.value))}
                disabled={isSubmitting}
              >
                <option value="none">Do not seed</option>
                <option value="unlimited">Seed without a limit</option>
                <option value="ratio">Stop at ratio</option>
                <option value="duration">Stop after active seeding time</option>
                <option value="ratio_or_duration">Stop at ratio or duration</option>
              </select>
              {(seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') && (
                <label>Ratio target <input aria-label="Ratio target" type="number" min="0.01" max="1000" step="0.1" value={ratioLimit} onChange={(e) => setRatioLimit(Number(e.target.value))} disabled={isSubmitting} /></label>
              )}
              {(seedingMode === 'duration' || seedingMode === 'ratio_or_duration') && (
                <label>Active seeding hours <input aria-label="Active seeding hours" type="number" min="0.01" max="87600" step="0.5" value={durationHours} onChange={(e) => setDurationHours(Number(e.target.value))} disabled={isSubmitting} /></label>
              )}
            </div>
            <div style={{ fontSize: '0.78rem', opacity: 0.6, marginTop: '4px' }}>
              ℹ️ Torrent conflict policy is engine-managed by qBittorrent.
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' }}>
              <button className="btn btn-secondary" onClick={onClose} disabled={isSubmitting}>Cancel</button>
              <button className="btn btn-primary" onClick={handleStart} disabled={selectedCount === 0 || isSubmitting}>
                {isSubmitting ? 'Starting…' : 'Start Download'}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
};
