import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import api from '@/services/api'
import { mountTools, tool } from './helpers/toolsPage'

// Spec 134 FR-057: selection works across collapsed sections; the bar states the
// total and the server count; each action keeps its eligibility rule.
vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn() },
}))

const SERVERS = [{ name: 'broken', enabled: true, quarantined: false, tool_count: 2, health: { status: 'error', level: 'unhealthy', usable: false } }]
const TOOLS = [
  tool({ name: 'p1', server_name: 'broken', approval_status: 'pending' }),
  tool({ name: 'ok1', server_name: 'broken' }),
  tool({ name: 'c1', server_name: 'alpha', approval_status: 'changed' }),
  tool({ name: 'ok2', server_name: 'alpha' }),
]

describe('Tools batch bar in grouped mode', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.approveTools as any).mockResolvedValue({ success: true })
    ;(api.blockTools as any).mockResolvedValue({ success: true })
  })

  it('select-all selects the page, collapsed sections included, and says so', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    // `broken` is collapsed (not usable).
    expect(wrapper.find('[data-test="tools-section-broken"] [data-test="tools-section-body"]').isVisible()).toBe(false)
    expect(wrapper.text()).toContain('Select all tools on this page')
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    expect(wrapper.find('[data-test="tools-selected-count"]').text()).toBe('4 tools selected on 2 servers')
  })

  it('Approve applies to the approvable selected tools in a collapsed section, one call per server', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    await wrapper.find('[data-test="batch-approve"]').trigger('click')
    await flushPromises()
    expect(api.approveTools).toHaveBeenCalledTimes(2)
    const calls = (api.approveTools as any).mock.calls.map((c: any[]) => [c[0], c[1]]).sort()
    expect(calls).toEqual([['alpha', ['c1']], ['broken', ['p1']]])
  })

  it('Reject skips already approved tools and a selection of only approved tools disables it', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    await wrapper.find('[data-test="tool-row"] [data-test="tool-select"]').setValue(true) // first row: alpha (usable) in name order
    const rows = wrapper.findAll('[data-test="tool-row"]')
    const okRow = rows.find(r => r.text().includes('ok2'))!
    for (const r of rows) { if (r !== okRow) { const cb = r.find('[data-test="tool-select"]'); if ((cb.element as HTMLInputElement).checked) await cb.setValue(false) } }
    await okRow.find('[data-test="tool-select"]').setValue(true)
    expect((wrapper.find('[data-test="batch-reject"]').element as HTMLButtonElement).disabled).toBe(true)
    expect((wrapper.find('[data-test="batch-approve"]').element as HTMLButtonElement).disabled).toBe(true)
  })

  it('keeps Enable, Disable and Clear selection', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, servers: SERVERS })
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    expect(wrapper.find('[data-test="batch-enable"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="batch-disable"]').exists()).toBe(true)
    await wrapper.find('[data-test="batch-clear"]').trigger('click')
    expect(wrapper.find('[data-test="tools-batch-bar"]').exists()).toBe(false)
  })

  it('names the selection that is not on this page', async () => {
    const many = Array.from({ length: 30 }, (_, i) => tool({ name: `t${String(i).padStart(2, '0')}`, server_name: 'alpha' }))
    const { wrapper } = await mountTools({ api, tools: many, servers: [] })
    await wrapper.find('[data-test="tools-select-all"]').setValue(true)
    await wrapper.find('[data-test="tools-pager"] .join-item:nth-child(4)').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="tools-selected-count"]').text()).toBe('25 tools selected on 1 server (25 on other pages)')
  })
})
