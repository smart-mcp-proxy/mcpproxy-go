import { describe, it, expect, beforeEach, vi } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109-m SC-002 (T145, M6): the Web leg of the attention parity chain. The
// REST golden internal/runtime/testdata/attention_parity_rest.json (written by
// the Go httpapi test from the real Compute + GET /attention handler) is served
// to the shared attention store; Home's list, the header pill and the sidebar
// Home badge must show the same ids, in the same order, with the same count.

const root = resolve(__dirname, '../../../internal/runtime/testdata')
const golden = JSON.parse(readFileSync(resolve(root, 'attention_parity_rest.json'), 'utf8')) as {
  count: number
  items: { id: string; summary: string }[]
}
const expectedIds = (JSON.parse(readFileSync(resolve(root, 'attention_parity_fixture.json'), 'utf8')) as {
  expected_ids: string[]
}).expected_ids

const attentionSpy = vi.hoisted(() => vi.fn())

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
    getAttention: attentionSpy,
    getServers: ok({ servers: [] }),
    getReviewQueue: ok({ count: 0, servers: [] }),
    getOnboardingState: ok({ incomplete_tab_count: 0, state: { engaged: true } }),
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
import SidebarNav from '@/components/SidebarNav.vue'
import TopHeader from '@/components/TopHeader.vue'

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
      { path: '/', name: 'home', component: Home },
      { path: '/servers/:name', name: 'server-detail', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountWith(component: object, stubs: Record<string, boolean> = {}) {
  const router = makeRouter()
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(component as never, {
    global: { plugins: [createPinia(), router], stubs: { RouterLink: false, ...stubs } },
  })
  await flushPromises()
  return wrapper
}

describe('attention parity on the Web UI (SC-002)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    attentionSpy.mockReset()
    attentionSpy.mockResolvedValue({ success: true, data: { ...golden, generated_at: '2026-10-02T12:00:00Z' } })
    ;(globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource
  })

  it('the golden carries exactly the Go-computed ids, in order', () => {
    expect(golden.items.map(i => i.id)).toEqual(expectedIds)
    expect(golden.count).toBe(expectedIds.length)
  })

  it('Home lists every item in the served order', async () => {
    const wrapper = await mountWith(Home, { AttentionList: false, UsageSummaryStrip: false })
    const ids = wrapper
      .findAll('[data-test^="attention-item-"]')
      .map(row => row.attributes('data-test')!.replace('attention-item-', ''))
    expect(ids).toEqual(expectedIds)
  })

  it('the header pill shows the same count', async () => {
    const wrapper = await mountWith(TopHeader)
    const pill = wrapper.find('[data-test="header-attention-pill"]')
    expect(pill.exists()).toBe(true)
    expect(pill.text()).toContain(String(expectedIds.length))
  })

  it('the sidebar Home badge shows the same count', async () => {
    const wrapper = await mountWith(SidebarNav)
    expect(wrapper.find('[data-test="sidebar-home-badge"]').text()).toBe(String(expectedIds.length))
  })
})
