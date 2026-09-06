import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import { useServersStore } from '@/stores/servers'

// Audit F11: the Tools page search matched the WHOLE query as one contiguous
// substring of a single field, so "react documentation" found nothing while
// "documentation" found Context7. The box looks like natural-language discovery
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

  it('drops the "disabled included" claim when a status filter excludes them', async () => {
    const wrapper = await mountTools('/tools?q=kubernetes')
    expect(wrapper.find('[data-test="tools-empty-search"]').text()).toContain('disabled tools included')

    await wrapper.find('[data-test="filter-status"]').setValue('enabled')
    expect(wrapper.find('[data-test="tools-empty-search"]').text()).not.toContain('disabled tools included')
  })
})
