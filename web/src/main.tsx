import { mountApp } from './bootstrap';
import { initSession } from './transport/http';

// In browser HTTP mode, proactively bootstrap the session to obtain CSRF cookie/token.
// Even if initSession fails (e.g. backend offline at startup), mountApp proceeds
// so the UI can mount and display the offline/retry state without a blank-page crash.
initSession();
mountApp();
