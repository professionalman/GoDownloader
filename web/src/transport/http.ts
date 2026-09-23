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
  RecoverySummary,
} from '../types';
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

const API_BASE = '/api/v1';

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

/** HTTP EventSource subscription wrapper supporting unsubscribe and event listener hooks */
export class HttpEventSubscription implements EventSubscription {
  private es: EventSource | null;
  private onOpenListener: (() => void) | null = null;
  private onErrorListener: (() => void) | null = null;

  constructor(
    es: EventSource,
    onConnected?: () => void,
    onError?: (err?: unknown) => void,
  ) {
    this.es = es;
    if (onConnected) {
      this.onOpenListener = onConnected;
      this.es.addEventListener('open', this.onOpenListener);
    }
    if (onError) {
      this.onErrorListener = () => onError();
      this.es.addEventListener('error', this.onErrorListener);
    }
  }

  close(): void {
    if (!this.es) return;
    if (this.onOpenListener) {
      this.es.removeEventListener('open', this.onOpenListener);
      this.onOpenListener = null;
    }
    if (this.onErrorListener) {
      this.es.removeEventListener('error', this.onErrorListener);
      this.onErrorListener = null;
    }
    try {
      this.es.close();
    } catch {
      // ignore
    }
    this.es = null;
  }

  addEventListener(type: string, listener: (e?: any) => void): void {
    this.es?.addEventListener(type, listener);
  }

  removeEventListener(type: string, listener: (e?: any) => void): void {
    this.es?.removeEventListener(type, listener);
  }

  get rawSource(): EventSource | null {
    return this.es;
  }
}

class HttpJobsOperations implements JobsOperations {
  async getJobs(): Promise<Job[]> {
    const res = await authFetch(`${API_BASE}/jobs`);
    return handleResponse<Job[]>(res);
  }

