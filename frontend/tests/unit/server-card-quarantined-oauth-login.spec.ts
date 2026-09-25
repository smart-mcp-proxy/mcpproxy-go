import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import { useServersStore } from '@/stores/servers'
import type { Server } from '@/types'

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
    getSecurityOverview: vi.fn().mockResolvedValue({ data: {} }),
  },
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

const LOGIN_ERROR =
  "OAuth authentication required for server 'github' - login available via Web UI or 'mcpproxy auth login --server=github'"

// The shape GET /api/v1/servers returns for a remote OAuth server imported with
// quarantine on (e.g. GitHub MCP at api.githubcopilot.com): health.action is
// 'approve' because the quarantine branch wins, yet the server cannot connect
// until the user signs in.
function quarantinedOAuthServer(overrides: Partial<Server> = {}): Server {
  return {
    name: 'github',
    protocol: 'http',
    url: 'https://api.githubcopilot.com/mcp/',
    enabled: true,
    quarantined: true,
    connected: false,
    connecting: false,
    tool_count: 0,
    last_error: LOGIN_ERROR,
    diagnostic: {
      code: 'MCPX_OAUTH_LOGIN_REQUIRED',
      severity: 'warn',
      user_message: 'This server needs you to sign in before it can connect.',
    },
    health: {
      level: 'degraded',
      admin_state: 'quarantined',
      summary: 'Quarantined — Sign-in required',
      detail: LOGIN_ERROR,
      action: 'approve',
    },
    ...overrides,
  } as Server
}

function mountCard(server: Server) {
  return mount(ServerCard, {
    props: { server },
    global: {
      stubs: { RouterLink: RouterLinkStub, 'router-link': RouterLinkStub },
    },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
})

describe('ServerCard — quarantined server that needs OAuth sign-in', () => {
  it('offers Login alongside Approve even though health.action is "approve"', () => {
    const card = mountCard(quarantinedOAuthServer())

    const login = card.find('[data-test="server-card-login"]')
    expect(login.exists()).toBe(true)
    expect(login.text()).toContain('Login')
    expect(card.find('[data-test="server-card-approve"]').exists()).toBe(true)
    expect(card.find('[data-test="server-status-chip"]').text()).toBe('Sign-in required')
  })

  it('Login triggers the OAuth flow for the server', async () => {
    const card = mountCard(quarantinedOAuthServer())
    const store = useServersStore()
    const spy = vi.spyOn(store, 'triggerOAuthLogin').mockResolvedValue(undefined as never)

    await card.find('[data-test="server-card-login"]').trigger('click')
    await flushPromises()

    expect(spy).toHaveBeenCalledWith('github')
  })

  it('drops the red error alert — the Login button already conveys the sign-in', () => {
    const card = mountCard(quarantinedOAuthServer())
    expect(card.find('[data-test="server-card-error"]').exists()).toBe(false)
  })

  it('offers no Login for a quarantined server that does not need sign-in', () => {
    const card = mountCard(
      quarantinedOAuthServer({
        last_error: undefined,
        diagnostic: undefined,
        health: { level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review', action: 'approve' },
      } as Partial<Server>)
    )
    expect(card.find('[data-test="server-card-login"]').exists()).toBe(false)
    expect(card.find('[data-test="server-card-approve"]').exists()).toBe(true)
  })

  it('offers Enable, not Login, for a disabled server with a stale sign-in diagnostic', () => {
    const card = mountCard(
      quarantinedOAuthServer({
        quarantined: false,
        enabled: false,
        health: { level: 'healthy', admin_state: 'disabled', summary: 'Disabled', action: 'enable' },
      } as Partial<Server>)
    )
    expect(card.find('[data-test="server-card-login"]').exists()).toBe(false)
    expect(card.find('[data-test="server-card-enable"]').exists()).toBe(true)
  })
})
