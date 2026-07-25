import React from 'react';
import type { Job } from '../types';

interface JobCardProps {
  job: Job;
  selected?: boolean;
  onToggleSelect?: (id: string) => void;
  onSetPriority?: (id: string, priority: import('../types').JobPriority) => void;
  onCancel?: (id: string) => void;
  onPause?: (id: string) => void;
  onResume?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
  onSelectFormat?: (id: string) => void;
  onSelectTorrentFiles?: (id: string) => void;
  onStopSeeding?: (id: string) => void;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + sizes[i];
}

function formatSpeed(bytesPerSec: number): string {
  if (bytesPerSec === 0) return '—';
  return formatBytes(bytesPerSec) + '/s';
}

function formatETA(seconds: number): string {
  if (seconds <= 0) return '—';
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) {
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}m ${s}s`;
  }
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  return `${h}h ${m}m`;
}

export const JobCard: React.FC<JobCardProps> = ({
  job,
  selected,
  onToggleSelect,
  onSetPriority,
  onCancel,
  onPause,
  onResume,
  onRetry,
  onOpenFolder,
  onSelectFormat,
  onSelectTorrentFiles,
  onStopSeeding,
}) => {
  const isDownloading = job.status === 'downloading';
  const isQueued = job.status === 'queued';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isAnalyzing = job.status === 'analyzing';
  const isProcessing = job.status === 'processing';
  const isAwaitingSelection = job.status === 'awaiting_selection';
  const isSeeding = job.status === 'seeding';
  const isActive = isDownloading || isQueued || isSeeding;
  const isMediaJob = job.type === 'media';
  const isTorrentJob = job.type === 'torrent';

  // Analysis is complete when mediaInfo has formats
  const analysisReady = isAnalyzing && job.mediaInfo && job.mediaInfo.formats && job.mediaInfo.formats.length > 0;
  const priority = job.priority || 'normal';

  return (
    <div className={`job-card job-${job.status} ${selected ? 'job-selected' : ''}`}>
      <div className="job-header">
        <div className="job-name-area">
          {onToggleSelect && (
            <input
              type="checkbox"
              className="job-select-checkbox"
              checked={!!selected}
              onChange={() => onToggleSelect(job.id)}
            />
          )}
          {isMediaJob && job.mediaInfo?.thumbnail && (
            <img
              src={job.mediaInfo.thumbnail}
              alt=""
              className="job-thumbnail"
              onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
            />
          )}
          <span className="job-name" title={job.source}>{job.name || 'Untitled'}</span>
        </div>
        <div className="job-badges">
          {onSetPriority ? (
            <select
              className="priority-select-sm"
              value={priority}
              onChange={(e) => onSetPriority(job.id, e.target.value as import('../types').JobPriority)}
            >
              <option value="high">🔥 High</option>
              <option value="normal">⚡ Normal</option>
              <option value="low">🐢 Low</option>
            </select>
          ) : (
            priority !== 'normal' && (
              <span className={`priority-badge priority-${priority}`}>
                {priority === 'high' ? '🔥 HIGH' : '🐢 LOW'}
              </span>
            )
          )}
          {isMediaJob && <span className="job-engine-badge">🎬 Media</span>}
          {isTorrentJob && <span className="job-engine-badge">🧲 Torrent</span>}
          <span className={`job-status status-${job.status}`}>{job.status.replace('_', ' ')}</span>
        </div>
      </div>

      {/* Analyzing state */}
      {isAnalyzing && !analysisReady && (
        <div className="job-analyzing">
          <span className="analyzing-spinner">⟳</span>
          <span>{isTorrentJob ? 'Fetching torrent metadata...' : 'Analyzing media...'}</span>
        </div>
      )}

      {/* Analysis complete — show select format button */}
      {analysisReady && onSelectFormat && (
        <div className="job-analyzing-ready">
          <span className="analyzing-ready-text">
            {job.mediaInfo!.formats.length} formats available
          </span>
          <button className="btn btn-primary btn-sm" onClick={() => onSelectFormat(job.id)}>
            🎬 Select Format
          </button>
        </div>
      )}

      {/* Awaiting file selection state (Torrent) */}
      {isAwaitingSelection && onSelectTorrentFiles && (
        <div className="job-analyzing-ready">
          <span className="analyzing-ready-text">
            Torrent metadata loaded
          </span>
          <button className="btn btn-primary btn-sm" onClick={() => onSelectTorrentFiles(job.id)}>
            🎬 Select Files
          </button>
        </div>
      )}

      {/* Processing state (FFmpeg) */}
      {isProcessing && (
        <div className="job-processing">
          <div className="progress-bar-container">
            <div className="progress-bar-fill progress-processing" style={{ width: '100%' }} />
          </div>
          <div className="job-details">
            <span className="processing-label">⚙ Processing (FFmpeg merging)...</span>
          </div>
        </div>
      )}

      {/* Progress bar for downloading, queued, paused, and seeding jobs */}
      {(isActive || isPaused) && !isSeeding && (
        <>
          <div className="progress-bar-container">
            <div
              className={`progress-bar-fill ${isPaused ? 'progress-paused' : ''}`}
              style={{ width: `${Math.min(job.progress, 100)}%` }}
            />
          </div>
          <div className="job-details">
            <span className="job-progress">{job.progress.toFixed(1)}%</span>
            <span className="job-size">
              {formatBytes(job.completedBytes)} / {job.totalBytes > 0 ? formatBytes(job.totalBytes) : '...'}
            </span>
            {isDownloading && (
              <>
                <span className="job-speed">{formatSpeed(job.speedBytesPerSecond)}</span>
                <span className="job-eta">ETA: {formatETA(job.etaSeconds)}</span>
              </>
            )}
            {isPaused && <span className="job-paused-label">Paused</span>}
            {isTorrentJob && job.torrentInfo && isDownloading && (
              <span className="job-torrent-peers">
                Peers: {job.torrentInfo.seeders} S / {job.torrentInfo.leechers} L
              </span>
            )}
          </div>
        </>
      )}

      {/* Seeding state */}
      {isSeeding && (
        <>
          <div className="progress-bar-container">
            <div className="progress-bar-fill progress-seeding" style={{ width: '100%' }} />
          </div>
          <div className="job-details">
            <span className="job-progress">100% (Seeding)</span>
            <span className="job-size">{formatBytes(job.totalBytes)}</span>
            {job.torrentInfo && (
              <>
                <span className="job-speed">↑ {formatSpeed(job.torrentInfo.uploadSpeed)}</span>
                <span className="job-torrent-peers">
                  Peers: {job.torrentInfo.seeders} S / {job.torrentInfo.leechers} L
                </span>
                <span className="job-torrent-ratio">
                  Ratio: {job.torrentInfo.ratio.toFixed(2)} (↑ {formatBytes(job.torrentInfo.uploaded)})
                </span>
              </>
            )}
          </div>
        </>
      )}

      {/* Completed info */}
      {isCompleted && (
        <div className="job-details">
          <span className="job-size">{formatBytes(job.totalBytes > 0 ? job.totalBytes : job.completedBytes)}</span>
          <span className="job-progress">100%</span>
        </div>
      )}

      {/* Destination & Final Path display */}
      {(job.finalPath || job.destinationDir) && (
        <div className="job-storage-path" style={{ fontSize: '0.75rem', opacity: 0.7, marginTop: '0.4rem', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }} title={job.finalPath || job.destinationDir}>
          📁 {job.finalPath ? `Saved to: ${job.finalPath}` : `Dest: ${job.destinationDir}`}
        </div>
      )}

      {/* Error display */}
      {isFailed && job.error && (
        <div className="job-error">{job.error}</div>
      )}

      {/* Actions */}
      <div className="job-actions">
        {isSeeding && onStopSeeding && (
          <button className="btn btn-danger btn-sm" onClick={() => onStopSeeding(job.id)}>
            🛑 Stop Seeding
          </button>
        )}
        {isDownloading && onPause && job.type !== 'media' && (
          <button className="btn btn-secondary btn-sm" onClick={() => onPause(job.id)}>
            ⏸ Pause
          </button>
        )}
        {isPaused && onResume && (
          <button className="btn btn-primary btn-sm" onClick={() => onResume(job.id)}>
            ▶ Resume
          </button>
        )}
        {(isActive || isPaused || isAnalyzing || isProcessing || isAwaitingSelection) && !isSeeding && onCancel && (
          <button className="btn btn-danger btn-sm" onClick={() => onCancel(job.id)}>
            ✕ Cancel
          </button>
        )}
        {isFailed && onRetry && (
          <button className="btn btn-primary btn-sm" onClick={() => onRetry(job.id)}>
            ↻ Retry
          </button>
        )}
        {(isCompleted || isSeeding) && onOpenFolder && (
          <button className="btn btn-secondary btn-sm" onClick={onOpenFolder}>
            📂 Open Folder
          </button>
        )}
        {(isCompleted || isFailed || job.status === 'cancelled') && (
          <span className="job-time">
            {new Date(job.createdAt).toLocaleString()}
          </span>
        )}
      </div>
    </div>
  );
};

