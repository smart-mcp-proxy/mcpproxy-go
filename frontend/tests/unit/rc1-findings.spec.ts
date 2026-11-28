import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import ReviewScreen from '@/components/ReviewScreen.vue'
import ServerFacts from '@/components/ServerFacts.vue'
import AppFooter from '@/components/AppFooter.vue'
import UsageSummaryStrip from '@/components/UsageSummaryStrip.vue'
import { useSystemStore } from '@/stores/system'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { REVIEW_SELECTION_HINT, REVIEW_SELECTION_HINT_WITH_APPROVED, selectionHint, sortReviewTools } from '@/utils/reviewPresentation'
import api from '@/services/api'
import type { Server } from '@/types'

// Defects found while testing v0.76.0-rc.1 by hand.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
  getActivitySummary: vi.fn(),
} }))

const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }
const tool = { name: 't1', description: 'd', tier: 'read', scan_verdict: 'clean', approval_status: 'pending', default_allowed: true }
function review(captured: boolean, tools: unknown[] = [tool]) {
  return { success: true, data: { server: { name: 'fx', transport: 'stdio', quarantined: true, definitions_captured: captured }, tools: captured ? tools : [] } }
}

describe('review screen (rc.1 findings)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('the fetch outcome follows the review: definitions that arrive after the attempt replace "none captured"', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(false))
    ;(api.discoverServerTools as any).mockResolvedValue({ success: true })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-fetch-definitions"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="review-fetch-outcome"]').text()).toContain('no tool definitions were captured')
    // The core captured them a moment later and announced the change.
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed', { detail: { payload: { server: 'fx' } } })); await flushPromises()
    expect(w.find('[data-test="review-no-definitions"]').exists()).toBe(false)
    const outcome = w.get('[data-test="review-fetch-outcome"]').text()
    expect(outcome).not.toContain('no tool definitions were captured')
    expect(outcome).toContain('Tool definitions were captured')
    w.unmount()
  })

  it('approving before any scan keeps the review on screen and says what to do, without CLI flags', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    ;(api.securityApprove as any).mockResolvedValue({ success: false, error: 'no scan results found; run a scan first or use --force' })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-approve-server"]').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    expect(w.find('.alert-error').exists()).toBe(false)
    expect(w.find('[data-test="review-approve-server"]').exists()).toBe(true)
    const notice = w.get('[data-test="review-approve-notice"]')
    expect(notice.attributes('role')).toBe('alert')
    expect(notice.text()).toContain('Run the scan above')
    expect(notice.text()).not.toContain('--force')
    w.unmount()
  })

  it('a blind approval refused for the same reason points at fetching first: there is no scan button yet', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(false))
    ;(api.securityApprove as any).mockResolvedValue({ success: false, error: 'no scan results found; run a scan first or use --force' })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    expect(w.find('[data-test="review-scan-action"]').exists()).toBe(false)
    ;(w.findAll('dialog')[0].element as HTMLDialogElement).showModal = vi.fn()
    await w.get('[data-test="review-approve-server"]').trigger('click')
    await w.get('[data-test="review-blind-confirm"]').trigger('click'); await flushPromises()
    expect(api.securityApprove).toHaveBeenCalledTimes(1)
    expect(w.get('[data-test="review-approve-notice"]').text()).toContain('Fetch its tool definitions')
    w.unmount()
  })

  it('a refusal that answers after a scan settled is not shown over the new state', async () => {
    ;(api.getServerReview as any).mockResolvedValue(review(true))
    let answer!: (v: unknown) => void
    ;(api.securityApprove as any).mockReturnValue(new Promise(resolve => { answer = resolve }))
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-approve-server"]').trigger('click')
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'fx' } })); await flushPromises()
    answer({ success: false, error: 'no scan results found; run a scan first or use --force' }); await flushPromises()
    expect(w.find('[data-test="review-approve-notice"]').exists()).toBe(false)
    expect(w.find('.alert-error').exists()).toBe(false)
    w.unmount()
  })

  it('fetch feedback does not carry over to a server re-added under the same name', async () => {
    const withInc = (captured: boolean, incarnation: string) => { const r = review(captured); (r.data.server as any).incarnation = incarnation; return r }
    ;(api.getServerReview as any).mockResolvedValue(withInc(false, 'one'))
    ;(api.discoverServerTools as any).mockResolvedValue({ success: false, error: 'boom' })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    await w.get('[data-test="review-fetch-definitions"]').trigger('click'); await flushPromises()
    expect(w.get('[data-test="review-fetch-outcome"]').text()).toContain('boom')
    ;(api.getServerReview as any).mockResolvedValue(withInc(false, 'two'))
    window.dispatchEvent(new CustomEvent('mcpproxy:review-changed', { detail: { payload: { server: 'fx' } } })); await flushPromises()
    expect(w.find('[data-test="review-fetch-outcome"]').exists()).toBe(false)
    w.unmount()
  })

  it('the selection hint also explains tools that start checked because they are already approved', () => {
    expect(selectionHint([tool as any])).toBe(REVIEW_SELECTION_HINT)
    const approvedWrite = { ...tool, name: 'w1', tier: 'write', scan_verdict: 'not_scanned', approval_status: 'approved', default_allowed: true }
    const hint = selectionHint([tool as any, approvedWrite as any])
    expect(hint).toBe(REVIEW_SELECTION_HINT_WITH_APPROVED)
    expect(hint).toContain('already approved and not blocked')
    expect(hint).not.toContain('Only read-only') // the two sentences would contradict each other
    // An approved tool that is blocked does not start checked, so it does not change the hint.
    expect(selectionHint([tool as any, { ...approvedWrite, default_allowed: false, disabled: true } as any])).toBe(REVIEW_SELECTION_HINT)
  })

  it('renders that hint on the screen', async () => {
    const approvedWrite = { ...tool, name: 'w1', tier: 'write', scan_verdict: 'not_scanned', approval_status: 'approved', default_allowed: true }
    ;(api.getServerReview as any).mockResolvedValue(review(true, [tool, approvedWrite]))
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    expect(w.get('[data-test="review-selection-hint"]').text()).toContain('already approved')
    w.unmount()
  })
})

