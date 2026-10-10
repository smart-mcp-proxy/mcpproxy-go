import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import {
  APPROVE_ALL_HINT,
  REVIEW_SELECTION_HINT,
  approveAllLabel,
  approveLabel,
  initialSelection,
  mergeSelection,
  type SelectionChoice,
} from '@/utils/reviewPresentation'
import type { ReviewTool } from '@/types'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const tool = (name: string, extra: Partial<ReviewTool> = {}): ReviewTool => ({
  name, description: name, tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: true, ...extra,
})

describe('review selection helpers (D43)', () => {
  it('initialSelection reads default_allowed and counts a missing field as false', () => {
    const tools = [
      tool('read_a'),
      tool('write_a', { tier: 'write', default_allowed: false }),
      tool('old_core', { default_allowed: undefined }),
    ]
    expect(initialSelection(tools)).toEqual(['read_a'])
  })

  it('mergeSelection keeps an explicit uncheck', () => {
    const tools = [tool('read_a'), tool('read_b')]
    const choices = new Map<string, SelectionChoice>([['read_a', { allowed: false, tool: tools[0] }]])
    expect(mergeSelection(tools, choices)).toEqual(['read_b'])
  })

  it('mergeSelection keeps an explicit check only while the payload is unchanged', () => {
    const before = tool('write_a', { tier: 'write', default_allowed: false })
    const choices = new Map<string, SelectionChoice>([['write_a', { allowed: true, tool: before }]])
    expect(mergeSelection([structuredClone(before)], choices)).toEqual(['write_a'])
    // The definition, the verdict or the tier changed after the click: back to the default.
    expect(mergeSelection([{ ...before, description: 'now does more' }], choices)).toEqual([])
    expect(mergeSelection([{ ...before, scan_verdict: 'warnings' }], choices)).toEqual([])
    expect(mergeSelection([{ ...before, tier: 'destructive' }], choices)).toEqual([])
  })

  it('mergeSelection ignores choices for tools that are gone', () => {
    const choices = new Map<string, SelectionChoice>([['gone', { allowed: true, tool: tool('gone') }]])
    expect(mergeSelection([tool('read_a')], choices)).toEqual(['read_a'])
  })

  it('labels name the exact count', () => {
    expect(approveLabel(3, 9, true)).toBe('Approve server (3 of 9 tools)')
    expect(approveLabel(0, 9, true)).toBe('Approve server (0 of 9 tools)')
    expect(approveLabel(1, 1, true)).toBe('Approve server (1 of 1 tool)')
    expect(approveLabel(0, 0, true)).toBe('Approve without seeing tools')
    expect(approveLabel(0, 5, false)).toBe('Approve without seeing tools')
    expect(approveAllLabel(9)).toBe('Approve all (9 tools)')
    expect(approveAllLabel(1)).toBe('Approve all (1 tool)')
  })

  it('the hint is the shared sentence', () => {
    expect(REVIEW_SELECTION_HINT).toBe('Only read-only tools with a clean scan start checked. Unchecked tools stay blocked after approval until you enable them on the Tools tab.')
  })
})

const payload = (tools: ReviewTool[], definitionsCaptured = true) => ({
  success: true,
  data: {
    server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: definitionsCaptured },
    tools,
  },
})

const fixtureTools = () => [
  tool('read_file'),
  tool('read_more'),
  tool('write_file', { tier: 'write', default_allowed: false }),
  tool('remove_file', { tier: 'destructive', approval_status: 'changed', scan_verdict: 'warnings', default_allowed: false }),
  tool('implicit', { tier: 'unannotated', scan_verdict: 'not_scanned', default_allowed: false }),
]

