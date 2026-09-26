import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import { useServersStore } from '@/stores/servers'
import type { Server } from '@/types'

vi.mock('@/services/api', () => ({
  default: {
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

const RouterLinkStub = {
  props: ['to'],
  template: '<a :href="typeof to === \'string\' ? to : \'#\'"><slot /></a>',
}

function makeQuarantinedServer(overrides: Partial<Server> = {}): Server {
  return {
    name: 'test-server',
    protocol: 'http',
    url: 'https://example.invalid/mcp',
    enabled: true,
    quarantined: true,
    connected: false,
    connecting: false,
    tool_count: 0,
    health: {
      level: 'healthy',
      admin_state: 'quarantined',
      summary: 'Quarantined for review',
      action: 'approve',
    },
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

// FR-005 / specs/109-ux-navigation-consistency/contracts/health-vocabulary.md
// ("approve -> Review (opens the review screen; never approves directly,
// FR-005)"): the card's primary action for a quarantined server must open the
// review screen, not call the approve API directly — matching the macOS
// implementation (DashboardView.performAction .approve case).
//
// Spec 109-e rewrote ServerCard to a single primary action bound to
// `health.actions[0]`/`health.action` (data-test="server-card-primary-action"),
// replacing the per-action buttons (`server-card-approve` etc.) this test
// originally targeted. The Review link now opens the Tools tab directly
// (round 1, 109-e high finding: a `/review/<name>` redirect this used to rely
// on never existed on this branch), not the Security tab.
describe('ServerCard — approve action opens Review, never approves directly (FR-005)', () => {
  it('renders the primary action as "Review", not "Approve"', () => {
    const card = mountCard(makeQuarantinedServer())
    const action = card.find('[data-test="server-card-primary-action"]')
    expect(action.exists()).toBe(true)
    expect(action.text()).toBe('Review')
    expect(action.text()).not.toContain('Approve')
  })

  it('links to the server Tools tab instead of triggering approval', () => {
    const card = mountCard(makeQuarantinedServer())
    const action = card.find('[data-test="server-card-primary-action"]')
    expect(action.attributes('href')).toContain('tab=tools')
  })

  it('never calls securityApproveServer on click, even with a clean completed scan', async () => {
    const card = mountCard(
      makeQuarantinedServer({
        security_scan: {
          status: 'completed',
          risk_score: 0,
          last_scan_at: new Date().toISOString(),
          finding_counts: { dangerous: 0, warning: 0, info: 0 },
        },
      } as Partial<Server>)
    )
    const store = useServersStore()
    const spy = vi.spyOn(store, 'securityApproveServer')

    await card.find('[data-test="server-card-primary-action"]').trigger('click')

    expect(spy).not.toHaveBeenCalled()
  })

  it('renders no Approve Confirmation Modal on the card (relocated to the review screen)', () => {
    const card = mountCard(makeQuarantinedServer())
    expect(card.text()).not.toContain('Force Approve')
    expect(card.text()).not.toContain('No Security Scan Run')
  })
})
