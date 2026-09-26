import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// zcode review finding (Spec 109 self-import PR): the post-import summary
// hardcoded "· N skipped (already configured)" for EVERY skip reason. That is
// factually wrong for the new self_reference reason (internal/configimport:
// SkipReasonSelfReference) — a self-referencing entry was never "already
// configured", it was rejected to avoid mcpproxy proxying itself. The message
// must reflect the actual reason the backend returned, per skippedByLabel in
// OnboardingWizard.vue's onBulkImport.

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

async function openServersTabWithApplyResult(applyData: Record<string, unknown>) {
  ;(api.getCanonicalConfigPaths as any).mockResolvedValue({
    success: true,
    data: { paths: [{ name: 'Cursor', format: 'json', path: CURSOR_PATH, exists: true }] },
  })
  ;(api.importServersFromPath as any).mockImplementation((params: { preview?: boolean }) => {
    if (params.preview) {
      return Promise.resolve({ success: true, data: { imported: [{ name: 'memory' }] } })
    }
    return Promise.resolve({ success: true, data: applyData })
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
        AddServerModal: { name: 'AddServerModal', props: ['show'], template: '<div />' },
      },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.find('[data-test="tab-servers"]').trigger('click')
  await flushPromises()
  return wrapper
}

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

describe('OnboardingWizard — import skip-reason labeling', () => {
  it('labels a self_reference skip for what it is, not as "already configured"', async () => {
    const wrapper = await openServersTabWithApplyResult({
      summary: { imported: 0, skipped: 1, failed: 0 },
      imported: [],
      skipped: [{ name: 'proxy-self', reason: 'self_reference' }],
      failed: [],
    })

    await wrapper.find('[data-test="select-all-json"]').setValue(true)
    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    const message = wrapper.text()
    expect(message).toContain('connects to mcpproxy itself')
    expect(message).not.toContain('already configured')
  })

  it('still labels an ordinary duplicate skip as "already configured"', async () => {
    const wrapper = await openServersTabWithApplyResult({
      summary: { imported: 0, skipped: 1, failed: 0 },
      imported: [],
      skipped: [{ name: 'memory', reason: 'already_exists' }],
      failed: [],
    })

    await wrapper.find('[data-test="select-all-json"]').setValue(true)
    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('already configured')
  })

  it('labels mixed skip reasons separately rather than folding them into one count', async () => {
    const wrapper = await openServersTabWithApplyResult({
      summary: { imported: 0, skipped: 2, failed: 0 },
      imported: [],
      skipped: [
        { name: 'memory', reason: 'already_exists' },
        { name: 'proxy-self', reason: 'self_reference' },
      ],
      failed: [],
    })

    await wrapper.find('[data-test="select-all-json"]').setValue(true)
    await wrapper.find('[data-test="bulk-import-primary"]').trigger('click')
    await flushPromises()

    const message = wrapper.text()
    expect(message).toContain('1 skipped (already configured)')
    expect(message).toContain('1 skipped (connects to mcpproxy itself)')
  })
})
