import type { Job, CreateJobRequest, ApiError } from './types';

const API_BASE = '/api/v1';

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => null) as ApiError | null;
    throw new Error(body?.error?.message || `Request failed with status ${res.status}`);
  }
  return res.json();
}

export async function createJob(source: string): Promise<Job> {
  const body: CreateJobRequest = { source };
  const res = await fetch(`${API_BASE}/jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<Job>(res);
}

export async function getJobs(): Promise<Job[]> {
  const res = await fetch(`${API_BASE}/jobs`);
  return handleResponse<Job[]>(res);
}

export async function getJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${id}`);
  return handleResponse<Job>(res);
}

export async function pauseJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${id}/pause`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function resumeJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${id}/resume`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function retryJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${id}/retry`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function cancelJob(id: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${id}/cancel`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export function connectSSE(onEvent: (eventType: string, job: Job) => void): EventSource {
  const es = new EventSource(`${API_BASE}/events`);

  const handler = (e: MessageEvent) => {
    try {
      const job: Job = JSON.parse(e.data);
      onEvent(e.type, job);
    } catch {
      // ignore parse errors
    }
  };

  // Listen to all event types from the backend
  es.addEventListener('job.created', handler);
  es.addEventListener('job.updated', handler);
  es.addEventListener('job.completed', handler);
  es.addEventListener('job.failed', handler);
  es.addEventListener('job.cancelled', handler);

  es.onerror = () => {
    // EventSource will auto-reconnect
  };

  return es;
}

export async function openFolder(): Promise<void> {
  await fetch(`${API_BASE}/open-folder`, { method: 'POST' });
}
