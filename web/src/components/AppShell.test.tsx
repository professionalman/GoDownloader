import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import App from '../App';
import * as api from '../api';
import type { Job, JobPriority, JobStatus } from '../types';
import { AppShell } from './AppShell';

const sse = vi.hoisted(() => {
  const listeners = new Map<string, () => void>();
  const source = {
    addEventListener: vi.fn((event: string, listener: () => void) => listeners.set(event, listener)),
    removeEventListener: vi.fn((event: string) => listeners.delete(event)),
    close: vi.fn(),
  };
  return { listeners, source };
});

const appHarness = vi.hoisted(() => ({
  submit: undefined as undefined | ((sources: string[], priority: JobPriority) => Promise<void>),
  sseCallback: undefined as undefined | ((eventType: string, job: Job) => void),
}));

vi.mock('../api', async () => {
  const actual = await vi.importActual<typeof import('../api')>('../api');
  return {
    ...actual,
    getJobs: vi.fn().mockResolvedValue([]),
    createJob: vi.fn(),
    getQueueSnapshot: vi.fn().mockResolvedValue({
      maxConcurrentDownloads: 3,
      runningDownloads: 0,
      queuedDownloads: 0,
      pausedDownloads: 0,
      items: [],
    }),
    getSettings: vi.fn().mockResolvedValue(null),
    connectSSE: vi.fn((callback: (eventType: string, job: Job) => void) => {
      appHarness.sseCallback = callback;
      return sse.source;
    }),
  };
});

vi.mock('./DownloadForm', () => ({
  DownloadForm: ({ onSubmit }: { onSubmit: (sources: string[], priority: JobPriority) => Promise<void> }) => {
    appHarness.submit = onSubmit;
    return <div>Download form</div>;
  },
}));
vi.mock('./JobList', () => ({
  JobList: ({ jobs }: { jobs: Job[] }) => (
    <div data-testid="job-list">
      {jobs.map((job) => <span key={job.id} data-job-id={job.id}>{`${job.id}:${job.status}`}</span>)}
    </div>
  ),
}));
vi.mock('./QueueSection', () => ({ QueueSection: () => <div>Queue section</div> }));
vi.mock('./SettingsPanel', () => ({ SettingsPanel: () => <div>Settings panel</div> }));
vi.mock('./FormatSelector', () => ({ FormatSelector: () => <div>Format selector</div> }));
vi.mock('./TorrentFileSelector', () => ({ TorrentFileSelector: () => <div>Torrent selector</div> }));

function makeJob(id: string, updatedAt: string, status: JobStatus): Job {
  return {
    id,
    updatedAt,
    status,
    source: 'https://example.com/media',
    name: id,
  } as Job;
}

describe('AppShell', () => {
  beforeEach(() => {
    sse.listeners.clear();
    appHarness.submit = undefined;
    appHarness.sseCallback = undefined;
    vi.clearAllMocks();
    vi.mocked(api.getJobs).mockResolvedValue([]);
    vi.mocked(api.createJob).mockReset();
  });

  it('renders state-based desktop and compact navigation with counts and settings', () => {
    const onViewModeChange = vi.fn();
    const onOpenSettings = vi.fn();

    render(
      <AppShell
        viewMode="downloads"
        downloadCount={7}
        queueCount={2}
        connectionState="connected"
        onViewModeChange={onViewModeChange}
        onOpenSettings={onOpenSettings}
      >
        <div>Existing content</div>
      </AppShell>,
    );

    const desktopNav = screen.getByRole('navigation', { name: 'Main navigation' });
    const compactNav = screen.getByRole('navigation', { name: 'Compact navigation' });
    expect(within(desktopNav).getByRole('button', { name: /Downloads/ })).toHaveAttribute('aria-current', 'page');
    expect(within(desktopNav).getByLabelText('7 downloads')).toBeInTheDocument();
    expect(within(compactNav).getByLabelText('2 queue')).toBeInTheDocument();
    expect(screen.getByText('Version 0.7')).toBeInTheDocument();
    expect(within(compactNav).getByText('v0.7')).toBeInTheDocument();

    fireEvent.click(within(desktopNav).getByRole('button', { name: /Queue/ }));
    expect(onViewModeChange).toHaveBeenCalledWith('queue');
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    expect(onOpenSettings).toHaveBeenCalledOnce();
  });

  it('projects only the supplied EventSource lifecycle state', () => {
    const props = {
      viewMode: 'downloads' as const,
      downloadCount: 0,
      queueCount: 0,
      onViewModeChange: vi.fn(),
      onOpenSettings: vi.fn(),
      children: <div />,
    };
    const { rerender } = render(<AppShell {...props} connectionState="connecting" />);
    expect(screen.getByLabelText('GoDownloader Connecting')).toBeInTheDocument();

    rerender(<AppShell {...props} connectionState="connected" />);
    expect(screen.getByLabelText('GoDownloader Connected')).toBeInTheDocument();

    rerender(<AppShell {...props} connectionState="reconnecting" />);
    expect(screen.getByLabelText('GoDownloader Reconnecting')).toBeInTheDocument();
  });

  it('updates connection status from the existing EventSource lifecycle and closes it', () => {
    const { unmount } = render(<App />);
    expect(screen.getByLabelText('GoDownloader Connecting')).toBeInTheDocument();

    act(() => sse.listeners.get('open')?.());
    expect(screen.getByLabelText('GoDownloader Connected')).toBeInTheDocument();

    act(() => sse.listeners.get('error')?.());
    expect(screen.getByLabelText('GoDownloader Reconnecting')).toBeInTheDocument();

    expect(sse.source.addEventListener).toHaveBeenCalledWith('open', expect.any(Function));
    expect(sse.source.addEventListener).toHaveBeenCalledWith('error', expect.any(Function));
    unmount();
    expect(sse.source.close).toHaveBeenCalledOnce();
  });

  it('keeps the newer SSE Job when the stale HTTP create response resolves later', async () => {
    const httpJob = makeJob('job-1', '2026-08-01T08:00:00Z', 'queued');
    const sseJob = makeJob('job-1', '2026-08-01T08:01:00Z', 'awaiting_selection');
    let resolveCreate!: (job: Job) => void;
    vi.mocked(api.createJob).mockImplementation(() => new Promise<Job>((resolve) => {
      resolveCreate = resolve;
    }));

    render(<App />);
    await waitFor(() => expect(appHarness.submit).toBeDefined());

    let submitPromise!: Promise<void>;
    act(() => {
      submitPromise = appHarness.submit!([httpJob.source], 'normal');
    });
    act(() => {
      appHarness.sseCallback?.('job.created', sseJob);
    });

    await waitFor(() => expect(screen.getByTestId('job-list')).toHaveTextContent('job-1:awaiting_selection'));
    await act(async () => {
      resolveCreate(httpJob);
      await submitPromise;
    });

    const jobList = screen.getByTestId('job-list');
    expect(jobList.querySelectorAll('[data-job-id="job-1"]')).toHaveLength(1);
    expect(jobList).toHaveTextContent('job-1:awaiting_selection');
  });
});