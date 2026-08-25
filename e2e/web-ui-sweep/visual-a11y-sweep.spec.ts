// Visual / accessibility sweep — the regression net for the 2026-08 UX audit
// findings F9 (WCAG AA contrast), F14 (390px layout), F30 (accessible names,
// aria-live, table caption), F29 (system theme) and F32 (header search).
//
// It runs against the Web UI served by a REAL mcpproxy binary, exactly like
// web-ui-sweep.spec.ts, and it MEASURES rather than eyeballs: every visible
// text node is resolved to its effective foreground/background (walking up
// through transparent ancestors and applying opacity) and scored with the WCAG
// 2.1 contrast formula, the same method the audit used.
//
// Launcher: scripts/run-web-smoke.sh (see docs/development/web-ui-verification.md).
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

/** Themes the app ships as its light/dark defaults; `system` resolves to one. */
const THEMES = ['corporate', 'dark'] as const

const VIEWPORTS = [
  { name: 'desktop', width: 1440, height: 900 },
  { name: 'tablet', width: 820, height: 1180 },
  { name: 'mobile', width: 390, height: 844 },
] as const

const ROUTES = ['/', '/servers', '/activity', '/tools', '/sessions'] as const

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

/**
 * Navigate, optionally pinning a theme first.
 *
 * The theme is written to localStorage and the page reloaded rather than
 * registered with `addInitScript`: init scripts ACCUMULATE across calls on the
 * same page, so a second theme would leave two setters racing on the next
 * navigation.
 */
async function goto(page: Page, route: string, theme?: string) {
  if (theme) {
    // localStorage needs an origin, so land on the page once before writing.
    if (new URL(page.url() || 'about:blank').origin !== new URL(BASE).origin) {
      await page.goto(url(route))
      await page.waitForLoadState('domcontentloaded')
    }
    await page.evaluate((t) => window.localStorage.setItem('mcpproxy-theme', t), theme)
  }
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  if (theme) {
    await expect
      .poll(() => page.evaluate(() => document.documentElement.getAttribute('data-theme')))
      .toBe(theme)
  }
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) {
    await closeWizard.click()
  }
  await page.locator('main').first().waitFor({ state: 'visible' })
  // Give SSE-driven content one tick to paint.
  await page.waitForTimeout(750)
}

interface ContrastFailure {
  text: string
  ratio: number
  fg: string
  bg: string
  selector: string
}

/**
 * Walk every visible text node, resolve its effective colours and return the
 * ones below the WCAG AA threshold for their size.
 *
 * Known limits, deliberately: the backdrop is taken from `background-color`
 * only (a gradient or background image behind text is not sampled), and text
 * over media is not evaluated. Both would need pixel sampling; the failures
 * this catches are the palette ones the audit was about. It errs toward
 * silence, never toward a false failure.
 */
