import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentTokens from '@/views/AgentTokens.vue'
import api from '@/services/api'
import { setAvailableFeatures } from '@/composables/useScopeQuery'
import { WORK_FULL, WORK_RO, apiError } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    listAgentTokens: vi.fn(),
    createAgentToken: vi.fn(),
    getServers: vi.fn(),
    getProfiles: vi.fn(),
  },
}))

const stub = { template: '<div />' }
const future = '2030-01-01T00:00:00Z'

const TOKENS = [
  { name: 'ci', token_prefix: 'mcp_agt_aa', allowed_servers: ['*'], permissions: ['read', 'write', 'destructive'], expires_at: future, created_at: future, last_used_at: null, revoked: false, profile_pin: 'work-ro', kind: 'agent', legacy_scope: false },
  { name: 'old-bot', token_prefix: 'mcp_agt_bb', allowed_servers: ['github'], permissions: ['read'], expires_at: future, created_at: future, last_used_at: null, revoked: false, kind: 'agent', legacy_scope: true },
  { name: 'client-cursor', token_prefix: 'mcp_cli_cc', allowed_servers: ['*'], permissions: ['read', 'write', 'destructive'], expires_at: future, created_at: future, last_used_at: null, revoked: true, profile_pin: 'work-ro', kind: 'client', client_id: 'cursor', profile_mode: 'locked', legacy_scope: false },
]

async function mountTokens(url = '/clients?tab=tokens') {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/clients', name: 'clients', component: stub }, { path: '/profiles', name: 'profiles', component: stub }] })
  await router.push(url)
  await router.isReady()
  const wrapper = mount(AgentTokens, { global: { plugins: [router] }, attachTo: document.body })
  await new Promise(resolve => setTimeout(resolve, 130))
  await flushPromises()
  return { wrapper, router }
}

async function openDialog(wrapper: ReturnType<typeof mount>) {
  await wrapper.find('button.btn-primary').trigger('click')
  await flushPromises()
}

