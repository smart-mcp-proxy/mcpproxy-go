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
describe('ServerCard — one security headline per card (#1065)', () => {
  const cleanScan = { status: 'clean' as const }

  it('shows the standalone scan verdict when the server is NOT quarantined', () => {
    const wrapper = mountCard(makeServer({ security_scan: cleanScan } as Partial<Server>))

    expect(wrapper.find('[data-test="security-scan-badge"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="server-card-quarantine"]').exists()).toBe(false)
  })

  it('suppresses the standalone verdict while quarantined', () => {
    const wrapper = mountCard(
      makeServer({ quarantined: true, security_scan: cleanScan } as Partial<Server>)
    )

    expect(wrapper.find('[data-test="security-scan-badge"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="server-card-quarantine"]').exists()).toBe(true)
  })

  it('restates a clean verdict inside the banner, subordinate to the review ask', () => {
    const wrapper = mountCard(
      makeServer({ quarantined: true, security_scan: cleanScan } as Partial<Server>)
    )

    const banner = wrapper.find('[data-test="server-card-quarantine"]')
    expect(banner.text()).toContain('Quarantined — needs security review')

    const note = wrapper.find('[data-test="server-card-quarantine-scan-note"]')
    expect(note.exists()).toBe(true)
    expect(note.text()).toBe('Last scan: clean — still needs review')
    // The note lives inside the banner, not beside it.
    expect(banner.element.contains(note.element)).toBe(true)
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
      const note = wrapper.find('[data-test="server-card-quarantine-scan-note"]')
      expect(note.exists(), `status ${String(scan.status)} produced no note`).toBe(true)
      expect(note.text()).toBe(expected)
      expect(note.text(), `status ${String(scan.status)} reads as settled`).toMatch(/review/)
    }
  })

  it('keeps the banner a one-liner when there is nothing to report', () => {
    for (const scan of [undefined, { status: 'not_scanned' as const }]) {
      const wrapper = mountCard(
        makeServer({ quarantined: true, security_scan: scan } as unknown as Partial<Server>)
      )
      expect(wrapper.find('[data-test="server-card-quarantine"]').exists()).toBe(true)
      expect(wrapper.find('[data-test="server-card-quarantine-scan-note"]').exists()).toBe(false)
    }
  })

  it('still offers Review from the banner', () => {
    const wrapper = mountCard(
      makeServer({ quarantined: true, security_scan: cleanScan } as Partial<Server>)
    )
    const review = wrapper.find('[data-test="server-card-quarantine-review"]')
    expect(review.exists()).toBe(true)
    expect(review.attributes('href')).toContain('tab=security')
  })
})
