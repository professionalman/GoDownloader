import { StrictMode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import './index.css';
import App from './App';

export interface MountAppOptions {
  container?: HTMLElement | null;
}

/**
 * Mounts the GoDownloader React application into the specified DOM container.
 * This is the framework-neutral React application entrypoint shared across all hosts
 * (browser web host and future desktop host).
 *
 * It contains zero HTTP, cookie, CSRF, or SSE transport concepts. Host-specific
 * transport configuration (such as installing a native desktop BackendClient or initiating
 * HTTP session bootstrap) is the responsibility of the host bootstrap entrypoint
 * before or during mount.
 */
export function mountApp(options: MountAppOptions = {}): Root {
  const container = options.container ?? document.getElementById('root');
  if (!container) {
    throw new Error('Failed to find root container element');
  }
  const root = createRoot(container);
  root.render(
    <StrictMode>
      <App />
    </StrictMode>,
  );
  return root;
}
