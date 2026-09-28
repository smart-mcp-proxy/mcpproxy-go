import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-ux-navigation-consistency US7 Acceptance Scenario 4 — live QA
// regression: "Given both imported servers are quarantined, Then the Servers
// step is not marked complete. It shows the inline review list ... with
// 'Approve a server to finish this step'." Before this fix, the wizard fell
// through to the generic "Nothing to import" dead end (Browse the
// registry / Add a server manually) whenever there were no *new* import
// candidates left, even when the reason there was nothing new was that
// everything already imported was sitting in quarantine — the servers were
// never shown, and nothing told the user to go approve them.

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
    getServers: vi.fn(),
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
      configured_server_count: 2,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
      has_usable_server: false,
      usable_servers: [],
      ...overrides,
    },
  }
}

function quarantinedServer(name: string, extra: Record<string, unknown> = {}) {
  return {
    name,
    protocol: 'stdio',
    command: 'npx',
    enabled: true,
    quarantined: true,
    connected: false,
    tool_count: 0,
    reconnect_count: 0,
    created: '2026-01-01T00:00:00Z',
    updated: '2026-01-01T00:00:00Z',
    status: 'quarantined',
    ...extra,
  }
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/servers/:serverName', name: 'server-detail', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountWizard() {
  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const wrapper = mount(OnboardingWizard, {
    props: { show: true },
    global: {
      plugins: [router],
      stubs: {
        AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('OnboardingWizard Servers step inline review (Spec 109-ux-navigation-consistency US7 AS4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
    // No new import candidates left on this machine — everything was already
    // imported.
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('shows the inline review list with "Approve a server to finish this step" instead of the dead end', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: false, usable_servers: [] })
    )
    ;(api.getServers as any).mockResolvedValue({
      success: true,
      data: {
        servers: [
          quarantinedServer('github', { url: 'https://api.githubcopilot.com/mcp/', protocol: 'http', command: undefined }),
          quarantinedServer('filesystem', { command: 'npx' }),
        ],
      },
    })

    const { wrapper } = await mountWizard()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    // The dead-end empty state must NOT render.
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(false)

    const review = wrapper.find('[data-test="servers-inline-review"]')
    expect(review.exists()).toBe(true)
    expect(review.text()).toContain('Approve a server to finish this step')

    expect(wrapper.find('[data-test="servers-review-row-github"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-review-row-filesystem"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="servers-review-link-github"]').attributes('href')).toBe('/servers/github')

    // Step is still not complete.
    const tab = wrapper.find('[data-test="tab-servers"]')
    expect(tab.text()).not.toContain('✓')
  })

  it('review link closes the wizard before navigating to the server detail page (review round 5)', async () => {
    // The Review link routes away like goToRegistry() does elsewhere in the
    // wizard — dismiss() must run first, or the route change unmounts the
    // Dashboard that owns wizardOpen and the wizard springs back open.
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: false, usable_servers: [] })
    )
    ;(api.getServers as any).mockResolvedValue({
      success: true,
      data: {
        servers: [
          quarantinedServer('github', { url: 'https://api.githubcopilot.com/mcp/', protocol: 'http', command: undefined }),
        ],
      },
    })

    const { wrapper, router } = await mountWizard()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    await wrapper.find('[data-test="servers-review-link-github"]').trigger('click')
    await flushPromises()

    expect(wrapper.emitted('close')).toBeTruthy()
    expect(router.currentRoute.value.path).toBe('/servers/github')
  })

  it('review link ignores the in-app handler on a modifier/middle click, leaving the native href to open a new tab (review round 5)', async () => {
    // router-link deliberately skips interception for cmd/ctrl/shift/alt and
    // non-left-button clicks so the browser's native "open in new tab" still
    // works; the plain <a> replacement must do the same rather than
    // hijacking every click via .prevent.
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: false, usable_servers: [] })
    )
    ;(api.getServers as any).mockResolvedValue({
      success: true,
      data: {
        servers: [
          quarantinedServer('github', { url: 'https://api.githubcopilot.com/mcp/', protocol: 'http', command: undefined }),
        ],
      },
    })

    const { wrapper, router } = await mountWizard()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    const link = wrapper.find('[data-test="servers-review-link-github"]')
    expect(link.attributes('href')).toBe('/servers/github')

    await link.trigger('click', { ctrlKey: true })
    await flushPromises()

    expect(wrapper.emitted('close')).toBeFalsy()
    expect(router.currentRoute.value.path).toBe('/')
  })

  it('still shows the generic empty state when there is nothing imported at all', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: false, usable_servers: [] })
    )
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [] } })

    const { wrapper } = await mountWizard()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="servers-inline-review"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(true)
  })

  it('does not show the review list once a usable server exists', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: true, usable_servers: ['filesystem'] })
    )
    ;(api.getServers as any).mockResolvedValue({
      success: true,
      data: {
        servers: [
          quarantinedServer('github'),
          { ...quarantinedServer('filesystem'), quarantined: false, connected: true, tool_count: 3 },
        ],
      },
    })

    const { wrapper } = await mountWizard()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="servers-inline-review"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(true)
  })
})
