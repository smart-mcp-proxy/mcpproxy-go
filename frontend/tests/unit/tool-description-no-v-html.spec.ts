import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// The security invariant of the whole TPA highlighting feature, enforced where
// it actually runs.
//
// Tool descriptions are attacker-authored — they ARE the Tool Poisoning Attack
// payload — and ToolDescription.vue is the one component that renders them with
// markup interleaved. A single `v-html` there turns the highlighter into the
// delivery vehicle.
//
// This is a test and not an ESLint rule because ESLint enforces nothing in this
// repo today: `npm run lint` is `echo 'Linting skipped …'`, and eslint.config.cjs
// is an eslintrc-shaped object while the installed ESLint is v10, which reads
// only flat config — `npx eslint src/components/ToolDescription.vue` reports
// "File ignored because no matching configuration was supplied" and exits 0. A
// `vue/no-v-html` override added there would be a comment that reads like a
// guarantee and enforces nothing, which is worse than no guard at all. Vitest
// runs on every change, so the guard lives here until the flat-config migration
// lands.
const COMPONENTS = ['ToolDescription.vue', 'FlaggedToolsPanel.vue', 'FindingChip.vue']

// Resolved from the vitest root (frontend/), which vitest.config.ts pins.
function readComponent(name: string): string {
  return readFileSync(resolve(process.cwd(), 'src/components', name), 'utf8')
}

// Deliberately NO comment stripping. An earlier version of this guard ran the
// source through /<!--[\s\S]*?-->/g before matching, so the component's own
// header comment — which names `v-html` in order to forbid it — could not trip
// the assertion. CodeQL flagged that (js/incomplete-multi-character-sanitization,
// alert 534) and it was right: a single-pass strip is defeatable. A source file
// containing the literal `<!--` inside a string, a real `v-html=` after it, and
// a `-->` later would have had the real usage deleted before matching, and the
// guard would have passed while the component shipped the vulnerability. A guard
// that can be silenced by the code it guards is worse than no guard.
//
// Stripping turned out to be unnecessary anyway: the patterns below both require
// characters that the prohibition prose does not contain — `v-html=` needs the
// equals sign, and no comment in these files mentions innerHTML at all. The raw
// sources match neither.
//
// If you are adding a comment to one of these components, write the directive
// without the equals sign (`v-html`, not `v-html=`) or this guard will fire on
// your prose. That is the intended trade: a false alarm you fix in a second,
// rather than a hole you never see.

describe('scan-finding components never render upstream text as HTML', () => {
  for (const name of COMPONENTS) {
    it(`${name} contains no v-html`, () => {
      // `v-html=` and `:v-html`/`v-bind` forms all carry the '=' — prose does not.
      expect(readComponent(name)).not.toMatch(/v-html\s*=/)
    })
  }

  it('ToolDescription.vue contains no innerHTML assignment either', () => {
    expect(readComponent('ToolDescription.vue')).not.toMatch(/innerHTML/)
  })

  it('the guard is wired to a file that actually exists', () => {
    // A typo'd path would make every assertion above vacuously true by throwing
    // in a way a future refactor could silence.
    expect(readComponent('ToolDescription.vue')).toContain('data-test="tool-description"')
  })
})
