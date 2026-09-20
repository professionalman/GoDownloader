import { setBackendClient } from './api';
import { mountApp } from './bootstrap';
import { desktopBackendClient } from './transport/desktop';

// 1. Install native Wails IPC transport before React mounts
setBackendClient(desktopBackendClient);

// 2. Mount framework-neutral React application
mountApp();
