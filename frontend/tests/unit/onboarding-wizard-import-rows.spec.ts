import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109-b FR-040: import candidate rows show a second line (command+args
// or url+auth-type) and tags (local process|remote|needs secret|oauth).

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
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
      has_usable_server: false,
      usable_servers: [],
    },
  }
}

const CURSOR_PATH = '/Users/test/.cursor/mcp.json'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

async function openServersTabWithRows(imported: any[]) {
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({
    success: true,
    data: { paths: [{ name: 'Cursor', format: 'json', path: CURSOR_PATH, exists: true }] },
  })
  ;(api.importServersFromPath as any).mockResolvedValue({ success: true, data: { imported } })

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
  await wrapper.find('[data-test="tab-servers"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('OnboardingWizard import rows (Spec 109-b FR-040)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
  })

  it('shows the summary second line and the local process tag for a stdio server', async () => {
    const wrapper = await openServersTabWithRows([
      {
        name: 'filesystem',
        protocol: 'stdio',
        command: 'npx',
        source_format: 'claude_desktop',
        original_name: 'filesystem',
        summary: 'npx -y @modelcontextprotocol/server-filesystem /tmp',
        tags: ['local process'],
      },
    ])

    const summary = wrapper.find('[data-test="import-summary-json-filesystem"]')
    expect(summary.exists()).toBe(true)
    expect(summary.text()).toBe('npx -y @modelcontextprotocol/server-filesystem /tmp')
    expect(wrapper.find('[data-test="import-tag-json-filesystem-local-process"]').exists()).toBe(true)
  })

  it('shows the needs-secret tag with a distinct style', async () => {
    const wrapper = await openServersTabWithRows([
      {
        name: 'github',
        protocol: 'stdio',
        command: 'uvx',
        source_format: 'claude_desktop',
        original_name: 'github',
        summary: 'uvx mcp-server-github',
        tags: ['local process', 'needs secret'],
        env: [{ name: 'GITHUB_TOKEN', value_present: false, secret_like: true, empty_or_placeholder: true }],
      },
    ])

    const tag = wrapper.find('[data-test="import-tag-json-github-needs-secret"]')
    expect(tag.exists()).toBe(true)
    expect(tag.classes()).toContain('badge-warning')
  })

  it('shows the remote and oauth tags for an HTTP server', async () => {
    const wrapper = await openServersTabWithRows([
      {
        name: 'linear',
        protocol: 'http',
        url: 'https://api.linear.app/mcp',
        source_format: 'claude_desktop',
        original_name: 'linear',
        summary: 'https://api.linear.app/mcp (oauth)',
        tags: ['remote', 'oauth'],
      },
    ])

    expect(wrapper.find('[data-test="import-tag-json-linear-remote"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="import-tag-json-linear-oauth"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="import-summary-json-linear"]').text()).toBe('https://api.linear.app/mcp (oauth)')
  })
})
