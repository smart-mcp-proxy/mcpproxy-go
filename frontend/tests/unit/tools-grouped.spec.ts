import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import api from '@/services/api'
import { mountTools, tool } from './helpers/toolsPage'

// Spec 134 PR 6 (FR-055, FR-056): the grouped default.
vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn() },
}))

const SERVERS = [
  { name: 'alpha', enabled: true, quarantined: false, tool_count: 2, health: { status: 'ready', level: 'healthy', usable: true } },
  { name: 'broken', enabled: true, quarantined: false, tool_count: 1, health: { status: 'error', level: 'unhealthy', usable: false } },
  { name: 'off', enabled: false, quarantined: false, tool_count: 1, health: { status: 'disabled', level: 'healthy', usable: false } },
  // A fleet member with no returned tool: it must never get a section.
  { name: 'secret-fleet-server', enabled: true, quarantined: false, tool_count: 9, health: { status: 'ready', level: 'healthy', usable: true } },
]
const TOOLS = [
  tool({ name: 'read_a', server_name: 'alpha' }),
  tool({ name: 'write_a', server_name: 'alpha', tier: 'write', last_used: new Date(Date.now() - 3 * 3600_000).toISOString(), usage: 4 }),
  tool({ name: 'broken_t', server_name: 'broken' }),
  tool({ name: 'off_t', server_name: 'off', disabled: true }),
]

describe('Tools grouped mode', () => {
  beforeEach(() => vi.clearAllMocks())

  it('groups by server, ordered not usable, usable, disabled, and only from returned tools', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    const names = wrapper.findAll('[data-test="tools-section-server-link"]').map(a => a.text())
    expect(names).toEqual(['broken', 'alpha', 'off'])
    expect(wrapper.text()).not.toContain('secret-fleet-server')
    expect(wrapper.find('[data-test="tools-table"]').exists()).toBe(false)
  })

  it('header shows server link, status pill and returned count', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    const alpha = wrapper.find('[data-test="tools-section-alpha"]')
    expect(alpha.find('[data-test="tools-section-server-link"]').attributes('href')).toContain('/servers/alpha')
    expect(alpha.find('[data-test="status-pill"]').text()).toBe('Online')
    expect(alpha.find('[data-test="tools-section-count"]').text()).toContain('2 tools')
  })

  it('shows no pill when the caller has no health for a server, and still groups', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: [] })
    expect(wrapper.findAll('[data-test="tools-section-server-link"]')).toHaveLength(3)
    expect(wrapper.find('[data-test="status-pill"]').exists()).toBe(false)
  })

  it('not usable and disabled sections start collapsed, usable ones open; toggling works by button', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    const body = (s: string) => wrapper.find(`[data-test="tools-section-${s}"] [data-test="tools-section-body"]`)
    expect(body('alpha').isVisible()).toBe(true)
    expect(body('broken').isVisible()).toBe(false)
    expect(body('off').isVisible()).toBe(false)
    const toggle = wrapper.find('[data-test="tools-section-broken"] [data-test="tools-section-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(body('broken').isVisible()).toBe(true)
    expect(toggle.attributes('aria-expanded')).toBe('true')
  })

  it('a search opens collapsed sections that hold a match', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    await wrapper.find('[data-test="tools-search"]').setValue('broken_t')
    const body = wrapper.find('[data-test="tools-section-broken"] [data-test="tools-section-body"]')
    expect(body.isVisible()).toBe(true)
  })

  it('an ordinary row has no label; exceptions are labelled; last used and Details are on the row', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    const rows = wrapper.findAll('[data-test="tool-row"]')
    const read = rows.find(r => r.text().includes('read_a'))!
    expect(read.find('[data-test="tool-exceptions"]').text()).toBe('')
    expect(read.text()).toContain('Last used: never')
    const write = rows.find(r => r.text().includes('write_a'))!
    expect(write.text()).toContain('Writes')
    expect(write.text()).toContain('3h ago')
    expect(write.find('[data-test="tool-details-button"]').attributes('aria-label')).toBe('Details for write_a')
    expect(rows.find(r => r.text().includes('off_t'))!.text()).toContain('Disabled')
  })

  it('a changed tool links its label to that server review', async () => {
    const { wrapper } = await mountTools({
      api, servers: SERVERS,
      tools: [tool({ name: 'ch', server_name: 'alpha', approval_status: 'changed' }), tool({ name: 'nw', server_name: 'alpha', approval_status: 'pending' })],
    })
    expect(wrapper.find('[data-test="tool-exception-changed"]').attributes('href')).toBe('/review/alpha?change=changed')
    expect(wrapper.find('[data-test="tool-exception-pending"]').attributes('href')).toBe('/review/alpha?change=pending')
    expect(wrapper.find('[data-test="tool-exception-changed"]').text()).toBe('Changed, needs review')
  })

  it('Only exceptions keeps rows with a label', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    await wrapper.find('[data-test="tools-only-exceptions"]').setValue(true)
    const names = wrapper.findAll('[data-test="tool-name"]').map(n => n.text()).sort()
    expect(names).toEqual(['off_t', 'write_a'])
  })

  it('keeps statistics and the Needs review link', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS, stats: { total: 4, enabled: 3, disabled: 1, pending_approval: 2 } })
    expect(wrapper.find('[data-test="stat-total"]').text()).toContain('4')
    expect(wrapper.find('[data-test="stat-pending"]').attributes('href')).toBe('/review')
  })

  it('shows the partial-results warning and the header count', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS, partial: true, failed: ['flaky'] })
    expect(wrapper.text()).toContain('Partial results')
    expect(wrapper.text()).toContain('flaky')
    expect(wrapper.find('[data-test="tools-refresh"]').exists()).toBe(true)
  })

  it('empty state offers its recovery actions', async () => {
    const { wrapper } = await mountTools({ api, tools: [], servers: SERVERS })
    expect(wrapper.text()).toContain('No tools available')
    expect(wrapper.text()).toContain('Manage Servers')
  })

  it('pages the ordered list and says the range; a section split by the page says so', async () => {
    const many = Array.from({ length: 30 }, (_, i) => tool({ name: `t${String(i).padStart(2, '0')}`, server_name: 'alpha' }))
    const { wrapper } = await mountTools({ api, tools: many, servers: SERVERS })
    expect(wrapper.find('[data-test="tools-pager"]').text()).toContain('Showing 1–25 of 30')
    expect(wrapper.find('[data-test="tools-section-count"]').text()).toContain('30 tools · 25 on this page')
    await flushPromises()
  })
})
