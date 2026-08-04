import { describe, it, expect } from 'vitest';
import {
  resolveDestination,
  parseHeaders,
  parseTrackers,
  buildNetworkPolicy,
} from './downloadPolicy';

describe('downloadPolicy pure functions', () => {
  describe('resolveDestination', () => {
    it('returns empty object in default mode', () => {
      expect(resolveDestination('default', 'cat-1', '/custom/path')).toEqual({});
    });

    it('returns categoryId exclusively in category mode', () => {
      const res = resolveDestination('category', 'cat-1', '/custom/path');
      expect(res).toEqual({ categoryId: 'cat-1' });
      expect(res.destinationDir).toBeUndefined();
    });

    it('returns destinationDir exclusively in custom mode', () => {
      const res = resolveDestination('custom', 'cat-1', '/custom/path');
      expect(res).toEqual({ destinationDir: '/custom/path' });
      expect(res.categoryId).toBeUndefined();
    });
  });

  describe('parseHeaders', () => {
    it('parses HTTP headers correctly from lines', () => {
      const raw = 'Authorization: Bearer token123\nUser-Agent: CustomBot/1.0';
      const parsed = parseHeaders(raw);
      expect(parsed).toEqual([
        { name: 'Authorization', value: 'Bearer token123' },
        { name: 'User-Agent', value: 'CustomBot/1.0' },
      ]);
    });

    it('returns undefined for empty input', () => {
      expect(parseHeaders('   \n  ')).toBeUndefined();
    });
  });

  describe('parseTrackers', () => {
    it('parses trackers into string array', () => {
      const raw = 'udp://tracker.openbittorrent.com:80/announce\n\nhttp://tracker.torrent.to/announce';
      expect(parseTrackers(raw)).toEqual([
        'udp://tracker.openbittorrent.com:80/announce',
        'http://tracker.torrent.to/announce',
      ]);
    });

    it('returns undefined for empty text', () => {
      expect(parseTrackers('')).toBeUndefined();
    });
  });

  describe('buildNetworkPolicy', () => {
    it('converts MiB/s rates to bytes/s', () => {
      const policy = buildNetworkPolicy({
        downloadLimitMiB: '1.5',
        uploadLimitMiB: '0.5',
        supportedCapabilities: { downloadLimit: true, uploadLimit: true },
      });
      expect(policy?.downloadLimitBytesPerSecond).toBe(Math.round(1.5 * 1024 * 1024));
      expect(policy?.uploadLimitBytesPerSecond).toBe(Math.round(0.5 * 1024 * 1024));
    });

    it('handles custom proxy settings', () => {
      const policy = buildNetworkPolicy({
        proxyMode: 'custom',
        proxyProtocol: 'socks5',
        proxyHost: '127.0.0.1',
        proxyPort: '1080',
        proxyUsername: 'user',
        proxyPassword: 'pass',
        supportedCapabilities: { proxy: true },
      });
      expect(policy?.proxy).toEqual({
        mode: 'custom',
        protocol: 'socks5',
        host: '127.0.0.1',
        port: 1080,
        username: 'user',
      });
      expect(policy?.proxyPassword).toBe('pass');
    });
  });
});
