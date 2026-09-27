import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'
import { useSystemStore } from '@/stores/system'

// Review round 3 finding: onBulkImport clears `selection` BEFORE building the
// success toast, and the toast's "(N renamed)" suffix reads `conflictCount`,
// a computed derived from `selection`. Clearing first makes conflictCount
// always 0 by the time the toast fires, so a bulk import of renamed/
// conflicting servers never reports the rename count in the toast — even
// though the pre-import footer correctly showed it.

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
      has_usable_server: false,
      usable_servers: [],
    },
  }
}

const PATH_A = '/Users/test/.cursor/mcp.json'
const PATH_B = '/Users/test/.claude/config.json'

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

describe('OnboardingWizard bulk-import conflict toast (review round 3)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({
      success: true,
      data: { config: { quarantine_enabled: true, docker_isolation: { enabled: true } } },
    })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { docker_available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())

    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({
      success: true,
      data: {
        paths: [
          { name: 'Cursor', format: 'cursor', path: PATH_A, exists: true },
          { name: 'Claude Code', format: 'claude-code', path: PATH_B, exists: true },
        ],
      },
    })
    ;(api.importServersFromPath as any).mockImplementation(async (req: any) => {
      if (req.preview) {
        // Both sources offer a server named "memory" — selecting both is
        // the conflict this toast is supposed to report.
        return { success: true, data: { imported: [{ name: 'memory' }] } }
      }
      return {
        success: true,
        data: { imported: req.server_names.map((name: string) => ({ name })), summary: { imported: 1, skipped: 0, failed: 0 } },
      }
    })
  })

  it('reports the renamed count in the success toast', async () => {
    const router = makeRouter()
    router.push('/')
    await router.isReady()

    const wrapper = mount(OnboardingWizard, {
      props: { show: false },
      global: {
        plugins: [router],
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
        },
      },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    // Select "memory" from BOTH sources — a name conflict.
    await wrapper.find('[data-test="server-checkbox-cursor-memory"]').setValue(true)
    await wrapper.find('[data-test="server-checkbox-claude-code-memory"]').setValue(true)
    await flushPromises()

    // Pre-import footer: both selected (path, "memory") entries are part of
    // the conflict, so conflictCount is 2 here.
    expect(wrapper.text()).toContain('2 renamed')

    const store = useSystemStore()
    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    expect(store.toasts).toHaveLength(1)
    expect(store.toasts[0].message).toContain('renamed')
  })
})
