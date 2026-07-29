import { useState, useEffect, useCallback } from 'react';
import { DownloadForm } from './components/DownloadForm';
import { JobList } from './components/JobList';
import { QueueSection } from './components/QueueSection';
import { SettingsPanel } from './components/SettingsPanel';
import { FormatSelector } from './components/FormatSelector';
import { TorrentFileSelector } from './components/TorrentFileSelector';
import type { Job, JobPriority, QueueSnapshot, AppSettings, TorrentFileSelection, JobNetworkPolicyOverride, SeedingPolicy } from './types';
import {
  getJobs,
  createJob,
  createBatchJobs,
  bulkAction,
  setJobPriority,
  getQueueSnapshot,
  reorderQueue,
  getSettings,
  updateSettings,
  cancelJob,
  pauseJob,
  resumeJob,
  retryJob,
  connectSSE,
  openFolder,
  selectFormat,
  uploadTorrent,
  startTorrent,
  stopSeeding,
} from './api';
import './App.css';

function App() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [viewMode, setViewMode] = useState<'downloads' | 'queue'>('downloads');
  const [queueSnapshot, setQueueSnapshot] = useState<QueueSnapshot | null>(null);
  const [settings, setSettings] = useState<AppSettings | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
  const [formatJobId, setFormatJobId] = useState<string | null>(null);
  const [torrentJobId, setTorrentJobId] = useState<string | null>(null);

  const fetchQueue = useCallback(async () => {
    try {
      const q = await getQueueSnapshot();
      setQueueSnapshot(q);
    } catch {
      // ignore
    }
  }, []);

  const fetchSettings = useCallback(async () => {
    try {
      const s = await getSettings();
      setSettings(s);
    } catch {
      // ignore
    }
  }, []);

  // Fetch initial data on mount
  useEffect(() => {
    getJobs()
      .then(setJobs)
      .catch((err) => setError(err.message));
    fetchQueue();
    fetchSettings();
  }, [fetchQueue, fetchSettings]);

  // Connect SSE for live progress
  useEffect(() => {
    const es = connectSSE((_eventType: string, updatedJob: Job) => {
      setJobs((prev) => {
        const idx = prev.findIndex((j) => j.id === updatedJob.id);
        if (idx === -1) {
          return [updatedJob, ...prev];
        }
        const updated = [...prev];
        updated[idx] = updatedJob;
        return updated;
      });
      fetchQueue();
    });

    return () => es.close();
  }, [fetchQueue]);

  const handleDownload = useCallback(async (
    sources: string[],
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: import('./types').FilenameConflictPolicy,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ) => {
    setLoading(true);
    setError('');
    try {
      if (sources.length === 1) {
        const job = await createJob(sources[0], priority, categoryId, destinationDir, conflictPolicy, networkPolicy, seedingPolicy, trackers);
        setJobs((prev) => [job, ...prev]);
      } else {
        const resp = await createBatchJobs(
          sources.map((s) => ({ source: s, priority, categoryId, destinationDir, conflictPolicy, networkPolicy, seedingPolicy, trackers }))
        );
        const newJobs = resp.items.map((it) => it.job).filter((j): j is Job => !!j);
        setJobs((prev) => [...newJobs, ...prev]);
      }
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to start download');
    } finally {
      setLoading(false);
    }
  }, [fetchQueue]);

  const handleUploadTorrent = useCallback(async (
    file: File,
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[]
  ) => {
    setLoading(true);
    setError('');
    try {
      const job = await uploadTorrent(file, priority, categoryId, destinationDir, networkPolicy, seedingPolicy, trackers);
      setJobs((prev) => [job, ...prev]);
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to upload torrent');
    } finally {
      setLoading(false);
    }
  }, [fetchQueue]);

  const handleToggleSelect = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const handleSelectAll = useCallback((ids: string[]) => {
    setSelectedIds(new Set(ids));
  }, []);

  const handleBulkAction = useCallback(async (action: 'pause' | 'resume' | 'cancel' | 'retry') => {
    const ids = Array.from(selectedIds);
    if (ids.length === 0) return;

    try {
      const resp = await bulkAction(action, ids);
      const updatedMap = new Map<string, Job>();
      resp.results.forEach((r) => {
        if (r.job) updatedMap.set(r.jobId, r.job);
      });
      setJobs((prev) => prev.map((j) => updatedMap.get(j.id) || j));
      setSelectedIds(new Set());
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : `Failed to ${action} selected jobs`);
    }
  }, [selectedIds, fetchQueue]);

  const handleSetPriority = useCallback(async (jobId: string, priority: JobPriority) => {
    try {
      const updated = await setJobPriority(jobId, priority);
      setJobs((prev) => prev.map((j) => (j.id === jobId ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to change priority');
    }
  }, [fetchQueue]);

  const handleReorderQueue = useCallback(async (priority: JobPriority, jobIds: string[]) => {
    try {
      await reorderQueue(priority, jobIds);
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to reorder queue');
    }
  }, [fetchQueue]);

  const handleUpdateSettings = useCallback(async (payload: import('./types').UpdateSettingsPayload) => {
    const updated = await updateSettings(payload);
    setSettings(updated);
    fetchQueue();
  }, [fetchQueue]);

  const handleCancel = useCallback(async (id: string) => {
    try {
      const updated = await cancelJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to cancel download');
    }
  }, [fetchQueue]);

  const handlePause = useCallback(async (id: string) => {
    try {
      const updated = await pauseJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to pause download');
    }
  }, [fetchQueue]);

  const handleResume = useCallback(async (id: string) => {
    try {
      const updated = await resumeJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to resume download');
    }
  }, [fetchQueue]);

  const handleRetry = useCallback(async (id: string) => {
    try {
      const updated = await retryJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to retry download');
    }
  }, [fetchQueue]);

  const handleSelectFormat = useCallback((id: string) => {
    setFormatJobId(id);
  }, []);

  const handleFormatSelected = useCallback(async (jobId: string, formatId: string) => {
    try {
      const updated = await selectFormat(jobId, formatId);
      setJobs((prev) => prev.map((j) => (j.id === jobId ? updated : j)));
      setFormatJobId(null);
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to select format');
    }
  }, [fetchQueue]);

  const handleSelectTorrentFiles = useCallback((id: string) => {
    setTorrentJobId(id);
  }, []);

  const handleStartTorrent = useCallback(async (jobId: string, files: TorrentFileSelection[], seedingPolicy: SeedingPolicy) => {
    try {
      const updated = await startTorrent(jobId, files, seedingPolicy);
      setJobs((prev) => prev.map((j) => (j.id === jobId ? updated : j)));
      setTorrentJobId(null);
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to start torrent');
    }
  }, [fetchQueue]);

  const handleStopSeeding = useCallback(async (id: string) => {
    try {
      const updated = await stopSeeding(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
      fetchQueue();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to stop seeding');
    }
  }, [fetchQueue]);

  const formatJob = formatJobId ? jobs.find((j) => j.id === formatJobId) : null;
  const torrentJob = torrentJobId ? jobs.find((j) => j.id === torrentJobId) : null;

  return (
    <div className="app">
      <header className="app-header">
        <div className="header-brand">
          <h1>⬇ GoDownloader</h1>
          <span className="app-version">v0.6</span>
        </div>

        <div className="header-nav">
          <div className="tab-group">
            <button
              type="button"
              className={`tab-btn ${viewMode === 'downloads' ? 'active' : ''}`}
              onClick={() => setViewMode('downloads')}
            >
              📥 All Downloads ({jobs.length})
            </button>
            <button
              type="button"
              className={`tab-btn ${viewMode === 'queue' ? 'active' : ''}`}
              onClick={() => setViewMode('queue')}
            >
              ⏳ Smart Queue {queueSnapshot?.queuedDownloads ? `(${queueSnapshot.queuedDownloads})` : ''}
            </button>
          </div>

          <button
            type="button"
            className="btn btn-secondary btn-settings"
            onClick={() => setShowSettings(true)}
            title="Settings"
          >
            ⚙️ Settings
          </button>
        </div>
      </header>

      <main className="app-main">
        <DownloadForm onSubmit={handleDownload} onUploadTorrent={handleUploadTorrent} disabled={loading} />

        {error && (
          <div className="app-error">
            {error}
            <button type="button" className="btn-dismiss" onClick={() => setError('')}>✕</button>
          </div>
        )}

        {/* Bulk Action Toolbar */}
        {selectedIds.size > 0 && (
          <div className="bulk-toolbar">
            <span className="bulk-count">{selectedIds.size} job(s) selected</span>
            <div className="bulk-actions">
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => handleBulkAction('pause')}>
                ⏸ Pause Selected
              </button>
              <button type="button" className="btn btn-sm btn-primary" onClick={() => handleBulkAction('resume')}>
                ▶ Resume Selected
              </button>
              <button type="button" className="btn btn-sm btn-secondary" onClick={() => handleBulkAction('retry')}>
                ↻ Retry Selected
              </button>
              <button type="button" className="btn btn-sm btn-danger" onClick={() => handleBulkAction('cancel')}>
                ✕ Cancel Selected
              </button>
              <button type="button" className="btn btn-sm btn-link" onClick={() => setSelectedIds(new Set())}>
                Clear Selection
              </button>
            </div>
          </div>
        )}

        {viewMode === 'downloads' ? (
          <JobList
            jobs={jobs}
            selectedIds={selectedIds}
            onToggleSelect={handleToggleSelect}
            onSelectAll={handleSelectAll}
            onCancel={handleCancel}
            onPause={handlePause}
            onResume={handleResume}
            onRetry={handleRetry}
            onOpenFolder={openFolder}
            onSelectFormat={handleSelectFormat}
            onSelectTorrentFiles={handleSelectTorrentFiles}
            onStopSeeding={handleStopSeeding}
          />
        ) : (
          <QueueSection
            snapshot={queueSnapshot}
            onSetPriority={handleSetPriority}
            onReorder={handleReorderQueue}
            onPause={handlePause}
            onResume={handleResume}
            onCancel={handleCancel}
          />
        )}
      </main>

      {/* Settings Modal */}
      {showSettings && (
        <SettingsPanel
          settings={settings}
          onSave={handleUpdateSettings}
          onClose={() => setShowSettings(false)}
        />
      )}

      {/* Format Selector Modal */}
      {formatJob && (
        <FormatSelector
          job={formatJob}
          onSelect={handleFormatSelected}
          onClose={() => setFormatJobId(null)}
        />
      )}

      {/* Torrent File Selector Modal */}
      {torrentJob && (
        <TorrentFileSelector
          job={torrentJob}
          onStart={handleStartTorrent}
          onClose={() => setTorrentJobId(null)}
        />
      )}
    </div>
  );
}

export default App;
