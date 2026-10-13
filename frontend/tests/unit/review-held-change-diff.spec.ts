import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import type { ReviewTool } from '@/types'
import { summarizeToolChange, changedToolNames, HELD_CHANGE_HINT } from '@/utils/reviewPresentation'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

// Maintainer decision 2026-10-11 ("Hold all changes, 1-click diff"): a changed tool shows what changed, and one click
// approves exactly the reviewed changed tools of the server, bound to the review on screen.

const schema = (props: Record<string, string>, required: string[] = []) => ({ type: 'object', properties: Object.fromEntries(Object.entries(props).map(([k, t]) => [k, { type: t }])), required })
const changed = (name: string, hash: string, extra: Partial<ReviewTool> = {}): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'changed', disabled: false, scan_verdict: 'clean', current_hash: hash, input_schema: schema({}), annotations: { readOnlyHint: true },
  diff: { description: '', input_schema: '', output_schema: '', annotations: '' }, ...extra,
  previous: { description: name, input_schema: schema({}), output_schema: null, annotations: { readOnlyHint: true }, ...extra.previous },
})
const approved = (name: string, hash: string): ReviewTool => ({ name, description: name, tier: 'read', approval_status: 'approved', disabled: false, scan_verdict: 'clean', current_hash: hash })

describe('summarizeToolChange', () => {
  it('reports a description change with before and after', () => {
    const t = changed('a', 'h', { description: 'sends mail', previous: { description: 'reads mail' } })
    expect(summarizeToolChange(t)).toEqual([{ section: 'description', title: 'Description', lines: [{ kind: 'changed', text: 'Description changed', before: 'reads mail', after: 'sends mail' }] }])
  })
  it('reports schema keys added, removed and retyped, and required flips', () => {
    const t = changed('a', 'h', {
      input_schema: schema({ q: 'integer', limit: 'number', path: 'string' }, ['q', 'path']),
      previous: { description: 'a', input_schema: schema({ q: 'string', old: 'string', path: 'string' }, ['q']) },
    })
    const s = summarizeToolChange(t)
    expect(s).toHaveLength(1)
    expect(s[0].section).toBe('input_schema')
    const text = s[0].lines.map(l => `${l.kind}:${l.text}`)
    expect(text).toContain('added:Parameter limit (number)')
    expect(text).toContain('removed:Parameter old (string)')
    expect(text).toContain('changed:Parameter q type string → integer')
    expect(text).toContain('changed:Parameter path is now required')
  })
  it('reports a nested-only schema difference without losing it', () => {
    const t = changed('a', 'h', { input_schema: { type: 'object', properties: { q: { type: 'string', maxLength: 5 } } }, previous: { description: 'a', input_schema: { type: 'object', properties: { q: { type: 'string', maxLength: 500 } } } } })
    expect(summarizeToolChange(t)[0].lines.map(l => l.text)).toEqual(['Parameter q changed'])
  })
  it('reports safety hint flips including set and unset', () => {
    const t = changed('a', 'h', { annotations: { readOnlyHint: false, openWorldHint: true }, previous: { description: 'a', annotations: { readOnlyHint: true, destructiveHint: false } } })
    const lines = summarizeToolChange(t).find(s => s.section === 'annotations')!.lines.map(l => l.text)
    expect(lines).toEqual(['readOnlyHint: true → false', 'destructiveHint: false → not set', 'openWorldHint: not set → true'])
  })
  it('reports output schema changes and nothing for an unchanged tool', () => {
    const t = changed('a', 'h', { output_schema: schema({ id: 'string' }), previous: { description: 'a', output_schema: schema({}) } })
    expect(summarizeToolChange(t).map(s => s.section)).toEqual(['output_schema'])
    expect(summarizeToolChange(changed('b', 'h'))).toEqual([])
  })
  it('lists only changed tools', () => {
    expect(changedToolNames([changed('a', '1'), approved('b', '2'), { ...approved('c', '3'), approval_status: 'pending' }])).toEqual(['a'])
  })
})

const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
const reviewOf = (tools: ReviewTool[], server: Record<string, unknown> = {}) => ({ success: true, data: { server: { name: 'fx', transport: 'stdio', quarantined: false, definitions_captured: true, incarnation: 7, ...server }, tools } })
const mountOn = async () => { const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises(); return w }
const STALE = "tool review for server 'fx' is out of date (definition changed since review: a); nothing was approved — fetch the review again"

