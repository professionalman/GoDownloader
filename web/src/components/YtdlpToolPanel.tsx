import { CheckCircle2, Download, RefreshCw, RotateCw, ShieldCheck, TriangleAlert } from 'lucide-react';
import { useEffect, useState } from 'react';
import { checkYtdlpUpdate, getYtdlpToolStatus, installYtdlp, updateYtdlp } from '../api';
import type { ToolUpdateCheck, YtdlpToolOwnership, YtdlpToolStatus } from '../types';
import { cx } from '../downloadUi';

const OWNERSHIP_LABELS: Record<YtdlpToolOwnership, string> = {
  managed: 'Managed by GoDownloader',
  legacy_managed_directory: 'Manually placed in the GoDownloader tools folder',
  user_configured: 'User-configured',
  system_path: 'System-provided (PATH)',
  none: 'Not installed',
};

type Operation = 'install' | 'update' | 'check' | null;

const buttonClass =
  'inline-flex h-9 items-center justify-center gap-1.5 rounded-md px-3 text-sm font-medium disabled:cursor-not-allowed disabled:opacity-55';

function errorMessage(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

export function YtdlpToolPanel() {
  const [status, setStatus] = useState<YtdlpToolStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [operation, setOperation] = useState<Operation>(null);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [updateCheck, setUpdateCheck] = useState<ToolUpdateCheck | null>(null);

  const refresh = async () => {
    try {
      setStatus(await getYtdlpToolStatus());
    } catch (err: unknown) {
      setError(errorMessage(err, 'Failed to load yt-dlp status'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void refresh();
  }, []);

  const run = async (op: Exclude<Operation, null>) => {
    setOperation(op);
    setError('');
    setNotice('');
    try {
      if (op === 'check') {
        const result = await checkYtdlpUpdate();
        setUpdateCheck(result);
        setNotice(result.updateAvailable ? `yt-dlp ${result.latestVersion} is available.` : 'yt-dlp is up to date.');
      } else {
        const result = op === 'install' ? await installYtdlp() : await updateYtdlp();
        setUpdateCheck(null);
        setNotice(
          result.alreadyCurrent
            ? `yt-dlp ${result.version} is already the current authenticated release.`
            : `yt-dlp ${result.version} was verified and installed. Quit GoDownloader completely and start it again to use it.`,
        );
      }
    } catch (err: unknown) {
      setError(errorMessage(err, 'The yt-dlp operation failed'));
    } finally {
      setOperation(null);
      await refresh();
    }
  };

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-border bg-surface p-4 text-sm text-muted-foreground">
        <RefreshCw className="size-4 animate-spin" /> Loading yt-dlp status…
      </div>
    );
  }

  if (!status) {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive" role="alert">
        {error || 'yt-dlp status is unavailable.'}
      </div>
    );
  }

  const busy = operation !== null || status.busy;
  const showUpdate = status.canUpdate && updateCheck?.updateAvailable;

  return (
    <div className="space-y-3 rounded-lg border border-border bg-surface p-4" aria-label="yt-dlp tool">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h4 className="text-sm font-semibold">yt-dlp</h4>
          <p className="mt-0.5 text-xs text-muted-foreground">Media analysis and download engine.</p>
        </div>
        <span
          className={cx(
            'rounded-md border px-1.5 py-0.5 text-xs',
            status.available ? 'border-success/35 bg-success/10 text-success' : 'border-warning/35 bg-warning/10 text-warning',
          )}
        >
          {status.available ? 'Available' : 'Unavailable'}
        </span>
      </div>

      <dl className="grid gap-x-4 gap-y-1 text-xs sm:grid-cols-[auto_1fr]">
        <dt className="text-muted-foreground">Version</dt>
        <dd>{status.version || '—'}</dd>
        <dt className="text-muted-foreground">Source</dt>
        <dd>{OWNERSHIP_LABELS[status.ownership] ?? status.ownership}</dd>
        {status.available && (
          <>
            <dt className="text-muted-foreground">Verification</dt>
            <dd className="flex items-center gap-1">
              {status.authenticated ? (
                <>
                  <ShieldCheck className="size-3.5 text-success" /> Signed release verified
                </>
              ) : (
                'Version checked only (not signature-verified)'
              )}
            </dd>
          </>
        )}
        {status.managedVersion && status.managedVersion !== status.version && (
          <>
            <dt className="text-muted-foreground">Managed version</dt>
            <dd>{status.managedVersion}</dd>
          </>
        )}
      </dl>

      {status.restartRequired && (
        <div className="flex items-start gap-2 rounded-md border border-warning/35 bg-warning/10 px-3 py-2 text-xs text-warning" role="status">
          <RotateCw className="mt-0.5 size-3.5 shrink-0" />
          <span>
            Quit GoDownloader completely and start it again to use the newly installed yt-dlp. Closing the window to the
            tray or reloading this page does not restart it. Until then, new and queued media work waits; downloads
            already running are not interrupted.
          </span>
        </div>
      )}

      {status.diagnostic && !status.restartRequired && (
        <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
          <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
          {status.diagnostic}
        </p>
      )}

      {(error || notice) && (
        <div
          className={cx(
            'flex items-start gap-1.5 rounded-md border px-3 py-2 text-xs',
            error ? 'border-destructive/40 bg-destructive/10 text-destructive' : 'border-success/35 bg-success/10 text-success',
          )}
          role={error ? 'alert' : 'status'}
        >
          {!error && <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" />}
          {error || notice}
        </div>
      )}

      {!status.platformSupported ? (
        <p className="text-xs text-muted-foreground">Managed installation is available on Windows (64-bit) only.</p>
      ) : (
        <div className="flex flex-wrap gap-2">
          {status.canInstall && (
            <button
              type="button"
              className={cx(buttonClass, 'bg-primary text-primary-foreground')}
              disabled={busy}
              onClick={() => void run('install')}
            >
              <Download className="size-4" />
              {operation === 'install' ? 'Installing…' : 'Install yt-dlp'}
            </button>
          )}
          {status.canUpdate && (
            <button
              type="button"
              className={cx(buttonClass, 'border border-border bg-surface-2 text-foreground')}
              disabled={busy}
              onClick={() => void run('check')}
            >
              <RefreshCw className={cx('size-4', operation === 'check' && 'animate-spin')} />
              {operation === 'check' ? 'Checking…' : 'Check for updates'}
            </button>
          )}
          {showUpdate && (
            <button
              type="button"
              className={cx(buttonClass, 'bg-primary text-primary-foreground')}
              disabled={busy}
              onClick={() => void run('update')}
            >
              <Download className="size-4" />
              {operation === 'update' ? 'Updating…' : `Update to ${updateCheck?.latestVersion}`}
            </button>
          )}
        </div>
      )}
      <p className="text-[11px] leading-relaxed text-muted-foreground">
        Downloads come only from the official yt-dlp GitHub releases and are installed only after the signed checksum
        verifies. Some sites (such as YouTube) may also need a JavaScript runtime like Deno, which GoDownloader does not
        install.
      </p>
    </div>
  );
}
