import { describe, expect, it, vi, beforeEach } from 'vitest';
import {
  wrapIpcError,
  DesktopEventSubscription,
  desktopBackendClient,
} from './desktop';
import { ApiResponseError } from './types';
import * as DesktopService from '../bindings/downloader/cmd/desktop/desktopservice';
import { Events } from '@wailsio/runtime';

vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: vi.fn(),
  },
}));

vi.mock('../bindings/downloader/cmd/desktop/desktopservice', () => ({
  GetJobs: vi.fn(),
  GetJob: vi.fn(),
  CreateJob: vi.fn(),
  CreateBatchJobs: vi.fn(),
  PauseJob: vi.fn(),
  ResumeJob: vi.fn(),
  RetryJob: vi.fn(),
  CancelJob: vi.fn(),
  DeleteJob: vi.fn(),
  BulkAction: vi.fn(),
  SetJobPriority: vi.fn(),
  SelectFormat: vi.fn(),
  UploadTorrent: vi.fn(),
  GetTorrentFiles: vi.fn(),
  StartTorrent: vi.fn(),
  StopSeeding: vi.fn(),
  GetCapabilities: vi.fn(),
  ResolveCapabilities: vi.fn(),
  GetJobCapabilities: vi.fn(),
  UpdateJobNetwork: vi.fn(),
  AddTorrentTrackers: vi.fn(),
  UpdateSeedingPolicy: vi.fn(),
  OpenFolder: vi.fn(),
  GetQueueSnapshot: vi.fn(),
  ReorderQueue: vi.fn(),
  GetSettings: vi.fn(),
  UpdateSettings: vi.fn(),
  GetCategories: vi.fn(),
  CreateCategory: vi.fn(),
  UpdateCategory: vi.fn(),
  DeleteCategory: vi.fn(),
  GetTrackerSources: vi.fn(),
  CreateTrackerSource: vi.fn(),
  UpdateTrackerSource: vi.fn(),
  DeleteTrackerSource: vi.fn(),
  RefreshTrackerSource: vi.fn(),
  RefreshAllTrackerSources: vi.fn(),
  GetMediaAuthSettings: vi.fn(),
  UpdateMediaAuthSettings: vi.fn(),
  ImportMediaCookies: vi.fn(),
  DeleteMediaCookies: vi.fn(),
  GetSyncSnapshot: vi.fn(),
  GetBackendInstanceID: vi.fn(),
  GetDataRootInfo: vi.fn(),
  GetSingleInstanceStatus: vi.fn(),
  ShowNativeDialog: vi.fn(),
  Quit: vi.fn(),
}));

