import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  GENERAL_FIELDS,
  allCatalogFields,
  normalizeFieldDefaults,
  restartRequiredLabels,
  getPath,
  type SettingField,
} from '@/views/settings/fields'

function field(key: string): SettingField {
  const f = allCatalogFields().find((x) => x.key === key)
  if (!f) throw new Error(`no settings field for ${key}`)
  return f
}

// Specs 085 + 102 — the two serialization knobs shipped with four ways to set them
// (config file, env var, serve flag, REST API) and zero UI surfaces. A tray- or
// Web-UI-only operator could not reach direct_tool_response_mode at all.
describe('serialization-mode settings fields', () => {
  it('exposes both serialization axes in the catalogue', () => {
    expect(field('tool_response_mode').control).toBe('select')
    expect(field('direct_tool_response_mode').control).toBe('select')
  })

  it('offers exactly the values the Go validator accepts', () => {
    expect(field('tool_response_mode').options?.map((o) => o.value)).toEqual(['full', 'compact'])
    expect(field('direct_tool_response_mode').options?.map((o) => o.value)).toEqual([
      'full',
      'deferred',
    ])
  })

  // Both are hot-reloadable: internal/runtime/config_hotreload.go appends them
  // to ChangedFields WITHOUT setting RequiresRestart. Badging them would tell
  // the operator to restart for nothing — and routing_mode, right above them,
  // genuinely does need one, so the distinction has to stay visible.
  it('carries no restart badge, unlike routing_mode', () => {
    expect(field('tool_response_mode').restart).toBeFalsy()
    expect(field('direct_tool_response_mode').restart).toBeFalsy()
    expect(field('routing_mode').restart).toBe(true)

    const labels = restartRequiredLabels()
    expect(labels).not.toContain(field('tool_response_mode').label)
    expect(labels).not.toContain(field('direct_tool_response_mode').label)
    expect(labels).toContain(field('routing_mode').label)
  })

  it('sits next to routing_mode, the knob it qualifies', () => {
    const keys = GENERAL_FIELDS.map((f) => f.key)
    expect(keys.slice(0, 3)).toEqual([
      'routing_mode',
      'tool_response_mode',
      'direct_tool_response_mode',
    ])
  })

  it('links each field to a page that documents that axis', () => {
    expect(field('tool_response_mode').docs).toBe('/features/search-discovery#tool-response-mode')
    expect(field('direct_tool_response_mode').docs).toBe('/features/schema-deferred-direct-mode')
  })

  it('names the routing mode each axis applies to, since only one is live at a time', () => {
    expect(field('tool_response_mode').help).toMatch(/Retrieve mode/)
    expect(field('direct_tool_response_mode').help).toMatch(/Direct mode/)
  })
})

describe('normalizeFieldDefaults', () => {
  it('renders an omitted omitempty mode as its real default, not a blank select', () => {
    const cfg = normalizeFieldDefaults({ listen: '127.0.0.1:8080' })
    expect(cfg.tool_response_mode).toBe('full')
    expect(cfg.direct_tool_response_mode).toBe('full')
  })

  it('treats an explicit empty string the same as absent (Go reads "" as full)', () => {
    const cfg = normalizeFieldDefaults({ tool_response_mode: '', direct_tool_response_mode: '' })
    expect(cfg.tool_response_mode).toBe('full')
    expect(cfg.direct_tool_response_mode).toBe('full')
  })

  it('never overwrites a value the operator actually set', () => {
    const cfg = normalizeFieldDefaults({
      tool_response_mode: 'compact',
      direct_tool_response_mode: 'deferred',
    })
    expect(cfg.tool_response_mode).toBe('compact')
    expect(cfg.direct_tool_response_mode).toBe('deferred')
  })

  // Settings.vue must normalize `working` AND `original` from the same
  // response. Normalizing only the working copy is a live footgun: the value
  // genuinely diverges from the raw response, so the untouched field would
  // compare unequal and the section would open claiming an unsaved change.
  // (Asserting the divergence is what gives the "normalize both" rule teeth —
  // comparing two identically-normalized objects would pass unconditionally.)
  it('diverges from the raw response, so both copies must be normalized', () => {
    const response = { listen: '127.0.0.1:8080' }
    const working = normalizeFieldDefaults({ ...response })

    expect(getPath(working, 'tool_response_mode')).toBe('full')
    expect(getPath(response, 'tool_response_mode')).toBeUndefined()

    const original = normalizeFieldDefaults({ ...response })
    for (const f of allCatalogFields()) {
      expect(getPath(working, f.key)).toEqual(getPath(original, f.key))
    }
  })

  it('leaves fields that declare no default untouched', () => {
    const cfg = normalizeFieldDefaults({})
    for (const f of allCatalogFields()) {
      if (f.defaultValue == null) expect(getPath(cfg, f.key)).toBeUndefined()
    }
  })

  it('is a no-op on a null/non-object config instead of throwing', () => {
    expect(normalizeFieldDefaults(null)).toBeNull()
    expect(normalizeFieldDefaults(undefined)).toBeUndefined()
  })
})

// The catalogue is inert until Settings.vue applies it. There is no mount
// harness for that view, so pin the wiring at the source level — the same
// approach settings-deep-scan-field.spec.ts uses. Without this, dropping the
// normalize call would leave every test above green while the Settings page
// showed two empty dropdowns.
describe('Settings.vue wiring', () => {
  const source = readFileSync(
    resolve(__dirname, '../../src/views/Settings.vue'),
    'utf-8',
  )

  it('normalizes both the working copy and the last-saved snapshot', () => {
    expect(source).toMatch(/state\.working = normalizeFieldDefaults\(/)
    expect(source).toMatch(/state\.original = normalizeFieldDefaults\(/)
  })

  it('leaves the Raw JSON tab on the untouched server response', () => {
    // configJson must serialize `cfg`, not a normalized clone — the Raw tab
    // shows what the core actually holds, and normalization invents keys the
    // config file does not contain.
    expect(source).toMatch(/configJson\.value = JSON\.stringify\(cfg,/)
  })
})
