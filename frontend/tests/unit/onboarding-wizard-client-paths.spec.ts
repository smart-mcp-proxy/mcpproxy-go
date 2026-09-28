import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-b FR-037: client rows show the home-shortened display_path (with
// the full path in a tooltip) and a per-client icon.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
  },
}))

function onboardingState() {
  return {
    success: true,
    data: {
      has_connected_client: false,
      has_configured_server: false,
      connected_client_count: 0,
      connected_client_ids: [],
      configured_server_count: 0,
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 3,
      has_usable_server: false,
      usable_servers: [],
    },
  }
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function mountOnClients(clients: any[]) {
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
  return wrapper
}

describe('OnboardingWizard Clients step paths/icons (Spec 109-b FR-037)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
  })

  it('shows the shortened display_path with the full path as a tooltip', async () => {
    const wrapper = await mountOnClients([
      {
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        display_path: '~/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
        reload_hint: 'Reload the Cursor window (or restart Cursor) to load MCPProxy',
      },
    ])

    const pathEl = wrapper.find('[data-test="client-path-cursor"]')
    expect(pathEl.exists()).toBe(true)
    expect(pathEl.text()).toBe('~/.cursor/mcp.json')
    expect(pathEl.attributes('title')).toBe('/Users/test/.cursor/mcp.json')
  })

  it('falls back to the full config_path when display_path is absent', async () => {
    const wrapper = await mountOnClients([
      {
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
      },
    ])

    const pathEl = wrapper.find('[data-test="client-path-cursor"]')
    expect(pathEl.text()).toBe('/Users/test/.cursor/mcp.json')
  })

  it('renders a per-client icon', async () => {
    const wrapper = await mountOnClients([
      {
        id: 'cursor',
        name: 'Cursor',
        config_path: '/Users/test/.cursor/mcp.json',
        display_path: '~/.cursor/mcp.json',
        exists: true,
        connected: false,
        supported: true,
        icon: 'cursor',
      },
    ])

    const icon = wrapper.find('[data-test="client-icon-cursor"]')
    expect(icon.exists()).toBe(true)
    expect(icon.text().length).toBeGreaterThan(0)
  })
})
