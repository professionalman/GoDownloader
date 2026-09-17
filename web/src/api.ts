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
  MediaAuthSettings,
  UpdateMediaAuthPayload,
  SubtitleOptions,
  SelectFormatRequest,
  SyncSnapshot,
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

let cachedCsrfToken: string | null = null;

export function setCsrfToken(token: string | null): void {
  cachedCsrfToken = token;
}

export function getCsrfToken(): string | null {
  if (cachedCsrfToken) return cachedCsrfToken;
  if (typeof document !== 'undefined' && document.cookie) {
    const match = document.cookie.match(/(^|;)\s*godownloader_csrf=([^;]+)/);
    if (match) return decodeURIComponent(match[2]);
  }
  return null;
}

export async function initSession(): Promise<{ csrfToken: string }> {
  try {
    const res = await fetch(`${API_BASE}/auth/session`, {
      method: 'GET',
      credentials: 'same-origin',
    });
    if (res.ok) {
      const data = await res.json();
      if (data.csrfToken) {
        cachedCsrfToken = data.csrfToken;
      }
      return data;
    }
  } catch {
    // Ignore bootstrap errors if offline; cookie or retry will handle it
  }
  return { csrfToken: getCsrfToken() || '' };
}

export async function authFetch(
  url: string,
  init?: RequestInit,
  isRetry = false,
): Promise<Response> {
  const method = (init?.method || 'GET').toUpperCase();
  const isMutating = ['POST', 'PUT', 'DELETE', 'PATCH'].includes(method);
  const headers = new Headers(init?.headers);
  if (isMutating) {
    const csrf = getCsrfToken();
    if (csrf && !headers.has('X-CSRF-Token')) {
      headers.set('X-CSRF-Token', csrf);
    }
  }
  const res = await fetch(url, {
    ...init,
    headers,
    credentials: 'same-origin',
  });

  // Handle session expiration / backend restart gracefully with a single re-bootstrap
  if (res.status === 401 && !isRetry && !url.includes('/auth/session')) {
    const session = await initSession();
    if (session.csrfToken) {
      const retryHeaders = new Headers(init?.headers);
      if (isMutating) {
        retryHeaders.set('X-CSRF-Token', session.csrfToken);
      }
      return authFetch(url, { ...init, headers: retryHeaders }, true);
    }
  }

  return res;
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
  const res = await authFetch(`${API_BASE}/jobs`, {
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
  const res = await authFetch(`${API_BASE}/jobs/batch`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<CreateBatchResponse>(res);
}

export async function bulkAction(action: 'pause' | 'resume' | 'cancel' | 'retry', jobIds: string[]): Promise<BulkActionResponse> {
  const body: BulkActionRequest = { action, jobIds };
  const res = await authFetch(`${API_BASE}/jobs/bulk`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  return handleResponse<BulkActionResponse>(res);
}

export async function setJobPriority(jobId: string, priority: JobPriority): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${jobId}/priority`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ priority }),
  });
  return handleResponse<Job>(res);
}

export async function getQueueSnapshot(): Promise<QueueSnapshot> {
  const res = await authFetch(`${API_BASE}/queue`);
  return handleResponse<QueueSnapshot>(res);
}

export async function reorderQueue(priority: JobPriority, jobIds: string[]): Promise<void> {
  const res = await authFetch(`${API_BASE}/queue/reorder`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ priority, jobIds }),
  });
  await handleResponse<{ status: string }>(res);
}

export async function getSettings(): Promise<AppSettings> {
  const res = await authFetch(`${API_BASE}/settings`);
  return handleResponse<AppSettings>(res);
}

export async function updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings> {
  const res = await authFetch(`${API_BASE}/settings`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<AppSettings>(res);
}

export async function getCategories(): Promise<Category[]> {
  const res = await authFetch(`${API_BASE}/categories`);
  return handleResponse<Category[]>(res);
}

export async function createCategory(payload: CreateCategoryPayload): Promise<Category> {
  const res = await authFetch(`${API_BASE}/categories`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<Category>(res);
}

export async function updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category> {
  const res = await authFetch(`${API_BASE}/categories/${id}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<Category>(res);
}

export async function deleteCategory(id: string): Promise<void> {
  const res = await authFetch(`${API_BASE}/categories/${id}`, {
    method: 'DELETE',
  });
  await handleResponse<{ status: string }>(res);
}

export async function getJobs(): Promise<Job[]> {
  const res = await authFetch(`${API_BASE}/jobs`);
  return handleResponse<Job[]>(res);
}

export async function getJob(id: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${id}`);
  return handleResponse<Job>(res);
}

export async function pauseJob(id: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${id}/pause`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function resumeJob(id: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${id}/resume`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function retryJob(id: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${id}/retry`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function cancelJob(id: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${id}/cancel`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function deleteJob(id: string, deleteFiles: boolean): Promise<void> {
  const res = await authFetch(`${API_BASE}/jobs/${encodeURIComponent(id)}`, {
    method: 'DELETE',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ deleteFiles }),
  });
  if (!res.ok) {
    const data = await res.json().catch(() => ({}));
    throw new Error(data?.error?.message || `Failed to delete job: ${res.statusText}`);
  }
}

export async function selectFormat(
  jobId: string,
  formatId: string,
  subtitleOptions?: SubtitleOptions,
): Promise<Job> {
  const payload: SelectFormatRequest = { formatId };
  if (subtitleOptions) {
    payload.subtitleOptions = subtitleOptions;
  }
  const res = await authFetch(`${API_BASE}/jobs/${jobId}/format`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<Job>(res);
}

export async function getSyncSnapshot(): Promise<SyncSnapshot> {
  const res = await authFetch(`${API_BASE}/sync/snapshot`);
  return handleResponse<SyncSnapshot>(res);
}

export function connectSSE(
  onEvent: (eventType: string, job: Job, seq?: number) => void,
  onSyncRequired?: (payload: { cursor: number; reason: string }) => void,
  getCursor?: () => number | undefined
): EventSource {
  const cursorVal = typeof getCursor === 'function' ? getCursor() : undefined;
  const url = cursorVal !== undefined && cursorVal >= 0
    ? `${API_BASE}/events?cursor=${encodeURIComponent(cursorVal)}`
    : `${API_BASE}/events`;

  const es = new EventSource(url);

  const handler = (e: MessageEvent) => {
    try {
      const job: Job = JSON.parse(e.data);
      const seq = e.lastEventId ? parseInt(e.lastEventId, 10) : undefined;
      onEvent(e.type, job, Number.isNaN(seq) ? undefined : seq);
    } catch {
      // ignore parse errors
    }
  };

  es.addEventListener('job.created', handler);
  es.addEventListener('job.updated', handler);
  es.addEventListener('job.completed', handler);
  es.addEventListener('job.failed', handler);
  es.addEventListener('job.cancelled', handler);
  es.addEventListener('job.deleted', handler);

  if (onSyncRequired) {
    es.addEventListener('sync.required', (e: MessageEvent) => {
      try {
        es.close();
      } catch {
        // ignore
      }
      try {
        const payload = JSON.parse(e.data);
        onSyncRequired(payload);
      } catch {
        onSyncRequired({ cursor: 0, reason: 'unknown' });
      }
    });
  }

  es.onerror = () => {
    // EventSource will auto-reconnect
  };

  return es;
}

export async function openFolder(): Promise<void> {
  await authFetch(`${API_BASE}/open-folder`, { method: 'POST' });
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
  const res = await authFetch(`${API_BASE}/jobs/torrent`, {
    method: 'POST',
    body: formData,
  });
  return handleResponse<Job>(res);
}

export async function getTorrentFiles(jobId: string): Promise<TorrentFile[]> {
  const res = await authFetch(`${API_BASE}/jobs/${jobId}/torrent/files`);
  return handleResponse<TorrentFile[]>(res);
}

export async function startTorrent(jobId: string, files: TorrentFileSelection[], seedingPolicy: SeedingPolicy): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${jobId}/torrent/start`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ files, seedingPolicy }),
  });
  return handleResponse<Job>(res);
}

export async function getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }> {
  return handleResponse(await authFetch(`${API_BASE}/capabilities`));
}

