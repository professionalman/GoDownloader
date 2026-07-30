import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AppSettings, TrackerSource } from '../types';
import { PowerSettingsPanel } from './PowerSettingsPanel';
import * as api from '../api';

vi.mock('../api', () => ({
  getTrackerSources: vi.fn(),
  createTrackerSource: vi.fn(),
  updateTrackerSource: vi.fn(),
  deleteTrackerSource: vi.fn(),
  refreshAllTrackerSources: vi.fn(),
  refreshTrackerSource: vi.fn(),
}));

const settings: AppSettings = {
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
};

const trackerSource: TrackerSource = {
  id: 'source-1', name: 'Source One', url: 'http://localhost/trackers',
  enabled: true, refreshIntervalSeconds: 900, trackerCount: 42,
  lastCheckedAt: '2026-07-30T10:00:00Z', lastSuccessAt: '2026-07-30T09:00:00Z',
  lastError: 'temporary fetch failure',
};

function renderPanel(onSave = vi.fn().mockResolvedValue(undefined)) {
  render(<PowerSettingsPanel settings={settings} onSave={onSave} />);
  return onSave;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getTrackerSources).mockResolvedValue([]);
  vi.mocked(api.createTrackerSource).mockImplementation(async (input) => ({ ...input, id: 'created', trackerCount: 0 }));
  vi.mocked(api.updateTrackerSource).mockImplementation(async (id, input) => ({ ...input, id, trackerCount: 42 }));
  vi.mocked(api.deleteTrackerSource).mockResolvedValue(undefined);
  vi.mocked(api.refreshTrackerSource).mockResolvedValue(trackerSource);
  vi.mocked(api.refreshAllTrackerSources).mockResolvedValue({ failureCount: 0 });
});

describe('PowerSettingsPanel secrets and seeding fields', () => {
  it('renders the proxy secret masked', () => {
    renderPanel();
    expect(screen.getByText('Configured')).toBeInTheDocument();
    expect(screen.getByLabelText(/Proxy password/)).toHaveValue('');
  });

  it('replaces the proxy secret without clearing it', async () => {
    const onSave = renderPanel();
    fireEvent.change(screen.getByLabelText(/Proxy password/), { target: { value: 'replacement' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      network: expect.objectContaining({ proxyPassword: 'replacement', clearProxyPassword: false }),
    })));
  });

  it('sends explicit proxy secret clear semantics', async () => {
    const onSave = renderPanel();
    fireEvent.click(screen.getByLabelText('Clear configured password'));
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      network: expect.objectContaining({ clearProxyPassword: true }),
    })));
  });

  it.each([
    ['ratio', true, false],
    ['duration', false, true],
    ['ratio_or_duration', true, true],
  ] as const)('shows conditional fields for %s', (mode, ratioVisible, durationVisible) => {
    renderPanel();
    fireEvent.change(screen.getByLabelText('Default seeding policy'), { target: { value: mode } });
    expect(screen.queryByLabelText('Ratio target') != null).toBe(ratioVisible);
    expect(screen.queryByLabelText('Active hours') != null).toBe(durationVisible);
  });
});

describe('PowerSettingsPanel tracker source management', () => {
  beforeEach(() => {
    vi.mocked(api.getTrackerSources).mockResolvedValue([trackerSource]);
  });

  it('creates a source with configurable enabled state and refresh interval', async () => {
    renderPanel();
    await screen.findByDisplayValue('Source One');
    fireEvent.change(screen.getByLabelText('New tracker source name'), { target: { value: 'New Source' } });
    fireEvent.change(screen.getByLabelText('New tracker source URL'), { target: { value: 'http://127.0.0.1/list' } });
    fireEvent.change(screen.getByLabelText('New tracker refresh interval'), { target: { value: '1800' } });
    fireEvent.click(screen.getByLabelText('New tracker source enabled'));
    fireEvent.click(screen.getByRole('button', { name: 'Add source' }));
    await waitFor(() => expect(api.createTrackerSource).toHaveBeenCalledWith({
      name: 'New Source', url: 'http://127.0.0.1/list', enabled: false, refreshIntervalSeconds: 1800,
    }));
  });

  it('enables and disables a source through the update route', async () => {
    renderPanel();
    const enabled = await screen.findByLabelText('Enabled Source One');
    fireEvent.click(enabled);
    await waitFor(() => expect(api.updateTrackerSource).toHaveBeenCalledWith('source-1', expect.objectContaining({ enabled: false })));
  });

  it('edits a source and updates its refresh interval', async () => {
    renderPanel();
    await screen.findByDisplayValue('Source One');
    fireEvent.change(screen.getByLabelText('Tracker name Source One'), { target: { value: 'Edited Source' } });
    fireEvent.change(screen.getByLabelText('Refresh interval Source One'), { target: { value: '1800' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(api.updateTrackerSource).toHaveBeenCalledWith('source-1', {
      name: 'Edited Source', url: 'http://localhost/trackers', enabled: true, refreshIntervalSeconds: 1800,
    }));
  });

  it('refreshes and deletes an individual source and refreshes all sources', async () => {
    renderPanel();
    await screen.findByDisplayValue('Source One');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(api.refreshTrackerSource).toHaveBeenCalledWith('source-1'));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh all' }));
    await waitFor(() => expect(api.refreshAllTrackerSources).toHaveBeenCalled());
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.deleteTrackerSource).toHaveBeenCalledWith('source-1'));
  });

  it('renders last-success, last-error, checked time, and tracker count', async () => {
    renderPanel();
    await screen.findByText(/42 trackers/);
    expect(screen.getByText(/Last checked:/)).toBeInTheDocument();
    expect(screen.getByText(/Last successful refresh:/)).toBeInTheDocument();
    expect(screen.getByText(/Last error: temporary fetch failure/)).toBeInTheDocument();
  });
});
