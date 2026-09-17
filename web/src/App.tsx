import { useState, useEffect, useCallback, useRef } from 'react';
import { DownloadForm } from './components/DownloadForm';
import { DownloadsPanel } from './components/DownloadsPanel';
import { QueueSection } from './components/QueueSection';
import { SettingsPanel } from './components/SettingsPanel';
import { FormatSelector } from './components/FormatSelector';
import { TorrentFileSelector } from './components/TorrentFileSelector';
import { DeleteConfirmDialog } from './components/DeleteConfirmDialog';
import { AppShell } from './components/AppShell';
import type { ConnectionState } from './components/AppShell';
import type {
  Job,
  JobPriority,
  QueueSnapshot,
  AppSettings,
  TorrentFileSelection,
  JobNetworkPolicyOverride,
  SeedingPolicy,
  BulkAction,
  SubtitleOptions,
} from './types';
import { removeJob, replaceJobsFromInitialLoad, reconcileJobsFromSnapshot, upsertJob, upsertJobs } from './jobState';
import { useJobSelection } from './hooks/useJobSelection';
import {
  getJobs,
  getSyncSnapshot,
  createJob,
  createBatchJobs,
  bulkAction,
  cancelJob,
  deleteJob,
  pauseJob,
  resumeJob,
  retryJob,
  getSettings,
  updateSettings,
  getQueueSnapshot,
  reorderQueue,
  startTorrent,
  stopSeeding,
  openFolder,
  selectFormat,
  uploadTorrent,
  setJobPriority,
  connectSSE,
  initSession,
} from './api';
import './App.css';

