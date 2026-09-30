import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import api from '@/services/api'
import { useProfilesStore } from '@/stores/profiles'
import { useClientsStore } from '@/stores/clients'
import { makeClient, makeProfile } from './fixtures/profiles108i'

// Spec 108-i T102 / I23: profiles.changed and client.binding_changed are
// invalidations. The profiles store and the clients store refetch; nothing is
// patched from the event payload.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getProfiles: vi.fn(),
    getClients: vi.fn(),
    getRouting: vi.fn(),
    createEventSource: vi.fn(),
  },
}))

describe('profiles and client rows refresh on SSE events (Spec 108-i T102)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [makeProfile('work')] })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor')], warnings: [] } })
    ;(api.getRouting as any).mockResolvedValue({ success: true, data: { endpoints: {}, routing_mode: 'retrieve_tools' } })
  })
  afterEach(() => {
    // Dispose the stores so their window listeners do not leak into the next test.
    for (const store of [useProfilesStore(), useClientsStore()]) store.$dispose()
    vi.useRealTimers()
  })

  it('refetches the profile list on mcpproxy:profiles.changed, debounced', async () => {
    const store = useProfilesStore()
    await store.fetchProfiles()
    expect(api.getProfiles).toHaveBeenCalledTimes(1)
    // A burst of events from one config write is one refetch.
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    expect(api.getProfiles).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(249)
    expect(api.getProfiles).toHaveBeenCalledTimes(1)
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [makeProfile('work'), makeProfile('new')], anonymous_profile: 'new' })
    await vi.advanceTimersByTimeAsync(2)
    expect(api.getProfiles).toHaveBeenCalledTimes(2)
    expect(store.profiles.map(profile => profile.name)).toEqual(['work', 'new'])
    expect(store.anonymousProfile).toBe('new')
  })

  it('refetches the client rows on client.binding_changed, keeping the scope and the warnings', async () => {
    const store = useClientsStore()
    await store.load({ profile: 'work' })
    expect(api.getClients).toHaveBeenLastCalledWith({ profile: 'work' })
    ;(api.getClients as any).mockResolvedValue({ success: true, data: { clients: [makeClient('cursor', { profile: 'work' })], warnings: [{ code: 'client_rotation_pending', severity: 'info', message: 'pending' }] } })
    window.dispatchEvent(new CustomEvent('mcpproxy:client.binding_changed', { detail: { client_id: 'cursor' } }))
    await vi.advanceTimersByTimeAsync(0)
    expect(api.getClients).toHaveBeenCalledTimes(2)
    expect(api.getClients).toHaveBeenLastCalledWith({ profile: 'work' })
    expect(store.clients[0].profile).toBe('work')
    expect(store.warnings.map(warning => warning.code)).toEqual(['client_rotation_pending'])
  })

  it('also refreshes the rows on profiles.changed (a rename moves pins)', async () => {
    const store = useClientsStore()
    await store.load()
    window.dispatchEvent(new CustomEvent('mcpproxy:profiles.changed'))
    await vi.advanceTimersByTimeAsync(0)
    expect(api.getClients).toHaveBeenCalledTimes(2)
    expect(store.clients).toHaveLength(1)
  })

  it('the SSE bridge re-dispatches both events as window events', async () => {
    const { useSystemStore } = await import('@/stores/system')
    const listeners = new Map<string, (event: MessageEvent) => void>()
    class FakeEventSource {
      onopen: (() => void) | null = null
      onerror: (() => void) | null = null
      readyState = 1
      constructor(public url: string) {}
      addEventListener(name: string, handler: (event: MessageEvent) => void) { listeners.set(name, handler) }
      close() {}
    }
    ;(api.createEventSource as any).mockImplementation(() => new FakeEventSource('/events'))
    const seen: string[] = []
    const capture = (event: Event) => seen.push(`${event.type}:${JSON.stringify((event as CustomEvent).detail)}`)
    window.addEventListener('mcpproxy:profiles.changed', capture)
    window.addEventListener('mcpproxy:client.binding_changed', capture)
    useSystemStore().connectEventSource()
    listeners.get('profiles.changed')!({ data: '{"change":"update"}' } as MessageEvent)
    listeners.get('client.binding_changed')!({ data: 'not json' } as MessageEvent)
    window.removeEventListener('mcpproxy:profiles.changed', capture)
    window.removeEventListener('mcpproxy:client.binding_changed', capture)
    expect(seen).toEqual(['mcpproxy:profiles.changed:{"change":"update"}', 'mcpproxy:client.binding_changed:null'])
  })
})
