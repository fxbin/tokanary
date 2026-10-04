import { computed, ref } from 'vue'

const THEME_KEY = 'tt-theme'

export function useTheme() {
  const theme = ref<string>('auto')
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark') theme.value = saved
  } catch { /* ignore */ }

  function effectiveTheme(): string {
    if (theme.value === 'light' || theme.value === 'dark') return theme.value
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
  }
  function applyTheme() {
    document.documentElement.setAttribute('data-theme', effectiveTheme())
  }
  function toggleTheme() {
    theme.value = effectiveTheme() === 'dark' ? 'light' : 'dark'
    try { localStorage.setItem(THEME_KEY, theme.value) } catch { /* ignore */ }
    applyTheme()
  }
  function effectiveThemeReactive(): string {
    void theme.value
    return effectiveTheme()
  }
  const themeIcon = computed(() => (effectiveThemeReactive() === 'dark'
    ? '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>'
    : '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>'))
  const themeTitle = computed(() => '当前' + (theme.value === 'auto' ? '跟随系统' : theme.value) + '，点击切换明/暗')

  return { theme, applyTheme, toggleTheme, themeIcon, themeTitle }
}
