import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ConnectModal from '@/components/ConnectModal.vue'
import api from '@/services/api'

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
    connected: false,
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

function connectOk(id: string) {
  return {
    success: true,
    data: { success: true, action: 'connected', message: `Connected ${id}`, config_path: `/Users/test/.${id}/mcp.json`, backup_path: '' },
  }
}

function resolvedRow(id: string) {
  return {
    success: true,
    data: row(id, { connected: true, access_state: 'accessible', registered_url: PROXY, endpoint_match: 'this' }),
  }
}

async function openModal(pinia: any) {
  const wrapper = mount(ConnectModal, { props: { show: false }, global: { plugins: [pinia] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

/**
 * The stat-only listing (GET /connect) never reports connected=true, so after
 * a successful write the modal must re-fetch the content-resolved onboarding
 * state — otherwise rows keep "Review & connect" and the footer keeps counting
 * clients the backend has already connected.
 */
describe('ConnectModal — state refresh after connect/disconnect', () => {
  let pinia: any
  let connectedIds: string[]

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    for (const fn of Object.values(api) as any[]) fn.mockReset()
    connectedIds = []
    ;(api.getOnboardingState as any).mockImplementation(async () => onboardingState([...connectedIds]))
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: [row('cursor'), row('codex')] })
    // Default: the verification read fails, so the refreshed onboarding state
    // is the only thing that can flip the row.
    ;(api.getConnectClientStatus as any).mockResolvedValue({ success: false, error: 'nope' })
    ;(api.connectClient as any).mockImplementation(async (id: string) => {
      connectedIds.push(id)
      return connectOk(id)
    })
  })

  it('flips a row to Disconnect and updates the footer after a single connect', async () => {
    ;(api.getConnectPreview as any).mockResolvedValue({
      success: true,
      data: { client_id: 'cursor', config_path: '/Users/test/.cursor/mcp.json', file_exists: true, entry_exists: false, server_name: 'mcpproxy', entry: {} },
    })
    const wrapper = await openModal(pinia)
    expect(wrapper.find('[data-test="connect-all"]').text()).toBe('Connect 2 clients')

    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-confirm-cursor"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-start-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-disconnect-cursor"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="connect-all"]').text()).toBe('Connect 1 client')
  })

  it('leaves no stale Connect buttons after Connect All', async () => {
    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()

    expect(api.connectClient).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="connect-start-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-start-codex"]').exists()).toBe(false)
    const all = wrapper.find('[data-test="connect-all"]')
    expect(all.text()).toBe('Connect All')
    expect(all.attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-test="connect-all-hint"]').text()).toContain('already connected')
  })

  it('flips a row back to Review & connect after a disconnect', async () => {
    connectedIds = ['cursor']
    ;(api.disconnectClient as any).mockImplementation(async (id: string) => {
      connectedIds = connectedIds.filter(c => c !== id)
      return { success: true, data: { success: true, action: 'disconnected', message: 'ok', config_path: '', backup_path: '' } }
    })
    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-disconnect-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-disconnect-confirm-button"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-disconnect-cursor"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="connect-start-cursor"]').exists()).toBe(true)
  })

  it('verifies the endpoint automatically after a connect', async () => {
    ;(api.getConnectClientStatus as any).mockImplementation(async (id: string) => resolvedRow(id))
    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()

    expect(api.getConnectClientStatus).toHaveBeenCalledWith('cursor')
    expect(api.getConnectClientStatus).toHaveBeenCalledWith('codex')
    // Both confirmation lines survive — a later connect's list refetch must not
    // wipe an earlier client's verification.
    expect(wrapper.find('[data-test="connect-endpoint-cursor"]').text()).toContain(PROXY)
    expect(wrapper.find('[data-test="connect-endpoint-codex"]').text()).toContain(PROXY)
  })

  it('keeps an earlier verified endpoint line through a later Connect All', async () => {
    ;(api.getConnectClientStatus as any).mockImplementation(async (id: string) => resolvedRow(id))
    ;(api.getConnectPreview as any).mockResolvedValue({
      success: true,
      data: { client_id: 'cursor', config_path: '/Users/test/.cursor/mcp.json', file_exists: true, entry_exists: false, server_name: 'mcpproxy', entry: {} },
    })
    const wrapper = await openModal(pinia)
    await wrapper.find('[data-test="connect-start-cursor"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-preview-confirm-cursor"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-endpoint-cursor"]').exists()).toBe(true)

    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    expect(api.connectClient).toHaveBeenLastCalledWith('codex', 'mcpproxy', false)
    expect(wrapper.find('[data-test="connect-endpoint-cursor"]').text()).toContain(PROXY)
    expect(wrapper.find('[data-test="connect-endpoint-codex"]').text()).toContain(PROXY)
  })

  it('does not read any config on a failed connect beyond the access check', async () => {
    ;(api.connectClient as any).mockResolvedValue({ success: false, error: 'boom' })
    const wrapper = await openModal(pinia)
    const before = (api.getOnboardingState as any).mock.calls.length
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    // No success → no onboarding refresh.
    expect((api.getOnboardingState as any).mock.calls.length).toBe(before)
    expect(wrapper.find('[data-test="connect-start-cursor"]').exists()).toBe(true)
  })
})
