import type {
  Job,
  JobPriority,
  JobStatus,
  MediaFormat,
  MediaInfo,
  QueuedJob,
} from './types';

export type DownloadFilter =
  | 'Active'
  | 'Completed'
  | 'Failed'
  | 'Cancelled'
  | 'All';

export const DOWNLOAD_FILTERS: readonly DownloadFilter[] = [
  'Active',
  'Completed',
  'Failed',
  'Cancelled',
  'All',
];

export const ACTIVE_JOB_STATUSES: readonly JobStatus[] = [
  'analyzing',
  'awaiting_selection',
  'queued',
  'downloading',
  'paused',
  'processing',
  'seeding',
];

export function cx(...values: Array<string | false | null | undefined>): string {
  return values.filter(Boolean).join(' ');
}

export function formatBytes(bytes?: number, digits = 1): string {
  if (!bytes || bytes <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  const value = bytes / 1024 ** index;
  return `${value.toFixed(index === 0 ? 0 : digits)} ${units[index]}`;
}

export function formatSpeed(bytesPerSecond?: number): string {
  if (!bytesPerSecond || bytesPerSecond <= 0) return '—';
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  const index = Math.min(
    Math.floor(Math.log(bytesPerSecond) / Math.log(1000)),
    units.length - 1,
  );
  const value = bytesPerSecond / 1000 ** index;
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function formatEta(seconds?: number): string {
  if (!seconds || seconds <= 0) return '—';
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const remainingSeconds = Math.floor(seconds % 60);

  if (hours > 0) return `${hours}h ${minutes}m`;
  if (minutes > 0) {
    return `${minutes}m ${remainingSeconds.toString().padStart(2, '0')}s`;
  }
  return `${remainingSeconds}s`;
}

export function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return '0m';
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  return hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
}

export function jobStatusLabel(job: Job): string {
  if (job.status === 'awaiting_selection') {
    return job.type === 'torrent'
      ? 'Awaiting file selection'
      : 'Awaiting format';
  }

  const labels: Record<Exclude<JobStatus, 'awaiting_selection'>, string> = {
    analyzing: 'Analyzing',
    queued: 'Queued',
    downloading: 'Downloading',
    paused: 'Paused',
    processing: 'Processing',
    seeding: 'Seeding',
    completed: 'Completed',
    failed: 'Failed',
    cancelled: 'Cancelled',
  };

  return labels[job.status];
}

export function statusToneClass(status: JobStatus): string {
  switch (status) {
    case 'downloading':
      return 'border-primary/40 bg-primary/15 text-primary';
    case 'analyzing':
    case 'processing':
      return 'border-info/35 bg-info/12 text-info';
    case 'awaiting_selection':
      return 'border-warning/35 bg-warning/12 text-warning';
    case 'seeding':
    case 'completed':
      return 'border-success/35 bg-success/12 text-success';
    case 'failed':
      return 'border-destructive/40 bg-destructive/12 text-destructive';
    default:
      return 'border-border-strong bg-surface-2 text-muted-foreground';
  }
}

export function priorityLabel(priority?: JobPriority): string {
  switch (priority) {
    case 'high':
      return 'High';
    case 'low':
      return 'Low';
    default:
      return 'Normal';
  }
}

export function priorityToneClass(priority?: JobPriority): string {
  switch (priority) {
    case 'high':
      return 'border-warning/35 bg-warning/10 text-warning';
    case 'low':
      return 'border-border bg-surface-2 text-muted-foreground';
    default:
      return 'border-border-strong bg-surface-2 text-muted-foreground';
  }
}

export function engineLabel(job: Job): string {
  switch (job.type) {
    case 'media':
      return 'Media · yt-dlp';
    case 'torrent':
      return 'Torrent · qBittorrent';
    default:
      return 'Direct · aria2';
  }
}

export function sourceDomain(source: string): string {
  const value = source.trim();
  if (value.toLowerCase().startsWith('magnet:')) return 'magnet';
  if (value.toLowerCase().endsWith('.torrent')) return 'torrent file';

  try {
    return new URL(value).hostname || value;
  } catch {
    return value || 'unknown source';
  }
}

export function jobMatchesFilter(job: Job, filter: DownloadFilter): boolean {
  switch (filter) {
    case 'Active':
      return ACTIVE_JOB_STATUSES.includes(job.status);
    case 'Completed':
      return job.status === 'completed';
    case 'Failed':
      return job.status === 'failed';
    case 'Cancelled':
      return job.status === 'cancelled';
    default:
      return true;
  }
}

export function jobMatchesQuery(job: Job, query: string): boolean {
  const needle = query.trim().toLowerCase();
  if (!needle) return true;

  return [
    job.name,
    job.source,
    sourceDomain(job.source),
    job.destinationDir,
    job.finalPath,
    job.engine,
    job.type,
    job.categoryId,
  ]
    .filter((value): value is string => Boolean(value))
    .some((value) => value.toLowerCase().includes(needle));
}

export interface MediaSizeEstimate {
  selected?: MediaFormat;
  bestAudio?: MediaFormat;
  videoBytes?: number;
  audioBytes?: number;
  totalBytes?: number;
  combinesSeparateAudio: boolean;
}

function hasCodec(codec?: string): boolean {
  return Boolean(codec && codec !== 'none');
}

export function getMediaSizeEstimate(mediaInfo?: MediaInfo): MediaSizeEstimate {
  if (!mediaInfo?.selectedFormat) {
    return { combinesSeparateAudio: false };
  }

  const selected = mediaInfo.formats.find(
    (format) => format.formatId === mediaInfo.selectedFormat,
  );

  if (!selected) {
    return { combinesSeparateAudio: false };
  }

  const hasVideo = hasCodec(selected.vcodec);
  const hasAudio = hasCodec(selected.acodec);
  const selectedBytes = selected.fileSize > 0 ? selected.fileSize : undefined;

  if (!hasVideo || hasAudio) {
    return {
      selected,
      totalBytes: selectedBytes,
      audioBytes: !hasVideo ? selectedBytes : undefined,
      combinesSeparateAudio: false,
    };
  }

  const bestAudio = mediaInfo.bestAudioFormat;
  const audioBytes =
    bestAudio && bestAudio.fileSize > 0 ? bestAudio.fileSize : undefined;

  return {
    selected,
    bestAudio,
    videoBytes: selectedBytes,
    audioBytes,
    totalBytes:
      selectedBytes !== undefined && audioBytes !== undefined
        ? selectedBytes + audioBytes
        : undefined,
    combinesSeparateAudio: true,
  };
}

export function queueEntryMap(items: readonly QueuedJob[]): Map<string, QueuedJob> {
  return new Map(items.map((item) => [item.jobId, item]));
}
