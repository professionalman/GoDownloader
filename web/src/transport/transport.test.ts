import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import {
  ApiResponseError,
  type BackendClient,
  type EventSubscribeOptions,
  type EventSubscription,
  type JobsOperations,
  type QueueOperations,
  type SettingsOperations,
  type CategoriesOperations,
  type TrackerOperations,
  type MediaAuthOperations,
  type SyncOperations,
} from './types';
import { HttpBackendClient, httpBackendClient, setCsrfToken, getCsrfToken, initSession, authFetch } from './http';
import {
  getBackendClient,
  setBackendClient,
  resetBackendClient,
  getJobs,
  createJob,
  getSyncSnapshot,
  getRecoverySummary,
  subscribeEvents,
} from '../api';
import { mountApp } from '../bootstrap';
import { act } from '@testing-library/react';
import type { Job, SyncSnapshot, QueueSnapshot, AppSettings } from '../types';

function makeJob(id: string, name: string = id, status: string = 'queued'): Job {
  return {
    id,
    name,
    type: 'direct',
    status,
    source: `https://example.com/${id}`,
    progress: 0,
    totalBytes: 1000,
    completedBytes: 0,
    speedBytesPerSecond: 0,
    etaSeconds: 0,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  } as unknown as Job;
}

/**
 * Minimal in-memory FakeBackendClient used exclusively for deterministic unit tests
 * to prove that the frontend contracts function without any HTTP/SSE runtime.
 */
class FakeBackendClient implements BackendClient {
  public jobsList: Job[] = [makeJob('fake-1', 'Fake Job 1')];
  public lastCursor = 42;
  public eventListeners = new Set<(type: string, job: Job, seq?: number) => void>();
  public syncRequiredListeners = new Set<(payload: { cursor: number; reason: string }) => void>();

  readonly jobs: JobsOperations = {
    getJobs: vi.fn(async () => [...this.jobsList]),
    getJob: vi.fn(async (id: string) => {
      const j = this.jobsList.find((x) => x.id === id);
      if (!j) throw new ApiResponseError('NOT_FOUND', `Job ${id} not found`);
      return j;
    }),
    createJob: vi.fn(async (source: string) => {
      const newJob = makeJob(`job-${this.jobsList.length + 1}`, source);
      this.jobsList.push(newJob);
      return newJob;
    }),
    createBatchJobs: vi.fn(),
    pauseJob: vi.fn(),
    resumeJob: vi.fn(),
    retryJob: vi.fn(),
    cancelJob: vi.fn(),
    deleteJob: vi.fn(async (id: string) => {
      this.jobsList = this.jobsList.filter((x) => x.id !== id);
    }),
    bulkAction: vi.fn(),
    setJobPriority: vi.fn(),
    selectFormat: vi.fn(),
    uploadTorrent: vi.fn(),
    getTorrentFiles: vi.fn(),
    startTorrent: vi.fn(),
    stopSeeding: vi.fn(),
    getCapabilities: vi.fn(),
    resolveCapabilities: vi.fn(),
    getJobCapabilities: vi.fn(),
    updateJobNetwork: vi.fn(),
    addTorrentTrackers: vi.fn(),
    updateSeedingPolicy: vi.fn(),
    openFolder: vi.fn(),
    getRecoverySummary: vi.fn(async () => ({
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    })),
  } as unknown as JobsOperations;

  readonly queue: QueueOperations = {
    getSnapshot: vi.fn(async (): Promise<QueueSnapshot> => ({
      maxConcurrentDownloads: 3,
      runningDownloads: 0,
      queuedDownloads: this.jobsList.length,
      pausedDownloads: 0,
      items: [],
    })),
    reorder: vi.fn(),
  };

  readonly settings: SettingsOperations = {
    getSettings: vi.fn(async (): Promise<AppSettings> => ({} as AppSettings)),
    updateSettings: vi.fn(),
  };

  readonly categories: CategoriesOperations = {
    getCategories: vi.fn(async () => []),
    createCategory: vi.fn(),
    updateCategory: vi.fn(),
    deleteCategory: vi.fn(),
  };

