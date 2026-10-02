import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 D35 (T163, demo finding #5): the palette also finds profiles,
// clients and agent tokens. The three lists load once, on the first non-empty
// query after opening (never on open), and each group is needle-only.

const searchTools = vi.hoisted(() => vi.fn())
const getProfiles = vi.hoisted(() => vi.fn())
const getClients = vi.hoisted(() => vi.fn())
const listAgentTokens = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    searchTools,
    getProfiles,
    getClients,
    listAgentTokens,
  },
}))

import CommandPalette from '@/components/CommandPalette.vue'
import { useAuthStore } from '@/stores/auth'
import { useServersStore } from '@/stores/servers'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import api from '@/services/api'

const stub = { template: '<div />' }

async function tick(ms = 0) {
  await vi.advanceTimersByTimeAsync(ms)
}

async function mountPalette(opts: { profiles?: boolean; clients?: boolean } = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      { path: '/tools', component: stub },
      ...(opts.profiles ? [
        { path: '/profiles', component: stub },
        { path: '/profiles/:name', name: 'profile-editor', component: stub },
      ] : []),
      ...(opts.clients ? [{ path: '/clients', component: stub }] : []),
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
const input = () => dialog().querySelector('input') as HTMLInputElement
const rows = () => Array.from(dialog().querySelectorAll('[role="option"]')) as HTMLElement[]
const sectionIds = () =>
  Array.from(dialog().querySelectorAll('[data-test^="palette-section-"]')).map((el) => el.getAttribute('data-test')!.replace('palette-section-', ''))
const directoryIds = () => sectionIds().filter((id) => ['profiles', 'clients', 'tokens'].includes(id))

function key(init: KeyboardEventInit) {
  window.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init }))
}

async function type(text: string) {
  input().value = text
  input().dispatchEvent(new Event('input', { bubbles: true }))
  await tick()
}

const PROFILE = { name: 'work-readonly', title: 'Work Read-only', max_tier: 'read', servers: ['notes'], effective_servers: ['notes'] }
const CLIENTS = [
  { id: 'cursor', display_name: 'Cursor', kind: 'supported', state: 'connected_seen' },
  { id: 'claude-code', display_name: 'Claude Code', kind: 'supported', state: 'connected_seen' },
]
const TOKENS = [
  { name: 'qa-ro', token_prefix: 'mcp_agt_aa', kind: 'agent', revoked: false },
  { name: 'qa-old', token_prefix: 'mcp_agt_bb', kind: 'agent', revoked: true },
  { name: 'client-cursor', token_prefix: 'mcp_cli_cc', kind: 'client', client_id: 'cursor', revoked: false },
]

