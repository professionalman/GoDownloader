import React from 'react';
import type { Job } from '../types';

interface JobCardProps {
  job: Job;
  onCancel?: (id: string) => void;
  onPause?: (id: string) => void;
  onResume?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
  onSelectFormat?: (id: string) => void;
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

export const JobCard: React.FC<JobCardProps> = ({ job, onCancel, onPause, onResume, onRetry, onOpenFolder, onSelectFormat }) => {
  const isDownloading = job.status === 'downloading';
  const isQueued = job.status === 'queued';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isAnalyzing = job.status === 'analyzing';
  const isProcessing = job.status === 'processing';
  const isActive = isDownloading || isQueued;
  const isMediaJob = job.type === 'media';

  // Analysis is complete when mediaInfo has formats
  const analysisReady = isAnalyzing && job.mediaInfo && job.mediaInfo.formats && job.mediaInfo.formats.length > 0;

  return (
    <div className={`job-card job-${job.status}`}>
      <div className="job-header">
        <div className="job-name-area">
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
          {isMediaJob && <span className="job-engine-badge">🎬 Media</span>}
          <span className={`job-status status-${job.status}`}>{job.status}</span>
        </div>
      </div>

      {/* Analyzing state */}
      {isAnalyzing && !analysisReady && (
        <div className="job-analyzing">
          <span className="analyzing-spinner">⟳</span>
          <span>Analyzing media...</span>
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

      {/* Progress bar for downloading, queued, and paused jobs */}
      {(isActive || isPaused) && (
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

      {/* Error display */}
      {isFailed && job.error && (
        <div className="job-error">{job.error}</div>
      )}

      {/* Actions */}
      <div className="job-actions">
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
        {(isActive || isPaused || isAnalyzing || isProcessing) && onCancel && (
          <button className="btn btn-danger btn-sm" onClick={() => onCancel(job.id)}>
            ✕ Cancel
          </button>
        )}
        {isFailed && onRetry && (
          <button className="btn btn-primary btn-sm" onClick={() => onRetry(job.id)}>
            ↻ Retry
          </button>
        )}
        {isCompleted && onOpenFolder && (
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
