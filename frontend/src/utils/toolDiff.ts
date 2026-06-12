// MCP-2096 (MCP-2085): build the list of diff sections shown in the
// tool-quarantine approval UI. A tool is flagged `changed` whenever its
// description OR input schema OR output schema hash differs from the approved
// version, but the UI historically rendered only the description diff — so a
// schema-only change read as a phantom false positive. This helper selects the
// fields that actually changed and produces readable before/after bodies (JSON
// schemas are pretty-printed) so the operator can see WHAT changed before
// approving.

import type { ToolApproval } from '@/types'

export interface ToolDiffSection {
  /** Stable key for v-for + tests. */
  key: 'description' | 'input_schema' | 'output_schema'
  /** Human label for the section header. */
  label: string
  /** Short explanation of why this section matters (e.g. additive schema). */
  hint?: string
  /** Approved (previous) body, normalized for display. */
  before: string
  /** Current body, normalized for display. */
  after: string
}

// Pretty-print a JSON schema string with 2-space indent so the word-diff is
// line-oriented and readable. Falls back to the raw string when the body isn't
// valid JSON (defensive — the backend stores schemas as JSON strings).
function formatSchema(raw: string | undefined): string {
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

export function computeToolDiffSections(tool: ToolApproval): ToolDiffSection[] {
  const sections: ToolDiffSection[] = []

  // 1. Description — plain text, no formatting.
  const prevDesc = tool.previous_description ?? ''
  const curDesc = tool.current_description ?? tool.description ?? ''
  if (prevDesc !== curDesc) {
    sections.push({
      key: 'description',
      label: 'Description changed',
      before: prevDesc,
      after: curDesc,
    })
  }

  // 2. Input schema — the tool's argument contract.
  const prevSchema = formatSchema(tool.previous_schema)
  const curSchema = formatSchema(tool.current_schema)
  if (prevSchema !== curSchema) {
    sections.push({
      key: 'input_schema',
      label: 'Input schema changed',
      hint: 'The arguments this tool accepts changed.',
      before: prevSchema,
      after: curSchema,
    })
  }

  // 3. Output schema — the shape of what the tool returns.
  const prevOut = formatSchema(tool.previous_output_schema)
  const curOut = formatSchema(tool.current_output_schema)
  if (prevOut !== curOut) {
    sections.push({
      key: 'output_schema',
      label: 'Output schema changed',
      hint: 'The shape of this tool’s results changed (e.g. a new enum value or field).',
      before: prevOut,
      after: curOut,
    })
  }

  return sections
}
