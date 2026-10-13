import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import router from '@/router'

describe('security route relocation (T088)', () => {
  beforeEach(() => { setActivePinia(createPinia()); document.head.innerHTML = '<meta name="mcpproxy-server-edition" content="false">' })
  it('redirects the retired security page to review but preserves a scan report route', async () => {
    await router.push('/security?change=changed')
    await router.isReady()
    expect(router.currentRoute.value.path).toBe('/review')
    expect(router.currentRoute.value.query.change).toBe('changed')
    await router.push('/security/scans/job-1')
    expect(router.currentRoute.value.name).toBe('scan-report')
  })
})
