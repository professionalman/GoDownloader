import type {
  Job,
  JobPriority,
  CreateBatchResponse,
  BulkActionResponse,
  QueueSnapshot,
  AppSettings,
  UpdateSettingsPayload,
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
  SyncSnapshot,
} from './types';
import {
  ApiResponseError,
  getBackendClient,
  setBackendClient,
  resetBackendClient,
  type BackendClient,
  type EventSubscription,
  type EventSubscribeOptions,
} from './transport';

export {
  ApiResponseError,
  getBackendClient,
  setBackendClient,
  resetBackendClient,
};
export type { BackendClient, EventSubscription, EventSubscribeOptions };

// ----------------------------------------------------------------------------
// Jobs Domain Facade
// ----------------------------------------------------------------------------

export async function createJob(
  source: string,
  priority: JobPriority = 'normal',
  categoryId?: string,
  destinationDir?: string,
  conflictPolicy?: FilenameConflictPolicy,
  networkPolicy?: JobNetworkPolicyOverride,
  seedingPolicy?: SeedingPolicy,
  trackers?: string[]
): Promise<Job> {
  return getBackendClient().jobs.createJob(
    source,
    priority,
    categoryId,
    destinationDir,
    conflictPolicy,
    networkPolicy,
    seedingPolicy,
    trackers
  );
}

export async function createBatchJobs(
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
  return getBackendClient().jobs.createBatchJobs(inputs);
}

export async function bulkAction(
  action: 'pause' | 'resume' | 'cancel' | 'retry',
  jobIds: string[]
): Promise<BulkActionResponse> {
  return getBackendClient().jobs.bulkAction(action, jobIds);
}

export async function setJobPriority(jobId: string, priority: JobPriority): Promise<Job> {
  return getBackendClient().jobs.setJobPriority(jobId, priority);
}

export async function getJobs(): Promise<Job[]> {
  return getBackendClient().jobs.getJobs();
}

export async function getJob(id: string): Promise<Job> {
  return getBackendClient().jobs.getJob(id);
}

export async function pauseJob(id: string): Promise<Job> {
  return getBackendClient().jobs.pauseJob(id);
}

export async function resumeJob(id: string): Promise<Job> {
  return getBackendClient().jobs.resumeJob(id);
}

export async function retryJob(id: string): Promise<Job> {
  return getBackendClient().jobs.retryJob(id);
}

export async function cancelJob(id: string): Promise<Job> {
  return getBackendClient().jobs.cancelJob(id);
}

export async function deleteJob(id: string, deleteFiles: boolean): Promise<void> {
  return getBackendClient().jobs.deleteJob(id, deleteFiles);
}

export async function selectFormat(
  jobId: string,
  formatId: string,
  subtitleOptions?: SubtitleOptions
): Promise<Job> {
  return getBackendClient().jobs.selectFormat(jobId, formatId, subtitleOptions);
}

export async function openFolder(): Promise<void> {
  return getBackendClient().jobs.openFolder();
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
  return getBackendClient().jobs.uploadTorrent(
    file,
    priority,
    categoryId,
    destinationDir,
    networkPolicy,
    seedingPolicy,
    trackers
  );
}

export async function getTorrentFiles(jobId: string): Promise<TorrentFile[]> {
  return getBackendClient().jobs.getTorrentFiles(jobId);
}

export async function startTorrent(
  jobId: string,
  files: TorrentFileSelection[],
  seedingPolicy: SeedingPolicy
): Promise<Job> {
  return getBackendClient().jobs.startTorrent(jobId, files, seedingPolicy);
}

export async function stopSeeding(jobId: string): Promise<Job> {
  return getBackendClient().jobs.stopSeeding(jobId);
}

export async function getCapabilities(): Promise<{ profiles: Record<string, JobCapabilities> }> {
  return getBackendClient().jobs.getCapabilities();
}

export async function resolveCapabilities(source: string | string[]): Promise<JobCapabilities> {
  return getBackendClient().jobs.resolveCapabilities(source);
}

export async function getJobCapabilities(jobId: string): Promise<JobCapabilities> {
  return getBackendClient().jobs.getJobCapabilities(jobId);
}

