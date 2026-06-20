import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// MCP-2932 (Spec 032 / parent MCP-2916): the server Configuration tab exposes an
// "Auto-approve tool changes" toggle bound to the per-server
// `auto_approve_tool_changes` config flag (MCP-2930). Enabling it disables
// rug-pull protection for that server, so a ⚠️ warning hint sits beneath it.
// Toggling persists through the existing PATCH /api/v1/servers/{id} path.

let serverAutoApprove = false

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
              auto_approve_tool_changes: serverAutoApprove,
            },
          ],
        })
      ),
      getToolApprovals: vi.fn(() => ok({ tools: [], count: 0 })),
      getToolDiff: vi.fn(() => ok({})),
      getServerTools: vi.fn(() => ok({ tools: [] })),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      patchServer: vi.fn(() => ok({})),
    },
  }
})

async function mountDetail() {
  const api = (await import('@/services/api')).default
  const ServerDetail = (await import('@/views/ServerDetail.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
  })
  await router.push('/servers/github?tab=config')
  await router.isReady()
  const wrapper = mount(ServerDetail, {
    props: { serverName: 'github' },
    global: { plugins: [createPinia(), router] },
  })
  await flushPromises()
  return { wrapper, api }
}

describe('ServerDetail — Auto-approve tool changes (MCP-2932)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    serverAutoApprove = false
  })

  it('renders the auto-approve checkbox and rug-pull warning hint', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="auto-approve-tool-changes"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="auto-approve-warning"]').exists()).toBe(true)
  })

  it('checkbox reflects the server auto_approve_tool_changes value (off by default)', async () => {
    const { wrapper } = await mountDetail()
    const cb = wrapper.find('[data-test="auto-approve-tool-changes"]')
      .element as HTMLInputElement
    expect(cb.checked).toBe(false)
  })

  it('checkbox reflects an enabled server flag', async () => {
    serverAutoApprove = true
    const { wrapper } = await mountDetail()
    const cb = wrapper.find('[data-test="auto-approve-tool-changes"]')
      .element as HTMLInputElement
    expect(cb.checked).toBe(true)
  })

  it('toggling on persists via PATCH with auto_approve_tool_changes:true', async () => {
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="auto-approve-tool-changes"]').setValue(true)
    await flushPromises()
    expect(api.patchServer).toHaveBeenCalledWith('github', {
      auto_approve_tool_changes: true,
    })
  })

  it('toggling off persists via PATCH with auto_approve_tool_changes:false', async () => {
    serverAutoApprove = true
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="auto-approve-tool-changes"]').setValue(false)
    await flushPromises()
    expect(api.patchServer).toHaveBeenCalledWith('github', {
      auto_approve_tool_changes: false,
    })
  })
})
