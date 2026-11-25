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
    expect(dlg.attributes('role')).toBe('dialog')
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

  it('Escape closes the dialog and focus returns to the Details button', async () => {
    const { wrapper } = await open('grouped')
    const opener = wrapper.find('[data-test="tool-details-button"]').element as HTMLElement
    await wrapper.find('[data-test="tool-details-dialog"]').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(wrapper.find('[data-test="tool-details-dialog"]').exists()).toBe(false)
    expect(document.activeElement).toBe(opener)
  })

  it('the Close button closes it', async () => {
    const { wrapper } = await open('grouped')
    await wrapper.find('[data-test="tool-details-close"]').trigger('click')
    expect(wrapper.find('[data-test="tool-details-dialog"]').exists()).toBe(false)
  })

  it('focus moves into the dialog when it opens', async () => {
    const { wrapper } = await open('grouped')
    expect(document.activeElement).toBe(wrapper.find('[data-test="tool-details-close-x"]').element)
  })
})
