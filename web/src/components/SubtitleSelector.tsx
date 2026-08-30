import { useState, useEffect } from 'react';
import type {
  SubtitleCapabilities,
  SubtitleOptions,
  SubtitleMode,
  SubtitleFormat,
  SubtitleTrack,
} from '../types';

export interface SubtitleSelectorProps {
  subtitles?: SubtitleCapabilities;
  onChange: (options?: SubtitleOptions) => void;
  disabled?: boolean;
}

export function SubtitleSelector({
  subtitles,
  onChange,
  disabled = false,
}: SubtitleSelectorProps) {
  const [selectedLanguages, setSelectedLanguages] = useState<string[]>([]);
  const [includeAuto, setIncludeAuto] = useState<boolean>(false);
  const [englishTranslation, setEnglishTranslation] = useState<boolean>(false);
  const [mode, setMode] = useState<SubtitleMode>('separate');
  const [format, setFormat] = useState<SubtitleFormat>('srt');

  const tracks = subtitles?.tracks || [];
  const englishTranslationAvailable = !!subtitles?.englishTranslationAvailable;
  const hasSubtitles = tracks.length > 0 || englishTranslationAvailable;
  const hasSelection = selectedLanguages.length > 0 || englishTranslation;

  const availableTracks = tracks.filter((t) => t.manual);
  const autoOnlyTracks = tracks.filter((t) => !t.manual && t.auto);
  const hasAutoTracks = autoOnlyTracks.length > 0 || tracks.some((t) => t.auto);

  // Notify parent whenever options change
  useEffect(() => {
    if (!hasSelection) {
      onChange(undefined);
      return;
    }

    onChange({
      languages: selectedLanguages,
      includeAuto,
      englishTranslation,
      mode,
      format,
    });
  }, [selectedLanguages, includeAuto, englishTranslation, mode, format, hasSelection, onChange]);

  if (!hasSubtitles) {
    return (
      <div className="space-y-2 pt-4 border-t border-border/40">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Subtitles
        </h4>
        <p className="text-xs text-muted-foreground">No subtitles available</p>
      </div>
    );
  }

  const handleToggleIncludeAuto = (checked: boolean) => {
    if (disabled) return;
    setIncludeAuto(checked);
    if (!checked) {
      // Clear any selected auto-only tracks automatically
      setSelectedLanguages((prev) =>
        prev.filter((lang) => {
          const track = tracks.find((t) => t.language === lang);
          return track?.manual;
        })
      );
    }
  };

  const handleToggleLanguage = (lang: string) => {
    if (disabled) return;
    setSelectedLanguages((prev) => {
      if (prev.includes(lang)) {
        return prev.filter((l) => l !== lang);
      }
      const track = tracks.find((t) => t.language === lang);
      if (track && track.auto && !track.manual) {
        setIncludeAuto(true);
      }
      return [...prev, lang];
    });
  };

  const getTrackBadgeLabel = (manual: boolean, auto: boolean): string => {
    if (manual && auto) return 'Manual · Auto available';
    if (manual) return 'Manual';
    if (auto) return 'Auto-generated';
    return 'Available';
  };

  const renderTrackCard = (track: SubtitleTrack) => {
    const isChecked = selectedLanguages.includes(track.language);
    const badge = getTrackBadgeLabel(track.manual, track.auto);
    return (
      <label
        key={track.language}
        className={`flex items-center justify-between p-2 rounded-md border text-xs cursor-pointer transition-colors ${
          isChecked
            ? 'border-primary/50 bg-primary/10 text-foreground'
            : 'border-border/40 bg-card hover:bg-accent/40 text-foreground/80'
        } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
      >
        <div className="flex items-center space-x-2 truncate pr-2">
          <input
            type="checkbox"
            checked={isChecked}
            onChange={() => handleToggleLanguage(track.language)}
            disabled={disabled}
            className="rounded border-border text-primary focus:ring-primary h-3.5 w-3.5"
          />
          <span className="font-medium truncate">
            {track.name ? `${track.name} (${track.language})` : track.language}
          </span>
        </div>
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted text-muted-foreground shrink-0">
          {badge}
        </span>
      </label>
    );
  };

  return (
    <div className="space-y-4 pt-4 border-t border-border/40" data-testid="subtitle-selector">
      <div className="flex items-center justify-between">
        <h4 className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          Subtitles
        </h4>
        {hasSelection && (
          <button
            type="button"
            onClick={() => {
              setSelectedLanguages([]);
              setEnglishTranslation(false);
            }}
            className="text-xs text-primary hover:underline"
            disabled={disabled}
          >
            Clear subtitle selection
          </button>
        )}
      </div>

      {/* Available manual/default tracks list */}
      {availableTracks.length > 0 && (
        <div className="space-y-2">
          <div className="text-xs font-medium text-foreground/80">Available subtitles</div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 max-h-48 overflow-y-auto pr-1">
            {availableTracks.map(renderTrackCard)}
          </div>
        </div>
      )}

      {/* Auto captions toggle */}
      {hasAutoTracks && (
        <div className="pt-1">
          <label className="flex items-center space-x-2 text-xs text-muted-foreground cursor-pointer">
            <input
              type="checkbox"
              checked={includeAuto}
              onChange={(e) => handleToggleIncludeAuto(e.target.checked)}
              disabled={disabled}
              className="rounded border-border text-primary focus:ring-primary h-3.5 w-3.5"
            />
            <span>Allow auto-generated subtitles</span>
          </label>
        </div>
      )}

      {/* Auto-generated tracks list (only shown when includeAuto is enabled) */}
      {includeAuto && autoOnlyTracks.length > 0 && (
        <div className="space-y-2">
          <div className="text-xs font-medium text-foreground/80">Auto-generated subtitles</div>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 max-h-48 overflow-y-auto pr-1">
            {autoOnlyTracks.map(renderTrackCard)}
          </div>
        </div>
      )}

      {/* English Translation */}
      {englishTranslationAvailable && (
        <div className="space-y-1.5 pt-1">
          <div className="text-xs font-medium text-foreground/80">Additional</div>
          <label
            className={`flex items-center space-x-2 p-2 rounded-md border text-xs cursor-pointer transition-colors ${
              englishTranslation
                ? 'border-primary/50 bg-primary/10 text-foreground'
                : 'border-border/40 bg-card hover:bg-accent/40 text-foreground/80'
            } ${disabled ? 'opacity-50 cursor-not-allowed' : ''}`}
          >
            <input
              type="checkbox"
              checked={englishTranslation}
              onChange={(e) => setEnglishTranslation(e.target.checked)}
              disabled={disabled}
              className="rounded border-border text-primary focus:ring-primary h-3.5 w-3.5"
            />
            <span className="font-medium">Add English translation</span>
          </label>
        </div>
      )}

      {/* Format and Output controls (shown when at least one selection is active) */}
      {hasSelection && (
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 pt-2 border-t border-border/30 animate-in fade-in duration-200">
          {/* Format */}
          <div className="space-y-1.5">
            <div className="text-xs font-medium text-foreground/80">Format</div>
            <div className="flex space-x-3 text-xs">
              <label className="flex items-center space-x-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="subFormat"
                  value="srt"
                  checked={format === 'srt'}
                  onChange={() => setFormat('srt')}
                  disabled={disabled}
                  className="text-primary focus:ring-primary"
                />
                <span>SRT</span>
              </label>
              <label className="flex items-center space-x-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="subFormat"
                  value="vtt"
                  checked={format === 'vtt'}
                  onChange={() => setFormat('vtt')}
                  disabled={disabled}
                  className="text-primary focus:ring-primary"
                />
                <span>VTT</span>
              </label>
            </div>
          </div>

          {/* Output */}
          <div className="space-y-1.5">
            <div className="text-xs font-medium text-foreground/80">Output</div>
            <div className="flex space-x-3 text-xs">
              <label className="flex items-center space-x-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="subMode"
                  value="separate"
                  checked={mode === 'separate'}
                  onChange={() => setMode('separate')}
                  disabled={disabled}
                  className="text-primary focus:ring-primary"
                />
                <span>Separate</span>
              </label>
              <label className="flex items-center space-x-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="subMode"
                  value="embed"
                  checked={mode === 'embed'}
                  onChange={() => setMode('embed')}
                  disabled={disabled}
                  className="text-primary focus:ring-primary"
                />
                <span>Embed</span>
              </label>
              <label className="flex items-center space-x-1.5 cursor-pointer">
                <input
                  type="radio"
                  name="subMode"
                  value="both"
                  checked={mode === 'both'}
                  onChange={() => setMode('both')}
                  disabled={disabled}
                  className="text-primary focus:ring-primary"
                />
                <span>Both</span>
              </label>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
