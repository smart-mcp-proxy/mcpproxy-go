import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import { useOnboardingStore } from '@/stores/onboarding'
import api from '@/services/api'

// UX audit F19: the wizard's Servers step was entirely "pick which servers from
// your existing AI clients to import".
//
//   - A user with no prior MCP config saw an empty list and no alternative —
//     a dead end on the step that decides whether the product gets used.
//   - The footer offered two similar primaries ("Import as active" /
//     "Import & quarantine") with the safer one rendered as the weaker.
//   - The security choice (Docker isolation, quarantine) sat behind a
//     collapsed disclosure, and was absent entirely when nothing was
//     importable.
//   - There was no Back.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    markOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    importServersFromPath: vi.fn(),
  },
}))

function onboardingState() {
  return {
    success: true,
    data: {
      has_connected_client: true,
      has_configured_server: false,
      connected_client_count: 1,
      connected_client_ids: ['cursor'],
      configured_server_count: 0,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
    },
  }
}

const CURSOR_PATH = '/Users/test/.cursor/mcp.json'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/repositories', name: 'repositories', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

/**
 * Mount the wizard and land on the Servers tab.
 *
 * `importable` drives the only axis these tests care about: whether any client
 * config on this machine has servers to import.
 */
async function openServersTab(importable: string[]) {
  const paths = importable.length > 0
    ? [{ name: 'Cursor', format: 'json', path: CURSOR_PATH, exists: true }]
    : []
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths } })
  ;(api.importServersFromPath as any).mockResolvedValue({
    success: true,
    data: { imported: importable.map(name => ({ name })) },
  })

  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const wrapper = mount(OnboardingWizard, {
    props: { show: false },
    global: {
      plugins: [router],
      stubs: {
        RouterLink: { template: '<a><slot /></a>' },
        AddServerModal: {
          name: 'AddServerModal',
          props: ['show'],
          template: '<div class="add-server-modal" :data-show="String(show)" />',
        },
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.find('[data-test="tab-servers"]').trigger('click')
  await flushPromises()
  return { wrapper, router }
}

describe('OnboardingWizard servers step (F19)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
  })

  describe('with nothing to import', () => {
    it('offers a registry branch and a manual-add branch instead of a dead end', async () => {
      const { wrapper } = await openServersTab([])

      const panel = wrapper.find('[data-test="servers-nothing-to-import"]')
      expect(panel.exists()).toBe(true)
      expect(panel.text()).toContain('Nothing to import')
      expect(wrapper.find('[data-test="nothing-to-import-registry"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="nothing-to-import-manual"]').exists()).toBe(true)
    })

    it('the manual branch opens the add-server form', async () => {
      const { wrapper } = await openServersTab([])

      expect(wrapper.find('.add-server-modal').attributes('data-show')).toBe('false')
      await wrapper.find('[data-test="nothing-to-import-manual"]').trigger('click')
      expect(wrapper.find('.add-server-modal').attributes('data-show')).toBe('true')
    })

    it('the registry branch closes the wizard before navigating away', async () => {
      const { wrapper, router } = await openServersTab([])

      await wrapper.find('[data-test="nothing-to-import-registry"]').trigger('click')
      await flushPromises()

      // A modal left open would hang over the registry page.
      expect(wrapper.emitted('close')).toBeTruthy()
      expect(router.currentRoute.value.path).toBe('/repositories')
    })

    it('emits close BEFORE the route changes, not after', async () => {
      // `dismiss()` awaits its mark-skipped calls before emitting. If the route
      // moved first it would unmount the Dashboard that owns the open flag, and
      // the late emit could find nothing left to clear it — the wizard would
      // spring back open on return. Sample the emit state from inside the
      // navigation itself, which is the only way to see the real ordering.
      const { wrapper, router } = await openServersTab([])

      let closeAlreadyEmitted: boolean | null = null
      router.beforeEach(() => {
        closeAlreadyEmitted = Boolean(wrapper.emitted('close'))
        return true
      })

      await wrapper.find('[data-test="nothing-to-import-registry"]').trigger('click')
      await flushPromises()

      expect(closeAlreadyEmitted, 'navigation started before close was emitted').toBe(true)
      expect(router.currentRoute.value.path).toBe('/repositories')
    })

    it('still exposes the security choice', async () => {
      const { wrapper } = await openServersTab([])

      const security = wrapper.find('[data-test="security-panel"]')
      expect(security.exists()).toBe(true)
      expect(security.attributes('open')).toBeDefined()
      expect(wrapper.find('[data-test="toggle-quarantine"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="toggle-docker-isolation"]').exists()).toBe(true)
    })

    it('offers Back', async () => {
      const { wrapper } = await openServersTab([])
      expect(wrapper.find('[data-test="wizard-back"]').exists()).toBe(true)
    })

    it('does not repeat manual-add as a second collapsed disclosure', async () => {
      const { wrapper } = await openServersTab([])
      expect(wrapper.find('[data-test="manual-add-details"]').exists()).toBe(false)
    })
  })

  describe('with servers to import', () => {
    it('expands the security choice by default rather than hiding it', async () => {
      const { wrapper } = await openServersTab(['memory', 'everything'])

      const security = wrapper.find('[data-test="security-panel"]')
      expect(security.exists()).toBe(true)
      // A `<details>` the user has to think to click is not a surfaced choice.
      expect(security.attributes('open')).toBeDefined()
    })

    it('makes the reviewed import the single primary action', async () => {
      const { wrapper } = await openServersTab(['memory'])

      const quarantine = wrapper.find('[data-test="bulk-import-quarantine"]')
      const active = wrapper.find('[data-test="bulk-import-active"]')
      expect(quarantine.exists()).toBe(true)
      expect(active.exists()).toBe(true)

      // Exactly one primary, and it is the one that holds servers for review.
      expect(quarantine.classes()).toContain('btn-primary')
      expect(active.classes()).not.toContain('btn-primary')
      expect(active.classes()).not.toContain('btn-secondary')
      expect(active.classes()).toContain('btn-link')
      // …and the unreviewed path says what it skips.
      expect(active.text()).toContain('Import without review')
    })

    it('caps the import list so the last row is not clipped without a scrollbar', async () => {
      const { wrapper } = await openServersTab(['memory'])

      const list = wrapper.find('[data-test="import-section-json"]').element.parentElement!
      expect(list.className).toContain('overflow-y-auto')
      expect(list.className).toMatch(/max-h-/)
    })

    it('offers Back', async () => {
      const { wrapper } = await openServersTab(['memory'])
      expect(wrapper.find('[data-test="wizard-back"]').exists()).toBe(true)
    })

    it('keeps a Close, so the step is never a trap', async () => {
      // A step whose only exits are "import" strands the user, and the
      // committed e2e sweep dismisses an auto-opened wizard through exactly
      // this control — on a fresh instance the wizard can land here.
      const { wrapper } = await openServersTab(['memory'])
      const close = wrapper.find('[data-test="close-wizard"]')
      expect(close.exists()).toBe(true)
      expect(close.attributes('disabled')).toBeUndefined()
    })

    it('Back returns to the previous step', async () => {
      const { wrapper } = await openServersTab(['memory'])

      await wrapper.find('[data-test="wizard-back"]').trigger('click')
      await flushPromises()

      expect(wrapper.find('[data-test="panel-clients"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(false)
    })
  })

  it('opens on the step the caller asked for, once', async () => {
    setActivePinia(createPinia())
    const store = useOnboardingStore()
    store.openWizard('servers')

    const { wrapper } = await openServersTab([])
    // The request is one-shot: a later plain open must not be redirected.
    expect(store.wizardInitialTab).toBe(null)
    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(true)
  })
})

// The Servers page's import link flips the store flag and THEN routes to the
// Dashboard — which is the component that mounts the wizard. So the wizard can
// come into existence with `show` already true, and never see a change to
// watch. It must still initialise, and still honour the requested step.
describe('OnboardingWizard mounted already-open', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
  })

  async function mountAlreadyOpen() {
    const router = makeRouter()
    router.push('/')
    await router.isReady()
    const wrapper = mount(OnboardingWizard, {
      // Already open at mount — no false→true transition ever happens.
      props: { show: true },
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  it('lands on the requested step and consumes the request', async () => {
    const store = useOnboardingStore()
    store.openWizard('servers')

    const wrapper = await mountAlreadyOpen()

    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-clients"]').exists()).toBe(false)
    // Not left behind to hijack the next plain open.
    expect(store.wizardInitialTab).toBe(null)
  })

  it('an open that is still loading when the wizard closes does not stamp its tab', async () => {
    // Nothing cancels the open sequence's fetches. A close (or a close then a
    // reopen) mid-flight must not let the old run choose the tab or restart
    // polling behind the newer state.
    let releaseState: (v: unknown) => void = () => {}
    ;(api.getOnboardingState as any).mockReturnValue(
      new Promise((resolve) => { releaseState = resolve })
    )

    const router = makeRouter()
    router.push('/')
    await router.isReady()
    const wrapper = mount(OnboardingWizard, {
      props: { show: true },
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
        },
      },
    })
    await flushPromises()

    // Close while the first fetch is still outstanding.
    await wrapper.setProps({ show: false })
    await flushPromises()

    // Now let the stale run finish. Its predicates would pick 'servers'
    // (has_connected_client true, has_configured_server false).
    releaseState({ success: true, data: onboardingState().data })
    await flushPromises()

    // The dialog keeps its content in the DOM when closed (only the `open`
    // attribute goes), so the invariant to check is which tab it is on: the
    // abandoned run must not have moved it off the default.
    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="panel-clients"]').exists()).toBe(true)
  })

  it('still initialises (fetches its state) with no request pending', async () => {
    const store = useOnboardingStore()
    store.openWizard()

    await mountAlreadyOpen()

    expect(api.getOnboardingState).toHaveBeenCalled()
    expect(api.getConnectStatus).toHaveBeenCalled()
    expect(store.wizardInitialTab).toBe(null)
  })
})
