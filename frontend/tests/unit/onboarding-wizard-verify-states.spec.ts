import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// UX audit F13: the Verify tab called the MCP `initialize` handshake
// "Round-trip verified", which reads as "you have seen the product work".
// It has not: `first_mcp_client_ever` is stamped from the AfterInitialize
// hook (internal/server/mcp.go), before any tool is listed let alone called,
// and all three suggested prompts target mcpproxy's own built-ins (recorded
// as `internal_tool_call`, never stamping `first_real_tool_call_ever`).
//
// The honest split is two milestones: the client connected, and an upstream
// tool actually ran. The second is already on the wire — `GET /api/v1/status`
// serves the whole activation block to an admin caller
// (internal/httpapi/server.go), including `first_real_tool_call_ever`.

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getOnboardingState: vi.fn(),
    getActivities: vi.fn(),
    getConfig: vi.fn(),
    getDockerStatus: vi.fn(),
    getCanonicalConfigPaths: vi.fn(),
    getStatus: vi.fn(),
  },
}))

function onboardingState() {
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
      // The handshake milestone IS satisfied in every case below — the whole
      // point is that it must not imply the second one.
      first_mcp_client_ever: true,
      mcp_clients_seen_ever: ['claude-code'],
      incomplete_tab_count: 0,
    },
  }
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'dashboard', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div />' } },
    ],
  })
}

/** Mount the wizard, land on Verify, with `getStatus` behaving as given. */
async function openVerifyTab(status: { resolved?: unknown; rejects?: boolean }) {
  if (status.rejects) {
    ;(api.getStatus as any).mockRejectedValue(new Error('boom'))
  } else {
    ;(api.getStatus as any).mockResolvedValue(status.resolved)
  }

  const router = makeRouter()
  router.push('/')
  await router.isReady()

  const wrapper = mount(OnboardingWizard, {
    props: { show: false },
    global: {
      plugins: [router],
      stubs: { RouterLink: { template: '<a><slot /></a>' } },
    },
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  await wrapper.find('[data-test="tab-verify"]').trigger('click')
  await flushPromises()
  return wrapper
}

describe('OnboardingWizard verify states (F13)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [] })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: false } })
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    ;(api.getStatus as any).mockResolvedValue({ success: true, data: {} })
  })

  it('never claims the handshake was a verified round-trip', async () => {
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { activation: { first_real_tool_call_ever: true } } },
    })
    const panel = wrapper.find('[data-test="panel-verify"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).not.toContain('Round-trip verified')
    // …and the handshake milestone's caveat retires once the second one lands,
    // instead of contradicting it.
    expect(wrapper.find('[data-test="verify-client-connected"]').text())
      .not.toContain('does not yet mean a tool has run')
  })

  it('reports the handshake as its own milestone, naming the client', async () => {
    const wrapper = await openVerifyTab({ resolved: { success: true, data: {} } })
    const row = wrapper.find('[data-test="verify-client-connected"]')
    expect(row.exists()).toBe(true)
    expect(row.attributes('data-state')).toBe('satisfied')
    expect(row.text()).toContain('claude-code')
    expect(row.text()).toContain('does not yet mean a tool has run')
  })

  it('shows the upstream-call milestone as pending when the activation block is absent', async () => {
    const wrapper = await openVerifyTab({ resolved: { success: true, data: {} } })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.exists()).toBe(true)
    expect(row.attributes('data-state')).toBe('pending')
  })

  it('shows the upstream-call milestone as pending when first_real_tool_call_ever is false', async () => {
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { activation: { first_real_tool_call_ever: false } } },
    })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.attributes('data-state')).toBe('pending')
  })

  it('shows the upstream-call milestone as satisfied when first_real_tool_call_ever is true', async () => {
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { activation: { first_real_tool_call_ever: true } } },
    })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.attributes('data-state')).toBe('satisfied')
  })

  it('degrades to pending (never an error) when the status call fails', async () => {
    const wrapper = await openVerifyTab({ rejects: true })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.exists()).toBe(true)
    expect(row.attributes('data-state')).toBe('pending')
    // The rest of the panel still rendered — the failure must not abort onOpened().
    expect(wrapper.find('[data-test="verify-sample-prompts"]').exists()).toBe(true)
  })

  it('suggests at least one prompt that actually dispatches to an upstream server', async () => {
    const wrapper = await openVerifyTab({ resolved: { success: true, data: {} } })
    expect(wrapper.find('[data-test="verify-sample-prompts"]').text()).toContain('call_tool_read')
  })
})
