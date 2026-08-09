import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import App from '../App';
import { DeleteConfirmDialog } from './DeleteConfirmDialog';
import { JobActionsMenu } from './job-card/JobActionsMenu';
import * as api from '../api';
import type { Job } from '../types';

vi.mock('../api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../api')>();
  return {
    ...actual,
    getJobs: vi.fn(),
    getQueueSnapshot: vi.fn(),
    getSettings: vi.fn(),
    getCategories: vi.fn().mockResolvedValue([]),
    deleteJob: vi.fn(),
    connectSSE: vi.fn(() => ({
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      close: vi.fn(),
    })),
    createJob: vi.fn(),
    createBatchJobs: vi.fn(),
    bulkAction: vi.fn(),
    cancelJob: vi.fn(),
    pauseJob: vi.fn(),
    resumeJob: vi.fn(),
    retryJob: vi.fn(),
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

describe('Delete Download Feature', () => {
  const baseJob: Job = {
    id: 'job-delete-test-1',
    name: 'ArchLinux ISO',
    source: 'https://example.com/arch.iso',
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
    createdAt: '2026-08-04T12:00:00Z',
    updatedAt: '2026-08-04T12:05:00Z',
  };

  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getQueueSnapshot).mockResolvedValue({
      runningDownloads: 0,
      maxConcurrentDownloads: 3,
      queuedDownloads: 0,
      pausedDownloads: 0,
      items: [],
    });
    vi.mocked(api.getSettings).mockResolvedValue({} as any);
  });

  // 1. Completed job ⋮ menu: Delete enabled
  it('1. Completed job menu has enabled Delete action', () => {
    const onDelete = vi.fn();
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'completed' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={onDelete}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn).not.toBeDisabled();

    fireEvent.click(deleteBtn);
    expect(onDelete).toHaveBeenCalledWith(baseJob.id);
  });

  // 2. Cancelled job ⋮ menu: Delete enabled
  it('2. Cancelled job menu has enabled Delete action', () => {
    const onDelete = vi.fn();
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'cancelled' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={onDelete}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn).not.toBeDisabled();

    fireEvent.click(deleteBtn);
    expect(onDelete).toHaveBeenCalledWith(baseJob.id);
  });

  // 3. Failed job ⋮ menu: Delete enabled
  it('3. Failed job menu has enabled Delete action', () => {
    const onDelete = vi.fn();
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'failed' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={onDelete}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: 'Delete' });
    expect(deleteBtn).toBeInTheDocument();
    expect(deleteBtn).not.toBeDisabled();

    fireEvent.click(deleteBtn);
    expect(onDelete).toHaveBeenCalledWith(baseJob.id);
  });

  // 4. Downloading: Delete visible but disabled
  it('4. Downloading job shows disabled Delete option', () => {
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'downloading' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={vi.fn()}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: /Delete/i });
    expect(deleteBtn).toBeDisabled();
    expect(deleteBtn).toHaveAttribute('title', 'Cancel the download first');
  });

  // 5. Paused: Delete disabled
  it('5. Paused job shows disabled Delete option', () => {
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'paused' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={vi.fn()}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: /Delete/i });
    expect(deleteBtn).toBeDisabled();
  });

  // 6. Seeding: Delete disabled
  it('6. Seeding job shows disabled Delete option', () => {
    render(
      <JobActionsMenu
        job={{ ...baseJob, status: 'seeding' }}
        detailsOpen={false}
        onToggleDetails={vi.fn()}
        onDelete={vi.fn()}
        onAction={vi.fn()}
      />
    );

    fireEvent.click(screen.getByLabelText('More actions'));
    const deleteBtn = screen.getByRole('menuitem', { name: /Delete/i });
    expect(deleteBtn).toBeDisabled();
  });

  // 7. Delete click: dialog opens with correct elements
  it('7. Delete confirmation dialog renders title, message, checkbox and buttons', () => {
    render(
      <DeleteConfirmDialog
        job={baseJob}
        onConfirm={vi.fn()}
        onClose={vi.fn()}
      />
    );

    expect(screen.getByText('Delete download?')).toBeInTheDocument();
    expect(screen.getByText('This will remove this download from GoDownloader.')).toBeInTheDocument();
    expect(screen.getByLabelText('Also delete files from storage')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
  });

  // 8. Dialog checkbox: defaults unchecked (false)
  it('8. Dialog checkbox starts unchecked by default', () => {
    render(
      <DeleteConfirmDialog
        job={baseJob}
        onConfirm={vi.fn()}
        onClose={vi.fn()}
      />
    );

    const checkbox = screen.getByLabelText('Also delete files from storage') as HTMLInputElement;
    expect(checkbox.checked).toBe(false);
  });

  // 9. Unchecked confirmation: API receives deleteFiles=false
  it('9. Unchecked confirmation calls onConfirm with deleteFiles=false', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();

    render(
      <DeleteConfirmDialog
        job={baseJob}
        onConfirm={onConfirm}
        onClose={onClose}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => {
      expect(onConfirm).toHaveBeenCalledWith(baseJob.id, false);
      expect(onClose).toHaveBeenCalled();
    });
  });

  // 10. Checked confirmation: API receives deleteFiles=true
  it('10. Checked confirmation calls onConfirm with deleteFiles=true', async () => {
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();

    render(
      <DeleteConfirmDialog
        job={baseJob}
        onConfirm={onConfirm}
        onClose={onClose}
      />
    );

    const checkbox = screen.getByLabelText('Also delete files from storage');
    fireEvent.click(checkbox);

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
    await waitFor(() => {
      expect(onConfirm).toHaveBeenCalledWith(baseJob.id, true);
      expect(onClose).toHaveBeenCalled();
    });
  });

  // 11. Cancel dialog: no API call made
  it('11. Clicking Cancel closes dialog without calling onConfirm', () => {
    const onConfirm = vi.fn();
    const onClose = vi.fn();

    render(
      <DeleteConfirmDialog
        job={baseJob}
        onConfirm={onConfirm}
        onClose={onClose}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  // 12. Full App integration: successful Delete removes job card immediately
  it('12. Successful delete removes job from UI', async () => {
    vi.mocked(api.getJobs).mockResolvedValue([baseJob]);
    vi.mocked(api.deleteJob).mockResolvedValue(undefined);

    render(<App />);

    // Switch to Completed filter to see completed job
    const completedFilter = await screen.findByRole('button', { name: /^Completed/ });
    fireEvent.click(completedFilter);

    // Wait for completed job to appear
    await waitFor(() => {
      expect(screen.getByText('ArchLinux ISO')).toBeInTheDocument();
    });

    // Open ⋮ menu and click Delete
    fireEvent.click(screen.getByLabelText('More actions'));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    // Confirmation dialog appears
    expect(screen.getByText('Delete download?')).toBeInTheDocument();

    // Click confirm Delete
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    // Verify API called
    await waitFor(() => {
      expect(api.deleteJob).toHaveBeenCalledWith(baseJob.id, false);
    });

    // Verify card is removed from screen
    await waitFor(() => {
      expect(screen.queryByText('ArchLinux ISO')).not.toBeInTheDocument();
    });
  });

  // 13. Failed Delete: card remains and error message is displayed
  it('13. Failed delete keeps job card and displays error', async () => {
    vi.mocked(api.getJobs).mockResolvedValue([baseJob]);
    vi.mocked(api.deleteJob).mockRejectedValue(new Error('Network error deleting download'));

    render(<App />);

    // Switch to Completed filter
    const completedFilter = await screen.findByRole('button', { name: /^Completed/ });
    fireEvent.click(completedFilter);

    await waitFor(() => {
      expect(screen.getByText('ArchLinux ISO')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByLabelText('More actions'));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    // Click confirm Delete
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    // Error message appears
    await waitFor(() => {
      expect(screen.getAllByText('Network error deleting download').length).toBeGreaterThanOrEqual(1);
    });

    // Job card remains
    expect(screen.getByText('ArchLinux ISO')).toBeInTheDocument();
  });

  // 14. Identical behavior in All vs Completed tab
  it('14. Delete works identically in All tab filter', async () => {
    vi.mocked(api.getJobs).mockResolvedValue([baseJob]);
    vi.mocked(api.deleteJob).mockResolvedValue(undefined);

    render(<App />);

    // Switch to All filter
    const allFilter = await screen.findByRole('button', { name: /^All/ });
    fireEvent.click(allFilter);

    await waitFor(() => {
      expect(screen.getByText('ArchLinux ISO')).toBeInTheDocument();
    });

    // Open ⋮ menu and delete
    fireEvent.click(screen.getByLabelText('More actions'));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Delete' }));

    // Confirm
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() => {
      expect(api.deleteJob).toHaveBeenCalledWith(baseJob.id, false);
      expect(screen.queryByText('ArchLinux ISO')).not.toBeInTheDocument();
    });
  });
});
