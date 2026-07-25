export type JobStatus = 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'analyzing' | 'processing' | 'awaiting_selection' | 'seeding';

export type JobPriority = 'low' | 'normal' | 'high';

export type FilenameConflictPolicy = 'rename' | 'overwrite' | 'fail' | 'engine_managed';

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
  createdAt: string;
  updatedAt: string;
}

export interface CreateJobRequest {
  source: string;
  priority?: JobPriority;
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
}

export interface BatchInput {
  source: string;
  priority?: JobPriority;
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
}

export interface CreateBatchRequest {
  inputs: BatchInput[];
  categoryId?: string;
  destinationDir?: string;
  conflictPolicy?: FilenameConflictPolicy;
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
