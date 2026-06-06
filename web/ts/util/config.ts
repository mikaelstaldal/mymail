declare global {
  interface Window {
    __serverConfig?: { mycalUrl?: string };
  }
}

// Returns the MyCal base URL: explicit localStorage setting takes priority,
// then the server-injected default (derived from --public-url), then empty string.
export function getMycalUrl(): string {
  return localStorage.getItem('mycalUrl') || window.__serverConfig?.mycalUrl || '';
}
