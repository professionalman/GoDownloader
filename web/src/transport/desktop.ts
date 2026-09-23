import { Events } from '@wailsio/runtime';
import * as DesktopService from '../bindings/downloader/cmd/desktop/desktopservice';
import type {
  Job,
  JobPriority,
  CreateBatchResponse,
  BulkActionResponse,
  QueueSnapshot,
  AppSettings,
  UpdateSettingsPayload,
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
  TorrentFile,
  TorrentFileSelection,
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
  type DesktopPreferences,
  type DesktopPreferencesOperations,
} from './types';

/** Error translation converting Go [ERROR_CODE] error prefixes into typed ApiResponseError */
export function wrapIpcError(err: unknown): never {
  const rawMsg = err instanceof Error ? err.message : String(err);
  const match = rawMsg.match(/^\[([A-Z0-9_]+)\]\s*(.*)$/);
  if (match) {
    throw new ApiResponseError(match[1], match[2]);
  }
  throw new ApiResponseError('INTERNAL_ERROR', rawMsg);
}

/** Helper converting browser File to Base64 data string */
async function fileToBase64(file: File): Promise<string> {
  const buffer = await file.arrayBuffer();
  let binary = '';
  const bytes = new Uint8Array(buffer);
  const len = bytes.byteLength;
  for (let i = 0; i < len; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary);
}

/** In-process desktop event subscription wrapping Wails Events.On with deterministic StateSync handshake */
export class DesktopEventSubscription implements EventSubscription {
  private unlisten: (() => void) | null = null;
  private isClosed = false;
  private handshakeComplete = false;
  private pendingLiveEvents: Array<{ type: string; job?: Job; data?: any; sequence?: number }> = [];
  private lastProcessedSeq = 0;
  readonly ready: Promise<void>;

  constructor(options: EventSubscribeOptions) {
    let resolveReady: () => void = () => {};
    this.ready = new Promise<void>((resolve) => {
      resolveReady = resolve;
    });

    try {
      this.unlisten = Events.On('godownloader.event', (wailsEv: any) => {
        if (this.isClosed) return;
        try {
          const payload = wailsEv?.data;
          if (!payload) return;

          // If handshake is still in progress, queue live events
          if (!this.handshakeComplete) {
            this.pendingLiveEvents.push(payload);
            return;
          }

          this.processEvent(payload, options);
        } catch (err) {
          options.onError?.(err);
        }
      });

      if (options.onConnected) {
        options.onConnected();
      }

      // Perform deterministic StateSync catchup handshake if cursor is supplied
      const initialCursor = options.getCursor ? options.getCursor() : undefined;
      if (typeof initialCursor === 'number' && initialCursor > 0) {
        this.lastProcessedSeq = initialCursor;
        DesktopService.GetEventsAfter(initialCursor)
          .then((res: any) => {
            if (this.isClosed) return;
            if (res?.gapDetected) {
              options.onSyncRequired?.({ cursor: initialCursor, reason: 'event_gap' });
              this.pendingLiveEvents = [];
              return;
            }

            if (Array.isArray(res?.events)) {
              for (const replayed of res.events) {
                if (this.isClosed) return;
                const seq = replayed.sequence;
                if (typeof seq === 'number' && seq > 0) {
                  this.lastProcessedSeq = Math.max(this.lastProcessedSeq, seq);
                }
                options.onEvent(replayed.type, (replayed.job || {}) as Job, seq);
              }
            }

            // Drain queued live events
            this.handshakeComplete = true;
            for (const queued of this.pendingLiveEvents) {
              if (this.isClosed) return;
              this.processEvent(queued, options);
            }
            this.pendingLiveEvents = [];
          })
          .catch((err) => {
            if (this.isClosed) return;
            options.onError?.(err);
          })
          .finally(() => {
            this.handshakeComplete = true;
            resolveReady();
            DesktopService.RecordStateSyncCompleted?.(this.lastProcessedSeq)?.catch?.(() => {});
          });
      } else {
        this.handshakeComplete = true;
        resolveReady();
        DesktopService.RecordStateSyncCompleted?.(0)?.catch?.(() => {});
      }
    } catch (err) {
      if (options.onError) {
        options.onError(err);
      }
      this.handshakeComplete = true;
      resolveReady();
    }
  }

