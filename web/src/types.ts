export type JobStatus = 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'analyzing' | 'processing' | 'awaiting_selection' | 'seeding';

export type JobPriority = 'low' | 'normal' | 'high';

export type FilenameConflictPolicy = 'rename' | 'overwrite' | 'fail' | 'engine_managed';
export type ProxyMode = 'disabled' | 'system' | 'custom';
export type ProxyProtocol = 'http' | 'https' | 'socks5';
export type SeedingMode = 'none' | 'unlimited' | 'ratio' | 'duration' | 'ratio_or_duration';

export interface ProxyPolicy {
  mode: ProxyMode;
  protocol?: ProxyProtocol;
  host?: string;
  port?: number;
  username?: string;
  hasPassword?: boolean;
  secretSource?: string;
  noProxy?: string[];
}

export interface HTTPHeaderPolicy {
  name: string;
  value?: string;
  hasValue?: boolean;
  sensitive?: boolean;
  clearValue?: boolean;
}

export interface RetryPolicy {
  maxAttempts: number;
  retryWaitSeconds: number;
}

export interface TimeoutPolicy {
  connectTimeoutSeconds: number;
  requestTimeoutSeconds: number;
}

export interface DirectConnectionPolicy {
  split: number;
  maxConnectionsPerServer: number;
  minSplitSizeBytes: number;
}

export interface JobNetworkPolicy {
  downloadLimitBytesPerSecond: number;
  uploadLimitBytesPerSecond?: number;
  proxy: ProxyPolicy;
  userAgent?: string;
  httpHeaders?: HTTPHeaderPolicy[];
  retryPolicy: RetryPolicy;
  timeoutPolicy: TimeoutPolicy;
  directConnections?: DirectConnectionPolicy;
}

export interface JobNetworkPolicyOverride {
  downloadLimitBytesPerSecond?: number;
  uploadLimitBytesPerSecond?: number;
  proxy?: ProxyPolicy;
  proxyPassword?: string;
  clearProxyPassword?: boolean;
  userAgent?: string;
  httpHeaders?: HTTPHeaderPolicy[];
  retryPolicy?: RetryPolicy;
  timeoutPolicy?: TimeoutPolicy;
  directConnections?: DirectConnectionPolicy;
}

export interface SeedingPolicy {
  mode: SeedingMode;
  ratioLimit?: number;
  timeLimitSeconds?: number;
}

export interface CapabilityState {
  supported: boolean;
  mutableNow: boolean;
  startupOnly?: boolean;
  reason?: string;
  supportedProtocols?: string[];
  supportedFields?: string[];
}

export interface JobCapabilities {
  pause: CapabilityState;
  resume: CapabilityState;
  cancel: CapabilityState;
  retry: CapabilityState;
  downloadLimit: CapabilityState;
  uploadLimit: CapabilityState;
  proxy: CapabilityState;
  userAgent: CapabilityState;
  customHeaders: CapabilityState;
  retryPolicy: CapabilityState;
  timeoutPolicy: CapabilityState;
  connections: CapabilityState;
  fileSelection: CapabilityState;
  trackers: CapabilityState;
  seedingPolicy: CapabilityState;
}

export interface TrackerSource {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
  refreshIntervalSeconds: number;
  lastCheckedAt?: string;
  lastSuccessAt?: string;
  lastError?: string;
  trackerCount: number;
}

export interface Category {
  id: string;
  name: string;
  directory: string;
  resolvedDirectory?: string;
  createdAt?: string;
  updatedAt?: string;
}

export interface CreateCategoryPayload {
  name: string;
  directory: string;
}

export interface UpdateCategoryPayload {
  name: string;
  directory: string;
}

export interface MediaFormat {
  formatId: string;
  ext: string;
  resolution: string;
  fileSize: number;
  vcodec: string;
  acodec: string;
  fps: number;
  quality: string;
  note: string;
}

export interface MediaInfo {
  title: string;
  duration: number;
  thumbnail: string;
  url: string;
  formats: MediaFormat[];
  selectedFormat?: string;
}

export type TorrentFilePriority = 'skip' | 'normal' | 'high' | 'maximum';

export interface TorrentInfo {
  name: string;
  infoHash: string;
  totalSize: number;
  seeders: number;
  leechers: number;
  uploaded: number;
  uploadSpeed: number;
  ratio: number;
}

export interface TorrentFile {
  index: number;
  path: string;
  size: number;
  progress: number;
  priority: TorrentFilePriority;
  selected: boolean;
}

export interface TorrentFileSelection {
  index: number;
  priority: TorrentFilePriority;
}

