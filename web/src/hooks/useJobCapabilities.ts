import { useEffect, useState } from 'react';
import { getJobCapabilities } from '../api';
import type { Job, JobCapabilities } from '../types';

const cache = new Map<string, JobCapabilities>();

export function clearJobCapabilitiesCache(jobId?: string) {
  if (jobId) {
    for (const key of cache.keys()) {
      if (key.startsWith(jobId + ':') || key === jobId) {
        cache.delete(key);
      }
    }
  } else {
    cache.clear();
  }
}

export function invalidateJobCapabilities(jobId?: string) {
  clearJobCapabilitiesCache(jobId);
}

export function useJobCapabilities(job: Job | string | null | undefined): {
  capabilities: JobCapabilities | null;
  loading: boolean;
  refetch: () => void;
} {
  const jobId = typeof job === 'string' ? job : job?.id;
  const jobStatus = typeof job === 'string' ? '' : job?.status ?? '';
  const updatedAt = typeof job === 'string' ? '' : job?.updatedAt ?? '';

  const cacheKey = jobId ? `${jobId}:${jobStatus}` : '';

  const cached = cacheKey ? cache.get(cacheKey) ?? null : null;
  const [capabilities, setCapabilities] = useState<JobCapabilities | null>(cached);
  const [loading, setLoading] = useState(!cached && !!jobId);

  useEffect(() => {
    if (!jobId) {
      setCapabilities(null);
      setLoading(false);
      return;
    }

    const key = `${jobId}:${jobStatus}`;
    const existing = cache.get(key);
    if (existing) {
      setCapabilities(existing);
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);

    getJobCapabilities(jobId)
      .then((result) => {
        cache.set(key, result);
        if (!cancelled) setCapabilities(result);
      })
      .catch(() => {
        if (!cancelled) setCapabilities(null);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [jobId, jobStatus, updatedAt]);

  const refetch = () => {
    if (jobId) {
      clearJobCapabilitiesCache(jobId);
      setLoading(true);
      getJobCapabilities(jobId)
        .then((result) => {
          const key = `${jobId}:${jobStatus}`;
          cache.set(key, result);
          setCapabilities(result);
        })
        .catch(() => setCapabilities(null))
        .finally(() => setLoading(false));
    }
  };

  return { capabilities, loading, refetch };
}
