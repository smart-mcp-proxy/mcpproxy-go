import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import type { Server } from '@/types'

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

// hasEnabledScanners() drives the Scan control; force it on so the disabled
// state is renderable.
vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ hasEnabledScanners: () => true }),
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

function makeServer(overrides: Partial<Server> = {}): Server {
  return {
    name: 'test-server',
    protocol: 'http',
    url: 'https://example.invalid/mcp',
    enabled: true,
    quarantined: false,
    connected: false,
    connecting: false,
    tool_count: 0,
    ...overrides,
  } as Server
}

function mountCard(server: Server) {
  return mount(ServerCard, {
    props: { server },
    global: {
      plugins: [createPinia()],
      stubs: { RouterLink: RouterLinkStub, 'router-link': RouterLinkStub },
    },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
})

// Audit F7: the Servers page states "Quarantined N — Need security review" on
// the stat tile above, then offered the card no way to review anything.
describe('ServerCard — quarantined card affords review (audit F7)', () => {
  it('offers Review, deep-linked to the server Security tab', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        enabled: false,
        health: { level: 'healthy', admin_state: 'disabled', summary: 'Disabled', action: 'enable' },
      } as Partial<Server>)
    )

    const review = wrapper.find('[data-test="server-card-quarantine-review"]')
    expect(review.exists()).toBe(true)
    expect(review.text()).toContain('Review')
    expect(review.attributes('href')).toContain('tab=security')
  })

  it('demotes Enable while the server is still held back', () => {
    const quarantined = mountCard(
      makeServer({
        quarantined: true,
        enabled: false,
        health: { level: 'healthy', admin_state: 'disabled', summary: 'Disabled', action: 'enable' },
      } as Partial<Server>)
    )
    expect(quarantined.find('[data-test="server-card-enable"]').classes()).toContain('btn-outline')

    const plainDisabled = mountCard(
      makeServer({
        enabled: false,
        health: { level: 'healthy', admin_state: 'disabled', summary: 'Disabled', action: 'enable' },
      } as Partial<Server>)
    )
    expect(plainDisabled.find('[data-test="server-card-enable"]').classes()).toContain('btn-primary')
  })

  it('explains why the Scan button is disabled', () => {
    const wrapper = mountCard(makeServer({ enabled: false }))
    const scan = wrapper.find('[data-test="server-card-scan-disabled"]')
    expect(scan.exists()).toBe(true)
    expect(scan.attributes('title')).toBeTruthy()
    expect(scan.attributes('title')).toContain('Enable the server')
  })
})

// Audit F12: the card rendered a good badge ("Host not found") AND a full-width
// red block containing the entire wrapped Go error chain.
describe('ServerCard — error dump is collapsed (audit F12)', () => {
  const wrappedError =
    'failed to connect: MCP initialize failed during no-auth strategy: transport error: ' +
    'failed to send request: Post "https://example.invalid/mcp": ' +
    'dial tcp: lookup example.invalid: no such host'

  it('shows the plain-language summary, with the raw chain behind a disclosure', () => {
    const wrapper = mountCard(
      makeServer({
        last_error: wrappedError,
        health: {
          level: 'unhealthy',
          admin_state: 'enabled',
          summary: 'Host not found',
          detail: wrappedError,
          action: 'edit_url',
        },
      } as Partial<Server>)
    )

    expect(wrapper.find('[data-test="server-card-error-summary"]').text()).toBe('Host not found')

    const details = wrapper.find('[data-test="server-card-error"] details')
    expect(details.exists()).toBe(true)
    // Collapsed by default — no `open` attribute.
    expect(details.attributes('open')).toBeUndefined()
    expect(wrapper.find('[data-test="server-card-error-detail"]').text()).toContain('dial tcp')
  })

  it('falls back to the root cause when no health summary is present', () => {
    const wrapper = mountCard(makeServer({ last_error: 'outer: inner: the real cause' }))
    expect(wrapper.find('[data-test="server-card-error-summary"]').text()).toBe('the real cause')
  })

  // For a quarantined or disabled server the health calculator short-circuits
  // and summary describes the ADMIN state, not the failure. Using it here would
  // print "Quarantined for review" in a red error alert sitting directly above
  // the quarantine banner that already says exactly that.
  it('does not restate the admin state as the error on a quarantined server', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        last_error: 'failed to connect: stdio transport: transport error: transport closed',
        health: {
          level: 'healthy',
          admin_state: 'quarantined',
          summary: 'Quarantined for review',
          action: 'approve',
        },
        diagnostic: {
          code: 'MCPX_STDIO_EXIT_BEFORE_INITIALIZE',
          severity: 'error',
          user_message: 'The stdio server process exited before completing the MCP initialize handshake.',
        },
      } as Partial<Server>)
    )

    const summary = wrapper.find('[data-test="server-card-error-summary"]').text()
    expect(summary).not.toContain('Quarantined')
    expect(summary).toContain('exited before completing')
  })

  it('falls back to the root cause on a quarantined server with no diagnostic', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        last_error: 'failed to connect: transport error: transport closed',
        health: {
          level: 'healthy',
          admin_state: 'quarantined',
          summary: 'Quarantined for review',
          action: 'approve',
        },
      } as Partial<Server>)
    )
    expect(wrapper.find('[data-test="server-card-error-summary"]').text()).toBe('transport closed')
  })
})

// Audit F11: a name that does not resolve is an address problem. Restart
// redials the same broken address forever.
describe('ServerCard — edit_url action (audit F11)', () => {
  it('offers Edit URL pointing at the config tab with the endpoint focused', () => {
    const wrapper = mountCard(
      makeServer({
        last_error: 'no such host',
        health: {
          level: 'unhealthy',
          admin_state: 'enabled',
          summary: 'Host not found',
          action: 'edit_url',
        },
      } as Partial<Server>)
    )

    const link = wrapper.find('[data-test="server-card-edit-url"]')
    expect(link.exists()).toBe(true)
    expect(link.text()).toContain('Edit URL')
    expect(link.attributes('href')).toContain('tab=config')
    expect(link.attributes('href')).toContain('focus=endpoint')
  })
})
