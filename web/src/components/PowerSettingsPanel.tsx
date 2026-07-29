import React, { useEffect, useState } from 'react';
import type { AppSettings, ProxyMode, ProxyProtocol, SeedingMode, SeedingPolicy, TrackerSource, UpdateSettingsPayload } from '../types';
import { createTrackerSource, deleteTrackerSource, getTrackerSources, refreshAllTrackerSources, refreshTrackerSource } from '../api';

interface Props {
  settings: AppSettings | null;
  onSave: (payload: UpdateSettingsPayload) => Promise<void>;
}

export const PowerSettingsPanel: React.FC<Props> = ({ settings, onSave }) => {
  const network = settings?.network;
  const torrent = settings?.torrent;
  const [globalLimit, setGlobalLimit] = useState(network?.globalDownloadLimitBytesPerSecond ?? 0);
  const [proxyMode, setProxyMode] = useState<ProxyMode>(network?.proxy.mode ?? 'disabled');
  const [proxyProtocol, setProxyProtocol] = useState<ProxyProtocol>(network?.proxy.protocol ?? 'http');
  const [proxyHost, setProxyHost] = useState(network?.proxy.host ?? '');
  const [proxyPort, setProxyPort] = useState(network?.proxy.port ?? 8080);
  const [proxyUsername, setProxyUsername] = useState(network?.proxy.username ?? '');
  const [proxyPassword, setProxyPassword] = useState('');
  const [clearPassword, setClearPassword] = useState(false);
  const [userAgent, setUserAgent] = useState(network?.userAgent ?? '');
  const [headerLines, setHeaderLines] = useState((network?.httpHeaders ?? []).map((header) =>
    `${header.name}: ${header.sensitive && header.hasValue ? '<configured>' : (header.value ?? '')}`
  ).join('\n'));
  const [maxAttempts, setMaxAttempts] = useState(network?.retryPolicy.maxAttempts ?? 0);
  const [retryWait, setRetryWait] = useState(network?.retryPolicy.retryWaitSeconds ?? 0);
  const [connectTimeout, setConnectTimeout] = useState(network?.timeoutPolicy.connectTimeoutSeconds ?? 0);
  const [requestTimeout, setRequestTimeout] = useState(network?.timeoutPolicy.requestTimeoutSeconds ?? 0);
  const [split, setSplit] = useState(network?.directConnections.split ?? 5);
  const [connections, setConnections] = useState(network?.directConnections.maxConnectionsPerServer ?? 1);
  const [minSplitMiB, setMinSplitMiB] = useState((network?.directConnections.minSplitSizeBytes ?? 20 << 20) / (1 << 20));
  const [torrentDownload, setTorrentDownload] = useState(torrent?.downloadLimitBytesPerSecond ?? 0);
  const [torrentUpload, setTorrentUpload] = useState(torrent?.uploadLimitBytesPerSecond ?? 0);
  const [seedingMode, setSeedingMode] = useState<SeedingMode>(torrent?.seedingPolicy.mode ?? 'none');
  const [seedRatio, setSeedRatio] = useState(torrent?.seedingPolicy.ratioLimit ?? 1);
  const [seedHours, setSeedHours] = useState((torrent?.seedingPolicy.timeLimitSeconds ?? 86400) / 3600);
  const [autoTrackers, setAutoTrackers] = useState(torrent?.applyTrackerSubscriptionsToNewTorrents ?? false);
  const [manageQBit, setManageQBit] = useState(torrent?.manageQBitGlobalNetworkSettings ?? false);
  const [sources, setSources] = useState<TrackerSource[]>([]);
  const [sourceName, setSourceName] = useState('');
  const [sourceURL, setSourceURL] = useState('');
  const [status, setStatus] = useState('');

  useEffect(() => {
    getTrackerSources().then(setSources).catch(() => setSources([]));
  }, []);

  const savePower = async () => {
    const seedingPolicy: SeedingPolicy = { mode: seedingMode };
    if (seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') seedingPolicy.ratioLimit = seedRatio;
    if (seedingMode === 'duration' || seedingMode === 'ratio_or_duration') seedingPolicy.timeLimitSeconds = Math.round(seedHours * 3600);
    await onSave({
      network: {
        globalDownloadLimitBytesPerSecond: globalLimit,
        proxy: proxyMode === 'custom'
          ? { mode: proxyMode, protocol: proxyProtocol, host: proxyHost, port: proxyPort, username: proxyUsername }
          : { mode: proxyMode },
        proxyPassword: proxyPassword || undefined,
        clearProxyPassword: clearPassword,
        userAgent,
        httpHeaders: headerLines.split('\n').map((line) => line.trim()).filter(Boolean).map((line) => {
          const separator = line.indexOf(':');
          const name = line.slice(0, separator).trim();
          const value = line.slice(separator + 1).trim();
          if (value === '<configured>') return { name, hasValue: true, sensitive: true };
          if (value === '<clear>') return { name, clearValue: true };
          return { name, value };
        }),
        retryPolicy: { maxAttempts, retryWaitSeconds: retryWait },
        timeoutPolicy: { connectTimeoutSeconds: connectTimeout, requestTimeoutSeconds: requestTimeout },
        directConnections: { split, maxConnectionsPerServer: connections, minSplitSizeBytes: Math.round(minSplitMiB * (1 << 20)) },
      },
      torrent: {
        downloadLimitBytesPerSecond: torrentDownload,
        uploadLimitBytesPerSecond: torrentUpload,
        seedingPolicy,
        applyTrackerSubscriptionsToNewTorrents: autoTrackers,
        manageQBitGlobalNetworkSettings: manageQBit,
      },
    });
    setProxyPassword('');
    setClearPassword(false);
    setStatus('Network and torrent settings saved.');
  };

  const addSource = async () => {
    const source = await createTrackerSource({ name: sourceName, url: sourceURL, enabled: true, refreshIntervalSeconds: 3600 });
    setSources((current) => [...current, source]);
    setSourceName('');
    setSourceURL('');
  };

  return (
    <>
      <section className="setting-section" style={{ marginTop: '1.5rem' }}>
        <h4>Network</h4>
        <div className="power-grid">
          <label>Global download limit (bytes/s)<input type="number" min="0" value={globalLimit} onChange={(e) => setGlobalLimit(Number(e.target.value))} disabled={settings?.overrides?.['network.globalDownloadLimitBytesPerSecond']} /></label>
          <label>Proxy mode<select value={proxyMode} onChange={(e) => setProxyMode(e.target.value as ProxyMode)} disabled={settings?.overrides?.['network.proxy']}><option value="disabled">Disabled</option><option value="system">System</option><option value="custom">Custom</option></select></label>
          {proxyMode === 'custom' && <>
            <label>Protocol<select value={proxyProtocol} onChange={(e) => setProxyProtocol(e.target.value as ProxyProtocol)}><option value="http">HTTP</option><option value="https">HTTPS</option><option value="socks5">SOCKS5</option></select></label>
            <label>Host<input value={proxyHost} onChange={(e) => setProxyHost(e.target.value)} /></label>
            <label>Port<input type="number" min="1" max="65535" value={proxyPort} onChange={(e) => setProxyPort(Number(e.target.value))} /></label>
            <label>Username<input value={proxyUsername} onChange={(e) => setProxyUsername(e.target.value)} /></label>
          </>}
          <label>Proxy password <span className="secret-state">{network?.proxy.hasPassword ? 'Configured' : 'Not configured'}</span><input type="password" value={proxyPassword} onChange={(e) => { setProxyPassword(e.target.value); setClearPassword(false); }} placeholder={network?.proxy.hasPassword ? 'Replace' : 'Set password'} /></label>
          {network?.proxy.hasPassword && <label><input type="checkbox" checked={clearPassword} onChange={(e) => { setClearPassword(e.target.checked); if (e.target.checked) setProxyPassword(''); }} /> Clear configured password</label>}
          <label>User-Agent<input value={userAgent} onChange={(e) => setUserAgent(e.target.value)} disabled={settings?.overrides?.['network.userAgent']} /></label>
          <label style={{ gridColumn: '1 / -1' }}>HTTP headers<textarea value={headerLines} onChange={(e) => setHeaderLines(e.target.value)} placeholder="Name: value" /><span className="setting-hint">Sensitive values show as &lt;configured&gt;. Replace that marker with a new value, or use &lt;clear&gt;.</span></label>
          <label>Retry attempts<input type="number" min="0" max="100" value={maxAttempts} onChange={(e) => setMaxAttempts(Number(e.target.value))} /></label>
          <label>Retry wait (seconds)<input type="number" min="0" max="3600" value={retryWait} onChange={(e) => setRetryWait(Number(e.target.value))} /></label>
          <label>Connect timeout (seconds)<input type="number" min="0" max="86400" value={connectTimeout} onChange={(e) => setConnectTimeout(Number(e.target.value))} /></label>
          <label>Request timeout (seconds)<input type="number" min="0" max="86400" value={requestTimeout} onChange={(e) => setRequestTimeout(Number(e.target.value))} /></label>
        </div>
      </section>
      <section className="setting-section" style={{ marginTop: '1.5rem' }}>
        <h4>Direct Downloads</h4>
        <div className="power-grid">
          <label>Splits<input type="number" min="1" max="16" value={split} onChange={(e) => setSplit(Number(e.target.value))} /></label>
          <label>Connections/server<input type="number" min="1" max="16" value={connections} onChange={(e) => setConnections(Number(e.target.value))} /></label>
          <label>Minimum split (MiB)<input type="number" min="1" max="1024" value={minSplitMiB} onChange={(e) => setMinSplitMiB(Number(e.target.value))} /></label>
        </div>
      </section>
      <section className="setting-section" style={{ marginTop: '1.5rem' }}>
        <h4>Torrents</h4>
        <div className="power-grid">
          <label>Download limit (bytes/s)<input type="number" min="0" value={torrentDownload} onChange={(e) => setTorrentDownload(Number(e.target.value))} /></label>
          <label>Upload limit (bytes/s)<input type="number" min="0" value={torrentUpload} onChange={(e) => setTorrentUpload(Number(e.target.value))} /></label>
          <label>Default seeding policy<select value={seedingMode} onChange={(e) => setSeedingMode(e.target.value as SeedingMode)}><option value="none">None</option><option value="unlimited">Unlimited</option><option value="ratio">Ratio</option><option value="duration">Duration</option><option value="ratio_or_duration">Ratio or duration</option></select></label>
          {(seedingMode === 'ratio' || seedingMode === 'ratio_or_duration') && <label>Ratio target<input type="number" min="0.01" max="1000" value={seedRatio} onChange={(e) => setSeedRatio(Number(e.target.value))} /></label>}
          {(seedingMode === 'duration' || seedingMode === 'ratio_or_duration') && <label>Active hours<input type="number" min="0.01" value={seedHours} onChange={(e) => setSeedHours(Number(e.target.value))} /></label>}
          <label><input type="checkbox" checked={autoTrackers} onChange={(e) => setAutoTrackers(e.target.checked)} /> Apply subscriptions to new public torrents</label>
          <label><input type="checkbox" checked={manageQBit} onChange={(e) => setManageQBit(e.target.checked)} /> Manage dedicated qB global network settings</label>
        </div>
        <button type="button" className="btn btn-primary" onClick={() => savePower().catch((error) => setStatus(error.message))}>Save network controls</button>
        {status && <div className="setting-notice">{status}</div>}
      </section>
      <section className="setting-section" style={{ marginTop: '1.5rem' }}>
        <h4>Tracker Sources</h4>
        {sources.map((source) => <div key={source.id} className="tracker-source-row">
          <span><strong>{source.name}</strong> · {source.trackerCount} trackers {source.lastError && `· ${source.lastError}`}</span>
          <span><button type="button" className="btn btn-sm btn-secondary" onClick={() => refreshTrackerSource(source.id).then((updated) => setSources((all) => all.map((item) => item.id === updated.id ? updated : item)))}>Refresh</button> <button type="button" className="btn btn-sm btn-danger" onClick={() => deleteTrackerSource(source.id).then(() => setSources((all) => all.filter((item) => item.id !== source.id)))}>Delete</button></span>
        </div>)}
        <div className="power-grid"><label>Name<input value={sourceName} onChange={(e) => setSourceName(e.target.value)} /></label><label>HTTP(S) list URL<input value={sourceURL} onChange={(e) => setSourceURL(e.target.value)} /></label></div>
        <button type="button" className="btn btn-secondary" onClick={() => addSource().catch((error) => setStatus(error.message))}>Add source</button>{' '}
        <button type="button" className="btn btn-secondary" onClick={() => refreshAllTrackerSources().then(() => getTrackerSources().then(setSources))}>Refresh all</button>
      </section>
    </>
  );
};
