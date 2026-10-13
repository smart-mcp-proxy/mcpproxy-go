import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

// Spec 109-i FR-052: "+ Add -> Token" lands on /clients?tab=tokens&create=1.
// The Agent tokens tab opens its create dialog for that flag, then strips
// `create` so a reload or Back does not reopen it.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    listAgentTokens: vi.fn().mockResolvedValue({ success: true, data: { tokens: [] } }),
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

import AgentTokens from '@/views/AgentTokens.vue'

const stub = { template: '<div />' }

async function mountAt(url: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/clients', component: stub }],
  })
  await router.push(url)
  await router.isReady()
  const wrapper = mount(AgentTokens, { global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, router }
}

describe('AgentTokens ?create=1 (Spec 109-i FR-052)', () => {
  let showModal: ReturnType<typeof vi.fn>
  const original = (HTMLDialogElement.prototype as any).showModal

  beforeEach(() => {
    setActivePinia(createPinia())
    showModal = vi.fn()
    ;(HTMLDialogElement.prototype as any).showModal = showModal
  })

  afterEach(() => {
    ;(HTMLDialogElement.prototype as any).showModal = original
  })

  it('opens the create dialog and strips create, keeping tab and other params', async () => {
    const { router } = await mountAt('/clients?tab=tokens&create=1&token=t')
    expect(showModal).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.path).toBe('/clients')
    expect(router.currentRoute.value.query).toEqual({ tab: 'tokens', token: 't' })
  })

  it('does not open the dialog without the flag', async () => {
    await mountAt('/clients?tab=tokens')
    expect(showModal).not.toHaveBeenCalled()
  })

  it('opens again when create=1 arrives while the tab is already mounted', async () => {
    const { router } = await mountAt('/clients?tab=tokens')
    await router.push('/clients?tab=tokens&create=1')
    await flushPromises()
    expect(showModal).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.query).toEqual({ tab: 'tokens' })
  })
})
