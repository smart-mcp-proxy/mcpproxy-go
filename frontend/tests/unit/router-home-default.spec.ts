import { describe, it, expect } from 'vitest'

// Spec 109 T006 / T018 (FR-051, research D3): `/` is the 109-a interim
// landing step — it renders the Dashboard's Overview panel, not Usage.
// `/usage` keeps rendering the full analytics panel (interim N8: Home
// replaces `/` in 109-d, but that PR is out of scope here).
import appRouter from '@/router'

describe('router home default (109-a interim)', () => {
  it('renders the Overview panel at "/"', () => {
    const root = appRouter.resolve('/')
    expect(root.name).toBe('dashboard')
    expect(root.meta.dashboardView).toBe('overview')
  })

  it('renders the Usage panel at "/usage", sharing the same component as "/"', () => {
    const root = appRouter.resolve('/')
    const usage = appRouter.resolve('/usage')
    expect(usage.name).toBe('usage')
    expect(usage.meta.dashboardView).toBe('usage')
    expect(usage.matched[0].components?.default).toBe(root.matched[0].components?.default)
  })

  it('keeps "/overview" deep-linkable to the same Overview panel', () => {
    const overview = appRouter.resolve('/overview')
    expect(overview.name).toBe('dashboard-overview')
    expect(overview.meta.dashboardView).toBe('overview')
  })
})
