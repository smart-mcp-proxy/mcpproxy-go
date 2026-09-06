import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// UX audit F07, consumer half. AddServerModal now names the server a single
// add created; the NAVIGATION lives in the consumers, never in handleSubmit —
// OnboardingWizard also listens to `added` and a push from inside the modal
// would unmount the wizard (Dashboard.vue mounts it) mid-flow, possibly before
// markServerCompleted() lands.
//
// Servers.vue stands in for the three navigating consumers (Dashboard.vue,
// Servers.vue, TopHeader.vue) — all three have the identical
// `if (serverName) router.push(serverDetailPath(serverName))` shape. What is
// asserted here is the contract that makes the emit worth anything: a named
// add lands on the server-detail page (where Approve / the quarantine banner
// already live), and a payload-free bulk import does NOT navigate.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getServers: ok({ servers: [] }),
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
    hasEnabledScanners: () => false,
    totalFindings: ref(0),
    totalScans: ref(0),
    loaded: ref(true),
  }),
}))

import Servers from '@/views/Servers.vue'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/servers', name: 'servers', component: Servers },
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div />' }, props: true },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountServers() {
  const router = makeRouter()
  router.push('/servers')
  await router.isReady()
  const wrapper = mount(Servers, {
    global: {
      plugins: [createPinia(), router],
      stubs: {
        ServerCard: { template: '<div class="server-card" />' },
        AddServerModal: {
          name: 'AddServerModal',
          props: ['show'],
          template: '<div class="add-server-modal" />',
        },
        CollapsibleHintsPanel: true,
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('post-add hand-off in the @added consumers (F07)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('navigates to the new server detail view after a single add', async () => {
    const { wrapper, router } = await mountServers()

    wrapper.findComponent({ name: 'AddServerModal' }).vm.$emit('added', 'fs-server')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('server-detail')
    expect(router.currentRoute.value.params.serverName).toBe('fs-server')
  })

  it('percent-encodes a registry name so a "/" does not fall through to the 404 catch-all', async () => {
    // MCP-1112 (#598): official-registry names look like "io.github.owner/repo".
    const { wrapper, router } = await mountServers()

    wrapper.findComponent({ name: 'AddServerModal' }).vm.$emit('added', 'io.github.owner/repo')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('server-detail')
    expect(router.currentRoute.value.params.serverName).toBe('io.github.owner/repo')
  })

  it('stays on the list for a payload-free bulk import', async () => {
    const { wrapper, router } = await mountServers()

    wrapper.findComponent({ name: 'AddServerModal' }).vm.$emit('added')
    await flushPromises()

    expect(router.currentRoute.value.name).toBe('servers')
  })
})
