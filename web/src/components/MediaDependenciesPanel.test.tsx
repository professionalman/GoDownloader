import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import type { ExternalDependencyStatus, MediaDependencyStatus } from '../types';
import { MediaDependenciesPanel } from './MediaDependenciesPanel';

vi.mock('../api', () => ({
  getMediaDependencyStatus: vi.fn(),
}));

const dep = (tool: string, overrides: Partial<ExternalDependencyStatus> = {}): ExternalDependencyStatus => ({
  tool,
  state: 'available',
  available: true,
  version: '7.1.1',
  ownership: 'external_user',
  source: 'system_path',
  restartRequired: false,
  ...overrides,
});

function status(overrides: Partial<MediaDependencyStatus> = {}): MediaDependencyStatus {
  return {
    ytdlp: {
      tool: 'yt-dlp',
      available: true,
      ownership: 'managed',
      authenticated: true,
      restartRequired: false,
      busy: false,
      platformSupported: true,
      canInstall: false,
      canUpdate: true,
    },
    ffmpeg: dep('ffmpeg'),
    ffprobe: dep('ffprobe', { source: 'alongside_ffmpeg' }),
    javascriptRuntime: { detected: true, runtime: 'deno', diagnostic: 'Deno was found on PATH.' },
    workflows: [
      { id: 'media_analysis', label: 'Analyze media links', requires: ['yt-dlp'], ready: true, advisory: false },
      { id: 'merge_video_audio', label: 'Download best quality', requires: ['yt-dlp', 'ffmpeg', 'ffprobe'], ready: true, advisory: false },
    ],
    ...overrides,
  };
}

describe('MediaDependenciesPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows available external tools with source and ownership', async () => {
    vi.mocked(api.getMediaDependencyStatus).mockResolvedValue(status());
    render(<MediaDependenciesPanel />);
    const ffmpeg = await screen.findByLabelText('FFmpeg status');
    expect(within(ffmpeg).getByText('Available')).toBeInTheDocument();
    expect(within(ffmpeg).getByText('System-provided (PATH)')).toBeInTheDocument();
    expect(within(ffmpeg).getByText(/does not install or update it/)).toBeInTheDocument();
    expect(within(screen.getByLabelText('FFprobe status')).getByText('Next to FFmpeg')).toBeInTheDocument();
    expect(screen.getByLabelText('Download best quality: ready')).toBeInTheDocument();
    expect(screen.getByText('Found (deno)')).toBeInTheDocument();
  });

  it('names a missing dependency instead of a generic failure', async () => {
    vi.mocked(api.getMediaDependencyStatus).mockResolvedValue(
      status({
        ffmpeg: dep('ffmpeg', { state: 'missing', available: false, version: undefined, source: 'none', diagnostic: 'FFmpeg was not found. Install FFmpeg and add it to PATH.' }),
        workflows: [
          {
            id: 'merge_video_audio',
            label: 'Download best quality',
            requires: ['yt-dlp', 'ffmpeg', 'ffprobe'],
            ready: false,
            advisory: false,
            diagnostic: 'Unavailable until FFmpeg is ready.',
          },
        ],
      }),
    );
    render(<MediaDependenciesPanel />);
    const ffmpeg = await screen.findByLabelText('FFmpeg status');
    expect(within(ffmpeg).getByText('Not found')).toBeInTheDocument();
    expect(within(ffmpeg).getByText(/Install FFmpeg and add it to PATH/)).toBeInTheDocument();
    expect(screen.getByLabelText('Download best quality: not ready')).toBeInTheDocument();
    expect(screen.getByText('Unavailable until FFmpeg is ready.')).toBeInTheDocument();
  });

  it('distinguishes an unusable executable and a pending restart', async () => {
    vi.mocked(api.getMediaDependencyStatus).mockResolvedValue(
      status({
        ffmpeg: dep('ffmpeg', { restartRequired: true, diagnostic: 'Restart GoDownloader to use it.' }),
        ffprobe: dep('ffprobe', { state: 'invalid', available: false, diagnostic: 'The FFmpeg folder executable does not identify itself as FFprobe.' }),
        javascriptRuntime: { detected: false, diagnostic: 'No Deno runtime was found on PATH.' },
      }),
    );
    render(<MediaDependenciesPanel />);
    expect(within(await screen.findByLabelText('FFmpeg status')).getByText('Restart required')).toBeInTheDocument();
    expect(within(screen.getByLabelText('FFprobe status')).getByText('Not usable')).toBeInTheDocument();
    expect(within(screen.getByLabelText('JavaScript runtime status')).getByText('Not found')).toBeInTheDocument();
  });

  it('reports a load failure and re-checks on demand', async () => {
    vi.mocked(api.getMediaDependencyStatus).mockRejectedValueOnce(new Error('backend unavailable')).mockResolvedValueOnce(status());
    render(<MediaDependenciesPanel />);
    expect(await screen.findByRole('alert')).toHaveTextContent('backend unavailable');
    expect(api.getMediaDependencyStatus).toHaveBeenCalledTimes(1);
  });

  it('re-checks when asked', async () => {
    vi.mocked(api.getMediaDependencyStatus).mockResolvedValue(status());
    render(<MediaDependenciesPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Re-check/i }));
    await waitFor(() => expect(api.getMediaDependencyStatus).toHaveBeenCalledTimes(2));
  });
});
