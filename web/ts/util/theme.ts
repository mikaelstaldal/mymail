// Implements ../mysuite/spec/sidebar-footer.md: preference and resolved theme are distinct.
export type Theme = 'light' | 'dark';
export type ThemePreference = 'auto' | Theme;

const THEME_EVENT = 'mymail-themechange';
const browserTheme = typeof window.matchMedia === 'function'
  ? window.matchMedia('(prefers-color-scheme: dark)') : null;

export function getThemePreference(): ThemePreference {
  const stored = localStorage.getItem('theme');
  if (stored === 'auto' || stored === 'light' || stored === 'dark') return stored;
  if (stored !== null) return 'auto';
  // Preserve explicit preferences from before the three-way selector.
  const legacy = localStorage.getItem('darkMode');
  return legacy === 'true' ? 'dark' : legacy === 'false' ? 'light' : 'auto';
}

export function getTheme(): Theme {
  const preference = getThemePreference();
  return preference === 'auto' ? (browserTheme?.matches ? 'dark' : 'light') : preference;
}

export function applyTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

function updateTheme(): void {
  const theme = getTheme();
  applyTheme(theme);
  document.dispatchEvent(new CustomEvent<Theme>(THEME_EVENT, { detail: theme }));
}

export function setTheme(preference: ThemePreference): void {
  localStorage.setItem('theme', preference);
  updateTheme();
}

browserTheme?.addEventListener('change', () => {
  if (getThemePreference() === 'auto') updateTheme();
});

export function onThemeChange(cb: (theme: Theme) => void): () => void {
  const handler = (e: Event) => cb((e as CustomEvent<Theme>).detail);
  document.addEventListener(THEME_EVENT, handler);
  return () => document.removeEventListener(THEME_EVENT, handler);
}
