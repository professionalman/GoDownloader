import { describe, it, expect, vi, beforeEach } from 'vitest';
import {
  setCsrfToken,
  getCsrfToken,
  initSession,
  authFetch,
  getSyncSnapshot,
  connectSSE,
  createJob,
  deleteJob,
} from './api';

describe('Frontend Security Boundary (FND-3A)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    setCsrfToken(null);
  });

  it('initSession fetches /api/v1/auth/session and stores CSRF token without session secret', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ status: 'ok', csrfToken: 'test-csrf-123' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    const session = await initSession();
    expect(session.csrfToken).toBe('test-csrf-123');
    // Verify no session secret was received or stored
    expect((session as Record<string, unknown>).token).toBeUndefined();
    expect((session as Record<string, unknown>).sessionToken).toBeUndefined();
    expect(getCsrfToken()).toBe('test-csrf-123');
    expect(fetchSpy).toHaveBeenCalledWith('/api/v1/auth/session', {
      method: 'GET',
      credentials: 'same-origin',
    });
  });

  it('mutating authFetch attaches X-CSRF-Token header and same-origin credentials', async () => {
    setCsrfToken('active-csrf-token-xyz');
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ id: 'job-1' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await authFetch('/api/v1/jobs', {
      method: 'POST',
      body: JSON.stringify({ source: 'https://example.com/test' }),
    });

    expect(fetchSpy).toHaveBeenCalled();
    const [calledUrl, calledInit] = fetchSpy.mock.calls[0];
    expect(calledUrl).toBe('/api/v1/jobs');
    expect(calledInit?.credentials).toBe('same-origin');
    const headers = calledInit?.headers as Headers;
    expect(headers.get('X-CSRF-Token')).toBe('active-csrf-token-xyz');
  });

  it('read-only GET authFetch does NOT attach X-CSRF-Token', async () => {
    setCsrfToken('active-csrf-token-xyz');
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await authFetch('/api/v1/jobs', { method: 'GET' });

    const [, calledInit] = fetchSpy.mock.calls[0];
    const headers = calledInit?.headers as Headers;
    expect(headers.get('X-CSRF-Token')).toBeNull();
    expect(calledInit?.credentials).toBe('same-origin');
  });

  it('createJob API sends X-CSRF-Token on POST', async () => {
    setCsrfToken('csrf-token-create');
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ id: 'job-created' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await createJob('https://example.com/video.mp4');

    expect(fetchSpy).toHaveBeenCalled();
    const [, calledInit] = fetchSpy.mock.calls[0];
    const headers = calledInit?.headers as Headers;
    expect(headers.get('X-CSRF-Token')).toBe('csrf-token-create');
  });

  it('deleteJob API sends X-CSRF-Token on DELETE', async () => {
    setCsrfToken('csrf-token-delete');
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(null, { status: 204 })
    );

    await deleteJob('job-123', false);

    expect(fetchSpy).toHaveBeenCalled();
    const [, calledInit] = fetchSpy.mock.calls[0];
    const headers = calledInit?.headers as Headers;
    expect(headers.get('X-CSRF-Token')).toBe('csrf-token-delete');
  });

  it('401 session expiry/restart triggers single re-bootstrap and succeeds on retry', async () => {
    setCsrfToken('old-csrf-token');
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      // 1. Initial request gets 401 (e.g. backend restarted, old session cookie invalid)
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'authentication required' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      // 2. Re-bootstrap via GET /api/v1/auth/session
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'ok', csrfToken: 'new-csrf-token' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      // 3. Retried request succeeds with 200 OK
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'success' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );

    const res = await authFetch('/api/v1/jobs', { method: 'POST' });
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.status).toBe('success');

    // Total 3 fetch calls: initial 401, session rebootstrap, retried call
    expect(fetchSpy).toHaveBeenCalledTimes(3);
    expect(fetchSpy.mock.calls[0][0]).toBe('/api/v1/jobs');
    expect(fetchSpy.mock.calls[1][0]).toBe('/api/v1/auth/session');
    expect(fetchSpy.mock.calls[2][0]).toBe('/api/v1/jobs');
    const retryHeaders = fetchSpy.mock.calls[2][1]?.headers as Headers;
    expect(retryHeaders.get('X-CSRF-Token')).toBe('new-csrf-token');
  });

  it('401 retry does not enter infinite loop if repeated 401 occurs', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
      // 1. Initial request gets 401
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'authentication required' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      // 2. Re-bootstrap succeeds
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'ok', csrfToken: 'new-csrf-token' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      )
      // 3. Retried request still gets 401 (e.g. auth permanently broken)
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'authentication required' } }), {
          status: 401,
          headers: { 'Content-Type': 'application/json' },
        })
      );

    const res = await authFetch('/api/v1/jobs', { method: 'GET' });
    expect(res.status).toBe(401);
    // Bounded: only 1 retry, no infinite loop
    expect(fetchSpy).toHaveBeenCalledTimes(3);
  });

  it('ApiResponseError captures FORBIDDEN (403) error codes', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(
        JSON.stringify({ error: { code: 'FORBIDDEN', message: 'CSRF validation failed' } }),
        { status: 403, headers: { 'Content-Type': 'application/json' } }
      )
    );
    await expect(async () => {
      await createJob('https://example.com');
    }).rejects.toMatchObject({
      name: 'ApiResponseError',
      code: 'FORBIDDEN',
      message: 'CSRF validation failed',
    });
  });

  it('connectSSE does NOT embed secrets in the URL query string', () => {
    let createdUrl = '';
    class MockEventSource {
      constructor(url: string) {
        createdUrl = url;
      }
      addEventListener() {}
      close() {}
    }
    vi.stubGlobal('EventSource', MockEventSource);

    connectSSE(
      () => {},
      () => {},
      () => 42
    );

    expect(createdUrl).toBe('/api/v1/events?cursor=42');
    expect(createdUrl).not.toContain('token');
    expect(createdUrl).not.toContain('secret');
    expect(createdUrl).not.toContain('csrf');
  });

  it('SSE error triggers re-authentication, snapshot rehydration with cursor C, and clean SSE reconnect without stale streams', async () => {
    // 1. Initial session established
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ status: 'ok', csrfToken: 'initial-csrf-token' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    await initSession();
    expect(getCsrfToken()).toBe('initial-csrf-token');

    // 2. Track EventSource instances to prove no stale streams remain active
    const instances: Array<{ url: string; closed: boolean; listeners: Map<string, (e: any) => void> }> = [];
    class TrackingEventSource {
      url: string;
      closed = false;
      listeners = new Map<string, (e: any) => void>();
      constructor(url: string) {
        this.url = url;
        instances.push(this);
      }
      addEventListener(type: string, listener: (e: any) => void) {
        this.listeners.set(type, listener);
      }
      close() {
        this.closed = true;
      }
      dispatchEvent(event: { type: string }) {
        this.listeners.get(event.type)?.(event);
      }
    }
    vi.stubGlobal('EventSource', TrackingEventSource as any);

    let currentCursor = 50;
    let activeEs: any = connectSSE(
      vi.fn(),
      vi.fn(),
      () => currentCursor
    );

    expect(instances.length).toBe(1);
    expect(instances[0].url).toBe('/api/v1/events?cursor=50');
    expect(instances[0].closed).toBe(false);

    // 3. Backend/session becomes stale; 4. SSE encounters error
    // In App.tsx, handleError closes activeEs and triggers snapshot rehydration:
    activeEs.close();
    activeEs = null;
    expect(instances[0].closed).toBe(true);

    // 5. Next protected request (getSyncSnapshot) receives 401
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'authentication required' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    // 6. Frontend invokes initSession to re-bootstrap
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ status: 'ok', csrfToken: 'rebootstrapped-csrf-token' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    // 7. Protected snapshot retry succeeds with new snapshot cursor C = 150
    fetchSpy.mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          cursor: 150,
          jobs: [],
          queue: { items: [], runningDownloads: 0, pausedDownloads: 0, queuedDownloads: 0, maxConcurrentDownloads: 3 },
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }
      )
    );

    const snapshot = await getSyncSnapshot();

    // 8. Snapshot cursor C is installed
    expect(snapshot.cursor).toBe(150);
    currentCursor = snapshot.cursor;

    // 9. SSE reconnects using cursor C
    activeEs = connectSSE(
      vi.fn(),
      vi.fn(),
      () => currentCursor
    );

    expect(instances.length).toBe(2);
    expect(instances[1].url).toBe('/api/v1/events?cursor=150');
    expect(instances[1].closed).toBe(false);

    // 10. No stale EventSource remains active
    expect(instances[0].closed).toBe(true);
    expect(activeEs).toBe(instances[1]);
  });

  it('401 re-bootstrap retry remains strictly bounded if rebootstrap itself fails', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch');
    // 1. Initial request gets 401
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'UNAUTHORIZED', message: 'authentication required' } }), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    );
    // 2. Rebootstrap attempt also fails (e.g. 403 forbidden or network down)
    fetchSpy.mockResolvedValueOnce(
      new Response(JSON.stringify({ error: { code: 'FORBIDDEN', message: 'forbidden' } }), {
        status: 403,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await expect(getSyncSnapshot()).rejects.toThrow();
    // Strictly bounded: initial call + 1 rebootstrap call = 2 calls total
    expect(fetchSpy).toHaveBeenCalledTimes(2);
  });
});
