import React from 'react';
import type { Job } from '../types';
import { JobCard } from './JobCard';

interface JobListProps {
  jobs: Job[];
  onCancel: (id: string) => void;
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onRetry: (id: string) => void;
  onOpenFolder: () => void;
  onSelectFormat: (id: string) => void;
  onSelectTorrentFiles?: (id: string) => void;
  onStopSeeding?: (id: string) => void;
}

export const JobList: React.FC<JobListProps> = ({ jobs, onCancel, onPause, onResume, onRetry, onOpenFolder, onSelectFormat, onSelectTorrentFiles, onStopSeeding }) => {
  const activeJobs = jobs.filter(
    (j) => j.status === 'downloading' || j.status === 'queued' || j.status === 'paused'
      || j.status === 'analyzing' || j.status === 'processing' || j.status === 'awaiting_selection' || j.status === 'seeding'
  );
  const completedJobs = jobs.filter(
    (j) => j.status === 'completed' || j.status === 'failed' || j.status === 'cancelled'
  );

  return (
    <div className="job-list">
      <section className="job-section">
        <h2>Active Downloads</h2>
        {activeJobs.length === 0 ? (
          <p className="empty-message">No active downloads</p>
        ) : (
          activeJobs.map((job) => (
            <JobCard
              key={job.id}
              job={job}
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
        <h2>History</h2>
        {completedJobs.length === 0 ? (
          <p className="empty-message">No completed downloads</p>
        ) : (
          completedJobs.map((job) => (
            <JobCard
              key={job.id}
              job={job}
              onRetry={onRetry}
              onOpenFolder={onOpenFolder}
            />
          ))
        )}
      </section>
    </div>
  );
};

