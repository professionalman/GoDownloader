import React from 'react';
import type { RecoverySummary, RecoveryIssueKind } from '../types';

export interface RecoveryBannerProps {
  summary: RecoverySummary;
  onDismiss: () => void;
}

const ISSUE_MESSAGES: Record<RecoveryIssueKind, string> = {
  interrupted_media: 'Media download was interrupted by application shutdown and needs retry.',
  finalization_failed: 'Download finalization could not be completed after restart.',
  engine_unavailable: 'Download engine was not registered or available during startup recovery.',
  external_state_unrecoverable: 'Download engine state could not be recovered after restart.',
  torrent_metadata_unrecoverable: 'Torrent metadata state could not be recovered after restart.',
};

export function hasRecoveryInterventions(summary?: RecoverySummary | null): boolean {
  if (!summary) return false;
  return (
    summary.reconciledFinalizations > 0 ||
    summary.reattachedTransfers > 0 ||
    summary.restartedMetadataAcquisitions > 0 ||
    summary.interruptedMediaJobs > 0 ||
    (Array.isArray(summary.issues) && summary.issues.length > 0)
  );
}

export function formatInterventionMessages(summary: RecoverySummary): string[] {
  const messages: string[] = [];

  if (summary.reconciledFinalizations > 0) {
    const n = summary.reconciledFinalizations;
    messages.push(
      n === 1
        ? 'Completed remaining finalization work for 1 download after restart.'
        : `Completed remaining finalization work for ${n} downloads after restart.`
    );
  }

  if (summary.reattachedTransfers > 0) {
    const n = summary.reattachedTransfers;
    messages.push(
      n === 1
        ? 'Reattached to 1 active background transfer.'
        : `Reattached to ${n} active background transfers.`
    );
  }

  if (summary.restartedMetadataAcquisitions > 0) {
    const n = summary.restartedMetadataAcquisitions;
    messages.push(
      n === 1
        ? 'Restarted metadata acquisition for 1 torrent.'
        : `Restarted metadata acquisition for ${n} torrents.`
    );
  }

  if (summary.interruptedMediaJobs > 0) {
    const n = summary.interruptedMediaJobs;
    messages.push(
      n === 1
        ? '1 media download was interrupted by application shutdown and needs retry.'
        : `${n} media downloads were interrupted by application shutdown and need retry.`
    );
  }

  return messages;
}

export const RecoveryBanner: React.FC<RecoveryBannerProps> = ({ summary, onDismiss }) => {
  if (!hasRecoveryInterventions(summary)) {
    return null;
  }

  const messages = formatInterventionMessages(summary);
  const issues = summary.issues || [];
  const hasWarning = summary.interruptedMediaJobs > 0 || issues.length > 0;

  // Deduplicate issue messages by kind; exclude kinds already fully represented in aggregate messages
  const displayedIssueKinds = Array.from(
    new Set(
      issues
        .filter((iss) => {
          if (iss.kind === 'interrupted_media' && summary.interruptedMediaJobs > 0) {
            return false;
          }
          return true;
        })
        .map((iss) => iss.kind)
    )
  );

  return (
    <div
      role="status"
      aria-live="polite"
      className={`my-2 flex items-start justify-between gap-3 rounded-md border p-3 text-xs ${
        hasWarning
          ? 'border-amber-500/40 bg-amber-500/10 text-amber-900 dark:text-amber-200'
          : 'border-blue-500/40 bg-blue-500/10 text-blue-900 dark:text-blue-200'
      }`}
    >
      <div className="flex-1 space-y-1">
        <div className="font-semibold">
          {hasWarning ? 'Startup Recovery Attention Needed' : 'Startup Recovery Notice'}
        </div>
        {messages.map((msg, idx) => (
          <div key={idx}>{msg}</div>
        ))}
        {displayedIssueKinds.length > 0 && (
          <ul className="list-disc pl-4 space-y-0.5 mt-1">
            {displayedIssueKinds.map((kind) => {
              const text = ISSUE_MESSAGES[kind] || 'Recovery issue encountered.';
              return <li key={kind}>{text}</li>;
            })}
          </ul>
        )}
      </div>
      <button
        type="button"
        aria-label="Dismiss recovery notice"
        className="text-base font-bold leading-none hover:opacity-80 px-1 py-0.5 cursor-pointer"
        onClick={onDismiss}
      >
        ×
      </button>
    </div>
  );
};
