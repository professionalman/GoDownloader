import React, { useState, useRef } from 'react';
import type { JobPriority } from '../types';

interface DownloadFormProps {
  onSubmit: (sources: string[], priority: JobPriority) => void;
  onUploadTorrent?: (file: File, priority: JobPriority) => void;
  disabled?: boolean;
}

export const DownloadForm: React.FC<DownloadFormProps> = ({ onSubmit, onUploadTorrent, disabled }) => {
  const [inputText, setInputText] = useState('');
  const [priority, setPriority] = useState<JobPriority>('normal');
  const [error, setError] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const lines = inputText
      .split('\n')
      .map(line => line.trim())
      .filter(line => line.length > 0);

    if (lines.length === 0) {
      setError('Please enter at least one URL or Magnet link');
      return;
    }

    if (lines.length > 100) {
      setError('Batch submission cannot exceed 100 links at once');
      return;
    }

    setError('');
    onSubmit(lines, priority);
    setInputText('');
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file && onUploadTorrent) {
      onUploadTorrent(file, priority);
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  return (
    <form className="download-form" onSubmit={handleSubmit}>
      <div className="form-controls">
        <textarea
          className="url-input multiline-input"
          placeholder="Paste URL(s) or Magnet link(s)... One link per line for batch downloads"
          rows={3}
          value={inputText}
          onChange={(e) => { setInputText(e.target.value); setError(''); }}
          disabled={disabled}
        />
        
        <div className="form-action-bar">
          <div className="priority-selector">
            <label htmlFor="job-priority-select">Priority:</label>
            <select
              id="job-priority-select"
              className="priority-select"
              value={priority}
              onChange={(e) => setPriority(e.target.value as JobPriority)}
              disabled={disabled}
            >
              <option value="high">🔥 High</option>
              <option value="normal">⚡ Normal</option>
              <option value="low">🐢 Low</option>
            </select>
          </div>

          <div className="button-group">
            {onUploadTorrent && (
              <>
                <input
                  type="file"
                  accept=".torrent"
                  style={{ display: 'none' }}
                  ref={fileInputRef}
                  onChange={handleFileChange}
                  disabled={disabled}
                />
                <button
                  type="button"
                  className="btn btn-secondary btn-upload-torrent"
                  onClick={() => fileInputRef.current?.click()}
                  disabled={disabled}
                >
                  📁 Add .torrent
                </button>
              </>
            )}
            <button
              type="submit"
              className="btn btn-primary"
              disabled={disabled || !inputText.trim()}
            >
              Start Download
            </button>
          </div>
        </div>
      </div>
      {error && <div className="form-error">{error}</div>}
    </form>
  );
};
