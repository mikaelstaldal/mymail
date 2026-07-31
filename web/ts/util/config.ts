import { normalizeWrapColumn } from './wrap.js';

declare global {
  interface Window {
    __serverConfig?: { mycalUrl?: string };
  }
}

/** Key under which the compose wrap column is stored. */
export const WRAP_COLUMN_KEY = 'wrapColumn';

// Read at each wrap rather than cached, so a change in Preferences takes effect
// on the next edit without the compose form having to hear about it.
export function getWrapColumn(): number {
  return normalizeWrapColumn(localStorage.getItem(WRAP_COLUMN_KEY));
}

// Returns the MyCal base URL: explicit localStorage setting takes priority,
// then the server-injected default (derived from --public-url), then empty string.
export function getMycalUrl(): string {
  return localStorage.getItem('mycalUrl') || window.__serverConfig?.mycalUrl || '';
}
