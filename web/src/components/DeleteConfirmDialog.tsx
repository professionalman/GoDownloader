import { useState } from 'react';
import { LoaderCircle, Trash2, X } from 'lucide-react';
import type { Job } from '../types';

interface DeleteConfirmDialogProps {
  job: Job;
  onConfirm: (jobId: string, deleteFiles: boolean) => Promise<void>;
  onClose: () => void;
}

export function DeleteConfirmDialog({
  job,
  onConfirm,
  onClose,
}: DeleteConfirmDialogProps) {
  const [deleteFiles, setDeleteFiles] = useState(false);
  const [isDeleting, setIsDeleting] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isDeleting) return;

    setIsDeleting(true);
    setError('');
    try {
      await onConfirm(job.id, deleteFiles);
      onClose();
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : 'Failed to delete download';
      setError(msg);
      setIsDeleting(false);
    }
  };

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4 backdrop-blur-xs"
      role="dialog"
      aria-modal="true"
      aria-labelledby="delete-dialog-title"
    >
      <div className="w-full max-w-md rounded-xl border border-border bg-surface-1 p-6 shadow-2xl">
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-2 text-destructive">
            <Trash2 className="size-5" />
            <h2 id="delete-dialog-title" className="text-lg font-semibold text-foreground">
              Delete download?
            </h2>
          </div>
          <button
            type="button"
            className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground"
            onClick={onClose}
            disabled={isDeleting}
            aria-label="Close dialog"
          >
            <X className="size-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="mt-4 space-y-4">
          <p className="text-sm text-muted-foreground">
            This will remove this download from GoDownloader.
          </p>

          <label className="flex cursor-pointer items-center gap-2.5 text-sm text-foreground select-none">
            <input
              type="checkbox"
              className="size-4 rounded border-border text-primary focus:ring-primary"
              checked={deleteFiles}
              onChange={(e) => setDeleteFiles(e.target.checked)}
              disabled={isDeleting}
            />
            <span>Also delete files from storage</span>
          </label>

          {error && (
            <div
              className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive"
              role="alert"
            >
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              className="rounded-md border border-border bg-surface-2 px-4 py-2 text-xs font-medium text-foreground hover:bg-surface focus-visible:outline-none"
              onClick={onClose}
              disabled={isDeleting}
            >
              Cancel
            </button>
            <button
              type="submit"
              className="flex items-center gap-1.5 rounded-md bg-destructive px-4 py-2 text-xs font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50 focus-visible:outline-none"
              disabled={isDeleting}
            >
              {isDeleting && <LoaderCircle className="size-3.5 animate-spin" />}
              <span>Delete</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
