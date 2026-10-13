import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Spec 108 D39 (T151, demo finding #9): the unlayered daisyUI-v5 shim gives
// `.form-control > .label` `justify-content: space-between`. An unlayered rule
// beats the layered Tailwind `justify-start` utility, so a radio/checkbox label
// written as `<label class="label cursor-pointer justify-start ...">` directly
// inside a `.form-control` had its text pushed to the far right. One unlayered
// rule, placed after the shim, restores the author's intent for every instance.

const css = readFileSync(resolve(__dirname, '../../src/assets/main.css'), 'utf8')

// Remove every `@layer name { ... }` block (brace-matched) so what is left is
// the unlayered CSS.
function stripLayers(src: string): string {
  let out = ''
  let i = 0
  while (i < src.length) {
    const at = src.indexOf('@layer', i)
    if (at === -1) {
      out += src.slice(i)
      break
    }
    out += src.slice(i, at)
    const open = src.indexOf('{', at)
    if (open === -1) break
    let depth = 1
    let j = open + 1
    while (j < src.length && depth > 0) {
      if (src[j] === '{') depth++
      else if (src[j] === '}') depth--
      j++
    }
    i = j
  }
  return out
}

describe('form-control label shim (justify-start)', () => {
  const unlayered = stripLayers(css)

  it('has an unlayered `.form-control > .label.justify-start` rule with flex-start', () => {
    const m = unlayered.match(/\.form-control\s*>\s*\.label\.justify-start\s*\{([^}]*)\}/)
    expect(m, 'rule missing from the unlayered CSS').not.toBeNull()
    expect(m![1]).toMatch(/justify-content:\s*flex-start/)
  })

  it('comes after the space-between shim rule it overrides', () => {
    const base = unlayered.search(/\.form-control\s*>\s*\.label\s*\{/)
    const override = unlayered.search(/\.form-control\s*>\s*\.label\.justify-start\s*\{/)
    expect(base).toBeGreaterThanOrEqual(0)
    expect(override).toBeGreaterThan(base)
  })
})
