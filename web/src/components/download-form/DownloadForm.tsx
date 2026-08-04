import React, { useState } from 'react';
import { Layers, SlidersHorizontal, Settings2 } from 'lucide-react';
import type {
  JobPriority,
  FilenameConflictPolicy,
  JobNetworkPolicyOverride,
  SeedingPolicy,
} from '../../types';
import { useDownloadComposer } from './useDownloadComposer';
import { SourceComposer } from './SourceComposer';
import { BasicDownloadOptions } from './BasicDownloadOptions';
import { AdvancedDownloadOptions } from './AdvancedDownloadOptions';
import { resolveDestination, buildNetworkPolicy, parseTrackers } from './downloadPolicy';

export interface DownloadFormProps {
  onSubmit: (
    sources: string[],
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    conflictPolicy?: FilenameConflictPolicy,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[],
  ) => Promise<void>;
  onUploadTorrent?: (
    file: File,
    priority: JobPriority,
    categoryId?: string,
    destinationDir?: string,
    networkPolicy?: JobNetworkPolicyOverride,
    seedingPolicy?: SeedingPolicy,
    trackers?: string[],
  ) => Promise<void>;
  disabled?: boolean;
}

export function DownloadForm({ onSubmit, onUploadTorrent, disabled }: DownloadFormProps) {
  const composer = useDownloadComposer();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [localError, setLocalError] = useState('');

  // Single expanded toggle state for top-row chevron
  const optionsOpen = composer.showBasicOptions || composer.showAdvancedOptions;

  const handleToggleOptions = () => {
    if (optionsOpen) {
      composer.setShowBasicOptions(false);
      composer.setShowAdvancedOptions(false);
    } else {
      composer.setShowBasicOptions(true);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (isSubmitting || composer.sources.length === 0) return;

    setIsSubmitting(true);
    setLocalError('');

    try {
      const { categoryId, destinationDir } = resolveDestination(
        composer.destinationMode,
        composer.selectedCategoryId,
        composer.customDestDir,
      );

      const netPolicy = buildNetworkPolicy({
        downloadLimitMiB: composer.downloadLimitMiB,
        uploadLimitMiB: composer.uploadLimitMiB,
        proxyMode: composer.proxyMode,
        proxyProtocol: composer.proxyProtocol,
        proxyHost: composer.proxyHost,
        proxyPort: composer.proxyPort,
        proxyUsername: composer.proxyUsername,
        proxyPassword: composer.proxyPassword,
        userAgent: composer.userAgent,
        headersRaw: composer.headersRaw,
        maxAttempts: composer.maxAttempts,
        retryWaitSeconds: composer.retryWaitSeconds,
        connectTimeoutSeconds: composer.connectTimeoutSeconds,
        requestTimeoutSeconds: composer.requestTimeoutSeconds,
        split: composer.split,
        maxConnectionsPerServer: composer.maxConnectionsPerServer,
        minSplitMiB: composer.minSplitMiB,
        supportedCapabilities: {
          downloadLimit: composer.capabilities?.downloadLimit?.supported,
          uploadLimit: composer.capabilities?.uploadLimit?.supported,
          proxy: composer.capabilities?.proxy?.supported,
          userAgent: composer.capabilities?.userAgent?.supported,
          customHeaders: composer.capabilities?.customHeaders?.supported,
          retryPolicy: composer.capabilities?.retryPolicy?.supported,
          timeoutPolicy: composer.capabilities?.timeoutPolicy?.supported,
          connections: composer.capabilities?.connections?.supported,
        },
      });

      const trackers = parseTrackers(composer.trackersRaw);

      await onSubmit(
        composer.sources,
        composer.priority,
        categoryId,
        destinationDir,
        composer.conflictPolicy,
        netPolicy,
        undefined,
        trackers,
      );

      // Successful submission: clear source text only
      composer.setInputText('');
    } catch (err: unknown) {
      setLocalError(err instanceof Error ? err.message : 'Failed to start download');
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleUploadTorrentFile = async (file: File) => {
    if (isSubmitting || !onUploadTorrent) return;

    setIsSubmitting(true);
    setLocalError('');

    try {
      const { categoryId, destinationDir } = resolveDestination(
        composer.destinationMode,
        composer.selectedCategoryId,
        composer.customDestDir,
      );

      const netPolicy = buildNetworkPolicy({
        downloadLimitMiB: composer.downloadLimitMiB,
        uploadLimitMiB: composer.uploadLimitMiB,
        proxyMode: composer.proxyMode,
        proxyProtocol: composer.proxyProtocol,
        proxyHost: composer.proxyHost,
        proxyPort: composer.proxyPort,
        proxyUsername: composer.proxyUsername,
        proxyPassword: composer.proxyPassword,
        userAgent: composer.userAgent,
        headersRaw: composer.headersRaw,
        maxAttempts: composer.maxAttempts,
        retryWaitSeconds: composer.retryWaitSeconds,
        connectTimeoutSeconds: composer.connectTimeoutSeconds,
        requestTimeoutSeconds: composer.requestTimeoutSeconds,
        split: composer.split,
        maxConnectionsPerServer: composer.maxConnectionsPerServer,
        minSplitMiB: composer.minSplitMiB,
      });

      const trackers = parseTrackers(composer.trackersRaw);

      await onUploadTorrent(
        file,
        composer.priority,
        categoryId,
        destinationDir,
        netPolicy,
        undefined,
        trackers,
      );
    } catch (err: unknown) {
      setLocalError(err instanceof Error ? err.message : 'Failed to upload torrent');
    } finally {
      setIsSubmitting(false);
    }
  };

  const overLimit = composer.sources.length > 100;

  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Add downloads"
      className="rounded-lg border border-border bg-surface p-2"
    >
      <SourceComposer
        inputText={composer.inputText}
        onInputTextChange={composer.setInputText}
        isBatchMode={composer.isBatchMode}
        expanded={optionsOpen}
        onToggleExpanded={handleToggleOptions}
        sourcesCount={composer.sources.length}
        onUploadTorrentFile={handleUploadTorrentFile}
        onSubmit={handleSubmit}
        isSubmitting={isSubmitting}
        disabled={disabled}
      />

      {localError && (
        <div className="mt-2 text-xs font-medium text-destructive" role="alert">
          {localError}
        </div>
      )}

      {/* Expanded options panel */}
      {optionsOpen && (
        <div className="mt-3 border-t border-border pt-3 space-y-3">
          {/* Mode switch & Batch status */}
          <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-surface-2/60 p-1.5 border border-border/60">
            <div className="flex items-center gap-1">
              <button
                type="button"
                className={`flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  !composer.isBatchMode
                    ? 'bg-surface text-foreground shadow-sm'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
                onClick={() => composer.setIsBatchMode(false)}
              >
                <SlidersHorizontal className="size-3.5" />
                Single URL
              </button>
              <button
                type="button"
                className={`flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs font-medium transition-colors ${
                  composer.isBatchMode
                    ? 'bg-surface text-foreground shadow-sm'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
                title="Switch to batch mode"
                onClick={() => composer.setIsBatchMode(true)}
              >
                <Layers className="size-3.5" />
                Batch mode
              </button>
            </div>

            {composer.isBatchMode && (
              <div className="text-xs text-muted-foreground px-2">
                <span className={`num ${overLimit ? 'font-semibold text-destructive' : ''}`}>
                  {composer.sources.length} of 100 sources
                </span>
                {overLimit && <span className="ml-1 text-destructive font-medium">(Max 100)</span>}
              </div>
            )}
          </div>

          <BasicDownloadOptions
            priority={composer.priority}
            onPriorityChange={composer.setPriority}
            destinationMode={composer.destinationMode}
            onDestinationModeChange={composer.setDestinationMode}
            categories={composer.categories}
            selectedCategoryId={composer.selectedCategoryId}
            onCategoryChange={composer.setSelectedCategoryId}
            customDestDir={composer.customDestDir}
            onCustomDestDirChange={composer.setCustomDestDir}
            conflictPolicy={composer.conflictPolicy}
            onConflictPolicyChange={composer.setConflictPolicy}
            disabled={disabled || isSubmitting}
          />

          <div className="pt-1 flex justify-end">
            <button
              type="button"
              className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
              aria-expanded={composer.showAdvancedOptions}
              aria-label={composer.showAdvancedOptions ? 'Hide advanced options' : 'Show advanced options'}
              onClick={() => composer.setShowAdvancedOptions((prev) => !prev)}
            >
              <Settings2 className="size-3.5" />
              <span>{composer.showAdvancedOptions ? 'Hide advanced options' : 'Advanced options'}</span>
            </button>
          </div>

          {composer.showAdvancedOptions && (
            <AdvancedDownloadOptions
              capabilities={composer.capabilities}
              loading={composer.capabilitiesLoading}
              downloadLimitMiB={composer.downloadLimitMiB}
              onDownloadLimitChange={composer.setDownloadLimitMiB}
              uploadLimitMiB={composer.uploadLimitMiB}
              onUploadLimitChange={composer.setUploadLimitMiB}
              proxyMode={composer.proxyMode}
              onProxyModeChange={composer.setProxyMode}
              proxyProtocol={composer.proxyProtocol}
              onProxyProtocolChange={composer.setProxyProtocol}
              proxyHost={composer.proxyHost}
              onProxyHostChange={composer.setProxyHost}
              proxyPort={composer.proxyPort}
              onProxyPortChange={composer.setProxyPort}
              proxyUsername={composer.proxyUsername}
              onProxyUsernameChange={composer.setProxyUsername}
              proxyPassword={composer.proxyPassword}
              onProxyPasswordChange={composer.setProxyPassword}
              userAgent={composer.userAgent}
              onUserAgentChange={composer.setUserAgent}
              headersRaw={composer.headersRaw}
              onHeadersRawChange={composer.setHeadersRaw}
              maxAttempts={composer.maxAttempts}
              onMaxAttemptsChange={composer.setMaxAttempts}
              retryWaitSeconds={composer.retryWaitSeconds}
              onRetryWaitSecondsChange={composer.setRetryWaitSeconds}
              connectTimeoutSeconds={composer.connectTimeoutSeconds}
              onConnectTimeoutSecondsChange={composer.setConnectTimeoutSeconds}
              requestTimeoutSeconds={composer.requestTimeoutSeconds}
              onRequestTimeoutSecondsChange={composer.setRequestTimeoutSeconds}
              split={composer.split}
              onSplitChange={composer.setSplit}
              maxConnectionsPerServer={composer.maxConnectionsPerServer}
              onMaxConnectionsPerServerChange={composer.setMaxConnectionsPerServer}
              minSplitMiB={composer.minSplitMiB}
              onMinSplitMiBChange={composer.setMinSplitMiB}
              trackersRaw={composer.trackersRaw}
              onTrackersRawChange={composer.setTrackersRaw}
              disabled={disabled || isSubmitting}
            />
          )}
        </div>
      )}
    </form>
  );
}
