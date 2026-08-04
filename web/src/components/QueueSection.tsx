import {
  ArrowDown,
  ArrowUp,
  CirclePause,
  Gauge,
  Pause,
  Play,
  Timer,
  X,
} from 'lucide-react';
import type { JobPriority, QueuedJob, QueueSnapshot } from '../types';
import { cx, jobStatusLabel, priorityLabel, statusToneClass } from '../downloadUi';

interface QueueSectionProps {
  snapshot: QueueSnapshot | null;
  onSetPriority: (jobId: string, priority: JobPriority) => void;
  onReorder: (priority: JobPriority, jobIds: string[]) => void;
  onPause: (jobId: string) => void;
  onResume: (jobId: string) => void;
  onCancel: (jobId: string) => void;
}

const lanes: Array<{ priority: JobPriority; label: string }> = [
  { priority: 'high', label: 'High Priority' },
  { priority: 'normal', label: 'Normal Priority' },
  { priority: 'low', label: 'Low Priority' },
];

function SummaryCard({
  label,
  value,
  icon: Icon,
  tone,
}: {
  label: string;
  value: string | number;
  icon: typeof Gauge;
  tone?: string;
}) {
  return (
    <div className="rounded-lg border border-border bg-surface px-3 py-2.5">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Icon className="size-3.5" aria-hidden="true" />
        <span>{label}</span>
      </div>
      <p className={cx('num mt-1 text-lg font-semibold tracking-tight', tone)}>{value}</p>
    </div>
  );
}

export function QueueSection({
  snapshot,
  onSetPriority,
  onReorder,
  onPause,
  onResume,
  onCancel,
}: QueueSectionProps) {
  if (!snapshot) {
    return (
      <div className="grid min-h-48 place-items-center rounded-lg border border-dashed border-border bg-surface/50 text-sm text-muted-foreground">
        Loading queue…
      </div>
    );
  }

  const handleMove = (
    priority: JobPriority,
    items: QueuedJob[],
    index: number,
    direction: -1 | 1,
  ) => {
    const target = index + direction;
    if (target < 0 || target >= items.length) return;
    const reordered = [...items];
    [reordered[index], reordered[target]] = [reordered[target], reordered[index]];
    onReorder(priority, reordered.map((item) => item.jobId));
  };

  const availableSlots = Math.max(
    0,
    snapshot.maxConcurrentDownloads - snapshot.runningDownloads,
  );

  return (
    <div className="space-y-4">
      <section className="grid grid-cols-2 gap-2 sm:grid-cols-4" aria-label="Queue summary">
        <SummaryCard label="Running" value={snapshot.runningDownloads} icon={Gauge} tone="text-primary" />
        <SummaryCard label="Available slots" value={availableSlots} icon={Play} tone="text-success" />
        <SummaryCard label="Waiting" value={snapshot.queuedDownloads} icon={Timer} />
        <SummaryCard label="Paused" value={snapshot.pausedDownloads} icon={CirclePause} tone="text-muted-foreground" />
      </section>

      <div className="grid gap-3 xl:grid-cols-3">
        {lanes.map(({ priority, label }) => {
          const laneItems = snapshot.items.filter(
            (item) => (item.job?.priority ?? 'normal') === priority,
          );

          return (
            <section
              key={priority}
              className="min-w-0 rounded-lg border border-border bg-surface p-3"
              aria-label={label}
            >
              <div className="flex items-center justify-between gap-2 pb-2">
                <div>
                  <h2 className="text-sm font-semibold tracking-tight">{label}</h2>
                  <p className="text-xs text-muted-foreground">{priorityLabel(priority)} lane</p>
                </div>
                <span className="num rounded-md border border-border bg-surface-2 px-1.5 py-0.5 text-xs text-muted-foreground">
                  {laneItems.length}
                </span>
              </div>

              {laneItems.length === 0 ? (
                <p className="rounded-lg border border-dashed border-border px-3 py-7 text-center text-xs text-muted-foreground">
                  No jobs waiting in this lane
                </p>
              ) : (
                <ul className="space-y-2">
                  {laneItems.map((item, index) => {
                    const job = item.job;
                    const name = job?.name || item.jobId;
                    return (
                      <li key={item.jobId} className="rounded-lg border border-border bg-surface-2/70 p-3">
                        <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2.5">
                          <span className="num grid size-7 place-items-center rounded-md border border-border bg-surface text-xs text-muted-foreground">
                            {item.position || index + 1}
                          </span>
                          <div className="min-w-0">
                            <p className="truncate text-sm font-medium" title={name}>{name}</p>
                            <div className="mt-1 flex flex-wrap items-center gap-1.5 text-xs">
                              {job && (
                                <span className={cx('inline-flex rounded-md border px-1.5 py-0.5', statusToneClass(job.status))}>
                                  {jobStatusLabel(job)}
                                </span>
                              )}
                              <span className="text-muted-foreground">
                                {item.action === 'start' ? 'Fresh start' : 'Resume'}
                              </span>
                            </div>
                            <p className="mt-1 text-xs text-muted-foreground">
                              {item.waitingReason === 'paused_by_user'
                                ? 'Paused by user'
                                : item.waitingReason || 'Waiting for an available download slot'}
                            </p>

                            <div className="mt-3 flex flex-wrap gap-1.5">
                              <select
                                aria-label={`Priority for ${name}`}
                                className="h-8 rounded-md border border-border bg-surface px-2 text-xs text-foreground"
                                value={priority}
                                onChange={(event) => onSetPriority(item.jobId, event.target.value as JobPriority)}
                              >
                                <option value="high">High</option>
                                <option value="normal">Normal</option>
                                <option value="low">Low</option>
                              </select>
                              <button
                                type="button"
                                className="grid size-8 place-items-center rounded-md border border-border bg-surface text-muted-foreground hover:text-foreground disabled:opacity-35"
                                disabled={index === 0}
                                onClick={() => handleMove(priority, laneItems, index, -1)}
                                aria-label={`Move ${name} up`}
                              >
                                <ArrowUp className="size-3.5" />
                              </button>
                              <button
                                type="button"
                                className="grid size-8 place-items-center rounded-md border border-border bg-surface text-muted-foreground hover:text-foreground disabled:opacity-35"
                                disabled={index === laneItems.length - 1}
                                onClick={() => handleMove(priority, laneItems, index, 1)}
                                aria-label={`Move ${name} down`}
                              >
                                <ArrowDown className="size-3.5" />
                              </button>
                              <button
                                type="button"
                                className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-xs hover:bg-surface-2"
                                onClick={() => job?.status === 'paused' ? onResume(item.jobId) : onPause(item.jobId)}
                              >
                                {job?.status === 'paused' ? <Play className="size-3.5" /> : <Pause className="size-3.5" />}
                                {job?.status === 'paused' ? 'Resume' : 'Pause'}
                              </button>
                              <button
                                type="button"
                                className="inline-flex h-8 items-center gap-1.5 rounded-md border border-destructive/45 bg-destructive/10 px-2.5 text-xs text-destructive hover:bg-destructive/20"
                                onClick={() => onCancel(item.jobId)}
                              >
                                <X className="size-3.5" /> Cancel
                              </button>
                            </div>
                          </div>
                        </div>
                      </li>
                    );
                  })}
                </ul>
              )}
            </section>
          );
        })}
      </div>
    </div>
  );
}
