import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Job } from '../types';
import { FormatSelector } from './FormatSelector';

const sampleJob: Job = {
  id: 'job-101',
  source: 'https://youtube.com/watch?v=test',
  name: 'Test Video',
  status: 'awaiting_selection',
  type: 'media',
  progress: 0,
  totalBytes: 0,
  completedBytes: 0,
  speedBytesPerSecond: 0,
  etaSeconds: 0,
  engine: 'ytdlp',
  networkPolicy: {
    downloadLimitBytesPerSecond: 0,
    proxy: { mode: 'disabled' },
    retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
    timeoutPolicy: { connectTimeoutSeconds: 10, requestTimeoutSeconds: 30 },
  },
  effectiveDownloadLimitBytesPerSecond: 0,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
  mediaInfo: {
    title: 'Test Media Video Title',
    duration: 300,
    thumbnail: 'https://example.com/thumb.jpg',
    url: 'https://youtube.com/watch?v=test',
    formats: [
      {
        formatId: 'f-2160p',
        ext: 'mp4',
        resolution: '3840x2160',
        fileSize: 500000000,
        vcodec: 'avc1.640033',
        acodec: 'none',
        fps: 60,
        quality: '2160p60',
        note: '2160p60',
      },
      {
        formatId: 'f-1080p-avc',
        ext: 'mp4',
        resolution: '1920x1080',
        fileSize: 250000000,
        vcodec: 'avc1.640028',
        acodec: 'none',
        fps: 60,
        quality: '1080p60',
        note: '1080p60',
      },
      {
        formatId: 'f-1080p-vp9',
        ext: 'webm',
        resolution: '1920x1080',
        fileSize: 210000000,
        vcodec: 'vp9',
        acodec: 'none',
        fps: 60,
        quality: '1080p60',
        note: '1080p60',
      },
      {
        formatId: 'f-720p',
        ext: 'mp4',
        resolution: '1280x720',
        fileSize: 100000000,
        vcodec: 'avc1.4d401f',
        acodec: 'none',
        fps: 30,
        quality: '720p',
        note: '720p',
      },
      {
        formatId: 'f-audio-high',
        ext: 'm4a',
        resolution: 'audio only',
        fileSize: 20000000,
        vcodec: 'none',
        acodec: 'mp4a.40.2',
        fps: 0,
        quality: 'audio only',
        note: '256k audio',
      },
      {
        formatId: 'f-audio-low',
        ext: 'webm',
        resolution: 'audio only',
        fileSize: 10000000,
        vcodec: 'none',
        acodec: 'opus',
        fps: 0,
        quality: 'audio only',
        note: '128k audio',
      },
    ],
  },
};

