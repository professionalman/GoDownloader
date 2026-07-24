export type JobStatus = 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'analyzing' | 'processing';

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

export interface Job {
  id: string;
  source: string;
  name: string;
  status: JobStatus;
  type: string;
  progress: number;
  totalBytes: number;
  completedBytes: number;
  speedBytesPerSecond: number;
  etaSeconds: number;
  error?: string;
  engine: string;
  mediaInfo?: MediaInfo;
  createdAt: string;
  updatedAt: string;
}

export interface CreateJobRequest {
  source: string;
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
