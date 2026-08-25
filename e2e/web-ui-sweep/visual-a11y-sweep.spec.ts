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

async function goto(page: Page, route: string, theme?: string) {
  if (theme) {
    await page.addInitScript((t) => {
      window.localStorage.setItem('mcpproxy-theme', t)
    }, theme)
  }
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
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
  for (const theme of THEMES) {
    await goto(page, '/servers', theme)
    const failures = (await contrastFailures(page)).filter((f) =>
      f.selector.includes('btn-primary'),
    )
    expect(failures, `primary button contrast in ${theme}`).toEqual([])
  }
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
        const labelled =
          c.getAttribute('aria-label') ||
          c.getAttribute('aria-labelledby') ||
          c.getAttribute('placeholder') ||
          c.getAttribute('title') ||
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
  await expect(page.locator('[data-test="activity-live-region"]')).toHaveCount(1)
  await expect(page.locator('[data-test="activity-live-region"]')).toHaveAttribute(
    'aria-live',
    'polite',
  )
  const captions = await page.locator('table caption').count()
  expect(captions, 'activity table has no <caption>').toBeGreaterThan(0)
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
