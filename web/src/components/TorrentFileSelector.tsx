import { File, Folder, LoaderCircle, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { getTorrentFiles, ApiResponseError } from '../api';
import type {
  Job,
  SeedingMode,
  SeedingPolicy,
  TorrentFile,
  TorrentFilePriority,
  TorrentFileSelection,
} from '../types';

function formatBytesHuman(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

/**
 * Parse the backend INSUFFICIENT_DISK_SPACE message and return a user-friendly
 * multi-line description. The backend message format is:
 *   "INSUFFICIENT_DISK_SPACE: insufficient free space in <dir> (free: <n>, required: <n>, reserve: <n>, remaining: <n>)"
 */
export function formatDiskSpaceError(message: string): string {
  const match = message.match(/free:\s*(\d+),\s*required:\s*(\d+),\s*reserve:\s*(\d+),\s*remaining:\s*(\d+)/);
  if (!match) return `Insufficient disk space\n${message}`;
  const free = Number(match[1]);
  const required = Number(match[2]);
  const reserve = Number(match[3]);
  const remaining = Number(match[4]);
  return [
    'Insufficient disk space',
    `Available: ${formatBytesHuman(free)}`,
    `Selected remaining: ${formatBytesHuman(remaining)}`,
    `Reserved: ${formatBytesHuman(reserve)}`,
    `Required: ${formatBytesHuman(required)}`,
  ].join('\n');
}

interface TorrentFileSelectorProps {
  job: Job;
  onStart: (jobId: string, files: TorrentFileSelection[], seedingPolicy: SeedingPolicy) => Promise<void>;
  onClose: () => void;
}

export function normalizeSeedingMode(value: unknown): SeedingMode {
  if (typeof value !== 'string') return 'none';
  return ['none', 'unlimited', 'ratio', 'duration', 'ratio_or_duration'].includes(value)
    ? value as SeedingMode
    : 'none';
}

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

export function TorrentFileSelector({ job, onStart, onClose }: TorrentFileSelectorProps) {
  const [files, setFiles] = useState<TorrentFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState('');
  const [seedingMode, setSeedingMode] = useState<SeedingMode>(() => normalizeSeedingMode(job.seedingPolicy?.mode));
  const [ratioLimit, setRatioLimit] = useState(job.seedingPolicy?.ratioLimit ?? 1);
  const [durationHours, setDurationHours] = useState((job.seedingPolicy?.timeLimitSeconds ?? 86400) / 3600);

  useEffect(() => {
    let active = true;
    getTorrentFiles(job.id)
      .then((nextFiles) => {
        if (active) setFiles(nextFiles);
      })
      .catch((reason: unknown) => {
        if (active) setError(reason instanceof Error ? reason.message : 'Failed to fetch torrent files');
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => { active = false; };
  }, [job.id]);

  const toggleSelection = (index: number) => {
    setFiles((current) => current.map((file) => file.index === index ? { ...file, selected: !file.selected } : file));
  };

  const changePriority = (index: number, priority: TorrentFilePriority) => {
    setFiles((current) => current.map((file) => file.index === index ? { ...file, priority } : file));
  };

  const selectAll = (selected: boolean) => {
    setFiles((current) => current.map((file) => ({ ...file, selected })));
  };

  const selectedCount = files.filter((file) => file.selected).length;
  const selectedSize = files.filter((file) => file.selected).reduce((sum, file) => sum + file.size, 0);
  const totalSize = files.reduce((sum, file) => sum + file.size, 0);

  const handleStart = async () => {
    if (isSubmitting || selectedCount === 0) return;
    setIsSubmitting(true);
    setSubmitError('');
    const selection = files.map((file) => ({
      index: file.index,
      priority: file.selected ? file.priority : 'skip' as TorrentFilePriority,
    }));
    const seedingPolicy: SeedingPolicy = { mode: seedingMode };
    if (seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') seedingPolicy.ratioLimit = ratioLimit;
    if (seedingMode === 'duration' || seedingMode === 'ratio_or_duration') seedingPolicy.timeLimitSeconds = Math.round(durationHours * 3600);

    try {
      await onStart(job.id, selection, seedingPolicy);
    } catch (reason: unknown) {
      if (reason instanceof ApiResponseError && reason.code === 'INSUFFICIENT_DISK_SPACE') {
        setSubmitError(formatDiskSpaceError(reason.message));
      } else {
        setSubmitError(reason instanceof Error ? reason.message : String(reason));
      }
      setIsSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 grid bg-black/75 sm:place-items-center sm:p-4" role="presentation" onMouseDown={(event) => {
      if (event.target === event.currentTarget && !isSubmitting) onClose();
    }}>
      <section className="flex h-[100dvh] w-full flex-col overflow-hidden bg-background sm:h-auto sm:max-h-[86vh] sm:max-w-3xl sm:rounded-lg sm:border sm:border-border" role="dialog" aria-modal="true" aria-labelledby="torrent-selector-title">
        <header className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-3">
          <span className="grid size-10 shrink-0 place-items-center rounded-md border border-border bg-surface-2 text-info">
            <Folder className="size-4" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 id="torrent-selector-title" className="truncate text-base font-semibold">{job.name}</h2>
            <p className="text-xs text-muted-foreground">
              {files.length} files · {formatBytes(totalSize)} total · {selectedCount} selected
            </p>
          </div>
          <button type="button" className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground" onClick={onClose} disabled={isSubmitting} aria-label="Close torrent file selection">
            <X className="size-4" />
          </button>
        </header>

        {(error || submitError) && (
          <div className="mx-4 mt-3 whitespace-pre-line rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive" role="alert" data-testid="torrent-submit-error">
            {submitError || error}
          </div>
        )}

        <div className="scrollbar-thin flex shrink-0 items-center gap-2 overflow-x-auto border-b border-border px-4 py-2">
          <button type="button" className="h-8 shrink-0 rounded-md border border-border bg-surface-2 px-3 text-xs hover:border-border-strong" onClick={() => selectAll(true)} disabled={isSubmitting}>Select all</button>
          <button type="button" className="h-8 shrink-0 rounded-md border border-border bg-surface-2 px-3 text-xs hover:border-border-strong" onClick={() => selectAll(false)} disabled={isSubmitting}>Select none</button>
          <span className="ml-auto shrink-0 text-xs text-muted-foreground">{formatBytes(selectedSize)} selected</span>
        </div>

        <div className="scrollbar-thin min-h-0 flex-1 overflow-y-auto p-3">
          {loading ? (
            <div className="grid min-h-48 place-items-center text-sm text-muted-foreground" aria-busy="true">
              <span className="flex items-center gap-2"><LoaderCircle className="size-4 animate-spin" /> Loading torrent metadata…</span>
            </div>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border">
              <table className="w-full table-fixed text-sm">
                <thead className="bg-surface-2 text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="w-10 px-3 py-2"><input aria-label="Select every torrent file" type="checkbox" checked={files.length > 0 && selectedCount === files.length} onChange={(event) => selectAll(event.target.checked)} disabled={isSubmitting} /></th>
                    <th className="px-2 py-2 font-medium">File</th>
                    <th className="hidden w-28 px-2 py-2 text-right font-medium sm:table-cell">Size</th>
                    <th className="w-28 px-3 py-2 font-medium">Priority</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-border">
                  {files.map((file) => (
                    <tr key={file.index} className="bg-surface hover:bg-surface-2/70">
                      <td className="px-3 py-2"><input aria-label={`Select ${file.path}`} type="checkbox" checked={file.selected} onChange={() => toggleSelection(file.index)} disabled={isSubmitting} /></td>
                      <td className="min-w-0 px-2 py-2">
                        <span className="flex min-w-0 items-center gap-2">
                          <File className="size-4 shrink-0 text-muted-foreground" />
                          <span className="truncate" title={file.path}>{file.path}</span>
                        </span>
                      </td>
                      <td className="num hidden px-2 py-2 text-right text-xs text-muted-foreground sm:table-cell">{formatBytes(file.size)}</td>
                      <td className="px-3 py-2">
                        <select className="h-8 w-full rounded-md border border-border bg-surface-2 px-2 text-xs" value={file.priority} onChange={(event) => changePriority(file.index, event.target.value as TorrentFilePriority)} disabled={!file.selected || isSubmitting}>
                          <option value="normal">Normal</option>
                          <option value="high">High</option>
                          <option value="maximum">Maximum</option>
                        </select>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {!loading && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3">
              <h3 className="text-sm font-medium">Seeding policy</h3>
              <p className="mt-0.5 text-xs text-muted-foreground">Choose how long qBittorrent should seed after the payload completes.</p>
              <div className="mt-3 grid gap-3 sm:grid-cols-3">
                <label className="space-y-1 text-xs text-muted-foreground">
                  <span>Seeding policy</span>
                  <select className="h-9 w-full rounded-md border border-border bg-surface-2 px-2 text-sm text-foreground" value={seedingMode} onChange={(event) => setSeedingMode(normalizeSeedingMode(event.target.value))} disabled={isSubmitting}>
                    <option value="none">Do not seed</option>
                    <option value="unlimited">Unlimited</option>
                    <option value="ratio">Until ratio</option>
                    <option value="duration">For duration</option>
                    <option value="ratio_or_duration">Ratio or duration</option>
                  </select>
                </label>
                {(seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') && (
                  <label className="space-y-1 text-xs text-muted-foreground"><span>Ratio target</span><input aria-label="Ratio target" className="h-9 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground" type="number" min="0.01" max="1000" step="0.1" value={ratioLimit} onChange={(event) => setRatioLimit(Number(event.target.value))} disabled={isSubmitting} /></label>
                )}
                {(seedingMode === 'duration' || seedingMode === 'ratio_or_duration') && (
                  <label className="space-y-1 text-xs text-muted-foreground"><span>Active hours</span><input aria-label="Active seeding hours" className="h-9 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground" type="number" min="0.01" max="87600" step="0.5" value={durationHours} onChange={(event) => setDurationHours(Number(event.target.value))} disabled={isSubmitting} /></label>
                )}
              </div>
              <p className="mt-2 text-xs text-muted-foreground">Torrent filename conflicts remain engine-managed by qBittorrent.</p>
            </div>
          )}
        </div>

        <footer className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-t border-border bg-background px-4 py-3">
          <p className="num text-xs text-muted-foreground">{selectedCount} files · {formatBytes(selectedSize)}</p>
          <div className="flex gap-2">
            <button type="button" className="h-9 rounded-md border border-border bg-surface-2 px-4 text-sm hover:border-border-strong" onClick={onClose} disabled={isSubmitting}>Cancel</button>
            <button type="button" aria-label={isSubmitting ? 'Starting…' : 'Start Download'} className="h-9 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:brightness-110 disabled:opacity-50" onClick={handleStart} disabled={selectedCount === 0 || isSubmitting}>{isSubmitting ? 'Starting…' : 'Start selected files'}</button>
          </div>
        </footer>
      </section>
    </div>
  );
}
