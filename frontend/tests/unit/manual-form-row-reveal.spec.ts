import { describe, it, expect, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createWebHistory } from 'vue-router'
import ManualServerForm from '@/components/ManualServerForm.vue'

vi.mock('@/services/api', () => ({
  default: {
    getConfigSecrets: vi.fn(),
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
    callTool: vi.fn(),
    getServers: vi.fn(),
  },
}))
import api from '@/services/api'

// Spec 109 FR-065 / US5-5 (fix-usertest-web review F3.1): the Show/Hide state
// belongs to one row. Removing an earlier row must not hand its revealed
// state to the next row, which would render that credential in plaintext
// without the user ever clicking Show for it.

const router = createRouter({
  history: createWebHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }],
})

describe('ManualServerForm secret rows', () => {
  it('does not carry a revealed flag onto the next row after removing the revealed one', async () => {
    setActivePinia(createPinia())
    vi.mocked(api.getConfigSecrets).mockResolvedValue({
      success: true,
      data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
    })
    const wrapper = mount(ManualServerForm, { global: { plugins: [router] } })
    await flushPromises()

    await wrapper.find('[data-test="manual-type-stdio"]').setValue(true)
    await wrapper.find('[data-test="manual-env-add"]').trigger('click')
    await wrapper.find('[data-test="manual-env-add"]').trigger('click')
    await wrapper.find('[data-test="manual-env-name-0"]').setValue('API_TOKEN')
    await wrapper.find('[data-test="manual-env-name-1"]').setValue('OTHER_SECRET')

    const inputs = () => wrapper.findAll('[data-test="secret-toggle-value-input"]')
    await wrapper.findAll('[data-test="secret-toggle-reveal"]')[0].trigger('click')
    expect(inputs()[0].attributes('type')).toBe('text')
    expect(inputs()[1].attributes('type')).toBe('password')

    await wrapper.findAll('[data-test="manual-env-remove"]')[0].trigger('click')
    await flushPromises()

    expect(inputs()).toHaveLength(1)
    expect(inputs()[0].attributes('type')).toBe('password')
  })
})

describe('ManualServerForm value input accessible names (UX-09)', () => {
  it('gives every header/env value input a unique row-tied name, also after a removal and in Secret mode', async () => {
    setActivePinia(createPinia())
    vi.mocked(api.getConfigSecrets).mockResolvedValue({
      success: true,
      data: { secrets: [], environment_vars: [], total_secrets: 0, total_env_vars: 0, keyring_available: true },
    })
    const wrapper = mount(ManualServerForm, { global: { plugins: [router] } })
    await flushPromises()
    await wrapper.find('[data-test="manual-type-http"]').setValue(true)
    for (let i = 0; i < 3; i++) await wrapper.find('[data-test="manual-header-add"]').trigger('click')
    // Duplicate and empty names must still be distinguishable.
    await wrapper.find('[data-test="manual-header-name-0"]').setValue('X-Dup')
    await wrapper.find('[data-test="manual-header-name-1"]').setValue('X-Dup')
    const names = () => wrapper.findAll('[data-test="secret-toggle-value-input"]').map(i => i.attributes('aria-label'))
    expect(names()).toEqual(['Header 1 value', 'Header 2 value', 'Header 3 value'])
    await wrapper.findAll('[data-test="secret-toggle-mode-secret"]')[1].trigger('click')
    expect(names()).toEqual(['Header 1 value', 'Header 2 value', 'Header 3 value'])
    await wrapper.findAll('[data-test="manual-header-remove"]')[0].trigger('click')
    await flushPromises()
    expect(names()).toEqual(['Header 1 value', 'Header 2 value'])
    const inputs = wrapper.findAll('[data-test="secret-toggle-value-input"]')
    expect(new Set(inputs.map(i => i.attributes('id'))).size).toBe(2)
    expect(inputs[0].attributes('placeholder')).toBeTruthy()
    // env rows
    await wrapper.find('[data-test="manual-type-stdio"]').setValue(true)
    for (let i = 0; i < 2; i++) await wrapper.find('[data-test="manual-env-add"]').trigger('click')
    expect(names()).toEqual(['Environment variable 1 value', 'Environment variable 2 value'])
  })
})
