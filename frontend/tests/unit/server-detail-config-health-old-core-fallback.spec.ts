import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109 FR-011 review finding: the Config tab's Health card Status field
// and "Suggested Action" row are gated on the NEW `status`/`actions` fields
// with no fallback to the pre-109-c legacy `action` field, so an old-core
// payload (still shipping only `level`/`admin_state`/`summary`/`action`)
// renders a blank Status and drops the Suggested Action row entirely — a
// regression from pre-109-c behavior, and inconsistent with this same PR's
// macOS isUsable fallback, which deliberately still supports that old-core
// shape.

const state = { server: {} as Record<string, unknown> }

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() => ok({ servers: [state.server] })),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getScanReport: vi.fn(() => ok({})),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
    },
  }
})

async function mountConfigTab(server: Record<string, unknown>) {
  state.server = server
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/probe?tab=config')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'probe' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

const base = {
  name: 'probe',
  protocol: 'http',
  enabled: true,
  connected: false,
  quarantined: false,
  tool_count: 0,
}

describe('ServerDetail Config tab Health card — old-core fallback (Spec 109 FR-011)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('falls back to `summary` for the Status field when `status` is absent (old-core payload)', async () => {
    const wrapper = await mountConfigTab({
      ...base,
      health: {
        level: 'unhealthy',
        admin_state: 'enabled',
        summary: 'Missing secret',
        action: 'set_secret',
        // no `status`, no `usable`, no `actions` — the pre-109-c shape.
      },
    })
    const statusField = wrapper.find('[data-test="server-config-health-status"]')
    expect(statusField.exists()).toBe(true)
    expect(statusField.text()).toBe('Missing secret')
  })

  it('falls back to the legacy singular `action` for the Suggested Action row when `actions` is absent', async () => {
    const wrapper = await mountConfigTab({
      ...base,
      health: {
        level: 'unhealthy',
        admin_state: 'enabled',
        summary: 'Missing secret',
        action: 'set_secret',
      },
    })
    const actionsRow = wrapper.find('[data-test="server-config-health-actions"]')
    expect(actionsRow.exists()).toBe(true)
    expect(actionsRow.text()).toContain('Add secret')
  })

  it('still prefers `status`/`actions` when the core sends both (no double-rendering)', async () => {
    const wrapper = await mountConfigTab({
      ...base,
      health: {
        level: 'unhealthy',
        admin_state: 'enabled',
        summary: 'Missing secret',
        action: 'set_secret',
        status: 'needs_secret',
        usable: false,
        actions: ['set_secret'],
      },
    })
    const statusField = wrapper.find('[data-test="server-config-health-status"]')
    expect(statusField.text()).toBe('Secret required')
    const actionsRow = wrapper.find('[data-test="server-config-health-actions"]')
    const badges = actionsRow.findAll('span')
    expect(badges).toHaveLength(1)
    expect(badges[0].text()).toBe('Add secret')
  })
})
