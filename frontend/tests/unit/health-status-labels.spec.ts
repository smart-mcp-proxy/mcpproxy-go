import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import ServerCard from '@/components/ServerCard.vue'
import type { Server, HealthStatus } from '@/types'
import { HEALTH_STATUS_LABELS, HEALTH_ACTION_LABELS, type HealthStatusValue } from '@/types/contracts'
import { healthStatusLabel, healthActionLabel } from '@/utils/health'

// Spec 109 T042 (FR-010–012, FR-014 labels): the label table from
// contracts.ts, the card status line rendering `status` (not `level`), and
// the SC-003 forbidden-words guard for every `usable=false` fixture.

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
    name: 'test-server',
    protocol: 'http',
    url: 'https://example.invalid/mcp',
    enabled: true,
    quarantined: false,
    connected: false,
    connecting: false,
    tool_count: 0,
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

describe('healthStatusLabel / healthActionLabel (Spec 109 FR-014 label table)', () => {
  it('covers every status value from contracts.ts', () => {
    for (const status of Object.keys(HEALTH_STATUS_LABELS) as HealthStatusValue[]) {
      expect(healthStatusLabel(status)).toBe(HEALTH_STATUS_LABELS[status])
      expect(healthStatusLabel(status)).not.toBe('')
    }
  })

  it('falls back to the raw value for an unrecognized status (forward-compat)', () => {
    expect(healthStatusLabel('some_future_status')).toBe('some_future_status')
    expect(healthStatusLabel(undefined)).toBe('')
    expect(healthStatusLabel(null)).toBe('')
  })

  it('covers every action in the priority order', () => {
    for (const action of Object.keys(HEALTH_ACTION_LABELS)) {
      expect(healthActionLabel(action)).toBe(HEALTH_ACTION_LABELS[action])
    }
  })

  it('returns empty string for no action', () => {
    expect(healthActionLabel('')).toBe('')
    expect(healthActionLabel(undefined)).toBe('')
  })
})

describe('ServerCard status line renders `status`, never `level`, as text (FR-011)', () => {
  it('renders the status label when summary is absent', () => {
    const wrapper = mountCard(
      makeServer({
        health: {
          level: 'healthy',
          admin_state: 'enabled',
          summary: '',
          status: 'ready',
          usable: true,
          actions: [],
        } as unknown as HealthStatus,
      })
    )
    const chip = wrapper.find('[data-test="server-status-chip"]')
    expect(chip.text()).toBe('Online')
    expect(chip.text()).not.toMatch(/healthy/i)
  })

  it('prefers the free-text summary over the bare label when both are present', () => {
    const wrapper = mountCard(
      makeServer({
        health: {
          level: 'healthy',
          admin_state: 'enabled',
          summary: 'Connected (14 tools)',
          status: 'ready',
          usable: true,
          actions: [],
        } as unknown as HealthStatus,
      })
    )
    expect(wrapper.find('[data-test="server-status-chip"]').text()).toBe('Connected (14 tools)')
  })
})

// SC-003: for every fixture with usable=false, no renderer output contains
// "healthy"/"Healthy"/"online"/"Online"/"connected"/"Connected" as the
// server's state.
describe('SC-003 — forbidden renderings for usable=false servers', () => {
  const forbidden = ['healthy', 'Healthy', 'online', 'Online', 'connected', 'Connected']

  const unusableFixtures: Array<{ name: string; health: Partial<HealthStatus> }> = [
    {
      name: 'disabled',
      health: { level: 'healthy', admin_state: 'disabled', summary: '', status: 'disabled', usable: false, actions: ['enable'] },
    },
    {
      name: 'needs_review',
      health: { level: 'healthy', admin_state: 'quarantined', summary: '', status: 'needs_review', usable: false, actions: ['approve'] },
    },
    {
      name: 'sign_in_required',
      health: { level: 'degraded', admin_state: 'enabled', summary: '', status: 'sign_in_required', usable: false, actions: ['login'] },
    },
    {
      name: 'needs_secret',
      health: { level: 'unhealthy', admin_state: 'enabled', summary: '', status: 'needs_secret', usable: false, actions: ['set_secret'] },
    },
    {
      name: 'needs_config',
      health: { level: 'unhealthy', admin_state: 'enabled', summary: '', status: 'needs_config', usable: false, actions: ['configure'] },
    },
    {
      name: 'error',
      health: { level: 'unhealthy', admin_state: 'enabled', summary: '', status: 'error', usable: false, actions: ['restart', 'view_logs'] },
    },
    {
      name: 'connecting',
      health: { level: 'healthy', admin_state: 'enabled', summary: '', status: 'connecting', usable: false, actions: [] },
    },
  ]

  for (const fixture of unusableFixtures) {
    it(`${fixture.name}: status chip never says healthy/online/connected`, () => {
      const wrapper = mountCard(makeServer({ health: fixture.health as HealthStatus }))
      const text = wrapper.find('[data-test="server-status-chip"]').text()
      for (const word of forbidden) {
        expect(text).not.toContain(word)
      }
    })
  }
})
