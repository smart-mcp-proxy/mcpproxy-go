import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import SecretToggle from '@/components/SecretToggle.vue'
import { resolveSecretFields, rollbackSecrets } from '@/composables/useSecretFields'

vi.mock('@/services/api', () => ({
  default: {
    getSecretRefs: vi.fn(),
    setSecret: vi.fn(),
    deleteSecret: vi.fn(),
  },
}))
import api from '@/services/api'

function mountToggle(props: Partial<InstanceType<typeof SecretToggle>['$props']> = {}) {
  return mount(SecretToggle, {
    props: {
      name: 'API_KEY',
      kind: 'env',
      modelValue: '',
      mode: 'value',
      keyringAvailable: true,
      ...props,
    },
  })
}

describe('SecretToggle', () => {
  it('renders Value and Secret mode buttons, and emits update:mode on click', async () => {
    const wrapper = mountToggle({ mode: 'value' })
    await wrapper.find('[data-test="secret-toggle-mode-secret"]').trigger('click')
    expect(wrapper.emitted('update:mode')?.[0]).toEqual(['secret'])
  })

  it('emits update:modelValue on input', async () => {
    const wrapper = mountToggle()
    const input = wrapper.find('[data-test="secret-toggle-value-input"]')
    await input.setValue('sk-live-abc123')
    expect(wrapper.emitted('update:modelValue')?.[0]).toEqual(['sk-live-abc123'])
  })

  it('disables Secret mode when the keyring is unavailable, with the reason as a tooltip', () => {
    const wrapper = mountToggle({ keyringAvailable: false, keyringReason: 'no display session' })
    const btn = wrapper.find('[data-test="secret-toggle-mode-secret"]')
    expect(btn.attributes('disabled')).toBeDefined()
    expect(btn.attributes('title')).toContain('no display session')
  })

  it('does not emit update:mode when clicking Secret while the keyring is unavailable', async () => {
    const wrapper = mountToggle({ keyringAvailable: false })
    await wrapper.find('[data-test="secret-toggle-mode-secret"]').trigger('click')
    expect(wrapper.emitted('update:mode')).toBeUndefined()
  })

  it('defaults to secret for a secret-like name, value otherwise (D13)', () => {
    const secretLike = mountToggle({ name: 'GITHUB_TOKEN' })
    expect((secretLike.vm as unknown as { defaultMode: () => string }).defaultMode()).toBe('secret')
    const ordinary = mountToggle({ name: 'WORKDIR' })
    expect((ordinary.vm as unknown as { defaultMode: () => string }).defaultMode()).toBe('value')
  })
})

describe('resolveSecretFields', () => {
  beforeEach(() => {
    vi.mocked(api.getSecretRefs).mockReset()
    vi.mocked(api.setSecret).mockReset()
    vi.mocked(api.deleteSecret).mockReset()
  })

  it('passes value-mode fields through unchanged, never touching the keyring', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    const result = await resolveSecretFields('github', [
      { kind: 'env', name: 'WORKDIR', value: '/tmp', mode: 'value' },
    ])
    expect(result.env).toEqual({ WORKDIR: '/tmp' })
    expect(api.setSecret).not.toHaveBeenCalled()
  })

  it('writes a secret-mode field to the keyring and references it with ${keyring:<ref>}', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })
    const result = await resolveSecretFields('github', [
      { kind: 'env', name: 'GITHUB_TOKEN', value: 'sk-live-abc', mode: 'secret' },
    ])
    expect(api.setSecret).toHaveBeenCalledWith('github-env-github-token', 'sk-live-abc')
    expect(result.env).toEqual({ GITHUB_TOKEN: '${keyring:github-env-github-token}' })
    expect(result.writtenRefs).toEqual(['github-env-github-token'])
  })

  it('writes an env var and a header of the same name to two distinct refs', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })
    const result = await resolveSecretFields('github', [
      { kind: 'env', name: 'API_KEY', value: 'env-value', mode: 'secret' },
      { kind: 'header', name: 'API_KEY', value: 'header-value', mode: 'secret' },
    ])
    expect(result.env.API_KEY).toBe('${keyring:github-env-api-key}')
    expect(result.headers.API_KEY).toBe('${keyring:github-header-api-key}')
  })

  it('gets a -2 suffix when GET /secrets/refs already carries the computed name', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({
      success: true,
      data: { refs: [{ type: 'keyring', name: 'github-env-api-key', original: '' }] },
    })
    vi.mocked(api.setSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '', reference: '' } })
    const result = await resolveSecretFields('github', [
      { kind: 'env', name: 'API_KEY', value: 'new-value', mode: 'secret' },
    ])
    expect(result.env.API_KEY).toBe('${keyring:github-env-api-key-2}')
  })

  it('rolls back only the secrets this call wrote when a later write fails', async () => {
    vi.mocked(api.getSecretRefs).mockResolvedValue({ success: true, data: { refs: [] } })
    vi.mocked(api.setSecret)
      .mockResolvedValueOnce({ success: true, data: { message: '', name: '', type: '', reference: '' } })
      .mockResolvedValueOnce({ success: false, error: 'keyring write failed' })
    vi.mocked(api.deleteSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '' } })

    await expect(
      resolveSecretFields('github', [
        { kind: 'env', name: 'FIRST', value: 'a', mode: 'secret' },
        { kind: 'env', name: 'SECOND', value: 'b', mode: 'secret' },
      ])
    ).rejects.toThrow()

    expect(api.deleteSecret).toHaveBeenCalledTimes(1)
    expect(api.deleteSecret).toHaveBeenCalledWith('github-env-first')
  })

  it('rollbackSecrets deletes exactly the given refs', async () => {
    vi.mocked(api.deleteSecret).mockResolvedValue({ success: true, data: { message: '', name: '', type: '' } })
    await rollbackSecrets(['a', 'b'])
    expect(api.deleteSecret).toHaveBeenCalledWith('a')
    expect(api.deleteSecret).toHaveBeenCalledWith('b')
  })
})
