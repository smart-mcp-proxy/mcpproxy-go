import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import OnboardingWizard from '@/components/OnboardingWizard.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 2 (finding 2): dismiss() (wired as useDialogOpen's
// onClose) used to be async and await up to three sequential API calls BEFORE
// emit('close'). Until they resolved, props.show (the isOpen() source) stayed
// true even though the dialog may have already closed natively, so the
// driving watch never refires and the wizard cannot be reopened from the
// sidebar Setup entry during that window. The fix emits 'close' synchronously
// and runs the engagement bookkeeping in the background.

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
      // Not engaged, and neither step has a recorded status yet, so
      // dismiss()'s bookkeeping branch has at least one await to make before
      // it used to emit 'close' — exactly the window the finding describes.
      state: { engaged: false },
      should_show_wizard: true,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
      incomplete_tab_count: 1,
    },
  }
}

describe('OnboardingWizard dismiss() race (review round 2, finding 2)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getActivities as any).mockResolvedValue({ success: true, data: { activities: [] } })
    ;(api.getConfig as any).mockResolvedValue({ success: true, data: {} })
    ;(api.getDockerStatus as any).mockResolvedValue({ success: true, data: { available: true } })
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: { clients: [] } })
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState())
    ;(api.getCanonicalConfigPaths as any).mockResolvedValue({ success: true, data: { paths: [] } })
    // Never resolves during the test — simulates a slow/in-flight network
    // call, which is exactly the window the bug lived in.
    ;(api.markOnboardingState as any).mockImplementation(() => new Promise(() => {}))
  })

  it('emits close synchronously, before the engagement bookkeeping settles', async () => {
    const wrapper = mount(OnboardingWizard, {
      props: { show: false },
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { template: '<div />' },
        },
      },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    // Triggering the click dispatches the DOM event (and therefore runs the
    // `dismiss` handler) synchronously; `.trigger()`'s returned promise only
    // covers Vue's subsequent re-render. Checking `emitted('close')` right
    // here — before awaiting anything else — is what distinguishes "emits
    // immediately" from "awaits bookkeeping calls that never resolve first".
    const pending = wrapper.find('[aria-label="Close"]').trigger('click')
    expect(wrapper.emitted('close'), 'close must be emitted synchronously, not after the bookkeeping awaits').toBeTruthy()

    await pending
    await flushPromises()

    // The background bookkeeping still ran (best-effort, just decoupled from
    // the dialog's own open/close state).
    expect(api.markOnboardingState).toHaveBeenCalled()
  })

  it('can be reopened immediately after dismiss, even while bookkeeping is still in flight', async () => {
    const wrapper = mount(OnboardingWizard, {
      props: { show: false },
      global: {
        stubs: {
          RouterLink: { template: '<a><slot /></a>' },
          AddServerModal: { template: '<div />' },
        },
      },
    })
    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.find('[aria-label="Close"]').trigger('click')
    // A real parent (Dashboard + the onboarding store) flips its own open
    // flag false the instant 'close' is emitted — do the same here.
    await wrapper.setProps({ show: false })
    await flushPromises()

    // Reopening (e.g. the sidebar Setup entry) must not be a no-op just
    // because the previous dismiss's bookkeeping call is still pending.
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.props('show')).toBe(true)
    // A second dismiss must work too — if the wizard had been left desynced
    // internally, this would either throw or silently fail to re-emit.
    await wrapper.find('[aria-label="Close"]').trigger('click')
    expect(wrapper.emitted('close')?.length).toBeGreaterThanOrEqual(2)
  })
})
