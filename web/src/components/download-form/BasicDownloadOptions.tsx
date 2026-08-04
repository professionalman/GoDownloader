import type { JobPriority, Category, FilenameConflictPolicy } from '../../types';
import { DestinationSelector } from './DestinationSelector';
import type { DestinationMode } from './downloadPolicy';

interface BasicDownloadOptionsProps {
  priority: JobPriority;
  onPriorityChange: (p: JobPriority) => void;
  destinationMode: DestinationMode;
  onDestinationModeChange: (m: DestinationMode) => void;
  categories: Category[];
  selectedCategoryId: string;
  onCategoryChange: (id: string) => void;
  customDestDir: string;
  onCustomDestDirChange: (path: string) => void;
  conflictPolicy: FilenameConflictPolicy;
  onConflictPolicyChange: (cp: FilenameConflictPolicy) => void;
  disabled?: boolean;
}

export function BasicDownloadOptions({
  priority,
  onPriorityChange,
  destinationMode,
  onDestinationModeChange,
  categories,
  selectedCategoryId,
  onCategoryChange,
  customDestDir,
  onCustomDestDirChange,
  conflictPolicy,
  onConflictPolicyChange,
  disabled,
}: BasicDownloadOptionsProps) {
  return (
    <div className="mt-2.5 border-t border-border pt-2.5">
      <div className="grid gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <label htmlFor="job-priority-select" className="text-xs font-medium text-muted-foreground">
            Priority
          </label>
          <select
            id="job-priority-select"
            value={priority}
            onChange={(e) => onPriorityChange(e.target.value as JobPriority)}
            disabled={disabled}
            className="h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
          >
            <option value="high">High priority</option>
            <option value="normal">Normal priority</option>
            <option value="low">Low priority</option>
          </select>
        </div>

        <DestinationSelector
          mode={destinationMode}
          onModeChange={onDestinationModeChange}
          categories={categories}
          selectedCategoryId={selectedCategoryId}
          onCategoryChange={onCategoryChange}
          customDestDir={customDestDir}
          onCustomDestDirChange={onCustomDestDirChange}
          disabled={disabled}
        />

        <div className="space-y-1">
          <label htmlFor="conflict-policy-select" className="text-xs font-medium text-muted-foreground">
            Filename collision
          </label>
          <select
            id="conflict-policy-select"
            value={conflictPolicy}
            onChange={(e) => onConflictPolicyChange(e.target.value as FilenameConflictPolicy)}
            disabled={disabled}
            className="h-8 w-full rounded-md border border-border bg-surface-2 px-3 text-xs text-foreground outline-none focus:border-primary disabled:opacity-50"
          >
            <option value="rename">Rename automatically</option>
            <option value="overwrite">Overwrite existing</option>
            <option value="fail">Fail if file exists</option>
          </select>
        </div>
      </div>
    </div>
  );
}
