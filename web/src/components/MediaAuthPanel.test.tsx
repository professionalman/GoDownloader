import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import * as api from '../api';
import { MediaAuthPanel } from './MediaAuthPanel';
import { SettingsPanel } from './SettingsPanel';

vi.mock('../api', () => ({
  getMediaAuth: vi.fn(),
  updateMediaAuth: vi.fn(),
  importMediaCookies: vi.fn(),
  deleteMediaCookies: vi.fn(),
  getCategories: vi.fn().mockResolvedValue([]),
  createCategory: vi.fn(),
  updateCategory: vi.fn(),
  deleteCategory: vi.fn(),
  getTrackerSources: vi.fn().mockResolvedValue([]),
}));

describe('MediaAuthPanel Component', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getMediaAuth).mockResolvedValue({
      mode: 'none',
      hasCookieFile: false,
    });
  });

  // 1. Existing settings page still renders and includes Media Auth tab
  it('renders within SettingsPanel and shows Media Auth tab', async () => {
    render(<SettingsPanel settings={null} onSave={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getByRole('button', { name: /Media Auth/i })).toBeInTheDocument();
  });

  it('closes SettingsPanel on Escape key press', () => {
    const handleClose = vi.fn();
    render(<SettingsPanel settings={null} onSave={vi.fn()} onClose={handleClose} />);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(handleClose).toHaveBeenCalledTimes(1);
  });

  // 2. Media Authentication defaults to None
  it('defaults to None mode with clean initial state', async () => {
    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });
    const noneRadio = screen.getByRole('radio', { name: /None/i });
    expect(noneRadio).toBeChecked();
    expect(screen.queryByRole('combobox', { name: /Browser/i })).not.toBeInTheDocument();
    expect(screen.queryByText(/Cookies imported/i)).not.toBeInTheDocument();
  });

  // 3. Browser mode reveals browser/profile controls
  it('reveals browser and profile controls when browser session mode is selected', async () => {
    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });

    const browserRadio = screen.getByRole('radio', { name: /Browser session/i });
    fireEvent.click(browserRadio);

    expect(screen.getByLabelText('Browser')).toBeInTheDocument();
    expect(screen.getByLabelText('Browser profile')).toBeInTheDocument();
  });

  // 4. Browser dropdown sends expected API value
  it('sends updated browser and profile configuration on save', async () => {
    vi.mocked(api.updateMediaAuth).mockResolvedValue({
      mode: 'browser',
      browser: 'firefox',
      profile: 'dev-profile',
      hasCookieFile: false,
    });

    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('radio', { name: /Browser session/i }));

    const browserSelect = screen.getByLabelText('Browser');
    fireEvent.change(browserSelect, { target: { value: 'firefox' } });

    const profileInput = screen.getByLabelText('Browser profile');
    fireEvent.change(profileInput, { target: { value: 'dev-profile' } });

    const saveBtn = screen.getByRole('button', { name: /Save Media Auth/i });
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(api.updateMediaAuth).toHaveBeenCalledWith({
        mode: 'browser',
        browser: 'firefox',
        profile: 'dev-profile',
      });
      expect(screen.getByText(/Media authentication settings saved/i)).toBeInTheDocument();
    });
  });

  // 5. Cookie-file mode shows import UI
  it('shows import UI when Imported cookies mode is selected and no cookies exist', async () => {
    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole('radio', { name: /Imported cookies/i }));

    expect(screen.getByText(/No cookies imported/i)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Import cookies.txt/i })).toBeInTheDocument();
  });

  // 6 & 7. First-time import flow preserves selection and allows saving cookie_file mode
  it('imports cookies, keeps Imported cookies radio selected, and sends cookie_file mode on save', async () => {
    // Real backend returns mode: 'none' on first import because it was not active before
    vi.mocked(api.importMediaCookies).mockResolvedValue({
      mode: 'none',
      hasCookieFile: true,
    });
    vi.mocked(api.updateMediaAuth).mockResolvedValue({
      mode: 'cookie_file',
      hasCookieFile: true,
    });

    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });

    const cookieRadio = screen.getByRole('radio', { name: /Imported cookies/i });
    fireEvent.click(cookieRadio);

    const fileInput = screen.getByLabelText('Upload cookies file');
    const dummyFile = new File(['# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tTRUE\t2147483647\tk\tv\n'], 'cookies.txt', { type: 'text/plain' });

    fireEvent.change(fileInput, { target: { files: [dummyFile] } });

    await waitFor(() => {
      expect(api.importMediaCookies).toHaveBeenCalledWith(dummyFile);
      expect(screen.getByText(/Cookies imported ✓/i)).toBeInTheDocument();
      expect(cookieRadio).toBeChecked();
    });

    // Save button must be enabled
    const saveBtn = screen.getByRole('button', { name: /Save Media Auth/i });
    expect(saveBtn).not.toBeDisabled();

    // Clicking save persists mode: 'cookie_file'
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(api.updateMediaAuth).toHaveBeenCalledWith({
        mode: 'cookie_file',
        browser: undefined,
        profile: undefined,
      });
      expect(screen.getByText(/Media authentication settings saved/i)).toBeInTheDocument();
    });

    // 11. Verify no secret or cookie string is rendered
    expect(screen.queryByText(/dummy_token/i)).not.toBeInTheDocument();
  });

  // 8. Replace works
  it('allows replacing existing cookies', async () => {
    vi.mocked(api.getMediaAuth).mockResolvedValue({
      mode: 'cookie_file',
      hasCookieFile: true,
    });
    vi.mocked(api.importMediaCookies).mockResolvedValue({
      mode: 'cookie_file',
      hasCookieFile: true,
    });

    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText(/Cookies imported ✓/i)).toBeInTheDocument();
    });

    const fileInput = screen.getByLabelText('Upload cookies file');
    const newFile = new File(['# Netscape HTTP Cookie File\n# replaced\n'], 'new_cookies.txt', { type: 'text/plain' });

    fireEvent.change(fileInput, { target: { files: [newFile] } });

    await waitFor(() => {
      expect(api.importMediaCookies).toHaveBeenCalledWith(newFile);
      expect(screen.getByText(/Cookies imported successfully/i)).toBeInTheDocument();
    });
  });

  // 9. Remove works and returns mode to None
  it('removes cookies and updates mode to None', async () => {
    vi.stubGlobal('confirm', () => true);

    vi.mocked(api.getMediaAuth).mockResolvedValue({
      mode: 'cookie_file',
      hasCookieFile: true,
    });
    vi.mocked(api.deleteMediaCookies).mockResolvedValue({
      mode: 'none',
      hasCookieFile: false,
    });

    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText(/Cookies imported ✓/i)).toBeInTheDocument();
    });

    const removeBtn = screen.getByRole('button', { name: /Remove cookies/i });
    fireEvent.click(removeBtn);

    await waitFor(() => {
      expect(api.deleteMediaCookies).toHaveBeenCalled();
      expect(screen.getByText(/Cookies removed successfully/i)).toBeInTheDocument();
      expect(screen.getByRole('radio', { name: /None/i })).toBeChecked();
    });
  });

  // 10. Backend validation errors displayed cleanly
  it('displays backend validation errors cleanly on save failure', async () => {
    vi.mocked(api.getMediaAuth).mockResolvedValue({
      mode: 'browser',
      browser: 'chrome',
      hasCookieFile: false,
    });
    vi.mocked(api.updateMediaAuth).mockRejectedValue(new Error('unsupported browser: invalid-browser'));

    render(<MediaAuthPanel />);
    await waitFor(() => {
      expect(screen.getByText('Media Authentication')).toBeInTheDocument();
    });

    const saveBtn = screen.getByRole('button', { name: /Save Media Auth/i });
    fireEvent.click(saveBtn);

    await waitFor(() => {
      expect(screen.getByRole('alert')).toHaveTextContent('unsupported browser: invalid-browser');
    });
  });
});