describe('review tool order (rc.1 feedback)', () => {
  const t = (name: string, approval_status: string, extra: Record<string, unknown> = {}) => ({ ...tool, name, approval_status, ...extra })
  const list = [t('b_ok', 'approved'), t('z_pending', 'pending'), t('a_blocked', 'approved', { disabled: true }), t('m_changed', 'changed'), t('c_pending', 'pending')] as any[]

  it('puts tools that need review first: changed, then pending, the rest in the order the server listed them', () => {
    expect(sortReviewTools(list, 'review').map(x => x.name)).toEqual(['m_changed', 'z_pending', 'c_pending', 'b_ok', 'a_blocked'])
    expect(sortReviewTools(list, 'name').map(x => x.name)).toEqual(['a_blocked', 'b_ok', 'c_pending', 'm_changed', 'z_pending'])
    expect(sortReviewTools(list, 'server').map(x => x.name)).toEqual(list.map(x => x.name))
    expect(list.map(x => x.name)).toEqual(['b_ok', 'z_pending', 'a_blocked', 'm_changed', 'c_pending']) // input untouched
  })

  it('the screen opens in that order and offers a Sort control', async () => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    ;(api.getServerReview as any).mockResolvedValue({ success: true, data: { server: { name: 'fx', transport: 'stdio', quarantined: false, definitions_captured: true }, tools: list } })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, ...stubs }); await flushPromises()
    const names = () => w.findAll('article[data-test^="review-tool-"]').map(a => a.attributes('data-test')!.replace('review-tool-', ''))
    expect(names()).toEqual(['m_changed', 'z_pending', 'c_pending', 'b_ok', 'a_blocked'])
    const sort = w.get('[data-test="review-sort"]')
    expect(sort.attributes('aria-label')).toBe('Sort tools')
    expect((sort.element as HTMLSelectElement).value).toBe('review')
    await sort.setValue('name')
    expect(names()).toEqual(['a_blocked', 'b_ok', 'c_pending', 'm_changed', 'z_pending'])
    w.unmount()
  })
})

