import { describe, it, expect, beforeEach } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

// Live QA finding on 109-k-activity-scope-filters: "spec.md line 66
// ('`/sessions`: Owner. Becomes a redirect to `/activity?view=sessions` that
// keeps the query ... (109-k)') is not implemented: frontend/src/router/
// index.ts still registers a standalone `path: '/sessions'` route to
// Sessions.vue (no redirect)." FR-070: Sessions is now one of Activity's
// views, not its own page.
describe('/sessions redirect (Spec 109-k FR-070)', () => {
  beforeEach(() => {
    // The app router's auth guard reaches for a store on every navigation.
    setActivePinia(createPinia())
  })

  it('redirects /sessions to /activity?view=sessions, keeping other params', async () => {
    await appRouter.push('/sessions?session=ws-abc123')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.name).not.toBe('not-found')
    expect(appRouter.currentRoute.value.path).toBe('/activity')
    expect(appRouter.currentRoute.value.query.view).toBe('sessions')
    expect(appRouter.currentRoute.value.query.session).toBe('ws-abc123')
  })

  it('a bare /sessions still lands on /activity?view=sessions', async () => {
    await appRouter.push('/sessions')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/activity')
    expect(appRouter.currentRoute.value.query.view).toBe('sessions')
  })
})
