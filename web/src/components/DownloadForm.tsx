import React, { useState, useEffect, useRef } from 'react';
import { Paperclip, Plus, ChevronDown, ChevronUp, SlidersHorizontal } from 'lucide-react';
import type {
  JobPriority,
  Category,
  FilenameConflictPolicy,
  JobNetworkPolicyOverride,
  SeedingPolicy,
  JobCapabilities,
} from '../types';
import { getCategories, resolveCapabilities } from '../api';

interface DownloadFormProps {
  onSubmit: (
    sources: string[],
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: FilenameConflictPolicy,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ) => void;
  onUploadTorrent?: (
    file: File,
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ) => void;
  disabled?: boolean;
}

type DestinationMode = 'category' | 'custom';

export const DownloadForm: React.FC<DownloadFormProps> = ({
  onSubmit,
  onUploadTorrent,
  disabled,
}) => {
  const [inputText, setInputText] = useState('');
  const [expanded, setExpanded] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [destinationMode, setDestinationMode] = useState<DestinationMode>('category');
  const [priority, setPriority] = useState<JobPriority>('normal');
  const [categories, setCategories] = useState<Category[]>([]);
  const [selectedCategoryId, setSelectedCategoryId] = useState<string>('');
  const [customDestDir, setCustomDestDir] = useState<string>('');
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>('rename');
  const [error, setError] = useState('');
  const [capabilities, setCapabilities] = useState<JobCapabilities | null>(null);

  // Advanced fields
  const [downloadLimit, setDownloadLimit] = useState('');
  const [uploadLimit, setUploadLimit] = useState('');
  const [userAgent, setUserAgent] = useState('');
  const [headers, setHeaders] = useState('');
  const [proxyMode, setProxyMode] = useState<'disabled' | 'system' | 'custom'>('disabled');
  const [proxyProtocol, setProxyProtocol] = useState<'http' | 'https' | 'socks5'>('http');
  const [proxyHost, setProxyHost] = useState('');
  const [proxyPort, setProxyPort] = useState('');
  const [proxyUsername, setProxyUsername] = useState('');
  const [proxyPassword, setProxyPassword] = useState('');
  const [trackers, setTrackers] = useState('');
  const [maxAttempts, setMaxAttempts] = useState('');
  const [retryWait, setRetryWait] = useState('');
  const [connectTimeout, setConnectTimeout] = useState('');
  const [requestTimeout, setRequestTimeout] = useState('');
  const [split, setSplit] = useState('');
  const [connections, setConnections] = useState('');
  const [minSplitMiB, setMinSplitMiB] = useState('');

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    getCategories()
      .then(setCategories)
      .catch(() => {});
  }, []);

  useEffect(() => {
    const sources = inputText
      .split('\n')
      .map((value) => value.trim())
      .filter(Boolean);
    if (!sources.length) {
      setCapabilities(null);
      return;
    }
    const timer = window.setTimeout(() => {
      resolveCapabilities(sources.length === 1 ? sources[0] : sources)
        .then(setCapabilities)
        .catch(() => setCapabilities(null));
    }, 250);
    return () => window.clearTimeout(timer);
  }, [inputText]);

  const sources = inputText
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  const sourceCount = sources.length;
  const overLimit = sourceCount > 100;

  const chooseDestinationMode = (mode: DestinationMode) => {
    setDestinationMode(mode);
    if (mode === 'category') {
      setCustomDestDir('');
    } else {
      setSelectedCategoryId('');
    }
  };

  const handleSourceChange = (
    e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>
  ) => {
    setInputText(e.target.value);
    setError('');
  };

  const buildPolicy = (): JobNetworkPolicyOverride | undefined => {
    const policy: JobNetworkPolicyOverride = {};
    const MIB = 1024 * 1024;

    if (downloadLimit !== '') {
      policy.downloadLimitBytesPerSecond = Math.round(Number(downloadLimit) * MIB);
    }
    if (uploadLimit !== '') {
      policy.uploadLimitBytesPerSecond = Math.round(Number(uploadLimit) * MIB);
    }
    if (capabilities?.proxy.supported) {
      policy.proxy = { mode: proxyMode };
      if (proxyMode === 'custom') {
        policy.proxy = {
          mode: proxyMode,
          protocol: proxyProtocol,
          host: proxyHost,
          port: Number(proxyPort),
          username: proxyUsername,
        };
        if (proxyPassword) policy.proxyPassword = proxyPassword;
      }
    }
    if (userAgent) policy.userAgent = userAgent;
    if (headers.trim()) {
      policy.httpHeaders = headers
        .split('\n')
        .filter(Boolean)
        .map((line) => {
          const separator = line.indexOf(':');
          return {
            name: line.slice(0, separator).trim(),
            value: line.slice(separator + 1).trim(),
          };
        });
    }
    if (maxAttempts !== '' || retryWait !== '') {
      policy.retryPolicy = {
        maxAttempts: Number(maxAttempts || 0),
        retryWaitSeconds: Number(retryWait || 0),
      };
    }
    if (connectTimeout !== '' || requestTimeout !== '') {
      policy.timeoutPolicy = {
        connectTimeoutSeconds: Number(connectTimeout || 0),
        requestTimeoutSeconds: Number(requestTimeout || 0),
      };
    }
    if (split !== '' || connections !== '' || minSplitMiB !== '') {
      policy.directConnections = {
        split: Number(split || 5),
        maxConnectionsPerServer: Number(connections || 1),
        minSplitSizeBytes: Number(minSplitMiB || 20) * MIB,
      };
    }
    return Object.keys(policy).length ? policy : undefined;
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (sources.length === 0) {
      setError('Please enter at least one URL or Magnet link');
      return;
    }

    if (sources.length > 100) {
      setError('Batch submission cannot exceed 100 links at once');
      return;
    }

    setError('');

    const categoryId =
      destinationMode === 'category' && selectedCategoryId
        ? selectedCategoryId
        : undefined;

    const destinationDir =
      destinationMode === 'custom' && customDestDir.trim()
        ? customDestDir.trim()
        : undefined;

    onSubmit(
      sources,
      priority,
      categoryId,
      destinationDir,
      conflictPolicy,
      buildPolicy(),
      undefined,
      trackers
        .split('\n')
        .map((value) => value.trim())
        .filter(Boolean)
    );
    setInputText('');
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file && onUploadTorrent) {
      const categoryId =
        destinationMode === 'category' && selectedCategoryId
          ? selectedCategoryId
          : undefined;

      const destinationDir =
        destinationMode === 'custom' && customDestDir.trim()
          ? customDestDir.trim()
          : undefined;

      onUploadTorrent(
        file,
        priority,
        categoryId,
        destinationDir,
        buildPolicy(),
        undefined,
        trackers
          .split('\n')
          .map((value) => value.trim())
          .filter(Boolean)
      );
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  const controlClass =
    'h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground outline-none focus:border-primary disabled:opacity-50';

  return (
    <form
      className="rounded-lg border border-border bg-surface p-2"
      onSubmit={handleSubmit}
      aria-label="Add downloads"
    >
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1 basis-full sm:basis-0">
          <label htmlFor="download-sources" className="sr-only">
            Download source
          </label>

          {expanded ? (
            <textarea
              id="download-sources"
              rows={3}
              value={inputText}
              onChange={handleSourceChange}
              placeholder="Paste download URLs or magnet links — one per line"
              className="min-h-[5rem] w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-foreground outline-none focus:border-primary disabled:opacity-50"
              disabled={disabled}
            />
          ) : (
            <input
              id="download-sources"
              value={inputText}
              onChange={handleSourceChange}
              placeholder="Paste a URL or magnet link"
              className="h-8 w-full rounded-md border border-border bg-surface-2 px-3 font-mono text-sm text-foreground outline-none focus:border-primary disabled:opacity-50"
              disabled={disabled}
            />
          )}
        </div>

        <div className="flex flex-1 flex-wrap gap-2 sm:flex-none sm:flex-nowrap">
          <input
            ref={fileInputRef}
            type="file"
            accept=".torrent"
            className="sr-only"
            onChange={handleFileChange}
            disabled={disabled}
          />

          <button
            type="button"
            className="grid size-8 shrink-0 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground hover:text-foreground disabled:opacity-50"
            onClick={() => fileInputRef.current?.click()}
            disabled={disabled || !onUploadTorrent}
            aria-label="Add torrent file"
            title="Add .torrent file"
          >
            <Paperclip className="size-4" aria-hidden="true" />
          </button>

          <button
            type="submit"
            className="flex h-8 flex-1 items-center justify-center gap-2 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50 sm:flex-none"
            disabled={disabled || sourceCount === 0 || overLimit}
          >
            <Plus className="size-4" aria-hidden="true" />
            {disabled ? 'Starting…' : 'Start'}
          </button>

          <button
            type="button"
            className="grid size-8 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground"
            onClick={() => setExpanded((current) => !current)}
            aria-expanded={expanded}
            aria-label={expanded ? 'Collapse add panel' : 'Expand add panel'}
          >
            {expanded ? (
              <ChevronUp className="size-4" aria-hidden="true" />
            ) : (
              <ChevronDown className="size-4" aria-hidden="true" />
            )}
          </button>
        </div>
      </div>

      {expanded && (
        <>
          <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>One source per line</span>
            <span aria-hidden="true">·</span>
            <span className={overLimit ? 'num text-destructive' : 'num'}>
              {sourceCount} of 100 sources
            </span>
            {overLimit && (
              <span className="text-destructive">
                Remove {sourceCount - 100} to continue
              </span>
            )}
          </div>

          <div className="mt-2 border-t border-border pt-2">
            <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-4">
              <div className="min-w-0 space-y-1">
                <label
                  htmlFor="job-priority-select"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Priority
                </label>
                <select
                  id="job-priority-select"
                  value={priority}
                  onChange={(e) => setPriority(e.target.value as JobPriority)}
                  className={controlClass}
                  disabled={disabled}
                >
                  <option value="high">High</option>
                  <option value="normal">Normal</option>
                  <option value="low">Low</option>
                </select>
              </div>

              <div className="min-w-0 space-y-1">
                <label
                  htmlFor="category-select"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Category
                </label>
                <select
                  id="category-select"
                  value={selectedCategoryId}
                  onChange={(e) => {
                    setSelectedCategoryId(e.target.value);
                    if (e.target.value) chooseDestinationMode('category');
                  }}
                  disabled={disabled || destinationMode === 'custom'}
                  className={controlClass}
                >
                  <option value="">Default download folder</option>
                  {categories.map((cat) => (
                    <option key={cat.id} value={cat.id}>
                      {cat.name} ({cat.directory})
                    </option>
                  ))}
                </select>
              </div>

              <div className="min-w-0 space-y-1">
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium text-muted-foreground">
                    Destination
                  </span>
                  <div role="group" aria-label="Destination mode" className="flex gap-1 text-xs">
                    <button
                      type="button"
                      className={`px-1.5 py-0.5 rounded ${
                        destinationMode === 'category'
                          ? 'bg-primary/20 text-primary font-medium'
                          : 'text-muted-foreground hover:text-foreground'
                      }`}
                      onClick={() => chooseDestinationMode('category')}
                    >
                      Category
                    </button>
                    <button
                      type="button"
                      className={`px-1.5 py-0.5 rounded ${
                        destinationMode === 'custom'
                          ? 'bg-primary/20 text-primary font-medium'
                          : 'text-muted-foreground hover:text-foreground'
                      }`}
                      onClick={() => chooseDestinationMode('custom')}
                    >
                      Custom
                    </button>
                  </div>
                </div>

                <input
                  id="custom-dest-dir"
                  value={customDestDir}
                  onChange={(e) => {
                    setCustomDestDir(e.target.value);
                    if (e.target.value.trim()) chooseDestinationMode('custom');
                  }}
                  disabled={disabled || destinationMode === 'category'}
                  placeholder="C:\Downloads\Custom"
                  className={controlClass}
                />
              </div>

              <div className="min-w-0 space-y-1">
                <label
                  htmlFor="conflict-policy-select"
                  className="text-xs font-medium text-muted-foreground"
                >
                  Conflict policy
                </label>
                <select
                  id="conflict-policy-select"
                  value={conflictPolicy}
                  onChange={(e) =>
                    setConflictPolicy(e.target.value as FilenameConflictPolicy)
                  }
                  className={controlClass}
                  disabled={disabled}
                >
                  <option value="rename">Rename with suffix</option>
                  <option value="overwrite">Overwrite existing</option>
                  <option value="fail">Fail on conflict</option>
                </select>
              </div>
            </div>

            <div className="mt-2 pt-2 border-t border-border/50">
              <button
                type="button"
                className="flex h-8 items-center gap-2 rounded-md px-2 text-sm text-muted-foreground hover:bg-surface-2 hover:text-foreground"
                onClick={() => setAdvanced((current) => !current)}
                aria-expanded={advanced}
              >
                <SlidersHorizontal className="size-4" aria-hidden="true" />
                Advanced options
                <span className="text-xs">{advanced ? 'Hide' : 'Show'}</span>
              </button>

              {advanced && (
                <div className="mt-2 space-y-3 rounded-md border border-border/60 bg-surface-2/50 p-3">
                  {capabilities ? (
                    <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-3">
                      {capabilities.downloadLimit.supported && (
                        <div className="space-y-1">
                          <label
                            htmlFor="download-limit-input"
                            className="text-xs font-medium text-muted-foreground"
                          >
                            Download limit (MiB/s)
                          </label>
                          <input
                            id="download-limit-input"
                            type="number"
                            min="0"
                            step="0.1"
                            value={downloadLimit}
                            onChange={(e) => setDownloadLimit(e.target.value)}
                            placeholder="Unlimited"
                            className={controlClass}
                          />
                        </div>
                      )}

                      {capabilities.uploadLimit.supported && (
                        <div className="space-y-1">
                          <label
                            htmlFor="upload-limit-input"
                            className="text-xs font-medium text-muted-foreground"
                          >
                            Upload limit (MiB/s)
                          </label>
                          <input
                            id="upload-limit-input"
                            type="number"
                            min="0"
                            step="0.1"
                            value={uploadLimit}
                            onChange={(e) => setUploadLimit(e.target.value)}
                            placeholder="Unlimited"
                            className={controlClass}
                          />
                        </div>
                      )}

                      {capabilities.proxy.supported && (
                        <div className="space-y-1">
                          <label className="text-xs font-medium text-muted-foreground">
                            Proxy mode
                          </label>
                          <select
                            value={proxyMode}
                            onChange={(e) =>
                              setProxyMode(e.target.value as typeof proxyMode)
                            }
                            className={controlClass}
                          >
                            <option value="disabled">Disabled</option>
                            <option value="system">System</option>
                            <option value="custom">Custom</option>
                          </select>
                        </div>
                      )}

                      {capabilities.proxy.supported && proxyMode === 'custom' && (
                        <>
                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Proxy protocol
                            </label>
                            <select
                              value={proxyProtocol}
                              onChange={(e) =>
                                setProxyProtocol(e.target.value as typeof proxyProtocol)
                              }
                              className={controlClass}
                            >
                              {capabilities.proxy.supportedProtocols?.map((protocol) => (
                                <option key={protocol} value={protocol}>
                                  {protocol.toUpperCase()}
                                </option>
                              ))}
                            </select>
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Proxy host
                            </label>
                            <input
                              value={proxyHost}
                              onChange={(e) => setProxyHost(e.target.value)}
                              placeholder="proxy.example.com"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Proxy port
                            </label>
                            <input
                              type="number"
                              min="1"
                              max="65535"
                              value={proxyPort}
                              onChange={(e) => setProxyPort(e.target.value)}
                              placeholder="8080"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Proxy username
                            </label>
                            <input
                              value={proxyUsername}
                              onChange={(e) => setProxyUsername(e.target.value)}
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Proxy password
                            </label>
                            <input
                              type="password"
                              value={proxyPassword}
                              onChange={(e) => setProxyPassword(e.target.value)}
                              placeholder="Leave empty to preserve"
                              className={controlClass}
                            />
                          </div>
                        </>
                      )}

                      {capabilities.userAgent.supported && (
                        <div className="space-y-1">
                          <label className="text-xs font-medium text-muted-foreground">
                            User-Agent
                          </label>
                          <input
                            value={userAgent}
                            onChange={(e) => setUserAgent(e.target.value)}
                            placeholder="Custom User-Agent"
                            className={controlClass}
                          />
                        </div>
                      )}

                      {capabilities.retryPolicy.supported && (
                        <>
                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Max attempts
                            </label>
                            <input
                              type="number"
                              min="0"
                              max="100"
                              value={maxAttempts}
                              onChange={(e) => setMaxAttempts(e.target.value)}
                              placeholder="Inherit"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Retry wait (s)
                            </label>
                            <input
                              type="number"
                              min="0"
                              max="3600"
                              value={retryWait}
                              onChange={(e) => setRetryWait(e.target.value)}
                              placeholder="Inherit"
                              className={controlClass}
                            />
                          </div>
                        </>
                      )}

                      {capabilities.timeoutPolicy.supported && (
                        <>
                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Connect timeout (s)
                            </label>
                            <input
                              type="number"
                              min="0"
                              max="86400"
                              value={connectTimeout}
                              onChange={(e) => setConnectTimeout(e.target.value)}
                              placeholder="Inherit"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Request timeout (s)
                            </label>
                            <input
                              type="number"
                              min="0"
                              max="86400"
                              value={requestTimeout}
                              onChange={(e) => setRequestTimeout(e.target.value)}
                              placeholder="Inherit"
                              className={controlClass}
                            />
                          </div>
                        </>
                      )}

                      {capabilities.connections.supported && (
                        <>
                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Splits
                            </label>
                            <input
                              type="number"
                              min="1"
                              max="16"
                              value={split}
                              onChange={(e) => setSplit(e.target.value)}
                              placeholder="5"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Connections / server
                            </label>
                            <input
                              type="number"
                              min="1"
                              max="16"
                              value={connections}
                              onChange={(e) => setConnections(e.target.value)}
                              placeholder="1"
                              className={controlClass}
                            />
                          </div>

                          <div className="space-y-1">
                            <label className="text-xs font-medium text-muted-foreground">
                              Minimum split (MiB)
                            </label>
                            <input
                              type="number"
                              min="1"
                              max="1024"
                              value={minSplitMiB}
                              onChange={(e) => setMinSplitMiB(e.target.value)}
                              placeholder="20"
                              className={controlClass}
                            />
                          </div>
                        </>
                      )}

                      {capabilities.customHeaders.supported && (
                        <div className="space-y-1 sm:col-span-2 xl:col-span-3">
                          <label className="text-xs font-medium text-muted-foreground">
                            Custom HTTP Headers (one Header: value per line)
                          </label>
                          <textarea
                            rows={2}
                            value={headers}
                            onChange={(e) => setHeaders(e.target.value)}
                            placeholder="Authorization: Bearer token"
                            className="w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-1.5 font-mono text-sm text-foreground outline-none focus:border-primary"
                          />
                        </div>
                      )}

                      {capabilities.trackers.supported && (
                        <div className="space-y-1 sm:col-span-2 xl:col-span-3">
                          <label className="text-xs font-medium text-muted-foreground">
                            Custom Torrent Trackers (one URL per line)
                          </label>
                          <textarea
                            rows={2}
                            value={trackers}
                            onChange={(e) => setTrackers(e.target.value)}
                            placeholder="udp://tracker.opentrackr.org:1337/announce"
                            className="w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-1.5 font-mono text-sm text-foreground outline-none focus:border-primary"
                          />
                        </div>
                      )}
                    </div>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      Enter a download source to view supported engine controls.
                    </span>
                  )}
                </div>
              )}
            </div>
          </div>
        </>
      )}

      {error && (
        <p className="mt-2 text-xs text-destructive" role="alert">
          {error}
        </p>
      )}
    </form>
  );
};
