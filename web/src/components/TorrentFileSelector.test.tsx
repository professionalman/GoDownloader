import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Job, SeedingPolicy } from '../types';
import { TorrentFileSelector } from './TorrentFileSelector';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getTorrentFiles: vi.fn() };
});

const torrentFile = {
  index: 0, path: 'a.bin', size: 10, progress: 0, priority: 'normal' as const, selected: true,
};

function jobWithPolicy(policy: SeedingPolicy): Job {
  return {
    id: 'j1', name: 'Torrent', type: 'torrent', status: 'awaiting_selection',
    source: 'magnet:test', progress: 0, totalBytes: 10, completedBytes: 0,
    speedBytesPerSecond: 0, etaSeconds: 0, engine: 'qbittorrent',
    networkPolicy: {
      downloadLimitBytesPerSecond: 0, proxy: { mode: 'disabled' },
      retryPolicy: { maxAttempts: 0, retryWaitSeconds: 0 },
      timeoutPolicy: { connectTimeoutSeconds: 0, requestTimeoutSeconds: 0 },
    },
    effectiveDownloadLimitBytesPerSecond: 0, seedingPolicy: policy,
    createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };
}

async function renderSelector(policy: SeedingPolicy) {
  const onStart = vi.fn();
  render(<TorrentFileSelector job={jobWithPolicy(policy)} onStart={onStart} onClose={vi.fn()} />);
  await screen.findByText('a.bin');
  return onStart;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getTorrentFiles).mockResolvedValue([torrentFile]);
});

describe('TorrentFileSelector snapshotted seeding policy', () => {
  it('opens with snapshotted none and preserves it on Start', async () => {
    const onStart = await renderSelector({ mode: 'none' });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('none');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'none' });
  });

  it('opens with snapshotted unlimited and preserves it on Start', async () => {
    const onStart = await renderSelector({ mode: 'unlimited' });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('unlimited');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'unlimited' });
  });

  it('opens with snapshotted ratio 1.5 and Start sends ratio 1.5', async () => {
    const onStart = await renderSelector({ mode: 'ratio', ratioLimit: 1.5 });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('ratio');
    expect(screen.getByLabelText('Ratio target')).toHaveValue(1.5);
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'ratio', ratioLimit: 1.5 });
  });

  it('initializes duration hours from the snapshotted duration', async () => {
    const onStart = await renderSelector({ mode: 'duration', timeLimitSeconds: 7200 });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('duration');
    expect(screen.getByLabelText('Active seeding hours')).toHaveValue(2);
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'duration', timeLimitSeconds: 7200 });
  });

  it('initializes both snapshotted ratio_or_duration thresholds', async () => {
    const onStart = await renderSelector({ mode: 'ratio_or_duration', ratioLimit: 2.5, timeLimitSeconds: 43200 });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('ratio_or_duration');
    expect(screen.getByLabelText('Ratio target')).toHaveValue(2.5);
    expect(screen.getByLabelText('Active seeding hours')).toHaveValue(12);
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'ratio_or_duration', ratioLimit: 2.5, timeLimitSeconds: 43200 });
  });
});
