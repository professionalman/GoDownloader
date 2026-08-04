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

  it('renders "Open downloads folder" action for completed downloads', () => {
    const completedJob: Job = { ...sampleJob, status: 'completed', progress: 100 };
    const onOpenFolder = vi.fn();

    render(
      <JobCard
        job={completedJob}
        selected={false}
        onOpenFolder={onOpenFolder}
      />
    );

    const btn = screen.getByRole('button', { name: /Open downloads folder/i });
    expect(btn).toBeInTheDocument();
    fireEvent.click(btn);
    expect(onOpenFolder).toHaveBeenCalledTimes(1);
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
});
