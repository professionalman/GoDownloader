import { Loader2 } from 'lucide-react';
import type { Job } from '../../types';
import {
  cx,
  formatBytes,
  formatSpeed,
  formatEta,
  getMediaSizeEstimate,
} from '../../downloadUi';

interface JobProgressProps {
  job: Job;
}

export function JobProgress({ job }: JobProgressProps) {
  const isDownloading = job.status === 'downloading';
  const isPaused = job.status === 'paused';
  const isCompleted = job.status === 'completed';
  const isFailed = job.status === 'failed';
  const isAnalyzing = job.status === 'analyzing';
  const isProcessing = job.status === 'processing';
  const isTorrentJob = job.type === 'torrent';

  const mediaInfo = job.mediaInfo;
  const mediaEstimate = getMediaSizeEstimate(mediaInfo);

  const totalBytesDisplay = mediaEstimate.combinesSeparateAudio
    ? mediaEstimate.totalBytes
      ? `~${formatBytes(mediaEstimate.totalBytes)} est.`
      : 'Unknown size'
    : job.totalBytes > 0
    ? formatBytes(job.totalBytes)
    : 'Unknown size';

  return (
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
  );
}
