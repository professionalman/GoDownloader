import React, { useState, useEffect, useRef, useId } from 'react';
import {
  Video, Music, Check, ChevronDown, ChevronUp, X, Download, Loader2, Sparkles, ShieldCheck, Zap, Info, Volume2
} from 'lucide-react';
import type { Job, MediaFormat } from '../types';

export interface FormatSelectorProps {
  job: Job;
  onSelect: (jobId: string, formatId: string) => Promise<void> | void;
  onClose: () => void;
}

export function formatBytes(bytes?: number): string {
  if (!bytes || bytes <= 0) return '—';
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + sizes[i];
}

export function formatDuration(seconds?: number): string {
  if (!seconds || seconds <= 0) return '—';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return `${m}:${String(s).padStart(2, '0')}`;
}

export function getCodecCategory(vcodec?: string): { name: string; label: string; isCompatible: boolean; isEfficient: boolean; priority: number } {
  if (!vcodec || vcodec === 'none') {
    return { name: 'Unknown', label: '', isCompatible: false, isEfficient: false, priority: 99 };
  }
  const vc = vcodec.toLowerCase();
  if (vc.startsWith('avc1') || vc.startsWith('h264') || vc.includes('avc')) {
    return { name: 'H.264', label: 'Broadly compatible', isCompatible: true, isEfficient: false, priority: 1 };
  }
  if (vc.startsWith('vp9') || vc.includes('vp9') || vc.startsWith('vp8')) {
    return { name: 'VP9', label: 'Efficient', isCompatible: false, isEfficient: true, priority: 2 };
  }
  if (vc.startsWith('av01') || vc.startsWith('av1') || vc.includes('av1')) {
    return { name: 'AV1', label: 'Efficient', isCompatible: false, isEfficient: true, priority: 3 };
  }
  const baseName = vcodec.split('.')[0] || vcodec;
  return { name: baseName.toUpperCase(), label: 'Standard', isCompatible: false, isEfficient: false, priority: 4 };
}

export function parseResolutionHeight(res?: string, note?: string): number {
  if (!res && !note) return 0;
  const str = `${res || ''} ${note || ''}`.toLowerCase();
  if (str.includes('4k') || str.includes('2160p')) return 2160;
  if (str.includes('1440p') || str.includes('2k')) return 1440;
  if (str.includes('1080p')) return 1080;
  if (str.includes('720p')) return 720;
  if (str.includes('480p')) return 480;
  if (str.includes('360p')) return 360;
  if (str.includes('240p')) return 240;
  if (str.includes('144p')) return 144;

  const match = str.match(/(\d+)\s*x\s*(\d+)/i);
  if (match) {
    const w = parseInt(match[1], 10);
    const h = parseInt(match[2], 10);
    return Math.min(w, h);
  }

  const singleMatch = str.match(/(\d{3,4})p/i);
  if (singleMatch) {
    return parseInt(singleMatch[1], 10);
  }

  return 0;
}

export function parseFPS(fps?: number, note?: string): number {
  if (fps && fps > 0) return Math.round(fps);
  if (!note) return 0;
  const match = note.match(/(\d{2,3})\s*fps/i) || note.match(/p(\d{2,3})/i);
  if (match) return parseInt(match[1], 10);
  return 0;
}

export interface VideoQualityGroup {
  key: string;
  height: number;
  fps: number;
  heightLabel: string;
  fpsLabel: string;
  recommendedFormat: MediaFormat;
  alternativeFormats: MediaFormat[];
  allFormats: MediaFormat[];
}

