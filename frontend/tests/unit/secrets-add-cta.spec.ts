import { describe, it, expect, beforeEach, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import Secrets from '@/views/Secrets.vue'
import api from '@/services/api'

vi.mock('@/services/api', () => ({
  default: {
    getConfigSecrets: vi.fn(),
    runMigrationAnalysis: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
  },
}))

function emptySecrets() {
  return {
    success: true,
    data: {
      secrets: [],
      environment_vars: [],
      total_secrets: 0,
      total_env_vars: 0,
    },
  }
}

async function mountSecrets() {
  const wrapper = mount(Secrets, { attachTo: document.body })
  // onMounted awaits a 100ms timer before loading.
  await vi.advanceTimersByTimeAsync(150)
  await flushPromises()
  return wrapper
}

describe('Secrets page add-secret CTA (UX audit F8)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers()
    ;(api.getConfigSecrets as any).mockReset()
    ;(api.getConfigSecrets as any).mockResolvedValue(emptySecrets())
  })

  it('offers a page-level Add Secret button', async () => {
    const wrapper = await mountSecrets()
    expect(wrapper.find('[data-test="secrets-add-button"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('offers an Add Secret CTA in the empty state', async () => {
    const wrapper = await mountSecrets()
    expect(wrapper.find('[data-test="secrets-empty-add-button"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('opens the add-secret modal with no predefined name from the empty state', async () => {
    const wrapper = await mountSecrets()

    expect(wrapper.findComponent({ name: 'AddSecretModal' }).props('show')).toBe(false)

    await wrapper.find('[data-test="secrets-empty-add-button"]').trigger('click')
    await flushPromises()

    const modal = wrapper.findComponent({ name: 'AddSecretModal' })
    expect(modal.props('show')).toBe(true)
    expect(modal.props('predefinedName')).toBeUndefined()
    // The name field is editable, i.e. the user can name a brand new secret.
    const nameInput = modal.find('input[type="text"]')
    expect(nameInput.attributes('readonly')).toBeUndefined()

    wrapper.unmount()
  })

  it('still passes the config-referenced name through from a per-row Set button', async () => {
    ;(api.getConfigSecrets as any).mockResolvedValue({
      success: true,
      data: {
        secrets: [
          {
            secret_ref: { name: 'my-api-key', original: '${keyring:my-api-key}', type: 'keyring' },
            is_set: false,
          },
        ],
        environment_vars: [],
        total_secrets: 1,
        total_env_vars: 0,
      },
    })

    const wrapper = await mountSecrets()
    const rowButtons = wrapper.findAll('button').filter((b) => b.text().includes('Add Value'))
    expect(rowButtons.length).toBe(1)
    await rowButtons[0].trigger('click')
    await flushPromises()

    const modal = wrapper.findComponent({ name: 'AddSecretModal' })
    expect(modal.props('predefinedName')).toBe('my-api-key')

    wrapper.unmount()
  })
})
