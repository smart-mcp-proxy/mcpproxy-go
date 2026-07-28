import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 088 US1 (T009) — the server Configuration tab exposes the tri-mode TRUST
// MODE selector (auto | scan | manual) in place of the legacy binary
// "Auto-approve tool changes" toggle (MCP-2932). Saving writes the modern
// `trust_mode` field ONLY (FR-004/FR-005) — `auto_approve_tool_changes` and
// `skip_quarantine` are never sent again — and surfaces the `restart_required`
// notice the PATCH response carries.
//
// Spec 088 T013 (FR-021) — the tool-quarantine hint no longer recommends the
// deprecated `skip_quarantine` setting; it points at the trust-mode selector on
// the Configuration tab instead.

let serverTrustMode: string | undefined
let patchResponse: { success: boolean; data?: unknown; error?: string }
let approvalTools: Array<Record<string, unknown>>
let inventoryTools: Array<Record<string, unknown>>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getServers: vi.fn(() =>
        ok({
          servers: [
            {
              name: 'github',
              protocol: 'stdio',
              enabled: true,
              connected: true,
              quarantined: false,
              tool_count: 1,
              ...(serverTrustMode === undefined ? {} : { trust_mode: serverTrustMode }),
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: approvalTools, count: approvalTools.length })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: inventoryTools })),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
      patchServer: vi.fn(() => Promise.resolve(patchResponse)),
    },
  }
})

async function mountDetail(tab: 'config' | 'tools') {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push(`/servers/github?tab=${tab}`)
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'github' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return { wrapper, api }
}

function isChecked(wrapper: ReturnType<typeof mount>, mode: string): boolean {
  const input = wrapper.find(`[data-test="trust-mode-option-${mode}"] input`)
  return (input.element as HTMLInputElement).checked
}

async function selectMode(wrapper: ReturnType<typeof mount>, mode: string) {
  await wrapper.find(`[data-test="trust-mode-option-${mode}"] input`).setValue(true)
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.clearAllMocks()
  serverTrustMode = undefined
  patchResponse = { success: true, data: { message: 'ok' } }
  approvalTools = []
  inventoryTools = []
})

