import React, { useState, useEffect } from 'react';
import type { AppSettings, Category, FilenameConflictPolicy, UpdateSettingsPayload } from '../types';
import { getCategories, createCategory, updateCategory, deleteCategory } from '../api';

interface SettingsPanelProps {
  settings: AppSettings | null;
  onSave: (payload: UpdateSettingsPayload) => Promise<void>;
  onClose: () => void;
}

export const SettingsPanel: React.FC<SettingsPanelProps> = ({ settings, onSave, onClose }) => {
  const [maxConcurrent, setMaxConcurrent] = useState<number>(
    settings?.queue?.maxConcurrentDownloads || settings?.maxConcurrentDownloads || 3
  );

  const [downloadDir, setDownloadDir] = useState<string>(
    settings?.storage?.defaultDownloadDirectory || ''
  );
  const [tempDir, setTempDir] = useState<string>(
    settings?.storage?.temporaryDirectory || ''
  );
  const [minFreeSpaceGiB, setMinFreeSpaceGiB] = useState<number>(
    (settings?.storage?.minimumFreeSpaceBytes || 1073741824) / (1024 * 1024 * 1024)
  );
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>(
    settings?.storage?.defaultConflictPolicy || 'rename'
  );

  const [categories, setCategories] = useState<Category[]>([]);
  const [catName, setCatName] = useState('');
  const [catDir, setCatDir] = useState('');
  const [editingCatId, setEditingCatId] = useState<string | null>(null);

  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    if (settings) {
      if (settings.queue) {
        setMaxConcurrent(settings.queue.maxConcurrentDownloads);
      } else if (settings.maxConcurrentDownloads) {
        setMaxConcurrent(settings.maxConcurrentDownloads);
      }
      if (settings.storage) {
        setDownloadDir(settings.storage.defaultDownloadDirectory);
        setTempDir(settings.storage.temporaryDirectory);
        setMinFreeSpaceGiB(settings.storage.minimumFreeSpaceBytes / (1024 * 1024 * 1024));
        setConflictPolicy(settings.storage.defaultConflictPolicy);
      }
    }
  }, [settings]);

  const loadCategories = async () => {
    try {
      const cats = await getCategories();
      setCategories(cats);
    } catch {
      // ignore
    }
  };

  useEffect(() => {
    loadCategories();
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (maxConcurrent < 1 || maxConcurrent > 20) {
      setError('Max concurrent downloads must be between 1 and 20');
      return;
    }
    if (minFreeSpaceGiB < 0) {
      setError('Minimum free space must be non-negative');
      return;
    }

    try {
      setSaving(true);
      setError('');
      setSuccess('');

      const payload: UpdateSettingsPayload = {
        queue: { maxConcurrentDownloads: maxConcurrent },
        storage: {
          defaultDownloadDirectory: downloadDir,
          temporaryDirectory: tempDir,
          minimumFreeSpaceBytes: Math.round(minFreeSpaceGiB * 1024 * 1024 * 1024),
          defaultConflictPolicy: conflictPolicy,
        },
      };

      await onSave(payload);
      setSuccess('Settings updated successfully!');
      setTimeout(() => setSuccess(''), 3000);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update settings');
    } finally {
      setSaving(false);
    }
  };

  const handleSaveCategory = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!catName.trim() || !catDir.trim()) {
      setError('Category name and directory are required');
      return;
    }
    try {
      setError('');
      if (editingCatId) {
        await updateCategory(editingCatId, { name: catName.trim(), directory: catDir.trim() });
        setEditingCatId(null);
      } else {
        await createCategory({ name: catName.trim(), directory: catDir.trim() });
      }
      setCatName('');
      setCatDir('');
      await loadCategories();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save category');
    }
  };

  const handleEditCat = (cat: Category) => {
    setEditingCatId(cat.id);
    setCatName(cat.name);
    setCatDir(cat.directory);
  };

  const handleDeleteCat = async (id: string) => {
    if (!confirm('Are you sure you want to delete this category? Existing jobs using this category will retain their destination folders.')) {
      return;
    }
    try {
      setError('');
      await deleteCategory(id);
      await loadCategories();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete category');
    }
  };

  return (
    <div className="modal-overlay">
      <div className="modal-content settings-modal" style={{ maxWidth: '650px', maxHeight: '90vh', overflowY: 'auto' }}>
        <div className="modal-header">
          <h3>⚙️ Application Settings</h3>
          <button type="button" className="btn-close" onClick={onClose}>×</button>
        </div>

        <form onSubmit={handleSubmit} className="settings-form">
          <div className="setting-section">
            <h4>⚡ Download Queue Settings</h4>
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
                  value={maxConcurrent}
                  onChange={(e) => setMaxConcurrent(parseInt(e.target.value, 10) || 1)}
                  disabled={saving}
                />
                <span className="setting-hint">(1 – 20)</span>
              </div>
            </div>
          </div>

          <div className="setting-section" style={{ marginTop: '1.5rem' }}>
            <h4>📁 Storage & Directory Settings</h4>
            
            <div className="setting-group" style={{ marginBottom: '1rem' }}>
              <label htmlFor="default-download-dir" className="setting-label">
                Default Download Directory
              </label>
              <input
                id="default-download-dir"
                type="text"
                className="input-text"
                style={{ width: '100%' }}
                value={downloadDir}
                onChange={(e) => setDownloadDir(e.target.value)}
                disabled={saving || settings?.storage?.overrides?.defaultDownloadDirectory}
              />
              {settings?.storage?.overrides?.defaultDownloadDirectory && (
                <span className="setting-notice">ℹ️ Overridden by DOWNLOAD_DIR env</span>
              )}
            </div>

            <div className="setting-group" style={{ marginBottom: '1rem' }}>
              <label htmlFor="temp-dir" className="setting-label">
                Temporary Working Directory
              </label>
              <input
                id="temp-dir"
                type="text"
                className="input-text"
                style={{ width: '100%' }}
                value={tempDir}
                onChange={(e) => setTempDir(e.target.value)}
                disabled={saving || settings?.storage?.overrides?.temporaryDirectory}
              />
              {settings?.storage?.overrides?.temporaryDirectory && (
                <span className="setting-notice">ℹ️ Overridden by TEMP_DIR env</span>
              )}
            </div>

            <div className="setting-group" style={{ marginBottom: '1rem' }}>
              <label htmlFor="min-free-space" className="setting-label">
                Minimum Free Disk Space Reserve (GiB)
              </label>
              <input
                id="min-free-space"
                type="number"
                step="0.1"
                min="0"
                className="input-number"
                value={minFreeSpaceGiB}
                onChange={(e) => setMinFreeSpaceGiB(parseFloat(e.target.value) || 0)}
                disabled={saving || settings?.storage?.overrides?.minimumFreeSpaceBytes}
              />
            </div>

            <div className="setting-group" style={{ marginBottom: '1rem' }}>
              <label htmlFor="default-conflict-policy" className="setting-label">
                Default Filename Conflict Policy
              </label>
              <select
                id="default-conflict-policy"
                className="select-dropdown"
                value={conflictPolicy}
                onChange={(e) => setConflictPolicy(e.target.value as FilenameConflictPolicy)}
                disabled={saving || settings?.storage?.overrides?.defaultConflictPolicy}
              >
                <option value="rename">Auto Rename (video (1).mp4)</option>
                <option value="overwrite">Overwrite Existing File</option>
                <option value="fail">Fail on Conflict</option>
              </select>
            </div>
          </div>

          <div className="setting-section" style={{ marginTop: '1.5rem' }}>
            <h4>🏷️ Download Categories</h4>
            
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.5rem', marginBottom: '1rem' }}>
              {categories.map((cat) => (
                <div key={cat.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', background: 'rgba(255,255,255,0.05)', padding: '0.5rem 0.8rem', borderRadius: '6px' }}>
                  <div>
                    <strong>{cat.name}</strong> <span style={{ opacity: 0.7, fontSize: '0.85rem' }}>({cat.directory})</span>
                    {cat.resolvedDirectory && (
                      <div style={{ fontSize: '0.75rem', opacity: 0.5 }}>→ {cat.resolvedDirectory}</div>
                    )}
                  </div>
                  <div style={{ display: 'flex', gap: '0.4rem' }}>
                    <button type="button" className="btn btn-sm btn-secondary" onClick={() => handleEditCat(cat)}>Edit</button>
                    <button type="button" className="btn btn-sm btn-danger" onClick={() => handleDeleteCat(cat.id)}>Delete</button>
                  </div>
                </div>
              ))}
            </div>

            <div style={{ display: 'flex', gap: '0.5rem', alignItems: 'center' }}>
              <input
                type="text"
                placeholder="Category Name (e.g. Movies)"
                value={catName}
                onChange={(e) => setCatName(e.target.value)}
                style={{ flex: 1, padding: '0.4rem' }}
              />
              <input
                type="text"
                placeholder="Folder (e.g. Movies or C:/Movies)"
                value={catDir}
                onChange={(e) => setCatDir(e.target.value)}
                style={{ flex: 1, padding: '0.4rem' }}
              />
              <button type="button" className="btn btn-secondary" onClick={handleSaveCategory}>
                {editingCatId ? 'Update' : 'Add Category'}
              </button>
              {editingCatId && (
                <button type="button" className="btn btn-link" onClick={() => { setEditingCatId(null); setCatName(''); setCatDir(''); }}>
                  Cancel
                </button>
              )}
            </div>
          </div>

          {error && <div className="form-error" style={{ marginTop: '1rem' }}>{error}</div>}
          {success && <div className="form-success" style={{ marginTop: '1rem' }}>{success}</div>}

          <div className="modal-actions" style={{ marginTop: '1.5rem' }}>
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
