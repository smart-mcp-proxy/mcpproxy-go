import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// UX audit F4 (residual after #1044): the analytics panel is now the default
// landing page and the hub diagram is demoted to the Overview tab — but the two
// banners that answer "what needs me" (servers in a bad state, tools awaiting
// approval) went down with it. A landing page that renders charts while a
// server is down and an unreviewed tool is waiting, and says neither, is only
// half of the finding's fix.
//
// These tests pin the banners above the panel switcher: visible on whichever
// panel is active, and absent when there is genuinely nothing to act on.

const pendingSpy = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const fakeEventSource = {
    onopen: null,
    onmessage: null,
    onerror: null,
    addEventListener() {},
    removeEventListener() {},
    close() {},
  }
  const base: Record<string, unknown> = {
    getServers: ok({
      servers: [
        {
          name: 'broken',
          enabled: true,
          connected: false,
          quarantined: false,
          tool_count: 0,
          health: {
            level: 'unhealthy',
            admin_state: 'enabled',
            summary: 'Connection refused',
            action: 'restart',
          },
        },
      ],
    }),
    getToolApprovals: pendingSpy,
    createEventSource: vi.fn(() => fakeEventSource),
    hasAPIKey: vi.fn(() => true),
    getAPIKeyPreview: vi.fn(() => 'test…'),
    onAuthError: vi.fn(() => () => {}),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = ok()
        return target[prop]
      },
    }),
  }
})

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  refreshSecurityScannerStatus: vi.fn().mockResolvedValue(undefined),
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Dashboard from '@/views/Dashboard.vue'
import { useServersStore } from '@/stores/servers'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/usage', name: 'usage', component: Dashboard, meta: { dashboardView: 'usage' } },
      { path: '/overview', name: 'dashboard-overview', component: Dashboard, meta: { dashboardView: 'overview' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountDashboard(path = '/') {
  const router = makeRouter()
  router.push(path)
  await router.isReady()
  const wrapper = shallowMount(Dashboard, {
    global: {
      plugins: [createPinia(), router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

/**
 * The banners must not be inside either panel wrapper — a `v-show`-hidden
 * ancestor would render them into the DOM while keeping them invisible, which
 * an `exists()` assertion alone would happily pass.
 */
function isOutsidePanels(el: Element): boolean {
  let node: Element | null = el.parentElement
  while (node) {
    const dt = node.getAttribute?.('data-test')
    if (dt === 'dashboard-usage-panel' || dt === 'dashboard-overview-panel') return false
    node = node.parentElement
  }
  return true
}

describe('Dashboard "what needs me" banners (F4 residual)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    pendingSpy.mockReset()
    pendingSpy.mockResolvedValue({ success: true, data: { tools: [] } })
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('shows the servers-needing-attention banner on the default landing panel', async () => {
    const wrapper = await mountDashboard('/')

    const banner = wrapper.findAll('.alert-warning').find(w => w.text().includes('needs attention'))
    expect(banner, 'attention banner missing from the default landing page').toBeTruthy()
    expect(banner!.text()).toContain('broken')
    expect(banner!.text()).toContain('Connection refused')
  })

  it('renders the banner outside both panel wrappers, so no tab can hide it', async () => {
    const wrapper = await mountDashboard('/')

    const banner = wrapper.findAll('.alert-warning').find(w => w.text().includes('needs attention'))
    expect(banner).toBeTruthy()
    expect(isOutsidePanels(banner!.element)).toBe(true)
  })

  it('still shows it on the Overview panel', async () => {
    const wrapper = await mountDashboard('/overview')

    const banner = wrapper.findAll('.alert-warning').find(w => w.text().includes('needs attention'))
    expect(banner).toBeTruthy()
  })

  it('shows the pending-tools banner on the default landing panel', async () => {
    pendingSpy.mockResolvedValue({
      success: true,
      data: { tools: [{ name: 'broken:danger', server_name: 'broken', status: 'pending' }] },
    })
    const wrapper = await mountDashboard('/')

    const banner = wrapper.findAll('.alert-warning').find(w => w.text().includes('pending approval'))
    expect(banner, 'pending-tools banner missing from the default landing page').toBeTruthy()
    expect(isOutsidePanels(banner!.element)).toBe(true)
  })

  it('stays silent when nothing needs attention', async () => {
    const wrapper = await mountDashboard('/')
    const store = useServersStore()
    store.servers = [
      { name: 'ok', enabled: true, connected: true, quarantined: false, tool_count: 3 } as never,
    ]
    await flushPromises()

    const banner = wrapper.findAll('.alert-warning').find(w => w.text().includes('needs attention'))
    expect(banner).toBeUndefined()
  })
})
