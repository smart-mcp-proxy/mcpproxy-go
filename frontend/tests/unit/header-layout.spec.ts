import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 109-i FR-050/FR-053 / T136: the header is search, an optional
// "Viewing" slot, the status pill, the attention pill and "+ Add" — in that
// order. Mode and endpoints moved to Clients -> Endpoint & mode.

const attention = vi.hoisted(() => vi.fn())
const hasAPIKey = vi.hoisted(() => vi.fn(() => true))

vi.mock('@/services/api', () => {
  const ok = (data: unknown = null) => vi.fn().mockResolvedValue({ success: true, data })
  const base: Record<string, unknown> = {
    getAttention: attention,
    hasAPIKey,
    onAuthError: vi.fn(() => () => {}),
    getProfiles: vi.fn().mockResolvedValue({ success: true, data: { profiles: [] } }),
    getActiveProfile: vi.fn().mockResolvedValue({ success: true, data: { active_profile: '' } }),
  }
  return {
    default: new Proxy(base, {
      get(target: Record<string, unknown>, prop: string) {
        if (prop in target) return target[prop]
        target[prop] = ok()
        return target[prop]
      },
    }),
  }
})

import TopHeader from '@/components/TopHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useProfilesStore } from '@/stores/profiles'

const stub = { template: '<div />' }

function attentionItems(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: `sign_in_required:server:s${i}`, kind: 'sign_in_required', rank: 10,
    subject: { type: 'server', id: `s${i}`, name: `s${i}` }, summary: `s${i}`,
    fix: { verb: 'login', label: 'Sign in', target: `/servers/s${i}` }, since: '2026-09-25T06:00:00Z',
  }))
}

async function mountHeader(opts: { slots?: Record<string, string>; tenant?: boolean; count?: number } = {}) {
  attention.mockResolvedValue({ success: true, data: { count: opts.count ?? 0, items: attentionItems(opts.count ?? 0) } })
  if (opts.tenant) {
    hasAPIKey.mockReturnValue(false)
    const auth = useAuthStore()
    auth.isTeamsEdition = true
    auth.loading = false
    auth.authResolvedSuccessfully = true
    auth.user = { id: 'u', email: 'u@x', display_name: 'U', role: 'user', provider: 'oidc', created_at: '', last_login_at: '' } as any
  }
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: stub }, { path: '/servers', component: stub }, { path: '/tools', component: stub }],
  })
  router.push('/')
  await router.isReady()
  const wrapper = mount(TopHeader, {
    attachTo: document.body,
    slots: opts.slots,
    global: { plugins: [router] },
  })
  await flushPromises()
  return wrapper
}

const el = (wrapper: ReturnType<typeof mount>, id: string) => wrapper.get(`[data-test="${id}"]`).element

function before(a: Element, b: Element): boolean {
  return Boolean(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING)
}

describe('TopHeader layout (Spec 109-i FR-053)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    document.body.innerHTML = ''
    hasAPIKey.mockReturnValue(true)
  })

  it('orders search < viewing slot < status pill < attention pill < add menu', async () => {
    const wrapper = await mountHeader({ slots: { viewing: '<span data-test="viewing-content">V</span>' }, count: 2 })
    const order = ['header-search-input', 'header-viewing-slot', 'header-status-pill', 'header-attention-pill', 'header-add-menu']
    for (let i = 0; i < order.length - 1; i++) {
      expect(before(el(wrapper, order[i]), el(wrapper, order[i + 1])), `${order[i]} before ${order[i + 1]}`).toBe(true)
    }
    wrapper.unmount()
  })

  it('has no Search button, header Add Server button, ModeSwitcher or endpoints dropdown', async () => {
    const wrapper = await mountHeader()
    for (const gone of ['header-search-button', 'header-add-server', 'mode-switcher']) {
      expect(wrapper.find(`[data-test="${gone}"]`).exists(), gone).toBe(false)
    }
    expect(wrapper.findComponent({ name: 'ModeSwitcher' }).exists()).toBe(false)
    expect(wrapper.text()).not.toContain('MCP Endpoints')
    expect(wrapper.find('code').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders a single search input with a shortcut hint and a narrow-width icon button', async () => {
    const wrapper = await mountHeader()
    const input = wrapper.get('[data-test="header-search-input"]')
    expect(input.attributes('placeholder')).toBe('Search servers, tools, settings…')
    expect(input.attributes('aria-keyshortcuts')).toBe('Meta+K Control+K')
    expect(wrapper.get('[data-test="header-search-icon"]').attributes('aria-label')).toBe('Search')
    expect(wrapper.get('[data-test="header-search-hint"]').text()).toMatch(/⌘K|Ctrl K/)
    wrapper.unmount()
  })

  it('renders no viewing wrapper with no profiles and no slot content', async () => {
    const wrapper = await mountHeader()
    expect(wrapper.find('[data-test="header-viewing-slot"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders the slot instead of ProfileSwitcher when filled', async () => {
    useProfilesStore().profiles = [{ name: 'work', servers: ['a'], tool_count: 1 }] as any
    const wrapper = await mountHeader({ slots: { viewing: '<span data-test="viewing-content">V</span>' } })
    expect(wrapper.find('[data-test="viewing-content"]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the ProfileSwitcher interim as the slot fallback once profiles exist', async () => {
    const api = (await import('@/services/api')).default as any
    api.getProfiles.mockResolvedValue({ success: true, data: { profiles: [{ name: 'work', servers: ['a'], tool_count: 1 }] } })
    const wrapper = await mountHeader()
    expect(wrapper.find('[data-test="header-viewing-slot"]').exists()).toBe(true)
    expect(wrapper.findComponent({ name: 'ProfileSwitcher' }).exists()).toBe(true)
    wrapper.unmount()
  })

  it('opens the command palette when the search input is focused, seeding typed text', async () => {
    const wrapper = await mountHeader()
    const input = wrapper.get('[data-test="header-search-input"]')
    await input.setValue('notes')
    await input.trigger('focus')
    await flushPromises()
    const dialog = document.querySelector('dialog[data-test="command-palette"]')!
    expect(dialog.hasAttribute('open')).toBe(true)
    expect((dialog.querySelector('input') as HTMLInputElement).value).toBe('notes')
    expect((input.element as HTMLInputElement).value).toBe('')
    wrapper.unmount()
  })

  it('opens the palette from the narrow-width search icon', async () => {
    const wrapper = await mountHeader()
    await wrapper.get('[data-test="header-search-icon"]').trigger('click')
    await flushPromises()
    expect(document.querySelector('dialog[data-test="command-palette"]')!.hasAttribute('open')).toBe(true)
    wrapper.unmount()
  })

  it('shows the attention pill only above zero', async () => {
    const none = await mountHeader({ count: 0 })
    expect(none.find('[data-test="header-attention-pill"]').exists()).toBe(false)
    none.unmount()
  })

  it('hides the status pill and the add menu for a tenant', async () => {
    const wrapper = await mountHeader({ tenant: true })
    expect(wrapper.find('[data-test="header-status-pill"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="header-add-menu"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
