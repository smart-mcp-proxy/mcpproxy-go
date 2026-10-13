import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(),
  startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const names = ['a', 'b', 'c', 'd', 'e']

function payload(scan: Record<string, unknown> | undefined, opts: { captured?: boolean; verdicts?: Record<string, string> } = {}) {
  const captured = opts.captured ?? true
  return {
    success: true,
    data: {
      server: { name: 'fixture', transport: 'stdio', quarantined: true, definitions_captured: captured, scan },
      tools: captured ? names.map(n => ({
        name: n, description: n, tier: 'read', approval_status: 'pending', disabled: false,
        scan_verdict: opts.verdicts?.[n] ?? 'clean',
      })) : [],
    },
  }
}

async function mountScreen(scan: Record<string, unknown> | undefined, opts: { captured?: boolean; verdicts?: Record<string, string> } = {}) {
  ;(api.getServerReview as any).mockResolvedValue(payload(scan, opts))
  const wrapper = mount(ReviewScreen, { props: { serverName: 'fixture' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
  await flushPromises()
  return wrapper
}

describe('ReviewScreen scan coverage banner', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
    ;(api.startScan as any).mockResolvedValue({ success: true })
    ;(api.discoverServerTools as any).mockResolvedValue({ success: true })
  })

  it('shows a covering clean scan as success with its risk score and coverage', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0, coverage: 'current', tools_scanned: 5 })
    const banner = wrapper.get('[data-test="review-scan-summary"]')
    expect(banner.classes()).toContain('alert-success')
    expect(banner.text()).toContain('Baseline scan: clean · risk 0/100 · covers all 5 tools')
    expect(wrapper.find('[data-test="review-scan-action"]').exists()).toBe(false)
  })

  it('shows covering warnings and dangerous scans as warning and error', async () => {
    const warn = await mountScreen({ verdict: 'warnings', risk_score: 30, coverage: 'current', tools_scanned: 5 })
    expect(warn.get('[data-test="review-scan-summary"]').classes()).toContain('alert-warning')
    expect(warn.get('[data-test="review-scan-summary"]').text()).toContain('risk 30/100')
    const bad = await mountScreen({ verdict: 'dangerous', risk_score: 90, coverage: 'current', tools_scanned: 5 })
    expect(bad.get('[data-test="review-scan-summary"]').classes()).toContain('alert-error')
  })

  it('shows a stale scan as a warning naming the tools, without a risk score, and Rescan starts a scan', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0, coverage: 'stale', tools_scanned: 5, unscanned_tools: ['a', 'b'] }, { verdicts: { a: 'not_scanned', b: 'not_scanned' } })
    const banner = wrapper.get('[data-test="review-scan-summary"]')
    expect(banner.classes()).toContain('alert-warning')
    expect(banner.text()).toContain('Scan out of date: 2 tool definitions changed or were added after the last scan (a, b). Last result: clean.')
    expect(banner.text()).not.toContain('risk')
    expect(banner.text()).not.toContain('Baseline scan: clean')

    await wrapper.get('[data-test="review-scan-action"]').trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('fixture')
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Scan in progress…')

    ;(api.getServerReview as any).mockResolvedValue(payload({ verdict: 'warnings', risk_score: 20, coverage: 'current', tools_scanned: 5 }))
    const loads = (api.getServerReview as any).mock.calls.length
    window.dispatchEvent(new CustomEvent('mcpproxy:scan-settled', { detail: { server_name: 'fixture' } }))
    await flushPromises()
    expect((api.getServerReview as any).mock.calls.length).toBe(loads + 1)
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Baseline scan: warnings')
  })

  it('does not reassure when definitions are not captured and keeps the fetch button in its own block', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0, coverage: 'not_captured' }, { captured: false })
    const banner = wrapper.get('[data-test="review-scan-summary"]')
    expect(banner.classes()).toContain('alert-warning')
    expect(banner.text()).toContain('they have not been captured yet')
    expect(banner.text()).not.toContain('clean')
    expect(banner.text()).not.toContain('risk')
    expect(wrapper.find('[data-test="review-scan-action"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="review-no-definitions"] button').text()).toContain('Fetch tool definitions')
  })

  it('shows a scan that exported no tools as a warning with Rescan', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0, coverage: 'tools_not_scanned' })
    const banner = wrapper.get('[data-test="review-scan-summary"]')
    expect(banner.classes()).toContain('alert-warning')
    expect(banner.text()).toContain('The last scan did not analyse tool definitions (0 exported).')
    expect(banner.text()).not.toContain('risk')
    expect(wrapper.get('[data-test="review-scan-action"]').text()).toBe('Rescan')
  })

  it('offers Scan now when there is no completed scan', async () => {
    const wrapper = await mountScreen({ verdict: 'not_scanned', coverage: 'none' })
    expect(wrapper.get('[data-test="review-scan-summary"]').classes()).toContain('alert-warning')
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Not scanned yet.')
    await wrapper.get('[data-test="review-scan-action"]').trigger('click')
    await flushPromises()
    expect(api.startScan).toHaveBeenCalledWith('fixture')
  })

  it('treats a payload without coverage like no completed scan', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0 })
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Not scanned yet.')
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).not.toContain('clean')
  })

  it('shows a running scan as info without an action', async () => {
    const wrapper = await mountScreen({ verdict: 'not_scanned', coverage: 'scanning' })
    expect(wrapper.get('[data-test="review-scan-summary"]').classes()).toContain('alert-info')
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Scan in progress…')
    expect(wrapper.find('[data-test="review-scan-action"]').exists()).toBe(false)
  })

  it('renders each tool badge from the payload scan_verdict', async () => {
    const wrapper = await mountScreen({ verdict: 'clean', risk_score: 0, coverage: 'stale', tools_scanned: 5, unscanned_tools: ['b'] }, { verdicts: { a: 'clean', b: 'not_scanned', c: 'warnings' } })
    expect(wrapper.get('[data-test="review-tool-a"]').text()).toContain('clean')
    expect(wrapper.get('[data-test="review-tool-b"]').text()).toContain('not_scanned')
    expect(wrapper.get('[data-test="review-tool-c"]').text()).toContain('warnings')
  })

  it('does not carry a rescan started on one server over to the next server', async () => {
    const noScan = (name: string) => ({
      success: true,
      data: {
        server: { name, transport: 'stdio', quarantined: true, definitions_captured: true, scan: { verdict: 'not_scanned', coverage: 'none' } },
        tools: [{ name: 'a', description: 'a', tier: 'read', approval_status: 'pending', disabled: false, scan_verdict: 'not_scanned' }],
      },
    })
    ;(api.getServerReview as any).mockImplementation(async (name: string) => noScan(name))
    const wrapper = mount(ReviewScreen, { props: { serverName: 'alpha' }, global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
    await flushPromises()
    await wrapper.get('[data-test="review-scan-action"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Scan in progress…')

    await wrapper.setProps({ serverName: 'beta' })
    await flushPromises()
    expect(wrapper.get('[data-test="review-scan-summary"]').text()).toContain('Not scanned yet.')
    expect(wrapper.find('[data-test="review-scan-action"]').exists()).toBe(true)
  })
})
