/**
 * WCAG AA contrast guarantees for the shipped themes (UX audit F9).
 *
 * This suite resolves the REAL tokens: it reads daisyUI's own theme files from
 * node_modules and layers `src/assets/main.css`'s `[data-theme=…]` overrides on
 * top, exactly as the browser does. A token regression — ours or an upstream
 * daisyUI bump — therefore fails here rather than in production.
 *
 * The "before" ratios in the comments are the values the audit sampled from a
 * live page (canvas-resolved, effective ratio including opacity).
 */
import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'
import {
  AA_NORMAL_TEXT,
  alphaOver,
  contrastRatio,
  parseColor,
  type RGB,
} from '@/utils/contrast'

const root = resolve(__dirname, '../..')

const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, '')

/** Pull `--custom-property: value;` pairs out of the first matching CSS block. */
function readTokens(source: string, selector: string): Record<string, string> {
  const css = stripComments(source)
  const start = css.indexOf(selector)
  if (start === -1) throw new Error(`selector not found: ${selector}`)
  const open = css.indexOf('{', start)
  const close = css.indexOf('}', open)
  const body = css.slice(open + 1, close)
  const tokens: Record<string, string> = {}
  for (const line of body.split(';')) {
    const m = /^\s*(--[\w-]+)\s*:\s*(.+?)\s*$/.exec(line)
    if (m) tokens[m[1]] = m[2]
  }
  return tokens
}

const appCss = readFileSync(resolve(root, 'src/assets/main.css'), 'utf8')

function themeTokens(theme: string): Record<string, RGB> {
  const daisy = readFileSync(
    resolve(root, `node_modules/daisyui/theme/${theme}.css`),
    'utf8',
  )
  const merged = {
    ...readTokens(daisy, `[data-theme="${theme}"]`),
    ...readTokens(appCss, `[data-theme="${theme}"]`),
  }
  const out: Record<string, RGB> = {}
  for (const [k, v] of Object.entries(merged)) {
    if (!v.startsWith('oklch') && !v.startsWith('#') && !v.startsWith('rgb')) continue
    out[k] = parseColor(v)
  }
  return out
}

const THEMES = ['corporate', 'dark'] as const
const SEMANTIC = ['primary', 'info', 'success', 'warning', 'error'] as const

// Round the way a report does, so failures print a readable number.
const ratio = (a: RGB, b: RGB) => Math.round(contrastRatio(a, b) * 100) / 100

describe.each(THEMES)('theme %s', (theme) => {
  const t = themeTokens(theme)

  const surfaces = () => [
    { name: 'base-100', color: t['--color-base-100'] },
    { name: 'base-200', color: t['--color-base-200'] },
    { name: 'base-300', color: t['--color-base-300'] },
  ]

  it('declares a --tone-* ramp for every semantic colour', () => {
    for (const name of SEMANTIC) {
      expect(t[`--tone-${name}`], `--tone-${name} missing in ${theme}`).toBeDefined()
    }
  })

  it.each(SEMANTIC)(
    'text-%s clears AA on every base surface and on its own 10% tint',
    (name) => {
      const tone = t[`--tone-${name}`]
      const fill = t[`--color-${name}`]
      for (const surface of surfaces()) {
        // Plain text on the surface — e.g. Activity's "4 errors" (was 2.92:1).
        expect(
          ratio(tone, surface.color),
          `text-${name} on ${surface.name}`,
        ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)

        // Tinted chip — e.g. the Dashboard "Docker isolation disabled" warning
        // (`bg-warning/10 text-warning`), which measured 1.28:1 in corporate.
        const tint = alphaOver(fill, surface.color, 0.1)
        expect(
          ratio(tone, tint),
          `text-${name} on bg-${name}/10 over ${surface.name}`,
        ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
      }
    },
  )

  it('filled buttons/badges pass AA against their content colour', () => {
    // `Add Server` / `Connect Clients` / `Restart`: white on primary was 4.13:1
    // in BOTH themes — every filled primary button in the app failed by the
    // same margin.
    for (const name of SEMANTIC) {
      const fill = t[`--color-${name}`]
      const content = t[`--color-${name}-content`]
      expect(
        ratio(content, fill),
        `${name}-content on ${name} fill`,
      ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    }
  })

  it('a code chip inverted onto a coloured alert stays readable', () => {
    // The `dig <hostname>` remediation chip inside alert-error rendered
    // base-200 under the alert's own dark content colour: 1.07:1 in dark.
    // main.css now paints the chip with the alert's content colour and prints
    // the fill colour on top, so the pair is the theme's own fill/content pair.
    for (const name of ['info', 'success', 'warning', 'error'] as const) {
      const fill = t[`--color-${name}`]
      const content = t[`--color-${name}-content`]
      expect(ratio(fill, content), `alert-${name} code chip`).toBeGreaterThanOrEqual(
        AA_NORMAL_TEXT,
      )
    }
  })

  it('keeps base text comfortably above AA', () => {
    for (const surface of surfaces()) {
      expect(
        ratio(t['--color-base-content'], surface.color),
        `base-content on ${surface.name}`,
      ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    }
  })
})

describe('audited regressions (before -> after)', () => {
  it('primary fills no longer sit at 4.13:1 with their content colour', () => {
    for (const theme of THEMES) {
      const t = themeTokens(theme)
      const after = ratio(t['--color-primary-content'], t['--color-primary'])
      expect(after, `${theme} primary button`).toBeGreaterThan(4.13)
      expect(after).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    }
  })

  it('the corporate security chips clear AA (were 1.28 and 2.69)', () => {
    const t = themeTokens('corporate')
    const warningChip = alphaOver(t['--color-warning'], t['--color-base-200'], 0.1)
    const successChip = alphaOver(t['--color-success'], t['--color-base-200'], 0.1)
    expect(ratio(t['--tone-warning'], warningChip)).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
    expect(ratio(t['--tone-success'], successChip)).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
  })

  it('the token-savings badge clears AA in both themes (were 3.37 / 3.60)', () => {
    for (const theme of THEMES) {
      const t = themeTokens(theme)
      const badge = alphaOver(t['--color-primary'], t['--color-base-200'], 0.1)
      expect(ratio(t['--tone-primary'], badge), theme).toBeGreaterThanOrEqual(
        AA_NORMAL_TEXT,
      )
    }
  })
})

describe('contrast maths', () => {
  it('matches known WCAG reference values', () => {
    const white = parseColor('#ffffff')
    const black = parseColor('#000000')
    expect(ratio(white, black)).toBe(21)
    expect(ratio(white, white)).toBe(1)
    // #767676 on white is the canonical "exactly AA" grey.
    expect(ratio(parseColor('#767676'), white)).toBeGreaterThanOrEqual(4.5)
    expect(ratio(parseColor('#777777'), white)).toBeLessThan(4.6)
  })

  it('parses the oklch syntax the themes use', () => {
    const white100 = parseColor('oklch(100% 0 0)')
    expect(white100.r).toBeCloseTo(1, 5)
    expect(white100.g).toBeCloseTo(1, 5)
    expect(white100.b).toBeCloseTo(1, 5)
    const black = parseColor('oklch(0% 0 0)')
    expect(black.r).toBeCloseTo(0, 5)
  })
})
