import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import { AA_NORMAL_TEXT, contrastRatio, parseColor, type RGB } from '@/utils/contrast'

// Spec 134 FR-001/FR-002/FR-007: the design-system tokens exist in both themes,
// the brand blue is the same primary fill in both, and every text tone clears
// WCAG AA on its own soft background and on the theme's base surfaces.

const root = resolve(__dirname, '../..')
const strip = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '')
function block(css: string, selector: string): Record<string, string> {
  const c = strip(css)
  const start = c.indexOf(selector)
  if (start === -1) throw new Error(`selector not found: ${selector}`)
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
const color = (v: string): RGB => parseColor(v)

const TONES = ['ok', 'review', 'error', 'busy', 'off'] as const
const TOKENS = ['--brand', '--on-brand', '--brand-text', '--brand-soft', '--border', '--focus',
  ...TONES.flatMap((t) => [`--status-${t}`, `--status-${t}-soft`])]

describe.each(['corporate', 'dark'] as const)('design tokens in %s', (theme) => {
  const daisy = block(readFileSync(resolve(root, `node_modules/daisyui/theme/${theme}.css`), 'utf8'), `[data-theme="${theme}"]`)
  const mine = block(app, `[data-theme="${theme}"]`)
  const surfaces = ['--color-base-100', '--color-base-200', '--color-base-300'].map((k) => color(mine[k] ?? daisy[k]))

  it('defines every token', () => {
    for (const k of TOKENS) expect(mine[k], `${k} in ${theme}`).toBeDefined()
  })

  it('uses a 4 px radius', () => {
    for (const k of ['--radius-selector', '--radius-field', '--radius-box']) expect(mine[k]).toBe('0.25rem')
  })

  it('primary is the brand blue with white content (FR-002)', () => {
    expect(mine['--color-primary']).toBe('oklch(55% 0.158 241.966)')
    expect(contrastRatio(color(mine['--on-brand']), color(mine['--color-primary']))).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })

  it.each(TONES)('status %s text clears AA on its soft background and on base surfaces', (tone) => {
    const fg = color(mine[`--status-${tone}`])
    const soft = color(mine[`--status-${tone}-soft`])
    expect(contrastRatio(fg, soft), `${tone} on soft`).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    for (const s of surfaces) expect(contrastRatio(fg, s), `${tone} on base`).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })

  it('brand text clears AA on base surfaces and on brand-soft', () => {
    const fg = color(mine['--brand-text'])
    expect(contrastRatio(fg, color(mine['--brand-soft']))).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    for (const s of surfaces) expect(contrastRatio(fg, s)).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })
})

describe('no text below 12 px (FR-006)', () => {
  it('no arbitrary text size under 12 px in src', async () => {
    const { execSync } = await import('child_process')
    const out = execSync(`grep -rnE "text-\\[(0|1[01])(\\.[0-9]+)?px\\]|text-\\[0\\.[0-7][0-9]*rem\\]" src || true`, { cwd: root }).toString()
    expect(out.trim()).toBe('')
  })
})
