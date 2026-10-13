import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import appRouter from '@/router'

describe('/tokens redirect (Spec 109-h FR-031)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.head.innerHTML = '<meta name="mcpproxy-server-edition" content="false">'
  })

  it('opens the Clients token tab and preserves other query parameters', async () => {
    await appRouter.push('/tokens?source=legacy&client=cursor')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/clients')
    expect(appRouter.currentRoute.value.query.tab).toBe('tokens')
    expect(appRouter.currentRoute.value.query.source).toBe('legacy')
    expect(appRouter.currentRoute.value.query.client).toBe('cursor')
  })

  it('opens the token tab for a bare /tokens link', async () => {
    await appRouter.push('/tokens')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/clients')
    expect(appRouter.currentRoute.value.query.tab).toBe('tokens')
  })
})
