import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import { canStartOAuthLogin, isOAuthTokenUndecryptable } from '@/utils/oauthActions'
import { oauthSignInState } from '@/utils/health'
import type { Server } from '@/types'

// Spec 123 PR-3 (FR-031): a server whose stored OAuth token cannot be
// decrypted gets the configure guidance, never a sign-in action: a new
// sign-in could not overwrite the row (the backend refuses it), so offering
// one would only produce an error.

const UNDECRYPTABLE = 'MCPX_OAUTH_TOKEN_UNDECRYPTABLE'

let server: Server

beforeEach(() => {
  setActivePinia(createPinia())
  server = {
    name: 'notion', protocol: 'http', url: 'https://mcp.notion.com/mcp',
    enabled: true, connected: false, connecting: false, authenticated: true,
    tool_count: 0, oauth: { client_id: 'test' },
    last_error: 'stored OAuth token cannot be decrypted: sealed with a different key',
    diagnostic: { code: UNDECRYPTABLE, severity: 'error', user_message: 'cannot be decrypted', fix_steps: [] },
    health: {
      level: 'unhealthy', admin_state: 'enabled', summary: 'Stored OAuth token cannot be decrypted',
      detail: 'stored OAuth token cannot be decrypted: sealed with a different key',
      action: 'configure', actions: ['configure'],
    },
  } as unknown as Server
})

describe('OAuth sign-in for an undecryptable stored token', () => {
  it('is not offered, however the server otherwise looks signed in', () => {
    expect(isOAuthTokenUndecryptable(server)).toBe(true)
    expect(canStartOAuthLogin(server)).toBe(false)
    expect(canStartOAuthLogin({ ...server, authenticated: true, connected: true } as Server)).toBe(false)
    expect(canStartOAuthLogin({ ...server, health: undefined } as Server)).toBe(false)
  })

  it('is recognised from the flat error_code as well', () => {
    const flat = { ...server, diagnostic: undefined, error_code: UNDECRYPTABLE } as unknown as Server
    expect(isOAuthTokenUndecryptable(flat)).toBe(true)
    expect(canStartOAuthLogin(flat)).toBe(false)
  })

  it('never reads as a login-required state', () => {
    expect(oauthSignInState(server)).toBeNull()
  })

  it('leaves every other OAuth diagnostic code signing in as before', () => {
    for (const code of ['MCPX_OAUTH_LOGIN_REQUIRED', 'MCPX_OAUTH_REAUTH_REQUIRED', 'MCPX_OAUTH_REFRESH_EXPIRED', 'MCPX_OAUTH_DISCOVERY_FAILED']) {
      const other = { ...server, diagnostic: { ...server.diagnostic, code }, last_error: undefined } as unknown as Server
      expect(isOAuthTokenUndecryptable(other)).toBe(false)
      expect(canStartOAuthLogin(other)).toBe(true)
    }
  })

  it('shows the configure action and no sign-in menu entry on the server card', () => {
    const card = mount(ServerCard, { props: { server }, global: { stubs: { RouterLink: true } } })
    expect(card.find('[data-test="server-card-menu-login"]').exists()).toBe(false)
    expect(card.text()).toContain('cannot be decrypted')
    card.unmount()
  })
})
