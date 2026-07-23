export type JobStatus = 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'cancelled';

export interface Job {
  id: string;
  source: string;
  name: string;
  status: JobStatus;
  progress: number;
  totalBytes: number;
  completedBytes: number;
  speedBytesPerSecond: number;
  etaSeconds: number;
  error?: string;
  engine: string;
  createdAt: string;
  updatedAt: string;
}

export interface CreateJobRequest {
  source: string;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
}
