import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import {
  GENERAL_FIELDS,
  hydrateConfigState,
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

  // The normalized copy genuinely diverges from the raw response — which is
  // what makes the "normalize both snapshots" rule load-bearing. (Comparing two
  // identically-normalized objects would pass unconditionally; that vacuous
  // form is what cross-model review caught in the tray twin of this test.)
  it('diverges from the raw response', () => {
    const response = { listen: '127.0.0.1:8080' }
    expect(getPath(response, 'tool_response_mode')).toBeUndefined()
    expect(getPath(normalizeFieldDefaults({ ...response }), 'tool_response_mode')).toBe('full')
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

// hydrateConfigState is what Settings.vue's loadConfig() actually calls, so
// these assert the invariant itself rather than matching source text — a
// source-text match cannot tell whether `cfg` is still the untouched response
// by the time the Raw tab serializes it.
describe('hydrateConfigState', () => {
  const response = () => ({ listen: '127.0.0.1:8080' })

  it('normalizes both snapshots, so nothing reads as dirty on open', () => {
    const { working, original } = hydrateConfigState(response())
    expect(getPath(working, 'tool_response_mode')).toBe('full')
    expect(getPath(original, 'tool_response_mode')).toBe('full')
    for (const f of allCatalogFields()) {
      expect(getPath(working, f.key)).toEqual(getPath(original, f.key))
    }
  })

  it('keeps raw as the untouched server response for the Raw JSON tab', () => {
    const { raw } = hydrateConfigState(response())
    expect(JSON.stringify(raw)).not.toContain('tool_response_mode')
    expect(JSON.stringify(raw)).not.toContain('direct_tool_response_mode')
    expect(getPath(raw, 'listen')).toBe('127.0.0.1:8080')
  })

  it('does not mutate its argument', () => {
    const cfg = response()
    hydrateConfigState(cfg)
    expect(getPath(cfg, 'tool_response_mode')).toBeUndefined()
  })

  it('still aliases a legacy teams-keyed config onto server_edition', () => {
    const { working } = hydrateConfigState({ teams: { enabled: true } })
    expect(getPath(working, 'server_edition.enabled')).toBe(true)
  })

  it('preserves an explicitly-set mode', () => {
    const { working, raw } = hydrateConfigState({ direct_tool_response_mode: 'deferred' })
    expect(getPath(working, 'direct_tool_response_mode')).toBe('deferred')
    expect(getPath(raw, 'direct_tool_response_mode')).toBe('deferred')
  })
})

// Regression: cross-model review found that a resolved default WAS writable.
// SettingsSection keeps a component-local `dirty` ref and dirtyKeys is the
// UNION of it and the working/original comparison. Reload replaces both
// snapshots but used to leave that ref populated, so: pick Compact -> Reload ->
// Save PATCHed `tool_response_mode: "full"` into a config that never had the
// key. Settings.vue now keys every section on a form epoch bumped per
// hydration, so the section remounts with an empty ref.
describe('Settings.vue rehydration (stale-dirty regression)', () => {
  const source = readFileSync(resolve(__dirname, '../../src/views/Settings.vue'), 'utf-8')

  it('bumps a form epoch on every hydration', () => {
    expect(source).toMatch(/formEpoch\.value\+\+/)
  })

  it('keys every SettingsSection on that epoch so the dirty ref cannot survive', () => {
    const sections = source.match(/<SettingsSection\b/g) || []
    const keyed = source.match(/<SettingsSection :key="`[^`]*\$\{formEpoch\}`"/g) || []
    expect(sections.length).toBeGreaterThan(0)
    expect(keyed.length).toBe(sections.length)
  })
})