  async getJob(id: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${id}`);
    return handleResponse<Job>(res);
  }

  async createJob(
    source: string,
    priority: JobPriority = 'normal',
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: FilenameConflictPolicy,
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

  async createBatchJobs(
    inputs: {
      source: string;
      priority?: JobPriority;
      categoryId?: string;
      destinationDir?: string;
      conflictPolicy?: FilenameConflictPolicy;
      networkPolicy?: JobNetworkPolicyOverride;
      seedingPolicy?: SeedingPolicy;
      trackers?: string[];
    }[]
  ): Promise<CreateBatchResponse> {
    const body: CreateBatchRequest = { inputs };
    const res = await authFetch(`${API_BASE}/jobs/batch`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return handleResponse<CreateBatchResponse>(res);
  }

  async pauseJob(id: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${id}/pause`, { method: 'POST' });
    return handleResponse<Job>(res);
  }

  async resumeJob(id: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${id}/resume`, { method: 'POST' });
    return handleResponse<Job>(res);
  }

  async retryJob(id: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${id}/retry`, { method: 'POST' });
    return handleResponse<Job>(res);
  }

  async cancelJob(id: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${id}/cancel`, { method: 'POST' });
    return handleResponse<Job>(res);
  }

  async deleteJob(id: string, deleteFiles: boolean): Promise<void> {
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

  async bulkAction(
    action: 'pause' | 'resume' | 'cancel' | 'retry',
    jobIds: string[]
  ): Promise<BulkActionResponse> {
    const body: BulkActionRequest = { action, jobIds };
    const res = await authFetch(`${API_BASE}/jobs/bulk`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
    return handleResponse<BulkActionResponse>(res);
  }

  async setJobPriority(jobId: string, priority: JobPriority): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${jobId}/priority`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ priority }),
    });
    return handleResponse<Job>(res);
  }

  async selectFormat(
    jobId: string,
    formatId: string,
    subtitleOptions?: SubtitleOptions
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

  async uploadTorrent(
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
    if (priority) formData.append('priority', priority);
    if (categoryId) formData.append('categoryId', categoryId);
    if (destinationDir) formData.append('destinationDir', destinationDir);
    if (networkPolicy) formData.append('networkPolicy', JSON.stringify(networkPolicy));
    if (seedingPolicy) formData.append('seedingPolicy', JSON.stringify(seedingPolicy));
    if (trackers?.length) formData.append('trackers', JSON.stringify(trackers));
    const res = await authFetch(`${API_BASE}/jobs/torrent`, {
      method: 'POST',
      body: formData,
    });
    return handleResponse<Job>(res);
  }

  async getTorrentFiles(jobId: string): Promise<TorrentFile[]> {
    const res = await authFetch(`${API_BASE}/jobs/${jobId}/torrent/files`);
    return handleResponse<TorrentFile[]>(res);
  }

  async startTorrent(
    jobId: string,
    files: TorrentFileSelection[],
    seedingPolicy: SeedingPolicy
  ): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${jobId}/torrent/start`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ files, seedingPolicy }),
    });
    return handleResponse<Job>(res);
  }

  async stopSeeding(jobId: string): Promise<Job> {
    const res = await authFetch(`${API_BASE}/jobs/${jobId}/stop-seeding`, { method: 'POST' });
    return handleResponse<Job>(res);
  }

  async getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }> {
    return handleResponse(await authFetch(`${API_BASE}/capabilities`));
  }

  async resolveCapabilities(source: string | string[]): Promise<JobCapabilities> {
    return handleResponse(await authFetch(`${API_BASE}/capabilities/resolve`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ source }),
    }));
  }

  async getJobCapabilities(jobId: string): Promise<JobCapabilities> {
    return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/capabilities`));
  }

  async updateJobNetwork(
    jobId: string,
    limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }
  ): Promise<Job> {
    return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/network`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(limits),
    }));
  }

  async addTorrentTrackers(
    jobId: string,
    trackers: string[]
  ): Promise<{ trackers: { url: string }[] }> {
    return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/torrent/trackers`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ trackers }),
    }));
  }

  async updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job> {
    return handleResponse(await authFetch(`${API_BASE}/jobs/${jobId}/torrent/seeding-policy`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(policy),
    }));
  }

  async openFolder(): Promise<void> {
    await authFetch(`${API_BASE}/open-folder`, { method: 'POST' });
  }

  async getRecoverySummary(): Promise<RecoverySummary> {
    const res = await authFetch(`${API_BASE}/jobs/recovery-summary`);
    return handleResponse<RecoverySummary>(res);
  }
}

class HttpQueueOperations implements QueueOperations {
  async getSnapshot(): Promise<QueueSnapshot> {
    const res = await authFetch(`${API_BASE}/queue`);
    return handleResponse<QueueSnapshot>(res);
  }

  async reorder(priority: JobPriority, jobIds: string[]): Promise<void> {
    const res = await authFetch(`${API_BASE}/queue/reorder`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ priority, jobIds }),
    });
    await handleResponse<{ status: string }>(res);
  }
}

class HttpSettingsOperations implements SettingsOperations {
  async getSettings(): Promise<AppSettings> {
    const res = await authFetch(`${API_BASE}/settings`);
    return handleResponse<AppSettings>(res);
  }

  async updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings> {
    const res = await authFetch(`${API_BASE}/settings`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    return handleResponse<AppSettings>(res);
  }
}

class HttpCategoriesOperations implements CategoriesOperations {
  async getCategories(): Promise<Category[]> {
    const res = await authFetch(`${API_BASE}/categories`);
    return handleResponse<Category[]>(res);
  }