describe('Token dialog and list (Spec 108-i T093, FR-043)', () => {
  const original = (HTMLDialogElement.prototype as any).showModal

  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    ;(HTMLDialogElement.prototype as any).showModal = vi.fn()
    setAvailableFeatures(['scope_filters'])
    ;(api.listAgentTokens as any).mockResolvedValue({ success: true, data: { tokens: TOKENS } })
    ;(api.getServers as any).mockResolvedValue({ success: true, data: { servers: [{ name: 'github', enabled: true, tool_count: 1 }] } })
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [WORK_RO, WORK_FULL] })
    ;(api.createAgentToken as any).mockResolvedValue({ success: true, data: { name: 'new', token: 'mcp_agt_secret', allowed_servers: ['*'], permissions: ['read'], expires_at: future, created_at: future } })
  })
  afterEach(() => {
    ;(HTMLDialogElement.prototype as any).showModal = original
  })

  it('puts the profile select first and enables the legacy disclosure only for "None — legacy scope"', async () => {
    const { wrapper } = await mountTokens()
    await openDialog(wrapper)
    const select = wrapper.get('[data-test="token-profile-select"]')
    const labels = select.findAll('option').map(option => option.text())
    expect(labels).toEqual(['Choose…', 'Work (work-ro)', 'Work Full (work-full)', 'None — legacy scope'])
    const legacy = wrapper.get('[data-test="token-legacy-scope"] fieldset')
    expect(legacy.attributes('disabled')).toBeDefined()
    await select.setValue('__legacy__')
    expect(wrapper.get('[data-test="token-legacy-scope"] fieldset').attributes('disabled')).toBeUndefined()
    await select.setValue('work-ro')
    expect(wrapper.get('[data-test="token-legacy-scope"] fieldset').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-test="token-legacy-scope"] summary').text()).toBe('Legacy scope (advanced)')
  })

  it('submit requires a choice, and a profile body is {name, profile, expires_in} only', async () => {
    const { wrapper } = await mountTokens()
    await openDialog(wrapper)
    await wrapper.get('#token-name').setValue('ci-pipeline')
    const create = wrapper.findAll('dialog button.btn-primary').find(button => button.text().includes('Create Token'))!
    await create.trigger('click')
    await flushPromises()
    expect(api.createAgentToken).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Choose a profile')

    await wrapper.get('[data-test="token-profile-select"]').setValue('work-ro')
    await create.trigger('click')
    await flushPromises()
    expect(api.createAgentToken).toHaveBeenCalledTimes(1)
    const body = (api.createAgentToken as any).mock.calls[0][0]
    expect(body).toEqual({ name: 'ci-pipeline', profile: 'work-ro', expires_in: '720h' })
    expect('allowed_servers' in body).toBe(false)
    expect('permissions' in body).toBe(false)
  })

  it('the legacy body keeps allowed_servers and permissions unchanged', async () => {
    const { wrapper } = await mountTokens()
    await openDialog(wrapper)
    await wrapper.get('#token-name').setValue('legacy-bot')
    await wrapper.get('[data-test="token-profile-select"]').setValue('__legacy__')
    const create = wrapper.findAll('dialog button.btn-primary').find(button => button.text().includes('Create Token'))!
    await create.trigger('click')
    await flushPromises()
    expect((api.createAgentToken as any).mock.calls[0][0]).toEqual({ name: 'legacy-bot', allowed_servers: ['*'], permissions: ['read'], expires_in: '720h' })
  })

  it('shows the reserved client- prefix error inline under Name', async () => {
    ;(api.createAgentToken as any).mockRejectedValue(apiError('token names starting with "client-" are reserved for client credentials', { field: 'name', status: 400 }))
    const { wrapper } = await mountTokens()
    await openDialog(wrapper)
    await wrapper.get('#token-name').setValue('client-x')
    await wrapper.get('[data-test="token-profile-select"]').setValue('work-ro')
    await wrapper.findAll('dialog button.btn-primary').find(button => button.text().includes('Create Token'))!.trigger('click')
    await flushPromises()
    expect(wrapper.get('#token-name').classes()).toContain('input-error')
    expect(wrapper.text()).toContain('reserved for client credentials')
  })

  it('presets the profile from ?profile=, which is also the list filter sent to the server', async () => {
    const { wrapper } = await mountTokens('/clients?tab=tokens&create=1&profile=work-full')
    expect((wrapper.get('[data-test="token-profile-select"]').element as HTMLSelectElement).value).toBe('work-full')
    expect(api.listAgentTokens).toHaveBeenCalledWith({ profile: 'work-full', token: undefined })
  })

  it('lists Kind, Profile, Mode and the Legacy scope badge', async () => {
    const { wrapper } = await mountTokens()
    expect(wrapper.findAll('thead th').map(th => th.text())).toEqual(['Name', 'Kind', 'Profile', 'Mode', 'Prefix', 'Expires', 'Last Used', 'Status', 'Actions'])
    expect(wrapper.get('[data-test="token-kind-ci"]').text()).toBe('Agent')
    expect(wrapper.get('[data-test="token-profile-ci"]').text()).toBe('Work')
    expect(wrapper.get('[data-test="token-kind-client-cursor"]').text()).toBe('Client')
    expect(wrapper.get('[data-test="token-row-client-cursor"]').text()).toContain('Locked')
    expect(wrapper.find('[data-test="token-legacy-badge-ci"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="token-legacy-badge-old-bot"]').text()).toBe('Legacy scope')
  })

  it('a legacy row expands to its scope read-only with the migrate hint and a prefilled dialog', async () => {
    const { wrapper } = await mountTokens()
    await wrapper.get('[data-test="token-expand-old-bot"]').trigger('click')
    const detail = wrapper.get('[data-test="token-detail-old-bot"]')
    expect(detail.text()).toContain('github')
    expect(detail.text()).toContain('read')
    expect(detail.get('[data-test="token-migrate-old-bot"]').text()).toContain('Migrate: create a new token with a profile.')
    expect(detail.find('input').exists()).toBe(false)
    await detail.get('[data-test="token-migrate-button-old-bot"]').trigger('click')
    await flushPromises()
    expect((wrapper.get('#token-name').element as HTMLInputElement).value).toBe('old-bot-profile')
  })

  it('client credential rows say "manage from Clients" and hide Regenerate, Revoke and Delete', async () => {
    const { wrapper } = await mountTokens()
    const row = wrapper.get('[data-test="token-row-client-cursor"]')
    expect(row.get('[data-test="token-client-note-client-cursor"]').text()).toContain('Client credential for cursor')
    expect(row.get('[data-test="token-client-note-client-cursor"]').text()).toContain('manage from Clients')
    expect(row.findAll('button').map(button => button.text())).toEqual(['client-cursor'])
    // A regular token keeps its actions.
    expect(wrapper.get('[data-test="token-row-ci"]').text()).toContain('Regenerate')
    expect(wrapper.get('[data-test="token-row-ci"]').text()).not.toContain('Delete')
  })

  it('shows the no-profiles hint and offers only the legacy choice', async () => {
    ;(api.getProfiles as any).mockResolvedValue({ profiles: [] })
    const { wrapper } = await mountTokens()
    await openDialog(wrapper)
    expect(wrapper.get('[data-test="token-no-profiles-hint"]').text()).toBe('Create a profile to scope tokens simply')
    expect(wrapper.get('[data-test="token-profile-select"]').findAll('option').map(option => option.text())).toEqual(['Choose…', 'None — legacy scope'])
  })
})
