import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-003/FR-051: the sidebar's "Dashboard" entry is renamed "Home"
// and carries the same FR-001 needs-attention count as a badge — hidden at
// 0, matching the header pill and the CLI/macOS surfaces.

const attentionSpy = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getAttention: attentionSpy,
    getOnboardingState: ok({ incomplete_tab_count: 0, state: { engaged: true } }),
    hasAPIKey: vi.fn(() => true),
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
  useSecurityScannerStatus: () => ({
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import SidebarNav from '@/components/SidebarNav.vue'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountSidebar() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(SidebarNav, {
    global: { plugins: [createPinia(), router], stubs: { RouterLink: false } },
  })
  await flushPromises()
  return wrapper
}

describe('sidebar Home entry and badge (Spec 109 FR-003/FR-051)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    attentionSpy.mockReset()
  })

  it('renames the Dashboard entry to Home and hides the badge at 0', async () => {
    attentionSpy.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const wrapper = await mountSidebar()

    expect(wrapper.text()).toContain('Home')
    expect(wrapper.text()).not.toContain('Dashboard')
    expect(wrapper.find('[data-test="sidebar-home-badge"]').exists()).toBe(false)
  })

  it('shows the FR-001 attention count as a badge', async () => {
    const items = Array.from({ length: 4 }, (_, i) => ({
      id: `sign_in_required:server:s${i}`,
      kind: 'sign_in_required',
      rank: 10,
      subject: { type: 'server', id: `s${i}`, name: `s${i}` },
      summary: `s${i}: sign in required`,
      fix: { verb: 'login', label: 'Sign in', target: `/servers/s${i}` },
      since: '2026-09-25T06:00:00Z',
    }))
    attentionSpy.mockResolvedValue({ success: true, data: { count: 4, items } })
    const wrapper = await mountSidebar()

    const badge = wrapper.find('[data-test="sidebar-home-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('4')
  })

  // Review finding: T057's coverage never exercised the SSE
  // `attention.changed` live-update path. This drives the actual production
  // path — the shared attention store's own window listener — so a
  // regression that stops the badge refetching on a live change would fail
  // here instead of staying green.
  it('updates the badge when mcpproxy:attention-changed fires', async () => {
    attentionSpy.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const wrapper = await mountSidebar()
    expect(wrapper.find('[data-test="sidebar-home-badge"]').exists()).toBe(false)

    attentionSpy.mockResolvedValue({
      success: true,
      data: {
        count: 2,
        items: [
          {
            id: 'sign_in_required:server:s0',
            kind: 'sign_in_required',
            rank: 10,
            subject: { type: 'server', id: 's0', name: 's0' },
            summary: 's0: sign in required',
            fix: { verb: 'login', label: 'Sign in', target: '/servers/s0' },
            since: '2026-09-25T06:00:00Z',
          },
          {
            id: 'sign_in_required:server:s1',
            kind: 'sign_in_required',
            rank: 10,
            subject: { type: 'server', id: 's1', name: 's1' },
            summary: 's1: sign in required',
            fix: { verb: 'login', label: 'Sign in', target: '/servers/s1' },
            since: '2026-09-25T06:00:00Z',
          },
        ],
      },
    })
    window.dispatchEvent(new CustomEvent('mcpproxy:attention-changed'))
    await flushPromises()

    const badge = wrapper.find('[data-test="sidebar-home-badge"]')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toBe('2')
  })
})