async function contrastFailures(page: Page): Promise<ContrastFailure[]> {
  return page.evaluate(() => {
    // Computed styles come back in the syntax they were authored in — the
    // daisyUI themes are `oklch()`, and Chromium keeps them that way — so the
    // only reliable reader is the browser's own colour pipeline. Painting the
    // value onto a 1x1 canvas gives the sRGB bytes a user actually sees, which
    // is exactly how the audit sampled its numbers.
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = 1
    const ctx = canvas.getContext('2d', { willReadFrequently: true })!
    const cache = new Map<string, [number, number, number, number]>()
    const parse = (value: string): [number, number, number, number] => {
      const hit = cache.get(value)
      if (hit) return hit
      ctx.clearRect(0, 0, 1, 1)
      ctx.fillStyle = '#000000'
      ctx.fillStyle = value
      // An unparseable value leaves fillStyle at the previous colour; treat a
      // literal `transparent` as fully transparent rather than opaque black.
      if (/^(transparent|rgba?\(0,\s*0,\s*0,\s*0\))$/i.test(value.trim())) {
        const clear: [number, number, number, number] = [0, 0, 0, 0]
        cache.set(value, clear)
        return clear
      }
      ctx.fillRect(0, 0, 1, 1)
      const d = ctx.getImageData(0, 0, 1, 1).data
      const out: [number, number, number, number] = [d[0], d[1], d[2], d[3] / 255]
      cache.set(value, out)
      return out
    }
    const lum = ([r, g, b]: number[]) => {
      const f = (c: number) => {
        const v = c / 255
        return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)
      }
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b)
    }
    const ratio = (a: number[], b: number[]) => {
      const [hi, lo] = lum(a) >= lum(b) ? [lum(a), lum(b)] : [lum(b), lum(a)]
      return (hi + 0.05) / (lo + 0.05)
    }
    const over = (fg: number[], alpha: number, bg: number[]) =>
      [0, 1, 2].map((i) => fg[i] * alpha + bg[i] * (1 - alpha))

    /** Composite every non-opaque background from the element up to <html>. */
    const effectiveBackground = (el: Element): number[] => {
      const stack: Array<[number[], number]> = []
      let node: Element | null = el
      while (node) {
        const cs = getComputedStyle(node)
        const [r, g, b, a] = parse(cs.backgroundColor)
        const alpha = a * Number(cs.opacity || '1')
        if (alpha > 0) stack.push([[r, g, b], alpha])
        if (alpha >= 1) break
        node = node.parentElement
      }
      let base = [255, 255, 255]
      for (let i = stack.length - 1; i >= 0; i--) {
        base = over(stack[i][0], stack[i][1], base)
      }
      return base
    }

    const cumulativeOpacity = (el: Element): number => {
      let o = 1
      let node: Element | null = el
      while (node) {
        o *= Number(getComputedStyle(node).opacity || '1')
        node = node.parentElement
      }
      return o
    }

    const describe = (el: Element): string => {
      const cls = (el.className || '').toString().split(/\s+/).filter(Boolean).slice(0, 4)
      return `${el.tagName.toLowerCase()}${cls.length ? '.' + cls.join('.') : ''}`
    }

    const failures: Array<{
      text: string
      ratio: number
      fg: string
      bg: string
      selector: string
    }> = []
    const seen = new Set<Element>()

    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT)
    let node: Node | null
    while ((node = walker.nextNode())) {
      const text = (node.textContent || '').trim()
      if (!text) continue
      const el = node.parentElement
      if (!el || seen.has(el)) continue
      seen.add(el)
      // WCAG 1.4.3 exempts disabled controls and purely decorative text.
      if (
        el.closest(
          'script, style, svg, [aria-hidden="true"], .sr-only, [disabled], .btn-disabled, .tab-disabled',
        )
      ) {
        continue
      }

      const rect = el.getBoundingClientRect()
      if (rect.width < 2 || rect.height < 2) continue
      const cs = getComputedStyle(el)
      if (cs.visibility === 'hidden' || cs.display === 'none') continue

      const [r, g, b, a] = parse(cs.color)
      const alpha = a * cumulativeOpacity(el)
      if (alpha <= 0.05) continue
      const bg = effectiveBackground(el)
      const fg = over([r, g, b], alpha, bg)

      const size = parseFloat(cs.fontSize)
      const weight = Number(cs.fontWeight) || 400
      const large = size >= 24 || (size >= 18.66 && weight >= 700)
      const threshold = large ? 3 : 4.5

      const value = ratio(fg, bg)
      if (value + 0.005 < threshold) {
        failures.push({
          text: text.slice(0, 70),
          ratio: Math.round(value * 100) / 100,
          fg: `rgb(${fg.map((c) => Math.round(c)).join(' ')})`,
          bg: `rgb(${bg.map((c) => Math.round(c)).join(' ')})`,
          selector: describe(el),
        })
      }
    }
    return failures
  })
}

// ---------------------------------------------------------------------------
// F9 — WCAG AA contrast on every main screen, in both shipped themes.
// ---------------------------------------------------------------------------
for (const theme of THEMES) {
  for (const route of ROUTES) {
    test(`contrast AA: ${route} (${theme})`, async ({ page }) => {
      await goto(page, route, theme)
      const failures = await contrastFailures(page)
      expect(
        failures,
        `WCAG AA contrast failures on ${route} (${theme}):\n` +
          failures
            .map((f) => `  ${f.ratio}:1  ${f.selector}  ${f.fg} on ${f.bg}  "${f.text}"`)
            .join('\n'),
      ).toEqual([])
    })
  }
}

