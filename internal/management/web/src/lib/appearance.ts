import { ref } from 'vue'
export type ThemeMode = 'system' | 'light' | 'dark'
const storageKey = 'cfst-appearance-v1'
export const themeMode = ref<ThemeMode>('system')
export function normalizeTheme(value: unknown): ThemeMode {
  return value === 'light' || value === 'dark' ? value : 'system'
}
function applyTheme() {
  const dark = themeMode.value === 'dark' || themeMode.value === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
}
export function setTheme(value: unknown) {
  themeMode.value = normalizeTheme(value)
  applyTheme()
  try { localStorage.setItem(storageKey, themeMode.value) } catch {}
}
export function initializeAppearance() {
  try { themeMode.value = normalizeTheme(localStorage.getItem(storageKey)) } catch {}
  applyTheme()
  window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', applyTheme)
  window.addEventListener('storage', event => { if (event.key === storageKey) { themeMode.value = normalizeTheme(event.newValue); applyTheme() } })
}
