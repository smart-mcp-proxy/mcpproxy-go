import { describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { HEALTH_STATUS_LABELS, type HealthStatusValue } from '@/types/contracts'

// Spec 134 FR-004 / SC-003: the two server-edition status cells (/my/servers,
// /admin/servers) print the Spec 109 label through the one status pill and keep
// `health.summary` as separate detail. Without `status` the present fallback
// (tested in *-status-fallback.spec.ts) is unchanged.

const getConfigMock = vi.hoisted(() => vi.fn())
vi.mock('@/services/api', () => ({ default: { getConfig: getConfigMock } }))

import AdminServers from '@/views/teams/AdminServers.vue'
import UserServers from '@/views/teams/UserServers.vue'

const STATUSES = Object.keys(HEALTH_STATUS_LABELS) as HealthStatusValue[]

function server(status: HealthStatusValue) {
  return {
    name: `s-${status}`, protocol: 'stdio', enabled: status !== 'disabled', connected: status === 'ready',
    quarantined: status === 'needs_review', shared: false,
    health: { level: 'unhealthy', admin_state: 'enabled', summary: `summary of ${status}`, status, usable: status === 'ready', actions: [] },
  }
}

async function mountView(view: 'admin' | 'user') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }, { path: '/servers/:name', name: 'server-detail', component: { template: '<div />' } }],
  })
  router.push('/')
  await router.isReady()
  const servers = STATUSES.map(server)
  ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => (view === 'admin' ? { servers } : { personal: servers, shared: [] }),
  })
  getConfigMock.mockResolvedValue({ success: true, data: { config: { server_edition: { access: { group_servers: {}, default_servers: [] } } } } })
  const wrapper = mount(view === 'admin' ? AdminServers : UserServers, { global: { plugins: [router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe.each(['admin', 'user'] as const)('%s servers status cell (spec 134 FR-004)', (view) => {
  it.each(STATUSES)('%s prints its Spec 109 label via the pill and the summary as detail', async (status) => {
    const wrapper = await mountView(view)
    const row = wrapper.findAll('tbody tr').find((r) => r.text().includes(`s-${status}`))!
    expect(row).toBeTruthy()
    expect(row.get('[data-test="status-pill"]').text()).toBe(HEALTH_STATUS_LABELS[status])
    expect(row.get('[data-test="server-status-summary"]').text()).toBe(`summary of ${status}`)
  })
})
