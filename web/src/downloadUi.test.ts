import { describe, it, expect } from 'vitest';
import type { Job, MediaInfo } from './types';
import {
  ACTIVE_JOB_STATUSES,
  jobStatusLabel,
  sourceDomain,
  jobMatchesQuery,
  jobMatchesFilter,
  formatBytes,
  formatSpeed,
  formatEta,
  formatDuration,
  getMediaSizeEstimate,
} from './downloadUi';

describe('downloadUi helpers', () => {
  it('identifies active job statuses correctly', () => {
    expect(ACTIVE_JOB_STATUSES).toContain('downloading');
    expect(ACTIVE_JOB_STATUSES).toContain('analyzing');
    expect(ACTIVE_JOB_STATUSES).toContain('queued');
    expect(ACTIVE_JOB_STATUSES).not.toContain('completed');
    expect(ACTIVE_JOB_STATUSES).not.toContain('failed');
    expect(ACTIVE_JOB_STATUSES).not.toContain('cancelled');
  });

  it('formats status labels cleanly', () => {
    const jobMedia: Partial<Job> = { status: 'awaiting_selection', type: 'media' };
    const jobTorrent: Partial<Job> = { status: 'awaiting_selection', type: 'torrent' };
    const jobDownloading: Partial<Job> = { status: 'downloading', type: 'download' };

    expect(jobStatusLabel(jobMedia as Job)).toBe('Awaiting format');
    expect(jobStatusLabel(jobTorrent as Job)).toBe('Awaiting file selection');
    expect(jobStatusLabel(jobDownloading as Job)).toBe('Downloading');
  });

  it('extracts source domain or falls back to label', () => {
    expect(sourceDomain('https://www.youtube.com/watch?v=123')).toBe('www.youtube.com');
    expect(sourceDomain('magnet:?xt=urn:btih:xyz')).toBe('magnet');
    expect(sourceDomain('ubuntu.torrent')).toBe('torrent file');
  });

  it('matches jobs against query string', () => {
    const job: Partial<Job> = {
      name: 'Linux ISO',
      source: 'https://releases.ubuntu.com/24.04/ubuntu.iso',
      destinationDir: 'C:\\Downloads',
      engine: 'aria2',
      type: 'download',
    };

    expect(jobMatchesQuery(job as Job, 'ubuntu')).toBe(true);
    expect(jobMatchesQuery(job as Job, 'Linux')).toBe(true);
    expect(jobMatchesQuery(job as Job, 'nonexistent')).toBe(false);
  });

  it('matches jobs against filter options', () => {
    const activeJob: Partial<Job> = { status: 'downloading' };
    const completedJob: Partial<Job> = { status: 'completed' };

    expect(jobMatchesFilter(activeJob as Job, 'Active')).toBe(true);
    expect(jobMatchesFilter(completedJob as Job, 'Active')).toBe(false);
    expect(jobMatchesFilter(completedJob as Job, 'Completed')).toBe(true);
    expect(jobMatchesFilter(activeJob as Job, 'All')).toBe(true);
  });

  it('formats bytes, speed, ETA, and duration', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(1024)).toBe('1.0 KiB');
    expect(formatBytes(50 * 1024 * 1024)).toBe('50.0 MiB');

    expect(formatSpeed(0)).toBe('—');
    expect(formatSpeed(1500000)).toBe('1.5 MB/s');

    expect(formatEta(0)).toBe('—');
    expect(formatEta(65)).toBe('1m 05s');

    expect(formatDuration(3700)).toBe('1h 1m');
  });

  it('calculates estimated combined media size for video-only selections', () => {
    const mediaInfo: MediaInfo = {
      title: 'Test Video',
      duration: 120,
      thumbnail: '',
      url: 'https://youtube.com/watch?v=test',
      selectedFormat: '137',
      formats: [
        {
          formatId: '137',
          ext: 'mp4',
          resolution: '1080p',
          fileSize: 47.5 * 1024 * 1024,
          vcodec: 'avc1',
          acodec: 'none',
          quality: 'hd1080',
          fps: 30,
          note: '',
        },
        {
          formatId: '140',
          ext: 'm4a',
          resolution: 'audio only',
          fileSize: 5.9 * 1024 * 1024,
          vcodec: 'none',
          acodec: 'mp4a.40.2',
          quality: 'medium',
          fps: 0,
          note: '',
        },
      ],
      bestAudioFormat: {
        formatId: '140',
        ext: 'm4a',
        resolution: 'audio only',
        fileSize: 5.9 * 1024 * 1024,
        vcodec: 'none',
        acodec: 'mp4a.40.2',
        quality: 'medium',
        fps: 0,
        note: '',
      },
    };

    const estimate = getMediaSizeEstimate(mediaInfo);
    expect(estimate.combinesSeparateAudio).toBe(true);
    expect(estimate.videoBytes).toBe(47.5 * 1024 * 1024);
    expect(estimate.audioBytes).toBe(5.9 * 1024 * 1024);
    expect(estimate.totalBytes).toBe((47.5 + 5.9) * 1024 * 1024);
  });

  it('does not double count audio for combined video-and-audio formats', () => {
    const mediaInfo: MediaInfo = {
      title: 'Test Video',
      duration: 120,
      thumbnail: '',
      url: 'https://youtube.com/watch?v=test',
      selectedFormat: '18',
      formats: [
        {
          formatId: '18',
          ext: 'mp4',
          resolution: '360p',
          fileSize: 32.1 * 1024 * 1024,
          vcodec: 'avc1.42001E',
          acodec: 'mp4a.40.2',
          quality: 'medium',
          fps: 30,
          note: '',
        },
      ],
    };

    const estimate = getMediaSizeEstimate(mediaInfo);
    expect(estimate.combinesSeparateAudio).toBe(false);
    expect(estimate.totalBytes).toBe(32.1 * 1024 * 1024);
  });
});
