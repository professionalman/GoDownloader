import { useRef } from 'react';
import { Plus, Paperclip, Layers, SlidersHorizontal, Settings2 } from 'lucide-react';

interface SourceComposerProps {
  inputText: string;
  onInputTextChange: (text: string) => void;
  isBatchMode: boolean;
  onToggleBatchMode: () => void;
  showBasicOptions: boolean;
  onToggleBasicOptions: () => void;
  showAdvancedOptions: boolean;
  onToggleAdvancedOptions: () => void;
  sourcesCount: number;
  onUploadTorrentFile: (file: File) => void;
  onSubmit: (e: React.FormEvent) => void;
  isSubmitting: boolean;
  disabled?: boolean;
}

export function SourceComposer({
  inputText,
  onInputTextChange,
  isBatchMode,
  onToggleBatchMode,
  showBasicOptions,
  onToggleBasicOptions,
  showAdvancedOptions,
  onToggleAdvancedOptions,
  sourcesCount,
  onUploadTorrentFile,
  onSubmit,
  isSubmitting,
  disabled,
}: SourceComposerProps) {
  const fileInputRef = useRef<HTMLInputElement>(null);
  const overLimit = sourcesCount > 100;
  const isFormDisabled = disabled || isSubmitting;

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) {
      onUploadTorrentFile(file);
    }
  };

  return (
    <div className="space-y-2">
      <div className="flex flex-wrap items-start gap-2">
        <div className="min-w-0 flex-1 basis-full sm:basis-0">
          <label htmlFor="download-sources" className="sr-only">
            Download source
          </label>
          {isBatchMode ? (
            <textarea
              id="download-sources"
              rows={3}
              value={inputText}
              onChange={(e) => onInputTextChange(e.target.value)}
              placeholder="Paste download URLs or magnet links — one per line"
              disabled={isFormDisabled}
              className="min-h-[5rem] w-full resize-y rounded-md border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-foreground outline-none focus:border-primary disabled:opacity-50"
            />
          ) : (
            <input
              id="download-sources"
              type="text"
              value={inputText}
              onChange={(e) => onInputTextChange(e.target.value)}
              placeholder="Paste a URL or magnet link"
              disabled={isFormDisabled}
              className="h-9 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground outline-none focus:border-primary disabled:opacity-50"
            />
          )}
        </div>

        <div className="flex flex-1 flex-wrap gap-1.5 sm:flex-none sm:flex-nowrap">
          <input
            ref={fileInputRef}
            type="file"
            accept=".torrent"
            onChange={handleFileChange}
            className="sr-only"
          />
          <button
            type="button"
            title="Add .torrent file"
            aria-label="Add torrent file"
            disabled={isFormDisabled}
            onClick={() => fileInputRef.current?.click()}
            className="grid size-9 shrink-0 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground hover:text-foreground disabled:opacity-50"
          >
            <Paperclip className="size-4" />
          </button>

          <button
            type="button"
            title={isBatchMode ? 'Switch to single source mode' : 'Switch to batch mode'}
            aria-pressed={isBatchMode}
            disabled={isFormDisabled}
            onClick={onToggleBatchMode}
            className={`grid size-9 shrink-0 place-items-center rounded-md border text-xs transition-colors ${
              isBatchMode
                ? 'border-primary/40 bg-primary/15 text-foreground'
                : 'border-border bg-surface-2 text-muted-foreground hover:text-foreground'
            }`}
          >
            <Layers className="size-4" />
          </button>

          <button
            type="button"
            title="Toggle download options"
            aria-pressed={showBasicOptions}
            aria-expanded={showBasicOptions}
            disabled={isFormDisabled}
            onClick={onToggleBasicOptions}
            className={`grid size-9 shrink-0 place-items-center rounded-md border text-xs transition-colors ${
              showBasicOptions
                ? 'border-primary/40 bg-primary/15 text-foreground'
                : 'border-border bg-surface-2 text-muted-foreground hover:text-foreground'
            }`}
          >
            <SlidersHorizontal className="size-4" />
          </button>

          <button
            type="submit"
            disabled={isFormDisabled || sourcesCount === 0 || overLimit}
            onClick={onSubmit}
            className="flex h-9 flex-1 items-center justify-center gap-1.5 rounded-md bg-primary px-3.5 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50 sm:flex-none"
          >
            <Plus className="size-4" />
            <span>{isSubmitting ? 'Starting…' : 'Start'}</span>
          </button>

          <button
            type="button"
            aria-expanded={showAdvancedOptions}
            aria-label={showAdvancedOptions ? 'Collapse advanced options' : 'Advanced options'}
            disabled={isFormDisabled}
            onClick={onToggleAdvancedOptions}
            className="grid size-9 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground"
          >
            <Settings2 className="size-4" />
          </button>
        </div>
      </div>

      {isBatchMode && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
          <span>One source per line</span>
          <span aria-hidden="true">·</span>
          <span className={`num ${overLimit ? 'font-semibold text-destructive' : ''}`}>
            {sourcesCount} of 100 sources
          </span>
          {overLimit && <span className="text-destructive font-medium">(Maximum 100 per batch)</span>}
        </div>
      )}
    </div>
  );
}
