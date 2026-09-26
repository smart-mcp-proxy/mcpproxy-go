import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSystemStore } from '@/stores/system'
import { useAttentionStore } from '@/stores/attention'
import api from '@/services/api'

// Review finding: nothing refetches attention state when the SSE connection
// reconnects. FR-002's threshold-crossing events (server_error,
// client_never_seen, …) fire only once at the threshold; if the stream is
// down for the ~20s the event would have fired in, the frame is lost forever
// and the header pill / sidebar badge / Home list stay wrong until an
// unrelated change happens to fire a fresh event — contradicting the stated
// "reconnect/missed-events gracefully" design goal.
//
// Fix: `es.onopen` (fired on the FIRST connect and on every reconnect after
// `onerror`'s retry) re-dispatches the same `mcpproxy:attention-changed`
// window event the SSE `attention.changed` handler dispatches, so the one
// attention store every surface reads refetches — the same pattern already
// used for section-specific refresh via CustomEvents (servers-changed,
// config-reloaded, …).

// jsdom does not implement EventSource; system.ts only reads the CLOSED
// constant off the global (`es.readyState === EventSource.CLOSED`).
;(globalThis as any).EventSource = { CONNECTING: 0, OPEN: 1, CLOSED: 2 }

function fakeEventSource() {
  const listeners: Record<string, ((ev: unknown) => void)[]> = {}
  return {
    onopen: null as null | (() => void),
    onmessage: null as null | ((ev: unknown) => void),
    onerror: null as null | (() => void),
    readyState: 2, // CLOSED — the state onerror observes on a real drop
    close: vi.fn(),
    addEventListener: vi.fn((type: string, cb: (ev: unknown) => void) => {
      listeners[type] = listeners[type] || []
      listeners[type].push(cb)
    }),
    _listeners: listeners,
  }
}

vi.mock('@/services/api', () => ({
  default: {
    createEventSource: vi.fn(),
    hasAPIKey: vi.fn().mockReturnValue(true),
    reinitializeAPIKey: vi.fn(),
    getAttention: vi.fn().mockResolvedValue({
      success: true,
      data: { count: 0, generated_at: new Date().toISOString(), items: [] },
    }),
  },
}))

describe('SSE reconnect resyncs the attention list', () => {
  let attention: ReturnType<typeof useAttentionStore>

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(api.getAttention as any).mockResolvedValue({
      success: true,
      data: { count: 0, generated_at: new Date().toISOString(), items: [] },
    })
  })

  afterEach(() => {
    attention?.cleanupEventListeners()
  })

  it('refetches attention when the EventSource reconnects after a drop', async () => {
    let created = 0
    const sources: ReturnType<typeof fakeEventSource>[] = []
    ;(api.createEventSource as any).mockImplementation(() => {
      created++
      const es = fakeEventSource()
      sources.push(es)
      return es
    })

    const system = useSystemStore()
    attention = useAttentionStore()

    system.connectEventSource()
    expect(created).toBe(1)

    // Initial connect opens.
    sources[0].onopen?.()
    await Promise.resolve()
    const callsAfterFirstOpen = (api.getAttention as any).mock.calls.length

    // The connection drops and system.ts's onerror handler schedules a retry.
    vi.useFakeTimers()
    sources[0].onerror?.()
    await vi.advanceTimersByTimeAsync(5000)
    vi.useRealTimers()

    expect(created).toBe(2)
    ;(api.getAttention as any).mockClear()

    // Reconnect opens the new EventSource.
    sources[1].onopen?.()
    await Promise.resolve()
    await Promise.resolve()

    expect((api.getAttention as any).mock.calls.length).toBeGreaterThan(0)
    void callsAfterFirstOpen
  })
})
