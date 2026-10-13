import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ProfileToolTable from '@/components/profiles/ProfileToolTable.vue'

// #1451 item 13: the server's stale_classification_reasons decides the note
// next to a stale classify entry, in the same words as `mcpproxy profile show
// --effective`. An older daemon sends no map, so the note stays neutral.
const ROW = {
  server: 'notion', tool: 'update_page', intrinsic_tier: 'write', profile_tier: 'write',
  access: { visible: false, callable: false, reason: 'above_tier_cap' }, classification_stale: false,
}

function mountTable(staleReasons?: Record<string, string>) {
  return mount(ProfileToolTable, {
    props: {
      rows: [ROW], stale: ['github:gone_tool', 'github:list_issues'], staleReasons,
      draftRules: undefined, profileLabel: 'work', serversChosen: true, editable: true,
    },
  })
}

describe('ProfileToolTable stale classification reasons (#1451)', () => {
  it('words each orphan note from stale_classification_reasons, like the CLI', () => {
    const w = mountTable({ 'github:gone_tool': 'missing', 'github:list_issues': 'annotated' })
    expect(w.get('[data-test="profile-stale-orphan-note-github:gone_tool"]').text())
      .toBe('classification ignored — tool not found')
    expect(w.get('[data-test="profile-stale-orphan-note-github:list_issues"]').text())
      .toBe('classification ignored — tool is now annotated')
  })

  it('stays neutral without a reason map (older daemon)', () => {
    const w = mountTable(undefined)
    const notes = w.findAll('[data-test^="profile-stale-orphan-note-"]').map(n => n.text())
    expect(notes).toEqual(['classification ignored', 'classification ignored'])
  })
})
