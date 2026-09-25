import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createRouter, createMemoryHistory, type Router } from 'vue-router'
import { defineComponent, h } from 'vue'
import {
  useScopeQuery,
  setAvailableFeatures,
  resolveScopeTime,
  usageWindowFor,
  splitScopeTool,
  sessionRestParam,
  OTHER_STATUS,
  type PageId,
  type UseScopeQueryResult,
} from '@/composables/useScopeQuery'

// Spec 109-k (activity-scope-filters, T111): url-filter-contract.md owns this
// composable's contract. Covers the registry, the REST mapping rules
// (tool-split, session ws- prefix, the Usage `window` mapping, the Web-only
// `status=other` bucket), sticky carry via linkTo, unknown-param
// preservation, and the profile/client/token availability gate.

async function withScopeQuery(page: PageId, initialQuery: Record<string, string>): Promise<{
  api: UseScopeQueryResult
  router: Router
}> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: { template: '<div/>' } },
      { path: '/usage', name: 'usage', component: { template: '<div/>' } },
      { path: '/tools', name: 'tools', component: { template: '<div/>' } },
      { path: '/servers', name: 'servers', component: { template: '<div/>' } },
      { path: '/sessions', name: 'sessions', component: { template: '<div/>' } },
    ],
  })

  let api!: UseScopeQueryResult
  const Harness = defineComponent({
    setup() {
      api = useScopeQuery(page)
      return () => h('div')
    },
  })

  const query = new URLSearchParams(initialQuery).toString()
  await router.push(`/${page}${query ? '?' + query : ''}`)
  mount(Harness, { global: { plugins: [router] } })
  await router.isReady()

  return { api, router }
}

describe('useScopeQuery', () => {
  afterEach(() => {
    setAvailableFeatures([])
  })

  describe('registry + read-before-first-fetch', () => {
    it('reads the URL state synchronously on setup (no unfiltered first fetch)', async () => {
      const { api } = await withScopeQuery('activity', { server: 'github', status: 'error' })
      expect(api.state.server).toBe('github')
      expect(api.state.status).toBe('error')
    })

    it('unknown params are preserved untouched by set()', async () => {
      const { api, router } = await withScopeQuery('activity', { server: 'github', weird: 'kept' })
      api.set({ server: 'slack' })
      await flushPromises()
      expect(router.currentRoute.value.query.weird).toBe('kept')
      expect(router.currentRoute.value.query.server).toBe('slack')
    })
  })

  describe('router.replace via set()/clear()', () => {
    it('set() removes a param when patched to undefined/empty', async () => {
      const { api, router } = await withScopeQuery('activity', { server: 'github' })
      api.set({ server: undefined })
      await flushPromises()
      expect(router.currentRoute.value.query.server).toBeUndefined()
    })

    it('clear() with no names clears every contract parameter the page supports', async () => {
      const { api, router } = await withScopeQuery('activity', { server: 'github', status: 'error', keep_me: 'x' })
      api.clear()
      await flushPromises()
      expect(router.currentRoute.value.query.server).toBeUndefined()
      expect(router.currentRoute.value.query.status).toBeUndefined()
      // Not a registered Activity param — untouched.
      expect(router.currentRoute.value.query.keep_me).toBe('x')
    })

    it('clear([name]) clears only the named parameter', async () => {
      const { api, router } = await withScopeQuery('activity', { server: 'github', status: 'error' })
      api.clear(['server'])
      await flushPromises()
      expect(router.currentRoute.value.query.server).toBeUndefined()
      expect(router.currentRoute.value.query.status).toBe('error')
    })
  })

  describe('toRest() mapping', () => {
    it('session=ws-... maps to work_session_id', async () => {
      const { api } = await withScopeQuery('activity', { session: 'ws-abc' })
      expect(api.toRest()).toEqual({ work_session_id: 'ws-abc' })
    })

    it('a legacy session id maps to session_id', async () => {
      const { api } = await withScopeQuery('activity', { session: 'raw-transport-id' })
      expect(api.toRest()).toEqual({ session_id: 'raw-transport-id' })
    })

    it('tool=server:tool splits into server + bare tool on Activity', async () => {
      const { api } = await withScopeQuery('activity', { tool: 'github:create_issue' })
      expect(api.toRest()).toEqual({ server: 'github', tool: 'create_issue' })
    })

    it('a disagreeing explicit server is kept as-is (never widened)', async () => {
      const { api } = await withScopeQuery('activity', { tool: 'github:create_issue', server: 'notes' })
      expect(api.toRest()).toEqual({ server: 'notes', tool: 'create_issue' })
    })

    it('a bare tool value (no colon) is sent as the bare tool', async () => {
      const { api } = await withScopeQuery('activity', { tool: 'search' })
      expect(api.toRest()).toEqual({ tool: 'search' })
    })

    it('status=other on Activity is never sent to REST (Web-only bucket)', async () => {
      const { api } = await withScopeQuery('activity', { status: OTHER_STATUS })
      expect(api.toRest()).toEqual({})
    })

    it('status on Tools never appears in toRest() (client-side only)', async () => {
      const { api } = await withScopeQuery('tools', { status: 'disabled' })
      expect(api.toRest()).toEqual({})
    })

    it('server on Tools never appears in toRest() (client-side only)', async () => {
      const { api } = await withScopeQuery('tools', { server: 'notes' })
      expect(api.toRest()).toEqual({})
    })

    it('from=-24h maps to window=24h on Usage', async () => {
      const { api } = await withScopeQuery('usage', { from: '-24h' })
      expect(api.toRest()).toEqual({ window: '24h' })
    })

    it('from=-7d maps to window=7d on Usage', async () => {
      const { api } = await withScopeQuery('usage', { from: '-7d' })
      expect(api.toRest()).toEqual({ window: '7d' })
    })

    it('no from/to maps to window=all on Usage', async () => {
      const { api } = await withScopeQuery('usage', {})
      expect(api.toRest()).toEqual({ window: 'all' })
    })

    it('an unsupported range is not sent on Usage', async () => {
      const { api } = await withScopeQuery('usage', { from: '-3d' })
      expect(api.toRest()).toEqual({})
    })

    it('from/to map to start_time/end_time on Activity, resolving relative shorthand', async () => {
      const { api } = await withScopeQuery('activity', { from: '-1h' })
      const rest = api.toRest()
      expect(rest.start_time).toBeDefined()
      expect(rest.start_time).not.toBe('-1h')
      expect(new Date(rest.start_time).getTime()).toBeLessThan(Date.now())
    })

    it('an absolute RFC3339 from/to passes through unchanged', async () => {
      const { api } = await withScopeQuery('activity', { from: '2025-01-01T00:00:00Z' })
      expect(api.toRest().start_time).toBe('2025-01-01T00:00:00Z')
    })

    it('view is never sent to REST directly (it maps to type client-side)', async () => {
      const { api } = await withScopeQuery('activity', { view: 'calls' })
      expect(api.toRest()).toEqual({})
    })
  })

  describe('profile/client/token gated behind features.scope_filters', () => {
    it('are hidden (no chip, not sent) while the feature is unavailable', async () => {
      const { api } = await withScopeQuery('activity', { profile: 'work', client: 'cursor', token: 'ci-bot' })
      expect(api.toRest()).toEqual({})
      expect(api.chips.value).toEqual([])
      // Kept untouched in the URL even while hidden.
      expect(api.state.profile).toBe('work')
    })

    it('behave like any sticky parameter once the feature is available', async () => {
      setAvailableFeatures(['scope_filters'])
      const { api } = await withScopeQuery('activity', { profile: 'work' })
      expect(api.toRest()).toEqual({ profile: 'work' })
      expect(api.chips.value.map(c => c.name)).toContain('profile')
    })
  })

  describe('linkTo() sticky carry', () => {
    it('carries from/to to another page', async () => {
      const { api } = await withScopeQuery('activity', { from: '-24h', server: 'github' })
      const link = api.linkTo('usage', { tool: 'github:search' })
      expect(link).toMatchObject({ name: 'usage', query: { from: '-24h', tool: 'github:search' } })
      // server is not sticky — page-specific params travel only when the link sets them.
      expect((link as { query: Record<string, string> }).query.server).toBeUndefined()
    })

    it('does not carry profile/client/token when the feature is unavailable', async () => {
      const { api } = await withScopeQuery('activity', { profile: 'work' })
      const link = api.linkTo('tools')
      expect((link as { query: Record<string, string> }).query.profile).toBeUndefined()
    })

    it('carries profile/client/token once the feature is available', async () => {
      setAvailableFeatures(['scope_filters'])
      const { api } = await withScopeQuery('activity', { profile: 'work' })
      const link = api.linkTo('tools')
      expect((link as { query: Record<string, string> }).query.profile).toBe('work')
    })
  })

  describe('chips', () => {
    it('one chip per active, available filter; remove() clears it', async () => {
      const { api, router } = await withScopeQuery('activity', { server: 'github', status: 'error' })
      expect(api.chips.value.map(c => c.name).sort()).toEqual(['server', 'status'])

      const serverChip = api.chips.value.find(c => c.name === 'server')!
      serverChip.remove()
      await flushPromises()
      expect(router.currentRoute.value.query.server).toBeUndefined()
      expect(router.currentRoute.value.query.status).toBe('error')
    })
  })
})

