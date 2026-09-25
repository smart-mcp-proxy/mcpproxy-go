import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import AddServer from '@/views/AddServer.vue'

vi.mock('@/services/api', () => ({
  default: {
    catalogSearch: vi.fn().mockResolvedValue({ success: true, data: { query: '', results: [], sections: { official: [], popular: [] }, unavailable: [] } }),
    getConfigSecrets: vi.fn().mockResolvedValue({ success: true, data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true } }),
    getCanonicalConfigPaths: vi.fn().mockResolvedValue({ success: true, data: { os: 'darwin', paths: [] } }),
    addServerFromRegistry: vi.fn(),
  },
}))

const router = createRouter({
  history: createWebHistory(),
  routes: [{ path: '/add-server', name: 'add-server', component: AddServer }],
})

async function mountAtTab(tab?: string, extraQuery = '') {
  setActivePinia(createPinia())
  await router.push(`/add-server${tab ? `?tab=${tab}${extraQuery}` : extraQuery ? `?${extraQuery.slice(1)}` : ''}`)
  await router.isReady()
  const wrapper = mount(AddServer, { global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}

describe('AddServer tab <-> URL sync (Spec 109 FR-062)', () => {
  it('defaults to the Catalog tab when ?tab= is absent', async () => {
    const wrapper = await mountAtTab()
    expect(wrapper.find('[data-test="add-server-tab-catalog"]').classes()).toContain('tab-active')
    expect(wrapper.find('[data-test="catalog-search"]').exists()).toBe(true)
  })

  it('reads ?tab=paste on mount', async () => {
    const wrapper = await mountAtTab('paste')
    expect(wrapper.find('[data-test="add-server-tab-paste"]').classes()).toContain('tab-active')
    expect(wrapper.find('[data-test="paste-server"]').exists()).toBe(true)
  })

  it('reads ?tab=import on mount', async () => {
    const wrapper = await mountAtTab('import')
    expect(wrapper.find('[data-test="import-servers-panel"]').exists()).toBe(true)
  })

  it('reads ?tab=manual on mount', async () => {
    const wrapper = await mountAtTab('manual')
    expect(wrapper.find('[data-test="manual-server-form"]').exists()).toBe(true)
  })

  it('writes the tab to the URL with router.replace when clicked, keeping other params', async () => {
    const wrapper = await mountAtTab('catalog', '&source=official')
    await wrapper.find('[data-test="add-server-tab-manual"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('manual')
    expect(router.currentRoute.value.query.source).toBe('official')
  })

  it('?source= narrows the Catalog tab only, never selects a tab', async () => {
    const wrapper = await mountAtTab(undefined, '?source=official')
    // Still defaults to Catalog even though only ?source= was set — source
    // never implies a tab.
    expect(wrapper.find('[data-test="add-server-tab-catalog"]').classes()).toContain('tab-active')
  })
})