async function mountScreen(tools: ReviewTool[] = fixtureTools(), definitionsCaptured = true) {
  ;(api.getServerReview as any).mockResolvedValue(payload(tools, definitionsCaptured))
  const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

const checked = (wrapper: Awaited<ReturnType<typeof mountScreen>>, name: string) =>
  (wrapper.get(`[data-test="review-allow-${name}"]`).element as HTMLInputElement).checked

describe('ReviewScreen default selection (D43)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.discoverServerTools as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('starts only the core-selected tools checked and names the exact count', async () => {
    const wrapper = await mountScreen()
    expect(checked(wrapper, 'read_file')).toBe(true)
    expect(checked(wrapper, 'read_more')).toBe(true)
    expect(checked(wrapper, 'write_file')).toBe(false)
    expect(checked(wrapper, 'remove_file')).toBe(false)
    expect(checked(wrapper, 'implicit')).toBe(false)
    expect(wrapper.get('[data-test="review-approve-server"]').text()).toBe('Approve server (2 of 5 tools)')
    expect(wrapper.get('[data-test="review-selection-hint"]').text()).toBe(REVIEW_SELECTION_HINT)
  })

  it('an older core without default_allowed fails closed', async () => {
    const tools = fixtureTools().map(t => ({ ...t, default_allowed: undefined }))
    const wrapper = await mountScreen(tools)
    for (const t of tools) expect(checked(wrapper, t.name)).toBe(false)
    expect(wrapper.get('[data-test="review-approve-server"]').text()).toBe('Approve server (0 of 5 tools)')
    expect(wrapper.find('[data-test="review-approve-all"]').exists()).toBe(true)
  })

  it('the primary button sends the unchecked tools as the block list', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, ['write_file', 'remove_file', 'implicit'])
  })

  it('Approve all sends an empty block list', async () => {
    const wrapper = await mountScreen()
    expect(wrapper.get('[data-test="review-approve-all"]').text()).toBe('Approve all (5 tools)')
    expect(wrapper.get('[data-test="review-approve-all"]').attributes('title')).toBe(APPROVE_ALL_HINT)
    expect(APPROVE_ALL_HINT).toContain('stay blocked')
    await wrapper.get('[data-test="review-approve-all"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledWith('fixture', false, [])
  })

  it('hides Approve all when everything is already selected or nothing is captured', async () => {
    const everything = await mountScreen([tool('read_a'), tool('read_b')])
    expect(everything.find('[data-test="review-approve-all"]').exists()).toBe(false)
    const blind = await mountScreen([], false)
    expect(blind.find('[data-test="review-approve-all"]').exists()).toBe(false)
    expect(blind.get('[data-test="review-approve-server"]').text()).toBe('Approve without seeing tools')
    expect(blind.find('[data-test="review-selection-hint"]').exists()).toBe(false)
  })

  it('the forced retry re-sends the block list of the attempt that triggered it', async () => {
    for (const trigger of ['review-approve-server', 'review-approve-all']) {
      vi.clearAllMocks()
      ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous baseline finding' }).mockResolvedValueOnce({ success: true })
      const wrapper = await mountScreen()
      const forceDialog = wrapper.findAll('dialog')[1].element as HTMLDialogElement & { showModal: () => void }
      forceDialog.showModal = vi.fn()
      await wrapper.get(`[data-test="${trigger}"]`).trigger('click')
      await flushPromises()
      expect(forceDialog.showModal).toHaveBeenCalled()
      await wrapper.findAll('dialog')[1].get('button.btn-error').trigger('click')
      await flushPromises()
      const expected = trigger === 'review-approve-all' ? [] : ['write_file', 'remove_file', 'implicit']
      expect(api.securityApprove).toHaveBeenNthCalledWith(1, 'fixture', false, expected)
      expect(api.securityApprove).toHaveBeenNthCalledWith(2, 'fixture', true, expected)
    }
  })

  it('navigating to another server drops the force retry state of the previous one', async () => {
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous baseline finding' }).mockResolvedValueOnce({ success: true })
    const wrapper = await mountScreen()
    const forceDialog = wrapper.findAll('dialog')[1].element as HTMLDialogElement & { showModal: () => void; close: () => void }
    forceDialog.showModal = vi.fn()
    forceDialog.close = vi.fn()
    await wrapper.get('[data-test="review-approve-all"]').trigger('click')
    await flushPromises()
    expect(forceDialog.showModal).toHaveBeenCalled()
    // Component reuse: /review/fixture -> /review/other while the force dialog is open.
    ;(api.getServerReview as any).mockResolvedValue(payload([tool('read_x'), tool('write_x', { tier: 'write', default_allowed: false })]))
    await wrapper.setProps({ serverName: 'other' })
    await flushPromises()
    expect(forceDialog.close).toHaveBeenCalled()
    await wrapper.findAll('dialog')[1].get('button.btn-error').trigger('click')
    await flushPromises()
    // Never fail open: the old attempt's block list is gone and a force click without a fresh decision is refused.
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    // A fresh approval derives the new server's own default block list, not [] from the old attempt.
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenNthCalledWith(2, 'other', false, ['write_x'])
  })

  it('a late dangerous 409 for the previous server does not open the force dialog on the next one', async () => {
    let resolveA: (v: { success: boolean; error?: string }) => void = () => {}
    ;(api.securityApprove as any).mockReturnValueOnce(new Promise(r => { resolveA = r }))
    const wrapper = await mountScreen()
    const forceDialog = wrapper.findAll('dialog')[1].element as HTMLDialogElement & { showModal: () => void; close: () => void }
    forceDialog.showModal = vi.fn()
    forceDialog.close = vi.fn()
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    // Component reuse while the approve call for "fixture" is still in flight.
    ;(api.getServerReview as any).mockResolvedValue(payload([tool('read_x'), tool('write_x', { tier: 'write', default_allowed: false })]))
    await wrapper.setProps({ serverName: 'other' })
    await flushPromises()
    resolveA({ success: false, error: 'dangerous baseline finding' })
    await flushPromises()
    expect(forceDialog.showModal).not.toHaveBeenCalled()
    expect(wrapper.text()).not.toContain('dangerous baseline finding')
    // The new server's own buttons are usable again and no force retry is armed.
    expect((wrapper.get('[data-test="review-approve-server"]').element as HTMLButtonElement).disabled).toBe(false)
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: true })
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenLastCalledWith('other', false, ['write_x'])
  })

  it('a late rescan failure for the previous server does not set an error on the next one', async () => {
    let resolveScan: (v: { success: boolean; error?: string }) => void = () => {}
    ;(api.startScan as any).mockReturnValueOnce(new Promise(r => { resolveScan = r }))
    const wrapper = await mountScreen()
    ;(wrapper.vm as any).rescan()
    await flushPromises()
    ;(api.getServerReview as any).mockResolvedValue(payload([tool('read_x')]))
    await wrapper.setProps({ serverName: 'other' })
    await flushPromises()
    resolveScan({ success: false, error: 'scan exploded for fixture' })
    await flushPromises()
    expect(wrapper.text()).not.toContain('scan exploded for fixture')
  })

  it('a review-changed reload keeps an explicit uncheck and an explicit check of an unchanged tool', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-allow-read_file"]').setValue(false)
    await wrapper.get('[data-test="review-allow-write_file"]').setValue(true)
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    await flushPromises()
    expect(checked(wrapper, 'read_file')).toBe(false)
    expect(checked(wrapper, 'write_file')).toBe(true)
    expect(wrapper.get('[data-test="review-approve-server"]').text()).toBe('Approve server (2 of 5 tools)')
  })

  it('a reload drops an explicit check when the tool changed underneath it', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-allow-write_file"]').setValue(true)
    const changed = fixtureTools().map(t => t.name === 'write_file' ? { ...t, description: 'now also deletes things' } : t)
    ;(api.getServerReview as any).mockResolvedValue(payload(changed))
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    await flushPromises()
    expect(checked(wrapper, 'write_file')).toBe(false)
  })

  it('choices are cleared after a successful approval', async () => {
    const wrapper = await mountScreen()
    await wrapper.get('[data-test="review-allow-read_file"]').setValue(false)
    await wrapper.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed'))
    await flushPromises()
    expect(checked(wrapper, 'read_file')).toBe(true)
  })
})