export interface Job {
  id: string;
  source: string;
  name: string;
  status: JobStatus;
  type: string;
  priority?: JobPriority;
  batchId?: string;
  categoryId?: string;
  destinationDir?: string;
  workDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
  finalPath?: string;
  progress: number;
  totalBytes: number;
  completedBytes: number;
  speedBytesPerSecond: number;
  etaSeconds: number;
  error?: string;
  engine: string;
  mediaInfo?: MediaInfo;
  torrentInfo?: TorrentInfo;
  seedAfterComplete?: boolean;
  networkPolicy: JobNetworkPolicy;
  effectiveDownloadLimitBytesPerSecond: number;
  effectiveUploadLimitBytesPerSecond?: number;
  networkReconcilePending?: boolean;
  seedingPolicy?: SeedingPolicy;
  seedingStartedAt?: string;
  seedingStopReason?: string;
  customTrackers?: string[];
  createdAt: string;
  updatedAt: string;
}

export interface CreateJobRequest {
  source: string;
  priority?: JobPriority;
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
  networkPolicy?: JobNetworkPolicyOverride;
  seedingPolicy?: SeedingPolicy;
  trackers?: string[];
}

export interface BatchInput {
  source: string;
  priority?: JobPriority;
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
  networkPolicy?: JobNetworkPolicyOverride;
  seedingPolicy?: SeedingPolicy;
  trackers?: string[];
}

export interface CreateBatchRequest {
  inputs: BatchInput[];
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
  networkPolicy?: JobNetworkPolicyOverride;
  seedingPolicy?: SeedingPolicy;
  trackers?: string[];
}

export interface BatchItemResult {
  index: number;
  job?: Job;
  error?: {
    code: string;
    message: string;
  };
}

export interface CreateBatchResponse {
  batchId: string;
  created: number;
  failed: number;
  items: BatchItemResult[];
}

export interface BulkActionRequest {
  action: 'pause' | 'resume' | 'cancel' | 'retry';
  jobIds: string[];
}

export interface BulkItemResult {
  jobId: string;
  success: boolean;
  job?: Job;
  error?: {
    code: string;
    message: string;
  };
}

export interface BulkActionResponse {
  action: string;
  succeeded: number;
  failed: number;
  results: BulkItemResult[];
}

export interface QueuedJob {
  jobId: string;
  position: number;
  action: 'start' | 'resume';
  enqueuedAt: string;
  updatedAt: string;
  waitingReason?: string;
  job?: Job;
}

export interface QueueSnapshot {
  maxConcurrentDownloads: number;
  runningDownloads: number;
  queuedDownloads: number;
  pausedDownloads: number;
  items: QueuedJob[];
}

export interface StorageOverrides {
  defaultDownloadDirectory: boolean;
  temporaryDirectory: boolean;
  minimumFreeSpaceBytes: boolean;
  defaultConflictPolicy: boolean;
}

export interface StorageSettings {
  defaultDownloadDirectory: string;
  temporaryDirectory: string;
  minimumFreeSpaceBytes: number;
  defaultConflictPolicy: FilenameConflictPolicy;
  overrides?: StorageOverrides;
}

export interface AppSettings {
  queue?: {
    maxConcurrentDownloads: number;
    source?: string;
  };
  storage?: StorageSettings;
  maxConcurrentDownloads?: number;
  maxConcurrentSource?: string;
  network?: {
    globalDownloadLimitBytesPerSecond: number;
    proxy: ProxyPolicy;
    userAgent: string;
    httpHeaders: HTTPHeaderPolicy[];
    retryPolicy: RetryPolicy;
    timeoutPolicy: TimeoutPolicy;
    directConnections: DirectConnectionPolicy;
  };
  torrent?: {
    downloadLimitBytesPerSecond: number;
    uploadLimitBytesPerSecond: number;
    seedingPolicy: SeedingPolicy;
    applyTrackerSubscriptionsToNewTorrents: boolean;
    manageQBitGlobalNetworkSettings: boolean;
  };
  overrides?: Record<string, boolean>;
  applicationResults?: { target: string; status: string; code?: string; message?: string }[];
}

export interface UpdateSettingsPayload {
  queue?: {
    maxConcurrentDownloads?: number;
  };
  storage?: {
    defaultDownloadDirectory?: string;
    temporaryDirectory?: string;
    minimumFreeSpaceBytes?: number;
    defaultConflictPolicy?: FilenameConflictPolicy;
  };
  network?: {
    globalDownloadLimitBytesPerSecond?: number;
    proxy?: ProxyPolicy;
    proxyPassword?: string;
    clearProxyPassword?: boolean;
    userAgent?: string;
    httpHeaders?: HTTPHeaderPolicy[];
    retryPolicy?: RetryPolicy;
    timeoutPolicy?: TimeoutPolicy;
    directConnections?: DirectConnectionPolicy;
  };
  torrent?: {
    downloadLimitBytesPerSecond?: number;
    uploadLimitBytesPerSecond?: number;
    seedingPolicy?: SeedingPolicy;
    applyTrackerSubscriptionsToNewTorrents?: boolean;
    manageQBitGlobalNetworkSettings?: boolean;
  };
}

export interface SelectFormatRequest {
  formatId: string;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
}
