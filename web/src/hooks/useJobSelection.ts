import { useState, useCallback, useEffect } from 'react';
import type { Job } from '../types';

export function useJobSelection(jobs: Job[]) {
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  // Automatically prune selectedIds that no longer exist in jobs
  useEffect(() => {
    if (selectedIds.size === 0) return;
    const existingIds = new Set(jobs.map((j) => j.id));
    setSelectedIds((prev) => {
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (existingIds.has(id)) {
          next.add(id);
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });
  }, [jobs]);

  const toggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const selectVisible = useCallback((ids: string[]) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      for (const id of ids) next.add(id);
      return next;
    });
  }, []);

  const deselectVisible = useCallback((ids: string[]) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      for (const id of ids) next.delete(id);
      return next;
    });
  }, []);

  const clearSelection = useCallback(() => {
    setSelectedIds(new Set());
  }, []);

  return {
    selectedIds,
    setSelectedIds,
    toggleSelect,
    selectVisible,
    deselectVisible,
    clearSelection,
  };
}
