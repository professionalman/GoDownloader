export type JobStatus = 'queued' | 'downloading' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'analyzing' | 'processing' | 'awaiting_selection' | 'seeding';

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
