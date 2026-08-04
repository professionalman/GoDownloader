import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { JobCard } from './JobCard';
import type { Job } from '../types';

describe('JobCard Phase 2 convergence', () => {
  const sampleJob: Job = {
    id: 'job-101',
    name: 'Sample Direct Video',
    source: 'https://example.com/video.mp4',
    type: 'download',
    engine: 'aria2',
    status: 'downloading',
    progress: 62.5,
    completedBytes: 6250000,
    totalBytes: 10000000,
    speedBytesPerSecond: 524288,
    etaSeconds: 8,
    networkPolicy: {
      downloadLimitBytesPerSecond: 0,
      proxy: { mode: 'disabled' },
      retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
      timeoutPolicy: { connectTimeoutSeconds: 30, requestTimeoutSeconds: 60 },
    },
    effectiveDownloadLimitBytesPerSecond: 0,
    createdAt: '2026-08-04T12:00:00Z',
    updatedAt: '2026-08-04T12:01:00Z',
  };

  it('renders status pills, domain, and progress metrics correctly', () => {
    render(
      <JobCard
        job={sampleJob}
        selected={false}
        onToggleSelect={vi.fn()}
      />
    );

    expect(screen.getByText('Sample Direct Video')).toBeInTheDocument();
    expect(screen.getByText('Downloading')).toBeInTheDocument();
    expect(screen.getByText('example.com')).toBeInTheDocument();
    expect(screen.getByText('Direct · aria2')).toBeInTheDocument();
    expect(screen.getByText('62.5%')).toBeInTheDocument();
  });

  it('renders completed card with status pill ONCE, no progress bar, and icon-only folder button', () => {
    const completedJob: Job = { ...sampleJob, status: 'completed', progress: 100 };
    const onOpenFolder = vi.fn();

    render(
      <JobCard
        job={completedJob}
        selected={false}
        onOpenFolder={onOpenFolder}
      />
    );

    // Status pill "Completed" rendered ONCE in metadata line
    const completedPills = screen.getAllByText('Completed');
    expect(completedPills).toHaveLength(1);

    // Progress bar percent text not rendered on completed card
    expect(screen.queryByText('100.0%')).not.toBeInTheDocument();

    // Icon-only button with aria-label/title "Open downloads folder"
    const btn = screen.getByRole('button', { name: /Open downloads folder/i });
    expect(btn).toBeInTheDocument();
    expect(btn).toHaveAttribute('title', 'Open downloads folder');
    fireEvent.click(btn);
    expect(onOpenFolder).toHaveBeenCalledTimes(1);
  });

  it('falls back to media estimate or Size unavailable for zero-byte completed media jobs', () => {
    const zeroByteMediaJob: Job = {
      ...sampleJob,
      type: 'media',
      status: 'completed',
      progress: 100,
      totalBytes: 0,
      completedBytes: 0,
      mediaInfo: {
        title: 'Sample Video',
        duration: 120,
        thumbnail: '',
        url: 'https://example.com/video',
        selectedFormat: '137',
        formats: [
          { formatId: '137', ext: 'mp4', resolution: '1080p', vcodec: 'h264', acodec: 'none', fileSize: 50000000, fps: 30, quality: '1080p', note: '' },
        ],
        bestAudioFormat: { formatId: '140', ext: 'm4a', resolution: '', vcodec: 'none', acodec: 'aac', fileSize: 5000000, fps: 0, quality: 'audio', note: '' },
      },
    };

    render(<JobCard job={zeroByteMediaJob} selected={false} />);

    // Never renders "0 B" for completed media file
    expect(screen.queryByText('0 B')).not.toBeInTheDocument();
    expect(screen.getByText('~52.5 MiB est.')).toBeInTheDocument();
  });

  it('renders Size unavailable when completed job has zero size and no media estimate', () => {
    const zeroByteNoEstimateJob: Job = {
      ...sampleJob,
      status: 'completed',
      progress: 100,
      totalBytes: 0,
      completedBytes: 0,
    };

    render(<JobCard job={zeroByteNoEstimateJob} selected={false} />);
    expect(screen.queryByText('0 B')).not.toBeInTheDocument();
    expect(screen.getByText('Size unavailable')).toBeInTheDocument();
  });

  it('toggles details panel when expand chevron is clicked', () => {
    render(
      <JobCard
        job={sampleJob}
        selected={false}
      />
    );

    expect(screen.queryByText(/Source URL:/i)).not.toBeInTheDocument();

    const chevron = screen.getByRole('button', { name: /Show details/i });
    fireEvent.click(chevron);

    expect(screen.getByText(/Source URL:/i)).toBeInTheDocument();
    expect(screen.getByText('https://example.com/video.mp4')).toBeInTheDocument();
  });

  it('never displays sensitive credentials or passwords in details', () => {
    const sensitiveJob: Job = {
      ...sampleJob,
      networkPolicy: {
        ...sampleJob.networkPolicy,
        proxy: { mode: 'custom', host: 'proxy.com', port: 8080, username: 'super_secret_username_123' },
      },
    };

    render(
      <JobCard
        job={sensitiveJob}
        selected={false}
      />
    );

    const chevron = screen.getByRole('button', { name: /Show details/i });
    fireEvent.click(chevron);

    expect(screen.queryByText('super_secret_password_123')).not.toBeInTheDocument();
  });

  it('exposes only one visible details control button', () => {
    render(<JobCard job={sampleJob} selected={false} />);
    const detailsButtons = screen.getAllByRole('button', { name: /Show details/i });
    expect(detailsButtons).toHaveLength(1);
  });

  it('closes overflow menu on Escape key press and outside click', () => {
    render(<JobCard job={sampleJob} selected={false} onCancel={vi.fn()} />);

    const menuTrigger = screen.getByRole('button', { name: 'More actions' });
    fireEvent.click(menuTrigger);

    expect(screen.getByRole('menu', { name: 'Job options' })).toBeInTheDocument();

    // Close on Escape key
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Job options' })).not.toBeInTheDocument();

    // Reopen and close on outside click
    fireEvent.click(menuTrigger);
    expect(screen.getByRole('menu', { name: 'Job options' })).toBeInTheDocument();

    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole('menu', { name: 'Job options' })).not.toBeInTheDocument();
  });
});
