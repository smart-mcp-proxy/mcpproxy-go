import { describe, it, expect, beforeEach, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import api from '@/services/api'
import { mountTools, tool } from './helpers/toolsPage'

// Spec 134 FR-056, inventory P6-21..27: Details shows everything the row and
// its detail surface show today.
vi.mock('@/services/api', () => ({
  default: { getGlobalTools: vi.fn(), setToolEnabled: vi.fn(), approveTools: vi.fn(), blockTools: vi.fn() },
}))

const FULL = tool({
  name: 'create_issue', server_name: 'github', description: 'Create an issue.\nSecond line of the full description.',
  tier: 'write', usage: 7, last_used: new Date(Date.now() - 2 * 86_400_000).toISOString(),
  approval_status: 'changed', held_reason: 'scan_findings', held_verdict: 'dangerous', held_signals: ['TPA-2026-0001'],
  annotations: { readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: true },
  input_schema: { type: 'object', properties: { title: { type: 'string' } } },
})

describe('Tools Details', () => {
  beforeEach(() => vi.clearAllMocks())

  async function open(layout: 'grouped' | 'flat', t = FULL, path?: string) {
    const ctx = await mountTools({ api, tools: [t], servers: [], layout, path })
    if (layout === 'grouped') await ctx.wrapper.find('[data-test="tool-details-button"]').trigger('click')
    else await ctx.wrapper.find('[data-test="tool-row"]').trigger('click')
    await flushPromises()
    return ctx
  }

  it.each(['grouped', 'flat'] as const)('%s: shows description, server link, tier, state, usage, last used, approval, hold evidence, annotations, schema', async layout => {
    const { wrapper } = await open(layout)
    const dlg = wrapper.find('[data-test="tool-details-dialog"]')
    expect(dlg.element.tagName).toBe('DIALOG')
    expect(dlg.attributes('open')).toBeDefined()
    expect(dlg.attributes('aria-labelledby')).toBeTruthy()
    expect(dlg.find('[data-test="tool-details-description"]').text()).toContain('Second line of the full description.')
    expect(dlg.find('[data-test="tool-details-server-link"]').attributes('href')).toContain('/servers/github')
    expect(dlg.find('[data-test="tool-details-tier"]').text()).toBe('Write')
    expect(dlg.find('[data-test="tool-details-state"]').text()).toBe('enabled')
    expect(dlg.find('[data-test="tool-calls-link"]').attributes('href')).toContain('/activity')
    expect(dlg.find('[data-test="tool-calls-link"]').attributes('href')).toContain('tool=github:create_issue')
    expect(dlg.find('[data-test="tool-details-last-used"]').text()).toBe('2d ago')
    expect(dlg.find('[data-test="tool-details-approval"]').text()).toContain('Changed, needs review')
    expect(dlg.find('[data-test="tool-held-cue"]').exists()).toBe(true)
    expect(dlg.find('[data-test="tool-review-link"]').attributes('href')).toBe('/review/github?change=changed')
    expect(dlg.find('[data-test="tool-hold-evidence"]').exists()).toBe(true)
    expect(dlg.find('[data-test="tool-details-annotations"]').text()).toContain('destructive')
    expect(dlg.text()).toContain('openWorld')
    expect(dlg.find('pre').text()).toContain('"title"')
  })

  it('says Never used and 0 calls for an unused tool, with no Calls link', async () => {
    const { wrapper } = await open('grouped', tool({ name: 'idle', server_name: 'github' }))
    expect(wrapper.find('[data-test="tool-details-usage"]').text()).toBe('0 calls')
    expect(wrapper.find('[data-test="tool-details-last-used"]').text()).toBe('Never used')
    expect(wrapper.find('[data-test="tool-calls-link"]').exists()).toBe(false)
  })

  it('distinguishes config-denied from disabled', async () => {
    const a = await open('grouped', tool({ config_denied: true, disabled: true }))
    expect(a.wrapper.find('[data-test="tool-details-state"]').text()).toBe('config-denied')
    a.wrapper.unmount()
    const b = await open('grouped', tool({ disabled: true }))
    expect(b.wrapper.find('[data-test="tool-details-state"]').text()).toBe('disabled')
  })

  it('Escape (the native cancel) closes the dialog and focus returns to the Details button', async () => {
    const { wrapper } = await open('grouped')
    const opener = wrapper.find('[data-test="tool-details-button"]').element as HTMLElement
    await wrapper.find('[data-test="tool-details-dialog"]').trigger('cancel')
    await flushPromises()
    expect(wrapper.find('[data-test="tool-details"]').exists()).toBe(false)
    expect(document.activeElement).toBe(opener)
  })

  it('the Close button closes it', async () => {
    const { wrapper } = await open('grouped')
    await wrapper.find('[data-test="tool-details-close"]').trigger('click')
    expect(wrapper.find('[data-test="tool-details"]').exists()).toBe(false)
  })

  it('a flat row opens Details from the keyboard (Enter) and a sortable header sorts on Enter', async () => {
    const { wrapper } = await mountTools({ api, tools: [FULL, tool({ name: 'aaa', server_name: 'a' })], servers: [], layout: 'flat' })
    const row = wrapper.findAll('[data-test="tool-row"]')[0]
    expect(row.attributes('tabindex')).toBe('0')
    await row.trigger('keydown', { key: 'Enter' })
    expect(wrapper.find('[data-test="tool-details"]').exists()).toBe(true)
    await wrapper.find('[data-test="tool-details-close"]').trigger('click')
    const th = wrapper.findAll('thead th').find(t => t.text().startsWith('Tool'))!
    expect(th.attributes('tabindex')).toBe('0')
    await th.trigger('keydown', { key: 'Enter' })
    expect(th.attributes('aria-sort')).toBe('descending')
  })

  it('shows a dash for a tool with no approval status and the hold reason as text', async () => {
    const a = await open('grouped', tool({ name: 'plain', approval_status: '' }))
    expect(a.wrapper.find('[data-test="tool-details-approval"]').text()).toBe('—')
    a.wrapper.unmount()
    const b = await open('grouped')
    expect(b.wrapper.find('[data-test="tool-hold-description"]').text()).toContain('TPA-2026-0001')
  })
})