describe('Desktop Transport Layer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('wrapIpcError', () => {
    it('translates [ERROR_CODE] prefix into ApiResponseError with code', () => {
      try {
        wrapIpcError(new Error('[INSUFFICIENT_DISK_SPACE] disk full on target'));
        expect.unreachable();
      } catch (err) {
        expect(err).toBeInstanceOf(ApiResponseError);
        const apiErr = err as ApiResponseError;
        expect(apiErr.code).toBe('INSUFFICIENT_DISK_SPACE');
        expect(apiErr.message).toBe('disk full on target');
      }
    });

    it('translates raw error into ApiResponseError with INTERNAL_ERROR', () => {
      try {
        wrapIpcError(new Error('connection dropped'));
        expect.unreachable();
      } catch (err) {
        expect(err).toBeInstanceOf(ApiResponseError);
        const apiErr = err as ApiResponseError;
        expect(apiErr.code).toBe('INTERNAL_ERROR');
        expect(apiErr.message).toBe('connection dropped');
      }
    });
  });

  describe('DesktopBackendClient contract completeness', () => {
    it('exposes all 7 domain namespaces', () => {
      expect(desktopBackendClient.jobs).toBeDefined();
      expect(desktopBackendClient.queue).toBeDefined();
      expect(desktopBackendClient.settings).toBeDefined();
      expect(desktopBackendClient.categories).toBeDefined();
      expect(desktopBackendClient.tracker).toBeDefined();
      expect(desktopBackendClient.mediaAuth).toBeDefined();
      expect(desktopBackendClient.sync).toBeDefined();
    });

    it('jobs.getJobs calls DesktopService.GetJobs', async () => {
      const mockJobs = [{ id: 'job-1', name: 'test.mp4' }];
      vi.mocked(DesktopService.GetJobs).mockResolvedValue(mockJobs as any);

      const res = await desktopBackendClient.jobs.getJobs();
      expect(res).toEqual(mockJobs);
      expect(DesktopService.GetJobs).toHaveBeenCalledTimes(1);
    });

    it('jobs.createJob serializes policies and calls DesktopService.CreateJob', async () => {
      const mockJob = { id: 'job-created', source: 'https://example.com/file' };
      vi.mocked(DesktopService.CreateJob).mockResolvedValue(mockJob as any);

      const res = await desktopBackendClient.jobs.createJob(
        'https://example.com/file',
        'high',
        'cat-1',
        'C:\\downloads',
        'overwrite',
        { downloadLimitBytesPerSecond: 1000 },
        { mode: 'unlimited' },
        ['udp://tracker.test:1337']
      );

      expect(res).toEqual(mockJob);
      expect(DesktopService.CreateJob).toHaveBeenCalledWith(
        'https://example.com/file',
        'high',
        'cat-1',
        'C:\\downloads',
        'overwrite',
        JSON.stringify({ downloadLimitBytesPerSecond: 1000 }),
        JSON.stringify({ mode: 'unlimited' }),
        ['udp://tracker.test:1337']
      );
    });

    it('queue.getSnapshot calls DesktopService.GetQueueSnapshot', async () => {
      const mockQueue = { maxConcurrentDownloads: 3, runningDownloads: 1, queuedDownloads: 0 };
      vi.mocked(DesktopService.GetQueueSnapshot).mockResolvedValue(mockQueue as any);

      const res = await desktopBackendClient.queue.getSnapshot();
      expect(res).toEqual(mockQueue);
      expect(DesktopService.GetQueueSnapshot).toHaveBeenCalledTimes(1);
    });

    it('settings.getSettings and updateSettings call DesktopService', async () => {
      const mockSettings = { queue: { maxConcurrentDownloads: 5 } };
      vi.mocked(DesktopService.GetSettings).mockResolvedValue(mockSettings as any);
      vi.mocked(DesktopService.UpdateSettings).mockResolvedValue(mockSettings as any);

      const st = await desktopBackendClient.settings.getSettings();
      expect(st).toEqual(mockSettings);

      const updated = await desktopBackendClient.settings.updateSettings({ queue: { maxConcurrentDownloads: 5 } });
      expect(updated).toEqual(mockSettings);
      expect(DesktopService.UpdateSettings).toHaveBeenCalledWith(
        JSON.stringify({ queue: { maxConcurrentDownloads: 5 } })
      );
    });

    it('categories operations call DesktopService methods', async () => {
      vi.mocked(DesktopService.GetCategories).mockResolvedValue([{ id: 'c1', name: 'Videos', directory: 'videos', resolvedDirectory: 'C:\\dl\\videos' }] as any);
      const cats = await desktopBackendClient.categories.getCategories();
      expect(cats).toHaveLength(1);

      vi.mocked(DesktopService.CreateCategory).mockResolvedValue({ id: 'c2', name: 'Music', directory: 'music', resolvedDirectory: 'C:\\dl\\music' } as any);
      const created = await desktopBackendClient.categories.createCategory({ name: 'Music', directory: 'music' });
      expect(created.name).toBe('Music');

      vi.mocked(DesktopService.DeleteCategory).mockResolvedValue(undefined as any);
      await desktopBackendClient.categories.deleteCategory('c2');
      expect(DesktopService.DeleteCategory).toHaveBeenCalledWith('c2');
    });

    it('tracker operations call DesktopService methods', async () => {
      vi.mocked(DesktopService.GetTrackerSources).mockResolvedValue([{ id: 't1', name: 'OpenTrackers', url: 'https://trackers.list', enabled: true, refreshIntervalSeconds: 3600 }] as any);
      const sources = await desktopBackendClient.tracker.getSources();
      expect(sources).toHaveLength(1);

      vi.mocked(DesktopService.RefreshAllTrackerSources).mockResolvedValue({ failureCount: 0 } as any);
      const refreshRes = await desktopBackendClient.tracker.refreshAllSources();
      expect(refreshRes.failureCount).toBe(0);
    });

    it('mediaAuth operations call DesktopService methods', async () => {
      vi.mocked(DesktopService.GetMediaAuthSettings).mockResolvedValue({ mode: 'none', cookiePath: '', updatedAt: '' } as any);
      const auth = await desktopBackendClient.mediaAuth.getSettings();
      expect(auth.mode).toBe('none');

      vi.mocked(DesktopService.DeleteMediaCookies).mockResolvedValue({ mode: 'none', cookiePath: '', updatedAt: '' } as any);
      await desktopBackendClient.mediaAuth.deleteCookies();
      expect(DesktopService.DeleteMediaCookies).toHaveBeenCalledTimes(1);
    });

    it('sync.getSnapshot calls DesktopService.GetSyncSnapshot', async () => {
      const mockSnapshot = { cursor: 42, generation: 1, jobs: [] };
      vi.mocked(DesktopService.GetSyncSnapshot).mockResolvedValue(mockSnapshot as any);

      const snap = await desktopBackendClient.sync.getSnapshot();
      expect(snap.cursor).toBe(42);
      expect(DesktopService.GetSyncSnapshot).toHaveBeenCalledTimes(1);
    });
  });

  describe('DesktopEventSubscription', () => {
    it('wires Events.On, calls onConnected, dispatches events, and unregisters on close', () => {
      let registeredCallback: ((ev: any) => void) | null = null;
      const unlistenMock = vi.fn();
      vi.mocked(Events.On).mockImplementation((_name: any, cb: any) => {
        registeredCallback = cb;
        return unlistenMock;
      });

      const onEvent = vi.fn();
      const onSyncRequired = vi.fn();
      const onConnected = vi.fn();

      const sub = new DesktopEventSubscription({
        onEvent,
        onSyncRequired,
        onConnected,
      });

      expect(Events.On).toHaveBeenCalledWith('godownloader.event', expect.any(Function));
      expect(onConnected).toHaveBeenCalledTimes(1);
      expect(registeredCallback).not.toBeNull();

      // Dispatch standard job event
      registeredCallback!({
        data: {
          sequence: 101,
          type: 'job.updated',
          job: { id: 'job-1', status: 'downloading' },
        },
      });
      expect(onEvent).toHaveBeenCalledWith('job.updated', { id: 'job-1', status: 'downloading' }, 101);

      // Dispatch sync.required event
      registeredCallback!({
        data: {
          type: 'sync.required',
          data: { cursor: 100, reason: 'gap' },
        },
      });
      expect(onSyncRequired).toHaveBeenCalledWith({ cursor: 100, reason: 'gap' });

      // Close subscription
      sub.close();
      expect(unlistenMock).toHaveBeenCalledTimes(1);
    });

    it('triggers onSyncRequired when sequence gap is detected', () => {
      let registeredCallback: ((ev: any) => void) | null = null;
      vi.mocked(Events.On).mockImplementation((_name: any, cb: any) => {
        registeredCallback = cb;
        return vi.fn();
      });

      const onEvent = vi.fn();
      const onSyncRequired = vi.fn();
      let currentCursor = 5;

      new DesktopEventSubscription({
        onEvent,
        onSyncRequired,
        getCursor: () => currentCursor,
      });

      expect(registeredCallback).not.toBeNull();

      // Dispatch event with sequence 8 (expected 6, gap detected!)
      registeredCallback!({
        data: {
          sequence: 8,
          type: 'job.updated',
          job: { id: 'job-1', status: 'downloading' },
        },
      });

      expect(onSyncRequired).toHaveBeenCalledWith({ cursor: 5, reason: 'event_gap' });
      expect(onEvent).not.toHaveBeenCalled();
    });
  });
});

