import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { setAvailableFeatures, useScopeQuery, type UseScopeQueryResult } from '@/composables/useScopeQuery'
import SessionsPanel from '@/components/activity/SessionsPanel.vue'
import { WS_FAILED, timelinePage } from './fixtures/workSessions124'

// Spec 124 PR-3 (T112; FR-063): the timeline is linkable. Its address,
// /activity?view=sessions&ws=<id>&at=<position>, survives a reload and a change
// of the page scope.

vi.mock('@/services/api', () => ({
  default: {
    getProfiles: vi.fn(),
    getClients: vi.fn(),
    getWorkSessions: vi.fn(() => Promise.resolve({ sessions: [], total: 0, limit: 25, offset: 0 })),
    getWorkSessionTimeline: vi.fn(() => Promise.resolve(timelinePage())),
  },
}))

const stub = { template: '<div />' }
const ADDRESS = `/activity?view=sessions&ws=${WS_FAILED.id}&at=failure:2`

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/usage', name: 'usage', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
    ],
  })
}

async function scopeQueryAt(path: string) {
  const router = makeRouter()
  await router.push(path)
  await router.isReady()
  let scope!: UseScopeQueryResult
  const Probe = defineComponent({
    setup() {
      scope = useScopeQuery('activity')
      return () => h('div')
    },
  })
  mount(Probe, { global: { plugins: [router] } })
  return { scope, router }
}

describe('timeline address in the scope contract (Spec 124 FR-063)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setAvailableFeatures(['profile', 'client', 'token'])
    vi.clearAllMocks()
  })
  afterEach(() => setAvailableFeatures([]))

  it('reads the session and the position from the address', async () => {
    const { scope } = await scopeQueryAt(ADDRESS)
    expect(scope.state.ws).toBe(WS_FAILED.id)
    expect(scope.state.at).toBe('failure:2')
  })

  it('reads the list filter and order from the address', async () => {
    const { scope } = await scopeQueryAt('/activity?view=sessions&failed=true&include_blocked=false&sort=last_failure')
    expect(scope.state.failed).toBe('true')
    expect(scope.state.include_blocked).toBe('false')
    expect(scope.state.sort).toBe('last_failure')
  })

  it('a scope change keeps the session and the position', async () => {
    const { scope, router } = await scopeQueryAt(ADDRESS)
    scope.set({ profile: 'work-readonly' })
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({
      view: 'sessions', ws: WS_FAILED.id, at: 'failure:2', profile: 'work-readonly',
    })
    scope.clear(['profile', 'client'])
    await flushPromises()
    expect(router.currentRoute.value.query).toMatchObject({ view: 'sessions', ws: WS_FAILED.id, at: 'failure:2' })
    expect(router.currentRoute.value.query.profile).toBeUndefined()
  })

  it('never sends the timeline address to the activity REST request', async () => {
    const { scope } = await scopeQueryAt(`${ADDRESS}&failed=true&sort=last_failure&include_blocked=false`)
    const rest = scope.toRest()!
    for (const name of ['ws', 'at', 'failed', 'include_blocked', 'sort']) expect(rest[name]).toBeUndefined()
  })

  it('does not carry the session to another page', async () => {
    const { scope, router } = await scopeQueryAt(`${ADDRESS}&profile=work-readonly`)
    for (const page of ['tools', 'usage'] as const) {
      const query = new URL(router.resolve(scope.linkTo(page)).fullPath, 'http://x').searchParams
      expect(query.has('ws')).toBe(false)
      expect(query.has('at')).toBe(false)
      expect(query.get('profile')).toBe('work-readonly')
    }
    // A link to the tool calls of a session leaves the timeline.
    const calls = new URL(router.resolve(scope.linkTo('activity', { view: 'calls', session: WS_FAILED.id })).fullPath, 'http://x').searchParams
    expect(calls.has('ws')).toBe(false)
    expect(calls.has('at')).toBe(false)
  })

  it('a reload at the address opens the same timeline at the same position', async () => {
    const api = (await import('@/services/api')).default as any
    for (let load = 0; load < 2; load++) {
      const router = makeRouter()
      await router.push(ADDRESS)
      await router.isReady()
      const wrapper = mount(SessionsPanel, { props: { sessions: [] }, global: { plugins: [router] } })
      await flushPromises()
      expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
      expect(api.getWorkSessionTimeline).toHaveBeenLastCalledWith(WS_FAILED.id, { at: 'failure:2', before: 20, after: 20 })
      wrapper.unmount()
    }
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(2)
    expect(api.getWorkSessions).not.toHaveBeenCalled()
  })

  it('a scope change while the timeline is open keeps it open and asks for nothing new', async () => {
    const api = (await import('@/services/api')).default as any
    const router = makeRouter()
    await router.push(ADDRESS)
    await router.isReady()
    const wrapper = mount(SessionsPanel, { props: { sessions: [] }, global: { plugins: [router] } })
    await flushPromises()
    await router.replace({ query: { ...router.currentRoute.value.query, client: 'cursor' } })
    await flushPromises()
    expect(wrapper.find('[data-test="session-timeline"]').exists()).toBe(true)
    expect(api.getWorkSessionTimeline).toHaveBeenCalledTimes(1)
    expect(router.currentRoute.value.query).toMatchObject({ ws: WS_FAILED.id, at: 'failure:2', client: 'cursor' })
  })
})
