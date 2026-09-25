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
  // Review round 1 (109-e high finding): `/review/<name>` is not a
  // registered route (109-a's interim `/review` → `?tab=tools` redirect it
  // depends on has not landed on this branch), so it 404s today. Points
  // straight at the Tools tab it was meant to redirect to.
  { name: 'approve', actions: ['approve'], label: 'Review', kind: 'navigate', hrefContains: 'tab=tools' },
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
    // Not `/review/srv` — that route does not exist (see the FIXTURES
    // comment above); it goes straight to the Tools tab.
    expect(button.attributes('href')).toBe('/servers/srv?tab=tools')
  })

  // Review round 1 (109-e medium finding, coverage gap b): only the empty
  // `action` + empty `actions` combination was ever tested for "no button".
  // The real old-core fallback branch in `primaryAction` — `actions` present
  // but empty, `action` truthy — was never exercised.
  it('falls back to the legacy scalar `action` when `actions` is empty', () => {
    const wrapper = mountCard(
      makeServer({
        health: {
          level: 'healthy',
          admin_state: 'enabled',
          summary: '',
          status: 'ready',
          usable: false,
          actions: [],
          action: 'restart',
        } as unknown as HealthStatus,
      })
    )
    const button = wrapper.find('[data-test="server-card-primary-action"]')
    expect(button.exists()).toBe(true)
    expect(button.text()).toBe('Restart')
    expect(button.element.tagName).toBe('BUTTON')
  })

  // Review round 1 (109-e medium finding, coverage gap a): no fixture ever
  // had more than one action, so a regression that re-rendered a second
  // button from `actions[1]` — re-growing the multi-button row FR-013
  // removed — would pass every FIXTURES case above unnoticed.
  it('renders only actions[0] as the primary button when actions has more than one entry', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        health: {
          level: 'degraded',
          admin_state: 'quarantined',
          summary: 'Sign-in required',
          status: 'signin_required',
          usable: false,
          actions: ['login', 'approve'],
          action: 'login',
        } as unknown as HealthStatus,
      })
    )
    const buttons = wrapper.findAll('[data-test="server-card-primary-action"]')
    expect(buttons).toHaveLength(1)
    expect(buttons[0].text()).toBe('Sign in')
  })
})

