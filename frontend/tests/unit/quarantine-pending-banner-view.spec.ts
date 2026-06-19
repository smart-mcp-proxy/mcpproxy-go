import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// MCP-2917: a non-quarantined server whose tools are all `pending` (new, never
// approved) must still surface the Tool-Quarantine banner with the per-tool and
// bulk Approve/Block buttons, the `pending` warning badge, and the dismissible
// hint. Pre-fix the banner was hidden whenever no tool was `changed`, even
// though those pending tools are genuinely blocked by the backend.

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
              quarantined: false, // NOT quarantined — the key condition
              tool_count: 1,
            },
          ],
        })
      ),
      // All tools pending, none changed — the exact MCP-2917 bug scenario.
      getToolApprovals: vi.fn(() =>
        ok({
          tools: [
            { tool_name: 'create_issue', status: 'pending', description: 'Create an issue' },
          ],
          count: 1,
        })
      ),
      getToolDiff: vi.fn(() => ok({})),
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

describe('ServerDetail — pending-only Tool Quarantine banner (MCP-2917)', () => {
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

  it('shows the banner with Approve/Block buttons for an all-pending, non-quarantined server', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="tool-quarantine-banner"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="quarantine-approve-all"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="quarantine-block-all"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="quarantine-approve-create_issue"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="quarantine-block-create_issue"]').exists()).toBe(true)
  })

  it('renders the pending warning badge for the tool', async () => {
    const { wrapper } = await mountDetail()
    const list = wrapper.find('[data-test="tool-quarantine-list"]')
    expect(list.exists()).toBe(true)
    const badge = list.find('.badge-warning')
    expect(badge.exists()).toBe(true)
    expect(badge.text()).toContain('pending')
  })

  it('shows the dismissible hint and lets the operator dismiss it', async () => {
    const { wrapper } = await mountDetail()
    expect(wrapper.find('[data-test="quarantine-hint"]').exists()).toBe(true)
    await wrapper.find('[data-test="quarantine-hint-dismiss"]').trigger('click')
    expect(wrapper.find('[data-test="quarantine-hint"]').exists()).toBe(false)
  })

  it('Approve calls api.approveTools with the pending tool name', async () => {
    const { wrapper, api } = await mountDetail()
    await wrapper.find('[data-test="quarantine-approve-create_issue"]').trigger('click')
    await flushPromises()
    expect(api.approveTools).toHaveBeenCalledWith('github', ['create_issue'])
  })
})
