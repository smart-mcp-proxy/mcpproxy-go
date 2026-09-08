import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import FlaggedToolsPanel from '@/components/FlaggedToolsPanel.vue'
import FindingChip from '@/components/FindingChip.vue'
import ServerCard from '@/components/ServerCard.vue'
import type { Server } from '@/types'

// UX audit F09. One Server Detail render said BOTH of these, about the same two
// findings:
//
//   FlaggedToolsPanel  "Informational — these findings do not block the tool."
//   Approve dialog     "Force-approving bypasses the scanner gate."
//
// The second is what actually happened: POST /servers/{id}/security/approve
// returned 409 "server has 2 dangerous (hard-tier) finding(s)" and Force
// Approve was the only way past it. The first is false whenever the finding is
// hard-tier and the server is quarantined — which is exactly when the panel is
// most likely to be read — and false again under trust_mode: scan, where a
// non-clean verdict holds the individual tool
// (internal/runtime/tool_quarantine.go, held_reason "scan_findings").
//
// Provenance of the false sentence: docs/research/tpa-scanner-inline-ui-design.md
// non-goal 9 read internal/server/scan_informational.go's "drives NO gating
// whatsoever" as a claim about the FINDINGS. It is a claim about the TRIGGER —
// the informational scan neither quarantines nor auto-approves — and the same
// comment says the verdict is "stored through the normal scan-summary path",
// which is precisely what ApproveServer then blocks on.
//
// The fix is the smallest one that leaves nothing false on screen: the panel
// stops making a claim about blocking that it has no state to support, and says
// instead the one thing that is unconditionally true — a hard-tier finding
// blocks SERVER APPROVAL. The dialogs name that same gate rather than an
// undefined "scanner gate". Making the panel's sentence conditional on
// quarantine state and trust mode (a new prop, three sentences) is a copy
// decision for the maintainer and is deliberately NOT taken here.

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

const GROUPS = [
  {
    tool: 'resolve-library-id',
    level: 'dangerous' as const,
    ruleIds: ['detect.shadowing.cross_server'],
    findings: [
      {
        rule_id: 'detect.shadowing.cross_server',
        description: 'Tool "resolve-library-id" clones server "context7"\'s tool.',
        threat_level: 'dangerous',
      },
    ],
  },
]

function mountPanel() {
  return mount(FlaggedToolsPanel, {
    props: { groups: GROUPS as never, presentTools: ['resolve-library-id'] },
  })
}

function mountQuarantinedCard(scanned = true) {
  const server = {
    name: 'context7-docs',
    protocol: 'http',
    url: 'https://mcp.context7.com/mcp',
    enabled: true,
    quarantined: true,
    connected: false,
    connecting: false,
    tool_count: 0,
    health: { action: 'approve' },
    security_scan: scanned
      ? {
          last_scan_at: '2026-09-05T10:00:00Z',
          status: 'dangerous',
          finding_counts: { dangerous: 2, warning: 0, info: 0, total: 2 },
        }
      : undefined,
  } as unknown as Server

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
  vi.clearAllMocks()
})

describe('scanner gate wording — the panel and the dialog agree (F09)', () => {
  it('the flagged-tools panel no longer claims the findings do not block', () => {
    const note = mountPanel().get('[data-test="flagged-tools-informational"]')
    expect(note.text()).not.toContain('do not block')
    expect(note.text()).not.toContain('Informational')
  })

  it('the flagged-tools panel names the gate a hard-tier finding actually holds', () => {
    const note = mountPanel().get('[data-test="flagged-tools-informational"]')
    expect(note.text()).toContain('block server approval')
  })

  it('a hard-tier chip stops calling itself informational', () => {
    const chip = mount(FindingChip, { props: { state: 'dangerous', count: 1 } }).get(
      '[data-test="finding-chip-dangerous"]',
    )
    const title = chip.attributes('title') ?? ''
    expect(title).not.toContain('does not block the tool')
    expect(title).toContain('block server approval')
  })

  it('a soft-tier chip keeps its review-only reassurance', () => {
    const chip = mount(FindingChip, { props: { state: 'warning', count: 1 } }).get(
      '[data-test="finding-chip-warning"]',
    )
    expect(chip.attributes('title')).toContain('review-only (soft-tier)')
  })

  async function openApproveDialog(wrapper: ReturnType<typeof mountQuarantinedCard>) {
    const approve = wrapper.findAll('button').find((b) => b.text().trim() === 'Approve')
    expect(approve).toBeTruthy()
    await approve!.trigger('click')
    return wrapper.get('.modal-open')
  }

  it('the force-approve dialog names the gate instead of "the scanner gate"', async () => {
    const modal = await openApproveDialog(mountQuarantinedCard())
    expect(modal.text()).toContain('2 dangerous findings')
    expect(modal.text()).not.toContain('the scanner gate')
    expect(modal.text()).toContain('scan-based approval gate')
    expect(modal.text()).toContain('unquarantines this server')
  })

  // The gate sentence is shared by BOTH dialog modes, so it must not name
  // findings: in no_scan mode there are none, and force skips that refusal
  // ("no scan results found; run a scan first or use --force") too.
  it('says nothing about findings in the no-scan mode of the same dialog', async () => {
    const modal = await openApproveDialog(mountQuarantinedCard(false))
    expect(modal.text()).toContain('No Security Scan Run')
    expect(modal.text()).toContain('scan-based approval gate')
    expect(modal.text()).not.toContain('these findings')
    expect(modal.text()).not.toContain('dangerous finding')
  })
})
