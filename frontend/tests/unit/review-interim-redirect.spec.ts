import { describe, it, expect, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

// Spec 109 T026a: `/review` and `/review/:server` are interim redirects
// (109-a is the first PR that links to `/review`) until 109-g ships the real
// review queue/detail views. Both must resolve to a REGISTERED route, never
// the `not-found` catch-all, and must keep the caller's other query params.
//
// The redirect targets are functions (they carry the server name / query
// through), which vue-router only evaluates during an actual navigation —
// `router.resolve()` alone does not run them — so this drives real
// navigations on the app's own router instance.
describe('review interim redirects (Spec 109 T026a)', () => {
  beforeEach(() => {
    // The app router's auth guard reaches for a store on every navigation.
    setActivePinia(createPinia())
  })

  it('redirects /review to /servers?status=needs_review, keeping other params', async () => {
    await appRouter.push('/review?foo=bar')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/servers')
    expect(appRouter.currentRoute.value.query.status).toBe('needs_review')
    expect(appRouter.currentRoute.value.query.foo).toBe('bar')
  })

  it('redirects /review/:server to /servers/:server?tab=tools, keeping other params', async () => {
    await appRouter.push('/review/my-server?foo=bar')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/servers/my-server')
    expect(appRouter.currentRoute.value.query.tab).toBe('tools')
    expect(appRouter.currentRoute.value.query.foo).toBe('bar')
  })
})