describe('summarizeToolChange schema edge cases (Sol r1)', () => {
  it('keeps a root-level change next to a property line', () => {
    const t = changed('a', 'h', {
      input_schema: { ...schema({ extra: 'string' }), additionalProperties: true },
      previous: { description: 'a', input_schema: { ...schema({}), additionalProperties: false }, output_schema: null, annotations: { readOnlyHint: true } },
    })
    const texts = summarizeToolChange(t)[0].lines.map(l => l.text)
    expect(texts).toContain('Parameter extra (string)')
    expect(texts).toContain('Schema setting additionalProperties changed')
  })
  it('reports a removed or added null-valued root setting next to a property line', () => {
    const prev = { description: 'a', input_schema: { ...schema({}), const: null, default: null }, output_schema: null, annotations: { readOnlyHint: true } }
    const removed = changed('a', 'h', { input_schema: schema({ extra: 'string' }), previous: prev })
    const texts = summarizeToolChange(removed)[0].lines.map(l => l.text)
    expect(texts).toContain('Parameter extra (string)')
    expect(texts).toContain('Schema setting const changed')
    expect(texts).toContain('Schema setting default changed')
    const added = changed('a', 'h', { input_schema: { ...schema({}), const: null }, previous: { ...prev, input_schema: schema({}) } })
    expect(summarizeToolChange(added)[0].lines.map(l => l.text)).toEqual(['Schema setting const changed'])
  })
  it('treats inherited names as plain property names', () => {
    const added = changed('a', 'h', { input_schema: schema({ constructor: 'string', toString: 'number' }) })
    expect(summarizeToolChange(added)[0].lines.map(l => l.text)).toEqual(['Parameter constructor (string)', 'Parameter toString (number)'])
    const removed = changed('a', 'h', { previous: { description: 'a', input_schema: schema({ constructor: 'string' }), output_schema: null, annotations: { readOnlyHint: true } } })
    expect(summarizeToolChange(removed)[0].lines.map(l => l.text)).toEqual(['Parameter constructor (string)'])
    expect(summarizeToolChange(removed)[0].lines[0].kind).toBe('removed')
  })
})

describe('ReviewScreen held changes', () => {
  beforeEach(() => { vi.clearAllMocks(); (api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } }); (api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } }) })

  const tools = () => [
    changed('desc', 'h-desc', { description: 'now sends mail', previous: { description: 'reads mail', annotations: { readOnlyHint: true } } }),
    changed('sch', 'h-sch', { input_schema: schema({ extra: 'string' }), previous: { description: 'sch', input_schema: schema({}), annotations: { readOnlyHint: true } } }),
    changed('hint', 'h-hint', { annotations: { readOnlyHint: false }, previous: { description: 'hint', annotations: { readOnlyHint: true } } }),
    approved('stable', 'h-stable'),
  ]

  it('renders a readable change summary for each changed tool and the hold hint', async () => {
    ;(api.getServerReview as any).mockResolvedValue(reviewOf(tools()))
    const w = await mountOn()
    const desc = w.get('[data-test="review-tool-desc"]').text()
    expect(desc).toContain('reads mail'); expect(desc).toContain('now sends mail')
    expect(w.get('[data-test="review-tool-sch"]').text()).toContain('Parameter extra (string)')
    expect(w.get('[data-test="review-tool-hint"]').text()).toContain('readOnlyHint: true → false')
    expect(w.find('[data-test="review-tool-stable"] [data-test="review-change-summary"]').exists()).toBe(false)
    const hint = w.get('[data-test="review-held-hint"]').text()
    expect(hint).toContain('auto_approve_tool_changes'); expect(hint).toBe(HELD_CHANGE_HINT + ' Learn more')
    expect(w.get('[data-test="review-held-hint"] a').attributes('href')).toBe('https://docs.mcpproxy.app/features/security-quarantine')
  })

  it('approves exactly the changed tools, bound to their fingerprints and the incarnation', async () => {
    ;(api.getServerReview as any).mockResolvedValue(reviewOf(tools()))
    ;(api.approveTools as any).mockResolvedValue({ success: true })
    const w = await mountOn()
    const btn = w.get('[data-test="review-approve-update"]')
    expect(btn.text()).toBe('Approve update (3 changed tools)')
    await btn.trigger('click'); await flushPromises()
    expect(api.approveTools).toHaveBeenCalledTimes(1)
    expect(api.approveTools).toHaveBeenCalledWith('fx', ['desc', 'sch', 'hint'], 7, { desc: 'h-desc', sch: 'h-sch', hint: 'h-hint' })
    expect(api.securityApprove).not.toHaveBeenCalled()
  })

  it('on 409 reloads and approves nothing further', async () => {
    ;(api.getServerReview as any).mockResolvedValueOnce(reviewOf(tools())).mockResolvedValue(reviewOf([changed('desc', 'h-desc2'), approved('stable', 's')]))
    ;(api.approveTools as any).mockResolvedValue({ success: false, error: STALE })
    const w = await mountOn()
    await w.get('[data-test="review-approve-update"]').trigger('click'); await flushPromises()
    expect(api.approveTools).toHaveBeenCalledTimes(1)
    expect(api.getServerReview).toHaveBeenCalledTimes(2)
    expect(w.text()).toContain('out of date')
    expect(w.get('[data-test="review-approve-update"]').text()).toBe('Approve update (1 changed tool)')
  })

  it('shows no update button without changed tools or on a quarantined server', async () => {
    ;(api.getServerReview as any).mockResolvedValue(reviewOf([approved('stable', 's')]))
    expect((await mountOn()).find('[data-test="review-approve-update"]').exists()).toBe(false)
    ;(api.getServerReview as any).mockResolvedValue(reviewOf(tools(), { quarantined: true }))
    expect((await mountOn()).find('[data-test="review-approve-update"]').exists()).toBe(false)
  })
})
