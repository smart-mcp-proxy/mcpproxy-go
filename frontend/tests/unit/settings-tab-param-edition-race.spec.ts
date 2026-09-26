import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109 PR-a review round 2 (finding 8): App.vue's systemStore.status is
// populated asynchronously (connectEventSource's SSE, in its own onMounted),
// while Settings.vue reads `?tab=` synchronously in its OWN onMounted, gated
// by hasServerEdition (computed from systemStore.status?.edition). On a
// reload of /settings?tab=teams, Settings.vue can mount and run that check
// BEFORE the SSE status frame lands: `hasServerEdition` is still false,
// `tabs.value` excludes `teams`, the membership check silently fails, and the
// param is dropped — the page falls back to the default tab and never
// retries once status actually arrives.

const emptyConfig = vi.hoisted(() => ({ server_edition: { enabled: false } }))

vi.mock('@/services/api', () => ({
  default: {
    getConfig: vi.fn().mockResolvedValue({ success: true, data: { config: emptyConfig } }),
    getStatus: vi.fn().mockResolvedValue({ success: true, data: {} }),
    validateConfig: vi.fn(),
    applyConfig: vi.fn(),
  },
}))

import Settings from '@/views/Settings.vue'
import { useSystemStore } from '@/stores/system'

function makeRouter(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings, meta: { title: 'Settings' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div/>' } },
    ],
  })
  router.push(path)
  return router
}

async function mountSettings(path: string) {
  const router = makeRouter(path)
  await router.isReady()
  const wrapper = mount(Settings, {
    global: {
      plugins: [router],
      stubs: { VueMonacoEditor: true, ConnectModal: true },
    },
  })
  return wrapper
}

describe('Settings ?tab=teams vs. late systemStore.status (review round 2, finding 8)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('lands on the teams tab once status arrives late, instead of dropping the param', async () => {
    // systemStore.status is still unset at mount time — the SSE frame has
    // not arrived yet (the exact race on a fresh /settings?tab=teams load).
    const wrapper = await mountSettings('/settings?tab=teams')
    await flushPromises()

    // Precondition: the race is live — the tab isn't available/selected yet.
    expect(wrapper.find('[data-test="settings-tab-teams"]').exists()).toBe(false)

    // The status frame lands (App.vue's connectEventSource callback).
    const systemStore = useSystemStore()
    // @ts-expect-error partial StatusUpdate is enough for this computed
    systemStore.status = { edition: 'server' }
    await flushPromises()

    const teamsTab = wrapper.find('[data-test="settings-tab-teams"]')
    expect(teamsTab.exists()).toBe(true)
    expect(teamsTab.classes()).toContain('tab-active')
    wrapper.unmount()
  })

  it('does not clobber a tab the user already picked once status resolves', async () => {
    const wrapper = await mountSettings('/settings?tab=teams')
    await flushPromises()

    // The user clicks over to General before the status frame arrives.
    await wrapper.find('[data-test="settings-tab-general"]').trigger('click')
    expect(wrapper.find('[data-test="settings-tab-general"]').classes()).toContain('tab-active')

    const systemStore = useSystemStore()
    // @ts-expect-error partial StatusUpdate is enough for this computed
    systemStore.status = { edition: 'server' }
    await flushPromises()

    // The late-arriving status must not retroactively yank the user back to
    // the tab from the URL.
    expect(wrapper.find('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
    wrapper.unmount()
  })

  it('is a no-op when the resolved edition really is personal (no phantom teams tab)', async () => {
    const wrapper = await mountSettings('/settings?tab=teams')
    await flushPromises()

    const systemStore = useSystemStore()
    // @ts-expect-error partial StatusUpdate is enough for this computed
    systemStore.status = { edition: 'personal' }
    await flushPromises()

    expect(wrapper.find('[data-test="settings-tab-teams"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="settings-tab-security"]').classes()).toContain('tab-active')
    wrapper.unmount()
  })
})
