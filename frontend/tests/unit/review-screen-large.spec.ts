import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'
import { clampPage, filterReviewTools, formatLaunchCommand, posixQuote, NO_REVIEW_FILTERS } from '@/utils/reviewPresentation'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

// Like the QA fixture: every 12th tool is destructive, the rest are read and default-allowed.
function tools(n: number) {
  return Array.from({ length: n }, (_, i) => {
    const destructive = i % 12 === 0
    return { name: `${destructive ? 'delete' : 'read'}_customer_record_${String(i).padStart(3, '0')}`, description: `${destructive ? 'Permanently delete' : 'Read'} customer record ${i}`, tier: destructive ? 'destructive' : 'read', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: !destructive }
  })
}
const payload = (n: number, server: Record<string, unknown> = {}) => ({
  success: true,
  data: { server: { name: 'big', transport: 'stdio', command: '/usr/bin/python3', args: ['/opt/fixture.py', '180'], quarantined: true, definitions_captured: true, ...server }, tools: tools(n) },
})
async function mountN(n: number, server: Record<string, unknown> = {}) {
  ;(api.getServerReview as any).mockResolvedValue(payload(n, server))
  const wrapper = mount(ReviewScreen, { props: { serverName: 'big' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}
const articles = (w: any) => w.findAll('article').length
const blockedArg = () => (api.securityApprove as any).mock.calls.at(-1)[2] as string[]

describe('ReviewScreen at scale (UX-03)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.securityApprove as any).mockResolvedValue({ success: true })
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('renders every tool when the list fits one page, and one page of 25 otherwise', async () => {
    expect(articles(await mountN(25))).toBe(25)
    expect(articles(await mountN(1))).toBe(1)
    const w = await mountN(180)
    expect(articles(w)).toBe(25)
    expect(w.get('[data-test="review-range"]').text()).toBe('Showing 1–25 of 180')
    await w.get('[data-test="review-page-size"]').setValue(100)
    expect(articles(w)).toBe(100)
  })

  it('puts the selection hint before the list and the sticky decision bar after it', async () => {
    const w = await mountN(180)
    const html = w.html()
    expect(html.indexOf('review-selection-hint')).toBeLessThan(html.indexOf('<article'))
    expect(html.lastIndexOf('</article>')).toBeLessThan(html.indexOf('review-decision-bar'))
    expect(w.get('[data-test="review-decision-bar"]').classes()).toContain('sticky')
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 165')
    expect(w.get('[data-test="review-blocked-count"]').text()).toBe('Blocked 15')
  })

  it('finds a tool by name or description and filters by tier', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-search"]').setValue('record_179')
    expect(articles(w)).toBe(1)
    expect(w.find('[data-test="review-tool-read_customer_record_179"]').exists()).toBe(true)
    await w.get('[data-test="review-search"]').setValue('Permanently delete')
    expect(w.get('[data-test="review-range"]').text()).toContain('of 15 (filtered from 180)')
    await w.get('[data-test="review-search"]').setValue('')
    await w.get('[data-test="review-filter-tier"]').setValue('destructive')
    expect(w.get('[data-test="review-range"]').text()).toContain('of 15 (filtered from 180)')
    await w.get('[data-test="review-search"]').setValue('no-such-tool')
    expect(w.find('[data-test="review-no-match"]').exists()).toBe(true)
    await w.get('[data-test="review-clear-filters"]').trigger('click')
    expect(w.get('[data-test="review-range"]').text()).toBe('Showing 1–25 of 180')
  })

  it('reaches the last item through the pager', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-page-last"]').trigger('click')
    expect(w.get('[data-test="review-page-label"]').text()).toBe('Page 8 of 8')
    expect(w.find('[data-test="review-tool-read_customer_record_179"]').exists()).toBe(true)
    expect(articles(w)).toBe(5)
  })

  it('keeps off-page and filtered-out selections: approve blocks exactly the unchecked tools of the FULL list', async () => {
    const w = await mountN(180)
    // Block one tool on page 1, then move away from it and filter it out of view.
    await w.get('[data-test="review-allow-read_customer_record_001"]').setValue(false)
    await w.get('[data-test="review-page-last"]').trigger('click')
    await w.get('[data-test="review-filter-tier"]').setValue('destructive')
    // Allow one destructive tool, then clear the filters.
    await w.get('[data-test="review-allow-delete_customer_record_012"]').setValue(true)
    await w.get('[data-test="review-clear-filters"]').trigger('click')
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 165')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    const blocked = blockedArg()
    const expected = tools(180).filter(t => t.default_allowed === false || t.name === 'read_customer_record_001').map(t => t.name).filter(n => n !== 'delete_customer_record_012')
    expect([...blocked].sort()).toEqual(expected.sort())
    expect(blocked).toContain('read_customer_record_001')
    expect(blocked).not.toContain('delete_customer_record_012')
    expect(blocked.length).toBe(15)
  })

  it('a filter alone never approves or blocks anything', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-filter-tier"]').setValue('destructive')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg().length).toBe(15) // the defaults, not "everything visible"
    expect(blockedArg().every(n => n.startsWith('delete_'))).toBe(true)
    await w.get('[data-test="review-filter-tier"]').setValue('read')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg().length).toBe(15)
  })

  it('bulk Allow/Block act on the whole filtered set (all pages) and leave the rest alone', async () => {
    const w = await mountN(180)
    // Unfiltered allow-all is the explicit Approve all action, not a bulk shortcut.
    expect(w.get('[data-test="review-bulk-allow"]').attributes('disabled')).toBeDefined()
    await w.get('[data-test="review-filter-tier"]').setValue('read')
    await w.get('[data-test="review-bulk-block"]').trigger('click')
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 0')
    await w.get('[data-test="review-filter-tier"]').setValue('destructive')
    await w.get('[data-test="review-bulk-allow"]').trigger('click')
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 15')
    await w.get('[data-test="review-clear-filters"]').trigger('click')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg().length).toBe(165)
    expect(blockedArg().every(n => n.startsWith('read_'))).toBe(true)
  })

  it('selection filter shows allowed or blocked from the full selection', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-filter-selection"]').setValue('blocked')
    expect(w.get('[data-test="review-range"]').text()).toContain('of 15 (filtered from 180)')
    await w.get('[data-test="review-filter-selection"]').setValue('allowed')
    expect(w.get('[data-test="review-range"]').text()).toContain('of 165 (filtered from 180)')
  })

  it('a background refresh keeps the selection, the filters and the page', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-allow-read_customer_record_001"]').setValue(false)
    await w.get('[data-test="review-search"]').setValue('record')
    await w.get('[data-test="review-page-next"]').trigger('click')
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    expect(w.get('[data-test="review-page-label"]').text()).toBe('Page 2 of 8')
    expect((w.get('[data-test="review-search"]').element as HTMLInputElement).value).toBe('record')
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 164')
  })

  it('an older refresh that resolves last cannot restore an invalidated allow or replace newer definitions', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-allow-delete_customer_record_012"]').setValue(true)
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 166')
    const older = payload(180)
    const newer = payload(180)
    newer.data.tools = newer.data.tools.map(t => t.name === 'delete_customer_record_012' ? { ...t, description: 'CHANGED upstream', approval_status: 'changed' } : t)
    let resolveOlder!: (v: unknown) => void; let resolveNewer!: (v: unknown) => void
    ;(api.getServerReview as any)
      .mockImplementationOnce(() => new Promise(r => { resolveOlder = r }))
      .mockImplementationOnce(() => new Promise(r => { resolveNewer = r }))
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    resolveNewer(newer); await flushPromises()
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 165')
    resolveOlder(older); await flushPromises()
    expect(w.get('[data-test="review-allowed-count"]').text()).toBe('Allowed 165')
    expect(w.text()).toContain('CHANGED upstream')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg()).toContain('delete_customer_record_012')
    expect(blockedArg().length).toBe(15)
  })

  it('approval is disabled while a refresh is unresolved, so a stale list never decides the block list', async () => {
    const w = await mountN(180)
    const fresh = payload(181)
    fresh.data.tools.push({ name: 'delete_new_pending', description: 'Delete everything', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false } as any)
    let resolveFresh!: (v: unknown) => void
    ;(api.getServerReview as any).mockImplementationOnce(() => new Promise(r => { resolveFresh = r }))
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    const approve = w.get('[data-test="review-approve-server"]')
    expect(approve.attributes('disabled')).toBeDefined()
    expect(w.get('[data-test="review-approve-all"]').attributes('disabled')).toBeDefined()
    await approve.trigger('click')
    await flushPromises()
    expect(api.securityApprove).not.toHaveBeenCalled()
    resolveFresh(fresh); await flushPromises()
    expect(w.get('[data-test="review-approve-server"]').attributes('disabled')).toBeUndefined()
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg()).toContain('delete_new_pending')
  })

  it('a force retry is dropped when a refresh changes the reviewed snapshot (stale block list never reused)', async () => {
    const w = await mountN(180)
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous findings present' })
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    const fresh = payload(181)
    fresh.data.tools.push({ name: 'delete_new_pending', description: 'Delete everything', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false } as any)
    ;(api.getServerReview as any).mockResolvedValueOnce(fresh)
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    const force = w.findAll('button').find(b => b.text() === 'Force approve server')!
    await force.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1) // the stale retry was refused, nothing was sent
    expect(w.get('[data-test="review-stale-force-notice"]').text()).toContain('approve again')
    // A fresh decision derives its block list from the refreshed snapshot, so the new tool stays blocked.
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg()).toContain('delete_new_pending')
  })

  it('a force retry is dropped as soon as a refresh begins, even when the refresh then fails', async () => {
    const w = await mountN(180)
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous findings present' })
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    let resolveFresh!: (v: unknown) => void
    ;(api.getServerReview as any).mockImplementationOnce(() => new Promise(r => { resolveFresh = r }))
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    resolveFresh({ success: false, error: 'network down' }); await flushPromises()
    const force = w.findAll('button').find(b => b.text() === 'Force approve server')
    if (force) { await force.trigger('click'); await flushPromises() }
    expect(api.securityApprove).toHaveBeenCalledTimes(1) // nothing was sent from the stale block list
  })

  it('a force retry is dropped when an allowed pending tool changes definition during a refresh', async () => {
    const w = await mountN(180)
    await w.get('[data-test="review-allow-delete_customer_record_012"]').setValue(true)
    ;(api.securityApprove as any).mockResolvedValueOnce({ success: false, error: 'dangerous findings present' })
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    const changed = payload(180)
    changed.data.tools = changed.data.tools.map(t => t.name === 'read_customer_record_001' ? { ...t, description: 'CHANGED upstream', approval_status: 'changed', default_allowed: false } : t)
    ;(api.getServerReview as any).mockResolvedValueOnce(changed)
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises()
    await w.findAll('button').find(b => b.text() === 'Force approve server')!.trigger('click')
    await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
  })

  it('a late dangerous response does not reopen a force dialog that a refresh already invalidated', async () => {
    const w = await mountN(180)
    const dialog = w.findAll('dialog')[1].element as HTMLDialogElement & { showModal: () => void }
    dialog.showModal = vi.fn()
    let resolveApprove!: (v: unknown) => void
    ;(api.securityApprove as any).mockImplementationOnce(() => new Promise(r => { resolveApprove = r }))
    await w.get('[data-test="review-approve-server"]').trigger('click')
    const fresh = payload(181)
    fresh.data.tools.push({ name: 'delete_new_pending', description: 'Delete everything', tier: 'destructive', approval_status: 'pending', disabled: false, scan_verdict: 'clean', default_allowed: false } as any)
    ;(api.getServerReview as any).mockResolvedValueOnce(fresh)
    window.dispatchEvent(new Event('mcpproxy:review-changed'))
    await flushPromises() // the newer review load settles first
    resolveApprove({ success: false, error: 'dangerous findings present' })
    await flushPromises()
    expect(dialog.showModal).not.toHaveBeenCalled()
    expect(w.get('[data-test="review-stale-force-notice"]').text()).toContain('approve again')
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await flushPromises()
    expect(blockedArg()).toContain('delete_new_pending')
  })

  it('pure helpers: filter semantics and page clamping', () => {
    const t = tools(30)
    const allowed = new Set([t[1].name])
    expect(filterReviewTools(t, NO_REVIEW_FILTERS, allowed)).toHaveLength(30)
    expect(filterReviewTools(t, { ...NO_REVIEW_FILTERS, selection: 'allowed' }, allowed).map(x => x.name)).toEqual([t[1].name])
    expect(filterReviewTools(t, { ...NO_REVIEW_FILTERS, state: 'changed' }, allowed)).toHaveLength(0)
    expect(clampPage(9, 30, 25)).toBe(2)
    expect(clampPage(0, 0, 25)).toBe(1)
  })
})

