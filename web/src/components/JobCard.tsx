import { useState } from 'react';
import {
  Download,
  Film,
  Magnet,
  Pause,
  Play,
  RotateCcw,
  Square,
  FolderOpen,
  ChevronDown,
  ChevronUp,
  Loader2,
  Copy,
  Check,
  MoreVertical,
  Volume2,
} from 'lucide-react';
import type { Job, QueuedJob } from '../types';
import { useJobCapabilities } from '../hooks/useJobCapabilities';
import { JobPowerControls } from './JobPowerControls';
import {
  cx,
  formatBytes,
  formatSpeed,
  formatEta,
  jobStatusLabel,
  statusToneClass,
  priorityLabel,
  priorityToneClass,
  engineLabel,
  sourceDomain,
  getMediaSizeEstimate,
} from '../downloadUi';

interface JobCardProps {
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
  const [menuOpen, setMenuOpen] = useState(false);
  const [thumbnailFailed, setThumbnailFailed] = useState(false);
  const [copied, setCopied] = useState(false);
  const [pendingAction, setPendingAction] = useState(false);

  const { capabilities } = useJobCapabilities(job.id);

  const isDownloading = job.status === 'downloading';
  const isQueued = job.status === 'queued';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isCancelled = job.status === 'cancelled';
  const isAnalyzing = job.status === 'analyzing';
  const isProcessing = job.status === 'processing';
  const isAwaitingSelection = job.status === 'awaiting_selection';
  const isSeeding = job.status === 'seeding';
  const isMediaJob = job.type === 'media';
  const isTorrentJob = job.type === 'torrent';

  const mediaInfo = job.mediaInfo;
  const mediaEstimate = getMediaSizeEstimate(mediaInfo);
  const analysisReady =
    isAnalyzing && mediaInfo && mediaInfo.formats && mediaInfo.formats.length > 0;

  const handleCopySource = async () => {
    try {
      await navigator.clipboard.writeText(job.source);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Ignore clipboard error
    }
  };

  const handleAction = async (actionFn?: (id: string) => void) => {
    if (!actionFn || pendingAction) return;
    setPendingAction(true);
    try {
      await actionFn(job.id);
    } finally {
      setPendingAction(false);
    }
  };

  const EngineIcon = isMediaJob ? Film : isTorrentJob ? Magnet : Download;

