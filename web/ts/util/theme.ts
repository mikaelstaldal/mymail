// Light/dark theme: the single source of truth for the app's runtime theme,
// mirroring MyNotes' web/ts/util/theme.ts. The chosen value is persisted in
// localStorage under `darkMode` — the key the Preferences tab has always used,
// so an existing preference carries over — and applied to the document root as
// `data-theme`, which app.css keys every colour off. Light is the default.

export type Theme = 'light' | 'dark';

const STORAGE_KEY = 'darkMode';

// Fired on `document` whenever the theme changes. Two controls expose the same
// preference — the sidebar's toggle button and the switch in Preferences — and
// either may be mounted while the other is used, so both listen rather than
// reading localStorage once and drifting out of step.
const THEME_EVENT = 'mymail-themechange';

export function getTheme(): Theme {
  return localStorage.getItem(STORAGE_KEY) === 'true' ? 'dark' : 'light';
}

/** Reflect a theme on the document root; app.css keys every colour off this. */
export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
}

/** Persist, apply, and notify subscribers. */
export function setTheme(theme: Theme): void {
  localStorage.setItem(STORAGE_KEY, String(theme === 'dark'));
  applyTheme(theme);
  document.dispatchEvent(new CustomEvent<Theme>(THEME_EVENT, { detail: theme }));
}

/** Flip between light and dark, returning the new theme. */
export function toggleTheme(): Theme {
  const next: Theme = getTheme() === 'dark' ? 'light' : 'dark';
  setTheme(next);
  return next;
}

/** Subscribe to theme changes; returns an unsubscribe function. */
export function onThemeChange(cb: (theme: Theme) => void): () => void {
  const handler = (e: Event) => cb((e as CustomEvent<Theme>).detail);
  document.addEventListener(THEME_EVENT, handler);
  return () => document.removeEventListener(THEME_EVENT, handler);
}
