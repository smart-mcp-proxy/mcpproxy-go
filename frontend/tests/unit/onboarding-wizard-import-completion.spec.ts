import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109 US7-4 / FR-043 (fix-usertest-web T200, audit F-04). After an import
// the Servers step must show one completion state: the review sentence reads
// as a sentence ("step. 6 servers ...", never "step.6"), counts quarantined
// servers honestly, and an import that leaves a usable server never falls
// through to the false "Nothing to import" card.

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
      configured_server_count: 6,
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

function server(name: string, extra: Record<string, unknown> = {}) {
  return {
    name,
    protocol: 'stdio',
    command: 'node',
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

const sixQuarantined = ['memory', 'fixture', 'filesystem', 'everything', 'fetchy', 'thinker'].map(name => server(name))

const ImportServersStub = {
  emits: ['imported'],
  props: { detected: Boolean, showMessage: { type: Boolean, default: true } },
  template:
    '<div><button data-test="stub-import" @click="$emit(\'imported\', 2)">import</button><p v-if="detected && showMessage" data-test="stub-own-message">imported</p></div>',
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/review/:serverName', name: 'review-server', component: { template: '<div />' } },
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
    global: { plugins: [router], stubs: { ImportServers: ImportServersStub } },
  })
  await flushPromises()
  await wrapper.find('[data-test="tab-servers"]').trigger('click')
  await flushPromises()
  return wrapper
}

async function importTwo(wrapper: Awaited<ReturnType<typeof mountWizard>>) {
  await wrapper.find('[data-test="stub-import"]').trigger('click')
  await flushPromises()
}

describe('OnboardingWizard Servers step completion after an import (fix-usertest-web T200)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.markOnboardingState as any).mockResolvedValue(onboardingState())
    // Everything on this machine is already imported: no importable sources.
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
  })

  it('(a) reads as a sentence, counts all quarantined servers, and never shows an empty/nothing card', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState({ has_usable_server: false }))
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: sixQuarantined } })
    const wrapper = await mountWizard()
    await importTwo(wrapper)

    const panel = wrapper.find('[data-test="servers-inline-review"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain(
      'Approve a server to finish this step. 6 servers are waiting in quarantine, including the 2 you just imported — review their tools before they can run.',
    )
    expect(panel.text()).not.toMatch(/step\.\d/)
    const body = wrapper.find('[data-test="panel-servers"]').text()
    expect(body).not.toContain('No importable servers found')
    expect(body).not.toContain('Nothing to import')
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(false)
  })

  it('(b) shows "N servers imported." with Continue to Verify when a usable server exists and nothing is left', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(
      onboardingState({ has_usable_server: true, usable_servers: ['fetchy', 'thinker'] }),
    )
    ;(api.getServers as any).mockResolvedValue({
      success: true,
      data: {
        servers: [
          server('fetchy', { quarantined: false, connected: true, tool_count: 1, status: 'connected' }),
          server('thinker', { quarantined: false, connected: true, tool_count: 1, status: 'connected' }),
        ],
      },
    })
    const wrapper = await mountWizard()
    // Before any import in this session it is the genuine empty state.
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(true)

    await importTwo(wrapper)

    expect(wrapper.find('[data-test="servers-import-done"]').text()).toContain('2 servers imported.')
    expect(wrapper.find('[data-test="servers-nothing-to-import"]').exists()).toBe(false)
    // One completion state: the importer's own "imported" line is suppressed
    // while the wizard's done card shows (review F2.1).
    expect(wrapper.find('[data-test="stub-own-message"]').exists()).toBe(false)
    const verify = wrapper.find('[data-test="servers-import-done-verify"]')
    expect(verify.text()).toContain('Continue to Verify')
    await verify.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="panel-verify"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-servers"]').exists()).toBe(false)
  })

  it('(c) reopening the wizard resets the just-imported count', async () => {
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState({ has_usable_server: false }))
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: sixQuarantined } })
    const wrapper = await mountWizard()
    await importTwo(wrapper)
    expect(wrapper.find('[data-test="servers-inline-review"]').text()).toContain('including the 2 you just imported')

    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()
    await wrapper.find('[data-test="tab-servers"]').trigger('click')
    await flushPromises()

    const text = wrapper.find('[data-test="servers-inline-review"]').text()
    expect(text).toContain('6 servers are waiting in quarantine — review their tools before they can run.')
    expect(text).not.toContain('including')
  })
})
