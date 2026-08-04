import React, { useState } from 'react';
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

  return (
    <form
      onSubmit={handleSubmit}
      aria-label="Add downloads"
      className="rounded-lg border border-border bg-surface p-2.5"
    >
      <SourceComposer
        inputText={composer.inputText}
        onInputTextChange={composer.setInputText}
        isBatchMode={composer.isBatchMode}
        onToggleBatchMode={() => composer.setIsBatchMode((prev) => !prev)}
        showBasicOptions={composer.showBasicOptions}
        onToggleBasicOptions={() => composer.setShowBasicOptions((prev) => !prev)}
        showAdvancedOptions={composer.showAdvancedOptions}
        onToggleAdvancedOptions={() => composer.setShowAdvancedOptions((prev) => !prev)}
        sourcesCount={composer.sources.length}
        onUploadTorrentFile={handleUploadTorrentFile}
        onSubmit={handleSubmit}
        isSubmitting={isSubmitting}
        disabled={disabled}
      />

      {localError && (
        <div className="mt-2 text-xs text-destructive font-medium" role="alert">
          {localError}
        </div>
      )}

      {composer.showBasicOptions && (
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
      )}

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
    </form>
  );
}