describe('pure helpers', () => {
  it('resolveScopeTime resolves relative shorthand against a fixed now', () => {
    const now = new Date('2026-09-26T12:00:00Z')
    expect(resolveScopeTime('-1h', now)).toBe('2026-09-26T11:00:00Z')
    expect(resolveScopeTime('-24h', now)).toBe('2026-09-25T12:00:00Z')
    expect(resolveScopeTime('-7d', now)).toBe('2026-09-19T12:00:00Z')
  })

  it('resolveScopeTime passes an absolute timestamp through unchanged', () => {
    expect(resolveScopeTime('2025-01-01T00:00:00Z')).toBe('2025-01-01T00:00:00Z')
  })

  it('usageWindowFor maps the three presets and rejects everything else', () => {
    expect(usageWindowFor(undefined, undefined)).toBe('all')
    expect(usageWindowFor('-24h', undefined)).toBe('24h')
    expect(usageWindowFor('-7d', undefined)).toBe('7d')
    expect(usageWindowFor('-3d', undefined)).toBeUndefined()
    expect(usageWindowFor('-24h', '2025-01-01T00:00:00Z')).toBeUndefined()
  })

  it('splitScopeTool splits server:tool and keeps a disagreeing server', () => {
    expect(splitScopeTool('github:create_issue', undefined)).toEqual({ server: 'github', tool: 'create_issue' })
    expect(splitScopeTool('github:create_issue', 'notes')).toEqual({ server: 'notes', tool: 'create_issue' })
    expect(splitScopeTool('search', undefined)).toEqual({ server: undefined, tool: 'search' })
  })

  it('sessionRestParam routes by the ws- prefix', () => {
    expect(sessionRestParam('ws-abc')).toBe('work_session_id')
    expect(sessionRestParam('raw-id')).toBe('session_id')
  })
})
