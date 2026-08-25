import { describe, it, expect, beforeEach, vi } from 'vitest'
import { ref } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// UX audit F3: the Servers page is where a new user lands after closing the
// setup wizard, and with zero servers it used to render "No servers found /
// No servers available" — a restatement with no way forward — above four
// zero-valued stat tiles and four zero-count filter pills.
//
// These tests pin the two halves of the fix:
//   1. Zero servers => a real empty state (add / registry / import), and none
//      of the chrome that describes a list which does not exist.
//   2. Servers exist but none match the filter or search => the *other* empty
//      state. "You have nothing yet" and "nothing matched" are different
//      problems and must not collapse into one message.

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
import { useServersStore } from '@/stores/servers'
import { useOnboardingStore } from '@/stores/onboarding'
import type { Server } from '@/types'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/servers', name: 'servers', component: Servers },
      { path: '/repositories', name: 'repositories', component: { template: '<div />' } },
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
          template: '<div class="add-server-modal" :data-show="String(show)" />',
        },
        CollapsibleHintsPanel: true,
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

function server(name: string, overrides: Partial<Server> = {}): Server {
  return {
    name,
    enabled: true,
    connected: true,
    quarantined: false,
    tool_count: 1,
    ...overrides,
  } as Server
}

describe('Servers first-run empty state (F3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('offers add-server, registry and import actions when no servers are configured', async () => {
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = []
    store.loaded = true
    await flushPromises()

    const empty = wrapper.find('[data-test="servers-first-run-empty"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('No servers yet')
    expect(wrapper.find('[data-test="servers-empty-add"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-empty-registry"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-empty-import"]').exists()).toBe(true)
    // The generic "nothing matched" state must not also be on screen.
    expect(wrapper.find('[data-test="servers-filter-empty"]').exists()).toBe(false)
  })

  it('hides the stat tiles, filter pills and search box while there are no servers', async () => {
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = []
    store.loaded = true
    await flushPromises()

    expect(wrapper.find('[data-test="kpi-card-total"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="kpi-card-connected"]').exists()).toBe(false)
    expect(wrapper.find('input[placeholder="Search servers..."]').exists()).toBe(false)
  })

  it('does not claim "no servers" before a list has actually arrived', async () => {
    const { wrapper } = await mountServers()
    const store = useServersStore()
    // `servers` is empty at rest too — only `loaded` separates "none
    // configured" from "we have not been told yet".
    store.servers = []
    store.loaded = false
    store.loading = { loading: false, error: null }
    await flushPromises()

    expect(wrapper.find('[data-test="servers-first-run-empty"]').exists()).toBe(false)
  })

  it('does not claim "nothing matched" either, before a list has arrived', async () => {
    // This view does not fetch on mount, so `loading` can be false while the
    // list is still unknown. Neither empty state is true then; showing one
    // would tell the user something false about their own setup.
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = []
    store.loaded = false
    store.loading = { loading: false, error: null }
    await flushPromises()

    expect(wrapper.find('[data-test="servers-filter-empty"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="servers-pending"]').exists()).toBe(true)
  })

  it('renders servers it already holds even if the load flag was never set', async () => {
    // Servers in hand are a list by definition — the pending state must never
    // stand between the user and a grid we can already draw.
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = [server('alpha'), server('beta')]
    store.loaded = false
    await flushPromises()

    expect(wrapper.find('[data-test="servers-pending"]').exists()).toBe(false)
    expect(wrapper.findAll('.server-card')).toHaveLength(2)
  })

  it('opening the add-server modal shows it', async () => {
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = []
    store.loaded = true
    await flushPromises()

    expect(wrapper.find('.add-server-modal').attributes('data-show')).toBe('false')
    await wrapper.find('[data-test="servers-empty-add"]').trigger('click')
    expect(wrapper.find('.add-server-modal').attributes('data-show')).toBe('true')
  })

  it('the import link routes to the dashboard with the wizard queued on the servers step', async () => {
    const { wrapper, router } = await mountServers()
    const store = useServersStore()
    store.servers = []
    store.loaded = true
    await flushPromises()

    await wrapper.find('[data-test="servers-empty-import"]').trigger('click')
    await flushPromises()

    const onboarding = useOnboardingStore()
    expect(onboarding.wizardOpen).toBe(true)
    expect(onboarding.wizardInitialTab).toBe('servers')
    // The wizard is mounted by Dashboard.vue, so the Servers page has to hand
    // over to it rather than try to render it in place.
    expect(router.currentRoute.value.path).toBe('/')
  })

  it('falls back to the filter/search empty state once servers exist', async () => {
    const { wrapper } = await mountServers()
    const store = useServersStore()
    store.servers = [server('alpha')]
    store.loaded = true
    await flushPromises()

    await wrapper.find('input[placeholder="Search servers..."]').setValue('nothing-matches-this')
    await flushPromises()

    expect(wrapper.find('[data-test="servers-filter-empty"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-first-run-empty"]').exists()).toBe(false)
    // Chrome stays: the user has servers, they are just filtered out.
    expect(wrapper.find('[data-test="kpi-card-total"]').exists()).toBe(true)
  })
})