export function groupVideoFormats(formats: MediaFormat[]): {
  groups: VideoQualityGroup[];
  bestVideoFormat: MediaFormat | null;
} {
  const videoFormats = formats.filter(f => f.vcodec && f.vcodec !== 'none');
  if (videoFormats.length === 0) {
    return { groups: [], bestVideoFormat: null };
  }

  const sortedOverall = [...videoFormats].sort((a, b) => {
    const ha = parseResolutionHeight(a.resolution, a.note);
    const hb = parseResolutionHeight(b.resolution, b.note);
    if (hb !== ha) return hb - ha;

    const fpsA = parseFPS(a.fps, a.note);
    const fpsB = parseFPS(b.fps, b.note);
    if (fpsB !== fpsA) return fpsB - fpsA;

    const ca = getCodecCategory(a.vcodec).priority;
    const cb = getCodecCategory(b.vcodec).priority;
    if (ca !== cb) return ca - cb;

    if ((b.fileSize || 0) !== (a.fileSize || 0)) return (b.fileSize || 0) - (a.fileSize || 0);
    return a.formatId.localeCompare(b.formatId);
  });

  const bestVideoFormat = sortedOverall[0] || null;

  const groupsMap = new Map<string, { height: number; fps: number; formats: MediaFormat[] }>();

  for (const f of videoFormats) {
    const height = parseResolutionHeight(f.resolution, f.note);
    const fps = parseFPS(f.fps, f.note);
    const heightLabel = height > 0 ? `${height}p` : (f.quality || f.note || 'Video');
    const fpsLabel = fps >= 45 ? `${fps} FPS` : '';
    const key = `${heightLabel}${fpsLabel ? ' ' + fpsLabel : ''}`;

    if (!groupsMap.has(key)) {
      groupsMap.set(key, { height, fps, formats: [] });
    }
    groupsMap.get(key)!.formats.push(f);
  }

  const groups: VideoQualityGroup[] = [];

  for (const [key, group] of groupsMap.entries()) {
    const sorted = [...group.formats].sort((a, b) => {
      const ca = getCodecCategory(a.vcodec).priority;
      const cb = getCodecCategory(b.vcodec).priority;
      if (ca !== cb) return ca - cb;

      if ((b.fileSize || 0) !== (a.fileSize || 0)) return (b.fileSize || 0) - (a.fileSize || 0);
      return a.formatId.localeCompare(b.formatId);
    });

    const heightLabel = group.height > 0 ? `${group.height}p` : key;
    const fpsLabel = group.fps >= 45 ? `${group.fps} FPS` : '';

    groups.push({
      key,
      height: group.height,
      fps: group.fps,
      heightLabel,
      fpsLabel,
      recommendedFormat: sorted[0],
      alternativeFormats: sorted.slice(1),
      allFormats: sorted,
    });
  }

  groups.sort((a, b) => {
    if (b.height !== a.height) return b.height - a.height;
    if (b.fps !== a.fps) return b.fps - a.fps;
    return a.key.localeCompare(b.key);
  });

  return { groups, bestVideoFormat };
}

export function groupAudioFormats(formats: MediaFormat[]): {
  audioItems: MediaFormat[];
  bestAudioFormat: MediaFormat | null;
} {
  const audioFormats = formats.filter(
    f => !f.vcodec || f.vcodec === 'none' || f.quality === 'audio only' || f.resolution === 'audio only'
  );

  if (audioFormats.length === 0) {
    return { audioItems: [], bestAudioFormat: null };
  }

  const getBitrate = (f: MediaFormat): number => {
    const str = `${f.note || ''} ${f.quality || ''}`.toLowerCase();
    const match = str.match(/(\d+)\s*k/i);
    if (match) return parseInt(match[1], 10);
    return 0;
  };

  const sorted = [...audioFormats].sort((a, b) => {
    const brA = getBitrate(a);
    const brB = getBitrate(b);
    if (brB !== brA) return brB - brA;
    if ((b.fileSize || 0) !== (a.fileSize || 0)) return (b.fileSize || 0) - (a.fileSize || 0);
    return a.formatId.localeCompare(b.formatId);
  });

  return {
    audioItems: sorted,
    bestAudioFormat: sorted[0] || null,
  };
}

