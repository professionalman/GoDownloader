import { useState } from 'react';
import { Download, Film, Magnet } from 'lucide-react';
import type { Job } from '../../types';
import {
  cx,
  jobStatusLabel,
  statusToneClass,
  priorityLabel,
  priorityToneClass,
  engineLabel,
  sourceDomain,
} from '../../downloadUi';

interface JobCardHeaderProps {
  job: Job;
  selected: boolean;
  onToggleSelect?: (id: string) => void;
}

export function JobCardHeader({ job, selected, onToggleSelect }: JobCardHeaderProps) {
  const [thumbnailFailed, setThumbnailFailed] = useState(false);

  const isMediaJob = job.type === 'media';
  const isTorrentJob = job.type === 'torrent';
  const mediaInfo = job.mediaInfo;

  const EngineIcon = isMediaJob ? Film : isTorrentJob ? Magnet : Download;

  return (
    <div className="flex flex-col items-center gap-2 pt-0.5">
      {onToggleSelect && (
        <input
          type="checkbox"
          checked={selected}
          onChange={() => onToggleSelect(job.id)}
          aria-label={`Select ${job.name || 'Untitled'}`}
          className="size-4 accent-[var(--primary)]"
        />
      )}

      {isMediaJob && mediaInfo?.thumbnail && !thumbnailFailed ? (
        <img
          src={mediaInfo.thumbnail}
          alt=""
          loading="lazy"
          className="size-8 shrink-0 rounded-md border border-border object-cover"
          onError={() => setThumbnailFailed(true)}
        />
      ) : (
        <span className="grid size-8 shrink-0 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground">
          <EngineIcon className="size-4" aria-hidden="true" />
        </span>
      )}
    </div>
  );
}

export function JobCardMeta({ job }: { job: Job }) {
  return (
    <div className="min-w-0">
      <p className="truncate text-sm font-medium" title={job.source}>
        {job.name || 'Untitled'}
      </p>

      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
        <span
          className={cx(
            'inline-flex items-center rounded border px-1.5 py-0.5 font-medium',
            statusToneClass(job.status)
          )}
        >
          {jobStatusLabel(job)}
        </span>
        <span className="max-w-48 truncate">{sourceDomain(job.source)}</span>
        <span aria-hidden="true">·</span>
        <span>{engineLabel(job)}</span>
        <span aria-hidden="true">·</span>
        <span
          className={cx(
            'inline-flex items-center rounded border px-1.5 py-0.5 font-medium',
            priorityToneClass(job.priority)
          )}
        >
          {priorityLabel(job.priority)}
        </span>
      </div>
    </div>
  );
}
