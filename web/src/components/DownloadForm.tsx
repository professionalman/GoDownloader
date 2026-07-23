import React, { useState } from 'react';

interface DownloadFormProps {
  onSubmit: (url: string) => void;
  disabled?: boolean;
}

export const DownloadForm: React.FC<DownloadFormProps> = ({ onSubmit, disabled }) => {
  const [url, setUrl] = useState('');
  const [error, setError] = useState('');

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = url.trim();
    if (!trimmed) {
      setError('Please enter a URL');
      return;
    }
    try {
      const parsed = new URL(trimmed);
      if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
        setError('Only HTTP and HTTPS URLs are supported');
        return;
      }
    } catch {
      setError('Please enter a valid URL');
      return;
    }
    setError('');
    onSubmit(trimmed);
    setUrl('');
  };

  return (
    <form className="download-form" onSubmit={handleSubmit}>
      <div className="input-row">
        <input
          type="text"
          className="url-input"
          placeholder="Paste direct download URL..."
          value={url}
          onChange={(e) => { setUrl(e.target.value); setError(''); }}
          disabled={disabled}
        />
        <button type="submit" className="btn btn-primary" disabled={disabled || !url.trim()}>
          Download
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
    </form>
  );
};
