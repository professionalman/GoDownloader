import React, { useState, useEffect } from 'react';
import type { AppSettings } from '../types';

interface SettingsPanelProps {
  settings: AppSettings | null;
  onSave: (maxConcurrentDownloads: number) => Promise<void>;
  onClose: () => void;
}

export const SettingsPanel: React.FC<SettingsPanelProps> = ({ settings, onSave, onClose }) => {
  const [val, setVal] = useState<number>(settings?.maxConcurrentDownloads || 3);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    if (settings) {
      setVal(settings.maxConcurrentDownloads);
    }
  }, [settings]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (val < 1 || val > 20) {
      setError('Max concurrent downloads must be between 1 and 20');
      return;
    }

    try {
      setSaving(true);
      setError('');
      setSuccess('');
      await onSave(val);
      setSuccess('Settings updated successfully!');
      setTimeout(() => setSuccess(''), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update settings');
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="modal-overlay">
      <div className="modal-content settings-modal">
        <div className="modal-header">
          <h3>⚙️ Application Settings</h3>
          <button type="button" className="btn-close" onClick={onClose}>×</button>
        </div>

        <form onSubmit={handleSubmit} className="settings-form">
          <div className="setting-group">
            <label htmlFor="max-concurrent-input" className="setting-label">
              Max Concurrent Downloads
            </label>
            <div className="setting-control-row">
              <input
                id="max-concurrent-input"
                type="number"
                min={1}
                max={20}
                className="input-number"
                value={val}
                onChange={(e) => setVal(parseInt(e.target.value, 10) || 1)}
                disabled={saving}
              />
              <span className="setting-hint">(Range: 1 – 20)</span>
            </div>
            {settings?.maxConcurrentSource === 'env' && (
              <div className="setting-notice">
                ℹ️ Currently overridden by <code>MAX_CONCURRENT_DOWNLOADS</code> environment variable.
              </div>
            )}
          </div>

          {error && <div className="form-error">{error}</div>}
          {success && <div className="form-success">{success}</div>}

          <div className="modal-actions">
            <button type="button" className="btn btn-secondary" onClick={onClose} disabled={saving}>
              Cancel
            </button>
            <button type="submit" className="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : 'Save Settings'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};