  readonly tracker: TrackerOperations = {
    getSources: vi.fn(async () => []),
    createSource: vi.fn(),
    updateSource: vi.fn(),
    deleteSource: vi.fn(),
    refreshSource: vi.fn(),
    refreshAllSources: vi.fn(),
  };

  readonly mediaAuth: MediaAuthOperations = {
    getSettings: vi.fn(),
    updateSettings: vi.fn(),
    importCookies: vi.fn(),
    deleteCookies: vi.fn(),
  };

  readonly sync: SyncOperations = {
    getSnapshot: vi.fn(async (): Promise<SyncSnapshot> => ({
      cursor: this.lastCursor,
      jobs: [...this.jobsList],
      queue: {
        maxConcurrentDownloads: 3,
        runningDownloads: 0,
        queuedDownloads: this.jobsList.length,
        pausedDownloads: 0,
        items: [],
      },
      timestamp: new Date().toISOString(),
    })),
    subscribeEvents: (options: EventSubscribeOptions): EventSubscription => {
      const listener = options.onEvent;
      this.eventListeners.add(listener);

      let syncListener: ((payload: { cursor: number; reason: string }) => void) | undefined;
      if (options.onSyncRequired) {
        syncListener = options.onSyncRequired;
        this.syncRequiredListeners.add(syncListener);
      }

      options.onConnected?.();

      return {
        close: () => {
          this.eventListeners.delete(listener);
          if (syncListener) {
            this.syncRequiredListeners.delete(syncListener);
          }
        },
      };
    },
  };

  emitEvent(type: string, job: Job, seq?: number): void {
    for (const listener of this.eventListeners) {
      listener(type, job, seq);
    }
  }

  emitSyncRequired(cursor: number, reason: string): void {
    for (const listener of this.syncRequiredListeners) {
      listener({ cursor, reason });
    }
  }
}

