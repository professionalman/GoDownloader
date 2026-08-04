import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';
import { useJobCapabilities, clearJobCapabilitiesCache } from './useJobCapabilities';
import * as api from '../api';
import type { Job, JobCapabilities, CapabilityState } from '../types';

vi.mock('../api', () => ({
  getJobCapabilities: vi.fn(),
}));

const cap = (supported: boolean, mutableNow = supported): CapabilityState => ({
  supported,
  mutableNow,
});

const mockCapabilities: JobCapabilities = {
  pause: cap(true, true),
  resume: cap(false, false),
  cancel: cap(true, true),
  retry: cap(false, false),
  downloadLimit: cap(true, true),
  uploadLimit: cap(true, true),
  proxy: cap(true, true),
  userAgent: cap(true, true),
  customHeaders: cap(true, true),
  retryPolicy: cap(true, true),
  timeoutPolicy: cap(true, true),
  connections: cap(true, true),
  fileSelection: cap(false, false),
  trackers: cap(false, false),
  seedingPolicy: cap(false, false),
};

describe('useJobCapabilities', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearJobCapabilitiesCache();
  });

  it('returns null capabilities and loading false when job is null', () => {
    const { result } = renderHook(() => useJobCapabilities(null));
    expect(result.current.capabilities).toBeNull();
    expect(result.current.loading).toBe(false);
  });

  it('fetches capabilities for a job and caches them', async () => {
    vi.mocked(api.getJobCapabilities).mockResolvedValueOnce(mockCapabilities);

    const job: Job = { id: 'job-1', status: 'downloading' } as Job;
    const { result } = renderHook(() => useJobCapabilities(job));

    expect(result.current.loading).toBe(true);

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    expect(result.current.capabilities).toEqual(mockCapabilities);
    expect(api.getJobCapabilities).toHaveBeenCalledWith('job-1');
  });

  it('repeated render in same status uses cache without refetching', async () => {
    vi.mocked(api.getJobCapabilities).mockResolvedValueOnce(mockCapabilities);

    const job: Job = { id: 'job-1', status: 'downloading', updatedAt: '2026-08-01T10:00:00Z' } as Job;
    const { result, rerender } = renderHook(
      ({ j }: { j: Job }) => useJobCapabilities(j),
      { initialProps: { j: job } }
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });
    expect(api.getJobCapabilities).toHaveBeenCalledTimes(1);

    // Re-render with updated progress (same status)
    rerender({ j: { ...job, updatedAt: '2026-08-01T10:00:05Z' } as Job });

    expect(result.current.loading).toBe(false);
    expect(result.current.capabilities).toEqual(mockCapabilities);
    expect(api.getJobCapabilities).toHaveBeenCalledTimes(1);
  });

  it('invalidates cache and refetches when downloading changes to paused', async () => {
    const downloadingCaps = { ...mockCapabilities, pause: cap(true, true), resume: cap(false, false) };
    const pausedCaps = { ...mockCapabilities, pause: cap(false, false), resume: cap(true, true) };

    vi.mocked(api.getJobCapabilities)
      .mockResolvedValueOnce(downloadingCaps)
      .mockResolvedValueOnce(pausedCaps);

    const { result, rerender } = renderHook(
      ({ job }: { job: Job }) => useJobCapabilities(job),
      { initialProps: { job: { id: 'job-1', status: 'downloading' } as Job } }
    );

    await waitFor(() => {
      expect(result.current.capabilities?.pause.mutableNow).toBe(true);
    });

    // Pause transition
    rerender({ job: { id: 'job-1', status: 'paused' } as Job });

    await waitFor(() => {
      expect(result.current.capabilities?.resume.mutableNow).toBe(true);
      expect(result.current.capabilities?.pause.mutableNow).toBe(false);
    });

    expect(api.getJobCapabilities).toHaveBeenCalledTimes(2);
  });

  it('invalidates cache and refetches when paused changes to downloading on resume', async () => {
    const pausedCaps = { ...mockCapabilities, pause: cap(false, false), resume: cap(true, true) };
    const resumedCaps = { ...mockCapabilities, pause: cap(true, true), resume: cap(false, false) };

    vi.mocked(api.getJobCapabilities)
      .mockResolvedValueOnce(pausedCaps)
      .mockResolvedValueOnce(resumedCaps);

    const { result, rerender } = renderHook(
      ({ job }: { job: Job }) => useJobCapabilities(job),
      { initialProps: { job: { id: 'job-1', status: 'paused' } as Job } }
    );

    await waitFor(() => {
      expect(result.current.capabilities?.resume.mutableNow).toBe(true);
    });

    // Resume transition
    rerender({ job: { id: 'job-1', status: 'downloading' } as Job });

    await waitFor(() => {
      expect(result.current.capabilities?.pause.mutableNow).toBe(true);
      expect(result.current.capabilities?.resume.mutableNow).toBe(false);
    });

    expect(api.getJobCapabilities).toHaveBeenCalledTimes(2);
  });

  it('refetches capabilities on completed to seeding transition', async () => {
    const completedCaps = { ...mockCapabilities, seedingPolicy: cap(false, false) };
    const seedingCaps = { ...mockCapabilities, seedingPolicy: cap(true, true) };

    vi.mocked(api.getJobCapabilities)
      .mockResolvedValueOnce(completedCaps)
      .mockResolvedValueOnce(seedingCaps);

    const { result, rerender } = renderHook(
      ({ job }: { job: Job }) => useJobCapabilities(job),
      { initialProps: { job: { id: 'job-1', status: 'completed' } as Job } }
    );

    await waitFor(() => {
      expect(result.current.loading).toBe(false);
    });

    rerender({ job: { id: 'job-1', status: 'seeding' } as Job });

    await waitFor(() => {
      expect(result.current.capabilities?.seedingPolicy.supported).toBe(true);
    });

    expect(api.getJobCapabilities).toHaveBeenCalledTimes(2);
  });
});