test('contrast AA: filled primary buttons in both themes', async ({ page }) => {
  // Measured on the BUTTON elements directly. Filtering the generic walker's
  // output by class would silently match nothing, because a button's label
  // usually lives in a nested <span> and that span is what the walker reports.
  for (const theme of THEMES) {
    await goto(page, '/servers', theme)
    const measured = await page.evaluate(() => {
      const canvas = document.createElement('canvas')
      canvas.width = canvas.height = 1
      const ctx = canvas.getContext('2d', { willReadFrequently: true })!
      const resolve = (value: string) => {
        ctx.clearRect(0, 0, 1, 1)
        ctx.fillStyle = '#000000'
        ctx.fillStyle = value
        ctx.fillRect(0, 0, 1, 1)
        const d = ctx.getImageData(0, 0, 1, 1).data
        return [d[0], d[1], d[2]]
      }
      const lum = (c: number[]) =>
        c
          .map((v) => v / 255)
          .map((v) => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)))
          .reduce((acc, v, i) => acc + v * [0.2126, 0.7152, 0.0722][i], 0)

      const out: Array<{ label: string; ratio: number }> = []
      document.querySelectorAll('.btn-primary').forEach((el) => {
        const button = el as HTMLElement
        if (button.hasAttribute('disabled') || button.classList.contains('btn-disabled')) return
        const rect = button.getBoundingClientRect()
        if (rect.width < 2 || rect.height < 2) return
        const cs = getComputedStyle(button)
        const fg = resolve(cs.color)
        const bg = resolve(cs.backgroundColor)
        const a = lum(fg)
        const b = lum(bg)
        const ratio = (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
        out.push({ label: (button.textContent || '').trim().slice(0, 40), ratio })
      })
      return out
    })

    expect(measured.length, `no .btn-primary rendered in ${theme}`).toBeGreaterThan(0)
    const failures = measured.filter((m) => m.ratio + 0.005 < 4.5)
    expect(
      failures,
      `primary button contrast in ${theme}: ` +
        failures.map((f) => `${f.ratio.toFixed(2)}:1 "${f.label}"`).join(', '),
    ).toEqual([])
  }
})

test('contrast AA: buttons and links on a coloured alert, hovered', async ({ page }) => {
  // daisyUI paints a base surface into `--btn-bg` on hover, which on a coloured
  // alert dropped a ghost button to 3.47:1. Hover is only reachable with a real
  // pointer, so it is measured here rather than in the unit test.
  //
  // Real elements only: injecting markup that uses a class the app never ships
  // measures nothing meaningful, because Tailwind only compiles the classes it
  // finds in the source — an injected `.btn-link` inherits plain `.btn` styling
  // and reports a failure that cannot occur in the product.
  let measured = 0
  for (const theme of THEMES) {
    for (const route of ['/', '/servers'] as const) {
      await goto(page, route, theme)
      const targets = page.locator(
        ':is(.alert-info, .alert-success, .alert-warning, .alert-error) :is(.btn-ghost, a.link, .btn-link)',
      )
      const count = await targets.count()
      for (let i = 0; i < count; i++) {
        const target = targets.nth(i)
        if (!(await target.isVisible().catch(() => false))) continue
        await target.hover()
        const result = await target.evaluate((el) => {
          const canvas = document.createElement('canvas')
          canvas.width = canvas.height = 1
          const ctx = canvas.getContext('2d', { willReadFrequently: true })!
          const resolve = (v: string) => {
            ctx.clearRect(0, 0, 1, 1)
            ctx.fillStyle = '#000000'
            ctx.fillStyle = v
            ctx.fillRect(0, 0, 1, 1)
            const d = ctx.getImageData(0, 0, 1, 1).data
            return [d[0], d[1], d[2], d[3] / 255]
          }
          const lum = (c: number[]) =>
            c
              .slice(0, 3)
              .map((v) => v / 255)
              .map((v) => (v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4)))
              .reduce((acc, v, i) => acc + v * [0.2126, 0.7152, 0.0722][i], 0)
          const cs = getComputedStyle(el as HTMLElement)
          const alertBg = resolve(getComputedStyle((el as HTMLElement).closest('.alert')!).backgroundColor)
          const own = resolve(cs.backgroundColor)
          const bg = [0, 1, 2].map((i) => own[i] * own[3] + alertBg[i] * (1 - own[3]))
          const fg = resolve(cs.color)
          const [hi, lo] = lum(fg) >= lum(bg) ? [lum(fg), lum(bg)] : [lum(bg), lum(fg)]
          return {
            ratio: Math.round(((hi + 0.05) / (lo + 0.05)) * 100) / 100,
            label: (el.textContent || '').trim().slice(0, 30),
          }
        })
        measured++
        expect(
          result.ratio,
          `${theme} ${route}: "${result.label}" inside a coloured alert measures ${result.ratio}:1 while hovered`,
        ).toBeGreaterThanOrEqual(4.5 - 0.005)
      }
    }
  }
  expect(measured, 'no ghost/link controls inside a coloured alert were found').toBeGreaterThan(0)
})

