import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { RecoveryBanner } from './RecoveryBanner';
import type { RecoverySummary } from '../types';

describe('RecoveryBanner', () => {
  it('renders nothing on clean startup with zero counts and empty issues', () => {
    const cleanSummary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [],
    };
    const onDismiss = vi.fn();

    const { container } = render(
      <RecoveryBanner summary={cleanSummary} onDismiss={onDismiss} />
    );

    expect(container.firstChild).toBeNull();
  });

  it('renders truthful copy for reconciled finalizations (single and plural)', () => {
    const singleSummary: RecoverySummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
    };
    const onDismiss = vi.fn();

    const { unmount } = render(
      <RecoveryBanner summary={singleSummary} onDismiss={onDismiss} />
    );
    expect(
      screen.getByText('Completed remaining finalization work for 1 download after restart.')
    ).toBeInTheDocument();
    unmount();

    const pluralSummary: RecoverySummary = {
      reconciledFinalizations: 3,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
    };
    render(<RecoveryBanner summary={pluralSummary} onDismiss={onDismiss} />);
    expect(
      screen.getByText('Completed remaining finalization work for 3 downloads after restart.')
    ).toBeInTheDocument();
  });

  it('renders truthful copy for reattached transfers', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 2,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);
    expect(
      screen.getByText('Reattached to 2 active background transfers.')
    ).toBeInTheDocument();
  });

  it('renders truthful copy for restarted metadata acquisitions', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 1,
      interruptedMediaJobs: 0,
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);
    expect(
      screen.getByText('Restarted metadata acquisition for 1 torrent.')
    ).toBeInTheDocument();
  });

  it('renders truthful copy for interrupted media jobs', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 1,
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);
    expect(
      screen.getByText('1 media download was interrupted by application shutdown and needs retry.')
    ).toBeInTheDocument();
  });

  it('maps issue kinds to safe copy without leaking raw errors or secrets', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
      issues: [
        { jobId: 'job-2', kind: 'finalization_failed' },
        { jobId: 'job-3', kind: 'engine_unavailable' },
        { jobId: 'job-4', kind: 'external_state_unrecoverable' },
        { jobId: 'job-5', kind: 'torrent_metadata_unrecoverable' },
      ],
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);

    expect(screen.getByText('Download finalization could not be completed after restart.')).toBeInTheDocument();
    expect(screen.getByText('Download engine was not registered or available during startup recovery.')).toBeInTheDocument();
    expect(screen.getByText('Download engine state could not be recovered after restart.')).toBeInTheDocument();
    expect(screen.getByText('Torrent metadata state could not be recovered after restart.')).toBeInTheDocument();
  });

  it('deduplicates issue display when aggregate counter already describes the intervention', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 1,
      issues: [
        { jobId: 'job-1', kind: 'interrupted_media' },
      ],
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);

    // Aggregate text is present
    expect(
      screen.getByText('1 media download was interrupted by application shutdown and needs retry.')
    ).toBeInTheDocument();

    // No redundant bullet point for interrupted_media is rendered
    expect(screen.queryByRole('list')).toBeNull();
  });

  it('renders info styling for clean successful reconciliations and warning styling for issues', () => {
    const successOnlySummary: RecoverySummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 1,
      restartedMetadataAcquisitions: 1,
      interruptedMediaJobs: 0,
    };
    const { unmount } = render(<RecoveryBanner summary={successOnlySummary} onDismiss={vi.fn()} />);
    const infoBanner = screen.getByRole('status');
    expect(infoBanner).toHaveClass('border-blue-500/40');
    expect(screen.getByText('Startup Recovery Notice')).toBeInTheDocument();
    unmount();

    const warningSummary: RecoverySummary = {
      reconciledFinalizations: 0,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 1,
      issues: [{ jobId: 'j-warn', kind: 'interrupted_media' }],
    };
    render(<RecoveryBanner summary={warningSummary} onDismiss={vi.fn()} />);
    const warnBanner = screen.getByRole('status');
    expect(warnBanner).toHaveClass('border-amber-500/40');
    expect(screen.getByText('Startup Recovery Attention Needed')).toBeInTheDocument();
  });

  it('provides accessibility semantics role=status and aria-live=polite', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
    };
    render(<RecoveryBanner summary={summary} onDismiss={vi.fn()} />);

    const banner = screen.getByRole('status');
    expect(banner).toBeInTheDocument();
    expect(banner).toHaveAttribute('aria-live', 'polite');
  });

  it('calls onDismiss when dismiss button is clicked', () => {
    const summary: RecoverySummary = {
      reconciledFinalizations: 1,
      reattachedTransfers: 0,
      restartedMetadataAcquisitions: 0,
      interruptedMediaJobs: 0,
    };
    const onDismiss = vi.fn();
    render(<RecoveryBanner summary={summary} onDismiss={onDismiss} />);

    const button = screen.getByRole('button', { name: 'Dismiss recovery notice' });
    expect(button).toBeInTheDocument();
    fireEvent.click(button);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });
});
