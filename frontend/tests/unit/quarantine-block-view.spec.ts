import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// MCP-2199 (GH #632): the Tool Quarantine view must offer a "Block" next to
// every "Approve" and a "Block All" next to "Approve All". Clicking them calls
// api.blockTools with the right args (single tool name, or none = block_all).

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
            },
          ],
        })
      ),
      // selectQuarantinedTools surfaces both "changed" (rug-pull) and "pending"
      // tools on a non-quarantined server (MCP-2917); a "changed" tool here.
      getToolApprovals: vi.fn(() =>
        ok({
          tools: [
            { tool_name: 'create_issue', status: 'changed', description: 'Create an issue' },
          ],
          count: 1,
        })
      ),
      getToolDiff: vi.fn(() => ok({})),
      // The quarantine panel only renders inside the tools-tab's non-empty
      // branch, so the server must report at least one tool here.
      getServerTools: vi.fn(() =>
        ok({ tools: [{ name: 'create_issue', description: 'Create an issue', enabled: false }] })
      ),
      getSecurityOverview: vi.fn(() => ok({})),
      listScanners: vi.fn(() => ok({ scanners: [] })),
      getServerLogs: vi.fn(() => ok({ logs: [] })),
      discoverServerTools: vi.fn(() => ok({})),
      approveTools: vi.fn(() => ok({ approved: 1 })),
      blockTools: vi.fn(() => ok({ blocked: 1 })),
    },
  }
})

describe('ServerDetail — Block buttons in Tool Quarantine (MCP-2199)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  async function mountDetail() {
    const api = (await import('@/services/api')).default
    const ServerDetail = (await import('@/views/ServerDetail.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [{ path: '/servers/:serverName', component: { template: '<div/>' } }],
    })
    await router.push('/servers/github?tab=tools')
    await router.isReady()
    const wrapper = mount(ServerDetail, {
      props: { serverName: 'github' },
      global: { plugins: [createPinia(), router] },
    })
    await flushPromises()
    return { wrapper, api }
  }

  it('renders a Block button next to each Approve and a Block All next to Approve All', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-block-all"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="quarantine-block-create_issue"]').exists()).toBe(true)
  })

  it('Block calls api.blockTools with the single tool name', async () => {
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="quarantine-block-create_issue"]').trigger('click')
    await flushPromises()
    expect(api.blockTools).toHaveBeenCalledWith('github', ['create_issue'])
  })

  it('Block All calls api.blockTools with no tool list (block_all)', async () => {
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="quarantine-block-all"]').trigger('click')
    await flushPromises()
    expect(api.blockTools).toHaveBeenCalledWith('github')
  })

  // MCP-2217 (PR #654 review): blocking a tool must also re-fetch serverTools so
  // the Available Tools row/toggle reflects the now-disabled state immediately,
  // not just after a manual reload. Mirrors the approve / enable-disable paths.
  it('Block refreshes serverTools (getServerTools) after success', async () => {
    const { wrapper, api } = await mountDetail()
    ;(api.getServerTools as ReturnType<typeof vi.fn>).mockClear()
    await wrapper.find('[data-test="quarantine-block-create_issue"]').trigger('click')
    await flushPromises()
    expect(api.getServerTools).toHaveBeenCalledWith('github')
  })

  it('Block All refreshes serverTools (getServerTools) after success', async () => {
    const { wrapper, api } = await mountDetail()
    ;(api.getServerTools as ReturnType<typeof vi.fn>).mockClear()
    await wrapper.find('[data-test="quarantine-block-all"]').trigger('click')
    await flushPromises()
    expect(api.getServerTools).toHaveBeenCalledWith('github')
  })
})
