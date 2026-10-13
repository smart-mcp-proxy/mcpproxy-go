import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// The approval badges use the longer labels "Changed, needs review" and
// "New, needs review". daisyUI's badge-sm has a fixed 20px height, so at
// 900-1100px viewports the label wrapped onto a second line that spilled out
// of the coloured background. Every badge rendering an approval label must stay
// on one line.
describe('Tools.vue approval badges', () => {
  it('never wraps the approval label', () => {
    const src = readFileSync(resolve(process.cwd(), 'src/views/Tools.vue'), 'utf8')
    const tags = src.match(/<span[^>]*getApprovalBadgeClass\([^>]*>/g) ?? []
    expect(tags.length).toBeGreaterThanOrEqual(2)
    for (const tag of tags) {
      expect(tag).toContain('whitespace-nowrap')
    }
  })
})
