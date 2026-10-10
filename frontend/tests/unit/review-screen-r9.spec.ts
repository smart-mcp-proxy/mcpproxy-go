import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

// UX-02 cross-review round 9: every async handler on the review screen answers
// only the review session that started it. A late capture, quarantine, rescan
// or stale-approval reload for server A must not clear B's busy state, close
// B's dialog, reload B, or put A's error/notice on B — including after
// A -> B -> A, where the server name matches again.

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { RouterLink: { template: '<a><slot /></a>' } }
type Shape = 'quarantined' | 'uncaptured' | 'approved' | 'stale-scan'
function reviewFor(name: string, shape: Shape) {
  const quarantined = shape !== 'approved'
  const captured = shape !== 'uncaptured'
  return {
    success: true,
    data: {
      server: {
        name, transport: 'stdio', quarantined, definitions_captured: captured,
        ...(shape === 'stale-scan' ? { scan: { verdict: 'clean', coverage: 'stale', unscanned_tools: ['read_b'] } } : {}),
      },
      tools: captured ? [
        { name: 'read_a', description: 'read', tier: 'read', approval_status: quarantined ? 'pending' : 'approved', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: `${name}-h1` },
        { name: 'read_b', description: 'read', tier: 'read', approval_status: quarantined ? 'pending' : 'approved', disabled: false, scan_verdict: 'clean', default_allowed: true, current_hash: `${name}-h2` },
      ] : [],
    },
  }
}
function deferred<T>() { let resolve!: (v: T) => void; const promise = new Promise<T>(r => { resolve = r }); return { promise, resolve } }
const loads = () => (api.getServerReview as any).mock.calls.length

beforeEach(() => {
  vi.clearAllMocks()
  ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
  ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
})

async function navigate(wrapper: any, path: string[]) {
  for (const name of path.slice(1)) { await wrapper.setProps({ serverName: name }); await flushPromises() }
  return path[path.length - 1]
}

const paths = [['A', 'B'], ['A', 'B', 'A']]
const outcomes = [{ label: 'success', res: { success: true } }, { label: 'failure', res: { success: false, error: 'OLD SESSION FAILED' } }]

