import { useState, useEffect, useCallback } from 'react';
import { DownloadForm } from './components/DownloadForm';
import { JobList } from './components/JobList';
import type { Job } from './types';
import { getJobs, createJob, cancelJob, pauseJob, resumeJob, retryJob, connectSSE, openFolder } from './api';
import './App.css';

function App() {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

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

  return (
    <div className="app">
      <header className="app-header">
        <h1>⬇ Local Downloader</h1>
      </header>

      <main className="app-main">
        <DownloadForm onSubmit={handleDownload} disabled={loading} />

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
        />
      </main>
    </div>
  );
}

export default App;
