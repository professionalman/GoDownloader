import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import type { YtdlpToolStatus } from '../types';
import { YtdlpToolPanel } from './YtdlpToolPanel';

vi.mock('../api', () => ({
  getYtdlpToolStatus: vi.fn(),
  installYtdlp: vi.fn(),
  updateYtdlp: vi.fn(),
  checkYtdlpUpdate: vi.fn(),
}));

const base: YtdlpToolStatus = {
  tool: 'yt-dlp',
  available: false,
  ownership: 'none',
  authenticated: false,
  restartRequired: false,
  busy: false,
  platformSupported: true,
  canInstall: true,
  canUpdate: false,
};

function status(overrides: Partial<YtdlpToolStatus>): YtdlpToolStatus {
  return { ...base, ...overrides };
}

const managedActive = status({
  available: true,
  version: '2026.10.26',
  ownership: 'managed',
  verificationStatus: 'authenticated_release',
  authenticated: true,
  managedVersion: '2026.10.26',
  canInstall: false,
  canUpdate: true,
});

describe('YtdlpToolPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('shows a missing tool with an install action', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(
      status({ diagnostic: 'yt-dlp is not installed. Install the managed yt-dlp, or configure your own yt-dlp, to enable media downloads.' }),
    );
    render(<YtdlpToolPanel />);
    expect(await screen.findByText('Unavailable')).toBeInTheDocument();
    expect(screen.getByText('Not installed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Install yt-dlp/i })).toBeEnabled();
    expect(screen.queryByRole('button', { name: /Check for updates/i })).not.toBeInTheDocument();
  });

  it('shows an external PATH tool as version-checked only', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(
      status({ available: true, version: '2026.01.01', ownership: 'system_path', verificationStatus: 'version_probed' }),
    );
    render(<YtdlpToolPanel />);
    expect(await screen.findByText('System-provided (PATH)')).toBeInTheDocument();
    expect(screen.getByText(/not signature-verified/i)).toBeInTheDocument();
    expect(screen.queryByText(/Signed release verified/i)).not.toBeInTheDocument();
  });

  it('shows an authenticated managed tool with update checking', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(managedActive);
    render(<YtdlpToolPanel />);
    expect(await screen.findByText(/Signed release verified/i)).toBeInTheDocument();
    expect(screen.getByText('Managed by GoDownloader')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Check for updates/i })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Install yt-dlp/i })).not.toBeInTheDocument();
  });

  it('installs successfully and shows the restart requirement', async () => {
    vi.mocked(api.getYtdlpToolStatus)
      .mockResolvedValueOnce(base)
      .mockResolvedValueOnce(status({ restartRequired: true, canInstall: false, managedVersion: '2026.10.26' }));
    vi.mocked(api.installYtdlp).mockResolvedValue({
      tool: 'yt-dlp', version: '2026.10.26', ownership: 'managed', status: 'restart_required', alreadyCurrent: false, restartRequired: true,
    });
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Install yt-dlp/i }));
    expect(await screen.findByText(/verified and installed\. Quit GoDownloader completely/i)).toBeInTheDocument();
    const banner = await screen.findByText(/start it again to use the newly installed yt-dlp/i);
    // A tray close or page reload is not a backend restart; the banner must say so.
    expect(banner).toHaveTextContent(/closing the window to the\s+tray or reloading this page does not restart it/i);
    expect(banner).toHaveTextContent(/queued media work waits/i);
    expect(screen.queryByRole('button', { name: /Install yt-dlp/i })).not.toBeInTheDocument();
  });

  it('reports an install failure safely', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(base);
    vi.mocked(api.installYtdlp).mockRejectedValue(new Error('Could not download yt-dlp from GitHub. Check your connection and try again.'));
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Install yt-dlp/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/Could not download yt-dlp/i);
  });

  it('reports a signature or hash verification failure', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(base);
    vi.mocked(api.installYtdlp).mockRejectedValue(new Error('The downloaded yt-dlp did not match its signed checksum. Nothing was installed.'));
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Install yt-dlp/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/did not match its signed checksum/i);
  });

  it('checks for an update and then updates', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(managedActive);
    vi.mocked(api.checkYtdlpUpdate).mockResolvedValue({
      tool: 'yt-dlp', currentVersion: '2026.10.26', latestVersion: '2026.11.05', managed: true, updateAvailable: true,
    });
    vi.mocked(api.updateYtdlp).mockResolvedValue({
      tool: 'yt-dlp', version: '2026.11.05', ownership: 'managed', status: 'restart_required', alreadyCurrent: false, restartRequired: true,
    });
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Check for updates/i }));
    expect(await screen.findByText(/2026\.11\.05 is available/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: /Update to 2026\.11\.05/i }));
    await waitFor(() => expect(api.updateYtdlp).toHaveBeenCalledTimes(1));
    expect(await screen.findByText(/verified and installed/i)).toBeInTheDocument();
  });

  it('reports an update that is already current', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(managedActive);
    vi.mocked(api.checkYtdlpUpdate).mockResolvedValue({
      tool: 'yt-dlp', currentVersion: '2026.10.26', latestVersion: '2026.10.26', managed: true, updateAvailable: false,
    });
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Check for updates/i }));
    expect(await screen.findByText(/yt-dlp is up to date/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Update to/i })).not.toBeInTheDocument();
  });

  it('reports an in-use refusal', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(managedActive);
    vi.mocked(api.checkYtdlpUpdate).mockResolvedValue({
      tool: 'yt-dlp', currentVersion: '2026.10.26', latestVersion: '2026.11.05', managed: true, updateAvailable: true,
    });
    vi.mocked(api.updateYtdlp).mockRejectedValue(new Error('yt-dlp is in use by an active analysis or download. Try again when media jobs are idle.'));
    render(<YtdlpToolPanel />);
    fireEvent.click(await screen.findByRole('button', { name: /Check for updates/i }));
    fireEvent.click(await screen.findByRole('button', { name: /Update to/i }));
    expect(await screen.findByRole('alert')).toHaveTextContent(/in use by an active analysis or download/i);
  });

  it('disables actions while an operation is busy', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(status({ busy: true }));
    render(<YtdlpToolPanel />);
    expect(await screen.findByRole('button', { name: /Install yt-dlp/i })).toBeDisabled();
  });

  it('never renders executable paths or download URLs', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(managedActive);
    const { container } = render(<YtdlpToolPanel />);
    await screen.findByText(/Signed release verified/i);
    const text = container.textContent ?? '';
    expect(text).not.toMatch(/[A-Za-z]:\\/);
    expect(text).not.toMatch(/https?:\/\//);
    expect(text).not.toMatch(/githubusercontent/);
  });

  it('offers no actions on unsupported platforms', async () => {
    vi.mocked(api.getYtdlpToolStatus).mockResolvedValue(status({ platformSupported: false, canInstall: false }));
    render(<YtdlpToolPanel />);
    expect(await screen.findByText(/Windows \(64-bit\) only/i)).toBeInTheDocument();
    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });
});
