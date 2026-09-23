import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import App from '../App';
import * as api from '../api';
import type { Job, SyncSnapshot, QueueSnapshot, RecoverySummary } from '../types';

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>();
  return {
    ...actual,
    getJobs: vi.fn(),
    getSyncSnapshot: vi.fn(),
    getRecoverySummary: vi.fn(),
    getQueueSnapshot: vi.fn(),
    getSettings: vi.fn(),
    getCategories: vi.fn().mockResolvedValue([]),
    subscribeEvents: vi.fn(() => ({
      close: vi.fn(),
    })),
    createJob: vi.fn(),
    createBatchJobs: vi.fn(),
    bulkAction: vi.fn(),
    cancelJob: vi.fn(),
    pauseJob: vi.fn(),
    resumeJob: vi.fn(),
    retryJob: vi.fn(),
    deleteJob: vi.fn(),
    startTorrent: vi.fn(),
    stopSeeding: vi.fn(),
    openFolder: vi.fn(),
    selectFormat: vi.fn(),
    uploadTorrent: vi.fn(),
    setJobPriority: vi.fn(),
    updateSettings: vi.fn(),
    reorderQueue: vi.fn(),
  };
});

describe('RecoveryBanner App Integration', () => {
  const sampleJob: Job = {
    id: 'job-1',
    name: 'Sample Download',
    type: 'direct',
    status: 'downloading',
    source: 'https://example.com/file.zip',
    progress: 50,
    totalBytes: 1000,
    completedBytes: 500,
    speedBytesPerSecond: 100,
    etaSeconds: 5,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  } as unknown as Job;

  const mockSnapshot: SyncSnapshot = {
    cursor: 10,
    jobs: [sampleJob],
    queue: {
      maxConcurrentDownloads: 3,
      runningDownloads: 1,
      queuedDownloads: 0,
      pausedDownloads: 0,
      items: [],
    } as QueueSnapshot,
    timestamp: '2026-01-01T00:00:00Z',
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getSyncSnapshot).mockResolvedValue(mockSnapshot);
    vi.mocked(api.getJobs).mockResolvedValue([sampleJob]);
    vi.mocked(api.getQueueSnapshot).mockResolvedValue(mockSnapshot.queue);
    vi.mocked(api.getSettings).mockResolvedValue({} as any);
  });

  it('requests recovery summary on mount and renders banner when interventions exist', async () => {
    const summaryWithInterventions: RecoverySummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    };
    vi.mocked(api.getRecoverySummary).mockResolvedValue(summaryWithInterventions);

    render(<App />);

    expect(api.getRecoverySummary).toHaveBeenCalledTimes(1);

    await waitFor(() => {
      expect(
        screen.getByText('Completed remaining finalization work for 1 download after restart.')
      ).toBeInTheDocument();
    });

    // Dismissing hides banner in memory
    const dismissBtn = screen.getByRole('button', { name: 'Dismiss recovery notice' });
    fireEvent.click(dismissBtn);

    await waitFor(() => {
      expect(
        screen.queryByText('Completed remaining finalization work for 1 download after restart.')
      ).not.toBeInTheDocument();
    });
  });

  it('continues normally when getRecoverySummary rejects (non-blocking supplementary UX)', async () => {
    vi.mocked(api.getRecoverySummary).mockRejectedValue(new Error('Network offline or IPC failure'));

    render(<App />);

    expect(api.getRecoverySummary).toHaveBeenCalledTimes(1);

    // Job UI still bootstraps and displays normal jobs from StateSync
    await waitFor(() => {
      expect(screen.getByText('Sample Download')).toBeInTheDocument();
    });

    // No recovery banner is rendered
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });

  it('renders nothing when clean startup returns zero counts and empty issues', async () => {
    const cleanSummary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    };
    vi.mocked(api.getRecoverySummary).mockResolvedValue(cleanSummary);

    render(<App />);

    expect(api.getRecoverySummary).toHaveBeenCalledTimes(1);

    await waitFor(() => {
      expect(screen.getByText('Sample Download')).toBeInTheDocument();
    });

    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});
