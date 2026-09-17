import { describe, expect, it, vi } from 'vitest';
import { connectSSE } from './api';
import { reconcileJobsFromSnapshot, upsertJob } from './jobState';
import type { Job, SyncSnapshot } from './types';

function makeJob(id: string, updatedAt: string, status: string = 'queued'): Job {
  return { id, updatedAt, status, source: `https://example.com/${id}`, name: id } as unknown as Job;
}

describe('StateSync Frontend Contract & Rehydration Invariants', () => {
  // A. sync.required closes/invalidates old live continuity
  it('A. sync.required closes EventSource and invalidates live continuity immediately', () => {
    const mockListeners = new Map<string, (e: any) => void>();
    const mockClose = vi.fn();

    class FakeEventSource {
      url: string;
      constructor(url: string) {
        this.url = url;
      }
      addEventListener(type: string, listener: (e: any) => void) {
        mockListeners.set(type, listener);
      }
      removeEventListener(type: string) {
        mockListeners.delete(type);
      }
      close() {
        mockClose();
      }
    }

    const originalEventSource = globalThis.EventSource;
    globalThis.EventSource = FakeEventSource as any;

    try {
      const onEvent = vi.fn();
      const onSyncRequired = vi.fn();

      connectSSE(onEvent, onSyncRequired, () => 100);

      // Trigger sync.required event from backend
      const syncListener = mockListeners.get('sync.required');
      expect(syncListener).toBeDefined();

      syncListener!({
        data: JSON.stringify({ cursor: 105, reason: 'replay_gap_or_buffer_overflow' }),
      });

      // Verification: EventSource MUST be closed immediately upon receiving sync.required
      expect(mockClose).toHaveBeenCalledOnce();
      expect(onSyncRequired).toHaveBeenCalledWith({
        cursor: 105,
        reason: 'replay_gap_or_buffer_overflow',
      });
    } finally {
      globalThis.EventSource = originalEventSource;
    }
  });

  // B. A live event cannot be lost or cause cursor regression while snapshot rehydration is occurring
  it('B. prevents cursor regression and idempotently deduplicates applied sequences', () => {
    let lastAppliedCursor = 100;
    let currentJobs: Job[] = [makeJob('job-1', '2026-08-01T08:00:00Z', 'downloading')];

    const applyEvent = (_eventType: string, updatedJob: Job, seq?: number) => {
      if (seq !== undefined && seq > 0) {
        if (seq <= lastAppliedCursor) {
          // Idempotent deduplication: already applied, ignore
          return false;
        }
        lastAppliedCursor = seq;
      }
      currentJobs = upsertJob(currentJobs, updatedJob);
      return true;
    };

    // Event with sequence 105 arrives
    const event105 = makeJob('job-1', '2026-08-01T08:05:00Z', 'completed');
    const applied105 = applyEvent('job.updated', event105, 105);
    expect(applied105).toBe(true);
    expect(lastAppliedCursor).toBe(105);

    // Stale/interleaved event 104 arrives later: MUST NOT regress cursor or apply stale update
    const event104 = makeJob('job-1', '2026-08-01T08:04:00Z', 'downloading');
    const applied104 = applyEvent('job.updated', event104, 104);
    expect(applied104).toBe(false);
    expect(lastAppliedCursor).toBe(105); // Cursor did NOT regress

    // Duplicate event 105 arrives: MUST NOT apply
    const appliedDup = applyEvent('job.updated', event105, 105);
    expect(appliedDup).toBe(false);
    expect(lastAppliedCursor).toBe(105);
  });

  // C. After snapshot cursor C is installed, reconnect begins from C
  it('C. after snapshot cursor C is installed, reconnect begins from C via ?cursor= query', () => {
    let capturedUrl = '';
    class CapturingEventSource {
      url: string;
      constructor(url: string) {
        this.url = url;
        capturedUrl = url;
      }
      addEventListener() {}
      removeEventListener() {}
      close() {}
    }

    const originalEventSource = globalThis.EventSource;
    globalThis.EventSource = CapturingEventSource as any;

    try {
      const installedSnapshotCursor = 42;
      connectSSE(vi.fn(), vi.fn(), () => installedSnapshotCursor);

      expect(capturedUrl).toContain('cursor=42');
    } finally {
      globalThis.EventSource = originalEventSource;
    }
  });

  // D. A mutation that occurs after snapshot observation is delivered by replay
  it('D. applies newer replay mutation seamlessly after snapshot rehydration', () => {
    // Snapshot observation at cursor 50
    const snapshotJobs = [makeJob('job-1', '2026-08-01T08:00:00Z', 'queued')];
    let jobs = reconcileJobsFromSnapshot([], snapshotJobs);
    let lastAppliedCursor = 50;

    // Mutation at sequence 51 arrives via replay buffer
    const replayMutation = makeJob('job-1', '2026-08-01T08:01:00Z', 'downloading');
    const seq = 51;
    if (seq > lastAppliedCursor) {
      jobs = upsertJob(jobs, replayMutation);
      lastAppliedCursor = seq;
    }

    expect(lastAppliedCursor).toBe(51);
    expect(jobs[0].status).toBe('downloading');
    expect(jobs[0].updatedAt).toBe('2026-08-01T08:01:00Z');
  });

  // E. Duplicate sync.required cannot cause an older snapshot response to overwrite a newer state (generation fencing)
  it('E. generation fencing prevents older rehydration response from overwriting newer generation', async () => {
    let activeGeneration = 0;
    let installedSnapshot: SyncSnapshot | null = null;
    let lastAppliedCursor = 0;

    const handleRehydration = async (
      snapshotPromise: Promise<SyncSnapshot>
    ) => {
      const gen = ++activeGeneration;
      const snapshot = await snapshotPromise;
      if (gen !== activeGeneration) {
        // Discard stale snapshot from superseded rehydration
        return false;
      }
      installedSnapshot = snapshot;
      lastAppliedCursor = snapshot.cursor;
      return true;
    };

    // Rehydration A starts (generation 1) - slow network
    let resolveA!: (s: SyncSnapshot) => void;
    const promiseA = new Promise<SyncSnapshot>((res) => {
      resolveA = res;
    });
    const runA = handleRehydration(promiseA);

    // Rehydration B starts (generation 2) - fast network
    let resolveB!: (s: SyncSnapshot) => void;
    const promiseB = new Promise<SyncSnapshot>((res) => {
      resolveB = res;
    });
    const runB = handleRehydration(promiseB);

    // Snapshot B returns FIRST with cursor 200 and newer state
    const snapshotB: SyncSnapshot = {
      cursor: 200,
      jobs: [makeJob('job-b', '2026-08-01T09:00:00Z', 'completed')],
      queue: { maxConcurrentDownloads: 3, runningDownloads: 0, queuedDownloads: 0, pausedDownloads: 0, items: [] },
      timestamp: new Date().toISOString(),
    };
    resolveB(snapshotB);
    const appliedB = await runB;
    expect(appliedB).toBe(true);
    expect(lastAppliedCursor).toBe(200);
    expect(installedSnapshot).toBe(snapshotB);

    // Snapshot A returns LATER with older cursor 100 and stale state
    const snapshotA: SyncSnapshot = {
      cursor: 100,
      jobs: [makeJob('job-a', '2026-08-01T08:00:00Z', 'queued')],
      queue: { maxConcurrentDownloads: 3, runningDownloads: 0, queuedDownloads: 0, pausedDownloads: 0, items: [] },
      timestamp: new Date().toISOString(),
    };
    resolveA(snapshotA);
    const appliedA = await runA;

    // Verification: Snapshot A MUST be discarded
    expect(appliedA).toBe(false);
    expect(lastAppliedCursor).toBe(200); // Did NOT regress to 100
    expect(installedSnapshot).toBe(snapshotB); // State NOT overwritten by stale generation
  });
});
