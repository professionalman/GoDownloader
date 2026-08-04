import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { BulkActionBar } from './BulkActionBar';
import type { Job } from '../types';

describe('BulkActionBar', () => {
  const sampleJobs: Job[] = [
    {
      id: 'job-1',
      name: 'Direct Download',
      source: 'https://example.com/file1.iso',
      type: 'download',
      engine: 'aria2',
      status: 'downloading',
      progress: 45,
      completedBytes: 450,
      totalBytes: 1000,
      speedBytesPerSecond: 100,
      etaSeconds: 5,
      networkPolicy: {
        downloadLimitBytesPerSecond: 0,
        proxy: { mode: 'disabled' },
        retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
        timeoutPolicy: { connectTimeoutSeconds: 30, requestTimeoutSeconds: 60 },
      },
      effectiveDownloadLimitBytesPerSecond: 0,
      createdAt: '2026-08-04T10:00:00Z',
      updatedAt: '2026-08-04T10:00:00Z',
    },
    {
      id: 'job-2',
      name: 'Paused Download',
      source: 'https://example.com/file2.zip',
      type: 'download',
      engine: 'aria2',
      status: 'paused',
      progress: 20,
      completedBytes: 200,
      totalBytes: 1000,
      speedBytesPerSecond: 0,
      etaSeconds: 0,
      networkPolicy: {
        downloadLimitBytesPerSecond: 0,
        proxy: { mode: 'disabled' },
        retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
        timeoutPolicy: { connectTimeoutSeconds: 30, requestTimeoutSeconds: 60 },
      },
      effectiveDownloadLimitBytesPerSecond: 0,
      createdAt: '2026-08-04T10:00:00Z',
      updatedAt: '2026-08-04T10:00:00Z',
    },
    {
      id: 'job-3',
      name: 'Failed Download',
      source: 'https://example.com/file3.mp4',
      type: 'media',
      engine: 'yt-dlp',
      status: 'failed',
      progress: 0,
      completedBytes: 0,
      totalBytes: 0,
      speedBytesPerSecond: 0,
      etaSeconds: 0,
      networkPolicy: {
        downloadLimitBytesPerSecond: 0,
        proxy: { mode: 'disabled' },
        retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
        timeoutPolicy: { connectTimeoutSeconds: 30, requestTimeoutSeconds: 60 },
      },
      effectiveDownloadLimitBytesPerSecond: 0,
      createdAt: '2026-08-04T10:00:00Z',
      updatedAt: '2026-08-04T10:00:00Z',
    },
  ];

  it('renders null when selectedIds is empty', () => {
    const { container } = render(
      <BulkActionBar
        jobs={sampleJobs}
        selectedIds={new Set()}
        onAction={vi.fn()}
        onClear={vi.fn()}
      />
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders count and eligibility metrics when items are selected', () => {
    const selectedIds = new Set(['job-1', 'job-2', 'job-3']);
    render(
      <BulkActionBar
        jobs={sampleJobs}
        selectedIds={selectedIds}
        onAction={vi.fn()}
        onClear={vi.fn()}
      />
    );

    expect(screen.getByText('3 selected')).toBeInTheDocument();
    expect(
      screen.getByText(/1 can pause · 1 can resume · 1 can retry/)
    ).toBeInTheDocument();
  });

  it('invokes onAction with correct command and eligible IDs on button clicks', () => {
    const onAction = vi.fn();
    const selectedIds = new Set(['job-1', 'job-2', 'job-3']);
    render(
      <BulkActionBar
        jobs={sampleJobs}
        selectedIds={selectedIds}
        onAction={onAction}
        onClear={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /Pause/i }));
    expect(onAction).toHaveBeenCalledWith('pause', ['job-1']);

    fireEvent.click(screen.getByRole('button', { name: /Resume/i }));
    expect(onAction).toHaveBeenCalledWith('resume', ['job-2']);

    fireEvent.click(screen.getByRole('button', { name: /Retry/i }));
    expect(onAction).toHaveBeenCalledWith('retry', ['job-3']);

    fireEvent.click(screen.getByRole('button', { name: /Cancel/i }));
    expect(onAction).toHaveBeenCalledWith('cancel', ['job-1', 'job-2']);
  });

  it('invokes onClear when Clear button is clicked', () => {
    const onClear = vi.fn();
    const selectedIds = new Set(['job-1']);
    render(
      <BulkActionBar
        jobs={sampleJobs}
        selectedIds={selectedIds}
        onAction={vi.fn()}
        onClear={onClear}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /Clear/i }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});
