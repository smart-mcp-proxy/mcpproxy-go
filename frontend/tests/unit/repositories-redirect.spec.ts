import { describe, it, expect, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

// Spec 109 FR-062: /repositories redirects to /add-server?tab=catalog, and
// /add-server is its own registered route (never the not-found catch-all).
describe('/repositories redirect (Spec 109 FR-062)', () => {
  beforeEach(() => {
    // The app router's auth guard reaches for a store on every navigation.
    setActivePinia(createPinia())
  })

  it('redirects to /add-server?tab=catalog', async () => {
    await appRouter.push('/repositories')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/add-server')
    expect(appRouter.currentRoute.value.query.tab).toBe('catalog')
  })

  it('preserves ?source= (the catalog-source filter) across the redirect', async () => {
    await appRouter.push('/repositories?source=official')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/add-server')
    expect(appRouter.currentRoute.value.query.tab).toBe('catalog')
    expect(appRouter.currentRoute.value.query.source).toBe('official')
  })

  it('registers /add-server as its own route', async () => {
    await appRouter.push('/add-server?tab=paste')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/add-server')
    expect(appRouter.currentRoute.value.name).toBe('add-server')
  })
})
