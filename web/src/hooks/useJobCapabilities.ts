import { useEffect, useState } from 'react';
import { getJobCapabilities } from '../api';
import type { JobCapabilities } from '../types';

const cache = new Map<string, JobCapabilities>();

export function useJobCapabilities(jobId: string): {
  capabilities: JobCapabilities | null;
  loading: boolean;
} {
  const cached = cache.get(jobId) ?? null;
  const [capabilities, setCapabilities] = useState<JobCapabilities | null>(cached);
  const [loading, setLoading] = useState(!cached);

  useEffect(() => {
    const existing = cache.get(jobId);
    if (existing) {
      setCapabilities(existing);
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);

    getJobCapabilities(jobId)
      .then((result) => {
        cache.set(jobId, result);
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
  }, [jobId]);

  return { capabilities, loading };
}
