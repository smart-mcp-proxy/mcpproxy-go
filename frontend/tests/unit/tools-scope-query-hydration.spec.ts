import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Regression test for the live-QA finding on 109-k-activity-scope-filters:
// "Second, independent instance of the same gap: /tools?server=<name>&tier=<tier>
// does not filter the Tools page on load. Tools.vue's filterServer/filterTier
// are plain refs never hydrated from route.query on mount." This suite mounts
// Tools.vue with an incoming filtered route (unlike tools-tier.spec.ts /
// tools-approval-filter.spec.ts, which only exercise the filters once already
// set through the UI) and asserts the table is narrowed on the very first
// render, matching url-filter-contract.md's Tools-page rows (`server`,
// `tier`/`risk` alias, `status`, `approval` — all client-side on this page).

const TOOLS = [
  { name: 'create_issue', server_name: 'github', description: '', enabled: true, tier: 'write', approval_status: 'approved' },
  { name: 'delete_repo', server_name: 'github', description: '', enabled: true, tier: 'destructive', approval_status: 'approved' },
  { name: 'read_file', server_name: 'filesystem', description: '', enabled: true, tier: 'read', approval_status: 'approved' },
  { name: 'write_file', server_name: 'filesystem', description: '', enabled: true, tier: 'write', approval_status: 'pending', disabled: true },
]

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({
          tools: TOOLS,
          stats: { total: TOOLS.length, enabled: 3, disabled: 1, pending_approval: 1 },
        })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
    },
  }
})

async function mountToolsAt(path: string) {
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

describe('Tools page — URL query hydration (Spec 109-k live-QA regression)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('with no query, shows every tool (baseline)', async () => {
    const wrapper = await mountToolsAt('/tools')
    expect(toolRows(wrapper)).toHaveLength(4)
  })

  it('?server= narrows to that server on first render, and the select reflects it', async () => {
    const wrapper = await mountToolsAt('/tools?server=filesystem')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(2)
    expect(rows.map(r => r.text()).join(' ')).not.toContain('create_issue')

    const select = wrapper.find('[data-test="filter-server"]')
    expect((select.element as HTMLSelectElement).value).toBe('filesystem')
  })

  it('?tier= narrows to that tier, and the select reflects it', async () => {
    const wrapper = await mountToolsAt('/tools?tier=destructive')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('delete_repo')

    const select = wrapper.find('[data-test="filter-tier"]')
    expect((select.element as HTMLSelectElement).value).toBe('destructive')
  })

  // url-filter-contract.md rule 6: "?risk=" stays a query alias for "?tier="
  // for old bookmarks/links.
  it('?risk= (the old alias) applies exactly like ?tier=', async () => {
    const wrapper = await mountToolsAt('/tools?risk=read')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('read_file')

    const select = wrapper.find('[data-test="filter-tier"]')
    expect((select.element as HTMLSelectElement).value).toBe('read')
  })

  it('?status= and ?approval= both narrow on first render', async () => {
    const wrapper = await mountToolsAt('/tools?status=disabled&approval=pending')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('write_file')
  })

  it('combines ?server=&tier= (both narrow together, matching the Tools row "Calls" link\'s sibling deep links)', async () => {
    const wrapper = await mountToolsAt('/tools?server=github&tier=write')
    const rows = toolRows(wrapper)
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('create_issue')
  })

  // zcode review round 1, F3: applyQueryParam only ever SET a filter when its
  // param was present, never cleared it when the param was absent. Vue Router
  // reuses this component across a same-route navigation (no remount), so a
  // later nav to the bare route left the stale filter narrowing the table
  // under a clean URL — and the write-back watch then resurrected the
  // "cleared" param back into the URL as soon as any other filter changed.
  it('navigating from /tools?server= to /tools (same route, no remount) clears the server filter', async () => {
    const wrapper = await mountToolsAt('/tools?server=filesystem')
    expect(toolRows(wrapper)).toHaveLength(2)

    const router = wrapper.vm.$.appContext.config.globalProperties.$router
    await router.push('/tools')
    await flushPromises()

    expect(toolRows(wrapper)).toHaveLength(4)
    const select = wrapper.find('[data-test="filter-server"]')
    expect((select.element as HTMLSelectElement).value).toBe('')

    // Picking a tier afterward must not resurrect `server` into the URL.
    const tierSelect = wrapper.find('[data-test="filter-tier"]')
    await tierSelect.setValue('write')
    await flushPromises()
    expect(router.currentRoute.value.query.server).toBeUndefined()
    expect(router.currentRoute.value.query.tier).toBe('write')
  })
})