describe('ServerCard — ⋯ menu (Spec 109 FR-013)', () => {
  it('lists Enable/Disable, Scan, Restart, Logs, Edit, Trust mode and Delete, correctly labelled', () => {
    const wrapper = mountCard(makeServer({ enabled: true, trust_mode: 'auto' }))
    const menu = wrapper.find('[data-test="server-card-menu"]')
    expect(menu.exists()).toBe(true)

    // Review round 1 (109-e medium finding, coverage gap d): the original
    // assertions only checked `.exists()` on each hook, so an inverted
    // Enable/Disable label or a blank Trust-mode entry would still pass.
    // Checking rendered text catches that class of regression.
    expect(wrapper.find('[data-test="server-card-menu-toggle"]').text()).toBe('Disable')
    expect(wrapper.find('[data-test="server-card-menu-scan"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-menu-restart"]').text()).toBe('Restart')
    expect(wrapper.find('[data-test="server-card-menu-logs"]').text()).toBe('Logs')
    expect(wrapper.find('[data-test="server-card-menu-edit"]').text()).toBe('Edit')
    expect(wrapper.find('[data-test="server-card-menu-trust"]').text()).toContain('Auto')
    expect(wrapper.find('[data-test="server-card-menu-delete"]').text()).toBe('Delete')
  })

  it('shows Disable/Enable inverted for a disabled server', () => {
    const wrapper = mountCard(makeServer({ enabled: false }))
    expect(wrapper.find('[data-test="server-card-menu-toggle"]').text()).toBe('Enable')
  })

  it('Delete is reachable only from the ⋯ menu, and its confirmation names the server', async () => {
    const wrapper = mountCard(makeServer({ name: 'my-server' }))

    // Review round 1 (109-e medium finding, coverage gap c): the old check
    // used a directional CSS adjacent-sibling selector plus an exact-string
    // match on "Delete Server", both of which a bare "Delete" placed
    // anywhere else on the card FACE (outside the ⋯ menu) would evade.
    // Scanning the primary-action row's own text directly does not.
    const primaryRow = wrapper.find('[data-test="server-card-primary-row"]')
    expect(primaryRow.exists()).toBe(true)
    expect(primaryRow.text()).not.toContain('Delete')
    // Nor anywhere else on the card face outside the ⋯ dropdown-content menu.
    const menuContent = wrapper.find('[data-test="server-card-menu"]')
    const outsideMenuButtons = wrapper
      .findAll('button')
      .filter((b) => !menuContent.element.contains(b.element))
    expect(outsideMenuButtons.some((b) => b.text().includes('Delete'))).toBe(false)

    await wrapper.find('[data-test="server-card-menu-delete"]').trigger('click')

    expect(wrapper.text()).toContain('Are you sure you want to delete the server')
    expect(wrapper.text()).toContain('my-server')
    expect(wrapper.find('[data-test="server-card-delete-confirm"]').exists()).toBe(true)
  })

  // Review round 1 (109-e high finding): a server that is BOTH quarantined
  // AND needs OAuth sign-in reports `actions = ["login", "approve"]`
  // (FR-010) — the primary button surfaces only `actions[0]` ("Sign in"),
  // so "approve" would otherwise vanish with no path to review left on the
  // card at all. The ⋯ menu must offer Review independently of whichever
  // action is primary, the same way the ⋯-menu already gates Scan/Logout on
  // server state rather than on the primary action.
  it('offers Review from the ⋯ menu whenever the server is quarantined, even when it is not the primary action', () => {
    const wrapper = mountCard(
      makeServer({
        name: 'srv',
        quarantined: true,
        health: {
          level: 'degraded',
          admin_state: 'quarantined',
          summary: 'Sign-in required',
          status: 'signin_required',
          usable: false,
          actions: ['login', 'approve'],
          action: 'login',
        } as unknown as HealthStatus,
      })
    )

    // The primary button is still "Sign in" — Review does not fight it for
    // the one primary slot.
    const primaryButton = wrapper.find('[data-test="server-card-primary-action"]')
    expect(primaryButton.text()).toBe('Sign in')

    const review = wrapper.find('[data-test="server-card-menu-review"]')
    expect(review.exists()).toBe(true)
    expect(review.text()).toBe('Review')
    expect(review.attributes('href')).toBe('/servers/srv?tab=tools')
  })

  it('does not offer Review from the ⋯ menu for a non-quarantined server', () => {
    const wrapper = mountCard(makeServer({ quarantined: false }))
    expect(wrapper.find('[data-test="server-card-menu-review"]').exists()).toBe(false)
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

  // Review round 1 (109-e medium finding): the FR-013 icon-only rewrite left
  // the shield with no accessible name at all outside the invalid-value edge
  // case (the CSS `data-tip` tooltip is not read by a screen reader). Every
  // card's trust mode, not just the invalid one, must have an accessible
  // name.
  it('has an accessible name naming the trust mode, for every mode — not only the invalid case', () => {
    const wrapper = mountCard(makeServer({ trust_mode: 'auto' }))
    const shield = wrapper.find('[data-test="server-trust-mode"]')
    expect(shield.attributes('role')).toBe('img')
    expect(shield.attributes('aria-label')).toContain('Auto')
  })

  it('still names the effective mode and the invalid marker for an unrecognized value', () => {
    const wrapper = mountCard(makeServer({ trust_mode: 'bogus' }))
    const shield = wrapper.find('[data-test="server-trust-mode"]')
    expect(shield.attributes('aria-label')).toContain('not recognized')
    expect(shield.attributes('aria-label')).toContain('bogus')
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
