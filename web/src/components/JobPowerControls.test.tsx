import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { CapabilityState, Job, JobCapabilities, SeedingPolicy } from '../types';
import { JobPowerControls } from './JobPowerControls';
import * as api from '../api';

vi.mock('../api', () => ({
  addTorrentTrackers: vi.fn(),
  getJobCapabilities: vi.fn(),
  updateJobNetwork: vi.fn(),
  updateSeedingPolicy: vi.fn(),
}));

const unsupported = (): CapabilityState => ({ supported: false, mutableNow: false });
const supported = (mutableNow = true): CapabilityState => ({ supported: true, mutableNow });

function capabilities(overrides: Partial<JobCapabilities> = {}): JobCapabilities {
  return {
    pause: unsupported(), resume: unsupported(), cancel: supported(), retry: unsupported(),
    downloadLimit: supported(), uploadLimit: supported(), proxy: unsupported(),
    userAgent: unsupported(), customHeaders: unsupported(), retryPolicy: unsupported(),
    timeoutPolicy: unsupported(), connections: unsupported(), fileSelection: unsupported(),
    trackers: supported(), seedingPolicy: supported(),
    ...overrides,
  };
}

function torrentJob(policy: SeedingPolicy = { mode: 'unlimited' }): Job {
  return {
    id: 'job-1', source: 'magnet:test', name: 'Torrent', status: 'seeding', type: 'torrent',
    progress: 100, totalBytes: 1024, completedBytes: 1024, speedBytesPerSecond: 0,
    etaSeconds: 0, engine: 'qbittorrent', createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    networkPolicy: {
      downloadLimitBytesPerSecond: 100, uploadLimitBytesPerSecond: 50,
      proxy: { mode: 'disabled' }, retryPolicy: { maxAttempts: 0, retryWaitSeconds: 0 },
      timeoutPolicy: { connectTimeoutSeconds: 0, requestTimeoutSeconds: 0 },
    },
    effectiveDownloadLimitBytesPerSecond: 100, effectiveUploadLimitBytesPerSecond: 50,
    seedingPolicy: policy,
    torrentInfo: {
      name: 'Torrent', infoHash: 'hash', totalSize: 1024, seeders: 12, leechers: 3,
      uploaded: 2048, uploadSpeed: 1536, ratio: 0.84, seedingTimeSeconds: 2820,
    },
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getJobCapabilities).mockResolvedValue(capabilities());
  vi.mocked(api.updateJobNetwork).mockResolvedValue(torrentJob());
  vi.mocked(api.updateSeedingPolicy).mockResolvedValue(torrentJob());
  vi.mocked(api.addTorrentTrackers).mockResolvedValue({ trackers: [] });
});

describe('JobPowerControls capability-driven mutations', () => {
  it('hides unsupported controls and disables supported immutable controls', async () => {
    vi.mocked(api.getJobCapabilities).mockResolvedValue(capabilities({
      downloadLimit: unsupported(), uploadLimit: supported(false), trackers: unsupported(), seedingPolicy: unsupported(),
    }));
    render(<JobPowerControls job={torrentJob()} />);
    expect(await screen.findByLabelText('Upload bytes/s')).toBeDisabled();
    expect(screen.queryByLabelText('Download bytes/s')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Apply limits' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Add trackers')).not.toBeInTheDocument();
  });

  it('mutates capability-supported download and upload limits', async () => {
    render(<JobPowerControls job={torrentJob()} />);
    fireEvent.change(await screen.findByLabelText('Download bytes/s'), { target: { value: '200' } });
    fireEvent.change(screen.getByLabelText('Upload bytes/s'), { target: { value: '75' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply limits' }));
    await waitFor(() => expect(api.updateJobNetwork).toHaveBeenCalledWith('job-1', {
      downloadLimitBytesPerSecond: 200, uploadLimitBytesPerSecond: 75,
    }));
  });

  it('updates the normalized seeding policy', async () => {
    render(<JobPowerControls job={torrentJob()} />);
    fireEvent.change(await screen.findByLabelText('Seeding policy'), { target: { value: 'ratio_or_duration' } });
    fireEvent.change(screen.getByLabelText('Ratio'), { target: { value: '1.5' } });
    fireEvent.change(screen.getByLabelText('Active hours'), { target: { value: '2' } });
    fireEvent.click(screen.getByRole('button', { name: 'Apply seeding policy' }));
    await waitFor(() => expect(api.updateSeedingPolicy).toHaveBeenCalledWith('job-1', {
      mode: 'ratio_or_duration', ratioLimit: 1.5, timeLimitSeconds: 7200,
    }));
  });
});

describe('JobPowerControls active seeding targets', () => {
  it('renders current and target ratio for a ratio policy', async () => {
    render(<JobPowerControls job={torrentJob({ mode: 'ratio', ratioLimit: 1.5 })} />);
    await screen.findByLabelText('Active seeding status');
    expect(screen.getByText('Ratio: 0.84 / 1.50')).toBeInTheDocument();
    expect(screen.getByText('Policy: Stop at ratio')).toBeInTheDocument();
  });

  it('renders current and target active duration for a duration policy', async () => {
    render(<JobPowerControls job={torrentJob({ mode: 'duration', timeLimitSeconds: 7200 })} />);
    await screen.findByLabelText('Active seeding status');
    expect(screen.getByText('Seeding time: 47m / 2h')).toBeInTheDocument();
    expect(screen.getByText('Policy: Stop after active seeding time')).toBeInTheDocument();
  });

  it('renders ratio, duration, upload, peers, and combined policy while seeding', async () => {
    render(<JobPowerControls job={torrentJob({ mode: 'ratio_or_duration', ratioLimit: 1.5, timeLimitSeconds: 7200 })} />);
    await screen.findByLabelText('Active seeding status');
    expect(screen.getByText('Ratio: 0.84 / 1.50')).toBeInTheDocument();
    expect(screen.getByText('Seeding time: 47m / 2h')).toBeInTheDocument();
    expect(screen.getByText('Upload speed: 1.5 KiB/s')).toBeInTheDocument();
    expect(screen.getByText('Seeders: 12 · Leechers: 3')).toBeInTheDocument();
    expect(screen.getByText('Policy: Stop at ratio or duration')).toBeInTheDocument();
  });

  it('renders unlimited policy without invented targets', async () => {
    render(<JobPowerControls job={torrentJob({ mode: 'unlimited' })} />);
    await screen.findByLabelText('Active seeding status');
    expect(screen.getByText('Ratio: 0.84')).toBeInTheDocument();
    expect(screen.getByText('Seeding time: 47m')).toBeInTheDocument();
    expect(screen.getByText('Policy: Unlimited')).toBeInTheDocument();
  });
});