describe('ReviewScreen launch identity and layout (UX-10, UX-08)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('shows command, each argument, working dir and a POSIX-quoted copyable line from the payload', async () => {
    const w = await mountN(2, { command: 'npx', args: ['-y', '@scope/pkg', '--name', "it's a value", 'multi\nline'], working_dir: '/srv/my app' })
    expect(w.get('[data-test="review-server-identity"]').text()).toContain('/srv/my app')
    const args = w.findAll('[data-test="review-identity-arg"]').map(a => a.text())
    expect(args).toEqual(['-y', '@scope/pkg', '--name', "it's a value", 'multi\nline'])
    expect(w.get('[data-test="review-identity-launch-line"]').text()).toBe("npx -y @scope/pkg --name 'it'\\''s a value' 'multi\nline'")
  })

  it('copies exactly the displayed (already redacted) line', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    const w = await mountN(1, { command: 'uvx', args: ['srv', '--token', '[REDACTED]'] })
    await w.get('[data-test="review-identity-copy"]').trigger('click')
    expect(writeText).toHaveBeenCalledWith("uvx srv --token '[REDACTED]'")
  })

  it('command only when there are no args; remote servers keep the URL', async () => {
    const stdio = await mountN(1, { command: 'node ./fixture.js', args: undefined })
    expect(stdio.find('[data-test="review-identity-args"]').exists()).toBe(false)
    const remote = await mountN(1, { transport: 'http', command: undefined, args: undefined, url: 'https://example.test/mcp' })
    expect(remote.get('[data-test="review-server-identity"]').text()).toContain('https://example.test/mcp')
    expect(remote.find('[data-test="review-identity-launch"]').exists()).toBe(false)
  })

  it('posixQuote and formatLaunchCommand', () => {
    expect(posixQuote('')).toBe("''")
    expect(posixQuote('a b')).toBe("'a b'")
    expect(posixQuote('$(x)')).toBe("'$(x)'")
    expect(formatLaunchCommand('/usr/bin/python3', ['/p/fixture.py', '180'])).toBe('/usr/bin/python3 /p/fixture.py 180')
  })

  it('tier summary is a wrapping grid with all five tiers, not the non-wrapping stats row', async () => {
    const w = await mountN(30)
    const grid = w.get('[data-test="review-tier-counts"]')
    expect(grid.classes()).toContain('grid')
    expect(grid.classes()).not.toContain('stats')
    expect(grid.classes().some(c => c.startsWith('grid-cols-2'))).toBe(true)
    for (const tier of ['read', 'write', 'destructive', 'unannotated', 'unknown']) expect(grid.text()).toContain(tier)
  })
})
