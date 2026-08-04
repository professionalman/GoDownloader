import { Loader2 } from 'lucide-react';
import type { JobCapabilities } from '../../types';

interface AdvancedDownloadOptionsProps {
  capabilities: JobCapabilities | null;
  loading: boolean;
  downloadLimitMiB: string;
  onDownloadLimitChange: (val: string) => void;
  uploadLimitMiB: string;
  onUploadLimitChange: (val: string) => void;
  proxyMode: 'disabled' | 'system' | 'custom';
  onProxyModeChange: (val: 'disabled' | 'system' | 'custom') => void;
  proxyProtocol: 'http' | 'https' | 'socks5';
  onProxyProtocolChange: (val: 'http' | 'https' | 'socks5') => void;
  proxyHost: string;
  onProxyHostChange: (val: string) => void;
  proxyPort: string;
  onProxyPortChange: (val: string) => void;
  proxyUsername: string;
  onProxyUsernameChange: (val: string) => void;
  proxyPassword: string;
  onProxyPasswordChange: (val: string) => void;
  userAgent: string;
  onUserAgentChange: (val: string) => void;
  headersRaw: string;
  onHeadersRawChange: (val: string) => void;
  maxAttempts: string;
  onMaxAttemptsChange: (val: string) => void;
  retryWaitSeconds: string;
  onRetryWaitSecondsChange: (val: string) => void;
  connectTimeoutSeconds: string;
  onConnectTimeoutSecondsChange: (val: string) => void;
  requestTimeoutSeconds: string;
  onRequestTimeoutSecondsChange: (val: string) => void;
  split: string;
  onSplitChange: (val: string) => void;
  maxConnectionsPerServer: string;
  onMaxConnectionsPerServerChange: (val: string) => void;
  minSplitMiB: string;
  onMinSplitMiBChange: (val: string) => void;
  trackersRaw: string;
  onTrackersRawChange: (val: string) => void;
  disabled?: boolean;
}