  async createCategory(payload: CreateCategoryPayload): Promise<Category> {
    const res = await authFetch(`${API_BASE}/categories`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    return handleResponse<Category>(res);
  }

  async updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category> {
    const res = await authFetch(`${API_BASE}/categories/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    return handleResponse<Category>(res);
  }

  async deleteCategory(id: string): Promise<void> {
    const res = await authFetch(`${API_BASE}/categories/${id}`, {
      method: 'DELETE',
    });
    await handleResponse<{ status: string }>(res);
  }
}

class HttpTrackerOperations implements TrackerOperations {
  async getSources(): Promise<TrackerSource[]> {
    return handleResponse(await authFetch(`${API_BASE}/tracker-sources`));
  }

  async createSource(
    input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>
  ): Promise<TrackerSource> {
    return handleResponse(await authFetch(`${API_BASE}/tracker-sources`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }));
  }

  async updateSource(
    id: string,
    input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }
  ): Promise<TrackerSource> {
    return handleResponse(await authFetch(`${API_BASE}/tracker-sources/${id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    }));
  }

  async deleteSource(id: string): Promise<void> {
    const response = await authFetch(`${API_BASE}/tracker-sources/${id}`, { method: 'DELETE' });
    if (!response.ok) await handleResponse(response);
  }

  async refreshSource(id: string): Promise<TrackerSource> {
    return handleResponse(await authFetch(`${API_BASE}/tracker-sources/${id}/refresh`, { method: 'POST' }));
  }

  async refreshAllSources(): Promise<{ failureCount: number }> {
    return handleResponse(await authFetch(`${API_BASE}/tracker-sources/refresh`, { method: 'POST' }));
  }
}

class HttpMediaAuthOperations implements MediaAuthOperations {
  async getSettings(): Promise<MediaAuthSettings> {
    const res = await authFetch(`${API_BASE}/media-auth`);
    return handleResponse<MediaAuthSettings>(res);
  }

  async updateSettings(payload: UpdateMediaAuthPayload): Promise<MediaAuthSettings> {
    const res = await authFetch(`${API_BASE}/media-auth`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    return handleResponse<MediaAuthSettings>(res);
  }

  async importCookies(file: File): Promise<MediaAuthSettings> {
    const formData = new FormData();
    formData.append('file', file);
    const res = await authFetch(`${API_BASE}/media-auth/cookies`, {
      method: 'POST',
      body: formData,
    });
    return handleResponse<MediaAuthSettings>(res);
  }

  async deleteCookies(): Promise<MediaAuthSettings> {
    const res = await authFetch(`${API_BASE}/media-auth/cookies`, {
      method: 'DELETE',
    });
    return handleResponse<MediaAuthSettings>(res);
  }
}

export function connectSSE(
  onEvent: (eventType: string, job: Job, seq?: number) => void,
  onSyncRequired?: (payload: { cursor: number; reason: string }) => void,
  getCursor?: () => number | undefined,
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

class HttpSyncOperations implements SyncOperations {
  async getSnapshot(): Promise<SyncSnapshot> {
    const res = await authFetch(`${API_BASE}/sync/snapshot`);
    return handleResponse<SyncSnapshot>(res);
  }

  subscribeEvents(options: EventSubscribeOptions): EventSubscription {
    const es = connectSSE(options.onEvent, options.onSyncRequired, options.getCursor);
    return new HttpEventSubscription(es, options.onConnected, options.onError);
  }
}

/** HTTP backend client implementation coordinating all domain operations via REST and SSE */
export class HttpBackendClient implements BackendClient {
  readonly jobs: JobsOperations = new HttpJobsOperations();
  readonly queue: QueueOperations = new HttpQueueOperations();
  readonly settings: SettingsOperations = new HttpSettingsOperations();
  readonly categories: CategoriesOperations = new HttpCategoriesOperations();
  readonly tracker: TrackerOperations = new HttpTrackerOperations();
  readonly mediaAuth: MediaAuthOperations = new HttpMediaAuthOperations();
  readonly sync: SyncOperations = new HttpSyncOperations();
}

/** Default singleton instance of the HTTP backend client */
export const httpBackendClient = new HttpBackendClient();
