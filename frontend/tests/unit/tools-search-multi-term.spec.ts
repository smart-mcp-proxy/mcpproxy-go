import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { useServersStore } from '@/stores/servers'

// Audit F11: the Tools page search matched the WHOLE query as one contiguous
// substring of a single field, so "context7 documentation" found nothing while
// "documentation" found the same tool. The box looks like natural-language discovery
// and behaves like a phrase match, then says only "No matching tools" — with no
// hint of what was searched, or that quarantined servers were never in scope.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({
          tools: [
            {
              name: 'get-library-docs',
              server_name: 'context7',
              description: 'Fetches up-to-date documentation for a library',
              enabled: true,
            },
            {
              name: 'echo',
              server_name: 'everything',
              description: 'Echoes back the input',
              enabled: true,
            },
          ],
          stats: { total: 2, enabled: 2, disabled: 0, pending_approval: 0 },
        })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
    },
  }
})

async function mountTools(target: string) {
  const Tools = (await import('@/views/Tools.vue')).default
  const router = createRouter({
    history: createWebHistory(),
    routes: [
      { path: '/', component: { template: '<div/>' } },
      { path: '/tools', component: Tools },
      { path: '/servers', component: { template: '<div/>' } },
      { path: '/servers/:serverName', component: { template: '<div/>' } },
    ],
  })
  await router.push(target)
  await router.isReady()
  const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('Tools search matches each term separately (audit F11)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('finds a tool when the query terms live in different fields', async () => {
    // "context7" is only in server_name; "documentation" is only in the
    // description. No single field contains the whole phrase, which is exactly
    // the reported failure.
    const wrapper = await mountTools('/tools?q=context7%20documentation')

    expect(wrapper.find('[data-test="tools-table"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('get-library-docs')
    expect(wrapper.text()).not.toContain('Echoes back the input')
  })

  it('still matches a single-word query against one field', async () => {
    const wrapper = await mountTools('/tools?q=documentation')

    expect(wrapper.text()).toContain('get-library-docs')
    expect(wrapper.text()).not.toContain('Echoes back the input')
  })

  it('still excludes a tool when one of the terms matches nothing', async () => {
    const wrapper = await mountTools('/tools?q=context7%20kubernetes')

    expect(wrapper.find('[data-test="tools-table"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('get-library-docs')
    // The headline scenario: a multi-word query that finds nothing must still
    // reach the explanatory empty state, not just render an empty page.
    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('"context7 kubernetes"')
  })
})

describe('Tools search empty state says what was searched (audit F11)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('names the query and the scope that was actually searched', async () => {
    const wrapper = await mountTools('/tools?q=kubernetes')

    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    // The query, quoted, so the user can see what the box actually ran.
    expect(empty.text()).toContain('"kubernetes"')
    // The scope: how many tools, across how many servers.
    expect(empty.text()).toContain('2 tools')
    expect(empty.text()).toContain('2 servers')
    // A concrete next step.
    expect(empty.text()).toContain('fewer words')
  })

  it('names quarantined servers as excluded scope when there are any', async () => {
    const pinia = createPinia()
    setActivePinia(pinia)
    const store = useServersStore()
    store.servers = [
      { name: 'context7', quarantined: false, enabled: true, tool_count: 1 },
      { name: 'everything', quarantined: false, enabled: true, tool_count: 1 },
      { name: 'suspicious', quarantined: true, enabled: true, tool_count: 4 },
    ] as never

    const Tools = (await import('@/views/Tools.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/', component: { template: '<div/>' } },
        { path: '/tools', component: Tools },
        { path: '/servers', component: { template: '<div/>' } },
        { path: '/servers/:serverName', component: { template: '<div/>' } },
      ],
    })
    await router.push('/tools?q=kubernetes')
    await router.isReady()
    const wrapper = mount(Tools, { global: { plugins: [pinia, router] } })
    await flushPromises()
    await flushPromises()

    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('1 quarantined server')
  })

  it('says nothing about quarantine when no server is quarantined', async () => {
    const wrapper = await mountTools('/tools?q=kubernetes')

    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).not.toContain('quarantined')
  })

  it('reports the scope the search really covered, not the whole catalogue', async () => {
    // With another filter narrowing the list, "searched N tools" has to mean the
    // narrowed population — otherwise the empty state overstates what it looked at.
    const wrapper = await mountTools('/tools?q=kubernetes')
    await wrapper.find('[data-test="filter-server"]').setValue('context7')

    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('1 tool across 1 server')
  })

  it('does not blame the query when the other filters left nothing to search', async () => {
    // Click the "Disabled" stat card on an all-enabled catalogue, then search:
    // the search never ran against anything, so "try fewer words" is advice for
    // the wrong control.
    const wrapper = await mountTools('/tools?q=kubernetes')
    await wrapper.find('[data-test="filter-status"]').setValue('disabled')

    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('Nothing was in scope to search')
    expect(empty.text()).not.toContain('try fewer words')
    expect(empty.text()).not.toContain('Searched 0 tools')
  })

  // Round-2 cross-model review. Zero scope has TWO causes and the branch above
  // assumed only one of them. With an EMPTY catalogue — every server
  // quarantined, or none connected, both of which return no tools from
  // GET /api/v1/tools — searching produced "the other active filters excluded
  // every tool ... Clear them to search the full list" while no filter was set
  // at all. Clearing filters cannot fix it, so the advice was both false and
  // unfollowable: the same defect class this file exists to close.
  it('does not blame filters for an empty catalogue when no filter is set', async () => {
    const api = (await import('@/services/api')).default
    vi.mocked(api.getGlobalTools).mockResolvedValueOnce({
      success: true,
      data: {
        tools: [],
        stats: { total: 0, enabled: 0, disabled: 0, pending_approval: 0 },
      },
    } as never)

    const wrapper = await mountTools('/tools?q=kubernetes')
    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('There are no tools in this list to search')
    // The two claims that would be false here.
    expect(empty.text()).not.toContain('active filters excluded')
    expect(empty.text()).not.toContain('try fewer words')
    // And no cause is invented for an unremarkable empty catalogue.
    expect(empty.text()).not.toContain('could not be read')
  })

  // Round-3 cross-model review. The first attempt at the branch above said
  // "no connected server is currently exposing any", which is its own
  // unsupportable claim: GET /api/v1/tools returns success with an empty list
  // and `partial: true` when a server's tool fetch FAILED
  // (internal/httpapi/server.go sets Partial/FailedServers on a genuine fetch
  // error and returns everything it could gather). That is "we could not read
  // them", not "there are none" — the opposite diagnosis, on the same payload.
  it('does not claim an empty catalogue is empty when the fetch partially failed', async () => {
    const api = (await import('@/services/api')).default
    vi.mocked(api.getGlobalTools).mockResolvedValueOnce({
      success: true,
      data: {
        tools: [],
        stats: { total: 0, enabled: 0, disabled: 0, pending_approval: 0 },
        partial: true,
        failed_servers: ['context7'],
      },
    } as never)

    const wrapper = await mountTools('/tools?q=kubernetes')
    const empty = wrapper.find('[data-test="tools-empty-search"]')
    expect(empty.exists()).toBe(true)
    expect(empty.text()).toContain('could not be read')
    // Still never blames the filters, which is the round-2 guarantee.
    expect(empty.text()).not.toContain('active filters excluded')
  })

  it('drops the "disabled included" claim when a status filter excludes them', async () => {
    const wrapper = await mountTools('/tools?q=kubernetes')
    expect(wrapper.find('[data-test="tools-empty-search"]').text()).toContain('disabled tools included')

    await wrapper.find('[data-test="filter-status"]').setValue('enabled')
    expect(wrapper.find('[data-test="tools-empty-search"]').text()).not.toContain('disabled tools included')
  })
})
