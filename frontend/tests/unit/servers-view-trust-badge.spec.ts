import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { TRUST_MODES } from '@/utils/trustMode'

// Spec 088 US1 / FR-007 (T012): the servers list must show each server's trust
// mode at a glance. The list renders ServerCard tiles, so the compact badge
// lives next to the existing server-level status chip. The badge label comes
// from utils/trustMode.ts TRUST_MODES and always reflects the EFFECTIVE mode
// (fail-closed to manual); an unrecognized raw value is shown as effective +
// a subtle marker rather than being hidden or silently rewritten
// (US1 scenario 4 / FR-001).

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

async function mountServers(servers: ReturnType<typeof makeServer>[]) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const { useServersStore } = await import('@/stores/servers')
  const store = useServersStore()
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  store.servers = servers as any

  const Servers = (await import('@/views/Servers.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push('/')
  await router.isReady()

  const wrapper = mount(Servers, { global: { plugins: [pinia, router] } })
  await flushPromises()
  return wrapper
}

describe('Servers list — trust-mode badge (spec 088 FR-007)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('renders one trust-mode badge per server tile', async () => {
    const wrapper = await mountServers([
      makeServer('alpha', 'auto'),
      makeServer('beta', 'scan'),
      makeServer('gamma', 'manual'),
    ])
    const badges = wrapper.findAll('[data-test="server-trust-mode"]')
    expect(badges).toHaveLength(3)
  })

  it.each(TRUST_MODES.map((m) => [m.mode, m.label] as const))(
    'shows the compact TRUST_MODES label for trust_mode=%s',
    async (mode, label) => {
      const wrapper = await mountServers([makeServer('srv', mode)])
      const badge = wrapper.find('[data-test="server-trust-mode"]')
      expect(badge.exists()).toBe(true)
      expect(badge.text()).toContain(label)
    }
  )

  it('shows the effective default (Manual) when trust_mode is unset, with no invalid marker', async () => {
    const wrapper = await mountServers([makeServer('unset')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.text()).toContain('Manual')
    expect(badge.attributes('data-trust-invalid')).toBeUndefined()
  })

  it('shows the effective mode plus a marker (raw value in the tooltip) for an unrecognized value', async () => {
    const wrapper = await mountServers([makeServer('hand-edited', 'bogus')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    // Effective mode is shown, never the raw value as if it were a mode.
    expect(badge.text()).toContain('Manual')
    expect(badge.text()).not.toContain('bogus')
    // Subtle marker distinguishes it from an explicitly-configured manual.
    expect(badge.attributes('data-trust-invalid')).toBe('true')
    expect(badge.attributes('title')).toContain('bogus')
  })

  it('mis-cased values fail closed like any other unrecognized value', async () => {
    const wrapper = await mountServers([makeServer('miscased', 'Scan')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.text()).toContain('Manual')
    expect(badge.attributes('data-trust-invalid')).toBe('true')
  })

  it('styles the badge like the neighbouring server-level chips', async () => {
    const wrapper = await mountServers([makeServer('styled', 'scan')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.classes()).toContain('badge')
    expect(badge.classes()).toContain('badge-sm')
    // Sits alongside the existing status chip, not replacing it.
    expect(wrapper.find('[data-test="server-status-chip"]').exists()).toBe(true)
  })

  it('explains the mode in the tooltip for a valid mode', async () => {
    const wrapper = await mountServers([makeServer('tip', 'auto')])
    const badge = wrapper.find('[data-test="server-trust-mode"]')
    expect(badge.attributes('title')).toContain('Trust mode')
    expect(badge.attributes('title')).toContain('Auto')
  })
})
