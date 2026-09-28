import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109-k T113: url-filter-contract.md "Parameters" table — `server` and
// `status` are CLIENT-SIDE ONLY on Tools (`GET /tools` parses no query
// string, `handleGetGlobalTools`): the request must carry neither, no matter
// what the URL says, while the table still narrows. Plus the Tools row
// "Calls" link's target: `/activity?view=calls&tool=<server:tool>`, and the
// codex round 4 regression it guards against — a seeded `github:create_issue`
// record must actually show up on that page (sending `tool=github:create_issue`
// verbatim to REST matches nothing; the composable splits it first).

const TOOLS = [
  { name: 'create_issue', server_name: 'github', description: '', enabled: true, tier: 'write', approval_status: 'approved', usage: 3 },
  { name: 'read', server_name: 'notes', description: '', enabled: true, tier: 'read', approval_status: 'approved', usage: 0 },
  { name: 'write', server_name: 'notes', description: '', enabled: true, disabled: true, tier: 'write', approval_status: 'pending', usage: 1 },
]

let getGlobalToolsMock: ReturnType<typeof vi.fn>

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({ tools: TOOLS, stats: { total: TOOLS.length, enabled: 2, disabled: 1, pending_approval: 1 } })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
    },
  }
})

async function mountToolsAt(path: string) {
  const api = (await import('@/services/api')).default
  getGlobalToolsMock = api.getGlobalTools as unknown as ReturnType<typeof vi.fn>
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
  await router.push(path)
  await router.isReady()
  const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

const toolRows = (wrapper: Awaited<ReturnType<typeof mountToolsAt>>) =>
  wrapper.findAll('[data-test="tool-row"]')

describe('Tools — server/status stay client-side (Spec 109-k T113)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('?server=notes narrows the table but GET /tools carries no query at all', async () => {
    const wrapper = await mountToolsAt('/tools?server=notes')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(2)
    expect(rows.map(r => r.text()).join(' ')).not.toContain('create_issue')
    expect(getGlobalToolsMock).toHaveBeenCalledWith()
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
  })

  it('?status=disabled narrows the table but GET /tools carries no query at all', async () => {
    const wrapper = await mountToolsAt('/tools?status=disabled')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('write')
    expect(getGlobalToolsMock.mock.calls[0]).toHaveLength(0)
  })
})

describe('Tools row "Calls" link -> Activity (Spec 109-k link map + T113 regression)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('links to /activity?view=calls&tool=<server:tool>', async () => {
    const wrapper = await mountToolsAt('/tools')
    const row = toolRows(wrapper).find(r => r.text().includes('create_issue'))
    expect(row).toBeTruthy()
    const link = row!.find('[data-test="tool-calls-link"]')
    expect(link.exists()).toBe(true)
    expect(link.attributes('href')).toContain('/activity')
    expect(link.attributes('href')).toContain('view=calls')
    expect(link.attributes('href')).toContain('tool=github:create_issue')
  })
})

// The Activity-page side of this same regression (a seeded `github:create_issue`
// record actually appearing once `tool=github:create_issue` is split into
// `server=github&tool=create_issue`, not sent verbatim) is covered by
// activity-scope-query-hydration.spec.ts's "?view=calls&tool=<server:tool>"
// case, which mounts Activity.vue directly against its own fixtures.
