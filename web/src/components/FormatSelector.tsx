import React from 'react';
import type { Job, MediaFormat } from '../types';

interface FormatSelectorProps {
  job: Job;
  onSelect: (jobId: string, formatId: string) => void;
  onClose: () => void;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '—';
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + sizes[i];
}

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '—';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return `${m}:${String(s).padStart(2, '0')}`;
}

function categorizeFormats(formats: MediaFormat[]): {
  combined: MediaFormat[];
  videoOnly: MediaFormat[];
  audioOnly: MediaFormat[];
} {
  const combined: MediaFormat[] = [];
  const videoOnly: MediaFormat[] = [];
  const audioOnly: MediaFormat[] = [];

  for (const f of formats) {
    if (f.quality === 'audio only' || (!f.vcodec && f.acodec)) {
      audioOnly.push(f);
    } else if (f.vcodec && f.acodec) {
      combined.push(f);
    } else if (f.vcodec && !f.acodec) {
      videoOnly.push(f);
    }
  }

  return { combined, videoOnly, audioOnly };
}

export const FormatSelector: React.FC<FormatSelectorProps> = ({ job, onSelect, onClose }) => {
  const mediaInfo = job.mediaInfo;
  if (!mediaInfo) return null;

  const { combined, videoOnly, audioOnly } = categorizeFormats(mediaInfo.formats);

  const handleSelect = (formatId: string) => {
    onSelect(job.id, formatId);
    onClose();
  };

  const handleBestQuality = () => {
    // Find best combined, or best video
    const best = combined[0] || videoOnly[0];
    if (best) {
      handleSelect(best.formatId);
    }
  };

  return (
    <div className="format-overlay" onClick={onClose}>
      <div className="format-modal" onClick={(e) => e.stopPropagation()}>
        <div className="format-header">
          <div className="format-title-row">
            {mediaInfo.thumbnail && (
              <img
                src={mediaInfo.thumbnail}
                alt={mediaInfo.title}
                className="format-thumbnail"
                onError={(e) => { (e.target as HTMLImageElement).style.display = 'none'; }}
              />
            )}
            <div className="format-info">
              <h3 className="format-media-title">{mediaInfo.title}</h3>
              <span className="format-duration">Duration: {formatDuration(mediaInfo.duration)}</span>
            </div>
          </div>
          <button className="btn-dismiss format-close" onClick={onClose}>✕</button>
        </div>

        <div className="format-actions-top">
          <button className="btn btn-primary" onClick={handleBestQuality}>
            ⬇ Best Quality
          </button>
        </div>

        {combined.length > 0 && (
          <FormatTable title="Video + Audio" formats={combined} onSelect={handleSelect} />
        )}
        {videoOnly.length > 0 && (
          <FormatTable title="Video Only" formats={videoOnly} onSelect={handleSelect} />
        )}
        {audioOnly.length > 0 && (
          <FormatTable title="Audio Only" formats={audioOnly} onSelect={handleSelect} />
        )}

        {mediaInfo.formats.length === 0 && (
          <p className="empty-message">No formats available</p>
        )}
      </div>
    </div>
  );
};

interface FormatTableProps {
  title: string;
  formats: MediaFormat[];
  onSelect: (formatId: string) => void;
}

const FormatTable: React.FC<FormatTableProps> = ({ title, formats, onSelect }) => (
  <div className="format-section">
    <h4 className="format-section-title">{title}</h4>
    <div className="format-table">
      <div className="format-table-header">
        <span>Quality</span>
        <span>Format</span>
        <span>Codec</span>
        <span>Size</span>
        <span></span>
      </div>
      {formats.map((f) => (
        <div key={f.formatId} className="format-row">
          <span className="format-quality">{f.quality || f.note || '—'}</span>
          <span className="format-ext">.{f.ext}</span>
          <span className="format-codec">
            {[f.vcodec, f.acodec].filter(Boolean).join(' / ') || '—'}
          </span>
          <span className="format-size">{formatBytes(f.fileSize)}</span>
          <button className="btn btn-secondary btn-sm" onClick={() => onSelect(f.formatId)}>
            Select
          </button>
        </div>
      ))}
    </div>
  </div>
);