export function AdvancedDownloadOptions(props: AdvancedDownloadOptionsProps) {
  if (props.loading) {
    return (
      <div className="mt-2.5 rounded border border-border bg-surface-2/40 p-3 text-xs text-muted-foreground flex items-center gap-2">
        <Loader2 className="size-3.5 animate-spin text-primary" />
        <span>Resolving supported engine capabilities…</span>
      </div>
    );
  }

  if (!props.capabilities) {
    return (
      <div className="mt-2.5 rounded border border-border bg-surface-2/40 p-3 text-xs text-muted-foreground">
        <span>Enter a valid URL or magnet link to inspect supported advanced controls.</span>
      </div>
    );
  }

  const { capabilities } = props;
  const controlClass =
    'h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground outline-none focus:border-primary disabled:opacity-50';

  return (
    <div className="mt-2.5 border-t border-border pt-2.5 space-y-3">
      {/* Bandwidth section */}
      {(capabilities.downloadLimit.supported || capabilities.uploadLimit.supported) && (
        <div className="space-y-1.5">
          <h4 className="text-xs font-semibold text-foreground">Bandwidth Limits</h4>
          <div className="grid gap-2.5 sm:grid-cols-2">
            {capabilities.downloadLimit.supported && (
              <div className="space-y-1">
                <label htmlFor="download-limit-input" className="text-xs font-medium text-muted-foreground">
                  Download limit (MiB/s)
                </label>
                <input
                  id="download-limit-input"
                  type="number"
                  min="0"
                  step="0.1"
                  value={props.downloadLimitMiB}
                  onChange={(e) => props.onDownloadLimitChange(e.target.value)}
                  placeholder="Unlimited"
                  disabled={props.disabled}
                  className={controlClass}
                />
              </div>
            )}

            {capabilities.uploadLimit.supported && (
              <div className="space-y-1">
                <label htmlFor="upload-limit-input" className="text-xs font-medium text-muted-foreground">
                  Upload limit (MiB/s)
                </label>
                <input
                  id="upload-limit-input"
                  type="number"
                  min="0"
                  step="0.1"
                  value={props.uploadLimitMiB}
                  onChange={(e) => props.onUploadLimitChange(e.target.value)}
                  placeholder="Unlimited"
                  disabled={props.disabled}
                  className={controlClass}
                />
              </div>
            )}
          </div>
        </div>
      )}

      {/* Proxy & Headers section */}
      {(capabilities.proxy.supported || capabilities.userAgent.supported || capabilities.customHeaders.supported) && (
        <div className="space-y-1.5">
          <h4 className="text-xs font-semibold text-foreground">Proxy & Headers</h4>
          <div className="grid gap-2.5 sm:grid-cols-2">
            {capabilities.proxy.supported && (
              <>
                <div className="space-y-1">
                  <label htmlFor="proxy-mode-select" className="text-xs font-medium text-muted-foreground">
                    Proxy mode
                  </label>
                  <select
                    id="proxy-mode-select"
                    value={props.proxyMode}
                    onChange={(e) => props.onProxyModeChange(e.target.value as 'disabled' | 'system' | 'custom')}
                    disabled={props.disabled}
                    className={controlClass}
                  >
                    <option value="disabled">Disabled</option>
                    <option value="system">System proxy</option>
                    <option value="custom">Custom proxy</option>
                  </select>
                </div>

                {props.proxyMode === 'custom' && (
                  <>
                    <div className="space-y-1">
                      <label htmlFor="proxy-protocol-select" className="text-xs font-medium text-muted-foreground">
                        Proxy protocol
                      </label>
                      <select
                        id="proxy-protocol-select"
                        value={props.proxyProtocol}
                        onChange={(e) => props.onProxyProtocolChange(e.target.value as 'http' | 'https' | 'socks5')}
                        disabled={props.disabled}
                        className={controlClass}
                      >
                        <option value="http">HTTP</option>
                        <option value="https">HTTPS</option>
                        <option value="socks5">SOCKS5</option>
                      </select>
                    </div>

                    <div className="space-y-1">
                      <label htmlFor="proxy-host-input" className="text-xs font-medium text-muted-foreground">
                        Proxy host
                      </label>
                      <input
                        id="proxy-host-input"
                        type="text"
                        value={props.proxyHost}
                        onChange={(e) => props.onProxyHostChange(e.target.value)}
                        placeholder="127.0.0.1"
                        disabled={props.disabled}
                        className={controlClass}
                      />
                    </div>

                    <div className="space-y-1">
                      <label htmlFor="proxy-port-input" className="text-xs font-medium text-muted-foreground">
                        Proxy port
                      </label>
                      <input
                        id="proxy-port-input"
                        type="number"
                        value={props.proxyPort}
                        onChange={(e) => props.onProxyPortChange(e.target.value)}
                        placeholder="8080"
                        disabled={props.disabled}
                        className={controlClass}
                      />
                    </div>

                    <div className="space-y-1">
                      <label htmlFor="proxy-username-input" className="text-xs font-medium text-muted-foreground">
                        Proxy username
                      </label>
                      <input
                        id="proxy-username-input"
                        type="text"
                        value={props.proxyUsername}
                        onChange={(e) => props.onProxyUsernameChange(e.target.value)}
                        placeholder="Optional"
                        disabled={props.disabled}
                        className={controlClass}
                      />
                    </div>

                    <div className="space-y-1">
                      <label htmlFor="proxy-password-input" className="text-xs font-medium text-muted-foreground">
                        Proxy password
                      </label>
                      <input
                        id="proxy-password-input"
                        type="password"
                        value={props.proxyPassword}
                        onChange={(e) => props.onProxyPasswordChange(e.target.value)}
                        placeholder="Optional"
                        disabled={props.disabled}
                        className={controlClass}
                      />
                    </div>
                  </>
                )}
              </>
            )}

            {capabilities.userAgent.supported && (
              <div className="space-y-1 sm:col-span-2">
                <label htmlFor="user-agent-input" className="text-xs font-medium text-muted-foreground">
                  User-Agent string
                </label>
                <input
                  id="user-agent-input"
                  type="text"
                  value={props.userAgent}
                  onChange={(e) => props.onUserAgentChange(e.target.value)}
                  placeholder="Mozilla/5.0..."
                  disabled={props.disabled}
                  className={controlClass}
                />
              </div>
            )}

            {capabilities.customHeaders.supported && (
              <div className="space-y-1 sm:col-span-2">
                <label htmlFor="custom-headers-textarea" className="text-xs font-medium text-muted-foreground">
                  Custom HTTP Headers (Name: Value per line)
                </label>
                <textarea
                  id="custom-headers-textarea"
                  rows={2}
                  value={props.headersRaw}
                  onChange={(e) => props.onHeadersRawChange(e.target.value)}
                  placeholder="Authorization: Bearer token&#10;Referer: https://example.com"
                  disabled={props.disabled}
                  className="w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-1.5 font-mono text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
                />
              </div>
            )}
          </div>
        </div>
      )}

      {/* Retry & Timeout section */}
      {(capabilities.retryPolicy.supported || capabilities.timeoutPolicy.supported) && (
        <div className="space-y-1.5">
          <h4 className="text-xs font-semibold text-foreground">Retry & Timeout Policy</h4>
          <div className="grid gap-2.5 sm:grid-cols-2 xl:grid-cols-4">
            {capabilities.retryPolicy.supported && (
              <>
                <div className="space-y-1">
                  <label htmlFor="max-attempts-input" className="text-xs font-medium text-muted-foreground">
                    Max attempts
                  </label>
                  <input
                    id="max-attempts-input"
                    type="number"
                    min="1"
                    value={props.maxAttempts}
                    onChange={(e) => props.onMaxAttemptsChange(e.target.value)}
                    placeholder="3"
                    disabled={props.disabled}
                    className={controlClass}
                  />
                </div>

                <div className="space-y-1">
                  <label htmlFor="retry-wait-input" className="text-xs font-medium text-muted-foreground">
                    Retry wait (seconds)
                  </label>
                  <input
                    id="retry-wait-input"
                    type="number"
                    min="0"
                    value={props.retryWaitSeconds}
                    onChange={(e) => props.onRetryWaitSecondsChange(e.target.value)}
                    placeholder="5"
                    disabled={props.disabled}
                    className={controlClass}
                  />
                </div>
              </>
            )}

            {capabilities.timeoutPolicy.supported && (
              <>
                <div className="space-y-1">
                  <label htmlFor="connect-timeout-input" className="text-xs font-medium text-muted-foreground">
                    Connect timeout (sec)
                  </label>
                  <input
                    id="connect-timeout-input"
                    type="number"
                    min="1"
                    value={props.connectTimeoutSeconds}
                    onChange={(e) => props.onConnectTimeoutSecondsChange(e.target.value)}
                    placeholder="30"
                    disabled={props.disabled}
                    className={controlClass}
                  />
                </div>

                <div className="space-y-1">
                  <label htmlFor="request-timeout-input" className="text-xs font-medium text-muted-foreground">
                    Request timeout (sec)
                  </label>
                  <input
                    id="request-timeout-input"
                    type="number"
                    min="1"
                    value={props.requestTimeoutSeconds}
                    onChange={(e) => props.onRequestTimeoutSecondsChange(e.target.value)}
                    placeholder="60"
                    disabled={props.disabled}
                    className={controlClass}
                  />
                </div>
              </>
            )}
          </div>
        </div>
      )}

      {/* Direct Connection tuning */}
      {capabilities.connections.supported && (
        <div className="space-y-1.5">
          <h4 className="text-xs font-semibold text-foreground">Direct Download Tuning</h4>
          <div className="grid gap-2.5 sm:grid-cols-3">
            <div className="space-y-1">
              <label htmlFor="split-connections-input" className="text-xs font-medium text-muted-foreground">
                Connections (split)
              </label>
              <input
                id="split-connections-input"
                type="number"
                min="1"
                max="16"
                value={props.split}
                onChange={(e) => props.onSplitChange(e.target.value)}
                placeholder="5"
                disabled={props.disabled}
                className={controlClass}
              />
            </div>

            <div className="space-y-1">
              <label htmlFor="max-per-server-input" className="text-xs font-medium text-muted-foreground">
                Max per server
              </label>
              <input
                id="max-per-server-input"
                type="number"
                min="1"
                value={props.maxConnectionsPerServer}
                onChange={(e) => props.onMaxConnectionsPerServerChange(e.target.value)}
                placeholder="1"
                disabled={props.disabled}
                className={controlClass}
              />
            </div>

            <div className="space-y-1">
              <label htmlFor="min-split-input" className="text-xs font-medium text-muted-foreground">
                Min split size (MiB)
              </label>
              <input
                id="min-split-input"
                type="number"
                min="1"
                value={props.minSplitMiB}
                onChange={(e) => props.onMinSplitMiBChange(e.target.value)}
                placeholder="20"
                disabled={props.disabled}
                className={controlClass}
              />
            </div>
          </div>
        </div>
      )}

      {/* Trackers */}
      {capabilities.trackers.supported && (
        <div className="space-y-1.5">
          <h4 className="text-xs font-semibold text-foreground">Custom Trackers</h4>
          <textarea
            id="trackers-textarea"
            rows={2}
            value={props.trackersRaw}
            onChange={(e) => props.onTrackersRawChange(e.target.value)}
            placeholder="udp://tracker.opentrackr.org:1337/announce (one per line)"
            disabled={props.disabled}
            className="w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-1.5 font-mono text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
          />
        </div>
      )}
    </div>
  );
}
