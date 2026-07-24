import React, { useEffect, useState } from 'react';
import type { Job, TorrentFile, TorrentFileSelection, TorrentFilePriority } from '../types';
import { getTorrentFiles } from '../api';

interface TorrentFileSelectorProps {
  job: Job;
  onStart: (jobId: string, files: TorrentFileSelection[], seedAfterComplete: boolean) => void;
  onClose: () => void;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + sizes[i];
}

export const TorrentFileSelector: React.FC<TorrentFileSelectorProps> = ({ job, onStart, onClose }) => {
  const [files, setFiles] = useState<TorrentFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [seedAfterComplete, setSeedAfterComplete] = useState(false);

  useEffect(() => {
    getTorrentFiles(job.id)
      .then(f => {
        setFiles(f);
        setLoading(false);
      })
      .catch(err => {
        setError(err.message || 'Failed to fetch torrent files');
        setLoading(false);
      });
  }, [job.id]);

  const toggleSelection = (index: number) => {
    setFiles(prev => prev.map(f => f.index === index ? { ...f, selected: !f.selected } : f));
  };

  const changePriority = (index: number, priority: TorrentFilePriority) => {
    setFiles(prev => prev.map(f => f.index === index ? { ...f, priority } : f));
  };

  const selectAll = (selected: boolean) => {
    setFiles(prev => prev.map(f => ({ ...f, selected })));
  };

  const handleStart = () => {
    const selection = files.map(f => ({
      index: f.index,
      priority: f.selected ? f.priority : ('skip' as TorrentFilePriority)
    }));
    onStart(job.id, selection, seedAfterComplete);
  };

  const selectedCount = files.filter(f => f.selected).length;
  const selectedSize = files.filter(f => f.selected).reduce((acc, f) => acc + f.size, 0);
  const totalSize = files.reduce((acc, f) => acc + f.size, 0);

  return (
    <div className="modal-overlay">
      <div className="modal-content torrent-modal">
        <h2>Select Files for {job.name}</h2>
        {error && <div className="app-error">{error}</div>}
        
        {loading ? (
          <div style={{ textAlign: 'center', padding: '20px' }}>Loading files...</div>
        ) : (
          <>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '10px' }}>
              <div>
                <button className="btn btn-sm btn-secondary" onClick={() => selectAll(true)} style={{ marginRight: '8px' }}>Select All</button>
                <button className="btn btn-sm btn-secondary" onClick={() => selectAll(false)}>Deselect All</button>
              </div>
              <div>
                Selected: {selectedCount}/{files.length} ({formatBytes(selectedSize)} / {formatBytes(totalSize)})
              </div>
            </div>

            <div style={{ maxHeight: '400px', overflowY: 'auto' }}>
              <table className="format-table torrent-file-table">
                <thead>
                  <tr>
                    <th><input type="checkbox" onChange={(e) => selectAll(e.target.checked)} checked={selectedCount === files.length && files.length > 0} /></th>
                    <th>File Path</th>
                    <th>Size</th>
                    <th>Priority</th>
                  </tr>
                </thead>
                <tbody>
                  {files.map(f => (
                    <tr key={f.index}>
                      <td>
                        <input type="checkbox" checked={f.selected} onChange={() => toggleSelection(f.index)} />
                      </td>
                      <td style={{ wordBreak: 'break-all' }}>{f.path}</td>
                      <td>{formatBytes(f.size)}</td>
                      <td>
                        <select
                          value={f.priority}
                          onChange={(e) => changePriority(f.index, e.target.value as TorrentFilePriority)}
                          disabled={!f.selected}
                          style={{ background: '#21262d', color: '#e1e4e8', border: '1px solid #30363d', borderRadius: '4px', padding: '2px 4px' }}
                        >
                          <option value="normal">Normal</option>
                          <option value="high">High</option>
                          <option value="maximum">Maximum</option>
                        </select>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            
            <div style={{ marginTop: '16px', display: 'flex', alignItems: 'center', gap: '8px' }}>
              <input 
                type="checkbox" 
                id="seedAfterComplete" 
                checked={seedAfterComplete} 
                onChange={(e) => setSeedAfterComplete(e.target.checked)} 
              />
              <label htmlFor="seedAfterComplete">Keep seeding after download completes</label>
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: '8px', marginTop: '16px' }}>
              <button className="btn btn-secondary" onClick={onClose}>Cancel</button>
              <button className="btn btn-primary" onClick={handleStart} disabled={selectedCount === 0}>
                Start Download
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
};
