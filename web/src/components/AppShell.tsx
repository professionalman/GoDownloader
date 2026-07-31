import type { ReactNode } from 'react';
import { Download, ListOrdered, LoaderCircle, Settings, Wifi, WifiOff } from 'lucide-react';

export type ViewMode = 'downloads' | 'queue';
export type ConnectionState = 'connecting' | 'connected' | 'reconnecting';

interface AppShellProps {
  viewMode: ViewMode;
  downloadCount: number;
  queueCount: number;
  connectionState: ConnectionState;
  onViewModeChange: (viewMode: ViewMode) => void;
  onOpenSettings: () => void;
  children: ReactNode;
}

const viewCopy: Record<ViewMode, { title: string; description: string }> = {
  downloads: {
    title: 'Downloads',
    description: 'Create downloads and monitor live progress.',
  },
  queue: {
    title: 'Smart Queue',
    description: 'Review queued jobs and manage their priority.',
  },
};

const connectionCopy: Record<ConnectionState, { label: string; tone: string }> = {
  connecting: { label: 'Connecting', tone: 'bg-info' },
  connected: { label: 'Connected', tone: 'bg-success' },
  reconnecting: { label: 'Reconnecting', tone: 'bg-warning' },
};

function Navigation({
  compact = false,
  viewMode,
  downloadCount,
  queueCount,
  onViewModeChange,
}: Pick<AppShellProps, 'viewMode' | 'downloadCount' | 'queueCount' | 'onViewModeChange'> & {
  compact?: boolean;
}) {
  const items = [
    { id: 'downloads' as const, label: 'Downloads', icon: Download, count: downloadCount },
    { id: 'queue' as const, label: 'Queue', icon: ListOrdered, count: queueCount },
  ];

  return (
    <nav
      className={compact ? 'shell-compact-nav scrollbar-thin' : 'shell-sidebar-nav'}
      aria-label={compact ? 'Compact navigation' : 'Main navigation'}
    >
      {items.map((item) => {
        const active = viewMode === item.id;
        const Icon = item.icon;
        return (
          <button
            key={item.id}
            type="button"
            className={`shell-nav-item ${compact ? 'shell-nav-item-compact' : ''} ${active ? 'is-active' : ''}`}
            aria-current={active ? 'page' : undefined}
            onClick={() => onViewModeChange(item.id)}
          >
            <Icon aria-hidden="true" />
            <span className="shell-nav-label">{item.label}</span>
            <span className="shell-count num" aria-label={`${item.count} ${item.label.toLowerCase()}`}>
              {item.count}
            </span>
          </button>
        );
      })}
      {compact && <span className="shell-compact-version">v0.7</span>}
    </nav>
  );
}

function ConnectionIndicator({ state }: { state: ConnectionState }) {
  const { label, tone } = connectionCopy[state];
  const Icon = state === 'connected' ? Wifi : state === 'connecting' ? LoaderCircle : WifiOff;

  return (
    <div className="shell-connection" aria-label={`GoDownloader ${label}`}>
      <span className={`shell-connection-dot ${tone}`} aria-hidden="true" />
      <Icon className={state === 'connecting' ? 'shell-spin' : ''} aria-hidden="true" />
      <span className="shell-connection-service">GoDownloader</span>
      <span className="shell-connection-state">{label}</span>
    </div>
  );
}

export function AppShell({
  viewMode,
  downloadCount,
  queueCount,
  connectionState,
  onViewModeChange,
  onOpenSettings,
  children,
}: AppShellProps) {
  const copy = viewCopy[viewMode];

  return (
    <div className="shell-root">
      <aside className="shell-sidebar">
        <div className="shell-brand">
          <span className="shell-brand-mark">
            <Download aria-hidden="true" />
          </span>
          <span>GoDownloader</span>
        </div>

        <Navigation
          viewMode={viewMode}
          downloadCount={downloadCount}
          queueCount={queueCount}
          onViewModeChange={onViewModeChange}
        />

        <div className="shell-sidebar-footer">
          <p>GoDownloader</p>
          <span>Version 0.7</span>
        </div>
      </aside>

      <div className="shell-page">
        <header className="shell-header">
          <div className="shell-header-row">
            <div className="shell-heading">
              <h1>{copy.title}</h1>
              <p>{copy.description}</p>
            </div>

            <div className="shell-header-actions">
              <ConnectionIndicator state={connectionState} />
              <button type="button" className="shell-settings" onClick={onOpenSettings}>
                <Settings aria-hidden="true" />
                <span>Settings</span>
              </button>
            </div>
          </div>

          <Navigation
            compact
            viewMode={viewMode}
            downloadCount={downloadCount}
            queueCount={queueCount}
            onViewModeChange={onViewModeChange}
          />
        </header>

        <main className="shell-main">
          <div className="shell-content">{children}</div>
        </main>
      </div>
    </div>
  );
}