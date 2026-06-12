import { describe, it, expect } from 'vitest'
import { computeToolDiffSections } from '@/utils/toolDiff'
import type { ToolApproval } from '@/types'

// MCP-2096 (MCP-2085): the tool-quarantine diff UI previously rendered ONLY
// the description diff. The backend now exposes input + output schema fields
// (previous_schema/current_schema, previous_output_schema/current_output_schema
// — PR #638), so a `changed` tool whose only change is a schema must surface a
// labeled diff section instead of reading as a phantom false positive.

function makeTool(overrides: Partial<ToolApproval>): ToolApproval {
  return {
    server_name: 'srv',
    tool_name: 'do_thing',
    status: 'changed',
    hash: 'h',
    description: '',
    ...overrides,
  }
}

describe('computeToolDiffSections (MCP-2096)', () => {
  it('produces a non-empty, labeled Output-Schema section when ONLY the output schema changed', () => {
    const tool = makeTool({
      description: 'List files',
      previous_description: 'List files',
      current_description: 'List files',
      previous_schema: '{"type":"object"}',
      current_schema: '{"type":"object"}',
      previous_output_schema: '{"type":"object","properties":{"files":{"type":"array"}}}',
      current_output_schema:
        '{"type":"object","properties":{"files":{"type":"array"},"mode":{"enum":["r","w"]}}}',
    })

    const sections = computeToolDiffSections(tool)

    // Description + input schema are identical → skipped; only output schema.
    expect(sections).toHaveLength(1)
    const out = sections[0]
    expect(out.key).toBe('output_schema')
    expect(out.label.toLowerCase()).toContain('output schema')
    expect(out.before).not.toBe(out.after)
    expect(out.before.length).toBeGreaterThan(0)
    expect(out.after).toContain('mode')
  })

  it('renders all three sections in order when description + both schemas changed', () => {
    const tool = makeTool({
      previous_description: 'old desc',
      current_description: 'new desc',
      previous_schema: '{"a":1}',
      current_schema: '{"a":2}',
      previous_output_schema: '{"b":1}',
      current_output_schema: '{"b":2}',
    })

    const keys = computeToolDiffSections(tool).map(s => s.key)
    expect(keys).toEqual(['description', 'input_schema', 'output_schema'])
  })

  it('skips sections whose fields are identical (no phantom diffs)', () => {
    const tool = makeTool({
      previous_description: 'same',
      current_description: 'same',
      previous_schema: '{"x":1}',
      current_schema: '{"x":1}',
    })
    expect(computeToolDiffSections(tool)).toHaveLength(0)
  })

  it('pretty-prints JSON schema bodies so the word-diff is readable', () => {
    const tool = makeTool({
      previous_schema: '{"type":"object","properties":{"id":{"type":"string"}}}',
      current_schema: '{"type":"object","properties":{"id":{"type":"number"}}}',
    })
    const sections = computeToolDiffSections(tool)
    expect(sections).toHaveLength(1)
    expect(sections[0].key).toBe('input_schema')
    // Pretty-printed multi-line output (indented) rather than the raw one-liner.
    expect(sections[0].after).toContain('\n')
  })

  it('falls back to the raw string when a schema body is not valid JSON', () => {
    const tool = makeTool({
      previous_output_schema: 'not-json-old',
      current_output_schema: 'not-json-new',
    })
    const sections = computeToolDiffSections(tool)
    expect(sections).toHaveLength(1)
    expect(sections[0].before).toBe('not-json-old')
    expect(sections[0].after).toBe('not-json-new')
  })
})
