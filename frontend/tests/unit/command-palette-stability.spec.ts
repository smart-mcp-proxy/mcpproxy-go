import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Issues #1462 (item 1) / #1451 (item 11) / #1433 (item 2): the highlighted
// row is tracked by key so late tool results cannot shift it; tool rows never
// outlive the query that produced them; Escape then an immediate reopen is not
// undone by the Escape's queued native `cancel`.

const searchTools = vi.hoisted(() => vi.fn())
const getProfiles = vi.hoisted(() => vi.fn())
const getClients = vi.hoisted(() => vi.fn())
const listAgentTokens = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: { hasAPIKey: vi.fn(() => true), searchTools, getProfiles, getClients, listAgentTokens },
}))

import CommandPalette from '@/components/CommandPalette.vue'
import { useServersStore } from '@/stores/servers'
import { setAvailableFeatures } from '@/composables/useScopeQuery'

const stub = { template: '<div />' }
const tick = (ms = 0) => vi.advanceTimersByTimeAsync(ms)

async function mountPalette() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      { path: '/tools', component: stub },
      { path: '/profiles', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
    ],
  })
  router.push('/')
  await router.isReady()
  const push = vi.spyOn(router, 'push')
  const wrapper = mount(CommandPalette, { attachTo: document.body, global: { plugins: [router] } })
  await tick()
  return { wrapper, push }
}

const dialog = () => document.querySelector('dialog[data-test="command-palette"]') as HTMLDialogElement
const input = () => dialog().querySelector('input') as HTMLInputElement
const toolRowCount = () => dialog().querySelectorAll('[data-test^="palette-row-tools-"]').length
function key(init: KeyboardEventInit) {
  window.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))
}
async function type(text: string) {
  input().value = text
  input().dispatchEvent(new Event('input', { bubbles: true }))
  await tick()
}

describe('CommandPalette stability (#1462, #1451, #1433)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    document.body.innerHTML = ''
    searchTools.mockReset().mockResolvedValue({
      success: true,
      data: { results: [{ tool: { name: 'work_tool', description: '', server_name: 'notes' }, score: 1, matches: 1 }] },
    })
    getProfiles.mockReset().mockResolvedValue({
      profiles: [{ name: 'work-readonly', title: 'Work Read-only', max_tier: 'read', servers: [], effective_servers: [] }],
    })
    getClients.mockReset().mockResolvedValue({ success: true, data: { clients: [] } })
    listAgentTokens.mockReset().mockResolvedValue({ success: true, data: { tokens: [] } })
    setAvailableFeatures(['scope_filters'])
    useServersStore().servers = [] as any
  })
  afterEach(() => {
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('keeps the highlighted Profiles row when tool rows land before it (#1462)', async () => {
    const { wrapper, push } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('work')
    const profileRow = dialog().querySelector('[data-test="palette-row-profiles-0"]') as HTMLElement
    expect(profileRow).not.toBeNull()
    profileRow.dispatchEvent(new MouseEvent('mousemove', { bubbles: true }))
    await tick()
    expect(profileRow.getAttribute('aria-selected')).toBe('true')

    await tick(200) // debounce fires, tool rows are inserted before Profiles
    expect(toolRowCount()).toBe(1)
    const moved = dialog().querySelector('[data-test="palette-row-profiles-0"]') as HTMLElement
    expect(moved.getAttribute('aria-selected')).toBe('true')

    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }))
    await tick()
    expect(push).toHaveBeenLastCalledWith({ name: 'profile-editor', params: { name: 'work-readonly' }, query: {} })
    wrapper.unmount()
  })

  it('drops the previous query tool rows at once on a new query (#1451)', async () => {
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    await type('a')
    await tick(200)
    expect(toolRowCount()).toBe(1)
    await type('b')
    expect(toolRowCount()).toBe(0) // still inside the debounce window
    wrapper.unmount()
  })

  it('stays open when Escape is followed at once by Ctrl+K and the stale cancel arrives (#1433)', async () => {
    const { wrapper } = await mountPalette()
    key({ key: 'k', metaKey: true })
    await tick()
    expect(dialog().hasAttribute('open')).toBe(true)

    input().dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    key({ key: 'k', ctrlKey: true })
    dialog().dispatchEvent(new Event('cancel', { cancelable: true }))
    await tick()
    expect(dialog().hasAttribute('open')).toBe(true)

    // A genuine later Escape-cancel still closes it.
    await tick(500)
    dialog().dispatchEvent(new Event('cancel', { cancelable: true }))
    await tick()
    expect(dialog().hasAttribute('open')).toBe(false)
    wrapper.unmount()
  })
})
