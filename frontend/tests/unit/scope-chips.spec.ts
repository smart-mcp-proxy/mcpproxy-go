import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import ScopeChips from '@/components/scope/ScopeChips.vue'
import { setAvailableFeatures, useScopeQuery, type PageId } from '@/composables/useScopeQuery'
import { useClientsStore } from '@/stores/clients'
import { useProfilesStore } from '@/stores/profiles'
import { WORK_RO, makeClient } from './fixtures/profiles108i'

vi.mock('@/services/api', () => ({ default: { getProfiles: vi.fn(), getClients: vi.fn() } }))

// Spec 108-j J4 (rules 3, 5, 7): the removable profile/client/token chips.

const stub = { template: '<div />' }

async function mountChips(path: string, page: PageId, props: Record<string, unknown> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/activity', name: 'activity', component: stub },
      { path: '/tools', name: 'tools', component: stub },
    ],
  })
  await router.push(path)
  await router.isReady()
  const Host = defineComponent({
    setup() {
      const scopeQuery = useScopeQuery(page)
      return () => h(ScopeChips, { page, scopeQuery, ...props })
    },
  })
  const wrapper = mount(Host, { global: { plugins: [router] } })
  useProfilesStore().profiles = [WORK_RO] as any
  useClientsStore().clients = [makeClient('cursor')] as any
  await flushPromises()
  return { wrapper, router }
}

describe('ScopeChips (Spec 108-j J4)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    setAvailableFeatures(['profile', 'client', 'token'])
  })

  it('labels come from the stores: profile title, client display name, token name', async () => {
    const { wrapper } = await mountChips('/activity?profile=work-ro&client=cursor&token=ro-bot', 'activity')
    expect(wrapper.get('[data-test="scope-chip-profile"]').text()).toContain('Profile: Work')
    expect(wrapper.get('[data-test="scope-chip-client"]').text()).toContain('Client: Cursor')
    expect(wrapper.get('[data-test="scope-chip-token"]').text()).toContain('Token: ro-bot')
  })

  it('an unknown value falls back to the raw id; - renders as unattributed', async () => {
    const { wrapper } = await mountChips('/activity?client=ghost&profile=-', 'activity')
    expect(wrapper.get('[data-test="scope-chip-client"]').text()).toContain('Client: ghost')
    expect(wrapper.get('[data-test="scope-chip-profile"]').text()).toContain('Profile: unattributed')
  })

  it('every remove button has an aria-label naming the filter, and removing writes the URL without it', async () => {
    const { wrapper, router } = await mountChips('/activity?client=cursor&status=blocked', 'activity')
    const remove = wrapper.get('[data-test="scope-chip-remove-client"]')
    expect(remove.attributes('aria-label')).toBe('Remove Client: Cursor filter')
    await remove.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ status: 'blocked' })
  })

  it('rule 5: a sticky param the page does not register renders as a disabled "not applicable here" chip (token on Tools)', async () => {
    const { wrapper, router } = await mountChips('/tools?token=ro-bot&client=cursor', 'tools')
    const na = wrapper.get('[data-test="scope-chip-na-token"]')
    expect(na.attributes('aria-disabled')).toBe('true')
    expect(na.text()).toContain('not applicable here')
    expect(wrapper.find('[data-test="scope-chip-client"]').exists()).toBe(true)
    await wrapper.get('[data-test="scope-chip-remove-token"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ client: 'cursor' })
  })

  it('a disabled explanation renders the chip disabled with the sentence in visible text', async () => {
    const { wrapper } = await mountChips('/tools?client=-', 'tools', { disabled: { client: 'Unattributed applies to Activity and Usage only' } })
    const chip = wrapper.get('[data-test="scope-chip-client"]')
    expect(chip.attributes('aria-disabled')).toBe('true')
    expect(chip.text()).toContain('Unattributed applies to Activity and Usage only')
  })

  it('conflicting chips are marked', async () => {
    const { wrapper } = await mountChips('/tools?client=cursor&profile=work-ro', 'tools', { conflicting: ['client', 'profile'] })
    expect(wrapper.get('[data-test="scope-chip-client"]').attributes('data-conflicting')).toBe('true')
    expect(wrapper.get('[data-test="scope-chip-profile"]').attributes('data-conflicting')).toBe('true')
  })

  it('rule 7: nothing renders while the build does not advertise the filters', async () => {
    setAvailableFeatures([])
    const { wrapper } = await mountChips('/activity?client=cursor&token=x', 'activity')
    expect(wrapper.find('[data-test="scope-chips"]').exists()).toBe(false)
  })
})