describe('review: "review them" follows the order on screen', () => {
  it('focuses a held tool that the sort moved to the first page', async () => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    const many = Array.from({ length: 30 }, (_, i) => ({ ...tool, name: `ok_${String(i).padStart(2, '0')}`, approval_status: 'approved' }))
    const tools = [...many, { ...tool, name: 'held_last', approval_status: 'pending' }]
    ;(api.getServerReview as any).mockResolvedValue({ success: true, data: { server: { name: 'fx', transport: 'stdio', quarantined: false, definitions_captured: true }, tools } })
    const w = mount(ReviewScreen, { props: { serverName: 'fx' }, attachTo: document.body, ...stubs }); await flushPromises()
    await (w.vm as any).revealHeld(); await flushPromises()
    const target = w.find('[data-test="review-tool-held_last"]')
    expect(target.exists()).toBe(true) // on the page shown, although it is last in server order
    expect(document.activeElement).toBe(target.element)
    w.unmount()
  })
})

describe('Home usage strip (rc.1 findings)', () => {
  it('a blocked call is counted once: "errors" excludes the calls already shown as "blocked"', async () => {
    setActivePinia(createPinia()); setAvailableFeatures([])
    // error_count also holds a failed security scan here: it is not a call and must not be counted.
    ;(api.getActivitySummary as any).mockResolvedValue({ success: true, data: { call_count: 7, blocked_count: 1, error_count: 2, call_error_count: 2 } })
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/', name: 'home', component: { template: '<div />' } },
      { path: '/usage', name: 'usage', component: { template: '<div />' } },
      { path: '/activity', name: 'activity', component: { template: '<div />' } },
    ] })
    const w = shallowMount(UsageSummaryStrip, { global: { plugins: [router], stubs: { RouterLink: false } } }); await flushPromises()
    expect(w.get('[data-test="usage-strip-blocked"]').text()).toContain('1')
    expect(w.get('[data-test="usage-strip-errors"]').text()).toMatch(/^1\s*errors$/)
  })
})

describe('phone footer (rc.1 findings)', () => {
  it('keeps a release-candidate tag in the short version', () => {
    setActivePinia(createPinia())
    useSystemStore().$patch({ info: { version: 'v0.76.0-rc.1' } as any })
    const w = mount(AppFooter)
    expect(w.get('[data-test="footer-links-compact"]').get('summary').text()).toContain('v0.76.0-rc.1')
  })
  it('still drops a pseudo-version suffix', () => {
    setActivePinia(createPinia())
    useSystemStore().$patch({ info: { version: 'v0.69.1-0.20260926172229-fad8c6791615+dirty' } as any })
    const summary = mount(AppFooter).get('[data-test="footer-links-compact"]').get('summary').text()
    expect(summary).toContain('v0.69.1'); expect(summary).not.toContain('20260926')
  })
})

describe('server facts card (rc.1 findings)', () => {
  it('offers "Full diagnostic" only when there is more than the summary line', () => {
    const base = { name: 'x', protocol: 'stdio', enabled: true, quarantined: false, connected: true, connecting: false, tool_count: 15 }
    const healthy = { ...base, health: { level: 'healthy', admin_state: 'enabled', summary: 'Connected (15 tools)', status: 'ready', usable: true, actions: [] } } as unknown as Server
    expect(mount(ServerFacts, { props: { server: healthy } }).find('[data-test="open-diagnostic"]').exists()).toBe(false)
    const failing = { ...base, connected: false, last_error: 'exit status 1', health: { level: 'unhealthy', admin_state: 'enabled', summary: 'Server exited during startup', status: 'error', usable: false, actions: [] } } as unknown as Server
    expect(mount(ServerFacts, { props: { server: failing } }).get('[data-test="open-diagnostic"]').text()).toBe('Full diagnostic')
    const withDetail = { ...healthy, health: { ...(healthy as any).health, detail: 'Token expires in 2h' } } as unknown as Server
    expect(mount(ServerFacts, { props: { server: withDetail } }).find('[data-test="open-diagnostic"]').exists()).toBe(true)
  })

  it('gives the trust-mode explanation its own full-width line', () => {
    const server = { name: 'x', protocol: 'stdio', enabled: true, quarantined: false, connected: true, connecting: false, tool_count: 1 } as Server
    const cell = mount(ServerFacts, { props: { server } }).get('[data-test="fact-trust"]')
    expect(cell.classes()).toContain('flex-wrap')
    expect(cell.classes()).toContain('col-span-full')
  })
})
