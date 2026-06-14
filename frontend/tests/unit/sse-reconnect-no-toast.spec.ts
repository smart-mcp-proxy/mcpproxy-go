import { describe, it, expect, beforeEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useServersStore } from '@/stores/servers'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

// MCP-2215: a backend core stuck in a ~10s re-init loop makes the SSE stream
// drop and reconnect repeatedly, replaying per-server / per-event state in
// bursts. The reported symptom was "tens of Web UI toasts" on each replay.
//
// Invariant this test locks in: replayed SSE state (servers.changed /
// config.reloaded) is a passive snapshot, NOT a user-initiated transition, so
// it MUST NOT emit any toast. Toasts are reserved for user actions
// (button clicks, form submits) — see ServerCard.vue / Servers.vue handlers.
//
// system.ts forwards each SSE event onto `window` as a CustomEvent; the
// servers store subscribes to those. Firing the same window events the SSE
// layer dispatches is therefore a faithful, headless reproduction of a
// reconnect-replay storm. Live end-to-end coverage (mocked-stream storm + a
// real backend restart loop) lives in e2e/playwright/sse-reconnect-toast-storm/.

vi.mock('@/services/api', () => ({
  default: {
    // handleConfigReloaded() falls back to a silent refetch.
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
    createEventSource: vi.fn(),
  },
}))

function fullServerList(seq: number) {
  // Mirror the shape the backend publishes on servers.changed: a full list
  // wrapped in { payload: { servers } }. Flap connected/connecting between
  // bursts so any "state changed → toast" path would fire.
  return ['alpha', 'bravo', 'charlie/remote'].map((name, i) => ({
    id: name,
    name,
    protocol: i === 2 ? 'http' : 'stdio',
    enabled: true,
    quarantined: false,
    connected: seq % 2 === 0,
    connecting: seq % 2 !== 0,
    tool_count: seq % 2 === 0 ? 5 : 0,
    oauth_status: i === 2 ? 'expired' : 'none',
    last_error: i === 2 ? 'OAuth authentication required' : '',
  }))
}

describe('SSE reconnect-replay storm emits zero toasts (MCP-2215)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('replaying servers.changed / config.reloaded in bursts pushes no toast', async () => {
    const system = useSystemStore()
    // Instantiating the servers store registers the window listeners that the
    // SSE layer feeds (handleServersChanged / handleConfigReloaded).
    useServersStore()

    expect(system.toasts.length).toBe(0)

    // Simulate a storm: 30 reconnect bursts, each replaying the full server
    // list (with flapping connection state) plus a config reload.
    for (let seq = 0; seq < 30; seq++) {
      window.dispatchEvent(
        new CustomEvent('mcpproxy:servers-changed', {
          detail: { payload: { servers: fullServerList(seq) } },
        })
      )
      window.dispatchEvent(
        new CustomEvent('mcpproxy:config-reloaded', { detail: { reason: 'reinit' } })
      )
    }
    // Let the silent background refetch in handleConfigReloaded settle.
    await Promise.resolve()
    await Promise.resolve()

    expect(
      system.toasts.map((t) => t.title),
      'reconnect/replay must not surface toasts'
    ).toEqual([])
    expect(system.toasts.length).toBe(0)
  })

  it('a genuine user action still toasts (guard is not vacuously true)', () => {
    const system = useSystemStore()
    useServersStore()

    // Positive control: the toast path itself works for real user actions.
    system.addToast({ type: 'success', title: 'Server Disabled', message: 'alpha' })
    expect(system.toasts.length).toBe(1)
    expect(system.toasts[0].title).toBe('Server Disabled')
  })
})
