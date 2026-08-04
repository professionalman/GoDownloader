import { useState } from 'react';
import { ChevronDown, ChevronUp } from 'lucide-react';
import type { Job, QueuedJob } from '../../types';
import { useJobCapabilities } from '../../hooks/useJobCapabilities';
import { cx } from '../../downloadUi';
import { JobCardHeader, JobCardMeta } from './JobCardHeader';
import { JobPrimaryAction } from './JobPrimaryAction';
import { JobActionsMenu } from './JobActionsMenu';
import { JobProgress } from './JobProgress';
import { JobQueueStatus } from './JobQueueStatus';
import { JobSeedingStatus } from './JobSeedingStatus';
import { JobDetails } from './JobDetails';

export interface JobCardProps {
  job: Job;
  queueEntry?: QueuedJob;
  selected: boolean;
  onToggleSelect?: (id: string) => void;
  onCancel?: (id: string) => void;
  onPause?: (id: string) => void;
  onResume?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
  onSelectFormat?: (id: string) => void;
  onSelectTorrentFiles?: (id: string) => void;
  onStopSeeding?: (id: string) => void;
  onJobUpdated?: (job: Job) => void;
}

export function JobCard({
  job,
  queueEntry,
  selected,
  onToggleSelect,
  onCancel,
  onPause,
  onResume,
  onRetry,
  onOpenFolder,
  onSelectFormat,
  onSelectTorrentFiles,
  onStopSeeding,
  onJobUpdated,
}: JobCardProps) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const [pendingAction, setPendingAction] = useState(false);

  const { capabilities } = useJobCapabilities(job);

  const handleAction = async (actionFn?: (id: string) => void) => {
    if (!actionFn || pendingAction) return;
    setPendingAction(true);
    try {
      await actionFn(job.id);
    } finally {
      setPendingAction(false);
    }
  };

  return (
    <li
      className={cx(
        'rounded-lg border bg-surface p-3 transition-colors',
        selected ? 'border-primary/45 bg-primary/[0.06]' : 'border-border'
      )}
    >
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2.5">
        <JobCardHeader
          job={job}
          selected={selected}
          onToggleSelect={onToggleSelect}
        />

        <div className="min-w-0">
          <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-2.5">
            <JobCardMeta job={job} />

            <div className="flex shrink-0 items-center gap-2">
              <JobPrimaryAction
                job={job}
                capabilities={capabilities}
                pendingAction={pendingAction}
                onPause={onPause}
                onResume={onResume}
                onRetry={onRetry}
                onOpenFolder={onOpenFolder}
                onSelectFormat={onSelectFormat}
                onSelectTorrentFiles={onSelectTorrentFiles}
                onStopSeeding={onStopSeeding}
                onAction={handleAction}
              />

              <JobActionsMenu
                job={job}
                detailsOpen={detailsOpen}
                onToggleDetails={() => setDetailsOpen((prev) => !prev)}
                onCancel={onCancel}
                onRetry={onRetry}
                onOpenFolder={onOpenFolder}
                onAction={handleAction}
              />

              <button
                type="button"
                className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                onClick={() => setDetailsOpen((prev) => !prev)}
                aria-expanded={detailsOpen}
                aria-label={detailsOpen ? 'Hide details' : 'Show details'}
              >
                {detailsOpen ? (
                  <ChevronUp className="size-4" aria-hidden="true" />
                ) : (
                  <ChevronDown className="size-4" aria-hidden="true" />
                )}
              </button>
            </div>
          </div>

          <JobProgress job={job} />
          <JobQueueStatus job={job} queueEntry={queueEntry} />
          <JobSeedingStatus job={job} />

          {detailsOpen && (
            <JobDetails
              job={job}
              capabilities={capabilities}
              onJobUpdated={onJobUpdated}
            />
          )}
        </div>
      </div>
    </li>
  );
}
