import { describe, it, expect, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'

import UserServers from '@/views/teams/UserServers.vue'

// Round-6 review finding: TestUpstreamServerRows-style coverage existed for
// the `server.health` fallback logic (user-servers-status-fallback.spec.ts),
// but GET /api/v1/user/servers's real response
// (internal/serveredition/api/user_handlers.go ServerResponse) never sends
// `connected` or `health` at all — it embeds only *config.ServerConfig plus
// `ownership`/`user_enabled`. That earlier test mocked a payload shape the
// production endpoint cannot produce, so the suite went green while covering
// a code path production never reaches, leaving the endpoint's REAL,
// currently-degraded contract untested: every enabled server renders
// 'disconnected' regardless of its actual connection state, because the
// `!server.health` fallback branch is the only one ever exercised in
// practice.
//
// This test pins that real, if imperfect, contract instead: a payload
// shaped exactly like ServerResponse's actual JSON (no `connected`, no
// `health`) must render 'disconnected' for an enabled server and 'disabled'
// for a disabled one. If a future change wires real connection/health data
// into this endpoint, this test's mock payload should gain `connected`/
// `health` fields and its expectations should change to match — at which
// point this comment (and the one on healthLabel/healthBadgeClass in
// UserServers.vue) should be removed too.

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

async function mountUserServersWithRealShapePayload() {
  const router = makeRouter()
  await router.isReady()

  // Shaped exactly like the real /api/v1/user/servers response: a flattened
  // config.ServerConfig plus `ownership`/`user_enabled` only. No `connected`,
  // no `health` — those fields simply do not exist on ServerResponse today.
  ;(globalThis as unknown as { fetch: unknown }).fetch = vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({
      personal: [
        {
          name: 'real-personal-server',
          protocol: 'stdio',
          command: 'npx',
          enabled: true,
        },
      ],
      shared: [
        {
          name: 'real-shared-server',
          protocol: 'http',
          url: 'https://example.test/mcp',
          enabled: false,
          ownership: 'shared',
        },
      ],
    }),
  })

  const wrapper = mount(UserServers, {
    global: { plugins: [router] },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('UserServers with the real /api/v1/user/servers payload shape', () => {
  it('renders "disconnected" for an enabled server, since the endpoint sends no connected/health field', async () => {
    const wrapper = await mountUserServersWithRealShapePayload()
    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('real-personal-server'))
    expect(row).toBeTruthy()

    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('disconnected')
  })

  it('renders "disabled" for a disabled server regardless of the missing connected/health fields', async () => {
    const wrapper = await mountUserServersWithRealShapePayload()
    const row = wrapper.findAll('tbody tr').find(r => r.text().includes('real-shared-server'))
    expect(row).toBeTruthy()

    const statusCell = row!.findAll('td')[3]
    const badge = statusCell.find('span.badge')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('disabled')
  })
})