export async function updateJobNetwork(
  jobId: string,
  limits: { downloadLimitBytesPerSecond?: number; uploadLimitBytesPerSecond?: number }
): Promise<Job> {
  return getBackendClient().jobs.updateJobNetwork(jobId, limits);
}

export async function addTorrentTrackers(
  jobId: string,
  trackers: string[]
): Promise<{ trackers: { url: string }[] }> {
  return getBackendClient().jobs.addTorrentTrackers(jobId, trackers);
}

export async function updateSeedingPolicy(jobId: string, policy: SeedingPolicy): Promise<Job> {
  return getBackendClient().jobs.updateSeedingPolicy(jobId, policy);
}

// ----------------------------------------------------------------------------
// Queue Domain Facade
// ----------------------------------------------------------------------------

export async function getQueueSnapshot(): Promise<QueueSnapshot> {
  return getBackendClient().queue.getSnapshot();
}

export async function reorderQueue(priority: JobPriority, jobIds: string[]): Promise<void> {
  return getBackendClient().queue.reorder(priority, jobIds);
}

// ----------------------------------------------------------------------------
// Settings Domain Facade
// ----------------------------------------------------------------------------

export async function getSettings(): Promise<AppSettings> {
  return getBackendClient().settings.getSettings();
}

export async function updateSettings(payload: UpdateSettingsPayload): Promise<AppSettings> {
  return getBackendClient().settings.updateSettings(payload);
}

// ----------------------------------------------------------------------------
// Categories Domain Facade
// ----------------------------------------------------------------------------

export async function getCategories(): Promise<Category[]> {
  return getBackendClient().categories.getCategories();
}

export async function createCategory(payload: CreateCategoryPayload): Promise<Category> {
  return getBackendClient().categories.createCategory(payload);
}

export async function updateCategory(id: string, payload: UpdateCategoryPayload): Promise<Category> {
  return getBackendClient().categories.updateCategory(id, payload);
}

export async function deleteCategory(id: string): Promise<void> {
  return getBackendClient().categories.deleteCategory(id);
}

// ----------------------------------------------------------------------------
// Trackers Domain Facade
// ----------------------------------------------------------------------------

export async function getTrackerSources(): Promise<TrackerSource[]> {
  return getBackendClient().tracker.getSources();
}

export async function createTrackerSource(
  input: Omit<TrackerSource, 'id' | 'trackerCount' | 'lastCheckedAt' | 'lastSuccessAt' | 'lastError'>
): Promise<TrackerSource> {
  return getBackendClient().tracker.createSource(input);
}

export async function updateTrackerSource(
  id: string,
  input: { name: string; url: string; enabled: boolean; refreshIntervalSeconds: number }
): Promise<TrackerSource> {
  return getBackendClient().tracker.updateSource(id, input);
}

export async function deleteTrackerSource(id: string): Promise<void> {
  return getBackendClient().tracker.deleteSource(id);
}

export async function refreshTrackerSource(id: string): Promise<TrackerSource> {
  return getBackendClient().tracker.refreshSource(id);
}

export async function refreshAllTrackerSources(): Promise<{ failureCount: number }> {
  return getBackendClient().tracker.refreshAllSources();
}

// ----------------------------------------------------------------------------
// Media Auth Domain Facade
// ----------------------------------------------------------------------------

export async function getMediaAuth(): Promise<MediaAuthSettings> {
  return getBackendClient().mediaAuth.getSettings();
}

export async function updateMediaAuth(payload: UpdateMediaAuthPayload): Promise<MediaAuthSettings> {
  return getBackendClient().mediaAuth.updateSettings(payload);
}

export async function importMediaCookies(file: File): Promise<MediaAuthSettings> {
  return getBackendClient().mediaAuth.importCookies(file);
}

export async function deleteMediaCookies(): Promise<MediaAuthSettings> {
  return getBackendClient().mediaAuth.deleteCookies();
}

// ----------------------------------------------------------------------------
// StateSync & Events Domain Facade
// ----------------------------------------------------------------------------

export async function getSyncSnapshot(): Promise<SyncSnapshot> {
  return getBackendClient().sync.getSnapshot();
}

/**
 * Transport-neutral event subscription hook.
 */
export function subscribeEvents(options: EventSubscribeOptions): EventSubscription {
  return getBackendClient().sync.subscribeEvents(options);
}
