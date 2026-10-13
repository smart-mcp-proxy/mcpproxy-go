import { describe, it, expect, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

// Spec 109-g owns the registered queue and per-server review routes. Query
// parameters stay intact so a filtered queue can link into a matching detail.
describe('review routes (Spec 109-g)', () => {
  beforeEach(() => {
    // The app router's auth guard reaches for a store on every navigation.
    setActivePinia(createPinia())
    document.head.innerHTML = '<meta name="mcpproxy-server-edition" content="false">'
  })

  it('keeps /review as the queue, including its filters', async () => {
    await appRouter.push('/review?foo=bar')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/review')
    expect(appRouter.currentRoute.value.query.foo).toBe('bar')
  })

  it('keeps /review/:server as the per-server review screen', async () => {
    await appRouter.push('/review/my-server?foo=bar')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/review/my-server')
    expect(appRouter.currentRoute.value.query.foo).toBe('bar')
  })
})
