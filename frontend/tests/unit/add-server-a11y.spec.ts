import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import AddServer from '@/views/AddServer.vue'
import ManualServerForm from '@/components/ManualServerForm.vue'

// UX-09: the Manual form's inputs have programmatic names and the Add modes
// are real, keyboard-operable tabs with associated panels.
vi.mock('@/services/api', () => ({
  default: {
    catalogSearch: vi.fn().mockResolvedValue({ success: true, data: { query: '', results: [], sections: { official: [], popular: [] }, unavailable: [] } }),
    getConfigSecrets: vi.fn().mockResolvedValue({ success: true, data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true } }),
    getCanonicalConfigPaths: vi.fn().mockResolvedValue({ success: true, data: { os: 'darwin', paths: [] } }),
    addServerFromRegistry: vi.fn(),
    callTool: vi.fn().mockResolvedValue({ success: false, error: 'boom' }),
    getServers: vi.fn().mockResolvedValue({ success: true, data: { servers: [] } }),
  },
}))

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/add-server', name: 'add-server', component: AddServer },
    { path: '/servers/:serverName', component: { template: '<div />' } },
  ],
})

async function mountAdd(tab = 'manual') {
  setActivePinia(createPinia())
  await router.push(`/add-server?tab=${tab}`)
  await router.isReady()
  const wrapper = mount(AddServer, { attachTo: document.body, global: { plugins: [router] } })
  await flushPromises()
  return wrapper
}
const labelFor = (wrapper: ReturnType<typeof mount>, text: string) => {
  const label = wrapper.findAll('label').find((l) => l.text().trim() === text)
  expect(label, `label ${text}`).toBeTruthy()
  const id = label!.attributes('for')
  expect(id, `label ${text} has for=`).toBeTruthy()
  return wrapper.get(`[id="${id}"]`)
}

describe('Manual server form accessible names (UX-09)', () => {
  it('ties Name, Command and Arguments labels to their inputs', async () => {
    const wrapper = await mountAdd()
    expect(labelFor(wrapper, 'Name').attributes('data-test')).toBe('manual-name-input')
    expect(labelFor(wrapper, 'Command').attributes('data-test')).toBe('manual-command-input')
    expect(labelFor(wrapper, 'Arguments (space-separated)').attributes('data-test')).toBe('manual-args-input')
    wrapper.unmount()
  })

  it('ties the HTTP URL label, groups the server type radios and names header rows', async () => {
    const wrapper = await mountAdd()
    expect(wrapper.get('fieldset legend').text()).toBe('Server Type')
    expect(wrapper.findAll('fieldset input[type="radio"]')).toHaveLength(2)
    await wrapper.get('[data-test="manual-type-http"]').setValue(true)
    expect(labelFor(wrapper, 'URL').attributes('data-test')).toBe('manual-url-input')
    await wrapper.get('[data-test="manual-header-add"]').trigger('click')
    expect(wrapper.get('[data-test="manual-header-name-0"]').attributes('aria-label')).toBe('Header name 1')
    expect(wrapper.get('[data-test="manual-header-remove"]').attributes('aria-label')).toBe('Remove header 1')
    wrapper.unmount()
  })

  it('names environment variable rows', async () => {
    const wrapper = await mountAdd()
    await wrapper.get('[data-test="manual-env-add"]').trigger('click')
    expect(wrapper.get('[data-test="manual-env-name-0"]').attributes('aria-label')).toBe('Environment variable name 1')
    expect(wrapper.get('[data-test="manual-env-remove"]').attributes('aria-label')).toBe('Remove environment variable 1')
    wrapper.unmount()
  })

  it('uses unique ids when two forms are mounted in one app', () => {
    setActivePinia(createPinia())
    const Both = defineComponent({ components: { ManualServerForm }, template: '<div><ManualServerForm /><ManualServerForm /></div>' })
    const wrapper = mount(Both, { global: { plugins: [router] } })
    const ids = wrapper.findAll('[data-test="manual-name-input"]').map((i) => i.attributes('id'))
    expect(ids).toHaveLength(2)
    expect(ids[0]).not.toBe(ids[1])
    wrapper.unmount()
  })

  it('announces a failed submit and moves focus to the alert', async () => {
    const wrapper = await mountAdd()
    await wrapper.get('[data-test="manual-name-input"]').setValue('x')
    await wrapper.get('[data-test="manual-command-input"]').setValue('npx')
    await wrapper.get('[data-test="manual-server-form"]').trigger('submit')
    await flushPromises()
    const alert = wrapper.get('[data-test="manual-error"]')
    expect(alert.attributes('role')).toBe('alert')
    expect(document.activeElement).toBe(alert.element)
    wrapper.unmount()
  })
})

describe('Add server tablist (UX-09)', () => {
  beforeEach(() => { vi.clearAllMocks() })

  it('exposes four tabs, one selected, each controlling a panel', async () => {
    const wrapper = await mountAdd('paste')
    const list = wrapper.get('[role="tablist"]')
    expect(list.attributes('aria-label')).toBe('Add server method')
    const tabs = wrapper.findAll('[role="tab"]')
    expect(tabs.map((t) => t.text())).toEqual(['Catalog', 'Paste', 'Import', 'Manual'])
    expect(tabs.map((t) => t.attributes('aria-selected'))).toEqual(['false', 'true', 'false', 'false'])
    expect(tabs.map((t) => t.attributes('tabindex'))).toEqual(['-1', '0', '-1', '-1'])
    const panel = wrapper.get('[role="tabpanel"]')
    expect(panel.attributes('id')).toBe(tabs[1].attributes('aria-controls'))
    expect(panel.attributes('aria-labelledby')).toBe(tabs[1].attributes('id'))
    wrapper.unmount()
  })

  it('moves selection and focus with Arrow, Home and End, and keeps ?tab= in sync', async () => {
    const wrapper = await mountAdd('catalog')
    const tablist = wrapper.get('[role="tablist"]')
    await tablist.trigger('keydown', { key: 'ArrowRight' }); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('paste')
    expect(document.activeElement?.getAttribute('data-test')).toBe('add-server-tab-paste')
    await tablist.trigger('keydown', { key: 'End' }); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('manual')
    await tablist.trigger('keydown', { key: 'ArrowRight' }); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('catalog')
    await tablist.trigger('keydown', { key: 'ArrowLeft' }); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('manual')
    await tablist.trigger('keydown', { key: 'Home' }); await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('catalog')
    expect(wrapper.get('[data-test="add-server-tab-catalog"]').attributes('aria-selected')).toBe('true')
    wrapper.unmount()
  })
})
