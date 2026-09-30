import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { existsSync } from 'node:fs'
import { resolve } from 'node:path'
import ClientConnectList from '@/components/ClientConnectList.vue'
import api from '@/services/api'

// Spec 109 T127 (FR-032): a bulk "Connect N clients" shows the combined diff of
// EVERY file it will write (client, target path, entry, backup notice) and
// writes nothing until the user confirms. ConnectModal.vue is gone; this list is
// the one connect component.

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

const IDS = ['cursor', 'codex', 'windsurf']

function row(id: string) {
  return {
    id,
    name: id.toUpperCase(),
    config_path: `/Users/test/.${id}/mcp.json`,
    exists: true,
    connected: false,
    supported: true,
    icon: id,
  }
}

function preview(id: string, overrides: Record<string, unknown> = {}) {
  return {
    success: true,
    data: {
      client: id,
      config_path: `/Users/test/.${id}/mcp.json`,
      display_path: `~/.${id}/mcp.json`,
      format: 'json',
      server_key: 'mcpServers',
      server_name: 'mcpproxy',
      entry_text: `{"mcpproxy":{"url":"http://127.0.0.1:8080/mcp","note":"${id}"}}`,
      entry_exists: false,
      contains_api_key: false,
      access_state: 'accessible',
      ...overrides,
    },
  }
}

function connectOk(id: string) {
  return {
    success: true,
    data: { success: true, action: 'connected', message: `Connected ${id}`, config_path: `/Users/test/.${id}/mcp.json`, backup_path: '' },
  }
}

async function openList(pinia: ReturnType<typeof createPinia>) {
  const wrapper = mount(ClientConnectList, { props: { show: false }, global: { plugins: [pinia] } })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

describe('ClientConnectList bulk preview (FR-032)', () => {
  let pinia: ReturnType<typeof createPinia>

  beforeEach(() => {
    pinia = createPinia()
    setActivePinia(pinia)
    for (const fn of Object.values(api) as any[]) fn.mockReset()
    ;(api.getConnectStatus as any).mockResolvedValue({ success: true, data: IDS.map(row) })
    ;(api.getOnboardingState as any).mockResolvedValue({ success: true, data: null })
    ;(api.getConnectClientStatus as any).mockResolvedValue({ success: false })
    ;(api.getConnectPreview as any).mockImplementation(async (id: string) => preview(id))
    ;(api.connectClient as any).mockImplementation(async (id: string) => connectOk(id))
  })

  it('previews every client with its target path and the backup notice, writing nothing', async () => {
    const wrapper = await openList(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()

    expect(api.getConnectPreview).toHaveBeenCalledTimes(3)
    expect(api.connectClient).not.toHaveBeenCalled()

    const panel = wrapper.find('[data-test="connect-bulk-preview"]')
    expect(panel.exists()).toBe(true)
    for (const id of IDS) {
      expect(panel.text()).toContain(`"note":"${id}"`)
      expect(wrapper.find(`[data-test="connect-bulk-preview-path-${id}"]`).text()).toBe(`~/.${id}/mcp.json`)
    }
    expect(panel.text()).toContain('backed up first')
  })

  it('falls back to config_path when the preview has no display_path', async () => {
    ;(api.getConnectPreview as any).mockImplementation(async (id: string) => preview(id, { display_path: undefined }))
    const wrapper = await openList(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="connect-bulk-preview-path-cursor"]').text()).toBe('/Users/test/.cursor/mcp.json')
  })

  it('Cancel closes the preview and never writes', async () => {
    const wrapper = await openList(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-bulk-preview-cancel"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-bulk-preview"]').exists()).toBe(false)
    expect(api.connectClient).not.toHaveBeenCalled()
  })

  it('fails closed when one preview fails: message shown, no panel, no write', async () => {
    ;(api.getConnectPreview as any).mockImplementation(async (id: string) =>
      id === 'codex' ? { success: false, error: 'cannot read codex config' } : preview(id),
    )
    const wrapper = await openList(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="connect-bulk-preview"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('cannot read codex config')
    expect(api.connectClient).not.toHaveBeenCalled()
  })

  it('Confirm writes once per previewed client, in order', async () => {
    const wrapper = await openList(pinia)
    await wrapper.find('[data-test="connect-all"]').trigger('click')
    await flushPromises()
    await wrapper.find('[data-test="connect-bulk-preview-confirm"]').trigger('click')
    await flushPromises()

    expect((api.connectClient as any).mock.calls.map((c: unknown[]) => c[0])).toEqual(IDS)
  })
})

describe('ConnectModal shim (FR-032)', () => {
  it('stays deleted: ClientConnectList is the one connect component', () => {
    const shim = resolve(__dirname, '../../src/components/ConnectModal.vue')
    expect(existsSync(shim)).toBe(false)
  })
})
