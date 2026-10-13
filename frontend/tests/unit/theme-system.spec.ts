/**
 * "Match system" theme (UX audit F29): a user on a dark OS must not get a light
 * UI on first run, an explicit choice must still win, and the choice persists.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import {
  SYSTEM_DARK_THEME,
  SYSTEM_LIGHT_THEME,
  SYSTEM_THEME,
  THEME_STORAGE_KEY,
  resolveThemeName,
  useSystemStore,
} from '@/stores/system'

type Listener = () => void

let prefersDark = false
let listeners: Listener[] = []

function installMatchMedia() {
  listeners = []
  vi.stubGlobal(
    'matchMedia',
    vi.fn((query: string) => ({
      media: query,
      matches: query.includes('dark') ? prefersDark : false,
      addEventListener: (_: string, cb: Listener) => listeners.push(cb),
      removeEventListener: (_: string, cb: Listener) => {
        listeners = listeners.filter((l) => l !== cb)
      },
      addListener: (cb: Listener) => listeners.push(cb),
      removeListener: (cb: Listener) => {
        listeners = listeners.filter((l) => l !== cb)
      },
      dispatchEvent: () => false,
      onchange: null,
    })),
  )
}

const appliedTheme = () => document.documentElement.getAttribute('data-theme')

describe('theme selection', () => {
  beforeEach(() => {
    prefersDark = false
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
    installMatchMedia()
    setActivePinia(createPinia())
  })

  it('offers System as the first option', () => {
    const store = useSystemStore()
    expect(store.themes[0].name).toBe(SYSTEM_THEME)
    expect(store.themes.map((t) => t.name)).toContain('dark')
    expect(store.themes.map((t) => t.name)).toContain('corporate')
  })

  it('defaults to system and follows a light OS', () => {
    const store = useSystemStore()
    expect(store.currentTheme).toBe(SYSTEM_THEME)
    expect(store.resolvedTheme).toBe(SYSTEM_LIGHT_THEME)
    expect(appliedTheme()).toBe(SYSTEM_LIGHT_THEME)
  })

  it('defaults to dark on a dark OS', () => {
    prefersDark = true
    installMatchMedia()
    const store = useSystemStore()
    expect(store.currentTheme).toBe(SYSTEM_THEME)
    expect(store.resolvedTheme).toBe(SYSTEM_DARK_THEME)
    expect(appliedTheme()).toBe(SYSTEM_DARK_THEME)
  })

  it('re-resolves when the OS preference flips while on system', () => {
    const store = useSystemStore()
    expect(appliedTheme()).toBe(SYSTEM_LIGHT_THEME)

    prefersDark = true
    listeners.forEach((cb) => cb())
    expect(store.resolvedTheme).toBe(SYSTEM_DARK_THEME)
    expect(appliedTheme()).toBe(SYSTEM_DARK_THEME)
  })

  it('keeps an explicit choice pinned when the OS flips', () => {
    const store = useSystemStore()
    store.setTheme('dracula')
    expect(appliedTheme()).toBe('dracula')

    prefersDark = true
    listeners.forEach((cb) => cb())
    expect(store.currentTheme).toBe('dracula')
    expect(appliedTheme()).toBe('dracula')
  })

  it('persists the selection, including system itself', () => {
    const store = useSystemStore()
    store.setTheme('corporate')
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe('corporate')
    store.setTheme(SYSTEM_THEME)
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe(SYSTEM_THEME)
  })

  it('restores a stored explicit choice on load', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'forest')
    const store = useSystemStore()
    expect(store.currentTheme).toBe('forest')
    expect(appliedTheme()).toBe('forest')
  })

  it('falls back to system when the stored theme no longer exists', () => {
    localStorage.setItem(THEME_STORAGE_KEY, 'theme-that-was-removed')
    const store = useSystemStore()
    expect(store.currentTheme).toBe(SYSTEM_THEME)
    expect(appliedTheme()).toBe(SYSTEM_LIGHT_THEME)
  })

  it('ignores an unknown theme passed to setTheme', () => {
    const store = useSystemStore()
    store.setTheme('nope')
    expect(store.currentTheme).toBe(SYSTEM_THEME)
  })

  it('removes its media listener when the store is disposed', () => {
    const store = useSystemStore()
    expect(listeners.length, 'no listener registered').toBe(1)
    store.$dispose()
    expect(listeners.length, 'listener outlived the store').toBe(0)
  })

  it('registers exactly one listener even if loadTheme runs again', () => {
    const store = useSystemStore()
    store.loadTheme()
    store.loadTheme()
    expect(listeners.length).toBe(1)
  })

  it('resolveThemeName maps only the system pseudo-theme', () => {
    prefersDark = true
    installMatchMedia()
    expect(resolveThemeName(SYSTEM_THEME)).toBe(SYSTEM_DARK_THEME)
    expect(resolveThemeName('lofi')).toBe('lofi')
  })
})