export async function resolveCapabilities(source: string | string[]): Promise<JobCapabilities> {
  return handleResponse(await authFetch(`${API_BASE}/capabilities/resolve`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ source }),
  }));
}

export async function getJobCapabilities(jobId: string): Promise<JobCapabilities> {
  return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/capabilities`));
}

export async function updateJobNetwork(jobId: string, limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }): Promise<Job> {
  return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/network`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(limits),
  }));
}

export async function addTorrentTrackers(jobId: string, trackers: string[]): Promise<{ trackers: { url: string }[] }> {
  return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/torrent/trackers`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ trackers }),
  }));
}

export async function updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job> {
  return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/torrent/seeding-policy`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(policy),
  }));
}

export async function getTrackerSources(): Promise<TrackerSource[]> {
  return handleResponse(await authFetch(`${API_BASE}/tracker-sources`));
}

export async function createTrackerSource(input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>): Promise<TrackerSource> {
  return handleResponse(await authFetch(`${API_BASE}/tracker-sources`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }));
}

export async function updateTrackerSource(id: string, input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }): Promise<TrackerSource> {
  return handleResponse(await authFetch(`${API_BASE}/tracker-sources/${id}`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(input),
  }));
}

export async function deleteTrackerSource(id: string): Promise<void> {
  const response = await authFetch(`${API_BASE}/tracker-sources/${id}`, { method: 'DELETE' });
  if (!response.ok) await handleResponse(response);
}

export async function refreshTrackerSource(id: string): Promise<TrackerSource> {
  return handleResponse(await authFetch(`${API_BASE}/tracker-sources/${id}/refresh`, { method: 'POST' }));
}

export async function refreshAllTrackerSources(): Promise<{ failureCount: number }> {
  return handleResponse(await authFetch(`${API_BASE}/tracker-sources/refresh`, { method: 'POST' }));
}

export async function stopSeeding(jobId: string): Promise<Job> {
  const res = await authFetch(`${API_BASE}/jobs/${jobId}/stop-seeding`, { method: 'POST' });
  return handleResponse<Job>(res);
}

export async function getMediaAuth(): Promise<MediaAuthSettings> {
  const res = await authFetch(`${API_BASE}/media-auth`);
  return handleResponse<MediaAuthSettings>(res);
}

export async function updateMediaAuth(payload: UpdateMediaAuthPayload): Promise<MediaAuthSettings> {
  const res = await authFetch(`${API_BASE}/media-auth`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  });
  return handleResponse<MediaAuthSettings>(res);
}

export async function importMediaCookies(file: File): Promise<MediaAuthSettings> {
  const formData = new FormData();
  formData.append('file', file);
  const res = await authFetch(`${API_BASE}/media-auth/cookies`, {
    method: 'POST',
    body: formData,
  });
  return handleResponse<MediaAuthSettings>(res);
}

export async function deleteMediaCookies(): Promise<MediaAuthSettings> {
  const res = await authFetch(`${API_BASE}/media-auth/cookies`, {
    method: 'DELETE',
  });
  return handleResponse<MediaAuthSettings>(res);
}
