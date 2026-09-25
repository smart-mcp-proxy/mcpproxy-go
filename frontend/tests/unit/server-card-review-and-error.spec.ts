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
//
// Spec 109 FR-013/FR-014 (109-e) folded the card's whole action row down to
// ONE primary button = `health.actions[0]`, so "Review" is now that primary
// button, deep-linked to `/review/<name>` (never the old Security-tab link,
// and never a direct approve — FR-005).
describe('ServerCard — quarantined card affords review (audit F7)', () => {
  it('offers Review as the one primary action, deep-linked to /review/<name>', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        enabled: false,
        health: {
          level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review',
          status: 'needs_review', usable: false, actions: ['approve'], action: 'approve',
        },
      } as unknown as Partial<Server>)
    )

    const review = wrapper.find('[data-test="server-card-primary-action"]')
    expect(review.exists()).toBe(true)
    expect(review.text()).toContain('Review')
    expect(review.attributes('href')).toBe(`/review/${wrapper.props('server').name}`)
  })

  it('shows exactly one primary button, whatever the server state', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        enabled: false,
        health: {
          level: 'healthy', admin_state: 'disabled', summary: 'Disabled',
          status: 'disabled', usable: false, actions: ['enable'], action: 'enable',
        },
      } as unknown as Partial<Server>)
    )
    const primaryButtons = wrapper.findAll('[data-test="server-card-primary-action"]')
    expect(primaryButtons).toHaveLength(1)
    expect(primaryButtons[0].text()).toContain('Enable')
  })

  it('shows no primary button for a healthy, actionless server', () => {
    const wrapper = mountCard(
      makeServer({
        health: {
          level: 'healthy', admin_state: 'enabled', summary: 'Connected',
          status: 'ready', usable: true, actions: [], action: '',
        },
      } as unknown as Partial<Server>)
    )
    expect(wrapper.find('[data-test="server-card-primary-action"]').exists()).toBe(false)
  })

  it('explains why the Scan menu item is disabled', () => {
    const wrapper = mountCard(makeServer({ enabled: false }))
    const scan = wrapper.find('[data-test="server-card-scan-disabled"]')
    expect(scan.exists()).toBe(true)
    expect(scan.attributes('title')).toBeTruthy()
    expect(scan.attributes('title')).toContain('Enable the server')
  })

  it('offers Delete only from the ⋯ menu, with a confirmation naming the server', async () => {
    const wrapper = mountCard(makeServer())

    // Never a bare Delete control on the card face.
    expect(wrapper.find('[data-test="server-card-delete-confirm"]').exists()).toBe(false)

    const menuDelete = wrapper.find('[data-test="server-card-menu-delete"]')
    expect(menuDelete.exists()).toBe(true)
    await menuDelete.trigger('click')

    expect(wrapper.text()).toContain('Are you sure you want to delete the server')
    expect(wrapper.text()).toContain('test-server')
    expect(wrapper.find('[data-test="server-card-delete-confirm"]').exists()).toBe(true)
  })
})

// Audit F12: the card rendered a good badge ("Host not found") AND a full-width
// red block containing the entire wrapped Go error chain.
//
// Spec 109 FR-013 folds that block into the ONE status line: the plain-language
// summary is the line's detail segment, and the raw chain moves to the
// tooltip — never printed on the card face.
describe('ServerCard — error dump is collapsed (audit F12)', () => {
  const wrappedError =
    'failed to connect: MCP initialize failed during no-auth strategy: transport error: ' +
    'failed to send request: Post "https://example.invalid/mcp": ' +
    'dial tcp: lookup example.invalid: no such host'

  it('shows the plain-language summary on the card, with the raw chain only in the tooltip', () => {
    const wrapper = mountCard(
      makeServer({
        last_error: wrappedError,
        health: {
          level: 'unhealthy', admin_state: 'enabled', summary: 'Host not found',
          detail: wrappedError, status: 'error', usable: false, actions: ['edit_url'], action: 'edit_url',
        },
      } as unknown as Partial<Server>)
    )

    const statusLine = wrapper.find('[data-test="server-card-status-line"]')
    expect(statusLine.text()).toContain('Host not found')
    // The raw wrapped chain never appears as visible text on the card.
    expect(statusLine.text()).not.toContain('dial tcp')
    expect(statusLine.attributes('data-tip')).toContain('dial tcp')
  })

  it('falls back to the root cause when no health summary is present', () => {
    const wrapper = mountCard(makeServer({ last_error: 'outer: inner: the real cause' }))
    expect(wrapper.find('[data-test="server-card-status-line"]').text()).toContain('the real cause')
  })

  // For a quarantined or disabled server the health calculator short-circuits
  // and summary describes the ADMIN state, not the failure. The status TEXT
  // segment already says that ("Needs review"), so the detail segment must
  // fall through to the structured diagnostic instead of restating it.
  it('does not restate the admin state as the error detail on a quarantined server', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        last_error: 'failed to connect: stdio transport: transport error: transport closed',
        health: {
          level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review',
          status: 'needs_review', usable: false, actions: ['approve'], action: 'approve',
        },
        diagnostic: {
          code: 'MCPX_STDIO_EXIT_BEFORE_INITIALIZE',
          severity: 'error',
          user_message: 'The stdio server process exited before completing the MCP initialize handshake.',
        },
      } as unknown as Partial<Server>)
    )

    const line = wrapper.find('[data-test="server-card-status-line"]').text()
    expect(line).not.toContain('Quarantined for review — Quarantined')
    expect(line).toContain('exited before completing')
  })

  it('falls back to the root cause on a quarantined server with no diagnostic', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        last_error: 'failed to connect: transport error: transport closed',
        health: {
          level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review',
          status: 'needs_review', usable: false, actions: ['approve'], action: 'approve',
        },
      } as unknown as Partial<Server>)
    )
    expect(wrapper.find('[data-test="server-card-status-line"]').text()).toContain('transport closed')
  })
})

// Audit F11: a name that does not resolve is not a restartable outage. Send
// the user to the field that is actually wrong — now via the ONE primary
// action (Spec 109 FR-013/FR-014), not a dedicated button.
describe('ServerCard — edit_url action (audit F11)', () => {
  it('offers Edit URL as the primary action, pointing at the config tab with the endpoint focused', () => {
    const wrapper = mountCard(
      makeServer({
        last_error: 'no such host',
        health: {
          level: 'unhealthy', admin_state: 'enabled', summary: 'Host not found',
          status: 'error', usable: false, actions: ['edit_url'], action: 'edit_url',
        },
      } as unknown as Partial<Server>)
    )

    const link = wrapper.find('[data-test="server-card-primary-action"]')
    expect(link.exists()).toBe(true)
    expect(link.text()).toContain('Edit URL')
    expect(link.attributes('href')).toContain('tab=config')
    expect(link.attributes('href')).toContain('focus=endpoint')
  })
})
