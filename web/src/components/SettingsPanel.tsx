import {
  Activity,
  ArrowDownToLine,
  FolderTree,
  Gauge,
  HardDrive,
  Lock,
  Magnet,
  Network,
  Pencil,
  Plus,
  Radio,
  Settings2,
  Trash2,
  X,
} from 'lucide-react';
import { useEffect, useState } from 'react';
import { createCategory, deleteCategory, getCategories, updateCategory } from '../api';
import type { AppSettings, Category, FilenameConflictPolicy, UpdateSettingsPayload } from '../types';
import { cx } from '../downloadUi';
import { PowerSettingsPanel } from './PowerSettingsPanel';

interface SettingsPanelProps {
  settings: AppSettings | null;
  onSave: (payload: UpdateSettingsPayload) => Promise<void>;
  onClose: () => void;
}

type SettingsSection = 'general' | 'storage' | 'categories' | 'network' | 'direct' | 'torrents' | 'trackers' | 'dependencies';

const sections: Array<{ id: SettingsSection; label: string; icon: typeof Settings2 }> = [
  { id: 'general', label: 'General', icon: Settings2 },
  { id: 'storage', label: 'Storage', icon: HardDrive },
  { id: 'categories', label: 'Categories', icon: FolderTree },
  { id: 'network', label: 'Network', icon: Network },
  { id: 'direct', label: 'Direct Downloads', icon: ArrowDownToLine },
  { id: 'torrents', label: 'Torrents', icon: Magnet },
  { id: 'trackers', label: 'Tracker Sources', icon: Radio },
  { id: 'dependencies', label: 'Dependencies', icon: Activity },
];

const inputClass = 'h-9 w-full rounded-md border border-border bg-surface-2 px-3 text-sm text-foreground outline-none focus:border-primary disabled:cursor-not-allowed disabled:opacity-55';

function SectionTitle({ title, description }: { title: string; description: string }) {
  return (
    <div>
      <h3 className="text-base font-semibold tracking-tight">{title}</h3>
      <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
    </div>
  );
}

function Field({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <label className="block space-y-1.5 text-xs font-medium text-muted-foreground">
      <span>{label}</span>
      {children}
      {hint && <span className="block font-normal text-muted-foreground/80">{hint}</span>}
    </label>
  );
}

