import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import FlaggedToolsPanel from '@/components/FlaggedToolsPanel.vue'
import { groupFindingsByTool } from '@/utils/toolLocation'
import type { SecurityScanFinding } from '@/types/api'

// Phase 1 (TPA inline findings) — the panel that finally names WHICH tool.
//
// Before it, the Security tab reported "1 dangerous" across fifteen tools and
// the operator had four clicks and a scan-history table between them and the
// answer. The panel lists the flagged tools where the tools already live, from
// the report ServerDetail already holds in memory.
//
// Two behaviours are load-bearing:
//   * it is NOT RENDERED at all when there is nothing to report — absence is
//     the clean state, and a permanent "0 findings" card is chrome an operator
//     learns to skip, which is exactly what must not happen the day it is not 0;
//   * it states that the findings are informational. scan_informational.go is
//     explicit that this path "drives NO gating whatsoever", so a panel that
//     looks like a block would send the operator hunting for a release button
//     that does not exist.

function finding(overrides: Partial<SecurityScanFinding> = {}): SecurityScanFinding {
  return {
    rule_id: 'detect.shadowing.cross_server',
    threat_type: 'tool_poisoning',
    threat_level: 'dangerous',
    title: 'Tool shadowing',
    description: 'description references cross-server tool "reason"',
    location: 'github:create_issue',
    ...overrides,
  }
}

function mountPanel(props: Record<string, unknown> = {}) {
  return mount(FlaggedToolsPanel, { props })
}

const panel = '[data-test="flagged-tools-panel"]'

describe('FlaggedToolsPanel — absence is the clean state', () => {
  it('renders nothing at all with no findings', () => {
    const wrapper = mountPanel()
    expect(wrapper.find(panel).exists()).toBe(false)
    expect(wrapper.text()).toBe('')
  })

  it('renders nothing when every finding was filtered out as non-tool', () => {
    const groups = groupFindingsByTool(
      [finding({ location: 'tool:add_numbers' }), finding({ location: 'dist/index.js' })],
      'github',
    )
    expect(groups).toHaveLength(0)
    expect(mountPanel({ groups }).find(panel).exists()).toBe(false)
  })
})

describe('FlaggedToolsPanel — flagged state', () => {
  const groups = groupFindingsByTool(
    [
      finding({ location: 'github:list_issues', threat_level: 'warning', rule_id: 'detect.phrase.injection' }),
      finding({ location: 'github:create_issue' }),
      finding({ location: 'github:create_issue', rule_id: 'detect.tpa.bundle', threat_level: 'warning' }),
    ],
    'github',
  )

  it('lists one row per flagged tool, dangerous first', () => {
    const wrapper = mountPanel({ groups })
    expect(wrapper.find(panel).exists()).toBe(true)
    expect(wrapper.find('[data-test="flagged-tool-row-create_issue"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="flagged-tool-row-list_issues"]').exists()).toBe(true)
    const rows = wrapper.findAll('li')
    expect(rows[0].attributes('data-test')).toBe('flagged-tool-row-create_issue')
  })

  it('shows the chip, the rule ids and a one-line detail per row', () => {
    const wrapper = mountPanel({ groups })
    const row = wrapper.get('[data-test="flagged-tool-row-create_issue"]')
    expect(row.find('[data-test="finding-chip-dangerous"]').exists()).toBe(true)
    expect(row.text()).toContain('detect.shadowing.cross_server')
    expect(row.text()).toContain('detect.tpa.bundle')
    expect(row.text()).toContain('description references cross-server tool')
  })

  it('gives a soft-only tool the warning chip, not the dangerous one', () => {
    const row = mountPanel({ groups }).get('[data-test="flagged-tool-row-list_issues"]')
    expect(row.find('[data-test="finding-chip-warning"]').exists()).toBe(true)
    expect(row.find('[data-test="finding-chip-dangerous"]').exists()).toBe(false)
  })

  it('says the findings are informational and do not block the tool', () => {
    const wrapper = mountPanel({ groups })
    expect(wrapper.get('[data-test="flagged-tools-informational"]').text()).toBe(
      'Informational — these findings do not block the tool.',
    )
  })

  it('emits show-in-description with the tool name', async () => {
    const wrapper = mountPanel({ groups })
    await wrapper.get('[data-test="flagged-tool-show-create_issue"]').trigger('click')
    expect(wrapper.emitted('show-in-description')).toEqual([['create_issue']])
  })

  it('announces staleness without hiding the findings', () => {
    const wrapper = mountPanel({ groups, stale: true })
    expect(wrapper.get('[data-test="flagged-tools-stale"]').text()).toContain(
      'predate the current tool descriptions',
    )
    expect(wrapper.find('[data-test="flagged-tool-row-create_issue"]').exists()).toBe(true)
  })
})

describe('FlaggedToolsPanel — loading and error keep the header', () => {
  it('shows skeleton rows while loading', () => {
    const wrapper = mountPanel({ loading: true })
    expect(wrapper.find(panel).exists()).toBe(true)
    expect(wrapper.findAll('[data-test="flagged-tools-loading"] .skeleton')).toHaveLength(3)
  })

  it('offers a retry on error rather than silently rendering nothing', async () => {
    const wrapper = mountPanel({ error: true })
    const error = wrapper.get('[data-test="flagged-tools-error"]')
    expect(error.text()).toContain('Could not load scan findings')
    await error.get('button').trigger('click')
    expect(wrapper.emitted('retry')).toHaveLength(1)
  })
})

// Regression: "Show in description" scrolls to the tool's card by DOM id. When
// the report names a tool the server no longer exposes — the steady state, since
// nothing rescans a `manual` server after admission — there is no card, and the
// handler's `getElementById(...) ?? return` made the button a silent no-op. The
// same happened on a disconnected server, whose tool list is empty. A row that
// cannot be shown must say so instead of offering a button that does nothing.
describe('FlaggedToolsPanel — a row with no card says so instead of offering a dead button', () => {
  const groups = groupFindingsByTool([finding()], 'github')

  it('keeps the button when the tool is in the current tool list', () => {
    const wrapper = mountPanel({ groups, presentTools: ['create_issue', 'list_issues'] })
    expect(wrapper.find('[data-test="flagged-tool-show-create_issue"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="flagged-tool-absent-create_issue"]').exists()).toBe(false)
  })

  it('replaces the button with an explanation when the tool is gone', () => {
    const wrapper = mountPanel({ groups, presentTools: ['list_issues'] })
    expect(wrapper.find('[data-test="flagged-tool-show-create_issue"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="flagged-tool-absent-create_issue"]').text()).toContain(
      'Not in the current tool list',
    )
    // The finding itself is still fully reported — only the navigation is gone.
    expect(wrapper.get('[data-test="flagged-tool-row-create_issue"]').text()).toContain('create_issue')
  })

  it('treats an EMPTY tool list as a real answer (disconnected server)', () => {
    const wrapper = mountPanel({ groups, presentTools: [] })
    expect(wrapper.find('[data-test="flagged-tool-show-create_issue"]').exists()).toBe(false)
  })

  it('keeps the button when the inventory is UNKNOWN, never mislabelling a slow load', () => {
    for (const presentTools of [undefined, null]) {
      const wrapper = mountPanel({ groups, presentTools })
      expect(wrapper.find('[data-test="flagged-tool-show-create_issue"]').exists()).toBe(true)
    }
  })
})