  const renderPrimaryAction = () => {
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
            onClick={() => handleAction(onPause)}
          >
            <Pause className="size-3.5" aria-hidden="true" />
            Pause
          </button>
        );
      }
    }

    if (isPaused && onResume) {
      const canResume =
        capabilities?.resume.mutableNow ?? true;
      if (canResume) {
        return (
          <button
            type="button"
            className="flex h-8 items-center gap-1.5 rounded-md bg-primary px-2.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            disabled={pendingAction}
            onClick={() => handleAction(onResume)}
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
            onClick={() => handleAction(onRetry)}
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
          onClick={() => handleAction(onStopSeeding)}
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
          className="flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 text-xs font-medium text-foreground hover:bg-surface-2"
          onClick={onOpenFolder}
        >
          <FolderOpen className="size-3.5" aria-hidden="true" />
          Open downloads folder
        </button>
      );
    }

    return null;
  };

  const totalBytesDisplay = mediaEstimate.combinesSeparateAudio
    ? mediaEstimate.totalBytes
      ? `~${formatBytes(mediaEstimate.totalBytes)} est.`
      : 'Unknown size'
    : job.totalBytes > 0
    ? formatBytes(job.totalBytes)
    : 'Unknown size';

  return (
    <li
      className={cx(
        'rounded-lg border bg-surface p-3 transition-colors',
        selected ? 'border-primary/45 bg-primary/[0.06]' : 'border-border'
      )}
    >
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2.5">
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

        <div className="min-w-0">
          <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-2.5">
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
                <span className="max-w-48 truncate">
                  {sourceDomain(job.source)}
                </span>
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

            <div className="flex shrink-0 items-center gap-2">
              {renderPrimaryAction()}

              <div className="relative">
                <button
                  type="button"
                  className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground"
                  onClick={() => setMenuOpen((prev) => !prev)}
                  aria-label="More actions"
                  aria-expanded={menuOpen}
                >
                  <MoreVertical className="size-4" aria-hidden="true" />
                </button>

                {menuOpen && (
                  <div
                    className="absolute right-0 top-full z-20 mt-1 min-w-[160px] rounded-md border border-border bg-surface-2 py-1 shadow-lg"
                    onClick={() => setMenuOpen(false)}
                  >
                    {onCancel && !isCompleted && !isCancelled && (
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-destructive hover:bg-surface"
                        onClick={() => handleAction(onCancel)}
                      >
                        Cancel
                      </button>
                    )}

                    {onRetry && (isFailed || isCancelled) && (
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface"
                        onClick={() => handleAction(onRetry)}
                      >
                        Retry
                      </button>
                    )}

                    {onOpenFolder && (isCompleted || isSeeding) && (
                      <button
                        type="button"
                        className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface"
                        onClick={onOpenFolder}
                      >
                        Open downloads folder
                      </button>
                    )}

                    <button
                      type="button"
                      className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface"
                      onClick={handleCopySource}
                    >
                      {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
                      {copied ? 'Copied URL' : 'Copy source URL'}
                    </button>

                    <button
                      type="button"
                      className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface"
                      onClick={() => setDetailsOpen((prev) => !prev)}
                    >
                      {detailsOpen ? 'Hide details' : 'Show details'}
                    </button>
                  </div>
                )}
              </div>

              <button
                type="button"
                className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground"
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

          {/* Progress / Status row */}
          <div className="mt-2.5">
            {isAnalyzing && (
              <div className="flex items-center gap-2 text-xs text-info">
                <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
                <span>
                  {isTorrentJob
                    ? 'Fetching torrent metadata…'
                    : 'Analyzing media…'}
                </span>
              </div>
            )}

            {isProcessing && (
              <div className="space-y-1.5">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
                  <div className="h-full w-full animate-pulse bg-info" />
                </div>
                <div className="flex justify-between text-xs text-info">
                  <span>Download finished — merging video and audio with FFmpeg</span>
                  <span>Processing</span>
                </div>
              </div>
            )}

            {isQueued && (
              <div className="rounded border border-border bg-surface-2/50 px-2.5 py-1.5 text-xs text-muted-foreground">
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
            )}

            {(isDownloading || isPaused) && (
              <div className="space-y-1.5">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
                  <div
                    className={cx(
                      'h-full transition-all duration-300',
                      isPaused ? 'bg-muted-foreground' : 'bg-primary'
                    )}
                    style={{ width: `${Math.min(job.progress, 100)}%` }}
                  />
                </div>
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs">
                  <div className="flex items-center gap-2">
                    <span className="num font-medium text-foreground">
                      {job.progress.toFixed(1)}%
                    </span>
                    <span className="num text-muted-foreground">
                      {formatBytes(job.completedBytes)} / {totalBytesDisplay}
                    </span>
                  </div>

                  {isDownloading && (
                    <div className="flex items-center gap-3">
                      <span className="num text-foreground font-medium">
                        {formatSpeed(job.speedBytesPerSecond)}
                      </span>
                      <span className="num text-muted-foreground">
                        ETA {formatEta(job.etaSeconds)}
                      </span>
                    </div>
                  )}

                  {isPaused && (
                    <span className="font-medium text-muted-foreground">Paused</span>
                  )}
                </div>
              </div>
            )}

            {isSeeding && (
              <div className="space-y-1.5">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
                  <div className="h-full w-full bg-success" />
                </div>
                <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-xs">
                  <div className="flex items-center gap-2">
                    <span className="font-medium text-success">Seeding</span>
                    <span className="num text-muted-foreground">
                      {formatBytes(job.totalBytes)}
                    </span>
                  </div>

                  {job.torrentInfo && (
                    <div className="flex flex-wrap items-center gap-3 num text-muted-foreground">
                      <span className="text-success font-medium">
                        ↑ {formatSpeed(job.torrentInfo.uploadSpeed)}
                      </span>
                      <span>
                        Peers: {job.torrentInfo.seeders} S / {job.torrentInfo.leechers} L
                      </span>
                      <span>Ratio: {job.torrentInfo.ratio.toFixed(2)}</span>
                    </div>
                  )}
                </div>
              </div>
            )}

            {isCompleted && (
              <div className="flex items-center justify-between text-xs text-muted-foreground">
                <span className="font-medium text-success">Completed</span>
                <span className="num">
                  {formatBytes(job.totalBytes > 0 ? job.totalBytes : job.completedBytes)}
                </span>
              </div>
            )}

            {isFailed && job.error && (
              <div className="mt-1 text-xs text-destructive" role="alert">
                <span>{job.error}</span>
              </div>
            )}
          </div>

          {/* Expanded details section */}
          {detailsOpen && (
            <div className="mt-3 border-t border-border pt-3 space-y-3">
              <JobPowerControls
                job={job}
                capabilities={capabilities}
                onUpdated={onJobUpdated}
              />

              <div className="grid gap-2 text-xs sm:grid-cols-2">
                <div className="space-y-1">
                  <span className="font-medium text-muted-foreground">Source URL:</span>
                  <p className="break-all font-mono text-foreground/90">{job.source}</p>
                </div>

                {job.destinationDir && (
                  <div className="space-y-1">
                    <span className="font-medium text-muted-foreground">Destination:</span>
                    <p className="break-all font-mono text-foreground/90">{job.destinationDir}</p>
                  </div>
                )}

                {job.finalPath && (
                  <div className="space-y-1">
                    <span className="font-medium text-muted-foreground">Final Path:</span>
                    <p className="break-all font-mono text-foreground/90">{job.finalPath}</p>
                  </div>
                )}

                <div className="space-y-1">
                  <span className="font-medium text-muted-foreground">Created:</span>
                  <p className="text-foreground/90">{new Date(job.createdAt).toLocaleString()}</p>
                </div>

                {job.categoryId && (
                  <div className="space-y-1">
                    <span className="font-medium text-muted-foreground">Category:</span>
                    <p className="text-foreground/90">{job.categoryId}</p>
                  </div>
                )}

                {job.conflictPolicy && (
                  <div className="space-y-1">
                    <span className="font-medium text-muted-foreground">Conflict policy:</span>
                    <p className="text-foreground/90">{job.conflictPolicy}</p>
                  </div>
                )}

                {/* Media breakdown inside expanded details */}
                {isMediaJob && mediaEstimate.selected && (
                  <div className="sm:col-span-2 rounded border border-border/60 bg-surface-2/40 p-2 space-y-1">
                    <div className="flex items-center gap-1.5 font-medium text-foreground">
                      <Volume2 className="size-3.5 text-primary" />
                      <span>Media Details</span>
                    </div>
                    <div className="grid gap-1 sm:grid-cols-3 text-muted-foreground">
                      <div>Video: {mediaEstimate.videoBytes ? formatBytes(mediaEstimate.videoBytes) : 'Size unavailable'}</div>
                      <div>Best audio: {mediaEstimate.audioBytes ? formatBytes(mediaEstimate.audioBytes) : 'Size unavailable'}</div>
                      <div>Estimated total: {mediaEstimate.totalBytes ? formatBytes(mediaEstimate.totalBytes) : 'Unknown'}</div>
                    </div>
                  </div>
                )}

                {/* Torrent details */}
                {isTorrentJob && job.torrentInfo && (
                  <div className="sm:col-span-2 rounded border border-border/60 bg-surface-2/40 p-2 grid grid-cols-2 sm:grid-cols-4 gap-2 text-muted-foreground">
                    <div>Uploaded: {formatBytes(job.torrentInfo.uploaded)}</div>
                    <div>Ratio: {job.torrentInfo.ratio.toFixed(2)}</div>
                    <div>Seeders: {job.torrentInfo.seeders}</div>
                    <div>Leechers: {job.torrentInfo.leechers}</div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </li>
  );
}
