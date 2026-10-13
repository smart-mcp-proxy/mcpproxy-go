import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k (activity-scope-filters, T113): a Tools row's usage count links
// to `/activity?view=calls&tool=<server:tool>` (url-filter-contract.md link
// map, "Tools row" -> "Calls").

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({
          tools: [
            { name: 'create_issue', server_name: 'github', description: 'Create an issue', enabled: true, usage: 42 },
            { name: 'get_repo', server_name: 'github', description: 'Get repo info', enabled: true, usage: 0 },
          ],
          stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 0 },
        })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
    },
  }
})

async function mountTools() {
  const Tools = (await import('@/views/Tools.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/tools', component: Tools },
      { path: '/servers', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
    ],
  })
  await router.push('/tools')
  await router.isReady()
  const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('Tools row "Calls" link', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('links a nonzero usage count to /activity?view=calls&tool=server:tool', async () => {
    const wrapper = await mountTools()
    const link = wrapper.find('[data-test="tool-calls-link"]')
    expect(link.exists()).toBe(true)
    expect(link.text()).toBe('42')

    const href = link.attributes('href') ?? ''
    expect(href).toContain('/activity')
    expect(href).toContain('view=calls')
    expect(href).toContain('tool=github:create_issue')
  })

  it('renders a plain zero for a tool with no usage (no link)', async () => {
    const wrapper = await mountTools()
    const rows = wrapper.findAll('[data-test="tool-row"]')
    const zeroUsageRow = rows[1]
    expect(zeroUsageRow.find('[data-test="tool-calls-link"]').exists()).toBe(false)
    expect(zeroUsageRow.text()).toContain('0')
  })
})
