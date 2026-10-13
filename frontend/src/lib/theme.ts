import { onBeforeUnmount, ref, watch } from 'vue'

export type Theme = 'auto' | 'light' | 'dark'

const storageKey = 'partfetch-theme'

export function readTheme(): Theme {
  try {
    const value = localStorage.getItem(storageKey)
    return value === 'light' || value === 'dark' ? value : 'auto'
  } catch {
    return 'auto'
  }
}

export function applyTheme(theme: Theme) {
  const root = document.documentElement
  if (theme === 'auto') {
    root.removeAttribute('data-theme')
    return
  }
  root.setAttribute('data-theme', theme)
}

export function saveTheme(theme: Theme) {
  try {
    localStorage.setItem(storageKey, theme)
  } catch {
    // The current theme still applies when browser storage is unavailable.
  }
}

export function useTheme() {
  const theme = ref(readTheme())
  watch(theme, value => {
    applyTheme(value)
    saveTheme(value)
  }, { immediate: true, flush: 'sync' })
  onBeforeUnmount(() => applyTheme('auto'))
  return { theme, setTheme: (value: Theme) => { theme.value = value } }
}

// A counter that changes whenever the colours on screen change: the user picked
// a theme, or the system theme flipped while "auto" is selected. Depend on it to
// recompute anything derived from the design tokens.
export function useThemeVersion() {
  const version = ref(0)
  const bump = () => { version.value++ }
  const observer = new MutationObserver(bump)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)')
  systemTheme.addEventListener('change', bump)
  onBeforeUnmount(() => {
    observer.disconnect()
    systemTheme.removeEventListener('change', bump)
  })
  return version
}
