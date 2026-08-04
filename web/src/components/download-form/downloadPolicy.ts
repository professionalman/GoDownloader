import type {
  JobNetworkPolicyOverride,
  HTTPHeaderPolicy,
} from '../../types';

export type DestinationMode = 'default' | 'category' | 'custom';

export function resolveDestination(
  mode: DestinationMode,
  categoryId?: string,
  customDestDir?: string,
): { categoryId?: string; destinationDir?: string } {
  if (mode === 'category' && categoryId) {
    return { categoryId };
  }
  if (mode === 'custom' && customDestDir && customDestDir.trim().length > 0) {
    return { destinationDir: customDestDir.trim() };
  }
  return {};
}

export function parseHeaders(rawHeaders: string): HTTPHeaderPolicy[] | undefined {
  if (!rawHeaders.trim()) return undefined;
  const lines = rawHeaders.split('\n').filter((l) => l.trim().length > 0);
  const result: HTTPHeaderPolicy[] = [];

  for (const line of lines) {
    const idx = line.indexOf(':');
    if (idx !== -1) {
      const name = line.slice(0, idx).trim();
      const value = line.slice(idx + 1).trim();
      if (name) {
        result.push({ name, value });
      }
    }
  }

  return result.length > 0 ? result : undefined;
}

export function parseTrackers(rawTrackers: string): string[] | undefined {
  if (!rawTrackers.trim()) return undefined;
  const trackers = rawTrackers
    .split('\n')
    .map((t) => t.trim())
    .filter(Boolean);
  return trackers.length > 0 ? trackers : undefined;
}

export interface NetworkPolicyInputs {
  downloadLimitMiB?: string;
  uploadLimitMiB?: string;
  proxyMode?: 'disabled' | 'system' | 'custom';
  proxyProtocol?: 'http' | 'https' | 'socks5';
  proxyHost?: string;
  proxyPort?: string;
  proxyUsername?: string;
  proxyPassword?: string;
  userAgent?: string;
  headersRaw?: string;
  maxAttempts?: string;
  retryWaitSeconds?: string;
  connectTimeoutSeconds?: string;
  requestTimeoutSeconds?: string;
  split?: string;
  maxConnectionsPerServer?: string;
  minSplitMiB?: string;
  supportedCapabilities?: {
    downloadLimit?: boolean;
    uploadLimit?: boolean;
    proxy?: boolean;
    userAgent?: boolean;
    customHeaders?: boolean;
    retryPolicy?: boolean;
    timeoutPolicy?: boolean;
    connections?: boolean;
  };
}

export function buildNetworkPolicy(
  inputs: NetworkPolicyInputs,
): JobNetworkPolicyOverride | undefined {
  const policy: JobNetworkPolicyOverride = {};
  const MIB = 1024 * 1024;
  const caps = inputs.supportedCapabilities ?? {};

  if (caps.downloadLimit !== false && inputs.downloadLimitMiB !== undefined && inputs.downloadLimitMiB !== '') {
    const val = Number(inputs.downloadLimitMiB);
    if (!isNaN(val) && val >= 0) {
      policy.downloadLimitBytesPerSecond = Math.round(val * MIB);
    }
  }

  if (caps.uploadLimit !== false && inputs.uploadLimitMiB !== undefined && inputs.uploadLimitMiB !== '') {
    const val = Number(inputs.uploadLimitMiB);
    if (!isNaN(val) && val >= 0) {
      policy.uploadLimitBytesPerSecond = Math.round(val * MIB);
    }
  }

  if (caps.proxy !== false && inputs.proxyMode && inputs.proxyMode !== 'disabled') {
    policy.proxy = { mode: inputs.proxyMode };
    if (inputs.proxyMode === 'custom') {
      policy.proxy = {
        mode: 'custom',
        protocol: inputs.proxyProtocol || 'http',
        host: inputs.proxyHost || '',
        port: inputs.proxyPort ? Number(inputs.proxyPort) : undefined,
        username: inputs.proxyUsername || undefined,
      };
      if (inputs.proxyPassword) {
        policy.proxyPassword = inputs.proxyPassword;
      }
    }
  }

  if (caps.userAgent !== false && inputs.userAgent && inputs.userAgent.trim().length > 0) {
    policy.userAgent = inputs.userAgent.trim();
  }

  if (caps.customHeaders !== false && inputs.headersRaw) {
    const parsed = parseHeaders(inputs.headersRaw);
    if (parsed) {
      policy.httpHeaders = parsed;
    }
  }

  if (
    caps.retryPolicy !== false &&
    ((inputs.maxAttempts !== undefined && inputs.maxAttempts !== '') ||
      (inputs.retryWaitSeconds !== undefined && inputs.retryWaitSeconds !== ''))
  ) {
    policy.retryPolicy = {
      maxAttempts: Number(inputs.maxAttempts || 0),
      retryWaitSeconds: Number(inputs.retryWaitSeconds || 0),
    };
  }

  if (
    caps.timeoutPolicy !== false &&
    ((inputs.connectTimeoutSeconds !== undefined && inputs.connectTimeoutSeconds !== '') ||
      (inputs.requestTimeoutSeconds !== undefined && inputs.requestTimeoutSeconds !== ''))
  ) {
    policy.timeoutPolicy = {
      connectTimeoutSeconds: Number(inputs.connectTimeoutSeconds || 0),
      requestTimeoutSeconds: Number(inputs.requestTimeoutSeconds || 0),
    };
  }

  if (
    caps.connections !== false &&
    ((inputs.split !== undefined && inputs.split !== '') ||
      (inputs.maxConnectionsPerServer !== undefined && inputs.maxConnectionsPerServer !== '') ||
      (inputs.minSplitMiB !== undefined && inputs.minSplitMiB !== ''))
  ) {
    policy.directConnections = {
      split: Number(inputs.split || 5),
      maxConnectionsPerServer: Number(inputs.maxConnectionsPerServer || 1),
      minSplitSizeBytes: Number(inputs.minSplitMiB || 20) * MIB,
    };
  }

  return Object.keys(policy).length > 0 ? policy : undefined;
}
