import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import type { Server } from '@/types'

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

vi.mock('@/composables/useSecurityScannerStatus', () => ({
  useSecurityScannerStatus: () => ({ hasEnabledScanners: () => true }),
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

function makeServer(overrides: Partial<Server> = {}): Server {
  return {
    name: 'memory',
    protocol: 'stdio',
    enabled: true,
    quarantined: false,
    connected: true,
    connecting: false,
    tool_count: 9,
    ...overrides,
  } as Server
}

function mountCard(server: Server) {
  return mount(ServerCard, {
    props: { server },
    global: {
      plugins: [createPinia()],
      stubs: { RouterLink: RouterLinkStub, 'router-link': RouterLinkStub },
    },
  })
}

beforeEach(() => {
  setActivePinia(createPinia())
})

// Issue #1065 defect 1: the card rendered a green "✓ Clean" scan verdict
// directly above a yellow "Quarantined — needs security review" banner. Both
// statements are individually true, but stacked as peers they tell the operator
// opposite things about whether the server is safe. The scan verdict is about
// CONTENT; quarantine is about REVIEW STATE.
//
// Spec 109 FR-013 folded the old full-width banner into the card's single
// stats line (D15), so this now pins the same "never say settled while
// quarantined" invariant against `[data-test="server-card-security-line"]`
// instead of a standalone banner element.
describe('ServerCard — one security headline per card (#1065)', () => {
  const cleanScan = { status: 'clean' as const }

  it('shows the standalone scan verdict when the server is NOT quarantined', () => {
    const wrapper = mountCard(makeServer({ security_scan: cleanScan } as Partial<Server>))

    const line = wrapper.find('[data-test="server-card-security-line"]')
    expect(line.exists()).toBe(true)
    expect(line.text()).toContain('Clean')
    expect(line.text()).not.toContain('review')
  })

  it('subordinates a clean verdict to the review ask while quarantined', () => {
    const wrapper = mountCard(
      makeServer({ quarantined: true, security_scan: cleanScan } as Partial<Server>)
    )

    const line = wrapper.find('[data-test="server-card-security-line"]')
    expect(line.exists()).toBe(true)
    expect(line.text()).toBe('· Last scan: clean — still needs review')
  })

  it('never says a quarantined server is settled — every verdict still asks for review', () => {
    const cases: Array<[Record<string, unknown>, string]> = [
      [{ status: 'clean' }, 'Last scan: clean — still needs review'],
      [{ status: 'failed' }, 'Last scan could not complete — still needs review'],
      [{ status: 'warnings', finding_counts: { warning: 1 } }, 'Last scan: 1 warning — needs review'],
      [{ status: 'warnings', finding_counts: { warning: 3 } }, 'Last scan: 3 warnings — needs review'],
      [{ status: 'dangerous' }, 'Last scan: dangerous findings — needs review'],
    ]

    for (const [scan, expected] of cases) {
      const wrapper = mountCard(
        makeServer({ quarantined: true, security_scan: scan } as unknown as Partial<Server>)
      )
      const line = wrapper.find('[data-test="server-card-security-line"]')
      expect(line.exists(), `status ${String(scan.status)} produced no line`).toBe(true)
      expect(line.text()).toBe(`· ${expected}`)
      expect(line.text(), `status ${String(scan.status)} reads as settled`).toMatch(/review/)
    }
  })

  it('has nothing to report when there is no scan yet', () => {
    for (const scan of [undefined, { status: 'not_scanned' as const }]) {
      const wrapper = mountCard(
        makeServer({ quarantined: true, security_scan: scan } as unknown as Partial<Server>)
      )
      expect(wrapper.find('[data-test="server-card-security-line"]').exists()).toBe(false)
    }
  })

  it('still offers Review as the primary action while quarantined', () => {
    const wrapper = mountCard(
      makeServer({
        quarantined: true,
        security_scan: cleanScan,
        health: { level: 'healthy', admin_state: 'quarantined', summary: 'Quarantined for review', status: 'needs_review', usable: false, actions: ['approve'], action: 'approve' },
      } as unknown as Partial<Server>)
    )
    const review = wrapper.find('[data-test="server-card-primary-action"]')
    expect(review.exists()).toBe(true)
    expect(review.text()).toContain('Review')
    expect(review.attributes('href')).toContain('/review/')
  })
})
