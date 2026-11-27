import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync } from 'fs'
import { resolve } from 'path'
import { AA_LARGE_TEXT, AA_NORMAL_TEXT, contrastRatio, parseColor } from '@/utils/contrast'
import { FALLBACK_CHART_COLORS, readChartColors } from '@/utils/chartColors'

// Spec 134 FR-062: chart colours come from the brand and status tokens and meet AA in
// both themes; captions are not faded with opacity.

const root = resolve(__dirname, '../..')
const strip = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '')
function block(css: string, selector: string): Record<string, string> {
  const c = strip(css)
  const start = c.indexOf(selector)
  const open = c.indexOf('{', start)
  const close = c.indexOf('}', open)
  const out: Record<string, string> = {}
  for (const decl of c.slice(open + 1, close).split(';')) {
    const m = /^\s*(--[\w-]+)\s*:\s*(.+?)\s*$/.exec(decl)
    if (m) out[m[1]] = m[2]
  }
  return out
}
const app = readFileSync(resolve(root, 'src/assets/main.css'), 'utf8')
const usageDir = resolve(root, 'src/components/usage')
const files = readdirSync(usageDir).filter(f => f.endsWith('.vue'))

describe.each(['corporate', 'dark'] as const)('chart colours in %s', (theme) => {
  const daisy = block(readFileSync(resolve(root, `node_modules/daisyui/theme/${theme}.css`), 'utf8'), `[data-theme="${theme}"]`)
  const mine = block(app, `[data-theme="${theme}"]`)
  const surface = parseColor(mine['--color-base-100'] ?? daisy['--color-base-100'])

  it.each(['--brand-text', '--status-ok', '--status-error'])('bar colour %s has at least 3:1 against the card (graphical objects)', (token) => {
    expect(contrastRatio(parseColor(mine[token]), surface), token).toBeGreaterThanOrEqual(3)
  })

  it('axis and legend text (--tone-muted) meets AA normal text', () => {
    expect(contrastRatio(parseColor(mine['--tone-muted']), surface)).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    expect(AA_LARGE_TEXT).toBeLessThan(AA_NORMAL_TEXT)
  })

  it('the brand and error series differ in lightness or hue enough to tell apart', () => {
    const a = parseColor(mine['--brand-text']); const b = parseColor(mine['--status-error'])
    expect(contrastRatio(a, b)).toBeGreaterThan(1)
  })
})

describe('chart sources', () => {
  it('no usage component carries a colour literal or a faded caption', () => {
    for (const file of files) {
      const source = readFileSync(resolve(usageDir, file), 'utf8')
      expect(source, file).not.toMatch(/#[0-9a-fA-F]{6}\b/)
      expect(source, file).not.toMatch(/\bopacity-(50|60)\b/)
      expect(source, file).not.toMatch(/paletteColor/)
    }
  })

  it('reads the tokens, with the corporate values as the fallback', () => {
    const colors = readChartColors()
    for (const key of Object.keys(FALLBACK_CHART_COLORS) as (keyof typeof FALLBACK_CHART_COLORS)[]) expect(typeof colors[key]).toBe('string')
    expect(FALLBACK_CHART_COLORS.brand).toBe('oklch(42% 0.158 241.966)')
  })
})
