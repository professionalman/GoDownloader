import { Volume2 } from 'lucide-react';
import type { Job, JobCapabilities } from '../../types';
import { formatBytes, getMediaSizeEstimate } from '../../downloadUi';
import { JobPowerControls } from '../JobPowerControls';

interface JobDetailsProps {
  job: Job;
  capabilities: JobCapabilities | null;
  onJobUpdated?: (job: Job) => void;
}

export function JobDetails({ job, capabilities, onJobUpdated }: JobDetailsProps) {
  const isMediaJob = job.type === 'media';
  const isTorrentJob = job.type === 'torrent';
  const mediaInfo = job.mediaInfo;
  const mediaEstimate = getMediaSizeEstimate(mediaInfo);

  return (
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
  );
}
