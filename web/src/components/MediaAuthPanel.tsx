import { CheckCircle2, FileUp, KeyRound, RefreshCw, Trash2, XCircle } from 'lucide-react';
import React, { useEffect, useRef, useState } from 'react';
import { deleteMediaCookies, getMediaAuth, importMediaCookies, updateMediaAuth } from '../api';
import type { MediaAuthMode } from '../types';
import { cx } from '../downloadUi';

const BROWSER_OPTIONS = [
  { id: 'chrome', label: 'Google Chrome' },
  { id: 'firefox', label: 'Mozilla Firefox' },
  { id: 'edge', label: 'Microsoft Edge' },
  { id: 'brave', label: 'Brave' },
  { id: 'chromium', label: 'Chromium' },
  { id: 'opera', label: 'Opera' },
  { id: 'safari', label: 'Safari' },
  { id: 'vivaldi', label: 'Vivaldi' },
  { id: 'whale', label: 'Naver Whale' },
];

const inputClass = 'h-9 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground outline-none focus:border-primary disabled:cursor-not-allowed disabled:opacity-55';

export function MediaAuthPanel() {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  const [mode, setMode] = useState<MediaAuthMode>('none');
  const [browser, setBrowser] = useState('chrome');
  const [profile, setProfile] = useState('');
  const [hasCookieFile, setHasCookieFile] = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);

  const loadSettings = async () => {
    try {
      setLoading(true);
      setError('');
      const data = await getMediaAuth();
      setMode(data.mode);
      setBrowser(data.browser || 'chrome');
      setProfile(data.profile || '');
      setHasCookieFile(data.hasCookieFile);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to load media auth settings');
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void loadSettings();
  }, []);

  const handleSave = async () => {
    try {
      setSaving(true);
      setError('');
      setSuccess('');
      const res = await updateMediaAuth({
        mode,
        browser: mode === 'browser' ? browser : undefined,
        profile: mode === 'browser' ? profile.trim() : undefined,
      });
      setMode(res.mode);
      setBrowser(res.browser || 'chrome');
      setProfile(res.profile || '');
      setHasCookieFile(res.hasCookieFile);
      setSuccess('Media authentication settings saved.');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to save media auth settings');
    } finally {
      setSaving(false);
    }
  };

  const handleFileUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file) return;

    // Reset the input value so the same file can be selected again if needed
    event.target.value = '';

    try {
      setUploading(true);
      setError('');
      setSuccess('');
      const res = await importMediaCookies(file);
      setHasCookieFile(res.hasCookieFile);
      setMode(res.mode);
      setSuccess('Cookies imported successfully.');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to import cookies');
    } finally {
      setUploading(false);
    }
  };

  const handleDeleteCookies = async () => {
    if (!confirm('Remove imported cookies? If cookie file mode is active, it will be reset to None.')) {
      return;
    }

    try {
      setSaving(true);
      setError('');
      setSuccess('');
      const res = await deleteMediaCookies();
      setHasCookieFile(res.hasCookieFile);
      setMode(res.mode);
      setSuccess('Cookies removed successfully.');
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to delete cookies');
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center p-12 text-muted-foreground">
        <RefreshCw className="mr-2 size-4 animate-spin" /> Loading media authentication settings…
      </div>
    );
  }

  return (
    <div className="max-w-xl space-y-6">
      <div>
        <h3 className="text-base font-semibold tracking-tight">Media Authentication</h3>
        <p className="mt-0.5 text-xs text-muted-foreground">
          Configure authentication credentials for yt-dlp when analyzing or downloading login-required media.
        </p>
      </div>

      {(error || success) && (
        <div
          className={cx(
            'rounded-md border px-3 py-2 text-xs',
            error ? 'border-destructive/40 bg-destructive/10 text-destructive' : 'border-success/35 bg-success/10 text-success'
          )}
          role={error ? 'alert' : 'status'}
        >
          {error || success}
        </div>
      )}

      {/* Mode Selector */}
      <div className="space-y-3">
        <label className="block text-xs font-medium text-muted-foreground">Authentication Mode</label>
        <div className="grid gap-3 sm:grid-cols-3">
          {/* None */}
          <label
            className={cx(
              'flex cursor-pointer flex-col justify-between rounded-lg border p-3.5 transition-colors',
              mode === 'none' ? 'border-primary bg-primary/5' : 'border-border bg-surface hover:bg-surface-2'
            )}
          >
            <div className="flex items-center gap-2">
              <input
                type="radio"
                name="media-auth-mode"
                value="none"
                checked={mode === 'none'}
                onChange={() => setMode('none')}
                className="size-4 text-primary"
              />
              <span className="text-sm font-medium">None</span>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">No authentication credentials sent to yt-dlp.</p>
          </label>

          {/* Browser Session */}
          <label
            className={cx(
              'flex cursor-pointer flex-col justify-between rounded-lg border p-3.5 transition-colors',
              mode === 'browser' ? 'border-primary bg-primary/5' : 'border-border bg-surface hover:bg-surface-2'
            )}
          >
            <div className="flex items-center gap-2">
              <input
                type="radio"
                name="media-auth-mode"
                value="browser"
                checked={mode === 'browser'}
                onChange={() => setMode('browser')}
                className="size-4 text-primary"
              />
              <span className="text-sm font-medium">Browser session</span>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">Extract active cookies directly from local browser.</p>
          </label>

          {/* Imported cookies.txt */}
          <label
            className={cx(
              'flex cursor-pointer flex-col justify-between rounded-lg border p-3.5 transition-colors',
              mode === 'cookie_file' ? 'border-primary bg-primary/5' : 'border-border bg-surface hover:bg-surface-2'
            )}
          >
            <div className="flex items-center gap-2">
              <input
                type="radio"
                name="media-auth-mode"
                value="cookie_file"
                checked={mode === 'cookie_file'}
                onChange={() => setMode('cookie_file')}
                className="size-4 text-primary"
              />
              <span className="text-sm font-medium">Imported cookies</span>
            </div>
            <p className="mt-2 text-xs text-muted-foreground">Ephemeral encrypted cookies.txt file storage.</p>
          </label>
        </div>
      </div>

      {/* Browser Mode Configuration */}
      {mode === 'browser' && (
        <div className="space-y-4 rounded-lg border border-border bg-surface p-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <label className="block space-y-1.5 text-xs font-medium text-muted-foreground">
              <span>Browser</span>
              <select
                aria-label="Browser"
                value={browser}
                onChange={(e) => setBrowser(e.target.value)}
                className={inputClass}
                disabled={saving}
              >
                {BROWSER_OPTIONS.map((opt) => (
                  <option key={opt.id} value={opt.id}>
                    {opt.label}
                  </option>
                ))}
              </select>
            </label>

            <label className="block space-y-1.5 text-xs font-medium text-muted-foreground">
              <span>Profile (optional)</span>
              <input
                aria-label="Browser profile"
                type="text"
                value={profile}
                onChange={(e) => setProfile(e.target.value)}
                placeholder="e.g. Default or Profile 1"
                className={inputClass}
                disabled={saving}
              />
              <span className="block font-normal text-muted-foreground/80">Leave blank for browser default profile.</span>
            </label>
          </div>
        </div>
      )}

      {/* Cookie File Mode Configuration */}
      {mode === 'cookie_file' && (
        <div className="space-y-4 rounded-lg border border-border bg-surface p-4">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-2">
              {hasCookieFile ? (
                <>
                  <CheckCircle2 className="size-4 text-success" />
                  <span className="text-sm font-medium text-success">Cookies imported ✓</span>
                </>
              ) : (
                <>
                  <XCircle className="size-4 text-muted-foreground" />
                  <span className="text-sm font-medium text-muted-foreground">No cookies imported</span>
                </>
              )}
            </div>

            <div className="flex items-center gap-2">
              <input
                type="file"
                ref={fileInputRef}
                onChange={handleFileUpload}
                accept=".txt,text/plain"
                className="hidden"
                aria-label="Upload cookies file"
              />

              {hasCookieFile ? (
                <>
                  <button
                    type="button"
                    onClick={() => fileInputRef.current?.click()}
                    disabled={uploading || saving}
                    className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border bg-surface-2 px-3 text-xs font-medium text-foreground hover:bg-surface-2/80 disabled:opacity-50"
                  >
                    <FileUp className="size-3.5" />
                    {uploading ? 'Importing…' : 'Replace cookies'}
                  </button>

                  <button
                    type="button"
                    onClick={() => void handleDeleteCookies()}
                    disabled={uploading || saving}
                    className="inline-flex h-8 items-center gap-1.5 rounded-md border border-destructive/40 bg-destructive/10 px-3 text-xs font-medium text-destructive hover:bg-destructive/20 disabled:opacity-50"
                  >
                    <Trash2 className="size-3.5" />
                    Remove cookies
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  onClick={() => fileInputRef.current?.click()}
                  disabled={uploading || saving}
                  className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
                >
                  <FileUp className="size-3.5" />
                  {uploading ? 'Importing…' : 'Import cookies.txt'}
                </button>
              )}
            </div>
          </div>
          <p className="text-xs text-muted-foreground">
            Export a Netscape-formatted <code className="rounded bg-surface-2 px-1 py-0.5 text-foreground">cookies.txt</code> from your browser. GoDownloader encrypts the credentials on disk and only materializes ephemeral files during yt-dlp invocations.
          </p>
        </div>
      )}

      {/* Save Button */}
      <div className="flex items-center justify-start gap-2 pt-2">
        <button
          type="button"
          onClick={() => void handleSave()}
          disabled={saving || uploading || (mode === 'cookie_file' && !hasCookieFile)}
          className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <KeyRound className="size-4" />
          {saving ? 'Saving…' : 'Save Media Auth'}
        </button>
      </div>
    </div>
  );
}
