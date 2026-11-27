import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// The approval badges use the longer labels "Changed, needs review" and
// "New, needs review". daisyUI's badge-sm has a fixed 20px height, so at
// 900-1100px viewports the label wrapped onto a second line that spilled out
// of the coloured background. Every badge rendering an approval label must stay
// on one line.
// Spec 134 PR 6: these cases read the flat table; the page opens grouped by default.
window.localStorage.setItem('mcpproxy.tools.layout', 'flat')

describe('Tools.vue approval badges', () => {
  it('never wraps the approval label', () => {
    // The flat table badge stays in Tools.vue; the dialog badge moved to ToolDetails (PR 6).
    const src = ['src/views/Tools.vue', 'src/components/tools/ToolDetails.vue']
      .map(f => readFileSync(resolve(process.cwd(), f), 'utf8'))
      .join('\n')
    const tags = src.match(/<(?:span|StateTag)[^>]*(?:getApprovalBadgeClass\(|:tone="approvalTone")[^>]*>/g) ?? []
    expect(tags.length).toBeGreaterThanOrEqual(2)
    for (const tag of tags) {
      expect(tag).toContain('whitespace-nowrap')
    }
  })
})
