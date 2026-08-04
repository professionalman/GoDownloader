import { Pause, Play, RotateCcw, X } from 'lucide-react';
import type { Job } from '../types';

interface BulkActionBarProps {
  jobs: Job[];
  selectedIds: Set<string>;
  onAction: (action: 'pause' | 'resume' | 'cancel' | 'retry') => void;
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
  const canPause = selectedJobs.filter(
    (job) => job.status === 'downloading' && job.type !== 'media',
  ).length;
  const canResume = selectedJobs.filter(
    (job) => job.status === 'paused',
  ).length;
  const canRetry = selectedJobs.filter(
    (job) => job.status === 'failed' || job.status === 'cancelled',
  ).length;

  return (
    <div className="sticky bottom-3 z-30 mt-3">
      <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg border border-primary/35 bg-surface-2/95 p-3 shadow-lg backdrop-blur sm:flex sm:flex-wrap sm:justify-between">
        <div className="min-w-0">
          <p className="num text-sm font-medium">
            {selectedIds.size} selected
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {canPause} can pause · {canResume} can resume · {canRetry} can retry
          </p>
        </div>

        <div className="col-span-2 flex flex-wrap items-center gap-2">
          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={canPause === 0}
            onClick={() => onAction('pause')}
          >
            <Pause className="size-3.5" aria-hidden="true" />
            Pause
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={canResume === 0}
            onClick={() => onAction('resume')}
          >
            <Play className="size-3.5" aria-hidden="true" />
            Resume
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-sm text-foreground hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
            disabled={canRetry === 0}
            onClick={() => onAction('retry')}
          >
            <RotateCcw className="size-3.5" aria-hidden="true" />
            Retry
          </button>

          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md border border-destructive/45 bg-destructive/10 px-2.5 text-sm text-destructive hover:bg-destructive/20 disabled:opacity-50"
            onClick={() => onAction('cancel')}
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
