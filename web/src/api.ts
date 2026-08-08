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
  JobNetworkPolicyOverride,
  SeedingPolicy,
  JobCapabilities,
  TrackerSource,
} from './types';

const API_BASE = '/api/v1';

/** Error class that carries the backend error code alongside the message. */
export class ApiResponseError extends Error {
  public readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'ApiResponseError';
    this.code = code;
  }
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.json().catch(() => null) as ApiError | null;
    const code = body?.error?.code ?? 'UNKNOWN';
    const message = body?.error?.message || `Request failed with status ${res.status}`;
    throw new ApiResponseError(code, message);
  }
  return res.json();
}

export async function createJob(
  source: string,
  priority: JobPriority = 'normal',
  categoryId?: string,
  destinationDir?: string,
  conflictPolicy?: FilenameConflictPolicy
  ,
  networkPolicy?: JobNetworkPolicyOverride,
  seedingPolicy?: SeedingPolicy,
  trackers?: string[]
): Promise<Job> {
  const body: CreateJobRequest = { source, priority, categoryId, destinationDir, conflictPolicy, networkPolicy, seedingPolicy, trackers };
  const res = await fetch(`${API_BASE}/jobs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<Job>(res);
}

export async function createBatchJobs(
  inputs: { source: string; priority?: JobPriority; categoryId?: string; destinationDir?: string; conflictPolicy?: FilenameConflictPolicy; networkPolicy?: JobNetworkPolicyOverride; seedingPolicy?: SeedingPolicy; trackers?: string[] }[]
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
  destinationDir?: string,
  networkPolicy?: JobNetworkPolicyOverride,
  seedingPolicy?: SeedingPolicy,
  trackers?: string[]
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
  if (networkPolicy) formData.append('networkPolicy', JSON.stringify(networkPolicy));
  if (seedingPolicy) formData.append('seedingPolicy', JSON.stringify(seedingPolicy));
  if (trackers?.length) formData.append('trackers', JSON.stringify(trackers));
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

export async function startTorrent(jobId: string, files: TorrentFileSelection[], seedingPolicy: SeedingPolicy): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/torrent/start`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ files, seedingPolicy }),
  });
  return handleResponse<Job>(res);
}

export async function getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }> {
  return handleResponse(await fetch(`${API_BASE}/capabilities`));
}

export async function resolveCapabilities(source: string | string[]): Promise<JobCapabilities> {
  return handleResponse(await fetch(`${API_BASE}/capabilities/resolve`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source }),
  }));
}

export async function getJobCapabilities(jobId: string): Promise<JobCapabilities> {
  return handleResponse(await fetch(`${API_BASE}/jobs/${jobId}/capabilities`));
}

export async function updateJobNetwork(jobId: string, limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }): Promise<Job> {
  return handleResponse(await fetch(`${API_BASE}/jobs/${jobId}/network`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(limits),
  }));
}

export async function addTorrentTrackers(jobId: string, trackers: string[]): Promise<{ trackers: { url: string }[] }> {
  return handleResponse(await fetch(`${API_BASE}/jobs/${jobId}/torrent/trackers`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ trackers }),
  }));
}

export async function updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job> {
  return handleResponse(await fetch(`${API_BASE}/jobs/${jobId}/torrent/seeding-policy`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(policy),
  }));
}

export async function getTrackerSources(): Promise<TrackerSource[]> {
  return handleResponse(await fetch(`${API_BASE}/tracker-sources`));
}

export async function createTrackerSource(input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>): Promise<TrackerSource> {
  return handleResponse(await fetch(`${API_BASE}/tracker-sources`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }));
}

export async function updateTrackerSource(id: string, input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }): Promise<TrackerSource> {
  return handleResponse(await fetch(`${API_BASE}/tracker-sources/${id}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }));
}

export async function deleteTrackerSource(id: string): Promise<void> {
  const response = await fetch(`${API_BASE}/tracker-sources/${id}`, { method: 'DELETE' });
  if (!response.ok) await handleResponse(response);
}

export async function refreshTrackerSource(id: string): Promise<TrackerSource> {
  return handleResponse(await fetch(`${API_BASE}/tracker-sources/${id}/refresh`, { method: 'POST' }));
}

export async function refreshAllTrackerSources(): Promise<{ failureCount: number }> {
  return handleResponse(await fetch(`${API_BASE}/tracker-sources/refresh`, { method: 'POST' }));
}

export async function stopSeeding(jobId: string): Promise<Job> {
  const res = await fetch(`${API_BASE}/jobs/${jobId}/stop-seeding`, { method: 'POST' });
  return handleResponse<Job>(res);
}
