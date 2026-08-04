import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DownloadForm } from './DownloadForm';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getCategories: vi.fn().mockResolvedValue([]),
    resolveCapabilities: vi.fn(),
  };
});

const capability = (supported: boolean, protocols?: string[]) => ({
  supported,
  mutableNow: supported,
  supportedProtocols: protocols,
});

describe('DownloadForm V0.7 controls', () => {
  beforeEach(() => {
    vi.mocked(api.resolveCapabilities).mockResolvedValue({
      pause: capability(true),
      resume: capability(true),
      cancel: capability(true),
      retry: capability(true),
      downloadLimit: capability(true),
      uploadLimit: capability(false),
      proxy: capability(true, ['http', 'socks5']),
      userAgent: capability(true),
      customHeaders: capability(true),
      retryPolicy: capability(true),
      timeoutPolicy: capability(true),
      connections: capability(true),
      fileSelection: capability(false),
      trackers: capability(false),
      seedingPolicy: capability(false),
    });
  });

  it('starts collapsed and renders projected controls after expansion', async () => {
    render(<DownloadForm onSubmit={vi.fn()} />);

    // Initially collapsed: input placeholder is single line
    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    expect(input).toBeInTheDocument();
    expect(screen.queryByLabelText(/Download limit \(MiB\/s\)/i)).not.toBeInTheDocument();

    // Type source to trigger capabilities
    fireEvent.change(input, { target: { value: 'https://example.com/file' } });
    await waitFor(() => expect(api.resolveCapabilities).toHaveBeenCalled());

    // Click expand chevron
    fireEvent.click(screen.getByRole('button', { name: /Expand add panel/i }));

    // Click advanced options toggle
    fireEvent.click(screen.getByRole('button', { name: /Advanced options/i }));

    expect(await screen.findByLabelText(/Download limit \(MiB\/s\)/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/Upload limit \(MiB\/s\)/i)).not.toBeInTheDocument();
  });

  it('emits normalized policy converting MiB/s to bytes/s', async () => {
    const onSubmit = vi.fn();
    render(<DownloadForm onSubmit={onSubmit} />);

    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    fireEvent.change(input, { target: { value: 'https://example.com/file' } });
    await waitFor(() => expect(api.resolveCapabilities).toHaveBeenCalled());

    fireEvent.click(screen.getByRole('button', { name: /Expand add panel/i }));
    fireEvent.click(screen.getByRole('button', { name: /Advanced options/i }));

    const rateInput = await screen.findByLabelText(/Download limit \(MiB\/s\)/i);
    fireEvent.change(rateInput, { target: { value: '2' } });

    fireEvent.click(screen.getByRole('button', { name: /Start/i }));

    expect(onSubmit).toHaveBeenCalled();
    expect(onSubmit.mock.calls[0][5]).toMatchObject({
      downloadLimitBytesPerSecond: 2 * 1024 * 1024,
    });
  });
});
