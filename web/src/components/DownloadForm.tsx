import React, { useState, useRef } from 'react';

interface DownloadFormProps {
  onSubmit: (url: string) => void;
  onUploadTorrent?: (file: File) => void;
  disabled?: boolean;
}

export const DownloadForm: React.FC<DownloadFormProps> = ({ onSubmit, onUploadTorrent, disabled }) => {
  const [url, setUrl] = useState('');
  const [error, setError] = useState('');
  const fileInputRef = useRef<HTMLInputElement>(null);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed) {
      setError('Please enter a URL');
      return;
    }
    
    if (trimmed.toLowerCase().startsWith('magnet:')) {
      // Valid magnet
    } else {
      try {
        const parsed = new URL(trimmed);
        if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
          setError('Only HTTP, HTTPS, and Magnet URLs are supported');
          return;
        }
      } catch {
        setError('Please enter a valid URL');
        return;
      }
    }
    
    setError('');
    onSubmit(trimmed);
    setUrl('');
  };

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file && onUploadTorrent) {
      onUploadTorrent(file);
    }
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  return (
    <form className="download-form" onSubmit={handleSubmit}>
      <div className="input-row">
        <input
          type="text"
          className="url-input"
          placeholder="Paste URL (HTTP/HTTPS) or Magnet link..."
          value={url}
          onChange={(e) => { setUrl(e.target.value); setError(''); }}
          disabled={disabled}
        />
        <button type="submit" className="btn btn-primary" disabled={disabled || !url.trim()}>
          Download
        </button>
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
      </div>
      {error && <div className="form-error">{error}</div>}
    </form>
  );
};
