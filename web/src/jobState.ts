import type { Job } from './types';

function jobTimestamp(job: Job): number | null {
  const timestamp = Date.parse(job.updatedAt);
  return Number.isNaN(timestamp) ? null : timestamp;
}

function newerJob(current: Job, incoming: Job): Job {
  const currentTimestamp = jobTimestamp(current);
  const incomingTimestamp = jobTimestamp(incoming);

  if (currentTimestamp !== null && incomingTimestamp !== null && incomingTimestamp < currentTimestamp) {
    return current;
  }

  return incoming;
}

export function deduplicateJobsById(jobs: readonly Job[]): Job[] {
  const deduplicated: Job[] = [];
  const indexes = new Map<string, number>();

  for (const job of jobs) {
    const existingIndex = indexes.get(job.id);
    if (existingIndex === undefined) {
      indexes.set(job.id, deduplicated.length);
      deduplicated.push(job);
      continue;
    }

    deduplicated[existingIndex] = newerJob(deduplicated[existingIndex], job);
  }

  return deduplicated;
}

export function upsertJobs(currentJobs: readonly Job[], incomingJobs: readonly Job[]): Job[] {
  const current = deduplicateJobsById(currentJobs);
  const incoming = deduplicateJobsById(incomingJobs);
  const indexes = new Map(current.map((job, index) => [job.id, index]));
  const newJobs: Job[] = [];

  for (const job of incoming) {
    const existingIndex = indexes.get(job.id);
    if (existingIndex === undefined) {
      newJobs.push(job);
      continue;
    }

    current[existingIndex] = newerJob(current[existingIndex], job);
  }

  return [...newJobs, ...current];
}

export function upsertJob(currentJobs: readonly Job[], incomingJob: Job): Job[] {
  return upsertJobs(currentJobs, [incomingJob]);
}

export function replaceJobsFromInitialLoad(currentJobs: readonly Job[], loadedJobs: readonly Job[]): Job[] {
  return upsertJobs(deduplicateJobsById(loadedJobs), currentJobs);
}