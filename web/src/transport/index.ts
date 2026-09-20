import type { BackendClient } from './types';
import { httpBackendClient } from './http';

export * from './types';
export * from './http';

let currentClient: BackendClient = httpBackendClient;

/**
 * Returns the active BackendClient instance.
 * Defaults to httpBackendClient unless swapped (e.g. for testing or native desktop shell).
 */
export function getBackendClient(): BackendClient {
  return currentClient;
}

/**
 * Sets the active BackendClient instance.
 */
export function setBackendClient(client: BackendClient): void {
  currentClient = client;
}

/**
 * Resets the active BackendClient to the default HTTP client.
 */
export function resetBackendClient(): void {
  currentClient = httpBackendClient;
}