export function SettingsPanel({ settings, onSave, onClose }: SettingsPanelProps) {
  const [section, setSection] = useState<SettingsSection>('general');
  const [maxConcurrent, setMaxConcurrent] = useState(settings?.queue?.maxConcurrentDownloads || settings?.maxConcurrentDownloads || 3);
  const [downloadDir, setDownloadDir] = useState(settings?.storage?.defaultDownloadDirectory || '');
  const [tempDir, setTempDir] = useState(settings?.storage?.temporaryDirectory || '');
  const [minFreeSpaceGiB, setMinFreeSpaceGiB] = useState((settings?.storage?.minimumFreeSpaceBytes || 1073741824) / 1024 ** 3);
  const [conflictPolicy, setConflictPolicy] = useState<FilenameConflictPolicy>(settings?.storage?.defaultConflictPolicy || 'rename');
  const [categories, setCategories] = useState<Category[]>([]);
  const [catName, setCatName] = useState('');
  const [catDir, setCatDir] = useState('');
  const [editingCatId, setEditingCatId] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  useEffect(() => {
    if (!settings) return;
    setMaxConcurrent(settings.queue?.maxConcurrentDownloads || settings.maxConcurrentDownloads || 3);
    if (settings.storage) {
      setDownloadDir(settings.storage.defaultDownloadDirectory);
      setTempDir(settings.storage.temporaryDirectory);
      setMinFreeSpaceGiB(settings.storage.minimumFreeSpaceBytes / 1024 ** 3);
      setConflictPolicy(settings.storage.defaultConflictPolicy);
    }
  }, [settings]);

  const loadCategories = async () => {
    try { setCategories(await getCategories()); } catch { setCategories([]); }
  };

  useEffect(() => { void loadCategories(); }, []);

  const saveCoreSettings = async () => {
    if (maxConcurrent < 1 || maxConcurrent > 20) throw new Error('Max concurrent downloads must be between 1 and 20');
    if (minFreeSpaceGiB < 0) throw new Error('Minimum free space must be non-negative');
    await onSave({
      queue: { maxConcurrentDownloads: maxConcurrent },
      storage: {
        defaultDownloadDirectory: downloadDir,
        temporaryDirectory: tempDir,
        minimumFreeSpaceBytes: Math.round(minFreeSpaceGiB * 1024 ** 3),
        defaultConflictPolicy: conflictPolicy,
      },
    });
  };

  const handleSaveSettings = async () => {
    try {
      setSaving(true);
      setError('');
      setSuccess('');
      await saveCoreSettings();
      setSuccess('Settings updated successfully.');
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Failed to update settings');
    } finally {
      setSaving(false);
    }
  };

  const handleSaveCategory = async () => {
    if (!catName.trim() || !catDir.trim()) {
      setError('Category name and directory are required');
      return;
    }
    try {
      setError('');
      if (editingCatId) await updateCategory(editingCatId, { name: catName.trim(), directory: catDir.trim() });
      else await createCategory({ name: catName.trim(), directory: catDir.trim() });
      setEditingCatId(null);
      setCatName('');
      setCatDir('');
      await loadCategories();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Failed to save category');
    }
  };

  const handleDeleteCategory = async (id: string) => {
    if (!confirm('Delete this category? Existing jobs retain their destination folders.')) return;
    try {
      await deleteCategory(id);
      await loadCategories();
    } catch (reason: unknown) {
      setError(reason instanceof Error ? reason.message : 'Failed to delete category');
    }
  };

  const powerSection = section === 'network' || section === 'direct' || section === 'torrents' || section === 'trackers';

  return (
    <div className="fixed inset-0 z-50 grid bg-black/75 sm:place-items-center sm:p-4" onMouseDown={(event) => {
      if (event.target === event.currentTarget && !saving) onClose();
    }}>
      <section className="flex h-[100dvh] w-full flex-col overflow-hidden bg-background sm:h-[86vh] sm:max-w-5xl sm:rounded-lg sm:border sm:border-border" role="dialog" aria-modal="true" aria-labelledby="settings-title">
        <header className="flex shrink-0 items-start gap-3 border-b border-border px-4 py-3">
          <span className="grid size-9 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground"><Settings2 className="size-4" /></span>
          <div className="min-w-0 flex-1">
            <h2 id="settings-title" className="text-base font-semibold">Settings</h2>
            <p className="text-xs text-muted-foreground">Configure GoDownloader storage, network, torrent, and engine defaults.</p>
          </div>
          <button type="button" className="grid size-8 place-items-center rounded-md text-muted-foreground hover:bg-surface-2 hover:text-foreground" onClick={onClose} aria-label="Close settings"><X className="size-4" /></button>
        </header>

        <div className="flex min-h-0 flex-1 flex-col md:flex-row">
          <nav className="scrollbar-thin flex shrink-0 gap-1.5 overflow-x-auto border-b border-border bg-sidebar p-2 md:w-56 md:flex-col md:overflow-y-auto md:border-b-0 md:border-r" aria-label="Settings sections">
            {sections.map((item) => {
              const Icon = item.icon;
              return (
                <button key={item.id} type="button" onClick={() => setSection(item.id)} aria-current={section === item.id ? 'true' : undefined} className={cx('flex h-8 shrink-0 items-center gap-2 whitespace-nowrap rounded-md px-3 text-sm transition-colors', section === item.id ? 'bg-sidebar-accent text-foreground' : 'text-muted-foreground hover:bg-sidebar-accent/60 hover:text-foreground')}>
                  <Icon className="size-4 shrink-0" /> {item.label}
                </button>
              );
            })}
          </nav>

          <form onSubmit={(event) => { event.preventDefault(); void handleSaveSettings(); }} className="scrollbar-thin min-w-0 flex-1 overflow-y-auto p-4 sm:p-5">
            {section === 'general' && (
              <div className="max-w-md space-y-5">
                <SectionTitle title="General" description="Global behaviour of the download scheduler." />
                <Field label="Maximum concurrent downloads" hint="Applies across all engines. Accepted range: 1–20.">
                  <input id="max-concurrent-input" aria-label="Max Concurrent Downloads" type="number" min={1} max={20} className={inputClass} value={maxConcurrent} onChange={(event) => setMaxConcurrent(Number(event.target.value) || 1)} disabled={saving} />
                </Field>
              </div>
            )}

            {section === 'storage' && (
              <div className="max-w-xl space-y-5">
                <SectionTitle title="Storage" description="Where completed and temporary files are written on this host." />
                <Field label="Default download directory">
                  <input id="default-download-dir" className={inputClass} value={downloadDir} onChange={(event) => setDownloadDir(event.target.value)} disabled={saving || settings?.storage?.overrides?.defaultDownloadDirectory} />
                  {settings?.storage?.overrides?.defaultDownloadDirectory && <span className="inline-flex items-center gap-1 text-xs text-muted-foreground"><Lock className="size-3" /> Controlled by DOWNLOAD_DIR</span>}
                </Field>
                <Field label="Temporary working directory">
                  <input id="temp-dir" className={inputClass} value={tempDir} onChange={(event) => setTempDir(event.target.value)} disabled={saving || settings?.storage?.overrides?.temporaryDirectory} />
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="Minimum free-space reserve (GiB)"><input id="min-free-space" type="number" step="0.1" min="0" className={inputClass} value={minFreeSpaceGiB} onChange={(event) => setMinFreeSpaceGiB(Number(event.target.value) || 0)} disabled={saving || settings?.storage?.overrides?.minimumFreeSpaceBytes} /></Field>
                  <Field label="Filename conflict policy"><select id="default-conflict-policy" className={inputClass} value={conflictPolicy} onChange={(event) => setConflictPolicy(event.target.value as FilenameConflictPolicy)} disabled={saving || settings?.storage?.overrides?.defaultConflictPolicy}><option value="rename">Rename with suffix</option><option value="overwrite">Overwrite existing</option><option value="fail">Fail download</option></select></Field>
                </div>
              </div>
            )}

            {section === 'categories' && (
              <div className="space-y-4">
                <SectionTitle title="Categories" description="Map reusable category names to destination folders." />
                <ul className="space-y-2">
                  {categories.map((category) => (
                    <li key={category.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-lg border border-border bg-surface p-3">
                      <div className="min-w-0"><p className="truncate text-sm font-medium">{category.name}</p><p className="truncate text-xs text-muted-foreground">Configured: {category.directory}</p>{category.resolvedDirectory && <p className="truncate text-xs text-muted-foreground">Resolved: {category.resolvedDirectory}</p>}</div>
                      <div className="flex gap-1.5">
                        <button type="button" className="grid size-8 place-items-center rounded-md border border-border bg-surface-2 text-muted-foreground hover:text-foreground" aria-label={`Edit ${category.name}`} onClick={() => { setEditingCatId(category.id); setCatName(category.name); setCatDir(category.directory); }}><Pencil className="size-3.5" /></button>
                        <button type="button" className="grid size-8 place-items-center rounded-md border border-destructive/40 bg-destructive/10 text-destructive hover:bg-destructive/20" aria-label={`Delete ${category.name}`} onClick={() => void handleDeleteCategory(category.id)}><Trash2 className="size-3.5" /></button>
                      </div>
                    </li>
                  ))}
                </ul>
                <div className="grid gap-2 rounded-lg border border-border bg-surface p-3 sm:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_auto]">
                  <input aria-label="Category name" className={inputClass} placeholder="Category name" value={catName} onChange={(event) => setCatName(event.target.value)} />
                  <input aria-label="Category directory" className={inputClass} placeholder="Folder or absolute path" value={catDir} onChange={(event) => setCatDir(event.target.value)} />
                  <button type="button" className="inline-flex h-9 items-center justify-center gap-1.5 rounded-md bg-primary px-3 text-sm font-medium text-primary-foreground" onClick={() => void handleSaveCategory()}><Plus className="size-4" />{editingCatId ? 'Update' : 'Add'}</button>
                </div>
              </div>
            )}

            {powerSection && <PowerSettingsPanel settings={settings} onSave={onSave} activeSection={section} />}

            {section === 'dependencies' && (
              <div className="space-y-4">
                <SectionTitle title="Dependencies" description="Read-only results reported by the backend. Secrets are never displayed." />
                {settings?.applicationResults?.length ? (
                  <ul className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
                    {settings.applicationResults.map((result, index) => (
                      <li key={`${result.target}-${index}`} className="rounded-lg border border-border bg-surface p-3">
                        <div className="flex items-center justify-between gap-2"><p className="truncate text-sm font-medium">{result.target}</p><span className={cx('rounded-md border px-1.5 py-0.5 text-xs', result.status === 'applied' || result.status === 'ok' ? 'border-success/35 bg-success/10 text-success' : 'border-warning/35 bg-warning/10 text-warning')}>{result.status}</span></div>
                        {result.code && <p className="mt-1 text-xs text-muted-foreground">{result.code}</p>}
                        {result.message && <p className="mt-1 text-xs text-muted-foreground">{result.message}</p>}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <div className="rounded-lg border border-dashed border-border bg-surface/50 px-4 py-10 text-center"><Gauge className="mx-auto size-5 text-muted-foreground" /><p className="mt-2 text-sm font-medium">No dependency application results</p><p className="mt-1 text-xs text-muted-foreground">Engine availability remains capability-driven and is reported on each job.</p></div>
                )}
              </div>
            )}
          </form>
        </div>

        {(error || success) && <div className={cx('mx-4 mb-3 rounded-md border px-3 py-2 text-xs', error ? 'border-destructive/40 bg-destructive/10 text-destructive' : 'border-success/35 bg-success/10 text-success')} role={error ? 'alert' : 'status'}>{error || success}</div>}
        <footer className="flex shrink-0 items-center justify-end gap-2 border-t border-border bg-background px-4 py-3">
          <button type="button" className="h-9 rounded-md border border-border bg-surface-2 px-4 text-sm" onClick={onClose} disabled={saving}>Cancel</button>
          {(section === 'general' || section === 'storage') && <button type="button" className="h-9 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground disabled:opacity-50" onClick={() => void handleSaveSettings()} disabled={saving}>{saving ? 'Saving…' : 'Save changes'}</button>}
        </footer>
      </section>
    </div>
  );
}
