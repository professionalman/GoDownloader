import type { Job, QueuedJob } from '../../types';
import { priorityLabel } from '../../downloadUi';

interface JobQueueStatusProps {
  job: Job;
  queueEntry?: QueuedJob;
}

export function JobQueueStatus({ job, queueEntry }: JobQueueStatusProps) {
  if (job.status !== 'queued') return null;

  return (
    <div className="mt-2.5 rounded border border-border bg-surface-2/50 px-2.5 py-1.5 text-xs text-muted-foreground">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        {job.priority && (
          <span className="font-medium text-foreground">
            {priorityLabel(job.priority)} Lane
          </span>
        )}
        {queueEntry?.position !== undefined && (
          <span>Position #{queueEntry.position}</span>
        )}
        {queueEntry?.waitingReason && (
          <span className="text-warning">· {queueEntry.waitingReason}</span>
        )}
      </div>
    </div>
  );
}
