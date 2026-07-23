import React from 'react';
import type { Job } from '../types';

interface JobCardProps {
  job: Job;
  onCancel?: (id: string) => void;
  onPause?: (id: string) => void;
  onResume?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
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

export const JobCard: React.FC<JobCardProps> = ({ job, onCancel, onPause, onResume, onRetry, onOpenFolder }) => {
  const isDownloading = job.status === 'downloading';
  const isQueued = job.status === 'queued';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isActive = isDownloading || isQueued;

  return (
    <div className={`job-card job-${job.status}`}>
      <div className="job-header">
        <span className="job-name" title={job.source}>{job.name || 'Untitled'}</span>
        <span className={`job-status status-${job.status}`}>{job.status}</span>
      </div>

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
        {isDownloading && onPause && (
          <button className="btn btn-secondary btn-sm" onClick={() => onPause(job.id)}>
            ⏸ Pause
          </button>
        )}
        {isPaused && onResume && (
          <button className="btn btn-primary btn-sm" onClick={() => onResume(job.id)}>
            ▶ Resume
          </button>
        )}
        {(isActive || isPaused) && onCancel && (
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
