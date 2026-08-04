import { useState, useEffect, useRef, useMemo } from 'react';
import type { JobCapabilities, JobPriority, Category, FilenameConflictPolicy } from '../../types';
import { getCategories, resolveCapabilities } from '../../api';
import type { DestinationMode } from './downloadPolicy';

export function useDownloadComposer() {
  const [inputText, setInputText] = useState('');
  const [isBatchMode, setIsBatchMode] = useState(false);
  const [showBasicOptions, setShowBasicOptions] = useState(false);
  const [showAdvancedOptions, setShowAdvancedOptions] = useState(false);

  const [priority, setPriority] = useState<JobPriority>('normal');
  const [destinationMode, setDestinationMode] = useState<DestinationMode>('default');
  const [selectedCategoryId, setSelectedCategoryId] = useState<string>('');
  const [customDestDir, setCustomDestDir] = useState<string>('');
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>('rename');

  // Network & Advanced options state
  const [downloadLimitMiB, setDownloadLimitMiB] = useState('');
  const [uploadLimitMiB, setUploadLimitMiB] = useState('');
  const [proxyMode, setProxyMode] = useState<'disabled' | 'system' | 'custom'>('disabled');
  const [proxyProtocol, setProxyProtocol] = useState<'http' | 'https' | 'socks5'>('http');
  const [proxyHost, setProxyHost] = useState('');
  const [proxyPort, setProxyPort] = useState('');
  const [proxyUsername, setProxyUsername] = useState('');
  const [proxyPassword, setProxyPassword] = useState('');
  const [userAgent, setUserAgent] = useState('');
  const [headersRaw, setHeadersRaw] = useState('');
  const [maxAttempts, setMaxAttempts] = useState('');
  const [retryWaitSeconds, setRetryWaitSeconds] = useState('');
  const [connectTimeoutSeconds, setConnectTimeoutSeconds] = useState('');
  const [requestTimeoutSeconds, setRequestTimeoutSeconds] = useState('');
  const [split, setSplit] = useState('');
  const [maxConnectionsPerServer, setMaxConnectionsPerServer] = useState('');
  const [minSplitMiB, setMinSplitMiB] = useState('');
  const [trackersRaw, setTrackersRaw] = useState('');

  // Capabilities state & categories
  const [capabilities, setCapabilities] = useState<JobCapabilities | null>(null);
  const [capabilitiesLoading, setCapabilitiesLoading] = useState(false);
  const [categories, setCategories] = useState<Category[]>([]);

  // Race prevention counter
  const requestIdRef = useRef(0);

  // Fetch categories on mount
  useEffect(() => {
    let active = true;
    getCategories()
      .then((res) => {
        if (active) setCategories(res);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, []);

  // Split sources from text input
  const sources = useMemo(() => {
    if (!inputText.trim()) return [];
    if (!isBatchMode) {
      const line = inputText.trim().split('\n')[0].trim();
      return line ? [line] : [];
    }
    return inputText
      .split('\n')
      .map((s) => s.trim())
      .filter(Boolean);
  }, [inputText, isBatchMode]);

  const sourceForCaps = sources[0] || '';

  // Debounced capability resolution with race protection
  useEffect(() => {
    if (!sourceForCaps) {
      setCapabilities(null);
      setCapabilitiesLoading(false);
      return;
    }

    const currentRequestId = ++requestIdRef.current;
    setCapabilitiesLoading(true);

    const timer = setTimeout(() => {
      resolveCapabilities(sourceForCaps)
        .then((res) => {
          if (requestIdRef.current === currentRequestId) {
            setCapabilities(res);
          }
        })
        .catch(() => {
          if (requestIdRef.current === currentRequestId) {
            setCapabilities(null);
          }
        })
        .finally(() => {
          if (requestIdRef.current === currentRequestId) {
            setCapabilitiesLoading(false);
          }
        });
    }, 250);

    return () => {
      clearTimeout(timer);
    };
  }, [sourceForCaps]);

  return {
    inputText,
    setInputText,
    isBatchMode,
    setIsBatchMode,
    showBasicOptions,
    setShowBasicOptions,
    showAdvancedOptions,
    setShowAdvancedOptions,
    priority,
    setPriority,
    destinationMode,
    setDestinationMode,
    selectedCategoryId,
    setSelectedCategoryId,
    customDestDir,
    setCustomDestDir,
    conflictPolicy,
    setConflictPolicy,
    downloadLimitMiB,
    setDownloadLimitMiB,
    uploadLimitMiB,
    setUploadLimitMiB,
    proxyMode,
    setProxyMode,
    proxyProtocol,
    setProxyProtocol,
    proxyHost,
    setProxyHost,
    proxyPort,
    setProxyPort,
    proxyUsername,
    setProxyUsername,
    proxyPassword,
    setProxyPassword,
    userAgent,
    setUserAgent,
    headersRaw,
    setHeadersRaw,
    maxAttempts,
    setMaxAttempts,
    retryWaitSeconds,
    setRetryWaitSeconds,
    connectTimeoutSeconds,
    setConnectTimeoutSeconds,
    requestTimeoutSeconds,
    setRequestTimeoutSeconds,
    split,
    setSplit,
    maxConnectionsPerServer,
    setMaxConnectionsPerServer,
    minSplitMiB,
    setMinSplitMiB,
    trackersRaw,
    setTrackersRaw,
    capabilities,
    capabilitiesLoading,
    categories,
    sources,
  };
}