// ---------------------------------------------------------------------------
// F29 — "match system" theme.
// ---------------------------------------------------------------------------
test('system theme follows the OS colour scheme', async ({ browser }) => {
  const dark = await browser.newPage({ colorScheme: 'dark' })
  await dark.goto(url('/'))
  await dark.waitForLoadState('domcontentloaded')
  await expect
    .poll(() => dark.evaluate(() => document.documentElement.getAttribute('data-theme')))
    .toBe('dark')
  await dark.close()

  const light = await browser.newPage({ colorScheme: 'light' })
  await light.goto(url('/'))
  await light.waitForLoadState('domcontentloaded')
  await expect
    .poll(() => light.evaluate(() => document.documentElement.getAttribute('data-theme')))
    .toBe('corporate')
  await light.close()
})

test('an explicit theme choice survives the OS preference', async ({ browser }) => {
  const page = await browser.newPage({ colorScheme: 'dark' })
  await page.addInitScript(() => window.localStorage.setItem('mcpproxy-theme', 'corporate'))
  await page.goto(url('/'))
  await page.waitForLoadState('domcontentloaded')
  await expect
    .poll(() => page.evaluate(() => document.documentElement.getAttribute('data-theme')))
    .toBe('corporate')
  await page.close()
})

// ---------------------------------------------------------------------------
// F14 — 390px layout: nothing clips, no horizontal page scroll, Status visible.
// ---------------------------------------------------------------------------
for (const vp of VIEWPORTS) {
  test(`layout holds at ${vp.width}px (${vp.name})`, async ({ page }) => {
    await page.setViewportSize({ width: vp.width, height: vp.height })
    for (const route of ROUTES) {
      await goto(page, route)
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      )
      expect(overflow, `horizontal page overflow on ${route} at ${vp.width}px`).toBeLessThanOrEqual(1)
    }
  })
}

test('activity keeps the Status column on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await goto(page, '/activity')
  const rows = page.locator('[data-test="activity-row"]')
  if ((await rows.count()) === 0) test.skip(true, 'no activity records on this instance')
  const status = rows.first().locator('[data-test="activity-status"]')
  await expect(status).toBeVisible()
  const box = await status.boundingBox()
  expect(box, 'status cell has no box').not.toBeNull()
  expect(box!.x + box!.width, 'Status column is clipped off the right edge').toBeLessThanOrEqual(390)
  // …and the label itself must not be cut off inside its own cell.
  const clipped = await status.evaluate((el) => {
    const cell = el.closest('td')!
    return cell.scrollWidth - cell.clientWidth
  })
  expect(clipped, 'Status label is clipped inside its cell').toBeLessThanOrEqual(1)
})