  private processEvent(
    payload: { type: string; job?: Job; data?: any; sequence?: number },
    options: EventSubscribeOptions
  ) {
    if (payload.type === 'sync.required') {
      options.onSyncRequired?.(payload.data || { cursor: 0, reason: 'event_gap' });
      return;
    }

    if (payload.type === 'diagnostic.ping') {
      const token = typeof payload.data === 'string' ? payload.data : '';
      DesktopService.RecordDiagnosticDelivery?.(token)?.catch?.(() => {});
    }

    const seq = payload.sequence;
    if (typeof seq === 'number' && seq > 0) {
      // Monotonic sequence event: deduplicate if already applied
      if (this.lastProcessedSeq > 0) {
        if (seq <= this.lastProcessedSeq) {
          return; // already processed
        }
        if (seq > this.lastProcessedSeq + 1) {
          // Gap detected in live stream
          options.onSyncRequired?.({ cursor: this.lastProcessedSeq, reason: 'event_gap' });
          return;
        }
      }
      this.lastProcessedSeq = seq;
    }

    // Ephemeral events (seq === 0 or undefined) pass through unconditionally
    options.onEvent(payload.type, (payload.job || {}) as Job, seq);
  }

  close(): void {
    this.isClosed = true;
    if (this.unlisten) {
      this.unlisten();
      this.unlisten = null;
    }
    this.pendingLiveEvents = [];
  }
}

class DesktopJobsOperations implements JobsOperations {
  async getJobs(): Promise<Job[]> {
    const res = await DesktopService.GetJobs().catch(wrapIpcError);
    return (res || []) as unknown as Job[];
  }

  async getJob(id: string): Promise<Job> {
    const res = await DesktopService.GetJob(id).catch(wrapIpcError);
    if (!res) throw new ApiResponseError('JOB_NOT_FOUND', 'job not found');
    return res as unknown as Job;
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
    const res = await DesktopService.CreateJob(
      source,
      priority,
      categoryId || '',
      destinationDir || '',
      conflictPolicy || '',
      networkPolicy ? JSON.stringify(networkPolicy) : '',
      seedingPolicy ? JSON.stringify(seedingPolicy) : '',
      trackers || null
    ).catch(wrapIpcError);
    return res as unknown as Job;
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
    const res = await DesktopService.CreateBatchJobs(
      JSON.stringify({ inputs })
    ).catch(wrapIpcError);
    return res as unknown as CreateBatchResponse;
  }

