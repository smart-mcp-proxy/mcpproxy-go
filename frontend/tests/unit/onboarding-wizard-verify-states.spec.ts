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
async function openVerifyTab(status: { resolved?: unknown; rejects?: boolean; impl?: () => any }) {
  if (status.rejects) {
    ;(api.getStatus as any).mockRejectedValue(new Error('boom'))
  } else if (status.impl) {
    ;(api.getStatus as any).mockImplementation(status.impl)
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
      resolved: { success: true, data: { routing_mode: 'retrieve_tools', activation: { first_real_tool_call_ever: true } } },
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
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { routing_mode: 'retrieve_tools', activation: { first_real_tool_call_ever: false } } },
    })
    const row = wrapper.find('[data-test="verify-client-connected"]')
    expect(row.exists()).toBe(true)
    expect(row.attributes('data-state')).toBe('satisfied')
    expect(row.text()).toContain('claude-code')
    expect(row.text()).toContain('does not yet mean a tool has run')
  })

  it('hides the upstream-call milestone when the activation block is absent', async () => {
    // Absent means "we cannot tell" (early startup / telemetry unwired), not
    // "no call happened". Asserting the negative would be the same class of
    // untruth as the "Round-trip verified" copy this change removes.
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { routing_mode: 'retrieve_tools' } },
    })
    expect(wrapper.find('[data-test="verify-first-upstream-call"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="verify-client-connected"]').text())
      .not.toContain('does not yet mean a tool has run')
  })

  it('shows the upstream-call milestone as pending when first_real_tool_call_ever is false', async () => {
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { routing_mode: 'retrieve_tools', activation: { first_real_tool_call_ever: false } } },
    })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.attributes('data-state')).toBe('pending')
  })

  it('shows the upstream-call milestone as satisfied when first_real_tool_call_ever is true', async () => {
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { routing_mode: 'retrieve_tools', activation: { first_real_tool_call_ever: true } } },
    })
    const row = wrapper.find('[data-test="verify-first-upstream-call"]')
    expect(row.attributes('data-state')).toBe('satisfied')
  })

  it('degrades silently (never an error, never a false negative) when the status call fails', async () => {
    const wrapper = await openVerifyTab({ rejects: true })
    expect(wrapper.find('[data-test="verify-first-upstream-call"]').exists()).toBe(false)
    // The rest of the panel still rendered — the failure must not abort onOpened().
    expect(wrapper.find('[data-test="verify-sample-prompts"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="panel-verify"]').text()).not.toContain('No upstream tool call yet')
  })

  // The core stamps first_real_tool_call_ever at exactly one site: the
  // call_tool_* handler. routing_mode=direct and code_execution dispatch
  // upstream WITHOUT stamping it, so a false flag there means "not tracked".
  for (const mode of ['direct', 'code_execution']) {
    it(`never claims "no upstream tool call yet" under routing_mode=${mode}, where the flag is blind`, async () => {
      const wrapper = await openVerifyTab({
        resolved: { success: true, data: { routing_mode: mode, activation: { first_real_tool_call_ever: false } } },
      })
      expect(wrapper.find('[data-test="verify-first-upstream-call"]').exists()).toBe(false)
      const panel = wrapper.find('[data-test="panel-verify"]')
      expect(panel.text()).not.toContain('No upstream tool call yet')
      expect(panel.text()).not.toContain('does not yet mean a tool has run')
    })
  }

  it('still reports the milestone reached under a non-stamping routing mode once the flag has latched', async () => {
    // The flag is a lifetime fact. If it is true it IS true, whatever surface
    // is serving /mcp now — so the positive is safe to show in any mode.
    const wrapper = await openVerifyTab({
      resolved: { success: true, data: { routing_mode: 'direct', activation: { first_real_tool_call_ever: true } } },
    })
    expect(wrapper.find('[data-test="verify-first-upstream-call"]').attributes('data-state')).toBe('satisfied')
  })

  it('does not un-latch a reached milestone when a later poll omits the activation block', async () => {
    vi.useFakeTimers()
    try {
      let call = 0
      const wrapper = await openVerifyTab({
        impl: () => {
          call++
          return Promise.resolve(
            call === 1
              ? { success: true, data: { routing_mode: 'retrieve_tools', activation: { first_real_tool_call_ever: true } } }
              : { success: true, data: { routing_mode: 'retrieve_tools' } },
          )
        },
      })
      expect(wrapper.find('[data-test="verify-first-upstream-call"]').attributes('data-state')).toBe('satisfied')
      await vi.advanceTimersByTimeAsync(5000)
      await flushPromises()
      expect(call).toBeGreaterThan(1)
      expect(wrapper.find('[data-test="verify-first-upstream-call"]').attributes('data-state')).toBe('satisfied')
    } finally {
      vi.useRealTimers()
    }
  })

  it('suggests at least one prompt that actually dispatches to an upstream server', async () => {
    const wrapper = await openVerifyTab({ resolved: { success: true, data: {} } })
    expect(wrapper.find('[data-test="verify-sample-prompts"]').text()).toContain('call_tool_read')
  })
})
