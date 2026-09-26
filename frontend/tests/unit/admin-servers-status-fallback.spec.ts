import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { healthStatusLabel } from '@/utils/health'

// Spec 109 FR-011 review finding: statusLabel()/healthLabel() fell back to
// `server.health.summary || server.health.level` when `status` is absent.
// Since `summary` can itself be empty (a payload from a core old/new enough
// to omit both — a version-skew scenario, not reachable against this
// branch's own core), that fallback would print the banned raw `level` word
// ("healthy"/"degraded"/"unhealthy") straight into the STATUS column,
// exactly what FR-011 forbids on every surface.

const getConfigMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: { getConfig: getConfigMock },
}))

import AdminServers from '@/views/teams/AdminServers.vue'

function makeRouter() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'root', component: { template: '<div />' } },
      { path: '/servers/:name', name: 'server-detail', component: { template: '<div />' } },
    ],
  })
  router.push('/')
  return router
}

async function mountAdminServers(servers: unknown[] = [
  {
    name: 'skewed-core',
    protocol: 'stdio',
    enabled: true,
    connected: true,
    quarantined: false,
    shared: false,
    // Neither `status` nor `summary` present — the version-skew shape
    // the finding describes. `level` must never leak as the rendered
    // text.
    health: { level: 'healthy', admin_state: 'enabled', summary: '' },
  },
]) {
  const router = makeRouter()
  await router.isReady()

  ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ servers }),
  })

  getConfigMock.mockResolvedValue({
    success: true,
    data: { config: { server_edition: { access: { group_servers: {}, default_servers: [] } } } },
  })

  const wrapper = mount(AdminServers, {
    global: { plugins: [router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

function statusBadgeText(wrapper: Awaited<ReturnType<typeof mountAdminServers>>, name: string) {
  const row = wrapper.findAll('tbody tr').find(r => r.text().includes(name))
  expect(row).toBeTruthy()
  const statusCell = row!.findAll('td')[3]
  const badge = statusCell.find('span.badge')
  expect(badge.exists()).toBe(true)
  return badge.text()
}

describe('AdminServers STATUS column (Spec 109 FR-011)', () => {
  beforeEach(() => {
    getConfigMock.mockReset()
  })

  it('renders the connected fallback, not the raw `level` word, when status and summary are both absent', async () => {
    const wrapper = await mountAdminServers()
    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('skewed-core'))
    expect(row).toBeTruthy()

    // Target the STATUS column specifically (Server, Protocol, Endpoint,
    // Status, Sharing, Groups, Actions) so this can't pass on a stray
    // "healthy"/"connected" appearing in an unrelated cell.
    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.exists()).toBe(true)

    // Positive assertion: `server.connected: true` with an empty
    // status/summary must fall through to the literal 'connected' label
    // (see statusLabel() in AdminServers.vue), not an empty string, which a
    // purely negative "doesn't contain a banned word" check would miss.
    expect(badge.text()).toBe('connected')
    for (const banned of ['healthy', 'degraded', 'unhealthy']) {
      expect(badge.text().toLowerCase()).not.toContain(banned)
    }
  })

  // This round's review finding: statusLabel()'s quarantined/disabled fallback
  // checks were hung off an `else if` attached to `if (server.health)`, so
  // they only ran when `server.health` was absent entirely. A server that HAS
  // a health object but whose summary/status are both empty (the same
  // version-skew shape as above) fell straight through to the bare
  // connected/disconnected fallback, skipping quarantined -> 'Needs review'
  // and disabled -> 'Disabled' — diverging from UserServers.vue's
  // healthLabel(), which re-checks `enabled` unconditionally in its own
  // fallback.
  it('renders "Disabled" for a disabled server with an empty-text health object, not connected/disconnected', async () => {
    const wrapper = await mountAdminServers([
      {
        name: 'disabled-skewed',
        protocol: 'stdio',
        enabled: false,
        connected: false,
        quarantined: false,
        shared: false,
        health: { level: 'healthy', admin_state: 'disabled', summary: '' },
      },
    ])

    const text = statusBadgeText(wrapper, 'disabled-skewed')
    expect(text).toBe(healthStatusLabel('disabled'))
    expect(text).not.toMatch(/^(dis)?connected$/)
  })

  it('renders "Needs review" for a quarantined server with an empty-text health object, not connected/disconnected', async () => {
    const wrapper = await mountAdminServers([
      {
        name: 'quarantined-skewed',
        protocol: 'stdio',
        enabled: true,
        connected: true,
        quarantined: true,
        shared: false,
        health: { level: 'unhealthy', admin_state: 'quarantined', summary: '' },
      },
    ])

    const text = statusBadgeText(wrapper, 'quarantined-skewed')
    expect(text).toBe(healthStatusLabel('needs_review'))
    expect(text).not.toMatch(/^(dis)?connected$/)
  })
})
