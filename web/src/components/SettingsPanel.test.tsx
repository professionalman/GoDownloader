import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { SettingsPanel } from './SettingsPanel';
import * as api from '../api';

vi.mock('../api', () => ({
  createCategory: vi.fn(),
  deleteCategory: vi.fn(),
  getCategories: vi.fn().mockResolvedValue([]),
  updateCategory: vi.fn(),
  getDesktopPreferences: vi.fn(),
  setCloseToTray: vi.fn(),
  setAutostart: vi.fn(),
}));

describe('SettingsPanel Desktop Preferences Integration', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const defaultProps = {
    settings: {
      queue: { maxConcurrentDownloads: 3 },
      storage: {
        defaultDownloadDirectory: 'C:\\Downloads',
        effectiveDefaultDownloadDirectory: 'C:\\Downloads',
        temporaryDirectory: 'C:\\Temp',
        effectiveTemporaryDirectory: 'C:\\Temp',
        minimumFreeSpaceBytes: 1073741824,
        effectiveMinimumFreeSpaceBytes: 1073741824,
        defaultConflictPolicy: 'rename' as const,
        effectiveDefaultConflictPolicy: 'rename' as const,
        overrides: {
          defaultDownloadDirectory: false,
          temporaryDirectory: false,
          minimumFreeSpaceBytes: false,
          defaultConflictPolicy: false,
        },
      },
      network: {
        globalDownloadLimitBytesPerSecond: 0,
        proxy: { mode: 'disabled' as const },
        userAgent: '',
        httpHeaders: [],
        retryPolicy: { maxAttempts: 3, retryWaitSeconds: 5 },
        timeoutPolicy: { connectTimeoutSeconds: 30, requestTimeoutSeconds: 60 },
        directConnections: { split: 5, maxConnectionsPerServer: 1, minSplitSizeBytes: 20971520 },
      },
      torrent: {
        downloadLimitBytesPerSecond: 0,
        uploadLimitBytesPerSecond: 0,
        seedingPolicy: { mode: 'none' as const },
        applyTrackerSubscriptionsToNewTorrents: false,
        manageQBitGlobalNetworkSettings: false,
      },
    },
    onSave: vi.fn().mockResolvedValue(undefined),
    onClose: vi.fn(),
  };

  it('hides desktop integration controls in browser mode (getDesktopPreferences returns null)', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue(null);

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(api.getDesktopPreferences).toHaveBeenCalled();
    });

    // In browser mode, desktop-specific controls should not be rendered
    expect(screen.queryByText('Desktop Integration')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Close to tray')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Start GoDownloader with Windows')).not.toBeInTheDocument();
  });

  it('hides desktop integration controls if getDesktopPreferences resolves undefined', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue(undefined as any);

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(api.getDesktopPreferences).toHaveBeenCalled();
    });

    expect(screen.queryByText('Desktop Integration')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Close to tray')).not.toBeInTheDocument();
    expect(screen.queryByLabelText('Start GoDownloader with Windows')).not.toBeInTheDocument();
  });

  it('renders desktop controls with correct initial state when desktop preferences are available', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByText('Desktop Integration')).toBeInTheDocument();
    });

    const closeToTray = screen.getByLabelText('Close to tray') as HTMLInputElement;
    const autostart = screen.getByLabelText('Start GoDownloader with Windows') as HTMLInputElement;

    expect(closeToTray).toBeChecked();
    expect(autostart).not.toBeChecked();
  });

  it('updates close-to-tray preference successfully when toggled', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });
    vi.mocked(api.setCloseToTray).mockResolvedValue({
      closeToTray: false,
      autostartEnabled: false,
    });

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Close to tray')).toBeInTheDocument();
    });

    const closeToTray = screen.getByLabelText('Close to tray') as HTMLInputElement;
    fireEvent.click(closeToTray);

    await waitFor(() => {
      expect(api.setCloseToTray).toHaveBeenCalledWith(false);
      expect(closeToTray).not.toBeChecked();
    });
  });

  it('displays error message when close-to-tray update fails', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });
    vi.mocked(api.setCloseToTray).mockRejectedValue(new Error('Failed to persist setting to database'));

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Close to tray')).toBeInTheDocument();
    });

    const closeToTray = screen.getByLabelText('Close to tray') as HTMLInputElement;
    fireEvent.click(closeToTray);

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Failed to persist setting to database');
    });
  });

  it('enables autostart and updates UI when toggled ON', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });
    vi.mocked(api.setAutostart).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: true,
    });

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Start GoDownloader with Windows')).toBeInTheDocument();
    });

    const autostart = screen.getByLabelText('Start GoDownloader with Windows') as HTMLInputElement;
    fireEvent.click(autostart);

    await waitFor(() => {
      expect(api.setAutostart).toHaveBeenCalledWith(true);
      expect(autostart).toBeChecked();
    });
  });

  it('disables autostart when toggled OFF', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: true,
    });
    vi.mocked(api.setAutostart).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Start GoDownloader with Windows')).toBeInTheDocument();
    });

    const autostart = screen.getByLabelText('Start GoDownloader with Windows') as HTMLInputElement;
    expect(autostart).toBeChecked();
    fireEvent.click(autostart);

    await waitFor(() => {
      expect(api.setAutostart).toHaveBeenCalledWith(false);
      expect(autostart).not.toBeChecked();
    });
  });

  it('displays error and rolls back / stays truthful when autostart fails', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });
    vi.mocked(api.setAutostart).mockRejectedValue(new Error('Access is denied to registry'));

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Start GoDownloader with Windows')).toBeInTheDocument();
    });

    const autostart = screen.getByLabelText('Start GoDownloader with Windows') as HTMLInputElement;
    fireEvent.click(autostart);

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('Access is denied to registry');
      expect(autostart).not.toBeChecked();
    });
  });

  it('supports keyboard interaction on checkboxes and Escape key for dismissal', async () => {
    vi.mocked(api.getDesktopPreferences).mockResolvedValue({
      closeToTray: true,
      autostartEnabled: false,
    });

    render(<SettingsPanel {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByLabelText('Close to tray')).toBeInTheDocument();
    });

    const closeToTray = screen.getByLabelText('Close to tray');
    closeToTray.focus();
    expect(closeToTray).toHaveFocus();

    // Escape dismissal
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(defaultProps.onClose).toHaveBeenCalled();
  });
});
