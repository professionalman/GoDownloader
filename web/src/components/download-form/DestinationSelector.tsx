import type { Category } from '../../types';
import type { DestinationMode } from './downloadPolicy';

interface DestinationSelectorProps {
  mode: DestinationMode;
  onModeChange: (mode: DestinationMode) => void;
  categories: Category[];
  selectedCategoryId: string;
  onCategoryChange: (id: string) => void;
  customDestDir: string;
  onCustomDestDirChange: (path: string) => void;
  disabled?: boolean;
}

export function DestinationSelector({
  mode,
  onModeChange,
  categories,
  selectedCategoryId,
  onCategoryChange,
  customDestDir,
  onCustomDestDirChange,
  disabled,
}: DestinationSelectorProps) {
  return (
    <div className="space-y-1.5">
      <label className="text-xs font-medium text-muted-foreground">
        Destination
      </label>
      <div className="flex flex-wrap gap-1" role="group" aria-label="Destination mode">
        <button
          type="button"
          aria-pressed={mode === 'default'}
          disabled={disabled}
          onClick={() => onModeChange('default')}
          className={`h-7 rounded-md border px-2.5 text-xs transition-colors ${
            mode === 'default'
              ? 'border-primary/40 bg-primary/15 text-foreground font-medium'
              : 'border-border bg-surface-2 text-muted-foreground hover:text-foreground'
          }`}
        >
          Default folder
        </button>

        <button
          type="button"
          aria-pressed={mode === 'category'}
          disabled={disabled}
          onClick={() => onModeChange('category')}
          className={`h-7 rounded-md border px-2.5 text-xs transition-colors ${
            mode === 'category'
              ? 'border-primary/40 bg-primary/15 text-foreground font-medium'
              : 'border-border bg-surface-2 text-muted-foreground hover:text-foreground'
          }`}
        >
          Category
        </button>

        <button
          type="button"
          aria-pressed={mode === 'custom'}
          disabled={disabled}
          onClick={() => onModeChange('custom')}
          className={`h-7 rounded-md border px-2.5 text-xs transition-colors ${
            mode === 'custom'
              ? 'border-primary/40 bg-primary/15 text-foreground font-medium'
              : 'border-border bg-surface-2 text-muted-foreground hover:text-foreground'
          }`}
        >
          Custom folder
        </button>
      </div>

      {mode === 'category' && (
        <div className="pt-1">
          <label htmlFor="category-select" className="sr-only">
            Select category
          </label>
          <select
            id="category-select"
            value={selectedCategoryId}
            onChange={(e) => onCategoryChange(e.target.value)}
            disabled={disabled}
            className="h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
          >
            <option value="">Choose category…</option>
            {categories.map((cat) => (
              <option key={cat.id} value={cat.id}>
                {cat.name} ({cat.directory})
              </option>
            ))}
          </select>
        </div>
      )}

      {mode === 'custom' && (
        <div className="pt-1">
          <label htmlFor="custom-dest-input" className="sr-only">
            Custom destination path
          </label>
          <input
            id="custom-dest-input"
            type="text"
            value={customDestDir}
            onChange={(e) => onCustomDestDirChange(e.target.value)}
            placeholder="e.g. C:\Downloads\MyFolder"
            disabled={disabled}
            className="h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
          />
        </div>
      )}
    </div>
  );
}
