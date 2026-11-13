import { CheckCircle2, CircleAlert, RefreshCw, RotateCw, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { getMediaDependencyStatus } from '../api';
import type { ExternalDependencyStatus, MediaDependencyState, MediaDependencyStatus } from '../types';
import { cx } from '../downloadUi';

const STATE_LABELS: Record<MediaDependencyState, string> = {
  available: 'Available',
  missing: 'Not found',
  invalid: 'Not usable',
  probe_failed: 'Check failed',
};

const SOURCE_LABELS: Record<string, string> = {
  user_configured: 'User-configured',
  system_path: 'System-provided (PATH)',
  alongside_ffmpeg: 'Next to FFmpeg',
  none: '—',
};

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

function badgeClass(ok: boolean) {
  return cx(
    'rounded-md border px-1.5 py-0.5 text-xs',
    ok ? 'border-success/35 bg-success/10 text-success' : 'border-warning/35 bg-warning/10 text-warning',
  );
}

function ExternalToolRow({ name, purpose, dep }: { name: string; purpose: string; dep: ExternalDependencyStatus }) {
  const usable = dep.available && !dep.restartRequired;
  return (
    <div className="space-y-1.5 rounded-md border border-border/70 p-3" aria-label={`${name} status`}>
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <p className="text-sm font-medium">{name}</p>
          <p className="text-xs text-muted-foreground">{purpose}</p>
        </div>
        <span className={badgeClass(usable)}>{dep.restartRequired ? 'Restart required' : STATE_LABELS[dep.state] ?? dep.state}</span>
      </div>
      <dl className="grid gap-x-4 gap-y-0.5 text-xs sm:grid-cols-[auto_1fr]">
        <dt className="text-muted-foreground">Version</dt>
        <dd>{dep.version || '—'}</dd>
        <dt className="text-muted-foreground">Source</dt>
        <dd>{SOURCE_LABELS[dep.source] ?? dep.source}</dd>
        <dt className="text-muted-foreground">Managed by</dt>
        <dd>You (GoDownloader does not install or update it)</dd>
      </dl>
      {dep.diagnostic && (
        <p className={cx('flex items-start gap-1.5 text-xs', usable ? 'text-muted-foreground' : 'text-warning')}>
          {dep.restartRequired ? <RotateCw className="mt-0.5 size-3.5 shrink-0" /> : <CircleAlert className="mt-0.5 size-3.5 shrink-0" />}
          <span>{dep.diagnostic}</span>
        </p>
      )}
    </div>
  );
}

/** Read-only media toolchain diagnostics: FFmpeg, FFprobe, the JavaScript runtime and per-workflow readiness. */
export function MediaDependenciesPanel() {
  const [status, setStatus] = useState<MediaDependencyStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const refresh = async () => {
    setLoading(true);
    setError('');
    try {
      setStatus(await getMediaDependencyStatus());
    } catch (err: unknown) {
      setError(errorMessage(err, 'Failed to load media dependency status'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  if (loading && !status) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-border bg-surface p-4 text-sm text-muted-foreground">
        <RefreshCw className="size-4 animate-spin" /> Checking media tools…
      </div>
    );
  }

  if (!status) {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive" role="alert">
        {error || 'Media dependency status is unavailable.'}
      </div>
    );
  }

  return (
    <div className="space-y-3 rounded-lg border border-border bg-surface p-4" aria-label="Media tools">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold">Media tools</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">
            Tools yt-dlp uses for merging and post-processing. They are not installed by GoDownloader.
          </p>
        </div>
        <button
          type="button"
          onClick={() => void refresh()}
          disabled={loading}
          className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-xs font-medium disabled:opacity-55"
        >
          <RefreshCw className={cx('size-3.5', loading && 'animate-spin')} /> Re-check
        </button>
      </div>

      {error && (
        <p className="text-xs text-destructive" role="alert">
          {error}
        </p>
      )}

      <ExternalToolRow name="FFmpeg" purpose="Merges video and audio, embeds and converts subtitles." dep={status.ffmpeg} />
      <ExternalToolRow name="FFprobe" purpose="Inspects media streams during post-processing." dep={status.ffprobe} />

      <div className="space-y-1.5 rounded-md border border-border/70 p-3" aria-label="JavaScript runtime status">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <p className="text-sm font-medium">JavaScript runtime</p>
          <span className={badgeClass(status.javascriptRuntime.detected)}>
            {status.javascriptRuntime.detected ? `Found (${status.javascriptRuntime.runtime})` : 'Not found'}
          </span>
        </div>
        <p className="text-xs text-muted-foreground">{status.javascriptRuntime.diagnostic}</p>
      </div>

      <div>
        <p className="mb-1.5 text-xs font-medium text-muted-foreground">What works right now</p>
        <ul className="space-y-1">
          {status.workflows.map((w) => (
            <li key={w.id} className="flex items-start gap-2 text-xs" aria-label={`${w.label}: ${w.ready ? 'ready' : 'not ready'}`}>
              {w.ready ? (
                <CheckCircle2 className="mt-0.5 size-3.5 shrink-0 text-success" />
              ) : (
                <TriangleAlert className={cx('mt-0.5 size-3.5 shrink-0', w.advisory ? 'text-muted-foreground' : 'text-warning')} />
              )}
              <span>
                {w.label}
                {!w.ready && w.diagnostic && <span className="block text-muted-foreground">{w.diagnostic}</span>}
              </span>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
