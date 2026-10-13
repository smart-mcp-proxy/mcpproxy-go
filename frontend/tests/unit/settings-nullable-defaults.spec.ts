import { describe, it, expect, vi, beforeEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { reactive } from 'vue'

// Spec 109 D35 (T161, demo finding #2): a nullable (*bool) config key that the
// API omits resolves to a concrete value in Go (quarantine on, telemetry on,
// ...). The Settings toggle used to read `!!undefined` and render OFF, while
// the posture chip said ON. The shared fixture below is the same file the Go
// test pins to the Go resolvers.

const patchConfig = vi.fn(() =>
  Promise.resolve({ success: true, data: { changed_fields: [], requires_restart: false } })
)
vi.mock('@/services/api', () => ({
  default: { patchConfig: (...args: unknown[]) => patchConfig(...args) },
}))
vi.mock('@/stores/system', () => ({ useSystemStore: () => ({ addToast: vi.fn() }) }))

import SettingsSection from '@/components/settings/SettingsSection.vue'
import SettingField from '@/components/settings/SettingField.vue'
import {
  hydrateConfigState,
  allCatalogFields,
  effectiveBool,
  refreshEditionDefaults,
  type SettingField as Field,
} from '@/views/settings/fields'

type BlockDefault = {
  absent_block: { personal: boolean; server: boolean }
  present_block: boolean
}
const fixture = JSON.parse(
  readFileSync(resolve(__dirname, '../../../internal/config/testdata/settings_nullable_defaults.json'), 'utf8')
) as { defaults: Record<string, boolean | BlockDefault>; not_in_settings: string[] }

const field = (key: string): Field => {
  const f = allCatalogFields().find((x) => x.key === key)
  if (!f) throw new Error(`no settings field for ${key}`)
  return f
}

describe('nullable boolean settings render their effective value', () => {
  it('quarantine_enabled absent -> ON in working and original, not dirty', () => {
    const { working, original } = hydrateConfigState({})
    expect(working.quarantine_enabled).toBe(true)
    expect(original.quarantine_enabled).toBe(true)
    expect(JSON.stringify(working)).toBe(JSON.stringify(original))
  })

  it('SettingField renders the quarantine toggle checked when the key is absent', () => {
    const { working } = hydrateConfigState({})
    const w = mount(SettingField, {
      props: { field: field('quarantine_enabled'), modelValue: working.quarantine_enabled, dirty: false },
    })
    const toggle = w.find('[data-test="setting-toggle-quarantine_enabled"]')
    expect((toggle.element as HTMLInputElement).checked).toBe(true)
  })

  it('an explicit false stays false', () => {
    const { working, original } = hydrateConfigState({ quarantine_enabled: false, telemetry: { enabled: false } })
    expect(working.quarantine_enabled).toBe(false)
    expect(original.quarantine_enabled).toBe(false)
    expect(working.telemetry.enabled).toBe(false)
  })

  it('telemetry.enabled absent (no block, or block without the key) -> ON', () => {
    expect(hydrateConfigState({}).working.telemetry.enabled).toBe(true)
    expect(hydrateConfigState({ telemetry: { endpoint: 'x' } }).working.telemetry.enabled).toBe(true)
  })

  it('server edition: audit_log block absent -> enabled, stdout and compress ON', () => {
    const { working, original } = hydrateConfigState({}, { edition: 'server' })
    expect(working.audit_log).toEqual({ enabled: true, stdout: true, compress: true })
    expect(original.audit_log).toEqual({ enabled: true, stdout: true, compress: true })
  })

  it('personal edition: audit_log block absent -> logging OFF, compress ON', () => {
    const { working } = hydrateConfigState({}, { edition: 'personal' })
    expect(working.audit_log.enabled).toBe(false)
    expect(working.audit_log.stdout).toBe(false)
    expect(working.audit_log.compress).toBe(true)
    // edition unknown behaves as personal (the safe, "off" reading)
    expect(hydrateConfigState({}).working.audit_log.enabled).toBe(false)
  })

  it('audit_log block present: enabled ON, stdout OFF (an explicit block never defaults stdout)', () => {
    const { working } = hydrateConfigState({ audit_log: { path: 'x' } }, { edition: 'server' })
    expect(working.audit_log.enabled).toBe(true)
    expect(working.audit_log.stdout).toBe(false)
    expect(working.audit_log.compress).toBe(true)
    expect(working.audit_log.path).toBe('x')
  })

  it('materialising audit_log.enabled does not flip the stdout rule (snapshot first)', () => {
    // stdout's default reads "is the block absent"; if enabled were written
    // first the block would exist by the time stdout was resolved.
    const { working } = hydrateConfigState({}, { edition: 'server' })
    expect(working.audit_log.stdout).toBe(true)
  })

  it('explicit audit_log values are never overwritten', () => {
    const { working } = hydrateConfigState(
      { audit_log: { enabled: false, stdout: true, compress: false } },
      { edition: 'server' }
    )
    expect(working.audit_log).toEqual({ enabled: false, stdout: true, compress: false })
  })

  it('the posture helper and the toggle agree on the effective value', () => {
    const empty = hydrateConfigState({}).working
    expect(effectiveBool(empty, 'quarantine_enabled')).toBe(true)
    expect(effectiveBool({ quarantine_enabled: false }, 'quarantine_enabled')).toBe(false)
    // works on a raw (un-hydrated) config too
    expect(effectiveBool({}, 'quarantine_enabled')).toBe(true)
  })
})

describe('fixture parity (internal/config/testdata/settings_nullable_defaults.json)', () => {
  const personal = hydrateConfigState({}, { edition: 'personal' }).working
  const server = hydrateConfigState({}, { edition: 'server' }).working
  const present = hydrateConfigState({ audit_log: {} }, { edition: 'server' }).working
  const get = (cfg: any, key: string) => key.split('.').reduce((o, k) => (o == null ? undefined : o[k]), cfg)

  for (const [key, def] of Object.entries(fixture.defaults)) {
    it(`${key} is a catalogue toggle whose default matches the fixture`, () => {
      expect(field(key).control).toBe('toggle')
      if (typeof def === 'boolean') {
        expect(get(personal, key), `${key} personal`).toBe(def)
        expect(get(server, key), `${key} server`).toBe(def)
      } else {
        expect(get(personal, key), `${key} absent/personal`).toBe(def.absent_block.personal)
        expect(get(server, key), `${key} absent/server`).toBe(def.absent_block.server)
        expect(get(present, key), `${key} present`).toBe(def.present_block)
      }
    })
  }

  it('no catalogue toggle is listed as not_in_settings', () => {
    const toggles = new Set(allCatalogFields().filter((f) => f.control === 'toggle').map((f) => f.key))
    for (const k of fixture.not_in_settings) expect(toggles.has(k), k).toBe(false)
  })
})

describe('saving an unrelated section never writes the defaulted keys', () => {
  beforeEach(() => patchConfig.mockClear())

  it('PATCH body for another field carries no audit_log / quarantine_enabled / telemetry key', async () => {
    const { working, original } = hydrateConfigState({}, { edition: 'server' })
    const other: Field = { key: 'tool_response_limit', label: 'Limit', control: 'number', min: 0 }
    const rw = reactive(working)
    const w = mount(SettingsSection, {
      props: { sectionId: 'general', fields: [other], working: rw, original: reactive(original) },
    })
    rw.tool_response_limit = 123
    await flushPromises()
    await w.find('[data-test="settings-apply-general"]').trigger('click')
    await flushPromises()
    expect(patchConfig).toHaveBeenCalledTimes(1)
    const body = patchConfig.mock.calls[0][0] as Record<string, unknown>
    expect(body).toEqual({ tool_response_limit: 123 })
    expect(Object.keys(body)).not.toContain('audit_log')
  })
})

describe('edition arriving after the config (status frame race)', () => {
  it('re-resolves untouched audit_log defaults in both copies, so nothing turns dirty', () => {
    const state = hydrateConfigState({}) // edition unknown yet
    expect(state.working.audit_log.enabled).toBe(false)
    refreshEditionDefaults(state, state.raw, { edition: 'server' })
    expect(state.working.audit_log).toEqual({ enabled: true, stdout: true, compress: true })
    expect(JSON.stringify(state.working)).toBe(JSON.stringify(state.original))
  })

  it('leaves a key the user already edited alone', () => {
    const state = hydrateConfigState({})
    state.working.audit_log.stdout = true // user flipped it
    refreshEditionDefaults(state, state.raw, { edition: 'server' })
    expect(state.working.audit_log.stdout).toBe(true)
    expect(state.original.audit_log.stdout).toBe(false)
  })
})
