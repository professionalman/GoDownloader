import type { Job, QueuedJob } from '../types';
import { JobCard } from './JobCard';

interface JobListProps {
  jobs: Job[];
  selectedIds: Set<string>;
  queueByJobId: ReadonlyMap<string, QueuedJob>;
  onToggleSelect: (id: string) => void;
  onCancel: (id: string) => void;
  onPause: (id: string) => void;
  onResume: (id: string) => void;
  onRetry: (id: string) => void;
  onOpenFolder: () => void;
  onSelectFormat: (id: string) => void;
  onSelectTorrentFiles: (id: string) => void;
  onStopSeeding: (id: string) => void;
  onJobUpdated: (job: Job) => void;
}

export function JobList(props: JobListProps) {
  return (
    <ul className="space-y-1.5">
      {props.jobs.map((job) => (
        <JobCard
          key={job.id}
          job={job}
          queueEntry={props.queueByJobId.get(job.id)}
          selected={props.selectedIds.has(job.id)}
          onToggleSelect={props.onToggleSelect}
          onCancel={props.onCancel}
          onPause={props.onPause}
          onResume={props.onResume}
          onRetry={props.onRetry}
          onOpenFolder={props.onOpenFolder}
          onSelectFormat={props.onSelectFormat}
          onSelectTorrentFiles={props.onSelectTorrentFiles}
          onStopSeeding={props.onStopSeeding}
          onJobUpdated={props.onJobUpdated}
        />
      ))}
    </ul>
  );
}
