import React from 'react';
import type { Job } from '../types';
import { JobCard } from './JobCard';

interface JobListProps {
  jobs: Job[];
  selectedIds?: Set<string>;
  onToggleSelect?: (id: string) => void;
  onSelectAll?: (ids: string[]) => void;
  onCancel: (id: string) => void;
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onRetry: (id: string) => void;
  onOpenFolder: () => void;
  onSelectFormat: (id: string) => void;
  onSelectTorrentFiles?: (id: string) => void;
  onStopSeeding?: (id: string) => void;
}

export const JobList: React.FC<JobListProps> = ({
  jobs,
  selectedIds = new Set(),
  onToggleSelect,
  onSelectAll,
  onCancel,
  onPause,
  onResume,
  onRetry,
  onOpenFolder,
  onSelectFormat,
  onSelectTorrentFiles,
  onStopSeeding,
}) => {
  const activeJobs = jobs.filter(
    (j) => j.status === 'downloading' || j.status === 'queued' || j.status === 'paused'
      || j.status === 'analyzing' || j.status === 'processing' || j.status === 'awaiting_selection' || j.status === 'seeding'
  );
  const completedJobs = jobs.filter(
    (j) => j.status === 'completed' || j.status === 'failed' || j.status === 'cancelled'
  );

  const allActiveSelected = activeJobs.length > 0 && activeJobs.every(j => selectedIds.has(j.id));

  return (
    <div className="job-list">
      <section className="job-section">
        <div className="section-header">
          <h2>Active Downloads ({activeJobs.length})</h2>
          {activeJobs.length > 0 && onSelectAll && (
            <button
              type="button"
              className="btn btn-sm btn-link"
              onClick={() => onSelectAll(allActiveSelected ? [] : activeJobs.map(j => j.id))}
            >
              {allActiveSelected ? 'Deselect All Active' : 'Select All Active'}
            </button>
          )}
        </div>
        {activeJobs.length === 0 ? (
          <p className="empty-message">No active downloads</p>
        ) : (
          activeJobs.map((job) => (
            <JobCard
              key={job.id}
              job={job}
              selected={selectedIds.has(job.id)}
              onToggleSelect={onToggleSelect}
              onCancel={onCancel}
              onPause={onPause}
              onResume={onResume}
              onSelectFormat={onSelectFormat}
              onSelectTorrentFiles={onSelectTorrentFiles}
              onStopSeeding={onStopSeeding}
              onOpenFolder={onOpenFolder}
            />
          ))
        )}
      </section>

      <section className="job-section">
        <h2>History ({completedJobs.length})</h2>
        {completedJobs.length === 0 ? (
          <p className="empty-message">No completed downloads</p>
        ) : (
          completedJobs.map((job) => (
            <JobCard
              key={job.id}
              job={job}
              selected={selectedIds.has(job.id)}
              onToggleSelect={onToggleSelect}
              onRetry={onRetry}
              onOpenFolder={onOpenFolder}
            />
          ))
        )}
      </section>
    </div>
  );
};

