import type {
  Job,
  JobPriority,
  CreateJobRequest,
  CreateBatchRequest,
  CreateBatchResponse,
  BulkActionRequest,
  BulkActionResponse,
  QueueSnapshot,
  AppSettings,
  UpdateSettingsPayload,
  ApiError,
  TorrentFile,
  TorrentFileSelection,
  Category,
  CreateCategoryPayload,
  UpdateCategoryPayload,
  FilenameConflictPolicy,
} from './types';

const API_BASE = '/api/v1';

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => null) as ApiError | null;
    throw new Error(body?.error?.message || `Request failed with status ${res.status}`);
  }
  return res.json();
}

export async function createJob(
  source: string,
  priority: JobPriority = 'normal',
  categoryId?: string,
  destinationDir?: string,
  conflictPolicy?: FilenameConflictPolicy
): Promise<Job> {
  const body: CreateJobRequest = { source, priority, categoryId, destinationDir, conflictPolicy };
  const res = await fetch(`${API_BASE}/jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<Job>(res);
}

export async function createBatchJobs(
  inputs: { source: string; priority?: JobPriority; categoryId?: string; destinationDir?: string; conflictPolicy?: FilenameConflictPolicy }[]
): Promise<CreateBatchResponse> {
  const body: CreateBatchRequest = { inputs };
  const res = await fetch(`${API_BASE}/jobs/batch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<CreateBatchResponse>(res);
}

export async function bulkAction(action: 'pause' | 'resume' | 'cancel' | 'retry', jobIds: string[]): Promise<BulkActionResponse> {
  const body: BulkActionRequest = { action, jobIds };
  const res = await fetch(`${API_BASE}/jobs/bulk`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<BulkActionResponse>(res);
}

export async function setJobPriority(jobId: string, priority: JobPriority): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/priority`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ priority }),
  });
  return handleResponse<Job>(res);
}

export async function getQueueSnapshot(): Promise<QueueSnapshot> {
  const res = await fetch(`${API_BASE}/queue`);
  return handleResponse<QueueSnapshot>(res);
}

export async function reorderQueue(priority: JobPriority, jobIds: string[]): Promise<void> {
  const res = await fetch(`${API_BASE}/queue/reorder`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ priority, jobIds }),
  });
  await handleResponse<{ status: string }>(res);
}

export async function getSettings(): Promise<AppSettings> {
  const res = await fetch(`${API_BASE}/settings`);
  return handleResponse<AppSettings>(res);
}

export async function updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings> {
  const res = await fetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<AppSettings>(res);
}

export async function getCategories(): Promise<Category[]> {
  const res = await fetch(`${API_BASE}/categories`);
  return handleResponse<Category[]>(res);
}

export async function createCategory(payload: CreateCategoryPayload): Promise<Category> {
  const res = await fetch(`${API_BASE}/categories`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<Category>(res);
}

export async function updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category> {
  const res = await fetch(`${API_BASE}/categories/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<Category>(res);
}

export async function deleteCategory(id: string): Promise<void> {
  const res = await fetch(`${API_BASE}/categories/${id}`, {
    method: 'DELETE',
  });
  await handleResponse<{ status: string }>(res);
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

export async function selectFormat(jobId: string, formatId: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/format`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ formatId }),
  });
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

export async function uploadTorrent(
  file: File,
  priority?: JobPriority,
  categoryId?: string,
  destinationDir?: string
): Promise<Job> {
  const formData = new FormData();
  formData.append('torrent', file);
  if (priority) {
    formData.append('priority', priority);
  }
  if (categoryId) {
    formData.append('categoryId', categoryId);
  }
  if (destinationDir) {
    formData.append('destinationDir', destinationDir);
  }
  const res = await fetch(`${API_BASE}/jobs/torrent`, {
    method: 'POST',
    body: formData,
  });
  return handleResponse<Job>(res);
}

export async function getTorrentFiles(jobId: string): Promise<TorrentFile[]> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/torrent/files`);
  return handleResponse<TorrentFile[]>(res);
}

export async function startTorrent(jobId: string, files: TorrentFileSelection[], seedAfterComplete: boolean): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/torrent/start`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ files, seedAfterComplete }),
  });
  return handleResponse<Job>(res);
}

export async function stopSeeding(jobId: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/stop-seeding`, { method: 'POST' });
  return handleResponse<Job>(res);
}
