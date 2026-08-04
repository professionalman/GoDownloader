import { useState, useRef, useEffect } from 'react';
import { Check, Copy, MoreVertical } from 'lucide-react';
import type { Job } from '../../types';

interface JobActionsMenuProps {
  job: Job;
  detailsOpen: boolean;
  onToggleDetails: () => void;
  onCancel?: (id: string) => void;
  onRetry?: (id: string) => void;
  onOpenFolder?: () => void;
  onAction: (actionFn?: (id: string) => void) => void;
}

export function JobActionsMenu({
  job,
  detailsOpen,
  onToggleDetails,
  onCancel,
  onRetry,
  onOpenFolder,
  onAction,
}: JobActionsMenuProps) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);

  const isCompleted = job.status === 'completed';
  const isCancelled = job.status === 'cancelled';
  const isFailed = job.status === 'failed';
  const isSeeding = job.status === 'seeding';

  // Handle outside click & Escape key
  useEffect(() => {
    if (!open) return;

    const handleOutsideClick = (e: MouseEvent | PointerEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) {
        setOpen(false);
      }
    };

    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setOpen(false);
      }
    };

    document.addEventListener('mousedown', handleOutsideClick);
    document.addEventListener('keydown', handleKeyDown);

    return () => {
      document.removeEventListener('mousedown', handleOutsideClick);
      document.removeEventListener('keydown', handleKeyDown);
    };
  }, [open]);

  const handleCopySource = async () => {
    try {
      await navigator.clipboard.writeText(job.source);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Ignore clipboard error
    }
  };

  return (
    <div className="relative" ref={menuRef}>
      <button
        type="button"
        className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        onClick={() => setOpen((prev) => !prev)}
        aria-label="More actions"
        aria-expanded={open}
        aria-haspopup="menu"
      >
        <MoreVertical className="size-4" aria-hidden="true" />
      </button>

      {open && (
        <div
          role="menu"
          aria-label="Job options"
          className="absolute right-0 top-full z-20 mt-1 min-w-[160px] rounded-md border border-border bg-surface-2 py-1 shadow-lg focus-visible:outline-none"
        >
          {onCancel && !isCompleted && !isCancelled && (
            <button
              type="button"
              role="menuitem"
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-destructive hover:bg-surface focus-visible:bg-surface focus-visible:outline-none"
              onClick={() => {
                setOpen(false);
                onAction(onCancel);
              }}
            >
              Cancel
            </button>
          )}

          {onRetry && (isFailed || isCancelled) && (
            <button
              type="button"
              role="menuitem"
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface focus-visible:bg-surface focus-visible:outline-none"
              onClick={() => {
                setOpen(false);
                onAction(onRetry);
              }}
            >
              Retry
            </button>
          )}

          {onOpenFolder && (isCompleted || isSeeding) && (
            <button
              type="button"
              role="menuitem"
              className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface focus-visible:bg-surface focus-visible:outline-none"
              onClick={() => {
                setOpen(false);
                onOpenFolder();
              }}
            >
              Open downloads folder
            </button>
          )}

          <button
            type="button"
            role="menuitem"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface focus-visible:bg-surface focus-visible:outline-none"
            onClick={handleCopySource}
          >
            {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
            {copied ? 'Copied URL' : 'Copy source URL'}
          </button>

          <button
            type="button"
            role="menuitem"
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-foreground hover:bg-surface focus-visible:bg-surface focus-visible:outline-none"
            onClick={() => {
              setOpen(false);
              onToggleDetails();
            }}
          >
            {detailsOpen ? 'Hide details' : 'Show details'}
          </button>
        </div>
      )}
    </div>
  );
}