test('the footer never overlaps the page content', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await goto(page, '/activity')
  const overlap = await page.evaluate(() => {
    const footer = document.querySelector('footer')
    const main = document.querySelector('main')
    if (!footer || !main) return 0
    return main.getBoundingClientRect().bottom - footer.getBoundingClientRect().top
  })
  expect(overlap, 'main content bleeds under the footer').toBeLessThanOrEqual(1)
})

// ---------------------------------------------------------------------------
// F30 — accessible names, live region, table caption.
// ---------------------------------------------------------------------------
test('every form control on the main screens has an accessible name', async ({ page }) => {
  for (const route of ROUTES) {
    await goto(page, route)
    const unnamed = await page.evaluate(() => {
      const out: string[] = []
      const controls = document.querySelectorAll('input, select, textarea')
      controls.forEach((el) => {
        const c = el as HTMLInputElement
        if (c.type === 'hidden') return
        const rect = c.getBoundingClientRect()
        if (rect.width < 2 || rect.height < 2) return
        // An `aria-labelledby` only names a control if its target exists and
        // actually has text, so it is resolved rather than trusted.
        const labelledBy = (c.getAttribute('aria-labelledby') || '')
          .split(/\s+/)
          .filter(Boolean)
          .map((id) => document.getElementById(id))
          .filter((n): n is HTMLElement => !!n && !!(n.textContent || '').trim())
        const labelled =
          (c.getAttribute('aria-label') || '').trim() ||
          labelledBy.length > 0 ||
          (c.getAttribute('placeholder') || '').trim() ||
          (c.getAttribute('title') || '').trim() ||
          (c.id && document.querySelector(`label[for="${CSS.escape(c.id)}"]`)) ||
          c.closest('label')
        if (!labelled) {
          out.push(`${c.tagName.toLowerCase()}[type=${c.type}] .${c.className}`)
        }
      })
      return out
    })
    expect(unnamed, `unnamed form controls on ${route}`).toEqual([])
  }
})

test('the activity table announces updates and carries a caption', async ({ page }) => {
  await goto(page, '/activity')
  // The live region reports the row count in every state, empty included, so
  // this check does not depend on the instance having traffic.
  await expect(page.locator('[data-test="activity-live-region"]')).toHaveCount(1)
  await expect(page.locator('[data-test="activity-live-region"]')).toHaveAttribute(
    'aria-live',
    'polite',
  )
  await expect(page.locator('[data-test="activity-live-region"]')).toContainText(/\d+ of \d+/)

  // The caption only exists when there is a table to caption.
  if ((await page.locator('[data-test="activity-row"]').count()) > 0) {
    const captions = await page.locator('table caption').count()
    expect(captions, 'activity table has no <caption>').toBeGreaterThan(0)
  }
})

test('activity rows open their details from the keyboard, at every width', async ({ page }) => {
  // Clicking the row is a shortcut; the per-row button is the real control, and
  // it must survive the narrow layout (it used to be hidden below `sm`).
  for (const width of [1440, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await goto(page, '/activity')
    const rows = page.locator('[data-test="activity-row"]')
    if ((await rows.count()) === 0) {
      test.skip(true, 'no activity records on this instance')
    }
    const open = rows.first().locator('button[aria-label^="Open details"]')
    await expect(open, `row button missing at ${width}px`).toBeVisible()
    await open.focus()
    await page.keyboard.press('Enter')
    await expect(page.locator('#activity-detail-drawer')).toBeChecked()
    await page.keyboard.press('Escape')
  }
})

// ---------------------------------------------------------------------------
// F32 — the header search must not read as disabled at rest.
// ---------------------------------------------------------------------------
test('header search button is enabled with an empty box', async ({ page }) => {
  await goto(page, '/')
  const button = page.locator('[data-test="header-search-button"]')
  await expect(button).toBeEnabled()
  await expect(page.locator('[data-test="header-search-input"]')).toHaveValue('')
  // Clicking with an empty query is a no-op, not a navigation.
  await button.click()
  await page.waitForTimeout(250)
  expect(page.url()).not.toContain('/search')
})
