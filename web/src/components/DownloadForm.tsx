import React, { useState, useEffect, useRef } from 'react';
import type { JobPriority, Category, FilenameConflictPolicy } from '../types';
import { getCategories } from '../api';

interface DownloadFormProps {
  onSubmit: (
    sources: string[],
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: FilenameConflictPolicy
  ) => void;
  onUploadTorrent?: (
    file: File,
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string
  ) => void;
  disabled?: boolean;
}

export const DownloadForm: React.FC<DownloadFormProps> = ({ onSubmit, onUploadTorrent, disabled }) => {
  const [inputText, setInputText] = useState('');
  const [priority, setPriority] = useState<JobPriority>('normal');
  const [categories, setCategories] = useState<Category[]>([]);
  const [selectedCategoryId, setSelectedCategoryId] = useState<string>('');
  const [customDestDir, setCustomDestDir] = useState<string>('');
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>('rename');
  const [error, setError] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    getCategories()
      .then(setCategories)
      .catch(() => {});
  }, []);

  const handleCategoryChange = (catId: string) => {
    setSelectedCategoryId(catId);
    if (catId) {
      setCustomDestDir(''); // Mutually exclusive with custom destination
    }
  };

  const handleCustomDestChange = (dir: string) => {
    setCustomDestDir(dir);
    if (dir.trim()) {
      setSelectedCategoryId(''); // Mutually exclusive with category
    }
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const lines = inputText
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line.length > 0);

    if (lines.length === 0) {
      setError('Please enter at least one URL or Magnet link');
      return;
    }

    if (lines.length > 100) {
      setError('Batch submission cannot exceed 100 links at once');
      return;
    }

    if (selectedCategoryId && customDestDir.trim()) {
      setError('Cannot specify both Category and Custom Destination Folder');
      return;
    }

    setError('');
    onSubmit(
      lines,
      priority,
      selectedCategoryId || undefined,
      customDestDir.trim() || undefined,
      conflictPolicy
    );
    setInputText('');
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file && onUploadTorrent) {
      onUploadTorrent(
        file,
        priority,
        selectedCategoryId || undefined,
        customDestDir.trim() || undefined
      );
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
          onChange={(e) => {
            setInputText(e.target.value);
            setError('');
          }}
          disabled={disabled}
        />

        <div className="form-options-grid" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: '0.8rem', marginTop: '0.8rem', marginBottom: '0.8rem' }}>
          <div className="form-option-group">
            <label htmlFor="category-select" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Category:</label>
            <select
              id="category-select"
              className="select-dropdown"
              style={{ width: '100%', padding: '0.4rem' }}
              value={selectedCategoryId}
              onChange={(e) => handleCategoryChange(e.target.value)}
              disabled={disabled}
            >
              <option value="">(None - Default Download Folder)</option>
              {categories.map((cat) => (
                <option key={cat.id} value={cat.id}>
                  {cat.name} ({cat.directory})
                </option>
              ))}
            </select>
          </div>

          <div className="form-option-group">
            <label htmlFor="custom-dest-dir" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Custom Folder:</label>
            <input
              id="custom-dest-dir"
              type="text"
              className="input-text"
              placeholder="e.g. /custom/path"
              style={{ width: '100%', padding: '0.4rem' }}
              value={customDestDir}
              onChange={(e) => handleCustomDestChange(e.target.value)}
              disabled={disabled || !!selectedCategoryId}
            />
          </div>

          <div className="form-option-group">
            <label htmlFor="conflict-policy-select" style={{ fontSize: '0.85rem', display: 'block', marginBottom: '0.2rem' }}>Conflict Policy:</label>
            <select
              id="conflict-policy-select"
              className="select-dropdown"
              style={{ width: '100%', padding: '0.4rem' }}
              value={conflictPolicy}
              onChange={(e) => setConflictPolicy(e.target.value as FilenameConflictPolicy)}
              disabled={disabled}
            >
              <option value="rename">Auto Rename</option>
              <option value="overwrite">Overwrite</option>
              <option value="fail">Fail on Conflict</option>
            </select>
          </div>
        </div>

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
      {error && <div className="form-error" style={{ marginTop: '0.5rem' }}>{error}</div>}
    </form>
  );
};
