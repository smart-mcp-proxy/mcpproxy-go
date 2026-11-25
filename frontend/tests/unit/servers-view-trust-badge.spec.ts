import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { TRUST_MODES } from '@/utils/trustMode'

// Spec 088 US1 / FR-007 (T012): the servers list must show each server's trust
// mode at a glance. The list renders ServerRow tiles, so the badge lives next
// to the existing server-level status chip. The mode comes from
// utils/trustMode.ts TRUST_MODES and always reflects the EFFECTIVE mode
// (fail-closed to manual); an unrecognized raw value is shown as effective +
// a subtle marker rather than being hidden or silently rewritten
// (US1 scenario 4 / FR-001).
//
// Spec 109 FR-013 (109-e) redefined the rendering: "Trust mode is a shield
// icon with a tooltip" — no visible text label on the card face any more, so
// the assertions below read the TOOLTIP (`title`/`data-tip`) instead of the
// element's text, which is now icon-only.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getSecurityOverview: vi.fn(() => ok({ scanners_enabled: 0, total_scans: 0 })),
      getServers: vi.fn(() => ok({ servers: [] })),
      scanAll: vi.fn(() => ok({})),
    },
  }
})

function makeServer(name: string, trustMode?: string) {
  return {
    name,
    protocol: 'stdio' as const,
    enabled: true,
    connected: true,
    quarantined: false,
    status: 'ready',
    reconnect_count: 0,
    tool_count: 3,
    created: '2026-07-28T00:00:00Z',
    updated: '2026-07-28T00:00:00Z',
    ...(trustMode === undefined ? {} : { trust_mode: trustMode }),
  }
}

// Spec 134 FR-020: the shield moved off the Servers row to the first screen of
// server detail (ServerFacts). The row keeps "Trust mode: <label>" in its menu.
async function mountServers(servers: ReturnType<typeof makeServer>[]) {
  setActivePinia(createPinia())
  const ServerFacts = (await import('@/components/ServerFacts.vue')).default
  const wrappers = servers.map(server => mount(ServerFacts, { props: { server: server as any } }))
  // Single-server helper surface used by the cases below.
  const first = wrappers[0]
  return Object.assign(first, { all: wrappers })
}

describe('Server detail facts — trust-mode badge (spec 088 FR-007, spec 134 FR-020)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders one trust-mode badge per server', async () => {
    const wrapper = await mountServers([
      makeServer('alpha', 'auto'),
      makeServer('beta', 'scan'),
      makeServer('gamma', 'manual'),
    ])
    const badges = (wrapper as any).all.flatMap((w: any) => w.findAll('[data-test="server-trust-mode"]'))
    expect(badges).toHaveLength(3)
  })

  it.each(TRUST_MODES.map((m) => [m.mode, m.label] as const))(
    'names the mode in the tooltip for trust_mode=%s',
    async (mode, label) => {
      const wrapper = await mountServers([makeServer('srv', mode)])
      const badge = wrapper.find('[data-test="server-trust-mode"]')
      expect(badge.exists()).toBe(true)
      expect(badge.attributes('data-tip')).toContain(label)
    }
  )

  it('shows the effective default (Scan, spec 119) when trust_mode is unset, with no invalid marker', async () => {
    const wrapper = await mountServers([makeServer('unset')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.attributes('data-tip')).toContain('Scan')
    expect(badge.attributes('data-tip')).toContain('default')
    expect(badge.attributes('data-trust-invalid')).toBeUndefined()
  })

  it('shows the effective mode plus a marker (raw value in the tooltip) for an unrecognized value', async () => {
    const wrapper = await mountServers([makeServer('hand-edited', 'bogus')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    // Effective mode is named, and the raw value appears too — but only in
    // the tooltip explaining WHY it fell back, never as if it were a mode.
    expect(badge.attributes('data-tip')).toContain('Manual')
    // Subtle marker distinguishes it from an explicitly-configured manual.
    expect(badge.attributes('data-trust-invalid')).toBe('true')
    expect(badge.attributes('data-tip')).toContain('bogus')
  })

  it('mis-cased values fail closed like any other unrecognized value', async () => {
    const wrapper = await mountServers([makeServer('miscased', 'Scan')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.attributes('data-tip')).toContain('Manual')
    expect(badge.attributes('data-trust-invalid')).toBe('true')
  })

  it('renders as a shield icon with its label as text', async () => {
    const wrapper = await mountServers([makeServer('styled', 'scan')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.find('svg').exists()).toBe(true)
    // The label is also printed as text (no hover needed on a phone).
    expect(wrapper.find('[data-test="server-facts-trust-label"]').text()).toBe('Scan')
  })

  it('explains the mode in the tooltip for a valid mode', async () => {
    const wrapper = await mountServers([makeServer('tip', 'auto')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.attributes('data-tip')).toContain('Trust mode')
    expect(badge.attributes('data-tip')).toContain('Auto')
  })
})

describe('Servers row — trust mode stays reachable from the more menu', () => {
  it('lists "Trust mode: <label>" linking to the configuration tab', async () => {
    setActivePinia(createPinia())
    const ServerRow = (await import('@/components/ServerRow.vue')).default
    const wrapper = mount(ServerRow, { props: { server: makeServer('srv', 'auto') as any }, global: { stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } } } })
    const entry = wrapper.find('[data-test="server-card-menu-trust"]')
    expect(entry.text()).toBe('Trust mode: Auto')
    expect(entry.attributes('href')).toBe('/servers/srv?tab=config')
  })
})
