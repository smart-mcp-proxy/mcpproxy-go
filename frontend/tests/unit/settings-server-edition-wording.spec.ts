import { describe, it, expect } from 'vitest'
import {
  SERVER_EDITION_TAB_LABEL,
  SERVER_EDITION_SECTION_TITLE,
  SERVER_EDITION_FIELDS,
} from '../../src/views/settings/fields'

// MCP-1087: the Settings server-edition surface must read "Server Edition",
// not the legacy "Teams" wording. The config *keys*, however, deliberately
// stay on the legacy `teams.*` dot-paths until the backend rename of the
// config key (`teams` -> `server_edition`, MCP-1085 / PR #607, currently
// unmerged) lands. Flipping the keys early would make `hasTeams` read
// `state.working.server_edition`, which a live `teams`-keyed config doesn't
// have -> the whole server-edition tab silently disappears.
describe('Settings server-edition wording (MCP-1087)', () => {
  it('uses "Server Edition" wording with no "Teams" left in user-facing labels', () => {
    expect(SERVER_EDITION_TAB_LABEL).toBe('Server Edition')
    expect(SERVER_EDITION_TAB_LABEL).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).not.toMatch(/team/i)
    expect(SERVER_EDITION_SECTION_TITLE).toMatch(/Server Edition/)
  })

  it('keeps the config field keys on the legacy `teams.*` contract (backend rename pending)', () => {
    expect(SERVER_EDITION_FIELDS.length).toBeGreaterThan(0)
    for (const f of SERVER_EDITION_FIELDS) {
      expect(f.key).toMatch(/^teams\./)
    }
  })
})