describe('ReviewScreen late responses for an old review session (UX-02 r9)', () => {
  for (const path of paths) for (const outcome of outcomes) {
    it(`capture: an old ${outcome.label} after ${path.join(' -> ')} leaves the new session's capture in flight`, async () => {
      ;(api.getServerReview as any).mockImplementation(async (name: string) => reviewFor(name, 'uncaptured'))
      const old = deferred<any>(); const current = deferred<any>()
      ;(api.discoverServerTools as any).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
      await flushPromises()
      const fetchBtn = () => wrapper.get('[data-test="review-no-definitions"] button')
      await fetchBtn().trigger('click')
      const name = await navigate(wrapper, path)
      await fetchBtn().trigger('click'); await flushPromises()
      expect((api.discoverServerTools as any).mock.calls.map((c: any[]) => c[0])).toEqual(['A', name])
      const before = loads()

      old.resolve(outcome.res); await flushPromises()
      expect(fetchBtn().attributes('disabled')).toBeDefined()
      expect(fetchBtn().text()).toBe('Fetching…')
      expect(loads()).toBe(before)
      expect(wrapper.text()).not.toContain('OLD SESSION FAILED')
      expect(wrapper.get('[data-test="review-heading"]').text()).toBe(`Review ${name}`)
      await fetchBtn().trigger('click'); await flushPromises()
      expect(api.discoverServerTools).toHaveBeenCalledTimes(2) // no duplicate submit

      current.resolve({ success: true }); await flushPromises()
      expect(loads()).toBe(before + 1)
    })

    it(`quarantine: an old ${outcome.label} after ${path.join(' -> ')} leaves the new session's quarantine in flight`, async () => {
      ;(api.getServerReview as any).mockImplementation(async (name: string) => reviewFor(name, 'approved'))
      const old = deferred<any>(); const current = deferred<any>()
      ;(api.quarantineServer as any).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
      await flushPromises()
      const confirmBtn = () => wrapper.get('[data-test="review-requarantine-confirm"]')
      await confirmBtn().trigger('click')
      const name = await navigate(wrapper, path)
      expect(confirmBtn().attributes('disabled')).toBeUndefined() // the new session starts idle
      await confirmBtn().trigger('click'); await flushPromises()
      expect((api.quarantineServer as any).mock.calls.map((c: any[]) => c[0])).toEqual(['A', name])
      const dialog = wrapper.get('[data-test="review-requarantine-dialog"]').element as any
      const close = vi.fn(); dialog.close = close
      const before = loads()

      old.resolve(outcome.res); await flushPromises()
      expect(confirmBtn().attributes('disabled')).toBeDefined()
      expect(wrapper.get('[data-test="review-requarantine"]').attributes('disabled')).toBeDefined()
      expect(close).not.toHaveBeenCalled()
      expect(loads()).toBe(before)
      expect(wrapper.text()).not.toContain('OLD SESSION FAILED')
      expect(wrapper.get('[data-test="review-heading"]').text()).toBe(`${name} is approved`)
      await confirmBtn().trigger('click'); await flushPromises()
      expect(api.quarantineServer).toHaveBeenCalledTimes(2)

      current.resolve({ success: true }); await flushPromises()
      expect(close).toHaveBeenCalled()
      expect(loads()).toBe(before + 1)
    })

    it(`rescan: an old ${outcome.label} after ${path.join(' -> ')} leaves the new session's rescan in progress`, async () => {
      ;(api.getServerReview as any).mockImplementation(async (name: string) => reviewFor(name, 'stale-scan'))
      const old = deferred<any>(); const current = deferred<any>()
      ;(api.startScan as any).mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
      const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
      await flushPromises()
      await wrapper.get('[data-test="review-scan-action"]').trigger('click')
      const name = await navigate(wrapper, path)
      await wrapper.get('[data-test="review-scan-action"]').trigger('click'); await flushPromises()
      expect((api.startScan as any).mock.calls.map((c: any[]) => c[0])).toEqual(['A', name])
      const checked = wrapper.findAll('input[type="checkbox"]').map(cb => (cb.element as HTMLInputElement).checked)
      const before = loads()

      old.resolve(outcome.res); await flushPromises()
      expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Scan in progress')
      expect(wrapper.text()).not.toContain('OLD SESSION FAILED')
      expect(wrapper.get('[data-test="review-heading"]').text()).toBe(`Review ${name}`)
      expect(wrapper.findAll('input[type="checkbox"]').map(cb => (cb.element as HTMLInputElement).checked)).toEqual(checked)
      expect(loads()).toBe(before)
    })
  }

  for (const path of paths) {
    it(`stale approval: A's out-of-date reload resolved after ${path.join(' -> ')} puts no notice on the new session`, async () => {
      let aLoads = 0
      const reload = deferred<any>()
      ;(api.getServerReview as any).mockImplementation(async (name: string) => {
        if (name === 'A' && ++aLoads === 2) return reload.promise
        return reviewFor(name, 'quarantined')
      })
      ;(api.securityApprove as any).mockResolvedValue({ success: false, error: 'review for A is out of date' })
      const wrapper = mount(ReviewScreen, { props: { serverName: 'A' }, global: { stubs } })
      await flushPromises()
      await wrapper.get('[data-test="review-approve-server"]').trigger('click'); await flushPromises()
      expect(aLoads).toBe(2) // A's stale-response reload is pending
      const name = await navigate(wrapper, path)
      await wrapper.get('[data-test="review-allow-read_a"]').setValue(false)
      const before = loads()

      reload.resolve(reviewFor('A', 'quarantined')); await flushPromises()
      expect(wrapper.find('[data-test="review-stale-notice"]').exists()).toBe(false)
      expect(wrapper.get('[data-test="review-heading"]').text()).toBe(`Review ${name}`)
      expect((wrapper.get('[data-test="review-allow-read_a"]').element as HTMLInputElement).checked).toBe(false)
      expect((wrapper.get('[data-test="review-allow-read_b"]').element as HTMLInputElement).checked).toBe(true)
      expect(loads()).toBe(before)
    })
  }
})
