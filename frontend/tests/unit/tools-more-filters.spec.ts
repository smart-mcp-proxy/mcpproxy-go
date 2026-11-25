import { describe, it, expect, beforeEach, vi } from 'vitest'
import api from '@/services/api'
import { mountTools, tool } from './helpers/toolsPage'

// Spec 134 FR-056: Status, Tier, Approval stay, under "More filters", with
// their URL parameters and the ?risk= alias.
vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn() },
}))

const TOOLS = [
  tool({ name: 'r', server_name: 'a' }),
  tool({ name: 'w', server_name: 'a', tier: 'write' }),
  tool({ name: 'u', server_name: 'a', tier: undefined }),
  tool({ name: 'd', server_name: 'a', config_denied: true, disabled: true }),
  tool({ name: 'c', server_name: 'a', approval_status: 'changed' }),
]
const shown = (w: any) => w.findAll('[data-test="tool-name"]').map((n: any) => n.text()).sort()

describe('Tools More filters', () => {
  beforeEach(() => vi.clearAllMocks())

  it('visible filters are search, server and Only exceptions; the rest wait behind a button', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS })
    expect(wrapper.find('[data-test="tools-search"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="filter-server"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="tools-only-exceptions"]').isVisible()).toBe(true)
    expect(wrapper.find('[data-test="filter-status"]').isVisible()).toBe(false)
    const toggle = wrapper.find('[data-test="tools-more-filters-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    for (const f of ['filter-status', 'filter-tier', 'filter-approval']) expect(wrapper.find(`[data-test="${f}"]`).isVisible()).toBe(true)
  })

  it.each([
    ['?status=config_denied', ['d']],
    ['?tier=unannotated', ['u']],
    ['?risk=write', ['w']],
    ['?approval=changed', ['c']],
  ])('%s opens the panel and filters', async (qs, expected) => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, path: `/tools${qs}` })
    expect(wrapper.find('[data-test="filter-status"]').isVisible()).toBe(true)
    expect(shown(wrapper)).toEqual(expected)
  })

  it('Only exceptions is a shortcut that does not replace those filters', async () => {
    const { wrapper } = await mountTools({ api, tools: TOOLS, path: '/tools?tier=write' })
    await wrapper.find('[data-test="tools-only-exceptions"]').setValue(true)
    expect(shown(wrapper)).toEqual(['w'])
    await wrapper.find('[data-test="filter-tier"]').setValue('')
    expect(shown(wrapper)).toEqual(['c', 'd', 'u', 'w'])
  })
})
