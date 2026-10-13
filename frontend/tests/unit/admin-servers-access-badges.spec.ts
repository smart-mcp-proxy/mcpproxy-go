import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 107 PR-C cross-review round 3, chunk 4 (P3): AdminServers.vue's
// read-only Groups-column annotation never expanded the "*" wildcard
// (entitlement-predicate.md §1: a group/default grant of "*" is every
// SHARED server) and never excluded a private server named directly in
// group_servers, even though entitlement composition only ever admits a
// Shared server. Both misrepresented the live access configuration to an
// administrator reading this table.

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

async function mountAdminServers() {
  const router = makeRouter()
  await router.isReady()

  ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      servers: [
        { name: 'shared-a', protocol: 'stdio', enabled: true, connected: true, quarantined: false, shared: true },
        { name: 'shared-b', protocol: 'stdio', enabled: true, connected: true, quarantined: false, shared: true },
        { name: 'private-a', protocol: 'stdio', enabled: true, connected: true, quarantined: false, shared: false },
      ],
    }),
  })

  getConfigMock.mockResolvedValue({
    success: true,
    data: {
      config: {
        server_edition: {
          access: {
            // "*" must expand to every SHARED server (shared-a, shared-b),
            // never to private-a.
            group_servers: { eng: ['*'], ops: ['private-a'] },
            default_servers: [],
          },
        },
      },
    },
  })

  const wrapper = mount(AdminServers, {
    global: { plugins: [router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('AdminServers access-group badges (Spec 107 cross-review round 3, chunk 4 P3)', () => {
  beforeEach(() => {
    getConfigMock.mockReset()
  })

  it('expands "*" to every shared server, not the literal string', async () => {
    const wrapper = await mountAdminServers()
    const rows = wrapper.findAll('tbody tr')
    const sharedA = rows.find(r => r.text().includes('shared-a'))
    const sharedB = rows.find(r => r.text().includes('shared-b'))
    expect(sharedA?.text()).toContain('eng')
    expect(sharedB?.text()).toContain('eng')
  })

  it('never labels a private server as granted via a group', async () => {
    const wrapper = await mountAdminServers()
    const rows = wrapper.findAll('tbody tr')
    const privateA = rows.find(r => r.text().includes('private-a'))
    expect(privateA?.text()).not.toContain('ops')
  })
})