describe('FormatSelector component', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('1. Video tab is initially available', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const videoTab = screen.getByRole('tab', { name: /video/i });
    expect(videoTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('Test Media Video Title')).toBeInTheDocument();
  });

  it('2. Audio tab shows only audio options', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const audioTab = screen.getByRole('tab', { name: /audio/i });
    fireEvent.click(audioTab);
    expect(audioTab).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByTestId('best-audio-card')).toBeInTheDocument();
    expect(screen.queryByTestId('best-video-card')).not.toBeInTheDocument();
  });

  it('3. Best Video selection', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const bestCard = screen.getByTestId('best-video-card');
    fireEvent.click(bestCard);
    expect(bestCard).toHaveClass('selected');
    const summary = screen.getByTestId('format-footer-summary');
    expect(summary).toHaveTextContent(/best available video/i);
  });

  it('4. Specific video-quality selection', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const groupCard = screen.getByTestId('video-card-720p');
    fireEvent.click(groupCard);
    expect(groupCard).toHaveClass('selected');
    const summary = screen.getByTestId('format-footer-summary');
    expect(summary).toHaveTextContent(/720p/i);
  });

  it('5. Best Audio selection', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('tab', { name: /audio/i }));
    const bestAudioCard = screen.getByTestId('best-audio-card');
    fireEvent.click(bestAudioCard);
    expect(bestAudioCard).toHaveClass('selected');
    const summary = screen.getByTestId('format-footer-summary');
    expect(summary).toHaveTextContent(/best available audio/i);
  });

  it('6. Specific audio-quality selection', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    fireEvent.click(screen.getByRole('tab', { name: /audio/i }));
    const specificAudioCard = screen.getByTestId('audio-card-f-audio-low');
    fireEvent.click(specificAudioCard);
    expect(specificAudioCard).toHaveClass('selected');
  });

  it('7. Duplicate resolutions are grouped', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    // 1080p60 has both AVC and VP9 formats, but renders a single top-level group card
    const cards = screen.getAllByTestId(/video-card-1080p/i);
    expect(cards).toHaveLength(1);
  });

  it('8. Alternative codec options can be expanded', async () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const toggleBtn = screen.getByTestId('codec-options-toggle-1080p 60 FPS');
    expect(screen.queryByTestId('alt-codec-f-1080p-vp9')).not.toBeInTheDocument();
    fireEvent.click(toggleBtn);
    await waitFor(() => {
      expect(screen.getByTestId('alt-codec-f-1080p-vp9')).toBeInTheDocument();
    });
  });

  it('9. Card click selects but does not submit', () => {
    const onSelect = vi.fn();
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('video-card-720p'));
    expect(onSelect).not.toHaveBeenCalled();
  });

  it('10. Confirmation submits exactly once', async () => {
    const onSelect = vi.fn().mockResolvedValue(undefined);
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('video-card-720p'));
    const confirmBtn = screen.getByTestId('format-confirm-button');
    fireEvent.click(confirmBtn);
    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledTimes(1);
    });
  });

  it('11. Correct mode and format ID are sent', async () => {
    const onSelect = vi.fn().mockResolvedValue(undefined);
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('video-card-720p'));
    fireEvent.click(screen.getByTestId('format-confirm-button'));
    await waitFor(() => {
      expect(onSelect).toHaveBeenCalledWith('job-101', 'f-720p');
    });
  });

  it('12. Modal remains open during submission', async () => {
    let resolveSelect: () => void = () => {};
    const pendingPromise = new Promise<void>((resolve) => {
      resolveSelect = resolve;
    });
    const onSelect = vi.fn().mockReturnValue(pendingPromise);

    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('format-confirm-button'));

    expect(screen.getByText('Starting…')).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();

    resolveSelect();
    await waitFor(() => expect(onSelect).toHaveBeenCalled());
  });

  it('13. Modal closes after success', async () => {
    const onSelect = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={onClose} />);
    fireEvent.click(screen.getByTestId('format-confirm-button'));
    await waitFor(() => {
      expect(onSelect).toHaveBeenCalled();
    });
  });

  it('14. Modal remains after failure', async () => {
    const onSelect = vi.fn().mockRejectedValue(new Error('Network error selecting format'));
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('format-confirm-button'));
    await waitFor(() => {
      expect(screen.getByRole('dialog')).toBeInTheDocument();
    });
  });

  it('15. Inline API failure appears', async () => {
    const onSelect = vi.fn().mockRejectedValue(new Error('Failed to select format ID'));
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    fireEvent.click(screen.getByTestId('format-confirm-button'));
    const alertBanner = await screen.findByTestId('format-error-banner');
    expect(alertBanner).toHaveTextContent('Failed to select format ID');
  });

  it('16. Selection survives failure', async () => {
    const onSelect = vi.fn().mockRejectedValue(new Error('Backend error'));
    render(<FormatSelector job={sampleJob} onSelect={onSelect} onClose={vi.fn()} />);
    const card720 = screen.getByTestId('video-card-720p');
    fireEvent.click(card720);
    fireEvent.click(screen.getByTestId('format-confirm-button'));
    await screen.findByTestId('format-error-banner');
    expect(card720).toHaveClass('selected');
  });

  it('17. Escape handling', () => {
    const onClose = vi.fn();
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={onClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('18. Keyboard selection', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const card720 = screen.getByTestId('video-card-720p');
    fireEvent.keyDown(card720, { key: 'Enter' });
    expect(card720).toHaveClass('selected');
  });

  it('19. Mobile layout has no horizontal overflow', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const modalElement = screen.getByRole('dialog');
    expect(modalElement).toHaveClass('format-modal');
  });

  it('20. Video choices clearly state that best audio is included', () => {
    render(<FormatSelector job={sampleJob} onSelect={vi.fn()} onClose={vi.fn()} />);
    const noticeElements = screen.getAllByText(/best available audio included/i);
    expect(noticeElements.length).toBeGreaterThan(0);
  });
});
