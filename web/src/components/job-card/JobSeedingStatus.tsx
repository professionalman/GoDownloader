import type { Job } from '../../types';
import { formatBytes, formatSpeed } from '../../downloadUi';

interface JobSeedingStatusProps {
  job: Job;
}

export function JobSeedingStatus({ job }: JobSeedingStatusProps) {
  if (job.status !== 'seeding') return null;

  return (
    <div className="mt-2.5 space-y-1.5">
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
  );
}
