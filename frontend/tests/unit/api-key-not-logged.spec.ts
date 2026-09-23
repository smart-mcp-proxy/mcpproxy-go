import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'

// SEC-07: the API service used to console.log the first 8 characters of the
// admin key on import, on setAPIKey, on EVERY request and when opening the SSE
// stream. Devtools console history is readable by anything with a debugger
// attached and ends up pasted into bug reports, so no key material — not even
// a prefix — may reach the console.

const SECRET = 'SUPERSECRETKEY-abcdef0123456789'
const STORAGE_KEY = 'mcpproxy-api-key'

type ConsoleMethod = 'log' | 'info' | 'debug' | 'warn' | 'error'
const CONSOLE_METHODS: ConsoleMethod[] = ['log', 'info', 'debug', 'warn', 'error']

/** Every argument passed to any console method during the spy's lifetime. */
function collectConsoleOutput(spies: Record<ConsoleMethod, ReturnType<typeof vi.spyOn>>): string {
  return CONSOLE_METHODS.flatMap((method) =>
    spies[method].mock.calls.flatMap((args: unknown[]) =>
      args.map((arg) => {
        if (typeof arg === 'string') return arg
        try {
          return JSON.stringify(arg)
        } catch {
          return String(arg)
        }
      })
    )
  ).join('\n')
}

describe('api service does not log key material', () => {
  let spies: Record<ConsoleMethod, ReturnType<typeof vi.spyOn>>

  beforeEach(() => {
    localStorage.clear()
    vi.resetModules()
    spies = {} as Record<ConsoleMethod, ReturnType<typeof vi.spyOn>>
    for (const method of CONSOLE_METHODS) {
      spies[method] = vi.spyOn(console, method).mockImplementation(() => {})
    }
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
    localStorage.clear()
    // Always clear a ?apikey= left on the jsdom URL by the query-parameter
    // test below, even if that test's assertions threw first — otherwise the
    // secret lingers in window.location for whatever spec runs next in this
    // module.
    window.history.replaceState({}, '', '/')
  })

  it('never writes the key or its prefix to the console', async () => {
    // The singleton runs initializeAPIKey() in its constructor at import time,
    // so seed storage before importing.
    localStorage.setItem(STORAGE_KEY, SECRET)

    // jsdom has no EventSource; stub one so createEventSource() can run.
    class FakeEventSource {
      constructor(public url: string) {}
      close() {}
    }
    vi.stubGlobal('EventSource', FakeEventSource as unknown as typeof EventSource)
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => new Response(JSON.stringify({ success: true, data: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' }
      }))
    )

    const api = (await import('@/services/api')).default

    // Exercise every path that used to echo the key.
    api.reinitializeAPIKey()
    api.setAPIKey(SECRET)
    await api.getStatus()
    api.createEventSource()

    const logged = collectConsoleOutput(spies)
    expect(logged).not.toContain(SECRET)
    expect(logged).not.toContain(SECRET.substring(0, 8))
  })

  // The service is not the only consumer: the system store used to log
  // api.getAPIKeyPreview() every time it opened the SSE stream, so a spec that
  // only drives APIService would miss the leak that actually fires on app boot.
  it('never leaks the key through the system store SSE connect path', async () => {
    localStorage.setItem(STORAGE_KEY, SECRET)

    // A faithful stand-in: the real EventSource carries the key in its `url`
    // (SSE cannot send headers), and the error event's `target` is the
    // EventSource itself — which is how logging the raw event leaked the whole
    // key, not just a prefix.
    const created: FakeEventSource[] = []
    class FakeEventSource {
      static readonly CLOSED = 2
      readonly CLOSED = 2
      readyState = 0
      onopen: (() => void) | null = null
      onerror: ((event: unknown) => void) | null = null
      constructor(public url: string) {
        created.push(this)
      }
      addEventListener() {}
      close() {}
    }
    vi.stubGlobal('EventSource', FakeEventSource as unknown as typeof EventSource)

    const { createPinia, setActivePinia } = await import('pinia')
    setActivePinia(createPinia())

    const { useSystemStore } = await import('@/stores/system')
    useSystemStore().connectEventSource()

    // Drive the failure path too: a dropped stream is routine, and its handler
    // used to log the credential-bearing event object.
    const es = created.at(-1)
    expect(es).toBeDefined()
    expect(es!.url).toContain(encodeURIComponent(SECRET))
    es!.readyState = 1
    es!.onerror?.({ type: 'error', target: es })

    const logged = collectConsoleOutput(spies)
    expect(logged).not.toContain(SECRET)
    expect(logged).not.toContain(SECRET.substring(0, 8))
  })

  it('does not log the key when it arrives via the URL parameter', async () => {
    window.history.replaceState({}, '', `/?apikey=${encodeURIComponent(SECRET)}`)

    const api = (await import('@/services/api')).default
    expect(api.hasAPIKey()).toBe(true)

    const logged = collectConsoleOutput(spies)
    expect(logged).not.toContain(SECRET)
    expect(logged).not.toContain(SECRET.substring(0, 8))
  })
})