function App() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [initialLoading, setInitialLoading] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');
  const [viewMode, setViewMode] = useState<'downloads' | 'queue'>('downloads');
  const [queueSnapshot, setQueueSnapshot] = useState<QueueSnapshot | null>(null);
  const [settings, setSettings] = useState<AppSettings | null>(null);
  const [showSettings, setShowSettings] = useState(false);
  const {
    selectedIds,
    setSelectedIds,
    toggleSelect: handleToggleSelect,
    selectVisible: handleSelectVisible,
    deselectVisible: handleDeselectVisible,
    clearSelection: handleClearSelection,
  } = useJobSelection(jobs);
  const [formatJobId, setFormatJobId] = useState<string | null>(null);
  const [torrentJobId, setTorrentJobId] = useState<string | null>(null);
  const [deleteJobId, setDeleteJobId] = useState<string | null>(null);
  const [connectionState, setConnectionState] = useState<ConnectionState>('connecting');
  const lastAppliedCursorRef = useRef<number>(0);
  const rehydrationGenRef = useRef<number>(0);
  const activeEsRef = useRef<EventSource | null>(null);
  const [streamTrigger, setStreamTrigger] = useState<number>(0);

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

  // Fetch initial authoritative data on mount
  useEffect(() => {
    let cancelled = false;
    setInitialLoading(true);

    initSession()
      .then(() => getSyncSnapshot())
      .then((snapshot) => {
        if (!cancelled) {
          lastAppliedCursorRef.current = snapshot.cursor;
          setJobs(snapshot.jobs);
          setQueueSnapshot(snapshot.queue);
          if (snapshot.cursor > 0) {
            setStreamTrigger((t) => t + 1);
          }
        }
      })
      .catch((err) => {
        getJobs()
          .then((loadedJobs) => {
            if (!cancelled) {
              setJobs((currentJobs) =>
                replaceJobsFromInitialLoad(currentJobs, loadedJobs)
              );
            }
          })
          .catch(() => {});
        if (!cancelled) setError(err.message);
      })
      .finally(() => {
        if (!cancelled) setInitialLoading(false);
      });

    fetchSettings();

    return () => {
      cancelled = true;
    };
  }, [fetchSettings]);

  const handleEvent = useCallback((eventType: string, updatedJob: Job, seq?: number) => {
    if (seq !== undefined && seq > 0) {
      if (seq <= lastAppliedCursorRef.current) {
        // Idempotent deduplication: already applied
        return;
      }
      lastAppliedCursorRef.current = seq;
    }

    if (eventType === 'job.deleted') {
      setJobs((currentJobs) => removeJob(currentJobs, updatedJob.id));
    } else {
      setJobs((currentJobs) => upsertJob(currentJobs, updatedJob));
    }
    fetchQueue();
  }, [fetchQueue]);

  const handleSyncRequired = useCallback(async () => {
    const gen = ++rehydrationGenRef.current;
    setConnectionState('reconnecting');

    if (activeEsRef.current) {
      try {
        activeEsRef.current.close();
      } catch {
        // ignore
      }
      activeEsRef.current = null;
    }

    try {
      const snapshot = await getSyncSnapshot();
      if (gen !== rehydrationGenRef.current) {
        // Superseded by newer rehydration request
        return;
      }
      setJobs((currentJobs) => reconcileJobsFromSnapshot(currentJobs, snapshot.jobs));
      setQueueSnapshot(snapshot.queue);
      lastAppliedCursorRef.current = snapshot.cursor;

      // Trigger reconnection from authoritative snapshot cursor
      setStreamTrigger((t) => t + 1);
    } catch (err) {
      console.error('Failed to rehydrate state from sync snapshot:', err);
    }
  }, []);

  // Connect SSE for live progress and bounded replay
  useEffect(() => {
    const es = connectSSE(
      handleEvent,
      handleSyncRequired,
      () => lastAppliedCursorRef.current
    );
    activeEsRef.current = es;

    const handleOpen = () => setConnectionState('connected');
    const handleError = () => {
      setConnectionState('reconnecting');
      if (activeEsRef.current === es) {
        handleSyncRequired();
      }
    };

    es.addEventListener('open', handleOpen);
    es.addEventListener('error', handleError);

    return () => {
      es.removeEventListener('open', handleOpen);
      es.removeEventListener('error', handleError);
      if (activeEsRef.current === es) {
        es.close();
        activeEsRef.current = null;
      }
    };
  }, [streamTrigger, handleEvent, handleSyncRequired]);

  const handleDownload = useCallback(
    async (
      sources: string[],
      priority: JobPriority,
      categoryId?: string,
      destinationDir?: string,
      conflictPolicy?: import('./types').FilenameConflictPolicy,
      networkPolicy?: JobNetworkPolicyOverride,
      seedingPolicy?: SeedingPolicy,
      trackers?: string[]
    ) => {
      setSubmitting(true);
      setError('');
      try {
        if (sources.length === 1) {
          const job = await createJob(
            sources[0],
            priority,
            categoryId,
            destinationDir,
            conflictPolicy,
            networkPolicy,
            seedingPolicy,
            trackers
          );
          setJobs((currentJobs) => upsertJob(currentJobs, job));
        } else {
          const resp = await createBatchJobs(
            sources.map((s) => ({
              source: s,
              priority,
              categoryId,
              destinationDir,
              conflictPolicy,
              networkPolicy,
              seedingPolicy,
              trackers,
            }))
          );
          const newJobs = resp.items.map((it) => it.job).filter((j): j is Job => !!j);
          setJobs((currentJobs) => upsertJobs(currentJobs, newJobs));
        }
        fetchQueue();
      } catch (err: unknown) {
        const errorObj = err instanceof Error ? err : new Error('Failed to start download');
        setError(errorObj.message);
        throw errorObj;
      } finally {
        setSubmitting(false);
      }
    },
    [fetchQueue]
  );

  const handleUploadTorrent = useCallback(
    async (
      file: File,
      priority: JobPriority,
      categoryId?: string,
      destinationDir?: string,
      networkPolicy?: JobNetworkPolicyOverride,
      seedingPolicy?: SeedingPolicy,
      trackers?: string[]
    ) => {
      setSubmitting(true);
      setError('');
      try {
        const job = await uploadTorrent(
          file,
          priority,
          categoryId,
          destinationDir,
          networkPolicy,
          seedingPolicy,
          trackers
        );
        setJobs((currentJobs) => upsertJob(currentJobs, job));
        fetchQueue();
      } catch (err: unknown) {
        const errorObj = err instanceof Error ? err : new Error('Failed to upload torrent');
        setError(errorObj.message);
        throw errorObj;
      } finally {
        setSubmitting(false);
      }
    },
    [fetchQueue]
  );

  const handleJobUpdated = useCallback((updated: Job) => {
    setJobs((currentJobs) => upsertJob(currentJobs, updated));
  }, []);

  const handleBulkAction = useCallback(
    async (action: BulkAction, eligibleIds: string[]) => {
      if (eligibleIds.length === 0) return;

      try {
        const response = await bulkAction(action, eligibleIds);

        const updatedJobs = response.results
          .map((result) => result.job)
          .filter((job): job is Job => Boolean(job));

        setJobs((currentJobs) => upsertJobs(currentJobs, updatedJobs));

        const succeededIds = new Set(
          response.results
            .filter((result) => result.success)
            .map((result) => result.jobId)
        );

        setSelectedIds((current) => new Set([...current].filter((id) => !succeededIds.has(id))));

        if (response.failed > 0) {
          const details = response.results
            .filter((result) => !result.success)
            .map((result) => {
              const job = jobs.find((candidate) => candidate.id === result.jobId);
              return `${job?.name ?? result.jobId}: ${
                result.error?.message ?? 'Action failed'
              }`;
            })
            .join(' | ');

          setError(
            `${response.succeeded} succeeded, ${response.failed} failed. ${details}`
          );
        } else {
          setError('');
        }

        fetchQueue();
      } catch (err: unknown) {
        setError(
          err instanceof Error
            ? err.message
            : `Failed to ${action} selected jobs`
        );
      }
    },
    [jobs, fetchQueue, setSelectedIds]
  );

  const handleSetPriority = useCallback(
    async (jobId: string, priority: JobPriority) => {
      try {
        const updated = await setJobPriority(jobId, priority);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to change priority');
      }
    },
    [fetchQueue]
  );

  const handleReorderQueue = useCallback(
    async (priority: JobPriority, jobIds: string[]) => {
      try {
        await reorderQueue(priority, jobIds);
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to reorder queue');
      }
    },
    [fetchQueue]
  );

  const handleUpdateSettings = useCallback(
    async (payload: import('./types').UpdateSettingsPayload) => {
      const updated = await updateSettings(payload);
      setSettings(updated);
      fetchQueue();
    },
    [fetchQueue]
  );

  const handleCancel = useCallback(
    async (id: string) => {
      try {
        const updated = await cancelJob(id);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to cancel download');
      }
    },
    [fetchQueue]
  );

  const handleDelete = useCallback(
    async (id: string, deleteFiles: boolean) => {
      try {
        await deleteJob(id, deleteFiles);
        setJobs((currentJobs) => removeJob(currentJobs, id));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to delete download');
        throw err;
      }
    },
    [fetchQueue]
  );

  const handleOpenDelete = useCallback((id: string) => {
    setDeleteJobId(id);
  }, []);

  const handlePause = useCallback(
    async (id: string) => {
      try {
        const updated = await pauseJob(id);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to pause download');
      }
    },
    [fetchQueue]
  );

  const handleResume = useCallback(
    async (id: string) => {
      try {
        const updated = await resumeJob(id);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to resume download');
      }
    },
    [fetchQueue]
  );

  const handleRetry = useCallback(
    async (id: string) => {
      try {
        const updated = await retryJob(id);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to retry download');
      }
    },
    [fetchQueue]
  );

  const handleSelectFormat = useCallback((id: string) => {
    setFormatJobId(id);
  }, []);

  const handleFormatSelected = useCallback(
    async (jobId: string, formatId: string, subtitleOptions?: SubtitleOptions) => {
      try {
        const updated = await selectFormat(jobId, formatId, subtitleOptions);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        setFormatJobId(null);
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to select format');
        throw err;
      }
    },
    [fetchQueue]
  );

  const handleSelectTorrentFiles = useCallback((id: string) => {
    setTorrentJobId(id);
  }, []);

  const handleStartTorrent = useCallback(
    async (
      jobId: string,
      files: TorrentFileSelection[],
      seedingPolicy: SeedingPolicy
    ) => {
      const updated = await startTorrent(jobId, files, seedingPolicy);
      setJobs((currentJobs) => upsertJob(currentJobs, updated));
      setTorrentJobId(null);
      fetchQueue();
    },
    [fetchQueue]
  );

  const handleStopSeeding = useCallback(
    async (id: string) => {
      try {
        const updated = await stopSeeding(id);
        setJobs((currentJobs) => upsertJob(currentJobs, updated));
        fetchQueue();
      } catch (err: unknown) {
        setError(err instanceof Error ? err.message : 'Failed to stop seeding');
      }
    },
    [fetchQueue]
  );

  const formatJob = formatJobId ? jobs.find((j) => j.id === formatJobId) : null;
  const torrentJob = torrentJobId ? jobs.find((j) => j.id === torrentJobId) : null;
  const jobToDelete = deleteJobId ? jobs.find((j) => j.id === deleteJobId) : null;

  return (
    <>
      <AppShell
        viewMode={viewMode}
        downloadCount={jobs.length}
        queueCount={queueSnapshot?.queuedDownloads ?? 0}
        connectionState={connectionState}
        onViewModeChange={setViewMode}
        onOpenSettings={() => setShowSettings(true)}
      >
        {viewMode === 'downloads' ? (
          <>
            <DownloadForm
              onSubmit={handleDownload}
              onUploadTorrent={handleUploadTorrent}
              disabled={submitting}
            />

            {error && (
              <div
                className="my-2 flex items-center justify-between rounded-md border border-destructive/40 bg-destructive/10 p-3 text-xs text-destructive"
                role="alert"
              >
                <span>{error}</span>
                <button
                  type="button"
                  aria-label="Dismiss error"
                  className="text-base font-bold leading-none hover:opacity-80"
                  onClick={() => setError('')}
                >
                  ×
                </button>
              </div>
            )}

            <DownloadsPanel
              jobs={jobs}
              initialLoading={initialLoading}
              selectedIds={selectedIds}
              queueSnapshot={queueSnapshot}
              onToggleSelect={handleToggleSelect}
              onSelectVisible={handleSelectVisible}
              onDeselectVisible={handleDeselectVisible}
              onBulkAction={handleBulkAction}
              onClearSelection={handleClearSelection}
              onCancel={handleCancel}
              onPause={handlePause}
              onResume={handleResume}
              onRetry={handleRetry}
              onDelete={handleOpenDelete}
              onOpenFolder={openFolder}
              onSelectFormat={handleSelectFormat}
              onSelectTorrentFiles={handleSelectTorrentFiles}
              onStopSeeding={handleStopSeeding}
              onJobUpdated={handleJobUpdated}
            />
          </>
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
      </AppShell>

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

      {/* Delete Confirmation Modal */}
      {jobToDelete && (
        <DeleteConfirmDialog
          job={jobToDelete}
          onConfirm={handleDelete}
          onClose={() => setDeleteJobId(null)}
        />
      )}
    </>
  );
}

export default App;