  async pauseJob(id: string): Promise<Job> {
    const res = await DesktopService.PauseJob(id).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async resumeJob(id: string): Promise<Job> {
    const res = await DesktopService.ResumeJob(id).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async retryJob(id: string): Promise<Job> {
    const res = await DesktopService.RetryJob(id).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async cancelJob(id: string): Promise<Job> {
    const res = await DesktopService.CancelJob(id).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async deleteJob(id: string, deleteFiles: boolean): Promise<void> {
    await DesktopService.DeleteJob(id, deleteFiles).catch(wrapIpcError);
  }

  async bulkAction(
    action: 'pause' | 'resume' | 'cancel' | 'retry',
    jobIds: string[]
  ): Promise<BulkActionResponse> {
    const res = await DesktopService.BulkAction(action, jobIds).catch(wrapIpcError);
    return res as unknown as BulkActionResponse;
  }

  async setJobPriority(jobId: string, priority: JobPriority): Promise<Job> {
    const res = await DesktopService.SetJobPriority(jobId, priority).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async selectFormat(
    jobId: string,
    formatId: string,
    subtitleOptions?: SubtitleOptions
  ): Promise<Job> {
    const res = await DesktopService.SelectFormat(
      jobId,
      formatId,
      subtitleOptions ? JSON.stringify(subtitleOptions) : ''
    ).catch(wrapIpcError);
    return res as unknown as Job;
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
    const base64Data = await fileToBase64(file);
    const res = await DesktopService.UploadTorrent(
      file.name,
      base64Data,
      priority || 'normal',
      categoryId || '',
      destinationDir || '',
      networkPolicy ? JSON.stringify(networkPolicy) : '',
      seedingPolicy ? JSON.stringify(seedingPolicy) : '',
      trackers || null
    ).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async getTorrentFiles(jobId: string): Promise<TorrentFile[]> {
    const res = await DesktopService.GetTorrentFiles(jobId).catch(wrapIpcError);
    return (res || []) as unknown as TorrentFile[];
  }

  async startTorrent(
    jobId: string,
    files: TorrentFileSelection[],
    seedingPolicy: SeedingPolicy
  ): Promise<Job> {
    const res = await DesktopService.StartTorrent(
      jobId,
      JSON.stringify(files),
      JSON.stringify(seedingPolicy)
    ).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async stopSeeding(jobId: string): Promise<Job> {
    const res = await DesktopService.StopSeeding(jobId).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }> {
    const res = await DesktopService.GetCapabilities().catch(wrapIpcError);
    return res as unknown as { profiles: Record<string, JobCapabilities> };
  }

  async resolveCapabilities(source: string | string[]): Promise<JobCapabilities> {
    const res = await DesktopService.ResolveCapabilities(JSON.stringify(source)).catch(wrapIpcError);
    return res as unknown as JobCapabilities;
  }

  async getJobCapabilities(jobId: string): Promise<JobCapabilities> {
    const res = await DesktopService.GetJobCapabilities(jobId).catch(wrapIpcError);
    return res as unknown as JobCapabilities;
  }

  async updateJobNetwork(
    jobId: string,
    limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }
  ): Promise<Job> {
    const res = await DesktopService.UpdateJobNetwork(jobId, JSON.stringify(limits)).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async addTorrentTrackers(
    jobId: string,
    trackers: string[]
  ): Promise<{ trackers: { url: string }[] }> {
    const res = await DesktopService.AddTorrentTrackers(jobId, trackers).catch(wrapIpcError);
    return res as unknown as { trackers: { url: string }[] };
  }

  async updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job> {
    const res = await DesktopService.UpdateSeedingPolicy(jobId, JSON.stringify(policy)).catch(wrapIpcError);
    return res as unknown as Job;
  }

  async openFolder(): Promise<void> {
    await DesktopService.OpenFolder().catch(wrapIpcError);
  }

  async getRecoverySummary(): Promise<RecoverySummary> {
    const res = await DesktopService.GetRecoverySummary().catch(wrapIpcError);
    return (res || {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    }) as unknown as RecoverySummary;
  }
}

class DesktopQueueOperations implements QueueOperations {
  async getSnapshot(): Promise<QueueSnapshot> {
    const res = await DesktopService.GetQueueSnapshot().catch(wrapIpcError);
    return res as unknown as QueueSnapshot;
  }

  async reorder(priority: JobPriority, jobIds: string[]): Promise<void> {
    await DesktopService.ReorderQueue(priority, jobIds).catch(wrapIpcError);
  }
}

class DesktopSettingsOperations implements SettingsOperations {
  async getSettings(): Promise<AppSettings> {
    const res = await DesktopService.GetSettings().catch(wrapIpcError);
    return res as unknown as AppSettings;
  }

  async updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings> {
    const res = await DesktopService.UpdateSettings(JSON.stringify(payload)).catch(wrapIpcError);
    return res as unknown as AppSettings;
  }
}

class DesktopCategoriesOperations implements CategoriesOperations {
  async getCategories(): Promise<Category[]> {
    const res = await DesktopService.GetCategories().catch(wrapIpcError);
    return (res || []) as unknown as Category[];
  }

  async createCategory(payload: CreateCategoryPayload): Promise<Category> {
    const res = await DesktopService.CreateCategory(payload.name, payload.directory).catch(wrapIpcError);
    return res as unknown as Category;
  }

  async updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category> {
    const res = await DesktopService.UpdateCategory(id, payload.name, payload.directory).catch(wrapIpcError);
    return res as unknown as Category;
  }

  async deleteCategory(id: string): Promise<void> {
    await DesktopService.DeleteCategory(id).catch(wrapIpcError);
  }
}

class DesktopTrackerOperations implements TrackerOperations {
  async getSources(): Promise<TrackerSource[]> {
    const res = await DesktopService.GetTrackerSources().catch(wrapIpcError);
    return (res || []) as unknown as TrackerSource[];
  }

  async createSource(
    input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>
  ): Promise<TrackerSource> {
    const res = await DesktopService.CreateTrackerSource(
      input.name,
      input.url,
      input.enabled,
      input.refreshIntervalSeconds
    ).catch(wrapIpcError);
    return res as unknown as TrackerSource;
  }

  async updateSource(
    id: string,
    input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }
  ): Promise<TrackerSource> {
    const res = await DesktopService.UpdateTrackerSource(
      id,
      input.name,
      input.url,
      input.enabled,
      input.refreshIntervalSeconds
    ).catch(wrapIpcError);
    return res as unknown as TrackerSource;
  }

  async deleteSource(id: string): Promise<void> {
    await DesktopService.DeleteTrackerSource(id).catch(wrapIpcError);
  }

  async refreshSource(id: string): Promise<TrackerSource> {
    const res = await DesktopService.RefreshTrackerSource(id).catch(wrapIpcError);
    return res as unknown as TrackerSource;
  }

  async refreshAllSources(): Promise<{ failureCount: number }> {
    const res = await DesktopService.RefreshAllTrackerSources().catch(wrapIpcError);
    return res as unknown as { failureCount: number };
  }
}

class DesktopMediaAuthOperations implements MediaAuthOperations {
  async getSettings(): Promise<MediaAuthSettings> {
    const res = await DesktopService.GetMediaAuthSettings().catch(wrapIpcError);
    return res as unknown as MediaAuthSettings;
  }

  async updateSettings(payload: UpdateMediaAuthPayload): Promise<MediaAuthSettings> {
    const res = await DesktopService.UpdateMediaAuthSettings(JSON.stringify(payload)).catch(wrapIpcError);
    return res as unknown as MediaAuthSettings;
  }

  async importCookies(file: File): Promise<MediaAuthSettings> {
    const content = await file.text();
    const res = await DesktopService.ImportMediaCookies(content).catch(wrapIpcError);
    return res as unknown as MediaAuthSettings;
  }

  async deleteCookies(): Promise<MediaAuthSettings> {
    const res = await DesktopService.DeleteMediaCookies().catch(wrapIpcError);
    return res as unknown as MediaAuthSettings;
  }
}

class DesktopSyncOperations implements SyncOperations {
  async getSnapshot(): Promise<SyncSnapshot> {
    const res = await DesktopService.GetSyncSnapshot().catch(wrapIpcError);
    return res as unknown as SyncSnapshot;
  }

  subscribeEvents(options: EventSubscribeOptions): EventSubscription {
    return new DesktopEventSubscription(options);
  }
}

class DesktopPreferencesOperationsImpl implements DesktopPreferencesOperations {
  async getPreferences(): Promise<DesktopPreferences> {
    const res = await DesktopService.GetDesktopPreferences().catch(wrapIpcError);
    return res as unknown as DesktopPreferences;
  }

  async setCloseToTray(enabled: boolean): Promise<DesktopPreferences> {
    const res = await DesktopService.SetCloseToTray(enabled).catch(wrapIpcError);
    return res as unknown as DesktopPreferences;
  }

  async setAutostart(enabled: boolean): Promise<DesktopPreferences> {
    const res = await DesktopService.SetAutostart(enabled).catch(wrapIpcError);
    return res as unknown as DesktopPreferences;
  }
}

/** DesktopBackendClient coordinates all domains via native Wails v3 IPC and Events */
export class DesktopBackendClient implements BackendClient {
  readonly jobs: JobsOperations = new DesktopJobsOperations();
  readonly queue: QueueOperations = new DesktopQueueOperations();
  readonly settings: SettingsOperations = new DesktopSettingsOperations();
  readonly categories: CategoriesOperations = new DesktopCategoriesOperations();
  readonly tracker: TrackerOperations = new DesktopTrackerOperations();
  readonly mediaAuth: MediaAuthOperations = new DesktopMediaAuthOperations();
  readonly sync: SyncOperations = new DesktopSyncOperations();
  readonly desktopPreferences: DesktopPreferencesOperations = new DesktopPreferencesOperationsImpl();
}

/** Singleton instance of the desktop backend client */
export const desktopBackendClient = new DesktopBackendClient();

