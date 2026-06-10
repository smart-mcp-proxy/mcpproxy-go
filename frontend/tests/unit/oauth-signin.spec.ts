import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { Server } from '@/types'
import SignInPanel from '@/components/diagnostics/SignInPanel.vue'
import ServerCard from '@/components/ServerCard.vue'
import { oauthSignInState, isOAuthDiagnosticCode } from '@/utils/health'

// ServerCard pulls in stores + the security-scanner composable (which fetches
// the security overview). Stub the API so mounting stays offline.
vi.mock('@/services/api', () => ({
  default: {
    getSecurityOverview: vi.fn().mockResolvedValue({ data: {} }),
  },
}))

// Build a minimal Server object; only the fields the helper reads matter.
function makeServer(overrides: Partial<Server> = {}): Server {
  return {
    id: 's1',
    name: 'github',
    protocol: 'streamable-http',
    enabled: true,
    quarantined: false,
    connected: false,
    status: 'disconnected',
    reconnect_count: 0,
    tool_count: 0,
    created: '',
    updated: '',
    ...overrides,
  } as Server
}

describe('oauthSignInState helper (MCP-1821)', () => {
  it('returns "login" when health.action === "login"', () => {
    const s = makeServer({
      health: { level: 'unhealthy', admin_state: 'enabled', summary: 'login required', action: 'login' },
    })
    expect(oauthSignInState(s)).toBe('login')
  })

  it('returns "login" for an explicit MCPX_OAUTH_LOGIN_REQUIRED code even without action', () => {
    const s = makeServer({
      diagnostic: { code: 'MCPX_OAUTH_LOGIN_REQUIRED', severity: 'warn' },
    })
    expect(oauthSignInState(s)).toBe('login')
  })

  it('returns "reauth" for expired-session codes (error tone)', () => {
    for (const code of ['MCPX_OAUTH_REAUTH_REQUIRED', 'MCPX_OAUTH_REFRESH_EXPIRED', 'MCPX_OAUTH_REFRESH_403']) {
      const s = makeServer({
        diagnostic: { code, severity: 'error' },
        health: { level: 'unhealthy', admin_state: 'enabled', summary: 'token expired', action: 'login' },
      })
      expect(oauthSignInState(s)).toBe('reauth')
    }
  })

  it('returns null when no sign-in is required', () => {
    expect(oauthSignInState(makeServer())).toBeNull()
    const healthy = makeServer({
      connected: true,
      health: { level: 'healthy', admin_state: 'enabled', summary: 'connected', action: '' },
    })
    expect(oauthSignInState(healthy)).toBeNull()
  })

  it('isOAuthDiagnosticCode matches MCPX_OAUTH_* codes only', () => {
    expect(isOAuthDiagnosticCode('MCPX_OAUTH_LOGIN_REQUIRED')).toBe(true)
    expect(isOAuthDiagnosticCode('MCPX_OAUTH_REFRESH_403')).toBe(true)
    expect(isOAuthDiagnosticCode('MCPX_UNKNOWN_UNCLASSIFIED')).toBe(false)
    expect(isOAuthDiagnosticCode('MCPX_STDIO_SPAWN_ENOENT')).toBe(false)
    expect(isOAuthDiagnosticCode(undefined)).toBe(false)
  })
})

describe('SignInPanel component (MCP-1821)', () => {
  it('renders a calm amber Sign-in CTA for state=login and emits login on click', async () => {
    const wrapper = mount(SignInPanel, {
      props: { serverName: 'github', state: 'login' },
    })

    const panel = wrapper.find('[data-test="oauth-signin-panel"]')
    expect(panel.exists()).toBe(true)
    // Calm/amber tone — NOT the red error alert.
    expect(panel.classes()).toContain('alert-warning')
    expect(panel.classes()).not.toContain('alert-error')

    // Title names the server.
    expect(wrapper.find('[data-test="oauth-signin-title"]').text()).toContain('github')

    const btn = wrapper.find('[data-test="oauth-signin-login-btn"]')
    expect(btn.exists()).toBe(true)
    expect(btn.text()).toContain('Log in')
    expect(btn.text()).not.toContain('Re-login')

    await btn.trigger('click')
    expect(wrapper.emitted('login')).toBeTruthy()
    expect(wrapper.emitted('login')).toHaveLength(1)
  })

  it('uses error tone and a Re-login label for state=reauth', () => {
    const wrapper = mount(SignInPanel, {
      props: { serverName: 'github', state: 'reauth' },
    })
    const panel = wrapper.find('[data-test="oauth-signin-panel"]')
    expect(panel.classes()).toContain('alert-error')
    expect(panel.classes()).not.toContain('alert-warning')
    expect(wrapper.find('[data-test="oauth-signin-login-btn"]').text()).toContain('Re-login')
  })

  it('never renders a file-a-bug / issues/new link', () => {
    const wrapper = mount(SignInPanel, {
      props: { serverName: 'github', state: 'login', docsUrl: 'https://docs.mcpproxy.app/oauth' },
    })
    expect(wrapper.html()).not.toContain('issues/new')
    // A docs link IS allowed.
    expect(wrapper.find('[data-test="oauth-signin-docs-link"]').exists()).toBe(true)
  })

  it('clarifies the quarantine gate when the server is also quarantined', () => {
    const wrapper = mount(SignInPanel, {
      props: { serverName: 'github', state: 'login', quarantined: true },
    })
    const note = wrapper.find('[data-test="oauth-signin-quarantine-note"]')
    expect(note.exists()).toBe(true)
    expect(note.text().toLowerCase()).toContain('approve')
  })
})

describe('ServerCard status chip — OAuth sign-in (MCP-1857)', () => {
  function mountCard(server: Server) {
    setActivePinia(createPinia())
    return mount(ServerCard, {
      props: { server },
      global: {
        plugins: [createPinia()],
        stubs: { 'router-link': { template: '<a><slot /></a>' } },
      },
    })
  }

  it('reads calm amber "Sign-in required" for a no-health diagnostic-only login code', () => {
    // The exact shape the bug regressed on: no health object, only a
    // diagnostic-code OAuth login state. Pre-fix this fell through to the
    // legacy fallback and rendered a red "Disconnected" chip.
    const card = mountCard(makeServer({
      diagnostic: { code: 'MCPX_OAUTH_LOGIN_REQUIRED', severity: 'warn' },
    }))
    const chip = card.find('[data-test="server-status-chip"]')
    expect(chip.exists()).toBe(true)
    expect(chip.text()).toBe('Sign-in required')
    expect(chip.classes()).toContain('badge-warning')
    expect(chip.classes()).not.toContain('badge-error')
  })

  it('still reads amber "Sign-in required" when health.action === "login"', () => {
    const card = mountCard(makeServer({
      health: { level: 'unhealthy', admin_state: 'enabled', summary: 'login required', action: 'login' },
    }))
    const chip = card.find('[data-test="server-status-chip"]')
    expect(chip.text()).toBe('Sign-in required')
    expect(chip.classes()).toContain('badge-warning')
  })

  it('reads red "Disconnected" for a genuinely disconnected server (no sign-in)', () => {
    const card = mountCard(makeServer({ connected: false }))
    const chip = card.find('[data-test="server-status-chip"]')
    expect(chip.text()).toBe('Disconnected')
    expect(chip.classes()).toContain('badge-error')
  })
})
