import React, { useState, useEffect, useRef } from 'react';
import type { JobPriority, Category, FilenameConflictPolicy, JobNetworkPolicyOverride, SeedingPolicy, JobCapabilities } from '../types';
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

export const DownloadForm: React.FC<DownloadFormProps> = ({ onSubmit, onUploadTorrent, disabled }) => {
  const [inputText, setInputText] = useState('');
  const [priority, setPriority] = useState<JobPriority>('normal');
  const [categories, setCategories] = useState<Category[]>([]);
  const [selectedCategoryId, setSelectedCategoryId] = useState<string>('');
  const [customDestDir, setCustomDestDir] = useState<string>('');
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>('rename');
  const [error, setError] = useState('');
  const [advanced, setAdvanced] = useState(false);
  const [capabilities, setCapabilities] = useState<JobCapabilities | null>(null);
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
    const sources = inputText.split('\n').map((value) => value.trim()).filter(Boolean);
    if (!sources.length) {
      setCapabilities(null);
      return;
    }
    const timer = window.setTimeout(() => resolveCapabilities(sources.length === 1 ? sources[0] : sources).then(setCapabilities).catch(() => setCapabilities(null)), 250);
    return () => window.clearTimeout(timer);
  }, [inputText]);

  const buildPolicy = (): JobNetworkPolicyOverride | undefined => {
    const policy: JobNetworkPolicyOverride = {};
    if (downloadLimit !== '') policy.downloadLimitBytesPerSecond = Number(downloadLimit);
    if (uploadLimit !== '') policy.uploadLimitBytesPerSecond = Number(uploadLimit);
    if (capabilities?.proxy.supported) {
      policy.proxy = { mode: proxyMode };
      if (proxyMode === 'custom') {
        policy.proxy = { mode: proxyMode, protocol: proxyProtocol, host: proxyHost, port: Number(proxyPort), username: proxyUsername };
        if (proxyPassword) policy.proxyPassword = proxyPassword;
      }
    }
    if (userAgent) policy.userAgent = userAgent;
    if (headers.trim()) {
      policy.httpHeaders = headers.split('\n').filter(Boolean).map((line) => {
        const separator = line.indexOf(':');
        return { name: line.slice(0, separator).trim(), value: line.slice(separator + 1).trim() };
      });
    }
    if (maxAttempts !== '' || retryWait !== '') policy.retryPolicy = { maxAttempts: Number(maxAttempts || 0), retryWaitSeconds: Number(retryWait || 0) };
    if (connectTimeout !== '' || requestTimeout !== '') policy.timeoutPolicy = { connectTimeoutSeconds: Number(connectTimeout || 0), requestTimeoutSeconds: Number(requestTimeout || 0) };
    if (split !== '' || connections !== '' || minSplitMiB !== '') {
      policy.directConnections = {
        split: Number(split || 5),
        maxConnectionsPerServer: Number(connections || 1),
        minSplitSizeBytes: Number(minSplitMiB || 20) * (1 << 20),
      };
    }
    return Object.keys(policy).length ? policy : undefined;
  };

  const handleCategoryChange = (catId: string) => {
    setSelectedCategoryId(catId);
    if (catId) {
      setCustomDestDir(''); // Mutually exclusive with custom destination
    }
  };

  const handleCustomDestChange = (dir: string) => {
    setCustomDestDir(dir);
    if (dir.trim()) {
      setSelectedCategoryId(''); // Mutually exclusive with category
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const lines = inputText
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0);

    if (lines.length === 0) {
      setError('Please enter at least one URL or Magnet link');
      return;
    }

    if (lines.length > 100) {
      setError('Batch submission cannot exceed 100 links at once');
      return;
    }

    if (selectedCategoryId && customDestDir.trim()) {
      setError('Cannot specify both Category and Custom Destination Folder');
      return;
    }

    setError('');
    onSubmit(
      lines,
      priority,
      selectedCategoryId || undefined,
      customDestDir.trim() || undefined,
      conflictPolicy
      ,
      buildPolicy(),
      undefined,
      trackers.split('\n').map((value) => value.trim()).filter(Boolean)
    );
    setInputText('');
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file && onUploadTorrent) {
      onUploadTorrent(
        file,
        priority,
        selectedCategoryId || undefined,
        customDestDir.trim() || undefined,
        buildPolicy(),
        undefined,
        trackers.split('\n').map((value) => value.trim()).filter(Boolean)
      );
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  return (
    <form className="download-form" onSubmit={handleSubmit}>
      <div className="form-controls">
        <textarea
          className="url-input multiline-input"
          placeholder="Paste URL(s) or Magnet link(s)... One link per line for batch downloads"
          rows={3}
          value={inputText}
          onChange={(e) => {
            setInputText(e.target.value);
            setError('');
          }}
          disabled={disabled}
        />

        <div className="form-options-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: '0.8rem', marginTop: '0.8rem', marginBottom: '0.8rem' }}>
          <div className="form-option-group">
            <label htmlFor="category-select" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Category:</label>
            <select
              id="category-select"
              className="select-dropdown"
              style={{ width: '100%', padding: '0.4rem' }}
              value={selectedCategoryId}
              onChange={(e) => handleCategoryChange(e.target.value)}
              disabled={disabled}
            >
              <option value="">(None - Default Download Folder)</option>
              {categories.map((cat) => (
                <option key={cat.id} value={cat.id}>
                  {cat.name} ({cat.directory})
                </option>
              ))}
            </select>
          </div>

          <div className="form-option-group">
            <label htmlFor="custom-dest-dir" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Custom Folder:</label>
            <input
              id="custom-dest-dir"
              type="text"
              className="input-text"
              placeholder="e.g. /custom/path"
              style={{ width: '100%', padding: '0.4rem' }}
              value={customDestDir}
              onChange={(e) => handleCustomDestChange(e.target.value)}
              disabled={disabled || !!selectedCategoryId}
            />
          </div>

          <div className="form-option-group">
            <label htmlFor="conflict-policy-select" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Conflict Policy:</label>
            <select
              id="conflict-policy-select"
              className="select-dropdown"
              style={{ width: '100%', padding: '0.4rem' }}
              value={conflictPolicy}
              onChange={(e) => setConflictPolicy(e.target.value as FilenameConflictPolicy)}
              disabled={disabled}
            >
              <option value="rename">Auto Rename</option>
              <option value="overwrite">Overwrite</option>
              <option value="fail">Fail on Conflict</option>
            </select>
          </div>
        </div>

        <details open={advanced} onToggle={(event) => setAdvanced(event.currentTarget.open)} className="advanced-network-controls">
          <summary>Advanced Network Controls</summary>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(190px, 1fr))', gap: '0.7rem', padding: '0.8rem 0' }}>
            {capabilities?.downloadLimit.supported && <label>Download limit (bytes/s)<input type="number" min="0" value={downloadLimit} onChange={(e) => setDownloadLimit(e.target.value)} placeholder="Inherit" /></label>}
            {capabilities?.uploadLimit.supported && <label>Upload limit (bytes/s)<input type="number" min="0" value={uploadLimit} onChange={(e) => setUploadLimit(e.target.value)} placeholder="Inherit" /></label>}
            {capabilities?.proxy.supported && <label>Proxy<select value={proxyMode} onChange={(e) => setProxyMode(e.target.value as typeof proxyMode)}><option value="disabled">Disabled</option><option value="system">System</option><option value="custom">Custom</option></select></label>}
            {capabilities?.proxy.supported && proxyMode === 'custom' && <>
              <label>Protocol<select value={proxyProtocol} onChange={(e) => setProxyProtocol(e.target.value as typeof proxyProtocol)}>{capabilities.proxy.supportedProtocols?.map((protocol) => <option key={protocol}>{protocol}</option>)}</select></label>
              <label>Proxy host<input value={proxyHost} onChange={(e) => setProxyHost(e.target.value)} /></label>
              <label>Proxy port<input type="number" min="1" max="65535" value={proxyPort} onChange={(e) => setProxyPort(e.target.value)} /></label>
              <label>Proxy username<input value={proxyUsername} onChange={(e) => setProxyUsername(e.target.value)} /></label>
              <label>Proxy password<input type="password" value={proxyPassword} onChange={(e) => setProxyPassword(e.target.value)} placeholder="Leave empty to preserve" /></label>
            </>}
            {capabilities?.userAgent.supported && <label>User-Agent<input value={userAgent} onChange={(e) => setUserAgent(e.target.value)} /></label>}
            {capabilities?.customHeaders.supported && <label style={{ gridColumn: '1 / -1' }}>Headers (one Name: value per line)<textarea value={headers} onChange={(e) => setHeaders(e.target.value)} /></label>}
            {capabilities?.retryPolicy.supported && <><label>Maximum attempts<input type="number" min="0" max="100" value={maxAttempts} onChange={(e) => setMaxAttempts(e.target.value)} placeholder="Inherit" /></label><label>Retry wait (seconds)<input type="number" min="0" max="3600" value={retryWait} onChange={(e) => setRetryWait(e.target.value)} placeholder="Inherit" /></label></>}
            {capabilities?.timeoutPolicy.supported && <><label>Connect timeout (seconds)<input type="number" min="0" max="86400" value={connectTimeout} onChange={(e) => setConnectTimeout(e.target.value)} placeholder="Inherit" /></label><label>Request timeout (seconds)<input type="number" min="0" max="86400" value={requestTimeout} onChange={(e) => setRequestTimeout(e.target.value)} placeholder="Inherit" /></label></>}
            {capabilities?.connections.supported && <><label>Splits<input type="number" min="1" max="16" value={split} onChange={(e) => setSplit(e.target.value)} placeholder="Inherit" /></label><label>Connections/server<input type="number" min="1" max="16" value={connections} onChange={(e) => setConnections(e.target.value)} placeholder="Inherit" /></label><label>Minimum split (MiB)<input type="number" min="1" max="1024" value={minSplitMiB} onChange={(e) => setMinSplitMiB(e.target.value)} placeholder="Inherit" /></label></>}
            {capabilities?.trackers.supported && <label style={{ gridColumn: '1 / -1' }}>Custom trackers (one URL per line)<textarea value={trackers} onChange={(e) => setTrackers(e.target.value)} /></label>}
            {!capabilities && <span>Enter a source to see supported controls.</span>}
          </div>
        </details>

        <div className="form-action-bar">
          <div className="priority-selector">
            <label htmlFor="job-priority-select">Priority:</label>
            <select
              id="job-priority-select"
              className="priority-select"
              value={priority}
              onChange={(e) => setPriority(e.target.value as JobPriority)}
              disabled={disabled}
            >
              <option value="high">🔥 High</option>
              <option value="normal">⚡ Normal</option>
              <option value="low">🐢 Low</option>
            </select>
          </div>

          <div className="button-group">
            {onUploadTorrent && (
              <>
                <input
                  type="file"
                  accept=".torrent"
                  style={{ display: 'none' }}
                  ref={fileInputRef}
                  onChange={handleFileChange}
                  disabled={disabled}
                />
                <button
                  type="button"
                  className="btn btn-secondary btn-upload-torrent"
                  onClick={() => fileInputRef.current?.click()}
                  disabled={disabled}
                >
                  📁 Add .torrent
                </button>
              </>
            )}
            <button
              type="submit"
              className="btn btn-primary"
              disabled={disabled || !inputText.trim()}
            >
              Start Download
            </button>
          </div>
        </div>
      </div>
      {error && <div className="form-error" style={{ marginTop: '0.5rem' }}>{error}</div>}
    </form>
  );
};
