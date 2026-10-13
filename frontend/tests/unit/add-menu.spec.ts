import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'
import { defineComponent, h } from 'vue'

// Spec 109-i FR-052 / T136: the header "+ Add" menu — Server, Client, Token,
// (Profile once /profiles exists). Server edition admins get a single
// "Personal server" item; tenants get no menu at all.

vi.mock('@/services/api', () => ({
  default: {
    hasAPIKey: vi.fn(() => true),
    getClients: vi.fn().mockResolvedValue({ success: true, data: { clients: [] } }),
  },
}))

import AddMenu from '@/components/AddMenu.vue'
import { useAuthStore } from '@/stores/auth'
import { useClientsStore } from '@/stores/clients'
import api from '@/services/api'

const stub = { template: '<div />' }

// Stands in for ClientConnectList: records its props and can emit `updated`.
const ConnectStub = defineComponent({
  name: 'ClientConnectList',
  props: { show: Boolean },
  emits: ['close', 'updated'],
  setup(props) {
    return () => h('div', { 'data-test': 'connect-stub', 'data-show': String(props.show) })
  },
})

async function mountMenu(opts: { profiles?: boolean } = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stub },
      { path: '/add-server', component: stub },
      { path: '/clients', component: stub },
      ...(opts.profiles ? [{ path: '/profiles', component: stub }] : []),
    ],
  })
  router.push('/')
  await router.isReady()
  const push = vi.spyOn(router, 'push')
  const wrapper = mount(AddMenu, {
    attachTo: document.body,
    global: { plugins: [router], stubs: { ClientConnectList: ConnectStub } },
  })
  await flushPromises()
  return { wrapper, router, push }
}

function items(wrapper: ReturnType<typeof mount>): string[] {
  return wrapper.findAll('[data-test^="add-menu-"]').map((el) => el.attributes('data-test')!.replace('add-menu-', ''))
}

describe('AddMenu (Spec 109-i FR-052)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.body.innerHTML = ''
  })

  it('opens to Server, Client, Token and no Profile without a /profiles route', async () => {
    const { wrapper } = await mountMenu()
    const button = wrapper.get('[data-test="header-add-menu"]')
    expect(button.attributes('aria-haspopup')).toBe('menu')
    expect(button.attributes('aria-expanded')).toBe('false')
    expect(button.attributes('aria-label')).toBe('Add')
    await button.trigger('click')
    expect(button.attributes('aria-expanded')).toBe('true')
    expect(items(wrapper)).toEqual(['server', 'client', 'token'])
    expect(wrapper.get('[role="menu"]').exists()).toBe(true)
    expect(wrapper.findAll('[role="menuitem"]')).toHaveLength(3)
    wrapper.unmount()
  })

  it('adds a Profile item targeting /profiles?create=1 once the route exists', async () => {
    const { wrapper, push } = await mountMenu({ profiles: true })
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    expect(items(wrapper)).toEqual(['server', 'client', 'token', 'profile'])
    await wrapper.get('[data-test="add-menu-profile"]').trigger('click')
    expect(push).toHaveBeenCalledWith({ path: '/profiles', query: { create: '1' } })
    wrapper.unmount()
  })

  it('Server pushes /add-server and closes the menu', async () => {
    const { wrapper, push } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    await wrapper.get('[data-test="add-menu-server"]').trigger('click')
    expect(push).toHaveBeenCalledWith('/add-server')
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('Token pushes the Clients tokens tab with create=1', async () => {
    const { wrapper, push } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    await wrapper.get('[data-test="add-menu-token"]').trigger('click')
    expect(push).toHaveBeenCalledWith({ path: '/clients', query: { tab: 'tokens', create: '1' } })
    wrapper.unmount()
  })

  it('Client opens the connect dialog lazily and refreshes presence on updated', async () => {
    const { wrapper } = await mountMenu()
    expect(wrapper.find('[data-test="connect-stub"]').exists()).toBe(false)
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    await wrapper.get('[data-test="add-menu-client"]').trigger('click')
    const connect = wrapper.getComponent(ConnectStub)
    expect(connect.props('show')).toBe(true)

    const refresh = vi.spyOn(useClientsStore(), 'refreshPresence')
    connect.vm.$emit('updated')
    await flushPromises()
    expect(refresh).toHaveBeenCalled()
    expect(api.getClients).toHaveBeenCalled()

    connect.vm.$emit('close')
    await flushPromises()
    expect(wrapper.getComponent(ConnectStub).props('show')).toBe(false)
    wrapper.unmount()
  })

  it('Escape closes the menu', async () => {
    const { wrapper } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    expect(wrapper.find('[role="menu"]').exists()).toBe(true)
    await wrapper.get('[data-test="header-add-menu"]').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('an outside click closes the menu', async () => {
    const { wrapper } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
    await flushPromises()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('arrow keys move focus between items', async () => {
    const { wrapper } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    await flushPromises()
    const menu = wrapper.get('[role="menu"]')
    const els = wrapper.findAll('[role="menuitem"]').map((w) => w.element as HTMLElement)
    expect(document.activeElement).toBe(els[0])
    await menu.trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement).toBe(els[1])
    await menu.trigger('keydown', { key: 'ArrowUp' })
    await menu.trigger('keydown', { key: 'ArrowUp' })
    expect(document.activeElement).toBe(els[els.length - 1])
    wrapper.unmount()
  })

  it('a server-edition admin sees only "Personal server"', async () => {
    const auth = useAuthStore()
    ;(api.hasAPIKey as any).mockReturnValue(false)
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'a', email: 'a@x', display_name: 'A', role: 'admin', provider: 'oidc', created_at: '', last_login_at: '' } as any
    const { wrapper } = await mountMenu()
    await wrapper.get('[data-test="header-add-menu"]').trigger('click')
    expect(items(wrapper)).toEqual(['server'])
    expect(wrapper.get('[data-test="add-menu-server"]').text()).toBe('Personal server')
    wrapper.unmount()
    ;(api.hasAPIKey as any).mockReturnValue(true)
  })

  it('renders no menu for a tenant', async () => {
    const auth = useAuthStore()
    ;(api.hasAPIKey as any).mockReturnValue(false)
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
    const { wrapper } = await mountMenu()
    expect(wrapper.find('[data-test="header-add-menu"]').exists()).toBe(false)
    wrapper.unmount()
    ;(api.hasAPIKey as any).mockReturnValue(true)
  })
})
