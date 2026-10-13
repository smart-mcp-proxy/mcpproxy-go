import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ReviewScreen from '@/components/ReviewScreen.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({ default: {
  getServerReview: vi.fn(), securityApprove: vi.fn(), securityReject: vi.fn(),
  approveTools: vi.fn(), blockTools: vi.fn(), discoverServerTools: vi.fn(), listScanHistory: vi.fn(), scanAll: vi.fn(), getQueueProgress: vi.fn(), cancelAllScans: vi.fn(), startScan: vi.fn(), quarantineServer: vi.fn(),
} }))

const stubs = { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } }

// Spec 117 PR-1 (FR-026, #1567): a blind approval records the server's tool
// baseline, so the confirmation says later tools wait for review.
describe('ReviewScreen blind-approval copy (Spec 117 FR-026)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ;(api.listScanHistory as any).mockResolvedValue({ success: true, data: { scans: [], total: 0 } })
    ;(api.getQueueProgress as any).mockResolvedValue({ success: true, data: { status: 'idle' } })
  })

  it('tells the operator that tools the server lists later will wait for review', async () => {
    ;(api.getServerReview as any).mockResolvedValue({ success: true, data: {
      server: { name: 'a', transport: 'stdio', quarantined: true, definitions_captured: false }, tools: [],
    } })
    const w = mount(ReviewScreen, { props: { serverName: 'a' }, ...stubs }); await flushPromises()
    const dialog = w.findAll('dialog')[0]
    expect(dialog.text()).toContain('Approve without seeing tools?')
    expect(dialog.text()).toMatch(/lists later will wait for review/i)
  })
})
