import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import AuthErrorModal from '@/components/AuthErrorModal.vue'
import ConnectionStatus from '@/components/ConnectionStatus.vue'
import { useSystemStore } from '@/stores/system'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => false),
    getAPIKeyPreview: vi.fn(() => 'none'),
    setAPIKey: vi.fn(),
    validateAPIKey: vi.fn(),
    reinitializeAPIKey: vi.fn(),
    createEventSource: vi.fn(),
  },
}))

/**
 * Audit F28: with no API key the user got three messages for one cause — the
 * "Authentication Required" modal, a red inline load error behind it, and a
 * yellow "Connection Lost — Reconnecting to server…" toast bottom-left. The key
 * field was also labelled "(optional)" while being the only way in, and
 * "Continue Without Auth" sat at equal weight beside it.
 */
describe('Authentication error surfaces (audit F28)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('calls the API key required, not optional', () => {
    const wrapper = mount(AuthErrorModal, {
      props: { show: true },
      global: { plugins: [createPinia()] },
    })

    const label = wrapper.find('label .label-text').text()
    expect(label).toContain('API key')
    expect(label).toContain('(required)')
    expect(label.toLowerCase()).not.toContain('optional')
  })

  it('demotes the dismiss action and says what dismissing costs', () => {
    const wrapper = mount(AuthErrorModal, {
      props: { show: true, canClose: true },
      global: { plugins: [createPinia()] },
    })

    const dismiss = wrapper.find('[data-test="auth-dismiss"]')
    expect(dismiss.exists()).toBe(true)
    // Ghost, not an outline button at equal weight with the primary.
    expect(dismiss.classes()).toContain('btn-ghost')
    expect(dismiss.text().toLowerCase()).toContain('empty')
  })

  it('offers a non-tray route to the key for headless and server installs', () => {
    const wrapper = mount(AuthErrorModal, {
      props: { show: true },
      global: { plugins: [createPinia()] },
    })

    const text = wrapper.text()
    expect(text).toContain('mcpproxy doctor')
    expect(text).toContain('mcp_config.json')
  })

  it('suppresses the reconnect toast while the auth modal owns the screen', async () => {
    const wrapper = mount(ConnectionStatus, { global: { plugins: [createPinia()] } })
    const store = useSystemStore()

    // Disconnected with no auth problem: the toast is the right message.
    store.connected = false
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="connection-lost-toast"]').exists()).toBe(true)

    // Disconnected *because* there is no key: the modal says it better.
    store.setAuthRequired(true)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="connection-lost-toast"]').exists()).toBe(false)

    store.setAuthRequired(false)
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[data-test="connection-lost-toast"]').exists()).toBe(true)
  })
})
