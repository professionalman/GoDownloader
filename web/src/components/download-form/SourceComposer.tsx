import { useRef } from 'react';
import { Plus, Paperclip, ChevronDown } from 'lucide-react';
import { cx } from '../../downloadUi';

interface SourceComposerProps {
  inputText: string;
  onInputTextChange: (text: string) => void;
  isBatchMode: boolean;
  expanded: boolean;
  onToggleExpanded: () => void;
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
  expanded,
  onToggleExpanded,
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
    <div className="grid grid-cols-[auto_minmax(0,1fr)_auto] gap-1.5 sm:flex sm:items-center">
      <div className="col-span-3 min-w-0 sm:col-span-1 sm:flex-1">
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
            className="min-h-[5rem] w-full resize-y rounded-lg border border-border bg-surface-2 px-3 py-2 font-mono text-sm text-foreground outline-none transition focus:border-primary focus:ring-2 focus:ring-primary/20 disabled:opacity-50"
          />
        ) : (
          <input
            id="download-sources"
            type="text"
            value={inputText}
            onChange={(e) => onInputTextChange(e.target.value)}
            placeholder="Paste a URL or magnet link"
            disabled={isFormDisabled}
            className="h-10 w-full rounded-md border border-border bg-surface-2 px-3 font-mono text-sm text-foreground outline-none transition focus:border-primary focus:ring-2 focus:ring-primary/20 disabled:opacity-50"
          />
        )}
      </div>

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
        className="grid size-10 shrink-0 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground transition hover:border-border-strong hover:text-foreground disabled:opacity-50"
      >
        <Paperclip className="size-4.5" aria-hidden="true" />
      </button>

      <button
        type="submit"
        disabled={isFormDisabled || sourcesCount === 0 || overLimit}
        onClick={onSubmit}
        className="flex h-10 w-full shrink-0 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-semibold text-primary-foreground transition hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50 sm:w-auto"
      >
        <Plus className="size-4" aria-hidden="true" />
        <span>{isSubmitting ? 'Starting…' : 'Start'}</span>
      </button>

      <button
        type="button"
        aria-expanded={expanded}
        aria-label={expanded ? 'Hide download options' : 'Show download options'}
        title="Toggle download options"
        disabled={isFormDisabled}
        onClick={onToggleExpanded}
        className="grid size-10 shrink-0 place-items-center rounded-md border border-transparent text-muted-foreground transition hover:border-border hover:bg-surface-2 hover:text-foreground disabled:opacity-50"
      >
        <ChevronDown
          className={cx('size-4 transition-transform duration-200', expanded && 'rotate-180')}
          aria-hidden="true"
        />
      </button>
    </div>
  );
}