export const FormatSelector: React.FC<FormatSelectorProps> = ({ job, onSelect, onClose }) => {
  const mediaInfo = job.mediaInfo;
  const modalId = useId();
  const modalTitleId = `format-modal-title-${modalId}`;
  const videoTabId = `tab-video-${modalId}`;
  const audioTabId = `tab-audio-${modalId}`;
  const videoPanelId = `panel-video-${modalId}`;
  const audioPanelId = `panel-audio-${modalId}`;

  const modalRef = useRef<HTMLDivElement>(null);
  const prevFocusRef = useRef<HTMLElement | null>(null);

  const formats = mediaInfo?.formats || [];
  const { groups: videoGroups, bestVideoFormat } = groupVideoFormats(formats);
  const { audioItems, bestAudioFormat } = groupAudioFormats(formats);

  const hasVideo = videoGroups.length > 0 || !!bestVideoFormat;
  const hasAudio = audioItems.length > 0 || !!bestAudioFormat;

  const [activeTab, setActiveTab] = useState<'video' | 'audio'>(hasVideo ? 'video' : 'audio');
  const [selectedFormatId, setSelectedFormatId] = useState<string>(() => {
    if (hasVideo && bestVideoFormat) return bestVideoFormat.formatId;
    if (hasAudio && bestAudioFormat) return bestAudioFormat.formatId;
    return formats[0]?.formatId || '';
  });

  const [expandedCodecs, setExpandedCodecs] = useState<Record<string, boolean>>({});
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Store previously focused element and focus modal on mount
  useEffect(() => {
    prevFocusRef.current = document.activeElement as HTMLElement;
    modalRef.current?.focus();
    return () => {
      prevFocusRef.current?.focus();
    };
  }, []);

  // Keyboard escape handler
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !isSubmitting) {
        onClose();
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose, isSubmitting]);

  if (!mediaInfo) return null;

  const handleTabChange = (tab: 'video' | 'audio') => {
    if (isSubmitting) return;
    setActiveTab(tab);
    setError(null);
    if (tab === 'video' && bestVideoFormat) {
      setSelectedFormatId(bestVideoFormat.formatId);
    } else if (tab === 'audio' && bestAudioFormat) {
      setSelectedFormatId(bestAudioFormat.formatId);
    }
  };

  const handleSelectFormat = (formatId: string) => {
    if (isSubmitting) return;
    setSelectedFormatId(formatId);
    setError(null);
  };

  const toggleCodecExpansion = (groupKey: string, e: React.MouseEvent) => {
    e.stopPropagation();
    e.preventDefault();
    setExpandedCodecs(prev => ({ ...prev, [groupKey]: !prev[groupKey] }));
  };

  const handleSubmit = async () => {
    if (!selectedFormatId || isSubmitting) return;
    try {
      setIsSubmitting(true);
      setError(null);
      await onSelect(job.id, selectedFormatId);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to select format');
      setIsSubmitting(false);
    }
  };

  // Find currently selected format metadata for footer summary
  const selectedFormat = formats.find(f => f.formatId === selectedFormatId);
  const isBestVideoSelected = activeTab === 'video' && bestVideoFormat && selectedFormatId === bestVideoFormat.formatId;
  const isBestAudioSelected = activeTab === 'audio' && bestAudioFormat && selectedFormatId === bestAudioFormat.formatId;

  const getFooterSummary = () => {
    if (!selectedFormatId) return 'No selection';
    if (isBestVideoSelected) {
      return 'Selected: Best available video · Best available audio included';
    }
    if (isBestAudioSelected) {
      return 'Selected: Best available audio';
    }
    if (!selectedFormat) return 'Selected format';

    if (activeTab === 'video') {
      const height = parseResolutionHeight(selectedFormat.resolution, selectedFormat.note);
      const fps = parseFPS(selectedFormat.fps, selectedFormat.note);
      const codecInfo = getCodecCategory(selectedFormat.vcodec);
      const label = height > 0 ? `${height}p` : (selectedFormat.quality || 'Video');
      const fpsStr = fps >= 45 ? ` ${fps} FPS` : '';
      const sizeStr = formatBytes(selectedFormat.fileSize);
      return `Selected: ${label}${fpsStr} (${codecInfo.name}) · ${sizeStr} · Best audio included`;
    } else {
      const codecInfo = getCodecCategory(selectedFormat.acodec || selectedFormat.vcodec);
      const ext = selectedFormat.ext ? `.${selectedFormat.ext}` : '';
      const sizeStr = formatBytes(selectedFormat.fileSize);
      return `Selected: Audio (${codecInfo.name || 'Audio'} ${ext}) · ${sizeStr}`;
    }
  };

  return (
    <div
      className="format-overlay"
      onClick={() => { if (!isSubmitting) onClose(); }}
      data-testid="format-modal-overlay"
    >
      <div
        ref={modalRef}
        className="format-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={modalTitleId}
        tabIndex={-1}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Header */}
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
              <h3 id={modalTitleId} className="format-media-title">
                {mediaInfo.title}
              </h3>
              {mediaInfo.duration > 0 && (
                <span className="format-duration">
                  Duration: {formatDuration(mediaInfo.duration)}
                </span>
              )}
            </div>
          </div>
          <button
            type="button"
            className="btn-dismiss format-close"
            onClick={onClose}
            disabled={isSubmitting}
            aria-label="Close"
          >
            <X size={18} />
          </button>
        </div>

        {/* Tab Navigation */}
        <div className="format-tabs" role="tablist" aria-label="Media Quality Tabs">
          <button
            id={videoTabId}
            type="button"
            role="tab"
            aria-selected={activeTab === 'video'}
            aria-controls={videoPanelId}
            className={`format-tab ${activeTab === 'video' ? 'active' : ''}`}
            onClick={() => handleTabChange('video')}
            disabled={isSubmitting}
          >
            <Video size={16} />
            <span>Video</span>
          </button>
          <button
            id={audioTabId}
            type="button"
            role="tab"
            aria-selected={activeTab === 'audio'}
            aria-controls={audioPanelId}
            className={`format-tab ${activeTab === 'audio' ? 'active' : ''}`}
            onClick={() => handleTabChange('audio')}
            disabled={isSubmitting}
          >
            <Music size={16} />
            <span>Audio</span>
          </button>
        </div>

        {/* Body */}
        <div className="format-body scrollbar-thin">
          {error && (
            <div className="format-error" role="alert" data-testid="format-error-banner">
              <Info size={16} />
              <span>{error}</span>
            </div>
          )}

          {/* VIDEO TAB PANEL */}
          {activeTab === 'video' && (
            <div id={videoPanelId} role="tabpanel" aria-labelledby={videoTabId} tabIndex={0} className="format-panel">
              {bestVideoFormat && (
                <div
                  className={`format-card format-card-recommended ${selectedFormatId === bestVideoFormat.formatId ? 'selected' : ''}`}
                  role="button"
                  tabIndex={0}
                  aria-pressed={selectedFormatId === bestVideoFormat.formatId}
                  onClick={() => handleSelectFormat(bestVideoFormat.formatId)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      handleSelectFormat(bestVideoFormat.formatId);
                    }
                  }}
                  data-testid="best-video-card"
                >
                  <div className="format-card-top">
                    <div className="format-card-title-wrap">
                      <span className="format-badge format-badge-recommended">
                        <Sparkles size={12} /> Best Available
                      </span>
                      <h4 className="format-card-title">Best Available Video</h4>
                    </div>
                    <span className="format-card-size">{formatBytes(bestVideoFormat.fileSize)}</span>
                  </div>
                  <p className="format-card-sub">Automatically downloads the highest resolution and frame rate available.</p>
                  <div className="format-card-audio-notice">
                    <Volume2 size={13} />
                    <span>Best available audio included</span>
                  </div>
                </div>
              )}

              {videoGroups.length > 0 ? (
                <div className="format-groups-list">
                  {videoGroups.map(group => {
                    const rec = group.recommendedFormat;
                    const isGroupSelected = group.allFormats.some(f => f.formatId === selectedFormatId);
                    const activeFormat = group.allFormats.find(f => f.formatId === selectedFormatId) || rec;
                    const codecInfo = getCodecCategory(activeFormat.vcodec);
                    const isExpanded = !!expandedCodecs[group.key];

                    return (
                      <div
                        key={group.key}
                        className={`format-card ${isGroupSelected ? 'selected' : ''}`}
                        role="button"
                        tabIndex={0}
                        aria-pressed={isGroupSelected}
                        onClick={() => handleSelectFormat(activeFormat.formatId)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            handleSelectFormat(activeFormat.formatId);
                          }
                        }}
                        data-testid={`video-card-${group.key}`}
                      >
                        <div className="format-card-top">
                          <div className="format-card-title-wrap">
                            <h4 className="format-card-title">
                              {group.heightLabel}
                              {group.fpsLabel && <span className="format-fps-tag"> {group.fpsLabel}</span>}
                            </h4>
                            {codecInfo.label && (
                              <span className={`format-badge ${codecInfo.isCompatible ? 'badge-compatible' : 'badge-efficient'}`}>
                                {codecInfo.isCompatible ? <ShieldCheck size={12} /> : <Zap size={12} />}
                                {codecInfo.label}
                              </span>
                            )}
                          </div>
                          <span className="format-card-size">{formatBytes(activeFormat.fileSize)}</span>
                        </div>

                        <div className="format-card-details-row">
                          <span className="format-detail-pill">Codec: {codecInfo.name}</span>
                          {activeFormat.ext && <span className="format-detail-pill">.{activeFormat.ext}</span>}
                          <span className="format-card-audio-notice">
                            <Volume2 size={13} />
                            Best available audio included
                          </span>
                        </div>

                        {group.alternativeFormats.length > 0 && (
                          <div className="format-codec-options-wrap">
                            <button
                              type="button"
                              className="btn-codec-toggle"
                              onClick={(e) => toggleCodecExpansion(group.key, e)}
                              aria-expanded={isExpanded}
                              data-testid={`codec-options-toggle-${group.key}`}
                            >
                              <span>Codec options ({group.allFormats.length})</span>
                              {isExpanded ? <ChevronUp size={14} /> : <ChevronDown size={14} />}
                            </button>

                            {isExpanded && (
                              <div className="format-codec-options-list" onClick={(e) => e.stopPropagation()}>
                                {group.allFormats.map(alt => {
                                  const altCodec = getCodecCategory(alt.vcodec);
                                  const isAltSelected = selectedFormatId === alt.formatId;
                                  return (
                                    <div
                                      key={alt.formatId}
                                      className={`format-codec-opt-item ${isAltSelected ? 'selected' : ''}`}
                                      role="button"
                                      tabIndex={0}
                                      onClick={() => handleSelectFormat(alt.formatId)}
                                      onKeyDown={(e) => {
                                        if (e.key === 'Enter' || e.key === ' ') {
                                          e.preventDefault();
                                          handleSelectFormat(alt.formatId);
                                        }
                                      }}
                                      data-testid={`alt-codec-${alt.formatId}`}
                                    >
                                      <div className="format-codec-opt-info">
                                        <span className="format-codec-name">{altCodec.name}</span>
                                        {altCodec.label && (
                                          <span className="format-codec-tag">{altCodec.label}</span>
                                        )}
                                      </div>
                                      <div className="format-codec-opt-meta">
                                        <span>{alt.ext ? `.${alt.ext}` : ''}</span>
                                        <span>{formatBytes(alt.fileSize)}</span>
                                        {isAltSelected && <Check size={14} className="text-primary" />}
                                      </div>
                                    </div>
                                  );
                                })}
                              </div>
                            )}
                          </div>
                        )}
                      </div>
                    );
                  })}
                </div>
              ) : (
                !bestVideoFormat && <p className="empty-message">No video formats available</p>
              )}
            </div>
          )}

          {/* AUDIO TAB PANEL */}
          {activeTab === 'audio' && (
            <div id={audioPanelId} role="tabpanel" aria-labelledby={audioTabId} tabIndex={0} className="format-panel">
              {bestAudioFormat && (
                <div
                  className={`format-card format-card-recommended ${selectedFormatId === bestAudioFormat.formatId ? 'selected' : ''}`}
                  role="button"
                  tabIndex={0}
                  aria-pressed={selectedFormatId === bestAudioFormat.formatId}
                  onClick={() => handleSelectFormat(bestAudioFormat.formatId)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') {
                      e.preventDefault();
                      handleSelectFormat(bestAudioFormat.formatId);
                    }
                  }}
                  data-testid="best-audio-card"
                >
                  <div className="format-card-top">
                    <div className="format-card-title-wrap">
                      <span className="format-badge format-badge-recommended">
                        <Sparkles size={12} /> Best Available
                      </span>
                      <h4 className="format-card-title">Best Available Audio</h4>
                    </div>
                    <span className="format-card-size">{formatBytes(bestAudioFormat.fileSize)}</span>
                  </div>
                  <p className="format-card-sub">Extracts and downloads the highest quality audio stream available.</p>
                </div>
              )}

              {audioItems.length > 0 ? (
                <div className="format-audio-list">
                  {audioItems.map(item => {
                    const isSelected = selectedFormatId === item.formatId;
                    const codecInfo = getCodecCategory(item.acodec || item.vcodec);
                    const extStr = item.ext ? `.${item.ext}` : '';
                    const qualityStr = item.note || item.quality || 'Audio';

                    return (
                      <div
                        key={item.formatId}
                        className={`format-card ${isSelected ? 'selected' : ''}`}
                        role="button"
                        tabIndex={0}
                        aria-pressed={isSelected}
                        onClick={() => handleSelectFormat(item.formatId)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault();
                            handleSelectFormat(item.formatId);
                          }
                        }}
                        data-testid={`audio-card-${item.formatId}`}
                      >
                        <div className="format-card-top">
                          <div className="format-card-title-wrap">
                            <h4 className="format-card-title">{qualityStr}</h4>
                          </div>
                          <span className="format-card-size">{formatBytes(item.fileSize)}</span>
                        </div>
                        <div className="format-card-details-row">
                          <span className="format-detail-pill">Codec: {codecInfo.name}</span>
                          {extStr && <span className="format-detail-pill">{extStr}</span>}
                          {item.formatId && <span className="format-detail-pill muted">ID: {item.formatId}</span>}
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                !bestAudioFormat && <p className="empty-message">No audio formats available</p>
              )}
            </div>
          )}
        </div>

        {/* Sticky Footer */}
        <div className="format-footer">
          <div className="format-footer-summary" data-testid="format-footer-summary">
            <span>{getFooterSummary()}</span>
          </div>
          <div className="format-footer-actions">
            <button
              type="button"
              className="btn btn-secondary"
              onClick={onClose}
              disabled={isSubmitting}
            >
              Cancel
            </button>
            <button
              type="button"
              className="btn btn-primary"
              onClick={handleSubmit}
              disabled={!selectedFormatId || isSubmitting}
              data-testid="format-confirm-button"
            >
              {isSubmitting ? (
                <>
                  <Loader2 size={16} className="spin icon-spin" />
                  <span>Starting…</span>
                </>
              ) : (
                <>
                  <Download size={16} />
                  <span>{activeTab === 'video' ? 'Download Video' : 'Download Audio'}</span>
                </>
              )}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
};
