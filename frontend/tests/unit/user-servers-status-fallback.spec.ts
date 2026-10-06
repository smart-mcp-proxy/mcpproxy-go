import { describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'

import UserServers from '@/views/teams/UserServers.vue'

// Spec 109 FR-011 review finding: healthLabel() fell back to
// `server.health.summary || server.health.level` when `status` is absent.
// Since `summary` can itself be empty (a version-skew payload from an
// older/newer core), that fallback would print the banned raw `level` word
// ("healthy"/"degraded"/"unhealthy") straight into the Status column.

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

async function mountUserServers() {
  const router = makeRouter()
  await router.isReady()

  ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      personal: [
        {
          name: 'skewed-core',
          protocol: 'stdio',
          enabled: true,
          connected: true,
          // Neither `status` nor `summary` present.
          health: { level: 'healthy', admin_state: 'enabled', summary: '' },
        },
      ],
      shared: [],
    }),
  })

  const wrapper = mount(UserServers, {
    global: { plugins: [router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('UserServers Status column (Spec 109 FR-011)', () => {
  it('renders the connected fallback, not the raw `level` word, when status and summary are both absent', async () => {
    const wrapper = await mountUserServers()
    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('skewed-core'))
    expect(row).toBeTruthy()

    // Target the Status column specifically (Server, Protocol, Endpoint,
    // Status, Tools, Actions) so this can't pass on a stray "connected" or
    // "healthy" appearing in some other cell.
    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.exists()).toBe(true)

    // Positive assertion: `server.connected: true` with an empty
    // status/summary must fall through to the literal 'connected' label
    // (see healthLabel() in UserServers.vue), not an empty string, which a
    // purely negative "doesn't contain a banned word" check would miss.
    expect(badge.text()).toBe('connected')
    for (const banned of ['healthy', 'degraded', 'unhealthy']) {
      expect(badge.text().toLowerCase()).not.toContain(banned)
    }
  })
})
