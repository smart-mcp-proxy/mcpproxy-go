import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'

// Spec 109 T093 (FR-027, url-filter-contract.md link map "Tools row with
// pending/changed"): a Tools row whose tool still needs review links to
// /review/<server>?change=<state> and shows the shared label, not the raw value.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({
          tools: [
            { name: 'create_issue', server_name: 'github', description: 'a', enabled: true, approval_status: 'pending' },
            { name: 'get_repo', server_name: 'github', description: 'b', enabled: true, approval_status: 'changed' },
            { name: 'list_repos', server_name: 'github', description: 'c', enabled: true, approval_status: 'approved' },
            { name: 'plain', server_name: 'github', description: 'd', enabled: true },
            { name: 'lookup', server_name: 'io.github.owner/repo', description: 'e', enabled: true, approval_status: 'pending' },
          ],
          stats: { total: 5, enabled: 5, disabled: 0, pending_approval: 3 },
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
      { path: '/review', name: 'review', component: { template: '<div/>' } },
      { path: '/review/:server', name: 'review-server', component: { template: '<div/>' } },
    ],
  })
  await router.push('/tools')
  await router.isReady()
  const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
  await flushPromises()
  await flushPromises()
  return { wrapper, router }
}

function rowFor(wrapper: ReturnType<typeof mount>, name: string) {
  const row = wrapper.findAll('[data-test="tool-row"]').find((r) => r.text().includes(name))
  expect(row, `row for ${name}`).toBeTruthy()
  return row!
}

describe('Tools row Review link', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('links a pending tool to /review/<server>?change=pending', async () => {
    const { wrapper } = await mountTools()
    const link = rowFor(wrapper, 'create_issue').find('[data-test="tool-review-link"]')
    expect(link.exists()).toBe(true)
    expect(link.text()).toBe('Review')
    expect(link.attributes('href')).toBe('/review/github?change=pending')
  })

  it('marks a pending or changed tool as held, and an approved one not', async () => {
    const { wrapper } = await mountTools()
    const cue = rowFor(wrapper, 'create_issue').find('[data-test="tool-held-cue"]')
    expect(cue.text()).toBe('Held')
    expect(cue.attributes('title')).toContain('cannot call')
    expect(rowFor(wrapper, 'get_repo').find('[data-test="tool-held-cue"]').exists()).toBe(true)
    expect(rowFor(wrapper, 'list_repos').find('[data-test="tool-held-cue"]').exists()).toBe(false)
  })

  it('links a changed tool to ?change=changed', async () => {
    const { wrapper } = await mountTools()
    const link = rowFor(wrapper, 'get_repo').find('[data-test="tool-review-link"]')
    expect(link.attributes('href')).toBe('/review/github?change=changed')
  })

  it('has no link for an approved tool or one without approval_status', async () => {
    const { wrapper } = await mountTools()
    expect(rowFor(wrapper, 'list_repos').find('[data-test="tool-review-link"]').exists()).toBe(false)
    expect(rowFor(wrapper, 'plain').find('[data-test="tool-review-link"]').exists()).toBe(false)
  })

  it('percent-encodes a slash in the server name', async () => {
    const { wrapper } = await mountTools()
    const link = rowFor(wrapper, 'lookup').find('[data-test="tool-review-link"]')
    expect(link.attributes('href')).toContain('io.github.owner%2Frepo')
  })

  it('does not open the row details drawer when the link is clicked', async () => {
    const { wrapper } = await mountTools()
    const link = rowFor(wrapper, 'create_issue').find('[data-test="tool-review-link"]')
    await link.trigger('click')
    await flushPromises()
    expect(wrapper.text()).not.toContain('Approval:')
  })

  it('renders the shared label in the approval badge, not the raw value', async () => {
    const { wrapper } = await mountTools()
    expect(rowFor(wrapper, 'create_issue').text()).toContain('New, needs review')
    expect(rowFor(wrapper, 'get_repo').text()).toContain('Changed, needs review')
    expect(rowFor(wrapper, 'list_repos').text()).toContain('Approved')
    expect(rowFor(wrapper, 'create_issue').text()).not.toMatch(/\bpending\b/)
  })
})
