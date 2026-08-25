/**
 * Colour maths for the WCAG contrast guarantees the UI ships with.
 *
 * The Web UI is themed with daisyUI tokens expressed in `oklch()`. To be able to
 * *assert* (rather than eyeball) that our text/background pairs clear WCAG 2.1 AA,
 * this module implements the same conversions a browser performs:
 *
 *   oklch → oklab → linear sRGB → sRGB   (CSS Color 4)
 *   color-mix(in oklab, …)               (CSS Color 5)
 *   alpha compositing of `bg-x/10` tints (CSS Compositing)
 *   relative luminance + contrast ratio  (WCAG 2.1 §1.4.3)
 *
 * `frontend/tests/unit/contrast.spec.ts` parses the shipped theme CSS and runs
 * every audited pair through these functions, so a token regression fails CI.
 *
 * One deliberate simplification: an oklch colour outside the sRGB gamut is
 * CLIPPED per channel here, where a browser reduces chroma to map it in. The
 * two differ by a hair of lightness, so these numbers are the design-time
 * guide and `e2e/web-ui-sweep/visual-a11y-sweep.spec.ts` measures what the
 * browser actually painted.
 */

export interface RGB {
  r: number
  g: number
  b: number
}

const clamp01 = (x: number) => (x < 0 ? 0 : x > 1 ? 1 : x)

/** sRGB gamma encoding (linear-light → 0..1 channel value). */
function encodeSrgb(x: number): number {
  const v = clamp01(x)
  return v <= 0.0031308 ? 12.92 * v : 1.055 * Math.pow(v, 1 / 2.4) - 0.055
}

/** sRGB gamma decoding (0..1 channel value → linear-light). */
function decodeSrgb(x: number): number {
  return x <= 0.04045 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4)
}

export function oklabToRgb(L: number, a: number, b: number): RGB {
  const l_ = L + 0.3963377774 * a + 0.2158037573 * b
  const m_ = L - 0.1055613458 * a - 0.0638541728 * b
  const s_ = L - 0.0894841775 * a - 1.291485548 * b
  const l = l_ * l_ * l_
  const m = m_ * m_ * m_
  const s = s_ * s_ * s_
  return {
    r: encodeSrgb(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
    g: encodeSrgb(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
    b: encodeSrgb(-0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s),
  }
}

export function oklchToRgb(L: number, C: number, hDeg: number): RGB {
  const h = (hDeg * Math.PI) / 180
  return oklabToRgb(L, C * Math.cos(h), C * Math.sin(h))
}

export function rgbToOklab(c: RGB): [number, number, number] {
  const r = decodeSrgb(c.r)
  const g = decodeSrgb(c.g)
  const b = decodeSrgb(c.b)
  const l = Math.cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
  const m = Math.cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
  const s = Math.cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
  // LMS' -> Oklab (CSS Color 4, §Oklab). The middle row is the one that is easy
  // to mangle: its coefficients are NOT shared with the other two.
  return [
    0.2104542553 * l + 0.793617785 * m - 0.0040720468 * s,
    1.9779984951 * l - 2.428592205 * m + 0.4505937099 * s,
    0.0259040371 * l + 0.7827717662 * m - 0.808675766 * s,
  ]
}

/**
 * Parse the colour syntaxes the theme files actually use: `oklch(L% C H)`
 * (with an optional `/ alpha`, which is ignored — callers composite explicitly),
 * `#rgb` / `#rrggbb`, and `rgb(r g b)` / `rgb(r, g, b)`.
 */
export function parseColor(input: string): RGB {
  const value = input.trim()

  const oklch = /^oklch\(\s*([\d.]+)%\s+([\d.]+)\s+([\d.]+)/i.exec(value)
  if (oklch) {
    return oklchToRgb(Number(oklch[1]) / 100, Number(oklch[2]), Number(oklch[3]))
  }

  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(value)
  if (hex) {
    const h = hex[1]
    const full = h.length === 3 ? h.split('').map((ch) => ch + ch).join('') : h
    return {
      r: parseInt(full.slice(0, 2), 16) / 255,
      g: parseInt(full.slice(2, 4), 16) / 255,
      b: parseInt(full.slice(4, 6), 16) / 255,
    }
  }

  const rgb = /^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i.exec(value)
  if (rgb) {
    return { r: Number(rgb[1]) / 255, g: Number(rgb[2]) / 255, b: Number(rgb[3]) / 255 }
  }

  throw new Error(`unsupported colour syntax: ${input}`)
}

/** CSS `color-mix(in oklab, a <weight>%, b)` for two opaque colours. */
export function mixOklab(a: RGB, b: RGB, weightA: number): RGB {
  const A = rgbToOklab(a)
  const B = rgbToOklab(b)
  const w = clamp01(weightA)
  return oklabToRgb(
    A[0] * w + B[0] * (1 - w),
    A[1] * w + B[1] * (1 - w),
    A[2] * w + B[2] * (1 - w),
  )
}

/**
 * Composite `fg` at `alpha` over an opaque `bg` — what the browser does for a
 * Tailwind tint such as `bg-warning/10`. Compositing happens in the (gamma
 * encoded) sRGB space the canvas reports, which is why the ratios computed here
 * match the values the audit sampled from a live page.
 */
export function alphaOver(fg: RGB, bg: RGB, alpha: number): RGB {
  const a = clamp01(alpha)
  return {
    r: fg.r * a + bg.r * (1 - a),
    g: fg.g * a + bg.g * (1 - a),
    b: fg.b * a + bg.b * (1 - a),
  }
}

/** WCAG 2.1 relative luminance. */
export function relativeLuminance(c: RGB): number {
  return (
    0.2126 * decodeSrgb(c.r) + 0.7152 * decodeSrgb(c.g) + 0.0722 * decodeSrgb(c.b)
  )
}

/** WCAG 2.1 contrast ratio, 1..21. */
export function contrastRatio(a: RGB, b: RGB): number {
  const la = relativeLuminance(a)
  const lb = relativeLuminance(b)
  const [hi, lo] = la >= lb ? [la, lb] : [lb, la]
  return (hi + 0.05) / (lo + 0.05)
}

/** WCAG 2.1 AA threshold: 4.5:1 for body text, 3:1 for large text / UI parts. */
export const AA_NORMAL_TEXT = 4.5
export const AA_LARGE_TEXT = 3

export function meetsAA(ratio: number, large = false): boolean {
  return ratio >= (large ? AA_LARGE_TEXT : AA_NORMAL_TEXT)
}