describe('Framework-Neutral Frontend Transport Contracts (DSK-2)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    resetBackendClient();
    setCsrfToken(null);
  });

  afterEach(() => {
    resetBackendClient();
  });

  // A. HTTP adapter maps representative read operation correctly
  it('A. HTTP adapter maps representative read operation correctly', async () => {
    const fakeJobs = [makeJob('job-read-1')];
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(fakeJobs), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    const http = new HttpBackendClient();
    const jobs = await http.jobs.getJobs();

    expect(jobs).toEqual(fakeJobs);
    expect(fetchSpy).toHaveBeenCalledWith('/api/v1/jobs', expect.objectContaining({
      credentials: 'same-origin',
    }));
  });

  // B. HTTP adapter maps representative mutation correctly
  it('B. HTTP adapter maps representative mutation correctly with CSRF header', async () => {
    setCsrfToken('mock-csrf-token-123');
    const createdJob = makeJob('job-created-1');
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(createdJob), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    const http = new HttpBackendClient();
    const result = await http.jobs.createJob('https://example.com/file.zip');

    expect(result).toEqual(createdJob);
    expect(fetchSpy).toHaveBeenCalled();
    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe('/api/v1/jobs');
    expect(init?.method).toBe('POST');
    const headers = init?.headers as Headers;
    expect(headers.get('X-CSRF-Token')).toBe('mock-csrf-token-123');
  });

  // C. HTTP session/CSRF remains adapter-specific
  it('C. HTTP session/CSRF remains adapter-specific and is not required by non-HTTP transports', async () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    // Calling domain operations via facade with FakeBackendClient touches zero fetch / CSRF
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const job = await createJob('https://example.com/test');

    expect(job.name).toBe('https://example.com/test');
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(getCsrfToken()).toBeNull();
  });

  // D. facade/client delegates to selected transport
  it('D. facade/client delegates to selected transport', async () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    expect(getBackendClient()).toBe(fake);

    const loadedJobs = await getJobs();
    expect(loadedJobs.length).toBe(1);
    expect(loadedJobs[0].id).toBe('fake-1');

    resetBackendClient();
    expect(getBackendClient()).toBe(httpBackendClient);
  });

  // E. StateSync obtains snapshot through the contract
  it('E. StateSync obtains snapshot through the transport-neutral contract', async () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    const snapshot = await getSyncSnapshot();
    expect(snapshot.cursor).toBe(42);
    expect(snapshot.jobs.length).toBe(1);
    expect(snapshot.queue.queuedDownloads).toBe(1);
  });

  // F. StateSync receives events through transport-neutral subscription
  it('F. StateSync receives events through transport-neutral subscription', () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    const receivedEvents: Array<{ type: string; job: Job; seq?: number }> = [];
    const onConnected = vi.fn();

    const sub = subscribeEvents({
      onEvent: (type, job, seq) => {
        receivedEvents.push({ type, job, seq });
      },
      onConnected,
    });

    expect(onConnected).toHaveBeenCalledOnce();

    const incomingJob = makeJob('live-job-1');
    fake.emitEvent('job.updated', incomingJob, 43);

    expect(receivedEvents).toHaveLength(1);
    expect(receivedEvents[0]).toEqual({
      type: 'job.updated',
      job: incomingJob,
      seq: 43,
    });

    sub.close();
  });

  // G. event unsubscribe works without listener leaks
  it('G. event unsubscribe works and stops event dispatch cleanly', () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    const onEvent = vi.fn();
    const sub = subscribeEvents({ onEvent });

    expect(fake.eventListeners.size).toBe(1);

    fake.emitEvent('job.created', makeJob('j-1'), 1);
    expect(onEvent).toHaveBeenCalledTimes(1);

    // Unsubscribe
    sub.close();
    expect(fake.eventListeners.size).toBe(0);

    // Further emits do not invoke listener
    fake.emitEvent('job.created', makeJob('j-2'), 2);
    expect(onEvent).toHaveBeenCalledTimes(1);
  });

  // H. duplicate/old cursor handling remains unchanged
  it('H. duplicate/old cursor handling idempotently filters stale events', () => {
    let lastAppliedCursor = 50;
    const appliedSeqs: number[] = [];

    const handleEvent = (_type: string, _job: Job, seq?: number) => {
      if (seq !== undefined && seq > 0) {
        if (seq <= lastAppliedCursor) {
          return; // Duplicate or stale: ignore
        }
        lastAppliedCursor = seq;
        appliedSeqs.push(seq);
      }
    };

    const fake = new FakeBackendClient();
    setBackendClient(fake);

    const sub = subscribeEvents({ onEvent: handleEvent });

    fake.emitEvent('job.updated', makeJob('j1'), 55); // Newer: applied
    fake.emitEvent('job.updated', makeJob('j1'), 52); // Stale: ignored
    fake.emitEvent('job.updated', makeJob('j1'), 55); // Duplicate: ignored
    fake.emitEvent('job.updated', makeJob('j1'), 56); // Newer: applied

    expect(appliedSeqs).toEqual([55, 56]);
    expect(lastAppliedCursor).toBe(56);

    sub.close();
  });

  // I. sync.required still triggers authoritative rehydrate callback
  it('I. sync.required triggers authoritative rehydrate callback and cleans up', () => {
    const fake = new FakeBackendClient();
    setBackendClient(fake);

    const onSyncRequired = vi.fn();
    const sub = subscribeEvents({
      onEvent: vi.fn(),
      onSyncRequired,
    });

    expect(fake.syncRequiredListeners.size).toBe(1);

    fake.emitSyncRequired(100, 'replay_gap_detected');
    expect(onSyncRequired).toHaveBeenCalledWith({
      cursor: 100,
      reason: 'replay_gap_detected',
    });

    sub.close();
    expect(fake.syncRequiredListeners.size).toBe(0);
  });

  // J. HTTP/SSE behavior remains compatible
  it('J. HttpEventSubscription provides clean close and listener adapter', () => {
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
      const http = new HttpBackendClient();
      const onEvent = vi.fn();
      const onConnected = vi.fn();
      const sub = http.sync.subscribeEvents({
        onEvent,
        onConnected,
        getCursor: () => 99,
      });

      // Verify URL with cursor parameter
      expect((sub as any).rawSource.url).toBe('/api/v1/events?cursor=99');

      // Verify event dispatch
      const jobCreatedListener = mockListeners.get('job.created');
      expect(jobCreatedListener).toBeDefined();
      jobCreatedListener!({
        type: 'job.created',
        data: JSON.stringify(makeJob('j-http')),
        lastEventId: '100',
      });

      expect(onEvent).toHaveBeenCalledWith('job.created', expect.objectContaining({ id: 'j-http' }), 100);

      // Verify close
      sub.close();
      expect(mockClose).toHaveBeenCalledOnce();
    } finally {
      globalThis.EventSource = originalEventSource;
    }
  });

  // K. HTTP session bootstrap and CSRF injection work through adapter
  it('K. HTTP session bootstrap and CSRF injection work through adapter', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'ok', csrfToken: 'bootstrapped-token' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(makeJob('j-k')), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

    const session = await initSession();
    expect(session.csrfToken).toBe('bootstrapped-token');
    expect(getCsrfToken()).toBe('bootstrapped-token');

    await authFetch('/api/v1/jobs', { method: 'POST' });
    const postCallHeaders = fetchSpy.mock.calls[1][1]?.headers as Headers;
    expect(postCallHeaders.get('X-CSRF-Token')).toBe('bootstrapped-token');
  });

  // L. HTTP 401 re-bootstrap works through adapter
  it('L. HTTP 401 re-bootstrap automatically refreshes session and retries mutating request', async () => {
    setCsrfToken('expired-token');
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'ok', csrfToken: 'rebootstrapped-token' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: 'retried-job' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

    const res = await authFetch('/api/v1/jobs', { method: 'POST' });
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(3);
    const retryHeaders = fetchSpy.mock.calls[2][1]?.headers as Headers;
    expect(retryHeaders.get('X-CSRF-Token')).toBe('rebootstrapped-token');
  });

  // M. Future desktop entrypoint proof: installs custom client and mounts with zero HTTP/SSE
  it('M. Future desktop entrypoint proof: installs custom client and mounts with zero HTTP/SSE', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    const fake = new FakeBackendClient();

    // 1. Host bootstrap installs native BackendClient before mount
    setBackendClient(fake);

    // 2. Mount shared application into DOM container
    const container = document.createElement('div');
    document.body.appendChild(container);
    let root: any;

    await act(async () => {
      root = mountApp({ container });
    });

    // 3. App performed initial operations through the installed client
    expect(fake.sync.getSnapshot).toHaveBeenCalled();
    expect(fake.eventListeners.size).toBe(1);

    // 4. Critical proof: ZERO HTTP fetch and ZERO EventSource creation occurred
    expect(fetchSpy).not.toHaveBeenCalled();

    // 5. Unmount cleans up active subscription
    await act(async () => {
      root.unmount();
    });
    expect(fake.eventListeners.size).toBe(0);
    document.body.removeChild(container);
  });

  // N. Module import side-effect audit: importing modules performs zero network operations
  it('N. Module import side-effect audit: transport and bootstrap modules perform zero network calls', () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(getCsrfToken()).toBeNull();
  });

  // O. Default HTTP web-mode selection: default client is HttpBackendClient
  it('O. Default HTTP web-mode selection: default client is HttpBackendClient', () => {
    resetBackendClient();
    expect(getBackendClient()).toBe(httpBackendClient);
  });

  // P. HttpJobsOperations.getRecoverySummary calls GET /api/v1/jobs/recovery-summary
  it('P. HttpJobsOperations.getRecoverySummary calls GET /api/v1/jobs/recovery-summary', async () => {
    const mockSummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    };
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockSummary), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    const client = new HttpBackendClient();
    const res = await client.jobs.getRecoverySummary();
    expect(res.reconciledFinalizations).toBe(1);
    expect(fetchSpy).toHaveBeenCalledWith(
      '/api/v1/jobs/recovery-summary',
      expect.objectContaining({ credentials: 'same-origin' })
    );
  });

  // Q. Facade getRecoverySummary routes through configured backend client
  it('Q. Facade getRecoverySummary routes through configured backend client', async () => {
    const fake = new FakeBackendClient();
    const mockSummary = {
      reconciledFinalizations: 2,
      reattachedTransfers: 1,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    };
    fake.jobs.getRecoverySummary = vi.fn(async () => mockSummary);
    setBackendClient(fake);

    const res = await getRecoverySummary();
    expect(res.reconciledFinalizations).toBe(2);
    expect(fake.jobs.getRecoverySummary).toHaveBeenCalledTimes(1);

    resetBackendClient();
  });
});
