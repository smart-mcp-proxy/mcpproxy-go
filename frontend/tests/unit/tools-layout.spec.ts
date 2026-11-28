import { describe, it, expect, beforeEach, vi } from 'vitest'
import api from '@/services/api'
import { mountTools, tool, LAYOUT_KEY } from './helpers/toolsPage'

// Spec 134 FR-055: Flat list is today's table, the choice is stored and
// switchable by keyboard-operable buttons.
vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn() },
}))

const TOOLS = [
  tool({ name: 'b_tool', server_name: 'zeta', usage: 5, tier: 'write', last_used: '2026-11-01T00:00:00Z' }),
  tool({ name: 'a_tool', server_name: 'alpha', usage: 1, approval_status: 'pending', tier: undefined }),
  tool({ name: 'c_tool', server_name: 'mid', usage: 9, disabled: true }),
]
const names = (w: any) => w.findAll('[data-test="tool-row"] code').map((c: any) => c.text())

describe('Tools layout switch', () => {
  beforeEach(() => vi.clearAllMocks())

  it('defaults to grouped and stores the choice', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS })
    expect(wrapper.find('[data-test="tools-layout-grouped"]').attributes('aria-pressed')).toBe('true')
    await wrapper.find('[data-test="tools-layout-flat"]').trigger('click')
    expect(window.localStorage.getItem(LAYOUT_KEY)).toBe('flat')
    expect(wrapper.find('[data-test="tools-table"]').exists()).toBe(true)
  })

  it('opens flat when flat was stored', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, layout: 'flat' })
    expect(wrapper.find('[data-test="tools-table"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="tools-grouped"]').exists()).toBe(false)
  })

  it('switch buttons are real buttons reachable by Tab', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS })
    for (const v of ['grouped', 'flat']) {
      const el = wrapper.find(`[data-test="tools-layout-${v}"]`).element as HTMLButtonElement
      expect(el.tagName).toBe('BUTTON')
      expect(el.tabIndex).toBe(0)
    }
  })

  it('flat keeps the columns and all seven sort keys in both directions', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, layout: 'flat' })
    const heads = wrapper.findAll('thead th').map(th => th.text().replace(/[↑↓]/g, '').trim())
    expect(heads).toEqual(['', 'Tool', 'Server', 'Description', 'Tier', 'Approval', 'Enabled', 'Usage', 'Last Used'])
    const th = (label: string) => wrapper.findAll('thead th').find(t => t.text().startsWith(label))!
    const cases: [string, string[]][] = [
      ['Tool', ['a_tool', 'b_tool', 'c_tool']],
      ['Server', ['a_tool', 'c_tool', 'b_tool']],
      ['Tier', ['c_tool', 'a_tool', 'b_tool']],
      ['Usage', ['a_tool', 'b_tool', 'c_tool']],
    ]
    for (const [label, asc] of cases) {
      await th(label).trigger('click')
      const first = names(wrapper)
      await th(label).trigger('click')
      const second = names(wrapper)
      expect([first, second], label).toContainEqual(asc)
      expect([first, second], label).toContainEqual([...asc].reverse())
    }
    // Approval: approved rows tie, pending sorts after them and flips with the direction.
    await th('Approval').trigger('click')
    const ap1 = names(wrapper)
    await th('Approval').trigger('click')
    const ap2 = names(wrapper)
    expect([ap1.indexOf('a_tool'), ap2.indexOf('a_tool')].sort()).toEqual([0, 2])
    // Last Used: only b_tool was ever used, so it is first one way and last the other.
    await th('Last Used').trigger('click')
    const lu1 = names(wrapper)
    await th('Last Used').trigger('click')
    expect([lu1.indexOf('b_tool'), names(wrapper).indexOf('b_tool')].sort()).toEqual([0, 2])
    // Enabled: enabled rows first when ascending; the toggle reverses.
    await th('Enabled').trigger('click')
    const e1 = names(wrapper)
    await th('Enabled').trigger('click')
    expect(names(wrapper)).not.toEqual(e1)
    expect(e1.indexOf('c_tool') === 2 || names(wrapper).indexOf('c_tool') === 2).toBe(true)
  })
})
