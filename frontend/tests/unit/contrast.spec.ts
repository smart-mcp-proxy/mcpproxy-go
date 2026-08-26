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
  mixOklab,
  oklchToRgb,
  parseColor,
  rgbToOklab,
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
const SEMANTIC = ['primary', 'secondary', 'accent', 'info', 'success', 'warning', 'error'] as const

// Compare the EXACT ratio — rounding first would let 4.496 pass as 4.50 — and
// only round when a message needs to be readable.
const ratio = (a: RGB, b: RGB) => contrastRatio(a, b)
const show = (value: number) => Math.round(value * 100) / 100

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

  it('keeps ghost buttons on a coloured alert readable while hovered', () => {
    // main.css pins those buttons to the alert's content colour in every state.
    // daisyUI's hover tints the button background with 10% base-content (20%
    // is the active/focus depth), so the pair has to hold there too.
    for (const name of ['info', 'success', 'warning', 'error'] as const) {
      const fill = t[`--color-${name}`]
      const content = t[`--color-${name}-content`]
      for (const alpha of [0.1, 0.2]) {
        const hovered = alphaOver(t['--color-base-content'], fill, alpha)
        expect(
          ratio(content, hovered),
          `alert-${name} ghost button at ${alpha * 100}% hover tint (${show(ratio(content, hovered))}:1)`,
        ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
      }
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

// Every theme the picker offers carries its own generated tone ramp, so the
// guarantee is not limited to the two defaults `system` resolves to.
const OFFERED_THEMES = [
  'light',
  'dark',
  'corporate',
  'business',
  'emerald',
  'forest',
  'aqua',
  'lofi',
  'pastel',
  'fantasy',
  'wireframe',
  'luxury',
  'dracula',
  'synthwave',
  'cyberpunk',
] as const

it('covers exactly the themes the picker offers', () => {
  // Read the store as text: a new theme in the dropdown without a tone ramp
  // would otherwise silently fall back to the approximate color-mix.
  const store = readFileSync(resolve(root, 'src/stores/system.ts'), 'utf8')
  const declaration = store.slice(store.indexOf('const themes: Theme[]'))
  const block = declaration.slice(declaration.indexOf('= ['), declaration.indexOf(']\n'))
  const offered = [...block.matchAll(/name:\s*'([\w-]+)'/g)]
    .map((m) => m[1])
    .filter((n) => n !== 'system')
  expect(new Set(offered)).toEqual(new Set(OFFERED_THEMES))
  for (const theme of offered) {
    expect(
      stripComments(appCss).includes(`[data-theme="${theme}"]`),
      `main.css has no tone ramp for the offered theme "${theme}"`,
    ).toBe(true)
  }
})

describe.each(OFFERED_THEMES)('offered theme %s', (theme) => {
  const t = themeTokens(theme)

  it('has a tone ramp that clears AA on every base surface and 10% tint', () => {
    for (const name of SEMANTIC) {
      const tone = t[`--tone-${name}`]
      expect(tone, `--tone-${name} missing in ${theme}`).toBeDefined()
      const fill = t[`--color-${name}`]
      for (const surface of ['base-100', 'base-200', 'base-300'] as const) {
        const bg = t[`--color-${surface}`]
        expect(ratio(tone, bg), `${theme}: text-${name} on ${surface}`).toBeGreaterThanOrEqual(
          AA_NORMAL_TEXT,
        )
        expect(
          ratio(tone, alphaOver(fill, bg, 0.1)),
          `${theme}: text-${name} on bg-${name}/10 over ${surface}`,
        ).toBeGreaterThanOrEqual(AA_NORMAL_TEXT)
      }
    }
  })

  it('has a muted body-text colour that clears AA on every base surface', () => {
    const muted = t['--tone-muted']
    expect(muted, `--tone-muted missing in ${theme}`).toBeDefined()
    for (const surface of ['base-100', 'base-200', 'base-300'] as const) {
      expect(
        ratio(muted, t[`--color-${surface}`]),
        `${theme}: muted text on ${surface}`,
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

  it('converts sRGB to Oklab with the published reference values', () => {
    // CSS Color 4 §Oklab worked examples: pure sRGB primaries.
    const expectations: Array<[RGB, [number, number, number]]> = [
      [{ r: 1, g: 0, b: 0 }, [0.6279, 0.2249, 0.1258]],
      [{ r: 0, g: 1, b: 0 }, [0.8664, -0.2339, 0.1795]],
      [{ r: 0, g: 0, b: 1 }, [0.452, -0.0324, -0.3115]],
      [{ r: 1, g: 1, b: 1 }, [1, 0, 0]],
    ]
    for (const [rgb, expected] of expectations) {
      const got = rgbToOklab(rgb)
      for (let i = 0; i < 3; i++) {
        expect(got[i], `channel ${i} of ${JSON.stringify(rgb)}`).toBeCloseTo(expected[i], 3)
      }
    }
  })

  it('round-trips an in-gamut oklch colour', () => {
    const rgb = oklchToRgb(0.55, 0.05, 241.966)
    const [L, a, b] = rgbToOklab(rgb)
    expect(L).toBeCloseTo(0.55, 3)
    expect(Math.hypot(a, b)).toBeCloseTo(0.05, 3)
  })

  it('clips (rather than gamut-maps) colours outside sRGB', () => {
    // A browser reduces chroma to bring an out-of-gamut oklch colour into sRGB;
    // this module clips per channel instead, which shifts lightness slightly.
    // That is why the numbers here are a design-time guide and the Playwright
    // sweep measures the real page — both must agree that a pair clears AA.
    const [L] = rgbToOklab(oklchToRgb(0.55, 0.158, 241.966))
    expect(L).toBeGreaterThan(0.55)
    expect(L).toBeLessThan(0.57)
  })

  it('mixes in oklab the way CSS color-mix does', () => {
    const white = parseColor('#ffffff')
    const black = parseColor('#000000')
    // color-mix(in oklab, white 50%, black) === oklch(50% 0 0)
    const mid = mixOklab(white, black, 0.5)
    const reference = oklchToRgb(0.5, 0, 0)
    expect(mid.r).toBeCloseTo(reference.r, 3)
    expect(mid.g).toBeCloseTo(reference.g, 3)
    expect(mid.b).toBeCloseTo(reference.b, 3)
    // Mixing a colour with itself is the identity.
    const red = parseColor('#ff0000')
    const same = mixOklab(red, red, 0.5)
    expect(same.r).toBeCloseTo(red.r, 3)
    expect(same.g).toBeCloseTo(red.g, 3)
    expect(same.b).toBeCloseTo(red.b, 3)
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
