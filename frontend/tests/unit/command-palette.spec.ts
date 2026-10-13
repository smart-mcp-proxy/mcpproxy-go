import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109-i FR-054 / T136: the command palette. Opens on Cmd/Ctrl+K and "/",
// fires no /index/search request until there is text (debounced 150 ms,
// limit 8, stale responses dropped), and free text defaults to
// "Search tools for ..." -> /tools?q=.

const searchTools = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    searchTools,
  },
}))

import CommandPalette from '@/components/CommandPalette.vue'
import { useAuthStore } from '@/stores/auth'
import { useServersStore } from '@/stores/servers'
import api from '@/services/api'

const stub = { template: '<div />' }

async function tick(ms = 0) {
  await vi.advanceTimersByTimeAsync(ms)
}

async function mountPalette(opts: { profiles?: boolean } = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      { path: '/tools', component: stub },
      ...(opts.profiles ? [{ path: '/profiles', component: stub }] : []),
    ],
  })
  router.push('/')
  await router.isReady()
  const push = vi.spyOn(router, 'push')
  const wrapper = mount(CommandPalette, { attachTo: document.body, global: { plugins: [router] } })
  await tick()
  return { wrapper, router, push }
}

const dialog = () => document.querySelector('dialog[data-test="command-palette"]') as HTMLDialogElement
const isOpen = () => dialog().hasAttribute('open')
const input = () => dialog().querySelector('input') as HTMLInputElement
const rows = () => Array.from(dialog().querySelectorAll('[role="option"]')) as HTMLElement[]
const sectionIds = () =>
  Array.from(dialog().querySelectorAll('[data-test^="palette-section-"]')).map((el) => el.getAttribute('data-test')!.replace('palette-section-', ''))

function key(init: KeyboardEventInit) {
  window.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))
}

async function type(text: string) {
  input().value = text
  input().dispatchEvent(new Event('input', { bubbles: true }))
  await tick()
}

