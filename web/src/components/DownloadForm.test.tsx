import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DownloadForm } from './DownloadForm';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return { ...actual, getCategories: vi.fn().mockResolvedValue([]), resolveCapabilities: vi.fn() };
});

const capability = (supported: boolean, protocols?: string[]) => ({
  supported, mutableNow: supported, supportedProtocols: protocols,
});

describe('DownloadForm V0.7 controls', () => {
  beforeEach(() => {
    vi.mocked(api.resolveCapabilities).mockResolvedValue({
      pause: capability(true), resume: capability(true), cancel: capability(true), retry: capability(true),
      downloadLimit: capability(true), uploadLimit: capability(false),
      proxy: capability(true, ['http', 'socks5']), userAgent: capability(true),
      customHeaders: capability(true), retryPolicy: capability(true), timeoutPolicy: capability(true),
      connections: capability(true), fileSelection: capability(false), trackers: capability(false),
      seedingPolicy: capability(false),
    });
  });

  it('starts collapsed and renders only projected controls after expansion', async () => {
    render(<DownloadForm onSubmit={vi.fn()} />);
    expect(screen.queryByLabelText('Download limit (bytes/s)')).not.toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText(/Paste URL/), { target: { value: 'https://example.com/file' } });
    await waitFor(() => expect(api.resolveCapabilities).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Advanced Network Controls'));
    expect(await screen.findByLabelText('Download limit (bytes/s)')).toBeInTheDocument();
    expect(screen.queryByLabelText('Upload limit (bytes/s)')).not.toBeInTheDocument();
  });

  it('emits normalized policy rather than raw engine options', async () => {
    const onSubmit = vi.fn();
    render(<DownloadForm onSubmit={onSubmit} />);
    fireEvent.change(screen.getByPlaceholderText(/Paste URL/), { target: { value: 'https://example.com/file' } });
    await waitFor(() => expect(api.resolveCapabilities).toHaveBeenCalled());
    fireEvent.click(screen.getByText('Advanced Network Controls'));
    fireEvent.change(await screen.findByLabelText('Download limit (bytes/s)'), { target: { value: '0' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start Download' }));
    expect(onSubmit).toHaveBeenCalled();
    expect(onSubmit.mock.calls[0][5]).toMatchObject({ downloadLimitBytesPerSecond: 0 });
  });
});
