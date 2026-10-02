import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import SecretToggle from '@/components/SecretToggle.vue'

// Spec 109 FR-065 / US5-5 (fix-usertest-web T174, audit F-07): a field in
// Secret mode OR whose name looks secret-like is masked while typing, in Value
// mode too. Value/Secret decides storage, not display; Show/Hide is display
// only. Before this, an API_TOKEN typed in Value mode was plain text on screen
// and then rendered masked after save.

function mountToggle(props: Record<string, unknown> = {}) {
  return mount(SecretToggle, {
    props: { name: 'API_TOKEN', kind: 'env', modelValue: '', mode: 'value', keyringAvailable: true, ...props },
  })
}

const input = (w: ReturnType<typeof mountToggle>) => w.find('[data-test="secret-toggle-value-input"]')
const reveal = (w: ReturnType<typeof mountToggle>) => w.find('[data-test="secret-toggle-reveal"]')

describe('SecretToggle masking', () => {
  it('masks a secret-like name in Value mode and reveals it on demand', async () => {
    const w = mountToggle({ name: 'API_TOKEN', mode: 'value', modelValue: 'fake-secret-value' })
    expect(input(w).attributes('type')).toBe('password')
    expect(input(w).attributes('autocomplete')).toBe('off')
    expect(input(w).attributes('spellcheck')).toBe('false')
    expect(reveal(w).exists()).toBe(true)
    expect(reveal(w).attributes('aria-pressed')).toBe('false')
    expect(reveal(w).attributes('aria-label')).toBe('Show API_TOKEN value')

    await reveal(w).trigger('click')
    expect(input(w).attributes('type')).toBe('text')
    expect(reveal(w).attributes('aria-pressed')).toBe('true')
    expect(reveal(w).attributes('aria-label')).toBe('Hide API_TOKEN value')

    await reveal(w).trigger('click')
    expect(input(w).attributes('type')).toBe('password')
  })

  it('leaves an ordinary Value field as plain text with no reveal button', () => {
    const w = mountToggle({ name: 'WORKDIR', mode: 'value' })
    expect(input(w).attributes('type')).toBe('text')
    expect(reveal(w).exists()).toBe(false)
  })

  it('masks any field in Secret mode, whatever its name', () => {
    const w = mountToggle({ name: 'WORKDIR', mode: 'secret' })
    expect(input(w).attributes('type')).toBe('password')
    expect(reveal(w).exists()).toBe(true)
  })

  it('masks when the name arrives after the row (Manual flow: env-0 then API_TOKEN)', async () => {
    const w = mountToggle({ name: 'env-0', mode: 'value' })
    expect(input(w).attributes('type')).toBe('text')
    await w.setProps({ name: 'API_TOKEN' })
    expect(input(w).attributes('type')).toBe('password')
    expect(reveal(w).exists()).toBe(true)
  })

  it('revealing is display only: it never emits a mode or value change', async () => {
    const w = mountToggle({ name: 'API_TOKEN', modelValue: 'x' })
    await reveal(w).trigger('click')
    await reveal(w).trigger('click')
    expect(w.emitted('update:mode')).toBeUndefined()
    expect(w.emitted('update:modelValue')).toBeUndefined()
  })

  it('masks a header whose name matches (Authorization)', () => {
    const w = mountToggle({ name: 'Authorization', kind: 'header' })
    expect(input(w).attributes('type')).toBe('password')
  })

  it('opts the field out of password-manager prompts', () => {
    const w = mountToggle()
    expect(input(w).attributes('data-1p-ignore')).toBeDefined()
    expect(input(w).attributes('data-lpignore')).toBe('true')
  })
})
