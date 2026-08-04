import { useState, useMemo } from 'react';
import { Search, X } from 'lucide-react';
import type { Job, QueueSnapshot } from '../types';
import type { DownloadFilter } from '../downloadUi';
import {
  DOWNLOAD_FILTERS,
  ACTIVE_JOB_STATUSES,
  cx,
  formatSpeed,
  jobMatchesFilter,
  jobMatchesQuery,
  queueEntryMap,
} from '../downloadUi';
import { JobList } from './JobList';
import { BulkActionBar } from './BulkActionBar';

interface DownloadsPanelProps {
  jobs: Job[];
  initialLoading: boolean;
  selectedIds: Set<string>;
  queueSnapshot: QueueSnapshot | null;
  onToggleSelect: (id: string) => void;
  onSelectVisible: (ids: string[]) => void;
  onDeselectVisible?: (ids: string[]) => void;
  onBulkAction: (action: 'pause' | 'resume' | 'cancel' | 'retry') => void;
  onClearSelection: () => void;
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

function Meter({
  value,
  label,
  dotClassName,
}: {
  value: number;
  label: string;
  dotClassName: string;
}) {
  return (
    <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className={cx('size-2 rounded-full', dotClassName)} />
      <span className="num font-medium text-foreground">{value}</span>
      <span>{label}</span>
    </div>
  );
}

export function DownloadsPanel(props: DownloadsPanelProps) {
  const [filter, setFilter] = useState<DownloadFilter>('Active');
  const [query, setQuery] = useState('');

  const counts = useMemo(
    () => ({
      Active: props.jobs.filter((job) =>
        ACTIVE_JOB_STATUSES.includes(job.status)
      ).length,
      Completed: props.jobs.filter((job) => job.status === 'completed').length,
      Failed: props.jobs.filter((job) => job.status === 'failed').length,
      Cancelled: props.jobs.filter((job) => job.status === 'cancelled').length,
      All: props.jobs.length,
    }),
    [props.jobs]
  );

  const downloadingJobs = useMemo(
    () => props.jobs.filter((job) => job.status === 'downloading'),
    [props.jobs]
  );

  const totalSpeed = downloadingJobs.reduce(
    (sum, job) => sum + (job.speedBytesPerSecond || 0),
    0
  );

  const queuedCount =
    props.queueSnapshot?.queuedDownloads ??
    props.jobs.filter((job) => job.status === 'queued').length;

  const visibleJobs = useMemo(
    () =>
      props.jobs.filter(
        (job) =>
          jobMatchesFilter(job, filter) && jobMatchesQuery(job, query)
      ),
    [props.jobs, filter, query]
  );

  const queueByJobId = useMemo(
    () => queueEntryMap(props.queueSnapshot?.items ?? []),
    [props.queueSnapshot]
  );

  const allVisibleSelected =
    visibleJobs.length > 0 &&
    visibleJobs.every((job) => props.selectedIds.has(job.id));

  const getEmptyStateMessage = () => {
    if (query.trim()) {
      return `No downloads match "${query.trim()}"`;
    }
    switch (filter) {
      case 'Active':
        return 'No active downloads';
      case 'Completed':
        return 'No completed downloads';
      case 'Failed':
        return 'No failed downloads';
      case 'Cancelled':
        return 'No cancelled downloads';
      default:
        return 'No downloads found';
    }
  };

  return (
    <div className="space-y-2.5">
      {/* Transfer summary card */}
      <div
        className="flex min-h-9 flex-wrap items-center gap-x-4 gap-y-1.5 rounded-lg border border-border bg-surface px-3 py-2"
        aria-label="Transfer status"
        aria-live="polite"
      >
        <Meter
          value={downloadingJobs.length}
          label={`downloading · ${formatSpeed(totalSpeed)}`}
          dotClassName="bg-primary"
        />

        <Meter
          value={queuedCount}
          label="queued"
          dotClassName="bg-muted-foreground"
        />

        {counts.Failed > 0 && (
          <Meter
            value={counts.Failed}
            label="failed"
            dotClassName="bg-destructive"
          />
        )}

        <span className="ml-auto text-xs text-muted-foreground">
          <span className="num font-medium text-foreground">{counts.Completed}</span>{' '}
          completed
        </span>
      </div>

      {/* Filters and search toolbar */}
      <div className="grid gap-2.5 xl:grid-cols-[minmax(0,1fr)_280px] xl:items-center">
        <div
          className="scrollbar-thin flex flex-wrap gap-1.5 sm:flex-nowrap sm:overflow-x-auto sm:pb-1"
          role="group"
          aria-label="Filter downloads"
        >
          {DOWNLOAD_FILTERS.map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => setFilter(option)}
              aria-pressed={filter === option}
              className={cx(
                'flex h-8 shrink-0 items-center gap-2 rounded-md border px-3 text-sm transition-colors',
                filter === option
                  ? 'border-primary/40 bg-primary/15 font-medium text-foreground'
                  : 'border-border bg-surface text-muted-foreground hover:text-foreground'
              )}
            >
              {option}
              <span className="num text-xs text-muted-foreground">
                {counts[option]}
              </span>
            </button>
          ))}
        </div>

        <div className="relative">
          <label htmlFor="search-downloads" className="sr-only">
            Search downloads
          </label>
          <Search
            className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground"
            aria-hidden="true"
          />
          <input
            id="search-downloads"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search downloads"
            className="h-8 w-full rounded-md border border-border bg-surface pl-9 pr-8 text-sm text-foreground outline-none transition focus:border-primary focus:ring-2 focus:ring-primary/20"
          />
          {query && (
            <button
              type="button"
              className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              onClick={() => setQuery('')}
              aria-label="Clear search"
            >
              <X className="size-4" />
            </button>
          )}
        </div>
      </div>

      {/* Master selection header bar */}
      {visibleJobs.length > 0 && (
        <div className="flex items-center justify-between px-1 text-xs text-muted-foreground">
          <label className="flex items-center gap-2 cursor-pointer select-none font-medium hover:text-foreground">
            <input
              type="checkbox"
              checked={allVisibleSelected}
              onChange={() => {
                const visibleIds = visibleJobs.map((job) => job.id);
                if (allVisibleSelected) {
                  if (props.onDeselectVisible) {
                    props.onDeselectVisible(visibleIds);
                  } else {
                    props.onSelectVisible([]);
                  }
                } else {
                  props.onSelectVisible(visibleIds);
                }
              }}
              className="size-4 rounded border-border text-primary focus:ring-primary/20"
            />
            <span>{allVisibleSelected ? 'Deselect visible' : 'Select visible'}</span>
          </label>
          <span>
            Showing <span className="num text-foreground font-medium">{visibleJobs.length}</span> of{' '}
            <span className="num text-foreground font-medium">{props.jobs.length}</span>
          </span>
        </div>
      )}

      {/* Loading Skeletons */}
      {props.initialLoading ? (
        <div className="space-y-2.5" data-testid="downloads-loading-skeleton">
          {[1, 2, 3, 4, 5].map((key) => (
            <div
              key={key}
              className="h-22 animate-pulse rounded-xl border border-border bg-surface-2/40 p-4"
            />
          ))}
        </div>
      ) : visibleJobs.length > 0 ? (
        <JobList
          jobs={visibleJobs}
          selectedIds={props.selectedIds}
          queueByJobId={queueByJobId}
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
      ) : (
        <div className="flex flex-col items-center justify-center rounded-lg border border-dashed border-border bg-surface/50 py-12 text-center">
          <p className="text-sm font-medium text-muted-foreground">
            {getEmptyStateMessage()}
          </p>
          {query.trim() && (
            <button
              type="button"
              className="mt-2 text-xs text-primary hover:underline"
              onClick={() => setQuery('')}
            >
              Clear search filter
            </button>
          )}
        </div>
      )}

      {/* Bulk Action Floating Bar */}
      <BulkActionBar
        jobs={props.jobs}
        selectedIds={props.selectedIds}
        onAction={props.onBulkAction}
        onClear={props.onClearSelection}
      />
    </div>
  );
}
