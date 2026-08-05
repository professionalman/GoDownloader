import { Pause, Play, RotateCcw, X } from 'lucide-react';
import type { Job, BulkAction } from '../types';

interface BulkActionBarProps {
  jobs: Job[];
  selectedIds: Set<string>;
  onAction: (action: BulkAction, eligibleIds: string[]) => void;
  onClear: () => void;
}

export function BulkActionBar({
  jobs,
  selectedIds,
  onAction,
  onClear,
}: BulkActionBarProps) {
  if (selectedIds.size === 0) return null;

  const selectedJobs = jobs.filter((job) => selectedIds.has(job.id));
  const pausableIds = selectedJobs
    .filter((job) => job.status === 'downloading' && job.type !== 'media')
    .map((job) => job.id);
  const resumableIds = selectedJobs
    .filter((job) => job.status === 'paused')
    .map((job) => job.id);
  const retryableIds = selectedJobs
    .filter((job) => job.status === 'failed' || job.status === 'cancelled')
    .map((job) => job.id);
  const cancellableIds = selectedJobs
    .filter((job) =>
      ['downloading', 'paused', 'queued', 'analyzing', 'awaiting_selection', 'processing'].includes(job.status)
    )
    .map((job) => job.id);

  return (
    <div className="sticky bottom-4 z-30 mt-4">
      <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-primary/35 bg-surface-2/95 p-4 shadow-xl backdrop-blur">
        <div className="min-w-0">
          <p className="num text-sm font-medium">
            {selectedIds.size} selected
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {pausableIds.length} can pause · {resumableIds.length} can resume · {retryableIds.length} can retry
          </p>
        </div>

        <div className="col-span-2 flex flex-wrap items-center gap-2">
          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={pausableIds.length === 0}
            onClick={() => onAction('pause', pausableIds)}
          >
            <Pause className="size-3.5" aria-hidden="true" />
            Pause
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={resumableIds.length === 0}
            onClick={() => onAction('resume', resumableIds)}
          >
            <Play className="size-3.5" aria-hidden="true" />
            Resume
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={retryableIds.length === 0}
            onClick={() => onAction('retry', retryableIds)}
          >
            <RotateCcw className="size-3.5" aria-hidden="true" />
            Retry
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-destructive/45 bg-destructive/10 px-2.5 text-sm text-destructive hover:bg-destructive/20 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={cancellableIds.length === 0}
            onClick={() => onAction('cancel', cancellableIds)}
          >
            <X className="size-3.5" aria-hidden="true" />
            Cancel
          </button>

          <span className="mx-1 hidden h-5 w-px bg-border sm:block" aria-hidden="true" />

          <button
            type="button"
            className="h-8 rounded-md px-2.5 text-sm text-muted-foreground hover:bg-surface hover:text-foreground"
            onClick={onClear}
          >
            Clear
          </button>
        </div>
      </div>
    </div>
  );
}
