import { describe, it, expect } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useJobSelection } from './useJobSelection';
import type { Job } from '../types';

const job1: Job = { id: 'j1', status: 'downloading' } as Job;
const job2: Job = { id: 'j2', status: 'completed' } as Job;
const job3: Job = { id: 'j3', status: 'paused' } as Job;

describe('useJobSelection', () => {
  it('toggles selection of individual items', () => {
    const { result } = renderHook(() => useJobSelection([job1, job2, job3]));

    act(() => {
      result.current.toggleSelect('j1');
    });
    expect(result.current.selectedIds.has('j1')).toBe(true);

    act(() => {
      result.current.toggleSelect('j1');
    });
    expect(result.current.selectedIds.has('j1')).toBe(false);
  });

  it('selects visible items without clearing already selected items that are hidden', () => {
    const { result } = renderHook(() => useJobSelection([job1, job2, job3]));

    act(() => {
      result.current.toggleSelect('j1');
    });

    // Select visible items j2
    act(() => {
      result.current.selectVisible(['j2']);
    });

    expect(result.current.selectedIds.has('j1')).toBe(true);
    expect(result.current.selectedIds.has('j2')).toBe(true);
  });

  it('deselects visible items without clearing other selected items', () => {
    const { result } = renderHook(() => useJobSelection([job1, job2, job3]));

    act(() => {
      result.current.selectVisible(['j1', 'j2', 'j3']);
    });

    act(() => {
      result.current.deselectVisible(['j2']);
    });

    expect(result.current.selectedIds.has('j1')).toBe(true);
    expect(result.current.selectedIds.has('j2')).toBe(false);
    expect(result.current.selectedIds.has('j3')).toBe(true);
  });

  it('prunes selected IDs when jobs no longer exist', () => {
    const { result, rerender } = renderHook(
      ({ jobs }: { jobs: Job[] }) => useJobSelection(jobs),
      { initialProps: { jobs: [job1, job2, job3] } }
    );

    act(() => {
      result.current.selectVisible(['j1', 'j2', 'j3']);
    });

    expect(result.current.selectedIds.size).toBe(3);

    // j3 removed from jobs
    rerender({ jobs: [job1, job2] });

    expect(result.current.selectedIds.has('j1')).toBe(true);
    expect(result.current.selectedIds.has('j2')).toBe(true);
    expect(result.current.selectedIds.has('j3')).toBe(false);
    expect(result.current.selectedIds.size).toBe(2);
  });

  it('retains selections across filter switching and SSE job replacement', () => {
    const { result, rerender } = renderHook(
      ({ jobs }: { jobs: Job[] }) => useJobSelection(jobs),
      { initialProps: { jobs: [job1, job2] } }
    );

    act(() => {
      result.current.selectVisible(['j1']);
    });

    // Simulate switching filter to Completed (where j2 is visible) and selecting j2
    act(() => {
      result.current.selectVisible(['j2']);
    });

    expect(result.current.selectedIds.has('j1')).toBe(true);
    expect(result.current.selectedIds.has('j2')).toBe(true);

    // Simulate SSE update that replaces job1 with an updated object instance
    const updatedJob1: Job = { id: 'j1', status: 'downloading', completedBytes: 500 } as unknown as Job;
    rerender({ jobs: [updatedJob1, job2] });

    expect(result.current.selectedIds.has('j1')).toBe(true);
    expect(result.current.selectedIds.has('j2')).toBe(true);
  });
});

