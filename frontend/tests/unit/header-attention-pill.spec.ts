import { describe, it, expect, beforeEach, vi } from 'vitest'
import { shallowMount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 FR-003/FR-051: the header's needs-attention pill — hidden at 0,
// otherwise showing the count with a popover listing the first 5 items and a
// "See all" link to Home, where the full list lives.

const attentionSpy = vi.hoisted(() => vi.fn())

function item(id: string) {
  return {
    id: `sign_in_required:server:${id}`,
    kind: 'sign_in_required',
    rank: 10,
    subject: { type: 'server', id, name: id },
    summary: `${id}: sign in required`,
    fix: { verb: 'login', label: 'Sign in', target: `/servers/${id}` },
    since: '2026-09-25T06:00:00Z',
  }
}

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getAttention: attentionSpy,
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

import TopHeader from '@/components/TopHeader.vue'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', name: 'home', component: { template: '<div />' } }],
  })
}

async function mountHeader() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()
  const wrapper = shallowMount(TopHeader, {
    global: {
      plugins: [createPinia(), router],
      stubs: { RouterLink: false },
    },
  })
  await flushPromises()
  return wrapper
}

describe('header needs-attention pill (Spec 109 FR-003/FR-051)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    attentionSpy.mockReset()
  })

  it('is hidden when the list is empty', async () => {
    attentionSpy.mockResolvedValue({ success: true, data: { count: 0, items: [] } })
    const wrapper = await mountHeader()
    expect(wrapper.find('[data-test="header-attention-pill"]').exists()).toBe(false)
  })

  it('shows the count and, on click, a popover with at most the first 5 items plus "See all"', async () => {
    const items = Array.from({ length: 7 }, (_, i) => item(`server-${i}`))
    attentionSpy.mockResolvedValue({ success: true, data: { count: 7, items } })
    const wrapper = await mountHeader()

    const pill = wrapper.find('[data-test="header-attention-pill"]')
    expect(pill.exists()).toBe(true)
    expect(pill.text()).toContain('7')

    expect(wrapper.find('[data-test="header-attention-popover"]').exists()).toBe(false)
    await wrapper.find('[data-test="header-attention-pill-button"]').trigger('click')

    const popover = wrapper.find('[data-test="header-attention-popover"]')
    expect(popover.exists()).toBe(true)
    const rows = wrapper.findAll('[data-test^="header-attention-item-"]')
    expect(rows).toHaveLength(5)
    expect(wrapper.find('[data-test="header-attention-see-all"]').exists()).toBe(true)
  })
})