describe('CommandPalette (Spec 109-i FR-054)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    document.body.innerHTML = ''
    searchTools.mockReset().mockResolvedValue({
      success: true,
      data: { results: [{ tool: { name: 'search_notes', description: '', server_name: 'notes' }, score: 1, matches: 1 }] },
    })
    ;(api.hasAPIKey as any).mockReturnValue(true)
    useServersStore().servers = [
      { name: 'notes', enabled: true, quarantined: false, connected: true, tool_count: 1 },
      { name: 'filesystem', enabled: true, quarantined: false, connected: true, tool_count: 2 },
    ] as any
  })

  afterEach(() => {
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('opens on Cmd+K, toggles on Ctrl+K and opens on "/" from the body', async () => {
    const { wrapper } = await mountPalette()
    expect(isOpen()).toBe(false)
    key({ key: 'k', metaKey: true })
    await tick()
    expect(isOpen()).toBe(true)
    key({ key: 'k', ctrlKey: true })
    await tick()
    expect(isOpen()).toBe(false)
    key({ key: '/' })
    await tick()
    expect(isOpen()).toBe(true)
    wrapper.unmount()
  })

  it('does not steal "/" from an input, a modified "/" or an open dialog', async () => {
    const { wrapper } = await mountPalette()
    const field = document.createElement('input')
    document.body.appendChild(field)
    field.focus()
    field.dispatchEvent(new KeyboardEvent('keydown', { key: '/', bubbles: true, cancelable: true }))
    await tick()
    expect(isOpen()).toBe(false)
    field.blur()

    key({ key: '/', ctrlKey: true })
    await tick()
    expect(isOpen()).toBe(false)

    const other = document.createElement('dialog')
    other.setAttribute('open', '')
    document.body.appendChild(other)
    key({ key: '/' })
    await tick()
    expect(isOpen()).toBe(false)
    wrapper.unmount()
  })

  it('opens with empty input: Pages, Actions and Servers, and no /index/search request', async () => {
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick(1000)
    expect(searchTools).not.toHaveBeenCalled()
    expect(sectionIds()).toEqual(['pages', 'actions', 'servers'])
    expect(dialog().querySelector('[data-test="palette-row-servers-0"]')).not.toBeNull()
    wrapper.unmount()
  })

  it('debounces tool search 150 ms, limit 8, and only the latest text is sent', async () => {
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('n')
    await tick(149)
    expect(searchTools).not.toHaveBeenCalled()
    await tick(1)
    expect(searchTools).toHaveBeenCalledTimes(1)
    expect(searchTools).toHaveBeenLastCalledWith('n', 8)

    await type('no')
    await tick(50)
    await type('not')
    await tick(150)
    expect(searchTools).toHaveBeenCalledTimes(2)
    expect(searchTools).toHaveBeenLastCalledWith('not', 8)
    wrapper.unmount()
  })

  it('pins the request URL: q=<text>&limit=8', async () => {
    const actual = await vi.importActual<typeof import('@/services/api')>('@/services/api')
    const request = vi.spyOn(actual.default as any, 'request').mockResolvedValue({ success: true, data: { results: [] } })
    await actual.default.searchTools('n', 8)
    expect(request).toHaveBeenCalledWith('/api/v1/index/search?q=n&limit=8')
    request.mockRestore()
  })

  it('clearing the input drops tool rows and issues no further request', async () => {
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('notes')
    await tick(150)
    expect(dialog().querySelector('[data-test^="palette-row-tools-"]')).not.toBeNull()
    await type('   ')
    await tick(1000)
    expect(searchTools).toHaveBeenCalledTimes(1)
    expect(dialog().querySelector('[data-test^="palette-row-tools-"]')).toBeNull()
    wrapper.unmount()
  })

  it('shows server, tool and settings rows with their targets', async () => {
    const { wrapper, push } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('notes')
    await tick(150)
    expect(sectionIds()).toEqual(['search', 'servers', 'tools'])

    const server = dialog().querySelector('[data-test="palette-row-servers-0"]') as HTMLElement
    expect(server.textContent).toContain('notes')
    server.click()
    await tick()
    expect(push).toHaveBeenLastCalledWith('/servers/notes')

    key({ key: 'k', metaKey: true })
    await tick()
    await type('notes')
    await tick(150)
    ;(dialog().querySelector('[data-test="palette-row-tools-0"]') as HTMLElement).click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/tools', query: { server: 'notes', q: 'search_notes' } })

    key({ key: 'k', metaKey: true })
    await tick()
    await type('telemetry')
    await tick(150)
    const settings = dialog().querySelector('[data-test="palette-row-settings-0"]') as HTMLElement
    expect(settings.textContent?.toLowerCase()).toContain('telemetry')
    settings.click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/settings', query: { focus: 'telemetry.enabled' } })
    wrapper.unmount()
  })

  it('Enter on the default row searches tools for the typed text', async () => {
    const { wrapper, push } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('not')
    const first = rows()[0]
    expect(first.textContent).toContain('Search tools for')
    expect(first.getAttribute('aria-selected')).toBe('true')
    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await tick()
    expect(push).toHaveBeenCalledWith({ path: '/tools', query: { q: 'not' } })
    expect(isOpen()).toBe(false)
    wrapper.unmount()
  })

  it('arrow keys move the active row (wrapping), Enter activates it, Escape closes', async () => {
    const { wrapper, push } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    const el = input()
    expect(el.getAttribute('role')).toBe('combobox')
    const listbox = dialog().querySelector('[role="listbox"]')!
    expect(el.getAttribute('aria-controls')).toBe(listbox.id)

    const press = (k: string) => el.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true, cancelable: true }))
    press('ArrowDown')
    press('ArrowDown')
    await tick()
    const all = rows()
    expect(all[2].getAttribute('aria-selected')).toBe('true')
    expect(el.getAttribute('aria-activedescendant')).toBe(all[2].id)

    press('ArrowUp')
    press('ArrowUp')
    press('ArrowUp')
    await tick()
    expect(rows()[all.length - 1].getAttribute('aria-selected')).toBe('true')

    press('ArrowDown')
    await tick()
    expect(rows()[0].getAttribute('aria-selected')).toBe('true')
    press('ArrowDown')
    press('ArrowDown')
    await tick()
    press('Enter')
    await tick()
    expect(push).toHaveBeenCalledTimes(1)

    key({ key: 'k', metaKey: true })
    await tick()
    press('Escape')
    await tick()
    expect(isOpen()).toBe(false)
    wrapper.unmount()
  })

  it('drops a stale response that resolves after a newer request', async () => {
    let resolveFirst: (v: unknown) => void = () => {}
    searchTools
      .mockReset()
      .mockImplementationOnce(() => new Promise((res) => { resolveFirst = res }))
      .mockResolvedValueOnce({
        success: true,
        data: { results: [{ tool: { name: 'fresh_tool', description: '', server_name: 'notes' }, score: 1, matches: 1 }] },
      })
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('a')
    await tick(150)
    await type('ab')
    await tick(150)
    resolveFirst({
      success: true,
      data: { results: [{ tool: { name: 'stale_tool', description: '', server_name: 'notes' }, score: 1, matches: 1 }] },
    })
    await tick()
    const text = dialog().textContent ?? ''
    expect(text).toContain('fresh_tool')
    expect(text).not.toContain('stale_tool')
    wrapper.unmount()
  })

  it('offers Create profile only when /profiles exists', async () => {
    const without = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    expect(dialog().textContent).not.toContain('Create profile')
    without.wrapper.unmount()

    document.body.innerHTML = ''
    const withRoute = await mountPalette({ profiles: true })
    key({ key: 'k', metaKey: true })
    await tick()
    expect(dialog().textContent).toContain('Create profile')
    withRoute.wrapper.unmount()
  })

  it('a tenant gets tenant pages only and never calls the search API', async () => {
    const auth = useAuthStore()
    ;(api.hasAPIKey as any).mockReturnValue(false)
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any

    const { wrapper, push } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    expect(sectionIds()).toEqual(['pages'])
    expect(dialog().textContent).toContain('My Servers')

    await type('notes')
    await tick(1000)
    expect(searchTools).not.toHaveBeenCalled()
    expect(sectionIds()).toEqual(['search'])

    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await tick()
    expect(push).toHaveBeenCalledWith({ path: '/tools', query: { q: 'notes' } })
    wrapper.unmount()
  })
})
