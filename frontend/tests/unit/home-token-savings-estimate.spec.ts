import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-073 (T166, #1394 item 1): the Home hub chip, the Token Savings
// Details stat and the details title badge say "estimate" while the core
// reports `ServerTokenMetrics.estimated`, exactly as Usage.vue already does.

const tokenStatsSpy = vi.hoisted(() => vi.fn())

function tokenStats(over: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      saved_tokens: 4200,
      saved_tokens_percentage: 92.5,
      total_server_tool_list_size: 4540,
      average_query_result_size: 340,
      per_server_tool_list_sizes: { notes: 4540 },
      ...over,
    },
  }
}

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
    getAttention: ok({ count: 0, items: [] }),
    getTokenStats: tokenStatsSpy,
    getServers: ok({ servers: [{ name: 'notes', enabled: true, connected: true, tool_count: 3 }] }),
    getActivitySummary: ok({ call_count: 0, blocked_count: 0, call_error_count: 0 }),
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

import Home from '@/views/Home.vue'
import api from '@/services/api'
import { useAuthStore } from '@/stores/auth'

class FakeEventSource {
  close() {}
  addEventListener() {}
  onmessage: ((e: unknown) => void) | null = null
  onerror: ((e: unknown) => void) | null = null
}

async function mountHome(tenant = false) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: Home },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
  router.push('/')
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  if (tenant) {
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.user = {
      id: 'carol',
      email: 'carol@example.com',
      display_name: 'Carol',
      role: 'user',
      provider: 'oidc',
      created_at: '',
      last_login_at: '',
    }
    // A tenant has no local API key.
    ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(false)
  }
  const wrapper = shallowMount(Home, {
    global: {
      plugins: [pinia, router],
      stubs: { RouterLink: { template: '<a><slot /></a>' }, AttentionList: false },
    },
  })
  await flushPromises()
  return wrapper
}

const CHIP = '[data-test="dashboard-token-savings-chip"]'
const CHIP_ESTIMATE = '[data-test="dashboard-token-savings-estimate"]'
const DETAILS_ESTIMATE = '[data-test="dashboard-token-savings-details-estimate"]'
const SAVED_BADGE = '[data-test="dashboard-token-savings-saved-badge"]'

describe('Home token savings estimate label (Spec 109 FR-073)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    tokenStatsSpy.mockReset()
    ;(api.hasAPIKey as unknown as ReturnType<typeof vi.fn>).mockReturnValue(true)
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('marks the hub chip, the details stat and the title badge as an estimate', async () => {
    tokenStatsSpy.mockResolvedValue(tokenStats({ estimated: true }))
    const wrapper = await mountHome()

    const chipMarker = wrapper.find(`${CHIP} ${CHIP_ESTIMATE}`)
    expect(chipMarker.exists()).toBe(true)
    expect(chipMarker.text()).toBe('estimate')
    expect(chipMarker.attributes('title')).toContain('simulated estimate')

    const detailsMarker = wrapper.find(DETAILS_ESTIMATE)
    expect(detailsMarker.exists()).toBe(true)
    expect(detailsMarker.text()).toBe('estimate')

    expect(wrapper.find(SAVED_BADGE).text()).toMatch(/· estimate$/)
  })

  it('renders no estimate marker once the figure is measured', async () => {
    tokenStatsSpy.mockResolvedValue(tokenStats({ estimated: false }))
    const wrapper = await mountHome()

    expect(wrapper.find(CHIP).exists()).toBe(true)
    expect(wrapper.find(CHIP_ESTIMATE).exists()).toBe(false)
    expect(wrapper.find(DETAILS_ESTIMATE).exists()).toBe(false)
    expect(wrapper.find(SAVED_BADGE).text()).toBe('4.2K saved')
  })

  it('treats an absent `estimated` (an older core) as measured', async () => {
    tokenStatsSpy.mockResolvedValue(tokenStats())
    const wrapper = await mountHome()

    expect(wrapper.find(CHIP).exists()).toBe(true)
    expect(wrapper.find(CHIP_ESTIMATE).exists()).toBe(false)
    expect(wrapper.find(DETAILS_ESTIMATE).exists()).toBe(false)
    expect(wrapper.find(SAVED_BADGE).text()).toBe('4.2K saved')
  })

  it('never requests token stats, and renders no chip, for a tenant principal', async () => {
    tokenStatsSpy.mockResolvedValue(tokenStats({ estimated: true }))
    const wrapper = await mountHome(true)

    expect(tokenStatsSpy).not.toHaveBeenCalled()
    expect(wrapper.find(CHIP).exists()).toBe(false)
  })
})
