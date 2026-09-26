import { describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 status-text precedence (FR-011/FR-014): every surface must
// resolve a server's status TEXT through the same helper and the same
// fallback order — `healthStatusText()` in @/utils/health, which is
// `summary || label(status) || connected-fallback` (see its doc comment and
// the ServerCard/ServerDetail specs that lock that order in).
//
// AdminServers.vue and UserServers.vue instead carried their OWN inline
// `statusLabel()`/`healthLabel()` that checked `status` BEFORE `summary` —
// the reverse order — so the same health payload rendered different text
// on the teams tables than it did on ServerCard/ServerDetail. This spec
// pins the ONE order across all four surfaces.

const getConfigMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: { getConfig: getConfigMock },
}))

import AdminServers from '@/views/teams/AdminServers.vue'
import UserServers from '@/views/teams/UserServers.vue'

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

// A health payload where `status` ("ready" -> label "Online") and `summary`
// ("Connected (14 tools)") disagree — the case that distinguishes the two
// possible precedence orders.
const disagreeingHealth = {
  level: 'healthy',
  admin_state: 'enabled',
  status: 'ready',
  summary: 'Connected (14 tools)',
}

describe('status-text precedence is the SAME on every surface (summary before the status label)', () => {
  it('AdminServers STATUS column prefers summary over the status label, like ServerCard', async () => {
    const router = makeRouter()
    await router.isReady()

    ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        servers: [
          {
            name: 'agreeing-and-disagreeing',
            protocol: 'stdio',
            enabled: true,
            connected: true,
            quarantined: false,
            shared: false,
            health: disagreeingHealth,
          },
        ],
      }),
    })
    getConfigMock.mockResolvedValue({
      success: true,
      data: { config: { server_edition: { access: { group_servers: {}, default_servers: [] } } } },
    })

    const wrapper = mount(AdminServers, { global: { plugins: [router] } })
    await flushPromises()
    await flushPromises()

    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('agreeing-and-disagreeing'))
    expect(row).toBeTruthy()
    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.text()).toBe('Connected (14 tools)')
    expect(badge.text()).not.toBe('Online')
  })

  it('UserServers Status column prefers summary over the status label, like ServerCard', async () => {
    const router = makeRouter()
    await router.isReady()

    ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({
        personal: [
          {
            name: 'agreeing-and-disagreeing',
            protocol: 'stdio',
            enabled: true,
            connected: true,
            health: disagreeingHealth,
          },
        ],
        shared: [],
      }),
    })

    const wrapper = mount(UserServers, { global: { plugins: [router] } })
    await flushPromises()
    await flushPromises()

    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('agreeing-and-disagreeing'))
    expect(row).toBeTruthy()
    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.text()).toBe('Connected (14 tools)')
    expect(badge.text()).not.toBe('Online')
  })
})
