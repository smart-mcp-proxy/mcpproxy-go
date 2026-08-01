import { describe, it, expect, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { Server } from '@/types'
import ServerCard from '@/components/ServerCard.vue'

// ServerCard pulls in stores + the security-scanner composable (which fetches
// the security overview). Stub the API so mounting stays offline.
vi.mock('@/services/api', () => ({
  default: {
    getSecurityOverview: vi.fn().mockResolvedValue({ data: {} }),
  },
}))

function makeServer(overrides: Partial<Server> = {}): Server {
  return {
    id: 's1',
    name: 'github',
    protocol: 'streamable-http',
    enabled: true,
    quarantined: false,
    connected: true,
    status: 'connected',
    reconnect_count: 0,
    tool_count: 1,
    created: '',
    updated: '',
    ...overrides,
  } as Server
}

function mountCard(server: Server) {
  setActivePinia(createPinia())
  return mount(ServerCard, {
    props: { server },
    global: {
      plugins: [createPinia()],
      stubs: { 'router-link': { template: '<a><slot /></a>' } },
    },
  })
}

// GH #938 finding 3: the card rendered a green shield reading "Clean" — the
// verdict of the last FULL-SERVER scan — directly above the text
// "1 tool changed since approval — re-review needed", while the tool-level gate
// was holding that tool. The juxtaposition reads as reassurance next to a
// warning. The badge must not claim all-clear while tools are held.
describe('ServerCard security badge vs. held tools (#938)', () => {
  const badge = '[data-test="security-scan-badge"]'

  it('does not read a plain green "Clean" while a tool is held', () => {
    const card = mountCard(makeServer({
      security_scan: { status: 'clean' },
      quarantine: { pending_count: 0, changed_count: 1, blocked_count: 0 },
    } as Partial<Server>))

    const el = card.find(badge)
    expect(el.exists()).toBe(true)
    expect(el.text()).not.toBe('Clean')
    expect(el.text().toLowerCase()).toContain('held')
    expect(el.classes()).not.toContain('text-success')
    expect(el.classes()).toContain('text-warning')
  })

  it('counts pending holds too', () => {
    const card = mountCard(makeServer({
      security_scan: { status: 'clean' },
      quarantine: { pending_count: 2, changed_count: 0, blocked_count: 0 },
    } as Partial<Server>))

    expect(card.find(badge).text()).toContain('2')
  })

  it('still reads a plain "Clean" when nothing is held', () => {
    const card = mountCard(makeServer({ security_scan: { status: 'clean' } } as Partial<Server>))

    const el = card.find(badge)
    expect(el.text()).toBe('Clean')
    expect(el.classes()).toContain('text-success')
  })

  it('leaves a dangerous verdict alone — the harder verdict always wins', () => {
    const card = mountCard(makeServer({
      security_scan: { status: 'dangerous' },
      quarantine: { pending_count: 0, changed_count: 1, blocked_count: 0 },
    } as Partial<Server>))

    const el = card.find(badge)
    expect(el.text()).toBe('Dangerous')
    expect(el.classes()).toContain('text-error')
  })
})
