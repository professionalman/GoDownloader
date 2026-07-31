import { describe, expect, it } from 'vitest';
import type { Job, JobStatus } from './types';
import { deduplicateJobsById, replaceJobsFromInitialLoad, upsertJob, upsertJobs } from './jobState';

function makeJob(id: string, updatedAt: string, status: JobStatus = 'queued', source = `https://example.com/${id}`): Job {
  return { id, updatedAt, status, source, name: id } as Job;
}

describe('Job state reconciliation', () => {
  it('deduplicates initial data by ID and keeps the newest version in the original position', () => {
    const older = makeJob('a', '2026-08-01T08:00:00Z', 'queued');
    const other = makeJob('b', '2026-08-01T08:01:00Z', 'downloading');
    const newer = makeJob('a', '2026-08-01T08:02:00Z', 'analyzing');

    const result = deduplicateJobsById([older, other, newer]);

    expect(result.map((job) => job.id)).toEqual(['a', 'b']);
    expect(result[0]).toBe(newer);
  });

  it('preserves existing positions and removes stale in-memory duplicates during an update', () => {
    const first = makeJob('a', '2026-08-01T08:00:00Z');
    const duplicate = makeJob('a', '2026-08-01T08:01:00Z', 'downloading');
    const second = makeJob('b', '2026-08-01T08:00:00Z');
    const updatedSecond = makeJob('b', '2026-08-01T08:02:00Z', 'paused');

    const result = upsertJob([first, duplicate, second], updatedSecond);

    expect(result.map((job) => job.id)).toEqual(['a', 'b']);
    expect(result[0]).toBe(duplicate);
    expect(result[1]).toBe(updatedSecond);
  });

  it('prepends genuinely new Jobs while preserving batch response order', () => {
    const existing = makeJob('existing', '2026-08-01T08:00:00Z');
    const firstNew = makeJob('new-1', '2026-08-01T08:01:00Z');
    const secondNew = makeJob('new-2', '2026-08-01T08:02:00Z');

    expect(upsertJobs([existing], [firstNew, secondNew]).map((job) => job.id)).toEqual([
      'new-1',
      'new-2',
      'existing',
    ]);
  });

  it('does not allow an older response to overwrite a newer SSE version', () => {
    const newer = makeJob('a', '2026-08-01T08:02:00Z', 'awaiting_selection');
    const stale = makeJob('a', '2026-08-01T08:00:00Z', 'queued');

    expect(upsertJob([newer], stale)).toEqual([newer]);
  });

  it('merges an initial-load response without dropping Jobs already received over SSE', () => {
    const loadedOlder = makeJob('shared', '2026-08-01T08:00:00Z', 'queued');
    const loadedOther = makeJob('loaded', '2026-08-01T08:00:00Z');
    const liveNewer = makeJob('shared', '2026-08-01T08:03:00Z', 'downloading');
    const liveOnly = makeJob('live', '2026-08-01T08:04:00Z');

    const result = replaceJobsFromInitialLoad([liveNewer, liveOnly], [loadedOlder, loadedOlder, loadedOther]);

    expect(result.map((job) => job.id)).toEqual(['live', 'shared', 'loaded']);
    expect(result[1]).toBe(liveNewer);
  });

  it('keeps intentionally repeated sources when Job IDs differ', () => {
    const source = 'https://example.com/repeat';
    const first = makeJob('a', '2026-08-01T08:00:00Z', 'queued', source);
    const second = makeJob('b', '2026-08-01T08:01:00Z', 'queued', source);

    expect(upsertJobs([], [first, second])).toEqual([first, second]);
  });
});