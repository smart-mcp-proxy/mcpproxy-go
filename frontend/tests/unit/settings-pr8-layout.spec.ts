import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createRouter, createMemoryHistory } from 'vue-router'

// Spec 134 PR 8 (FR-050..FR-052): Settings opens on General, sections are a
// side list (a select at phone width), long help is a short summary with the
// full text one activation away, and Save/Discard sit in normal flow.

vi.mock('@/services/api', () => ({
  default: {
    getConfig: vi.fn().mockResolvedValue({ success: true, data: { config: {} } }),
    getStatus: vi.fn().mockResolvedValue({ success: true, data: {} }),
    validateConfig: vi.fn(),
    applyConfig: vi.fn(),
  },
}))

import Settings from '@/views/Settings.vue'
import SettingField from '@/components/settings/SettingField.vue'
import SettingsSection from '@/components/settings/SettingsSection.vue'
import { useSystemStore } from '@/stores/system'
import { GENERAL_FIELDS } from '@/views/settings/fields'

async function mountSettings(path = '/settings') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/settings', name: 'settings', component: Settings },
      { path: '/:pathMatch(.*)*', name: 'other', component: { template: '<div/>' } },
    ],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(Settings, { global: { plugins: [router], stubs: { VueMonacoEditor: true } } })
  await flushPromises()
  return { wrapper, router }
}

const visible = (w: any, test: string) => w.get(`[data-test="${test}"]`).attributes('style') ?? ''

