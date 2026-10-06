import { describe, it, expect, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'
import {
  useScopeQuery,
  setAvailableFeatures,
  isScopeParamAvailable,
  type PageId,
  type UseScopeQueryResult,
} from '@/composables/useScopeQuery'

// Spec 108-e: GET /api/v1/status `features.scope_filters` is the list of
// parameter NAMES the backend accepts. The composable must un-hide exactly the
// listed names, and must never send a parameter to an endpoint that does not
// honour it. Spec 108-f makes GET /clients honour profile and client and GET
// /tokens honour profile and token; each still answers 400
// unsupported_scope_filter for the names it does not.

async function scopeFor(page: PageId, query: Record<string, string>): Promise<UseScopeQueryResult> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: `/${page}`, name: page, component: { template: '<div/>' } }],
  })
  let api!: UseScopeQueryResult
  const Harness = defineComponent({
    setup() {
      api = useScopeQuery(page)
      return () => h('div')
    },
  })
  await router.push(`/${page}?${new URLSearchParams(query).toString()}`)
  mount(Harness, { global: { plugins: [router] } })
  await router.isReady()
  return api
}

describe('scope filter availability (Spec 108-e)', () => {
  afterEach(() => setAvailableFeatures([]))

  it('the names the backend lists are what un-hides each parameter', () => {
    setAvailableFeatures([])
    expect(isScopeParamAvailable('profile')).toBe(false)

    // Exactly what the real backend advertises.
    setAvailableFeatures(['profile', 'client', 'token'])
    expect(isScopeParamAvailable('profile')).toBe(true)
    expect(isScopeParamAvailable('client')).toBe(true)
    expect(isScopeParamAvailable('token')).toBe(true)

    setAvailableFeatures(['profile'])
    expect(isScopeParamAvailable('profile')).toBe(true)
    expect(isScopeParamAvailable('client')).toBe(false)
  })

  it('sends profile/client/token to the endpoints that honour them', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const q = { profile: 'work-readonly', client: 'cursor', token: 'client-cursor' }
    expect((await scopeFor('activity', q)).toRest()).toMatchObject(q)
    expect((await scopeFor('usage', q)).toRest()).toMatchObject(q)
    expect((await scopeFor('tools', q)).toRest()).toEqual({ profile: 'work-readonly', client: 'cursor' })
    expect((await scopeFor('servers', q)).toRest()).toEqual({ profile: 'work-readonly' })
  })

  it('the Clients and Tokens pages send exactly the parameters their endpoint honours', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const q = { profile: 'work-readonly', client: 'cursor', token: 'ci-bot' }
    // Spec 108-f: GET /clients honours profile + client, GET /tokens honours
    // profile + token. The third name would be a 400 unsupported_scope_filter.
    expect((await scopeFor('clients', q)).toRest()).toEqual({ profile: 'work-readonly', client: 'cursor' })
    expect((await scopeFor('tokens', q)).toRest()).toEqual({ profile: 'work-readonly', token: 'ci-bot' })
  })

  it('the Clients and Tokens pages still show the active filters as chips', async () => {
    setAvailableFeatures(['profile', 'client', 'token'])
    const clients = await scopeFor('clients', { profile: 'work-readonly', client: 'cursor' })
    expect(clients.chips.value.map(c => c.name).sort()).toEqual(['client', 'profile'])
    const tokens = await scopeFor('tokens', { profile: 'work-readonly', token: 'ci-bot' })
    expect(tokens.chips.value.map(c => c.name).sort()).toEqual(['profile', 'token'])
  })
})
