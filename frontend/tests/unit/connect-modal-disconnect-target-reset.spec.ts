import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import api from '@/services/api'

// Spec 109 PR-a review round 2 (finding 3): close() (the useDialogOpen onClose
// handler, fired on a native Escape/backdrop dismiss as well as the explicit
// Close button) resets several result/preview fields but not
// `disconnectTarget`. Pressing Escape while the "Disconnect X?" confirm
// sub-panel is open and reopening the modal must not show the stale confirm
// panel for a client the user may no longer intend to touch.

// jsdom implements the `open` attribute but not showModal()/close(); polyfill
// just enough of the native behaviour to exercise the real Escape path (same
// approach as use-dialog-open-native-close.spec.ts).
function polyfillNativeDialog(el: HTMLDialogElement) {
  el.showModal = function (this: HTMLDialogElement) {
    this.open = true
  }
  el.close = function (this: HTMLDialogElement) {
    if (!this.open) return
    this.open = false
    this.dispatchEvent(new Event('close'))
  }
}

vi.mock('@/services/api', () => ({
  default: {
    getConnectStatus: vi.fn(),
    getConnectClientStatus: vi.fn(),
    getConnectPreview: vi.fn(),
    connectClient: vi.fn(),
    disconnectClient: vi.fn(),
    getOnboardingState: vi.fn(),
  },
}))

const PROXY = 'http://127.0.0.1:18080/mcp'

function row(id: string, overrides: Record<string, unknown> = {}) {
  return {
    id,
    name: id,
    config_path: `/Users/test/.${id}/mcp.json`,
    exists: true,
    connected: true,
    supported: true,
    icon: id,
    proxy_url: PROXY,
    ...overrides,
  }
}

function onboardingState(ids: string[]) {
  return {
    success: true,
    data: {
      has_connected_client: ids.length > 0,
      has_configured_server: false,
      connected_client_count: ids.length,
      connected_client_ids: ids,
      configured_server_count: 0,
      state: { engaged: false },
      should_show_wizard: false,
      first_mcp_client_ever: false,
      mcp_clients_seen_ever: [],
    },
  }
}

describe('ConnectModal — disconnectTarget cleared on close (review round 2, finding 3)', () => {
  let pinia: any

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    for (const fn of Object.values(api) as any[]) fn.mockReset()
    ;(api.getOnboardingState as any).mockResolvedValue(onboardingState(['cursor']))
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [row('cursor')] })
    ;(api.getConnectClientStatus as any).mockResolvedValue({ success: false, error: 'nope' })
  })

  it('does not show the stale "Disconnect cursor?" panel after Escape + reopen', async () => {
    const wrapper = mount(ConnectModal, { props: { show: false }, global: { plugins: [pinia] } })
    const dialogNode = wrapper.element as HTMLDialogElement
    polyfillNativeDialog(dialogNode)

    await wrapper.setProps({ show: true })
    await flushPromises()

    await wrapper.find('[data-test="connect-disconnect-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-disconnect-confirm"]').exists()).toBe(true)

    // Escape closes the dialog natively while the confirm sub-panel is open.
    dialogNode.close()
    await flushPromises()
    expect(wrapper.emitted('close')).toBeTruthy()

    // The parent flips its own `show` false on the emitted close, then the
    // user reopens the modal (e.g. from the sidebar).
    await wrapper.setProps({ show: false })
    await flushPromises()
    await wrapper.setProps({ show: true })
    await flushPromises()

    expect(wrapper.find('[data-test="connect-disconnect-confirm"]').exists()).toBe(false)
  })
})