describe('Settings sections (FR-050)', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('opens on General when neither ?tab= nor ?focus= selects a section', async () => {
    const { wrapper } = await mountSettings()
    expect(wrapper.get('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
    expect(wrapper.get('[data-test="settings-tab-security"]').classes()).not.toContain('tab-active')
    expect(visible(wrapper, 'settings-section-general')).not.toContain('display: none')
  })

  it('keeps tab ids and ?tab= selection', async () => {
    const { wrapper } = await mountSettings('/settings?tab=security')
    expect(wrapper.get('[data-test="settings-tab-security"]').classes()).toContain('tab-active')
    for (const id of ['general', 'security', 'catalog', 'advanced', 'raw']) {
      expect(wrapper.find(`[data-test="settings-tab-${id}"]`).exists()).toBe(true)
    }
  })

  it('uses the FR-050 labels, General first', async () => {
    const { wrapper } = await mountSettings()
    const labels = wrapper.findAll('[role="tab"]').map((t) => t.text())
    expect(labels).toEqual(['General', 'Access and keys', 'Catalog sources', 'Advanced', 'Edit as JSON'])
  })

  it('adds the server-edition section after Advanced', async () => {
    const { wrapper } = await mountSettings()
    const systemStore = useSystemStore()
    // @ts-expect-error partial status is enough
    systemStore.status = { edition: 'server' }
    await flushPromises()
    const ids = wrapper.findAll('[role="tab"]').map((t) => t.attributes('data-test'))
    expect(ids.indexOf('settings-tab-teams')).toBe(ids.indexOf('settings-tab-advanced') + 1)
  })

  it('offers a select with the same sections for phone width, in step with the list', async () => {
    const { wrapper } = await mountSettings()
    const select = wrapper.get('[data-test="settings-tab-select"]')
    expect(select.findAll('option').map((o) => o.attributes('value'))).toEqual(['general', 'security', 'catalog', 'advanced', 'raw'])
    expect((select.element as HTMLSelectElement).value).toBe('general')
    await select.setValue('advanced')
    expect(wrapper.get('[data-test="settings-tab-advanced"]').classes()).toContain('tab-active')
  })
})

describe('Review fixes', () => {
  beforeEach(() => setActivePinia(createPinia()))

  it('re-picking General before late edition discovery keeps General', async () => {
    const { wrapper } = await mountSettings('/settings?tab=teams')
    await wrapper.get('[data-test="settings-tab-general"]').trigger('click')
    const systemStore = useSystemStore()
    // @ts-expect-error partial status is enough
    systemStore.status = { edition: 'server' }
    await flushPromises()
    expect(wrapper.get('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
  })

  it('re-picking General rewrites a stale ?tab= in the URL', async () => {
    const { wrapper, router } = await mountSettings('/settings?tab=teams')
    await wrapper.get('[data-test="settings-tab-general"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.tab).toBe('general')
  })

  it('a bare /settings after ?tab=security falls back to General', async () => {
    const { wrapper, router } = await mountSettings('/settings?tab=security')
    await router.push('/settings')
    await flushPromises()
    expect(wrapper.get('[data-test="settings-tab-general"]').classes()).toContain('tab-active')
  })

  it('the section list is a roving-tabindex list: arrows move and select', async () => {
    const { wrapper } = await mountSettings()
    expect(wrapper.get('[data-test="settings-tab-general"]').attributes('tabindex')).toBe('0')
    expect(wrapper.get('[data-test="settings-tab-security"]').attributes('tabindex')).toBe('-1')
    await wrapper.get('[data-test="settings-tab-general"]').trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    expect(wrapper.get('[data-test="settings-tab-security"]').classes()).toContain('tab-active')
  })

  it('help text names sections that exist', async () => {
    const { ADVANCED_ACCORDIONS } = await import('@/views/settings/fields')
    const text = ADVANCED_ACCORDIONS.map((a) => a.description ?? '').join(' ')
    expect(text).not.toMatch(/Security & Access|Raw JSON/)
  })
})

describe('Field help (FR-051)', () => {
  const longHelp = 'First sentence explains the setting. ' + 'More detail follows here. '.repeat(12)
  const field: any = { key: 'x.y', label: 'X', control: 'toggle', help: longHelp }

  it('shows a short summary and the full help after one activation', async () => {
    const w = mount(SettingField, { props: { field, modelValue: false } })
    const toggle = w.get('[data-test="setting-help-toggle-x.y"]')
    expect(toggle.element.tagName).toBe('BUTTON')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(w.text()).toContain('First sentence explains the setting.')
    expect(w.text()).not.toContain(longHelp.trim())
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(w.get('[data-test="setting-help-full-x.y"]').text()).toBe(longHelp.trim())
  })

  it('has no toggle when the help is already short', () => {
    const w = mount(SettingField, { props: { field: { ...field, help: 'Short help.' }, modelValue: false } })
    expect(w.find('[data-test="setting-help-toggle-x.y"]').exists()).toBe(false)
    expect(w.text()).toContain('Short help.')
  })

  it('works with no docs page (the full text is local)', async () => {
    const w = mount(SettingField, { props: { field: { ...field, docs: undefined }, modelValue: false } })
    await w.get('[data-test="setting-help-toggle-x.y"]').trigger('click')
    expect(w.find('[data-test="setting-help-full-x.y"]').exists()).toBe(true)
  })

  it('every long catalogue help in General is reachable in full', () => {
    for (const f of GENERAL_FIELDS.filter((f) => (f.help?.length ?? 0) > 140)) {
      const w = mount(SettingField, { props: { field: f, modelValue: undefined } })
      expect(w.find(`[data-test="setting-help-toggle-${f.key}"]`).exists()).toBe(true)
    }
  })
})

describe('Save row (FR-052)', () => {
  it('sits after the last field in normal flow, covering nothing', () => {
    const w = mount(SettingsSection, {
      props: { sectionId: 'general', fields: GENERAL_FIELDS.slice(0, 3), working: {}, original: {} },
    })
    const root = w.element as HTMLElement
    const rows = [...root.querySelectorAll('[data-test^="setting-row-"]')]
    const save = root.querySelector('[data-test="settings-save-row-general"]') as HTMLElement
    expect(save).toBeTruthy()
    expect(rows[rows.length - 1].compareDocumentPosition(save) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(save.querySelector('[data-test="settings-apply-general"]')).toBeTruthy()
    expect(save.className).not.toMatch(/\b(sticky|fixed|absolute)\b/)
  })
})
