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

// The shape GET /api/v1/servers returns for a remote OAuth server imported
// with quarantine on (e.g. GitHub MCP at api.githubcopilot.com): quarantined
// AND needing OAuth sign-in. internal/health.quarantinedOAuthLoginState
// (calculator.go, PR #1366 + this branch's own round of fixes) resolves this
// to actions=[login, approve] — login is the ONE primary action
// (Spec 109 FR-013/FR-014, actions[0]), with Review still reachable
// independently from the ⋯ menu (gated on `server.quarantined`, mirroring
// macOS ServersView.swift's contextMenuActions) since the primary button
// alone can't surface both.
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
      status: 'sign_in_required',
      detail: LOGIN_ERROR,
      action: 'login',
      actions: ['login', 'approve'],
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
  it('shows Sign in as the ONE primary action, with Review still reachable from the ⋯ menu', () => {
    const card = mountCard(quarantinedOAuthServer())

    const primary = card.find('[data-test="server-card-primary-action"]')
    expect(primary.exists()).toBe(true)
    expect(primary.text()).toBe('Sign in')
    // actions=[login, approve]: login wins the ONE primary slot (FR-013/
    // FR-014), but quarantined still needs a path to Review — gated
    // independently on `server.quarantined` in the ⋯ menu, not on whichever
    // action is primary (109-e round 1 high finding).
    expect(card.find('[data-test="server-card-menu-review"]').exists()).toBe(true)
    expect(card.find('[data-test="server-status-chip"]').text()).toBe('Sign-in required')
  })

  it('the primary action triggers the OAuth flow for the server', async () => {
    const card = mountCard(quarantinedOAuthServer())
    const store = useServersStore()
    const spy = vi.spyOn(store, 'triggerOAuthLogin').mockResolvedValue(undefined as never)

    await card.find('[data-test="server-card-primary-action"]').trigger('click')
    await flushPromises()

    expect(spy).toHaveBeenCalledWith('github')
  })

  it('offers Review, not Sign in, for a quarantined server that does not need sign-in', () => {
    const card = mountCard(
      quarantinedOAuthServer({
        last_error: undefined,
        diagnostic: undefined,
        health: {
          level: 'healthy',
          admin_state: 'quarantined',
          summary: 'Quarantined for review',
          status: 'needs_review',
          action: 'approve',
          actions: ['approve'],
        },
      } as Partial<Server>)
    )
    const primary = card.find('[data-test="server-card-primary-action"]')
    expect(primary.text()).toBe('Review')
    expect(primary.text()).not.toBe('Sign in')
    expect(card.find('[data-test="server-card-menu-review"]').exists()).toBe(true)
  })

  it('offers Enable, not Sign in, for a disabled server with a stale sign-in diagnostic', () => {
    const card = mountCard(
      quarantinedOAuthServer({
        quarantined: false,
        enabled: false,
        health: {
          level: 'healthy',
          admin_state: 'disabled',
          summary: 'Disabled',
          status: 'disabled',
          action: 'enable',
          actions: ['enable'],
        },
      } as Partial<Server>)
    )
    const primary = card.find('[data-test="server-card-primary-action"]')
    expect(primary.text()).toBe('Enable')
  })

  it('shows Enable, not Sign in, for a just-disabled server whose stale health.action is still "login" (disableServer optimistic-update race)', () => {
    // disableServer() (stores/servers.ts) optimistically flips top-level
    // `enabled` to false immediately, but the `health` object (admin_state
    // still 'enabled', action still 'login') is only replaced once the
    // SSE-triggered refresh lands. During that window the primary action
    // must not render the stale Sign-in CTA for a server the user just
    // disabled — `primaryAction`'s own race guard forces 'enable' whenever
    // `enabled` (which updates immediately) says false but the stale action
    // still says 'login'.
    const card = mountCard(
      quarantinedOAuthServer({
        quarantined: false,
        enabled: false,
        health: {
          level: 'degraded',
          admin_state: 'enabled',
          summary: 'Sign-in required',
          status: 'sign_in_required',
          detail: LOGIN_ERROR,
          action: 'login',
          actions: ['login'],
        },
      } as Partial<Server>)
    )
    const primary = card.find('[data-test="server-card-primary-action"]')
    expect(primary.text()).toBe('Enable')
  })
})