describe('ServerDetail — trust-mode control (Spec 088 US1, T009)', () => {
  it('renders the trust-mode selector instead of the legacy auto-approve toggle', async () => {
    const { wrapper } = await mountDetail('config')
    expect(wrapper.find('[data-test="trust-mode-selector"]').exists()).toBe(true)
    // FR-005: the legacy binary control is gone from the Configuration tab.
    expect(wrapper.find('[data-test="auto-approve-tool-changes"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="auto-approve-card"]').exists()).toBe(false)
  })

  it('offers exactly the three trust modes', async () => {
    const { wrapper } = await mountDetail('config')
    for (const mode of ['auto', 'scan', 'manual']) {
      expect(wrapper.find(`[data-test="trust-mode-option-${mode}"]`).exists()).toBe(true)
    }
    expect(wrapper.findAll('[data-test^="trust-mode-option-"]')).toHaveLength(3)
  })

  it('presents manual as the current default when no trust mode is configured', async () => {
    const { wrapper } = await mountDetail('config')
    expect(isChecked(wrapper, 'manual')).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-default-note"]').exists()).toBe(true)
  })

  it('reflects an explicitly configured mode as the current selection', async () => {
    serverTrustMode = 'scan'
    const { wrapper } = await mountDetail('config')
    expect(isChecked(wrapper, 'scan')).toBe(true)
    expect(wrapper.find('[data-test="trust-mode-default-note"]').exists()).toBe(false)
  })

  it('shows the raw value plus the fail-closed effective mode for an unrecognized value', async () => {
    serverTrustMode = 'bogus'
    const { wrapper } = await mountDetail('config')
    const note = wrapper.find('[data-test="trust-mode-invalid-note"]')
    expect(note.exists()).toBe(true)
    expect(note.text()).toContain('bogus')
    expect(isChecked(wrapper, 'manual')).toBe(true)
  })

  it('saves a selection via PATCH {trust_mode} and never sends legacy fields', async () => {
    const { wrapper, api } = await mountDetail('config')
    await selectMode(wrapper, 'scan')
    await flushPromises()
    expect(api.patchServer).toHaveBeenCalledWith('github', { trust_mode: 'scan' })
    const patch = (api.patchServer as unknown as { mock: { calls: unknown[][] } }).mock.calls[0][1]
    expect(Object.keys(patch as object)).toEqual(['trust_mode'])
  })

  it('refreshes the server projection after a successful save', async () => {
    const { wrapper, api } = await mountDetail('config')
    const before = (api.getServers as unknown as { mock: { calls: unknown[] } }).mock.calls.length
    await selectMode(wrapper, 'scan')
    await flushPromises()
    expect(
      (api.getServers as unknown as { mock: { calls: unknown[] } }).mock.calls.length
    ).toBeGreaterThan(before)
  })

  it('does not save the least-safe auto mode until the warning is acknowledged (FR-003)', async () => {
    const { wrapper, api } = await mountDetail('config')
    await selectMode(wrapper, 'auto')
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-auto-confirm"]').exists()).toBe(true)
    expect(api.patchServer).not.toHaveBeenCalled()

    await wrapper.find('[data-test="trust-mode-auto-confirm-accept"]').trigger('click')
    await flushPromises()
    expect(api.patchServer).toHaveBeenCalledWith('github', { trust_mode: 'auto' })
  })

  it('cancelling the auto-mode warning saves nothing', async () => {
    const { wrapper, api } = await mountDetail('config')
    await selectMode(wrapper, 'auto')
    await wrapper.find('[data-test="trust-mode-auto-confirm-cancel"]').trigger('click')
    await flushPromises()
    expect(api.patchServer).not.toHaveBeenCalled()
  })

  it('surfaces the restart-required notice returned by the save (FR-004)', async () => {
    patchResponse = { success: true, data: { message: 'ok', restart_required: true } }
    const { wrapper } = await mountDetail('config')
    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(false)
    await selectMode(wrapper, 'scan')
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(true)
  })

  it('shows no restart notice when the response does not report one', async () => {
    patchResponse = { success: true, data: { message: 'ok' } }
    const { wrapper } = await mountDetail('config')
    await selectMode(wrapper, 'scan')
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(false)
  })

  it('reports a failed save and shows no restart notice', async () => {
    patchResponse = { success: false, error: 'boom' }
    const { wrapper } = await mountDetail('config')
    await selectMode(wrapper, 'scan')
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-restart-notice"]').exists()).toBe(false)
  })
})

describe('ServerDetail — tool-quarantine hint hygiene (Spec 088 FR-021, T013)', () => {
  beforeEach(() => {
    approvalTools = [
      { tool_name: 'create_issue', status: 'pending', description: 'Create an issue' },
    ]
    inventoryTools = [{ name: 'create_issue', description: 'Create an issue', enabled: false }]
  })

  it('no longer recommends the deprecated skip_quarantine setting', async () => {
    const { wrapper } = await mountDetail('tools')
    const hint = wrapper.find('[data-test="quarantine-hint"]')
    expect(hint.exists()).toBe(true)
    expect(hint.text()).not.toMatch(/skip_quarantine/)
  })

  it('points at the trust-mode selector on the Configuration tab', async () => {
    const { wrapper } = await mountDetail('tools')
    const hint = wrapper.find('[data-test="quarantine-hint"]')
    expect(hint.text()).toMatch(/trust mode/i)
    expect(hint.text()).toMatch(/configuration/i)
    expect(wrapper.find('[data-test="quarantine-hint-trust-mode"]').exists()).toBe(true)
  })

  it('the hint link opens the Configuration tab with the trust-mode selector', async () => {
    const { wrapper } = await mountDetail('tools')
    await wrapper.find('[data-test="quarantine-hint-trust-mode"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="trust-mode-selector"]').exists()).toBe(true)
  })

  it('stays dismissible (no regression)', async () => {
    const { wrapper } = await mountDetail('tools')
    await wrapper.find('[data-test="quarantine-hint-dismiss"]').trigger('click')
    expect(wrapper.find('[data-test="quarantine-hint"]').exists()).toBe(false)
  })
})
