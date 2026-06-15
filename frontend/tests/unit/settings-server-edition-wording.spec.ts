import { describe, it, expect } from 'vitest'
import {
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  SERVER_EDITION_FIELDS,
} from '../../src/views/settings/fields'

// MCP-1087 + MCP-1086: the Settings server-edition surface must read "Server
// Edition" (not "Teams"), and the config dot-paths must target the canonical
// `server_edition.*` key now that MCP-1086 (backend rename) has landed.
// Legacy `teams`-keyed configs still hydrate via aliasServerEdition() in
// Settings.vue + the backend loader alias.
describe('Settings server-edition wording (MCP-1087)', () => {
  it('uses "Server Edition" wording with no "Teams" left in user-facing labels', () => {
    expect(SERVER_EDITION_TAB_LABEL).toBe('Server Edition')
    expect(SERVER_EDITION_TAB_LABEL).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).toMatch(/Server Edition/)
  })

  it('uses the canonical server_edition.* config dot-paths (MCP-1086 backend rename landed)', () => {
    expect(SERVER_EDITION_FIELDS.length).toBeGreaterThan(0)
    for (const f of SERVER_EDITION_FIELDS) {
      expect(f.key).toMatch(/^server_edition\./)
    }
  })
})
