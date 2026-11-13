import { beforeEach, describe, expect, it, vi } from 'vitest';
import { getAppVersion } from './api';
import { getBackendClient } from './transport';

vi.mock('./transport', () => ({ getBackendClient: vi.fn() }));

const clientWith = (appInfo?: { getVersion: () => Promise<string> }) =>
  vi.mocked(getBackendClient).mockReturnValue({ appInfo } as unknown as ReturnType<typeof getBackendClient>);

describe('getAppVersion', () => {
  beforeEach(() => vi.clearAllMocks());

  it('returns the version reported by the running build', async () => {
    clientWith({ getVersion: async () => '0.9.1' });
    await expect(getAppVersion()).resolves.toBe('0.9.1');
    clientWith({ getVersion: async () => ' 1.4.0 ' });
    await expect(getAppVersion()).resolves.toBe('1.4.0');
  });

  it('returns null, never a guessed version, when the backend cannot report one', async () => {
    clientWith(undefined);
    await expect(getAppVersion()).resolves.toBeNull();
    clientWith({ getVersion: async () => '' });
    await expect(getAppVersion()).resolves.toBeNull();
    clientWith({ getVersion: async () => { throw new Error('ipc failed'); } });
    await expect(getAppVersion()).resolves.toBeNull();
  });
});
