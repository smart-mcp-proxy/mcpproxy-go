import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ServerCard from '@/components/ServerCard.vue'
import { healthStatusText } from '@/utils/health'
import type { Server, HealthStatus } from '@/types'

// Spec 109-m SC-003 (T146, M7): for every health status that is not `ready`
// (usable=false), no Web renderer prints "healthy", "online" or "connected" as
// the server's state: the Servers list card (status line) and the ServerDetail
// header. The card already had a per-status test (health-status-labels.spec.ts);
// this adds the detail header and the card's last-resort fallback branch, which
// used to say "Connected" for a connected-but-unusable server whose payload
// carried neither status nor summary (version skew).

const statuses = (
  JSON.parse(
    readFileSync(resolve(__dirname, '../../../internal/health/testdata/status_fixtures.json'), 'utf8'),
  ) as { status_order: string[] }
).status_order.filter(s => s !== 'ready')

const forbidden = /\b(healthy|online|connected)\b/i

type Overrides = Record<string, unknown>
const state = { server: {} as Overrides }

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

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ hasEnabledScanners: () => true }),
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

function server(health: Partial<HealthStatus>, connected = true): Overrides {
  return {
    name: 'probe',
    protocol: 'http',
    url: 'https://example.invalid/mcp',
    enabled: true,
    quarantined: false,
    connected,
    connecting: false,
    tool_count: 3,
    health,
  }
}

// The worst case for a renderer that leaks: the legacy level says healthy, the
// summary is empty (so the label table is the only text source), and the
// legacy `connected` field is true.
function unusable(status: string): Partial<HealthStatus> {
  return { level: 'healthy', admin_state: 'enabled', summary: '', status, usable: false, actions: [] } as Partial<HealthStatus>
}

async function mountDetail(s: Overrides) {
  state.server = s
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/probe')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'probe' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return wrapper
}

function mountCard(s: Overrides) {
  return mount(ServerCard, {
    props: { server: s as unknown as Server },
    global: { plugins: [createPinia()], stubs: { RouterLink: RouterLinkStub, 'router-link': RouterLinkStub } },
  })
}

describe('SC-003 on the Web UI: usable=false never renders healthy/online/connected', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('covers every non-ready status', () => {
    expect(statuses.length).toBe(7)
  })

  for (const status of statuses) {
    it(`${status}: Servers list card status line`, () => {
      const text = mountCard(server(unusable(status))).find('[data-test="server-status-chip"]').text()
      expect(text).not.toMatch(forbidden)
    })

    it(`${status}: ServerDetail header badge`, async () => {
      const wrapper = await mountDetail(server(unusable(status)))
      const badge = wrapper.find('[data-test="server-status-badge"]')
      expect(badge.exists()).toBe(true)
      expect(badge.text()).not.toMatch(forbidden)
    })
  }

  it('the last-resort fallback never says Connected for a connected-but-unusable server', () => {
    // A version-skew payload: no status, no summary, usable=false, connected=true.
    const health = { level: 'healthy', admin_state: 'enabled', summary: '', status: '', usable: false, actions: [] }
    expect(healthStatusText(health, true)).not.toMatch(forbidden)
    expect(mountCard(server(health as Partial<HealthStatus>, true)).find('[data-test="server-status-chip"]').text()).not.toMatch(forbidden)
  })

  it('the fallback still reads Connected / Disconnected when the server is usable or unknown', () => {
    expect(healthStatusText({ summary: '', status: '', usable: true } as never, true)).toBe('Connected')
    expect(healthStatusText({ summary: '', status: '' }, true)).toBe('Connected')
    expect(healthStatusText({ summary: '', status: '' }, false)).toBe('Disconnected')
  })
})
