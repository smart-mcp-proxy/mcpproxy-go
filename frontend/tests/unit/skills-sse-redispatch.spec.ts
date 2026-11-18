import { describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useSystemStore } from '@/stores/system'
import api from '@/services/api'

;(globalThis as any).EventSource = { CONNECTING: 0, OPEN: 1, CLOSED: 2 }

vi.mock('@/services/api', () => ({
  default: { createEventSource: vi.fn(), hasAPIKey: vi.fn().mockReturnValue(true), reinitializeAPIKey: vi.fn() },
}))

describe('SSE skills.changed', () => {
  it('is re-dispatched as mcpproxy:skills-changed with the parsed payload', () => {
    setActivePinia(createPinia())
    const listeners: Record<string, (ev: unknown) => void> = {}
    ;(api.createEventSource as any).mockReturnValue({
      onopen: null, onmessage: null, onerror: null, readyState: 1, close: vi.fn(),
      addEventListener: (type: string, cb: (ev: unknown) => void) => { listeners[type] = cb },
    })
    const seen: unknown[] = []
    const on = (e: Event) => seen.push((e as CustomEvent).detail)
    window.addEventListener('mcpproxy:skills-changed', on)
    useSystemStore().connectEventSource()
    listeners['skills.changed']({ data: JSON.stringify({ server: 'hf', counts: { pending: 1 } }) })
    listeners['skills.changed']({ data: 'not json' })
    window.removeEventListener('mcpproxy:skills-changed', on)
    expect(seen).toEqual([{ server: 'hf', counts: { pending: 1 } }])
  })
})
