import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ServerCard from '@/components/ServerCard.vue'
import ServerDetail from '@/views/ServerDetail.vue'
import { useServersStore } from '@/stores/servers'
import { canStartOAuthLogin } from '@/utils/oauthActions'
import type { Server } from '@/types'

let server: Server
vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return { default: {
    getServers: vi.fn(() => ok({ servers: [server] })),
    getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
    getToolDiff: vi.fn(() => ok({})),
    getServerTools: vi.fn(() => ok({ tools: [] })),
    getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, docker_available: true })),
    listScanners: vi.fn(() => ok([])),
    getScanReport: vi.fn(() => Promise.resolve({ success: false, error: 'no report' })),
    getScanStatus: vi.fn(() => ok({ status: 'completed' })),
    getServerLogs: vi.fn(() => ok({ logs: [] })),
  } }
})

beforeEach(() => {
  setActivePinia(createPinia())
  server = {
    name: 'notion', protocol: 'http', url: 'https://mcp.notion.com/mcp',
    enabled: true, connected: false, connecting: true, authenticated: true,
    tool_count: 0, oauth: { client_id: 'test' },
    last_error: 'OAuth authorization during MCP init failed: context deadline exceeded',
    health: { level: 'unhealthy', admin_state: 'enabled', summary: 'OAuth error', action: 'restart', actions: ['restart', 'view_logs'] },
  } as Server
})

describe('manual OAuth sign-in', () => {
  it('stays available while reconnecting and after a timeout or logout', () => {
    expect(canStartOAuthLogin(server)).toBe(true)
    expect(canStartOAuthLogin({ ...server, authenticated: false, user_logged_out: true })).toBe(true)
    expect(canStartOAuthLogin({ ...server, connected: true, connecting: false, health: undefined, last_error: undefined })).toBe(true)
  })
  it('does not offer OAuth on disabled, stdio, or unauthenticated non-OAuth HTTP servers', () => {
    expect(canStartOAuthLogin({ ...server, enabled: false })).toBe(false)
    expect(canStartOAuthLogin({ ...server, protocol: 'stdio' })).toBe(false)
    expect(canStartOAuthLogin({ ...server, oauth: undefined, authenticated: false, last_error: 'network timeout', health: undefined })).toBe(false)
  })
  it('recognizes auto-discovered OAuth without explicit config', () => {
    expect(canStartOAuthLogin({ ...server, oauth: undefined, authenticated: false })).toBe(true)
  })
  it('starts a fresh OAuth flow from the card even when reconnect is the primary action', async () => {
    const spy = vi.spyOn(useServersStore(), 'triggerOAuthLogin').mockResolvedValue(true)
    const card = mount(ServerCard, { props: { server }, global: { stubs: { RouterLink: true } } })
    const action = card.find('[data-test="server-card-menu-login"]')
    expect(action.text()).toBe('Sign in again')
    expect(action.attributes('disabled')).toBeUndefined()
    await action.trigger('click'); await flushPromises()
    expect(spy).toHaveBeenCalledWith('notion')
    await card.setProps({ server: { ...server, authenticated: false } })
    expect(card.find('[data-test="server-card-menu-login"]').text()).toBe('Sign in')
    card.unmount()
  })
  it('offers sign-in again from the detail Actions menu during an OAuth reconnect error', async () => {
    const router = createRouter({ history: createWebHistory(), routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }] })
    await router.push('/servers/notion'); await router.isReady()
    const pinia = createPinia(); setActivePinia(pinia)
    const spy = vi.spyOn(useServersStore(), 'triggerOAuthLogin').mockResolvedValue(true)
    const detail = mount(ServerDetail, { props: { serverName: 'notion' }, global: { plugins: [pinia, router] } })
    await flushPromises(); await flushPromises()
    const action = detail.find('[data-test="server-detail-menu-login"]')
    expect(action.text()).toBe('Sign in again')
    expect(action.attributes('disabled')).toBeUndefined()
    await action.trigger('click'); await flushPromises()
    expect(spy).toHaveBeenCalledWith('notion')
    detail.unmount()
  })
})
