import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync, statSync } from 'fs'
import { join, resolve } from 'path'

// Spec 134 FR-005 / SC-003: UI chrome contains no emoji. Icons are inline SVG.
// "Emoji" here is a character with emoji presentation, or a pictograph with the
// emoji variation selector (U+FE0F). Plain typographic symbols (check, cross,
// star, hamburger) are text, not emoji, and are not flagged.

const root = resolve(__dirname, '../../src')
const EMOJI = /\p{Emoji_Presentation}|\p{Extended_Pictographic}️|[\u{1F000}-\u{1FAFF}]/u

function walk(dir: string, out: string[] = []): string[] {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) walk(path, out)
    else if (/\.(vue|ts)$/.test(name)) out.push(path)
  }
  return out
}

function stripComments(source: string): string {
  return source
    .replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:'"`])\/\/[^\n]*/g, '$1')
}

describe('no emoji in UI chrome (spec 134 FR-005)', () => {
  it('no .vue or .ts file under frontend/src contains an emoji outside comments', () => {
    const offenders: string[] = []
    for (const file of walk(root)) {
      const lines = stripComments(readFileSync(file, 'utf8')).split('\n')
      lines.forEach((line, i) => {
        const m = EMOJI.exec(line)
        if (m) offenders.push(`${file.slice(root.length + 1)}:${i + 1} ${m[0]}`)
      })
    }
    expect(offenders).toEqual([])
  })

  it('the activity type, intent and sensitivity icon maps hold no emoji', async () => {
    const mod = await import('@/utils/activity')
    const maps = [
      mod.getActivityTypeIcon('tool_call'),
      mod.getActivityTypeIcon('skill_access'),
      mod.getActivityTypeIcon('skill_quarantine_change'),
      mod.getActivityTypeIcon('does_not_exist'),
      mod.getIntentIcon('read'),
      mod.getIntentIcon('write'),
      mod.getIntentIcon('destructive'),
      mod.getIntentIcon('weird'),
    ]
    for (const icon of maps) {
      expect(typeof icon).toBe('string')
      expect(EMOJI.test(icon)).toBe(false)
    }
  })
})
