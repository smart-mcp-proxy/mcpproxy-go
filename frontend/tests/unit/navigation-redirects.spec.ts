import { describe, it, expect } from 'vitest'
import { createRouter, createMemoryHistory, type RouteRecordRaw } from 'vue-router'
import realRouter from '@/router'

// Spec 109-i / navigation-map.md "Redirects (query kept)": every retired path
// still lands on its new home, and the query string (deep links) survives the
// hop. Uses the real routes array on a memory history; only components are
// swapped for a stub, so no view is loaded.

const stub = { template: '<div />' }

function memoryRouter() {
  const routes = realRouter.options.routes.map((r) => ('redirect' in r ? r : { ...r, component: stub })) as RouteRecordRaw[]
  return createRouter({ history: createMemoryHistory(), routes })
}

async function land(from: string) {
  const router = memoryRouter()
  await router.push(from)
  await router.isReady()
  const { path, query, hash } = router.currentRoute.value
  return { path, query, hash }
}

describe('retired paths redirect with their query kept', () => {
  it('/overview -> / keeping query and hash', async () => {
    expect(await land('/overview?x=1#h')).toEqual({ path: '/', query: { x: '1' }, hash: '#h' })
  })

  it('/tokens -> /clients?tab=tokens', async () => {
    expect(await land('/tokens?token=t&x=1')).toEqual({ path: '/clients', query: { token: 't', x: '1', tab: 'tokens' }, hash: '' })
  })

  it('/sessions -> /activity?view=sessions', async () => {
    expect(await land('/sessions?session=s')).toEqual({ path: '/activity', query: { session: 's', view: 'sessions' }, hash: '' })
  })

  it('/security -> /review', async () => {
    expect(await land('/security?x=1')).toEqual({ path: '/review', query: { x: '1' }, hash: '' })
  })

  it('/repositories -> /add-server on the catalog tab', async () => {
    expect(await land('/repositories?q=a')).toEqual({ path: '/add-server', query: { q: 'a', tab: 'catalog' }, hash: '' })
  })

  it('/search -> /tools', async () => {
    expect(await land('/search?q=a')).toEqual({ path: '/tools', query: { q: 'a' }, hash: '' })
  })
})
