import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-b FR-042: the Verify step shows each connected client's reload
// hint, and generates suggested prompts only from tools of usable servers —
// "Approve a server first" when there are none.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    markOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getStatus: vi.fn(),
  },
}))

function onboardingState(overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      has_connected_client: true,
      has_configured_server: true,
      connected_client_count: 1,
      connected_client_ids: ['cursor'],
      configured_server_count: 1,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: true,
      mcp_clients_seen_ever: ['cursor'],
      incomplete_tab_count: 0,
      has_usable_server: false,
      usable_servers: [],
      ...overrides,
    },
  }
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/servers', name: 'servers', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountOnVerify(clients: any[] = []) {
  ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: clients })
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
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
  await wrapper.find('[data-test="tab-verify"]').trigger('click')
  await flushPromises()
  return { wrapper, router }
}

describe('OnboardingWizard Verify step hints (Spec 109-b FR-042)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getStatus as any).mockResolvedValue({ success: true, data: {} })
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
  })

  it('shows "Approve a server first" with no usable server', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState({ usable_servers: [] }))
    const { wrapper } = await mountOnVerify()

    expect(wrapper.find('[data-test="verify-no-usable-server"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="verify-no-usable-server"]').text()).toContain('Approve a server first')
    expect(wrapper.find('[data-test="verify-sample-prompts"]').exists()).toBe(false)
  })

  it('the "review it on the Servers page" link closes the wizard before navigating away (review round 5)', async () => {
    // Same unmount-before-clear race as goToRegistry(): dismiss() must run
    // before the route changes, or the wizard springs back open on return.
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState({ usable_servers: [] }))
    const { wrapper, router } = await mountOnVerify()

    const link = wrapper.find('[data-test="verify-no-usable-server"] a')
    expect(link.exists()).toBe(true)

    await link.trigger('click')
    await flushPromises()

    expect(wrapper.emitted('close')).toBeTruthy()
    expect(router.currentRoute.value.path).toBe('/servers')
  })

  it('generates prompts referencing a usable server once one exists', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: true, usable_servers: ['github'] })
    )
    const { wrapper } = await mountOnVerify()

    expect(wrapper.find('[data-test="verify-no-usable-server"]').exists()).toBe(false)
    const prompts = wrapper.find('[data-test="verify-sample-prompts"]')
    expect(prompts.exists()).toBe(true)
    expect(prompts.text()).toContain('github')
  })

  it('shows the reload hint for each connected client', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: true, usable_servers: ['github'] })
    )
    const { wrapper } = await mountOnVerify([
      {
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        display_path: '~/.cursor/mcp.json',
        exists: true,
        connected: true,
        supported: true,
        icon: 'cursor',
        reload_hint: 'Reload the Cursor window (or restart Cursor) to load MCPProxy',
      },
    ])

    const hints = wrapper.find('[data-test="verify-reload-hints"]')
    expect(hints.exists()).toBe(true)
    expect(hints.text()).toContain('Reload the Cursor window')
  })

  it('does not show a reload-hints section when no client is connected', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: true, usable_servers: ['github'], connected_client_ids: [] })
    )
    const { wrapper } = await mountOnVerify([
      {
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        exists: false,
        connected: false,
        supported: true,
        icon: 'cursor',
        reload_hint: 'Reload the Cursor window (or restart Cursor) to load MCPProxy',
      },
    ])

    expect(wrapper.find('[data-test="verify-reload-hints"]').exists()).toBe(false)
  })
})
