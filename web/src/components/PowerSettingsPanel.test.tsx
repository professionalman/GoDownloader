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

describe('PowerSettingsPanel qBittorrent connection settings', () => {
  const qbitSettings: AppSettings = {
    ...settings,
    qbittorrent: {
      url: 'http://127.0.0.1:8081',
      username: 'admin',
      hasPassword: true,
      urlEnvironmentOverride: false,
      usernameEnvironmentOverride: false,
      passwordEnvironmentOverride: false,
    },
  };

  it('populates fields from safe DTO and never renders stored password in input', () => {
    render(<PowerSettingsPanel settings={qbitSettings} onSave={vi.fn()} />);
    expect(screen.getByLabelText('qBittorrent Web API endpoint URL')).toHaveValue('http://127.0.0.1:8081');
    expect(screen.getByLabelText('qBittorrent username')).toHaveValue('admin');
    expect(screen.getByLabelText('qBittorrent password')).toHaveValue('');
    expect(screen.getByLabelText('qBittorrent password').closest('label')).toHaveTextContent('Configured');
  });

  it('leaves password unchanged when password input is left blank on save', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PowerSettingsPanel settings={qbitSettings} onSave={onSave} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      qbittorrent: {
        url: 'http://127.0.0.1:8081',
        username: 'admin',
        password: undefined,
        clearPassword: false,
      },
    })));
  });

  it('sends explicit clear action with clearPassword=true', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PowerSettingsPanel settings={qbitSettings} onSave={onSave} />);
    fireEvent.click(screen.getByLabelText('Clear configured qBittorrent password'));
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      qbittorrent: {
        url: 'http://127.0.0.1:8081',
        username: 'admin',
        password: undefined,
        clearPassword: true,
      },
    })));
  });

  it('sends replacement password in write-only field and clears input after save', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PowerSettingsPanel settings={qbitSettings} onSave={onSave} />);
    fireEvent.change(screen.getByLabelText('qBittorrent password'), { target: { value: 'brand-new-secret-999' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    await waitFor(() => expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
      qbittorrent: {
        url: 'http://127.0.0.1:8081',
        username: 'admin',
        password: 'brand-new-secret-999',
        clearPassword: false,
      },
    })));
    expect(screen.getByLabelText('qBittorrent password')).toHaveValue('');
    expect(screen.queryByText('brand-new-secret-999')).not.toBeInTheDocument();
  });

  it('displays restart-required message after successful qB settings save', async () => {
    const onSave = vi.fn().mockResolvedValue(undefined);
    render(<PowerSettingsPanel settings={qbitSettings} onSave={onSave} />);
    fireEvent.click(screen.getByRole('button', { name: 'Save network controls' }));
    expect(await screen.findByText(/Restart GoDownloader to apply qBittorrent connection changes/)).toBeInTheDocument();
  });

  it('shows environment override notice and disables inputs when environment overrides are active', () => {
    const overriddenSettings: AppSettings = {
      ...settings,
      qbittorrent: {
        url: 'http://env-host:9999',
        username: 'env-user',
        hasPassword: true,
        urlEnvironmentOverride: true,
        usernameEnvironmentOverride: true,
        passwordEnvironmentOverride: true,
      },
    };
    render(<PowerSettingsPanel settings={overriddenSettings} onSave={vi.fn()} />);
    expect(screen.getByText('(Overridden by QBIT_URL)')).toBeInTheDocument();
    expect(screen.getByText('(Overridden by QBIT_USERNAME)')).toBeInTheDocument();
    expect(screen.getByText('(Overridden by QBIT_PASSWORD)')).toBeInTheDocument();
    expect(screen.getByLabelText('qBittorrent Web API endpoint URL')).toBeDisabled();
    expect(screen.getByLabelText('qBittorrent username')).toBeDisabled();
    expect(screen.getByLabelText('qBittorrent password')).toBeDisabled();
  });

  it('disables shared qB global network settings control and displays explanation', () => {
    render(<PowerSettingsPanel settings={qbitSettings} onSave={vi.fn()} />);
    const checkbox = screen.getByLabelText(/Manage dedicated qB global network settings/i);
    expect(checkbox).toBeDisabled();
    expect(checkbox).not.toBeChecked();
    expect(screen.getByText(/Global qBittorrent network settings are unavailable while using a shared, user-managed qBittorrent instance/)).toBeInTheDocument();
  });
});
