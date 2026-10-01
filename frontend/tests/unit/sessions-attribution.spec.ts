import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import SessionsPanel from '@/components/activity/SessionsPanel.vue'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { makeClient, makeProfile } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({ default: { getProfiles: vi.fn(), getClients: vi.fn() } }))

// Spec 108-j T105/T107 (FR-033; link map "Session row"): the Sessions view shows
// the credential and profile each session initialized with, and offers the
// tools that client sees.

const stub = { template: '<div />' }

function session(id: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    client_name: 'Cursor',
    status: 'active',
    start_time: '2026-09-30T10:00:00Z',
    last_activity: '2026-09-30T10:01:00Z',
    tool_call_count: 1,
    total_tokens: 10,
    ...extra,
  }
}

async function mountPanel(sessions: unknown[], path = '/activity?view=sessions') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
      { path: '/clients', name: 'clients', component: stub },
      { path: '/tokens', name: 'tokens', component: stub },
      { path: '/profiles/:name', name: 'profile-editor', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  useProfilesStore().profiles = [makeProfile('work-readonly', { title: 'Work' })] as any
  useClientsStore().clients = [makeClient('cursor')] as any
  const wrapper = mount(SessionsPanel, { props: { sessions: sessions as any }, global: { plugins: [router] } })
  await flushPromises()
  return { wrapper, router }
}

describe('Sessions attribution (Spec 108-j J11)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setAvailableFeatures(['profile', 'client', 'token'])
  })

  it('shows the client, the profile with how it was resolved, and the token', async () => {
    const { wrapper } = await mountPanel([session('s1', { client_id: 'cursor', profile: 'work-readonly', profile_source: 'pin', token_name: 'ro-bot' })])
    expect(wrapper.get('[data-test="sessions-client"]').text()).toBe('Cursor')
    expect(wrapper.get('[data-test="sessions-profile"]').text()).toBe('Work · locked by credential')
    expect(wrapper.get('[data-test="sessions-token"]').text()).toBe('ro-bot')
  })

  it("omits the client's own credential as a token", async () => {
    const { wrapper } = await mountPanel([session('s1', { client_id: 'cursor', profile: 'work-readonly', profile_source: 'pin', token_name: 'client-cursor' })])
    expect(wrapper.get('[data-test="sessions-token"]').text()).toBe('—')
  })

  it('a session with no client_id keeps its self-reported name and has no "Tools it sees"', async () => {
    const { wrapper } = await mountPanel([session('legacy')])
    expect(wrapper.get('[data-test="sessions-client"]').text()).toBe('Cursor')
    expect(wrapper.find('[data-test="sessions-tools-it-sees-legacy"]').exists()).toBe(false)
  })

  it('"Tools it sees" is present only with a client_id and resolves to the Tools page filtered by it', async () => {
    const { wrapper, router } = await mountPanel([session('s1', { client_id: 'cursor' }), session('s2')])
    const link = wrapper.get('[data-test="sessions-tools-it-sees-s1"]')
    expect(link.attributes('href')).toBe(router.resolve({ name: 'tools', query: { client: 'cursor' } }).fullPath)
    expect(wrapper.find('[data-test="sessions-tools-it-sees-s2"]').exists()).toBe(false)
  })

  it('"Tools it sees" is hidden while the client filter is not advertised (rule 7)', async () => {
    setAvailableFeatures([])
    const { wrapper } = await mountPanel([session('s1', { client_id: 'cursor' })])
    expect(wrapper.find('[data-test="sessions-tools-it-sees-s1"]').exists()).toBe(false)
  })

  it('"Tools it sees" needs the client filter itself: a build that advertises only profile and token hides it', async () => {
    setAvailableFeatures(['profile', 'token'])
    const { wrapper } = await mountPanel([session('s1', { client_id: 'cursor' })])
    expect(wrapper.find('[data-test="sessions-tools-it-sees-s1"]').exists()).toBe(false)
  })

  it('the empty state names the applied filter', async () => {
    const { wrapper } = await mountPanel([], '/activity?view=sessions&client=cursor')
    expect(wrapper.get('[data-test="sessions-empty-title"]').text()).toBe('No sessions for Client: Cursor')
  })

  it('without a filter the empty state is the plain one', async () => {
    const { wrapper } = await mountPanel([])
    expect(wrapper.get('[data-test="sessions-empty-title"]').text()).toBe('No sessions found')
  })
})
