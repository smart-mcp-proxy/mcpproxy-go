import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import appRouter from '@/router'

// Audit F20: `/search` was a third search surface — routed but absent from the
// sidebar, duplicating both the header box and the Tools page's own search. It
// now folds into Tools, which is the canonical one: in the sidebar, listing
// what it searches, and able to act on the results.

vi.mock('@/services/api', () => {
  const ok = (data: unknown = {}) => Promise.resolve({ success: true, data })
  return {
    default: {
      getGlobalTools: vi.fn(() =>
        ok({
          tools: [
            { name: 'echo', server_name: 'everything', description: 'Echoes back the input', enabled: true },
            { name: 'add', server_name: 'everything', description: 'Adds two numbers', enabled: true },
          ],
          stats: { total: 2, enabled: 2, disabled: 0 },
        })
      ),
      getToolApprovals: vi.fn(() => ok({ approvals: [] })),
      getQuarantinedTools: vi.fn(() => ok({ tools: [] })),
    },
  }
})

describe('/search route (audit F20)', () => {
  beforeEach(() => {
    // The app router's auth guard reaches for a store on every navigation.
    setActivePinia(createPinia())
  })

  it('redirects to the canonical Tools page', async () => {
    await appRouter.push('/search')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/tools')
  })

  it('carries ?q= across the redirect so old links keep their query', async () => {
    await appRouter.push('/search?q=echo')
    await appRouter.isReady()
    expect(appRouter.currentRoute.value.path).toBe('/tools')
    expect(appRouter.currentRoute.value.query.q).toBe('echo')
  })
})

describe('Tools page honours ?q= (audit F20)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  async function mountTools(target: string) {
    const Tools = (await import('@/views/Tools.vue')).default
    const router = createRouter({
      history: createWebHistory(),
      routes: [
        { path: '/', component: { template: '<div/>' } },
        { path: '/tools', component: Tools },
        { path: '/servers/:serverName', component: { template: '<div/>' } },
      ],
    })
    await router.push(target)
    await router.isReady()
    const wrapper = mount(Tools, { global: { plugins: [createPinia(), router] } })
    await flushPromises()
    await flushPromises()
    return { wrapper, router }
  }

  it('prefills the search box from the query parameter', async () => {
    const { wrapper } = await mountTools('/tools?q=echo')

    const input = wrapper.find('[data-test="tools-search"]')
    expect((input.element as HTMLInputElement).value).toBe('echo')

    const body = wrapper.text()
    expect(body).toContain('echo')
    expect(body).not.toContain('Adds two numbers')
  })

  it('leaves the box empty without a query parameter', async () => {
    const { wrapper } = await mountTools('/tools')

    const input = wrapper.find('[data-test="tools-search"]')
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('Adds two numbers')
  })
})