describe('CommandPalette profiles, clients and agent tokens (T163)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    document.body.innerHTML = ''
    searchTools.mockReset().mockResolvedValue({ success: true, data: { results: [] } })
    ;(api.hasAPIKey as any).mockReturnValue(true)
    getProfiles.mockReset().mockResolvedValue({ profiles: [PROFILE] })
    getClients.mockReset().mockResolvedValue({ success: true, data: { clients: CLIENTS } })
    listAgentTokens.mockReset().mockResolvedValue({ success: true, data: { tokens: TOKENS } })
    setAvailableFeatures(['scope_filters'])
    useServersStore().servers = [] as any
  })
  afterEach(() => {
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  const open = async (opts = { profiles: true, clients: true }) => {
    const mounted = await mountPalette(opts)
    key({ key: 'k', metaKey: true })
    await tick()
    return mounted
  }

  it('issues no /profiles, /clients or /tokens request on open, and exactly one each after the first input', async () => {
    const { wrapper } = await open()
    await tick(1000)
    expect(getProfiles).not.toHaveBeenCalled()
    expect(getClients).not.toHaveBeenCalled()
    expect(listAgentTokens).not.toHaveBeenCalled()

    await type('w')
    await tick(10)
    await type('wo')
    await tick(10)
    await type('wor')
    await tick(200)
    expect(getProfiles).toHaveBeenCalledTimes(1)
    expect(getClients).toHaveBeenCalledTimes(1)
    expect(listAgentTokens).toHaveBeenCalledTimes(1)
    // unscoped: the palette never reuses the page-scoped clients store
    expect(getClients).toHaveBeenCalledWith()
    wrapper.unmount()
  })

  it('"work" lists the profile by title with the slug hint and Enter opens its editor', async () => {
    const { wrapper, push } = await open()
    await type('work')
    await tick(200)
    expect(sectionIds()).toContain('profiles')
    const row = dialog().querySelector('[data-test="palette-row-profiles-0"]') as HTMLElement
    expect(row.textContent).toContain('Work Read-only')
    expect(row.textContent).toContain('work-readonly')
    row.click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ name: 'profile-editor', params: { name: 'work-readonly' }, query: {} })
    wrapper.unmount()
  })

  it('"cur" lists Cursor and routes to /clients?client=cursor', async () => {
    const { wrapper, push } = await open()
    await type('cur')
    await tick(200)
    const row = dialog().querySelector('[data-test="palette-row-clients-0"]') as HTMLElement
    expect(row.textContent).toContain('Cursor')
    row.click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/clients', query: { client: 'cursor' } })
    wrapper.unmount()
  })

  it('without scope filters a client row lands on the attention-fix focus instead', async () => {
    setAvailableFeatures([])
    const { wrapper, push } = await open()
    await type('cur')
    await tick(200)
    ;(dialog().querySelector('[data-test="palette-row-clients-0"]') as HTMLElement).click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/clients', query: { focus: 'cursor' } })
    wrapper.unmount()
  })

  it('a token row routes to /clients?tab=tokens&token=qa-ro; client credentials and revoked tokens are excluded', async () => {
    const { wrapper, push } = await open()
    await type('qa')
    await tick(200)
    const tokenRows = Array.from(dialog().querySelectorAll('[data-test^="palette-row-tokens-"]')) as HTMLElement[]
    expect(tokenRows).toHaveLength(1)
    expect(tokenRows[0].textContent).toContain('qa-ro')
    tokenRows[0].click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/clients', query: { tab: 'tokens', token: 'qa-ro' } })

    key({ key: 'k', metaKey: true })
    await tick()
    await type('client-cursor')
    await tick(200)
    expect(dialog().querySelector('[data-test^="palette-row-tokens-"]')).toBeNull()
    wrapper.unmount()
  })

  it('without scope filters a token row opens the tokens tab', async () => {
    setAvailableFeatures([])
    const { wrapper, push } = await open()
    await type('qa-ro')
    await tick(200)
    ;(dialog().querySelector('[data-test="palette-row-tokens-0"]') as HTMLElement).click()
    await tick()
    expect(push).toHaveBeenLastCalledWith({ path: '/clients', query: { tab: 'tokens' } })
    wrapper.unmount()
  })

  it('all three groups are needle-only: none shows on an empty input', async () => {
    const { wrapper } = await open()
    expect(directoryIds()).toEqual([])
    wrapper.unmount()
  })

  it('profile rows need the /profiles route; client and token rows need /clients', async () => {
    const { wrapper } = await open({ profiles: false, clients: false })
    await type('w')
    await tick(200)
    await type('cur')
    await tick(200)
    await type('qa')
    await tick(200)
    expect(directoryIds()).toEqual([])
    wrapper.unmount()
  })

  it('a failed /tokens request hides only the tokens group', async () => {
    listAgentTokens.mockReset().mockRejectedValue(new Error('boom'))
    const { wrapper } = await open()
    await type('c')
    await tick(200)
    const ids = sectionIds()
    expect(ids).toContain('clients')
    expect(ids).not.toContain('tokens')
    wrapper.unmount()
  })

  it('arrow keys and aria-activedescendant span the new sections', async () => {
    const { wrapper } = await open()
    await type('c')
    await tick(200)
    const all = rows()
    expect(sectionIds()).toContain('clients')
    const clientIdx = all.findIndex((r) => r.getAttribute('data-test')?.startsWith('palette-row-clients-'))
    expect(clientIdx).toBeGreaterThan(-1)
    for (let n = 0; n < clientIdx; n++) {
      input().dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }))
    }
    await tick()
    expect(rows()[clientIdx].getAttribute('aria-selected')).toBe('true')
    expect(input().getAttribute('aria-activedescendant')).toBe(rows()[clientIdx].id)
    wrapper.unmount()
  })

  it('a tenant and the server edition get none of the three groups and make no requests', async () => {
    const auth = useAuthStore()
    ;(api.hasAPIKey as any).mockReturnValue(false)
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
    const { wrapper } = await open()
    await type('cur')
    await tick(300)
    expect(getProfiles).not.toHaveBeenCalled()
    expect(getClients).not.toHaveBeenCalled()
    expect(listAgentTokens).not.toHaveBeenCalled()
    expect(directoryIds()).toEqual([])
    wrapper.unmount()

    document.body.innerHTML = ''
    auth.user = { id: 'a', email: 'a@x', display_name: 'A', role: 'admin', provider: 'oidc', created_at: '', last_login_at: '' } as any
    const admin = await open()
    await type('cur')
    await tick(300)
    expect(getClients).not.toHaveBeenCalled()
    expect(directoryIds()).toEqual([])
    admin.wrapper.unmount()
  })

  it('reloads the lists on the next open (cached for one open only)', async () => {
    const { wrapper } = await open()
    await type('cur')
    await tick(200)
    expect(getClients).toHaveBeenCalledTimes(1)
    key({ key: 'k', metaKey: true }) // close
    await tick()
    key({ key: 'k', metaKey: true }) // reopen
    await tick()
    await type('cur')
    await tick(200)
    expect(getClients).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
