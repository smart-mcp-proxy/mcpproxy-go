import { describe, it, expect } from 'vitest'
import {
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  SERVER_EDITION_FIELDS,
} from '../../src/views/settings/fields'

// MCP-1087: the Settings server-edition surface must read "Server Edition",
// not the legacy "Teams" wording. MCP-1086: the backend config-key rename
// (`teams` -> `server_edition`) has landed, so the config *keys* are now on the
// canonical `server_edition.*` dot-paths. `Settings.vue` gates the tab on
// `server_edition` (with a `teams` fallback) and aliases a legacy `teams`-keyed
// config onto `server_edition` at load, so old configs still hydrate the form
// while edits always save under `server_edition`.
describe('Settings server-edition wording (MCP-1087)', () => {
  it('uses "Server Edition" wording with no "Teams" left in user-facing labels', () => {
    expect(SERVER_EDITION_TAB_LABEL).toBe('Server Edition')
    expect(SERVER_EDITION_TAB_LABEL).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).toMatch(/Server Edition/)
  })

  it('binds the config field keys to the canonical `server_edition.*` contract (MCP-1086)', () => {
    expect(SERVER_EDITION_FIELDS.length).toBeGreaterThan(0)
    for (const f of SERVER_EDITION_FIELDS) {
      expect(f.key).toMatch(/^server_edition\./)
    }
  })
})
