import { useState, useEffect, useCallback } from 'react';
import { DownloadForm } from './components/DownloadForm';
import { JobList } from './components/JobList';
import { FormatSelector } from './components/FormatSelector';
import { TorrentFileSelector } from './components/TorrentFileSelector';
import type { Job, TorrentFileSelection } from './types';
import { getJobs, createJob, cancelJob, pauseJob, resumeJob, retryJob, connectSSE, openFolder, selectFormat, uploadTorrent, startTorrent, stopSeeding } from './api';
import './App.css';

function App() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [formatJobId, setFormatJobId] = useState<string | null>(null);
  const [torrentJobId, setTorrentJobId] = useState<string | null>(null);

  // Fetch jobs on mount
  useEffect(() => {
    getJobs()
      .then(setJobs)
      .catch((err) => setError(err.message));
  }, []);

  // Connect SSE for live progress
  useEffect(() => {
    const es = connectSSE((_eventType: string, updatedJob: Job) => {
      setJobs((prev) => {
        const idx = prev.findIndex((j) => j.id === updatedJob.id);
        if (idx === -1) {
          // New job — add to beginning
          return [updatedJob, ...prev];
        }
        const updated = [...prev];
        updated[idx] = updatedJob;
        return updated;
      });
    });

    return () => es.close();
  }, []);

  const handleDownload = useCallback(async (url: string) => {
    setLoading(true);
    setError('');
    try {
      const job = await createJob(url);
      setJobs((prev) => [job, ...prev]);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to start download');
    } finally {
      setLoading(false);
    }
  }, []);

  const handleUploadTorrent = useCallback(async (file: File) => {
    setLoading(true);
    setError('');
    try {
      const job = await uploadTorrent(file);
      setJobs((prev) => [job, ...prev]);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to upload torrent');
    } finally {
      setLoading(false);
    }
  }, []);

  const handleCancel = useCallback(async (id: string) => {
    try {
      const updated = await cancelJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to cancel download');
    }
  }, []);

  const handlePause = useCallback(async (id: string) => {
    try {
      const updated = await pauseJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to pause download');
    }
  }, []);

  const handleResume = useCallback(async (id: string) => {
    try {
      const updated = await resumeJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to resume download');
    }
  }, []);

  const handleRetry = useCallback(async (id: string) => {
    try {
      const updated = await retryJob(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to retry download');
    }
  }, []);

  const handleSelectFormat = useCallback((id: string) => {
    setFormatJobId(id);
  }, []);

  const handleFormatSelected = useCallback(async (jobId: string, formatId: string) => {
    try {
      const updated = await selectFormat(jobId, formatId);
      setJobs((prev) => prev.map((j) => (j.id === jobId ? updated : j)));
      setFormatJobId(null);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to select format');
    }
  }, []);

  const handleSelectTorrentFiles = useCallback((id: string) => {
    setTorrentJobId(id);
  }, []);

  const handleStartTorrent = useCallback(async (jobId: string, files: TorrentFileSelection[], seedAfterComplete: boolean) => {
    try {
      const updated = await startTorrent(jobId, files, seedAfterComplete);
      setJobs((prev) => prev.map((j) => (j.id === jobId ? updated : j)));
      setTorrentJobId(null);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to start torrent');
    }
  }, []);

  const handleStopSeeding = useCallback(async (id: string) => {
    try {
      const updated = await stopSeeding(id);
      setJobs((prev) => prev.map((j) => (j.id === id ? updated : j)));
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to stop seeding');
    }
  }, []);

  const formatJob = formatJobId ? jobs.find((j) => j.id === formatJobId) : null;
  const torrentJob = torrentJobId ? jobs.find((j) => j.id === torrentJobId) : null;

  return (
    <div className="app">
      <header className="app-header">
        <h1>⬇ GoDownloader</h1>
        <span className="app-version">v0.4</span>
      </header>

      <main className="app-main">
        <DownloadForm onSubmit={handleDownload} onUploadTorrent={handleUploadTorrent} disabled={loading} />

        {error && (
          <div className="app-error">
            {error}
            <button className="btn-dismiss" onClick={() => setError('')}>✕</button>
          </div>
        )}

        <JobList
          jobs={jobs}
          onCancel={handleCancel}
          onPause={handlePause}
          onResume={handleResume}
          onRetry={handleRetry}
          onOpenFolder={openFolder}
          onSelectFormat={handleSelectFormat}
          onSelectTorrentFiles={handleSelectTorrentFiles}
          onStopSeeding={handleStopSeeding}
        />
      </main>

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

