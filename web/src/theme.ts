// Theme switcher state. Persisted in localStorage (try/catch: private mode, blocked storage) and applied via data-theme;
// "system" removes the attribute so prefers-color-scheme (light / dark) decides. Themes are compiled in style.css.
export const THEMES = ['light', 'dark', 'corporate', 'business', 'nord'] as const
export type Theme = (typeof THEMES)[number] | 'system'
const KEY = 'c8:theme'

export function readTheme(): Theme {
  try { const t = localStorage.getItem(KEY); return (THEMES as readonly string[]).includes(t ?? '') ? (t as Theme) : 'system' } catch { return 'system' }
}
export function applyTheme(t: Theme, persist = false) {
  if (t === 'system') delete document.documentElement.dataset.theme; else document.documentElement.dataset.theme = t
  if (!persist) return
  try { if (t === 'system') localStorage.removeItem(KEY); else localStorage.setItem(KEY, t) } catch { /* theme still applies for this page view */ }
}
export const isDark = (t: Theme) => t === 'dark' || t === 'business' || (t === 'system' && !!globalThis.matchMedia?.('(prefers-color-scheme: dark)').matches)
