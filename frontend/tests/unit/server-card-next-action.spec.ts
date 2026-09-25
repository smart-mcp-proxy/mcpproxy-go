import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import type { Server, HealthStatus } from '@/types'

// Spec 109 (PR 109-e) T067, FR-013/FR-014: the server card shows exactly one
// primary button, driven by `health.actions[0]`, and folds every secondary
// action into a ⋯ menu — Delete is reachable ONLY from that menu, with a
// confirmation naming the server. Trust mode is a shield icon with a
// tooltip. The stats line links to Activity, scoped to this server and the
// last 24h (plus `status=error` when there were any).

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ hasEnabledScanners: () => true }),
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

function makeServer(overrides: Partial<Server> = {}): Server {
  return {
    name: 'srv',
    protocol: 'http',
    url: 'https://example.invalid/mcp',
    enabled: true,
    quarantined: false,
    connected: true,
    connecting: false,
    tool_count: 3,
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

// One fixture per internal/health.ActionLabels entry (mirrored by the Go
// T041 table this build's backend actually emits), plus the empty/"ready"
// case. `kind` records whether the primary button executes in place or
// navigates, so the href/click assertions below can branch per case.
const FIXTURES: Array<{
  name: string
  actions: string[]
  label: string | null
  kind: 'execute' | 'navigate' | 'none'
  hrefContains?: string
}> = [
  { name: 'ready (no action)', actions: [], label: null, kind: 'none' },
  { name: 'login', actions: ['login'], label: 'Sign in', kind: 'execute' },
  { name: 'restart', actions: ['restart'], label: 'Restart', kind: 'execute' },
  { name: 'enable', actions: ['enable'], label: 'Enable', kind: 'execute' },
  { name: 'approve', actions: ['approve'], label: 'Review', kind: 'navigate', hrefContains: '/review/' },
  { name: 'set_secret', actions: ['set_secret'], label: 'Add secret', kind: 'navigate', hrefContains: '/secrets' },
  { name: 'configure', actions: ['configure'], label: 'Fix config', kind: 'navigate', hrefContains: 'tab=config' },
  { name: 'edit_url', actions: ['edit_url'], label: 'Edit URL', kind: 'navigate', hrefContains: 'focus=endpoint' },
  { name: 'view_logs', actions: ['view_logs'], label: 'View logs', kind: 'navigate', hrefContains: 'tab=logs' },
]

describe('ServerCard — one primary action, from actions[0] (Spec 109 FR-013/FR-014)', () => {
  for (const fixture of FIXTURES) {
    it(`${fixture.name}: exactly ${fixture.kind === 'none' ? 'no' : 'one'} primary button`, () => {
      const wrapper = mountCard(
        makeServer({
          health: {
            level: 'healthy',
            admin_state: 'enabled',
            summary: '',
            status: 'ready',
            usable: fixture.actions.length === 0,
            actions: fixture.actions,
            action: fixture.actions[0] ?? '',
          } as unknown as HealthStatus,
        })
      )

      const buttons = wrapper.findAll('[data-test="server-card-primary-action"]')
      if (fixture.kind === 'none') {
        expect(buttons).toHaveLength(0)
        return
      }
      expect(buttons).toHaveLength(1)
      expect(buttons[0].text()).toBe(fixture.label)
      if (fixture.kind === 'navigate') {
        expect(buttons[0].element.tagName).toBe('A')
        if (fixture.hrefContains) {
          expect(buttons[0].attributes('href')).toContain(fixture.hrefContains)
        }
      } else {
        expect(buttons[0].element.tagName).toBe('BUTTON')
      }
    })
  }

  it('the token-expiring ready fixture shows Sign in, not "no button"', () => {
    // `actions` can carry a proactive nudge on an otherwise-ready, usable
    // server (a token expiring soon) — the primary button gates on
    // actions[0], never on `status`.
    const wrapper = mountCard(
      makeServer({
        health: {
          level: 'healthy',
          admin_state: 'enabled',
          summary: 'Connected',
          status: 'ready',
          usable: true,
          actions: ['login'],
          action: 'login',
        } as unknown as HealthStatus,
      })
    )
    const button = wrapper.find('[data-test="server-card-primary-action"]')
    expect(button.exists()).toBe(true)
    expect(button.text()).toBe('Sign in')
  })

  it('never executes approve directly — it always navigates to the review location', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        health: {
          level: 'healthy',
          admin_state: 'quarantined',
          summary: 'Quarantined for review',
          status: 'needs_review',
          usable: false,
          actions: ['approve'],
          action: 'approve',
        } as unknown as HealthStatus,
      })
    )
    const button = wrapper.find('[data-test="server-card-primary-action"]')
    expect(button.element.tagName).toBe('A')
    expect(button.attributes('href')).toBe('/review/srv')
  })
})

describe('ServerCard — ⋯ menu (Spec 109 FR-013)', () => {
  it('lists Enable/Disable, Scan, Restart, Logs, Edit, Trust mode and Delete', () => {
    const wrapper = mountCard(makeServer())
    const menu = wrapper.find('[data-test="server-card-menu"]')
    expect(menu.exists()).toBe(true)

    expect(wrapper.find('[data-test="server-card-menu-toggle"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-scan"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-restart"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-logs"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-edit"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-trust"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-delete"]').exists()).toBe(true)
  })

  it('Delete is reachable only from the ⋯ menu, and its confirmation names the server', async () => {
    const wrapper = mountCard(makeServer({ name: 'my-server' }))

    // No bare Delete control anywhere outside the menu.
    expect(wrapper.find('[data-test="server-detail-link"] + [data-test*="delete"]').exists()).toBe(false)
    expect(wrapper.findAll('button').some((b) => b.text().trim() === 'Delete Server')).toBe(false)

    await wrapper.find('[data-test="server-card-menu-delete"]').trigger('click')

    expect(wrapper.text()).toContain('Are you sure you want to delete the server')
    expect(wrapper.text()).toContain('my-server')
    expect(wrapper.find('[data-test="server-card-delete-confirm"]').exists()).toBe(true)
  })
})

describe('ServerCard — trust mode shield icon + tooltip (Spec 109 FR-013)', () => {
  it('renders an icon with a tooltip, not a text label', () => {
    const wrapper = mountCard(makeServer({ trust_mode: 'auto' }))
    const shield = wrapper.find('[data-test="server-trust-mode"]')
    expect(shield.exists()).toBe(true)
    expect(shield.find('svg').exists()).toBe(true)
    expect(shield.attributes('data-tip')).toContain('Auto')
  })
})

describe('ServerCard — stats line links to Activity (Spec 109 FR-013)', () => {
  it('links to /activity scoped to this server and the last 24h', () => {
    const wrapper = mountCard(makeServer({ name: 'alpha' }))
    const stats = wrapper.find('[data-test="server-card-stats-line"]')
    expect(stats.attributes('href')).toBe('/activity?server=alpha&from=-24h')
  })

  it('adds status=error when the server has errors in the period', () => {
    const wrapper = mountCard(makeServer({ name: 'beta' }), )
    wrapper.setProps({
      activityStats: { name: 'beta', calls: 5, errors: 2, last_call_at: new Date().toISOString() },
    })
    return wrapper.vm.$nextTick().then(() => {
      const stats = wrapper.find('[data-test="server-card-stats-line"]')
      expect(stats.attributes('href')).toBe('/activity?server=beta&from=-24h&status=error')
    })
  })
})
