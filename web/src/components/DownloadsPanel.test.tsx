import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { DownloadsPanel } from './DownloadsPanel';
import type { Job, QueueSnapshot } from '../types';

describe('DownloadsPanel', () => {
  const sampleJobs: Job[] = [
    {
      id: 'job-1',
      name: 'Direct File 1',
      source: 'https://example.com/file1.iso',
      type: 'download',
      engine: 'aria2',
      status: 'downloading',
      progress: 50,
      completedBytes: 500,
      totalBytes: 1000,
      speedBytesPerSecond: 1048576,
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
      name: 'Completed File',
      source: 'https://example.com/file2.zip',
      type: 'download',
      engine: 'aria2',
      status: 'completed',
      progress: 100,
      completedBytes: 1000,
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
      createdAt: '2026-08-04T09:00:00Z',
      updatedAt: '2026-08-04T09:05:00Z',
    },
  ];

  const sampleSnapshot: QueueSnapshot = {
    runningDownloads: 1,
    maxConcurrentDownloads: 3,
    queuedDownloads: 0,
    pausedDownloads: 0,
    items: [],
  };

  it('renders transfer strip and filter options', () => {
    render(
      <DownloadsPanel
        jobs={sampleJobs}
        initialLoading={false}
        selectedIds={new Set()}
        queueSnapshot={sampleSnapshot}
        onToggleSelect={vi.fn()}
        onSelectVisible={vi.fn()}
        onBulkAction={vi.fn()}
        onClearSelection={vi.fn()}
        onCancel={vi.fn()}
        onPause={vi.fn()}
        onResume={vi.fn()}
        onRetry={vi.fn()}
        onOpenFolder={vi.fn()}
        onSelectFormat={vi.fn()}
        onSelectTorrentFiles={vi.fn()}
        onStopSeeding={vi.fn()}
        onJobUpdated={vi.fn()}
      />
    );

    expect(screen.getByText(/downloading · 1.0 MB\/s/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Active/i })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /^Completed/i })).toBeInTheDocument();
  });

  it('renders loading skeletons when initialLoading is true', () => {
    render(
      <DownloadsPanel
        jobs={[]}
        initialLoading={true}
        selectedIds={new Set()}
        queueSnapshot={null}
        onToggleSelect={vi.fn()}
        onSelectVisible={vi.fn()}
        onBulkAction={vi.fn()}
        onClearSelection={vi.fn()}
        onCancel={vi.fn()}
        onPause={vi.fn()}
        onResume={vi.fn()}
        onRetry={vi.fn()}
        onOpenFolder={vi.fn()}
        onSelectFormat={vi.fn()}
        onSelectTorrentFiles={vi.fn()}
        onStopSeeding={vi.fn()}
        onJobUpdated={vi.fn()}
      />
    );

    expect(screen.getByTestId('downloads-loading-skeleton')).toBeInTheDocument();
  });

  it('filters visible jobs when changing filter tabs', () => {
    render(
      <DownloadsPanel
        jobs={sampleJobs}
        initialLoading={false}
        selectedIds={new Set()}
        queueSnapshot={sampleSnapshot}
        onToggleSelect={vi.fn()}
        onSelectVisible={vi.fn()}
        onBulkAction={vi.fn()}
        onClearSelection={vi.fn()}
        onCancel={vi.fn()}
        onPause={vi.fn()}
        onResume={vi.fn()}
        onRetry={vi.fn()}
        onOpenFolder={vi.fn()}
        onSelectFormat={vi.fn()}
        onSelectTorrentFiles={vi.fn()}
        onStopSeeding={vi.fn()}
        onJobUpdated={vi.fn()}
      />
    );

    expect(screen.getByText('Direct File 1')).toBeInTheDocument();
    expect(screen.queryByText('Completed File')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /^Completed/i }));

    expect(screen.queryByText('Direct File 1')).not.toBeInTheDocument();
    expect(screen.getByText('Completed File')).toBeInTheDocument();
  });

  it('displays contextual empty state when no jobs match filter/search', () => {
    render(
      <DownloadsPanel
        jobs={sampleJobs}
        initialLoading={false}
        selectedIds={new Set()}
        queueSnapshot={sampleSnapshot}
        onToggleSelect={vi.fn()}
        onSelectVisible={vi.fn()}
        onBulkAction={vi.fn()}
        onClearSelection={vi.fn()}
        onCancel={vi.fn()}
        onPause={vi.fn()}
        onResume={vi.fn()}
        onRetry={vi.fn()}
        onOpenFolder={vi.fn()}
        onSelectFormat={vi.fn()}
        onSelectTorrentFiles={vi.fn()}
        onStopSeeding={vi.fn()}
        onJobUpdated={vi.fn()}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /^Failed/i }));
    expect(screen.getByText('No failed downloads')).toBeInTheDocument();
  });
});
