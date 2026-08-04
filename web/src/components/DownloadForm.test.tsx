import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DownloadForm } from './DownloadForm';
import * as api from '../api';

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getCategories: vi.fn().mockResolvedValue([
      { id: 'cat-1', name: 'Media', directory: 'C:\\Media' },
    ]),
    resolveCapabilities: vi.fn(),
  };
});

const capability = (supported: boolean, protocols?: string[]) => ({
  supported,
  mutableNow: supported,
  supportedProtocols: protocols,
});

describe('DownloadForm Refactored Suite', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getCategories).mockResolvedValue([
      { id: 'cat-1', name: 'Media', directory: 'C:\\Media' },
    ]);
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

  it('preserves source text and options when submission fails', async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error('Network error starting download'));
    render(<DownloadForm onSubmit={onSubmit} />);

    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    fireEvent.change(input, { target: { value: 'https://example.com/file.zip' } });

    const startBtn = screen.getByRole('button', { name: /Start/i });
    fireEvent.click(startBtn);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('Network error starting download')).toBeInTheDocument();
    expect(input).toHaveValue('https://example.com/file.zip');
    expect(startBtn).not.toBeDisabled();
  });

  it('clears source text on successful submission', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<DownloadForm onSubmit={onSubmit} />);

    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    fireEvent.change(input, { target: { value: 'https://example.com/file.zip' } });

    const startBtn = screen.getByRole('button', { name: /Start/i });
    fireEvent.click(startBtn);

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    expect(input).toHaveValue('');
  });

  it('prevents duplicate submissions while awaiting', async () => {
    let resolveSubmit!: () => void;
    const onSubmit = vi.fn().mockImplementation(
      () => new Promise<void>((res) => { resolveSubmit = res; })
    );

    render(<DownloadForm onSubmit={onSubmit} />);

    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    fireEvent.change(input, { target: { value: 'https://example.com/file.zip' } });

    const startBtn = screen.getByRole('button', { name: /Start/i });
    fireEvent.click(startBtn);
    fireEvent.click(startBtn);

    expect(onSubmit).toHaveBeenCalledTimes(1);

    resolveSubmit();
    await waitFor(() => expect(input).toHaveValue(''));
  });

  it('allows batch mode and basic options to be configured independently', async () => {
    render(<DownloadForm onSubmit={vi.fn()} />);

    // Toggle basic options
    const optionsBtn = screen.getByTitle('Toggle download options');
    fireEvent.click(optionsBtn);
    expect(screen.getByLabelText('Priority')).toBeInTheDocument();

    // Input remains single line input
    expect(screen.getByPlaceholderText(/Paste a URL or magnet link/i)).toBeInTheDocument();

    // Toggle batch mode
    const batchBtn = screen.getByTitle('Switch to batch mode');
    fireEvent.click(batchBtn);
    expect(screen.getByPlaceholderText(/Paste download URLs or magnet links — one per line/i)).toBeInTheDocument();
  });

  it('produces mutually exclusive destination payloads', async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<DownloadForm onSubmit={onSubmit} />);

    const input = screen.getByPlaceholderText(/Paste a URL or magnet link/i);
    act(() => {
      fireEvent.change(input, { target: { value: 'https://example.com/file.zip' } });
    });

    // Open options
    act(() => {
      fireEvent.click(screen.getByTitle('Toggle download options'));
    });

    // Select Category mode
    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Category' }));
    });

    await waitFor(() => expect(screen.getByRole('option', { name: /Media/i })).toBeInTheDocument());

    const categorySelect = screen.getByLabelText('Select category');
    act(() => {
      fireEvent.change(categorySelect, { target: { value: 'cat-1' } });
    });

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: /Start/i }));
    });

    // Wait for the first submission to complete - input cleared indicates success
    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(input).toHaveValue(''));

    expect(onSubmit).toHaveBeenLastCalledWith(
      ['https://example.com/file.zip'],
      'normal',
      'cat-1',
      undefined,
      'rename',
      undefined,
      undefined,
      undefined
    );

    // Input was cleared after successful submit, set new value
    act(() => {
      fireEvent.change(input, { target: { value: 'https://example.com/file2.zip' } });
    });

    // Switch to Custom mode
    act(() => {
      fireEvent.click(screen.getByRole('button', { name: 'Custom folder' }));
    });

    await waitFor(() => expect(screen.getByLabelText('Custom destination path')).toBeInTheDocument());

    const customInput = screen.getByLabelText('Custom destination path');
    act(() => {
      fireEvent.change(customInput, { target: { value: 'C:\\Custom' } });
    });

    act(() => {
      fireEvent.click(screen.getByRole('button', { name: /Start/i }));
    });

    await waitFor(() => expect(onSubmit).toHaveBeenCalledTimes(2));

    expect(onSubmit).toHaveBeenLastCalledWith(
      ['https://example.com/file2.zip'],
      'normal',
      undefined,
      'C:\\Custom',
      'rename',
      undefined,
      undefined,
      undefined
    );
  });
});
