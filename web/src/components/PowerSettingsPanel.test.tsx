import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { PowerSettingsPanel } from './PowerSettingsPanel';

vi.mock('../api', () => ({
  getTrackerSources: vi.fn().mockResolvedValue([]),
  createTrackerSource: vi.fn(), deleteTrackerSource: vi.fn(),
  refreshAllTrackerSources: vi.fn(), refreshTrackerSource: vi.fn(),
}));

it('masks configured secrets and sends explicit clear semantics', async () => {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<PowerSettingsPanel settings={{
    network: {
      globalDownloadLimitBytesPerSecond: 0,
      proxy: { mode: 'custom', protocol: 'http', host: 'proxy.local', port: 8080, hasPassword: true },
      userAgent: '', httpHeaders: [], retryPolicy: { maxAttempts: 0, retryWaitSeconds: 0 },
      timeoutPolicy: { connectTimeoutSeconds: 0, requestTimeoutSeconds: 0 },
      directConnections: { split: 5, maxConnectionsPerServer: 1, minSplitSizeBytes: 20 << 20 },
    },
    torrent: {
      downloadLimitBytesPerSecond: 0, uploadLimitBytesPerSecond: 0,
      seedingPolicy: { mode: 'none' }, applyTrackerSubscriptionsToNewTorrents: false,
      manageQBitGlobalNetworkSettings: false,
    },
  }} onSave={onSave} />);
  expect(screen.getByText('Configured')).toBeInTheDocument();
  expect(screen.queryByDisplayValue(/secret/i)).not.toBeInTheDocument();
  fireEvent.click(screen.getByLabelText('Clear configured password'));
  fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
  expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
    network: expect.objectContaining({ clearProxyPassword: true }),
  }));
});
