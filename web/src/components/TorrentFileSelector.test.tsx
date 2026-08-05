import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Job, SeedingPolicy } from '../types';
import { TorrentFileSelector, normalizeSeedingMode, formatDiskSpaceError } from './TorrentFileSelector';
import { ApiResponseError } from '../api';
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

async function renderSelector(policy: SeedingPolicy, onStart = vi.fn().mockResolvedValue(undefined)) {
  const onClose = vi.fn();
  render(<TorrentFileSelector job={jobWithPolicy(policy)} onStart={onStart} onClose={onClose} />);
  await screen.findByText('a.bin');
  return { onStart, onClose };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getTorrentFiles).mockResolvedValue([torrentFile]);
});

describe('normalizeSeedingMode', () => {
  it('returns none for empty string', () => {
    expect(normalizeSeedingMode('')).toBe('none');
  });

  it('returns none for null', () => {
    expect(normalizeSeedingMode(null)).toBe('none');
  });

  it('returns none for undefined', () => {
    expect(normalizeSeedingMode(undefined)).toBe('none');
  });

  it('returns none for unknown string', () => {
    expect(normalizeSeedingMode('malformed_value')).toBe('none');
  });

  it('returns none for none', () => {
    expect(normalizeSeedingMode('none')).toBe('none');
  });

  it('returns unlimited for unlimited', () => {
    expect(normalizeSeedingMode('unlimited')).toBe('unlimited');
  });

  it('returns ratio for ratio', () => {
    expect(normalizeSeedingMode('ratio')).toBe('ratio');
  });

  it('returns duration for duration', () => {
    expect(normalizeSeedingMode('duration')).toBe('duration');
  });

  it('returns ratio_or_duration for ratio_or_duration', () => {
    expect(normalizeSeedingMode('ratio_or_duration')).toBe('ratio_or_duration');
  });
});

describe('TorrentFileSelector seeding policy & submission', () => {
  it('initializes dropdown to none when job policy mode is empty string', async () => {
    const { onStart } = await renderSelector({ mode: '' as any });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('none');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'none' });
  });

  it('opens with snapshotted none and produces mode none without thresholds', async () => {
    const { onStart } = await renderSelector({ mode: 'none' });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('none');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'none' });
    expect(onStart.mock.calls[0][2]).not.toHaveProperty('ratioLimit');
    expect(onStart.mock.calls[0][2]).not.toHaveProperty('timeLimitSeconds');
  });

  it('opens with snapshotted unlimited and produces mode unlimited without thresholds', async () => {
    const { onStart } = await renderSelector({ mode: 'unlimited' });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('unlimited');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'unlimited' });
    expect(onStart.mock.calls[0][2]).not.toHaveProperty('ratioLimit');
    expect(onStart.mock.calls[0][2]).not.toHaveProperty('timeLimitSeconds');
  });

  it('switching from ratio to none strips ratioLimit from submission payload', async () => {
    const { onStart } = await renderSelector({ mode: 'ratio', ratioLimit: 2.0 });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('ratio');
    fireEvent.change(screen.getByLabelText('Seeding policy'), { target: { value: 'none' } });
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('none');
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onStart.mock.calls[0][2]).toEqual({ mode: 'none' });
    expect(onStart.mock.calls[0][2]).not.toHaveProperty('ratioLimit');
  });

  it('displays Starting... and disables Start and Cancel buttons during pending API request', async () => {
    let resolvePromise!: () => void;
    const pendingOnStart = vi.fn().mockImplementation(() => new Promise<void>((resolve) => {
      resolvePromise = resolve;
    }));

    await renderSelector({ mode: 'none' }, pendingOnStart);
    const startBtn = screen.getByRole('button', { name: 'Start Download' });
    const cancelBtn = screen.getByRole('button', { name: 'Cancel' });

    fireEvent.click(startBtn);

    expect(screen.getByRole('button', { name: 'Starting…' })).toBeDisabled();
    expect(cancelBtn).toBeDisabled();

    resolvePromise();
    await waitFor(() => expect(pendingOnStart).toHaveBeenCalled());
  });

  it('on API error keeps modal open, displays inline error inside modal, re-enables buttons, and preserves selections', async () => {
    const failingOnStart = vi.fn().mockRejectedValue(new Error('Invalid seeding mode rejected by server'));
    await renderSelector({ mode: 'none' }, failingOnStart);

    const startBtn = screen.getByRole('button', { name: 'Start Download' });
    fireEvent.click(startBtn);

    await screen.findByText('Invalid seeding mode rejected by server');
    expect(screen.getByRole('button', { name: 'Start Download' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).not.toBeDisabled();
    expect(screen.getByLabelText('Seeding policy')).toHaveValue('none');
  });

  it('prevents double submission on rapid clicks', async () => {
    let resolvePromise!: () => void;
    const pendingOnStart = vi.fn().mockImplementation(() => new Promise<void>((resolve) => {
      resolvePromise = resolve;
    }));

    await renderSelector({ mode: 'none' }, pendingOnStart);
    const startBtn = screen.getByRole('button', { name: 'Start Download' });

    fireEvent.click(startBtn);
    fireEvent.click(startBtn);
    fireEvent.click(startBtn);

    expect(pendingOnStart).toHaveBeenCalledTimes(1);

    resolvePromise();
    await waitFor(() => expect(pendingOnStart).toHaveBeenCalled());
  });

  it('displays human-readable disk space message for INSUFFICIENT_DISK_SPACE errors', async () => {
    const diskSpaceError = new ApiResponseError(
      'INSUFFICIENT_DISK_SPACE',
      'INSUFFICIENT_DISK_SPACE: insufficient free space in /downloads (free: 16000000000, required: 23622320128, reserve: 1073741824, remaining: 22548578304)'
    );
    const failingOnStart = vi.fn().mockRejectedValue(diskSpaceError);
    await renderSelector({ mode: 'none' }, failingOnStart);

    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));

    const errorEl = await screen.findByTestId('torrent-submit-error');
    expect(errorEl.textContent).toContain('Insufficient disk space');
    expect(errorEl.textContent).toContain('Available:');
    expect(errorEl.textContent).toContain('Selected remaining:');
    expect(errorEl.textContent).toContain('Reserved:');
    expect(errorEl.textContent).toContain('Required:');
    // Should NOT contain the raw "an internal error occurred" fallback
    expect(errorEl.textContent).not.toContain('an internal error occurred');
  });

  it('shows raw error message for non-disk-space errors', async () => {
    const otherError = new ApiResponseError('ENGINE_ERROR', 'qBittorrent daemon unreachable');
    const failingOnStart = vi.fn().mockRejectedValue(otherError);
    await renderSelector({ mode: 'none' }, failingOnStart);

    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));

    await screen.findByText('qBittorrent daemon unreachable');
  });
});

describe('formatDiskSpaceError', () => {
  it('parses backend message into human-readable format', () => {
    const message = 'INSUFFICIENT_DISK_SPACE: insufficient free space in /downloads (free: 16000000000, required: 23622320128, reserve: 1073741824, remaining: 22548578304)';
    const result = formatDiskSpaceError(message);
    expect(result).toContain('Insufficient disk space');
    expect(result).toContain('Available: 14.9 GiB');
    expect(result).toContain('Selected remaining: 21.0 GiB');
    expect(result).toContain('Reserved: 1.0 GiB');
    expect(result).toContain('Required: 22.0 GiB');
  });

  it('falls back gracefully when message format is unexpected', () => {
    const message = 'some unexpected disk space error';
    const result = formatDiskSpaceError(message);
    expect(result).toContain('Insufficient disk space');
    expect(result).toContain(message);
  });
});
