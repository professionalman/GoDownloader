import { Film, FolderOpen, Magnet, Pause, Play, RotateCcw, Square } from 'lucide-react';
import type { Job, JobCapabilities } from '../../types';

interface JobPrimaryActionProps {
  job: Job;
  capabilities: JobCapabilities | null;
  pendingAction: boolean;
  onPause?: (id: string) => void;
  onResume?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
  onSelectFormat?: (id: string) => void;
  onSelectTorrentFiles?: (id: string) => void;
  onStopSeeding?: (id: string) => void;
  onAction: (actionFn?: (id: string) => void) => void;
}

export function JobPrimaryAction({
  job,
  capabilities,
  pendingAction,
  onPause,
  onResume,
  onRetry,
  onOpenFolder,
  onSelectFormat,
  onSelectTorrentFiles,
  onStopSeeding,
  onAction,
}: JobPrimaryActionProps) {
  const isDownloading = job.status === 'downloading';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isCancelled = job.status === 'cancelled';
  const isAnalyzing = job.status === 'analyzing';
  const isAwaitingSelection = job.status === 'awaiting_selection';
  const isSeeding = job.status === 'seeding';
  const isMediaJob = job.type === 'media';
  const isTorrentJob = job.type === 'torrent';

  const mediaInfo = job.mediaInfo;
  const analysisReady =
    isAnalyzing && mediaInfo && mediaInfo.formats && mediaInfo.formats.length > 0;

  if (isDownloading) {
    const canPause =
      capabilities?.pause.supported && capabilities?.pause.mutableNow;
    const fallbackAllow = !capabilities && !isMediaJob && onPause;

    if ((canPause || fallbackAllow) && onPause) {
      return (
        <button
          type="button"
          className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-xs font-medium text-foreground hover:bg-surface-2 disabled:opacity-50"
          disabled={pendingAction}
          onClick={() => onAction(onPause)}
        >
          <Pause className="size-3.5" aria-hidden="true" />
          Pause
        </button>
      );
    }
  }

  if (isPaused && onResume) {
    const canResume = capabilities?.resume.mutableNow ?? true;
    if (canResume) {
      return (
        <button
          type="button"
          className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={pendingAction}
          onClick={() => onAction(onResume)}
        >
          <Play className="size-3.5" aria-hidden="true" />
          Resume
        </button>
      );
    }
  }

  if ((isAwaitingSelection || analysisReady) && isMediaJob && onSelectFormat) {
    return (
      <button
        type="button"
        className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
        onClick={() => onSelectFormat(job.id)}
      >
        <Film className="size-3.5" aria-hidden="true" />
        Select format
      </button>
    );
  }

  if (isAwaitingSelection && isTorrentJob && onSelectTorrentFiles) {
    return (
      <button
        type="button"
        className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90"
        onClick={() => onSelectTorrentFiles(job.id)}
      >
        <Magnet className="size-3.5" aria-hidden="true" />
        Select files
      </button>
    );
  }

  if ((isFailed || isCancelled) && onRetry) {
    const canRetry = capabilities?.retry.mutableNow ?? true;
    if (canRetry) {
      return (
        <button
          type="button"
          className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
          disabled={pendingAction}
          onClick={() => onAction(onRetry)}
        >
          <RotateCcw className="size-3.5" aria-hidden="true" />
          Retry
        </button>
      );
    }
  }

  if (isSeeding && onStopSeeding) {
    return (
      <button
        type="button"
        className="flex h-8 items-center gap-1.5 rounded-md border border-destructive/40 bg-destructive/10 px-2.5 text-xs font-medium text-destructive hover:bg-destructive/20 disabled:opacity-50"
        disabled={pendingAction}
        onClick={() => onAction(onStopSeeding)}
      >
        <Square className="size-3.5" aria-hidden="true" />
        Stop seeding
      </button>
    );
  }

  if (isCompleted && onOpenFolder) {
    return (
      <button
        type="button"
        className="grid size-9 place-items-center rounded-lg border border-border bg-surface-2 text-muted-foreground transition hover:border-border-strong hover:text-foreground"
        aria-label="Open downloads folder"
        title="Open downloads folder"
        onClick={onOpenFolder}
      >
        <FolderOpen className="size-4" aria-hidden="true" />
      </button>
    );
  }

  return null;
}
