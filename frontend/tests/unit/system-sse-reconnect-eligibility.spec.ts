import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'

const createEventSourceMock = vi.hoisted(() => vi.fn())

vi.mock('@/services/api', () => ({
  default: {
    createEventSource: createEventSourceMock,
    hasAPIKey: vi.fn(() => false),
    reinitializeAPIKey: vi.fn(),
  },
}))

import { useSystemStore } from '@/stores/system'

class FakeEventSource {
  static readonly CLOSED = 2
  readyState = 1
  onopen: (() => void) | null = null
  onmessage: ((event: MessageEvent) => void) | null = null
  onerror: (() => void) | null = null
  addEventListener = vi.fn()
  close = vi.fn(() => { this.readyState = FakeEventSource.CLOSED })
}

describe('system SSE reconnect eligibility', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    createEventSourceMock.mockReset()
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('does not resurrect an errored admin stream after eligibility disconnects', () => {
    const first = new FakeEventSource()
    createEventSourceMock.mockReturnValue(first)
    const system = useSystemStore()
    system.connectEventSource()
    first.onerror?.()
    system.disconnectEventSource()

    vi.advanceTimersByTime(5000)
    expect(createEventSourceMock).toHaveBeenCalledTimes(1)
  })

  it('retries one current errored stream once while still connected', () => {
    const first = new FakeEventSource()
    const second = new FakeEventSource()
    createEventSourceMock.mockReturnValueOnce(first).mockReturnValueOnce(second)
    const system = useSystemStore()
    system.connectEventSource()
    first.onerror?.()
    first.onerror?.()

    vi.advanceTimersByTime(5000)
    expect(createEventSourceMock).toHaveBeenCalledTimes(2)
  })

  it('ignores a stale source error after replacement instead of scheduling it again', () => {
    const first = new FakeEventSource()
    const second = new FakeEventSource()
    createEventSourceMock.mockReturnValueOnce(first).mockReturnValueOnce(second)
    const system = useSystemStore()
    system.connectEventSource()
    system.connectEventSource()
    first.onerror?.()

    vi.advanceTimersByTime(5000)
    expect(createEventSourceMock).toHaveBeenCalledTimes(2)
  })
})
