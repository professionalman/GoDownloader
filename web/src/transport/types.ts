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

/** Error class that carries the backend error code alongside the message. */
export class ApiResponseError extends Error {
  public readonly code: string;
  constructor(code: string, message: string) {
    super(message);
    this.name = 'ApiResponseError';
    this.code = code;
  }
}

/** Subscription handle returned by subscribeEvents, supporting clean disposal. */
export interface EventSubscription {
  close(): void;
}

/** Options for subscribing to application domain events. */
export interface EventSubscribeOptions {
  onEvent: (eventType: string, job: Job, seq?: number) => void;
  onSyncRequired?: (payload: { cursor: number; reason: string }) => void;
  getCursor?: () => number | undefined;
  onConnected?: () => void;
  onError?: (err?: unknown) => void;
}

/** Domain: Jobs and transfers */
export interface JobsOperations {
  getJobs(): Promise<Job[]>;
  getJob(id: string): Promise<Job>;
  createJob(
    source: string,
    priority?: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: FilenameConflictPolicy,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ): Promise<Job>;
  createBatchJobs(
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
  ): Promise<CreateBatchResponse>;
  pauseJob(id: string): Promise<Job>;
  resumeJob(id: string): Promise<Job>;
  retryJob(id: string): Promise<Job>;
  cancelJob(id: string): Promise<Job>;
  deleteJob(id: string, deleteFiles: boolean): Promise<void>;
  bulkAction(
    action: 'pause' | 'resume' | 'cancel' | 'retry',
    jobIds: string[]
  ): Promise<BulkActionResponse>;
  setJobPriority(jobId: string, priority: JobPriority): Promise<Job>;
  selectFormat(
    jobId: string,
    formatId: string,
    subtitleOptions?: SubtitleOptions
  ): Promise<Job>;
  uploadTorrent(
    file: File,
    priority?: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ): Promise<Job>;
  getTorrentFiles(jobId: string): Promise<TorrentFile[]>;
  startTorrent(
    jobId: string,
    files: TorrentFileSelection[],
    seedingPolicy: SeedingPolicy
  ): Promise<Job>;
  stopSeeding(jobId: string): Promise<Job>;
  getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }>;
  resolveCapabilities(source: string | string[]): Promise<JobCapabilities>;
  getJobCapabilities(jobId: string): Promise<JobCapabilities>;
  updateJobNetwork(
    jobId: string,
    limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }
  ): Promise<Job>;
  addTorrentTrackers(
    jobId: string,
    trackers: string[]
  ): Promise<{ trackers: { url: string }[] }>;
  updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job>;
  openFolder(): Promise<void>;
  getRecoverySummary(): Promise<RecoverySummary>;
}

/** Domain: Queue management and scheduling */
export interface QueueOperations {
  getSnapshot(): Promise<QueueSnapshot>;
  reorder(priority: JobPriority, jobIds: string[]): Promise<void>;
}

/** Domain: Application settings */
export interface SettingsOperations {
  getSettings(): Promise<AppSettings>;
  updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings>;
}

/** Domain: Categories */
export interface CategoriesOperations {
  getCategories(): Promise<Category[]>;
  createCategory(payload: CreateCategoryPayload): Promise<Category>;
  updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category>;
  deleteCategory(id: string): Promise<void>;
}

/** Domain: Torrent tracker sources */
export interface TrackerOperations {
  getSources(): Promise<TrackerSource[]>;
  createSource(
    input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>
  ): Promise<TrackerSource>;
  updateSource(
    id: string,
    input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }
  ): Promise<TrackerSource>;
  deleteSource(id: string): Promise<void>;
  refreshSource(id: string): Promise<TrackerSource>;
  refreshAllSources(): Promise<{ failureCount: number }>;
}

/** Domain: Media authentication & cookies */
export interface MediaAuthOperations {
  getSettings(): Promise<MediaAuthSettings>;
  updateSettings(payload: UpdateMediaAuthPayload): Promise<MediaAuthSettings>;
  importCookies(file: File): Promise<MediaAuthSettings>;
  deleteCookies(): Promise<MediaAuthSettings>;
}

/** Domain: State synchronization & live event subscription */
export interface SyncOperations {
  getSnapshot(): Promise<SyncSnapshot>;
  subscribeEvents(options: EventSubscribeOptions): EventSubscription;
}

/** User-configurable desktop lifecycle preferences */
export interface DesktopPreferences {
  closeToTray: boolean;
  autostartEnabled: boolean;
}

/** Domain: Desktop OS and lifecycle preferences */
export interface DesktopPreferencesOperations {
  getPreferences(): Promise<DesktopPreferences>;
  setCloseToTray(enabled: boolean): Promise<DesktopPreferences>;
  setAutostart(enabled: boolean): Promise<DesktopPreferences>;
}

/** Complete transport-neutral client interface */
export interface BackendClient {
  readonly jobs: JobsOperations;
  readonly queue: QueueOperations;
  readonly settings: SettingsOperations;
  readonly categories: CategoriesOperations;
  readonly tracker: TrackerOperations;
  readonly mediaAuth: MediaAuthOperations;
  readonly sync: SyncOperations;
  readonly desktopPreferences?: DesktopPreferencesOperations;
}

