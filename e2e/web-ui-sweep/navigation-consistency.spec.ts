// Navigation-consistency sweep (Spec 109 "ux-navigation-consistency").
//
// Created here by PR 109-e (T068) for its own independent test — server
// cards render at a stable height whatever state they are in (FR-013) — and
// extended by later PRs in the same spec (109-i, T137) as their own
// navigation-consistency scenarios land. Registered in the Playwright file
// list in scripts/run-web-smoke.sh so the smoke gate runs it from this PR
// onward, and every later addition to this file is gated too.
//
// Launcher: scripts/run-web-smoke.sh (boots a real mcpproxy instance with its
// embedded frontend, never a dev server).
import { test, expect, Page } from '@playwright/test'

const BASE = process.env.MCPPROXY_BASE_URL || 'http://127.0.0.1:18080'
const KEY = process.env.MCPPROXY_API_KEY || ''

function url(route: string): string {
  const sep = route.includes('?') ? '&' : '?'
  return KEY ? `${BASE}/ui${route}${sep}apikey=${encodeURIComponent(KEY)}` : `${BASE}/ui${route}`
}

async function goto(page: Page, route: string, anchor: string) {
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) {
    await closeWizard.click()
  }
  await page.locator(anchor).first().waitFor({ state: 'visible' })
}

// Spec 109 FR-013 / D15: "Every card in a grid has the same height" — a fixed
// grid, not one that jumps as servers move between states (connected,
// quarantined, erroring, disabled, sign-in-required, ...). Measured at
// 1440px, the desktop breakpoint the grid's `lg:grid-cols-3` targets.
//
// Review round 2 (109-e medium finding): two config-identical, healthy
// fixture servers both land in the SAME grid row at this breakpoint, where
// CSS grid's `align-items:stretch` equalizes every card in a row regardless
// of content — `distinct.size === 1` passed unconditionally whether or not
// the `.server-card { min-height }` rule this invariant depends on even
// existed. scripts/run-web-smoke.sh now seeds 4 fixture servers, one
// quarantined, so the fleet both spans more than one grid row (stretch can
// no longer paper over a row-to-row difference) and includes a card whose
// content genuinely differs from the rest. The row check below turns the
// previously-silent false pass into an explicit, loud skip whenever the
// fixture set is too small to actually exercise the invariant.
test('server cards render at equal heights at 1440px (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })

  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const cards = page.locator('[data-test="server-card"]')
  const count = await cards.count()

  // A fleet with fewer than two servers cannot exercise the invariant, but
  // must not silently report a pass either. scripts/run-web-smoke.sh
  // registers fixture servers (review rounds 1 and 2, 109-e) specifically so
  // this test runs under the release-qa-gate `web-ui-sweep` job from this PR
  // onward; a hand run with no MCPPROXY_FIXTURE_PATH, or a leaner fixture
  // set, still falls back to skipping rather than reporting a false pass.
  test.skip(count < 2, 'fewer than two server cards rendered; nothing to compare')

  const heights: number[] = []
  const tops: number[] = []
  for (let i = 0; i < count; i++) {
    const box = await cards.nth(i).boundingBox()
    expect(box, `card ${i} has no layout box`).not.toBeNull()
    heights.push(Math.round(box!.height))
    tops.push(Math.round(box!.y))
  }

  // Cluster the cards' top offsets into grid rows (a few px of layout jitter
  // within one row is expected; a real row boundary is a much bigger jump).
  const sortedTops = [...tops].sort((a, b) => a - b)
  let rowCount = 1
  for (let i = 1; i < sortedTops.length; i++) {
    if (sortedTops[i] - sortedTops[i - 1] > 8) rowCount++
  }
  // If every card sits in the same grid row, CSS grid's `align-items:stretch`
  // equalizes their heights regardless of content or CSS — the assertion
  // below would pass whether or not the invariant it names actually holds.
  // Skip loudly instead of reporting a pass that tested nothing.
  test.skip(rowCount < 2,
    `all ${count} cards share one grid row at 1440px; CSS grid stretch makes height equality trivially true here — a larger MCPPROXY_FIXTURE_PATH fleet (scripts/run-web-smoke.sh) is needed to span a second row`)

  const distinct = new Set(heights)
  expect(distinct.size, `expected one height across ${count} cards spanning ${rowCount} grid rows, got ${[...distinct].sort((a, b) => a - b).join(', ')}`).toBe(1)
})

// The primary-action row reserves its height even for a `ready` server with
// no button — the row that would otherwise be the only variable-height part
// of an otherwise fixed grid.
//
// Review round 1 (109-e medium finding): the row's "Details" link is
// unconditional — every card renders it whether or not the primary button
// does — so a bounding-box `height > 0` check passes on the Details link
// alone and would still pass with the `min-h-[2.25rem]` reservation this
// test is named after deleted entirely. Reading the CSS `min-height` the
// row's own stylesheet applies, instead of the box Playwright measured
// after layout, tests the reservation itself rather than something else
// that happens to fill the same space.
test('the primary-action row keeps its height with no button (Spec 109 FR-013)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/servers', '[data-test="kpi-card-total"], [data-test="servers-first-run-empty"]')

  const rows = page.locator('[data-test="server-card-primary-row"]')
  const count = await rows.count()
  test.skip(count === 0, 'no server cards rendered')

  for (let i = 0; i < count; i++) {
    const row = rows.nth(i)
    const box = await row.boundingBox()
    expect(box, `primary-action row ${i} has no layout box`).not.toBeNull()
    expect(box!.height).toBeGreaterThan(0)

    const minHeightPx = await row.evaluate((el) => parseFloat(getComputedStyle(el).minHeight) || 0)
    expect(minHeightPx, `primary-action row ${i} has no min-height CSS reservation — a "ready" card's Details link would still render without it`).toBeGreaterThan(0)
  }
})

// ---------------------------------------------------------------------------
// Spec 109-i (T137): navigation and header sweep. Extends the file created by
// 109-e (T068) and takes over the two header tests that used to live in
// visual-a11y-sweep.spec.ts (the F32 search button, the Add Server button),
// now the palette and "+ Add" tests below.
// ---------------------------------------------------------------------------

const THEMES = ['corporate', 'dark'] as const
const WIDTHS = [1440, 1100, 900, 390] as const
const MATRIX_ROUTES = ['/', '/servers', '/tools', '/activity', '/clients'] as const

/** Pin a theme the same way visual-a11y-sweep does: localStorage, then reload. */
async function gotoThemed(page: Page, route: string, theme: string) {
  if (new URL(page.url() || 'about:blank').origin !== new URL(BASE).origin) {
    await page.goto(url(route))
    await page.waitForLoadState('domcontentloaded')
  }
  await page.evaluate((t) => window.localStorage.setItem('mcpproxy-theme', t), theme)
  await page.goto(url(route))
  await page.waitForLoadState('domcontentloaded')
  await expect
    .poll(() => page.evaluate(() => document.documentElement.getAttribute('data-theme')))
    .toBe(theme)
  const closeWizard = page.locator('[data-test="close-wizard"]')
  if (await closeWizard.isVisible().catch(() => false)) {
    await closeWizard.click()
  }
  await page.locator('header [data-test="header-add-menu"]').waitFor({ state: 'visible' })
  // Let the SSE-fed status pill and the attention list settle.
  await page.waitForTimeout(500)
}

async function apiGet(page: Page, path: string) {
  const res = await page.request.get(`${BASE}${path}`, { headers: KEY ? { 'X-API-Key': KEY } : {} })
  expect(res.ok(), `${path} -> ${res.status()}`).toBe(true)
  return (await res.json()).data
}

const HEADER_CONTROLS = [
  'header-drawer-toggle',
  'header-search-input',
  'header-search-icon',
  'header-viewing-slot',
  'header-status-pill',
  'header-attention-pill',
  'header-add-menu',
] as const

// FR-053 / H1 / H3: the header is one row that never overflows, whatever the
// width or theme, and collapses its text (not its controls) below 1100px.
for (const width of WIDTHS) {
  for (const theme of THEMES) {
    test(`header and sidebar lay out cleanly at ${width}px (${theme})`, async ({ page }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      const attention = await apiGet(page, '/api/v1/attention')
      const attentionCount: number = attention.count ?? attention.items?.length ?? 0
      const wide = width >= 1100

      for (const route of MATRIX_ROUTES) {
        const where = `${route} at ${width}px (${theme})`
        await gotoThemed(page, route, theme)

        const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
        expect(overflow, `horizontal page overflow on ${where}`).toBeLessThanOrEqual(0)

        const header = page.locator('header').first()
        const headerBox = (await header.boundingBox())!
        const visible: string[] = []
        for (const id of HEADER_CONTROLS) {
          const control = header.locator(`[data-test="${id}"]`)
          if (!(await control.isVisible().catch(() => false))) continue
          visible.push(id)
          const box = (await control.boundingBox())!
          expect(box.x, `${id} clipped left on ${where}`).toBeGreaterThanOrEqual(-0.5)
          expect(box.x + box.width, `${id} clipped right on ${where}`).toBeLessThanOrEqual(width + 0.5)
          expect(box.x + box.width, `${id} outside the header on ${where}`).toBeLessThanOrEqual(headerBox.x + headerBox.width + 0.5)
          const clipped = await control.evaluate((el) => el.scrollWidth - el.clientWidth)
          expect(clipped, `${id} text is cut off on ${where}`).toBeLessThanOrEqual(1)
        }

        expect(visible.includes('header-search-input'), `search input visibility on ${where}`).toBe(wide)
        expect(visible.includes('header-search-icon'), `search icon visibility on ${where}`).toBe(!wide)
        expect(visible.includes('header-drawer-toggle'), `drawer toggle visibility on ${where}`).toBe(width < 1024)

        const pill = (await header.locator('[data-test="header-status-pill"]').innerText()).trim()
        if (wide) expect(pill, `status pill on ${where}`).toMatch(/\d+ of \d+ online/)
        else expect(pill, `status pill on ${where}`).toMatch(/^\s*●?\s*\d+\/\d+\s*$/)

        const add = (await header.locator('[data-test="header-add-menu"]').innerText()).trim()
        if (wide) expect(add, `add button on ${where}`).toContain('Add')
        else expect(add, `add button on ${where}`).not.toContain('Add')

        if (width === 390) {
          const expected = ['header-drawer-toggle', 'header-search-icon', 'header-status-pill']
          if (attentionCount > 0) expected.push('header-attention-pill')
          expected.push('header-add-menu')
          expect(visible, `the phone header holds exactly these controls on ${where}`).toEqual(expected)
        }
      }
    })
  }
}

// FR-050 / N1 / N7: one sidebar structure at desktop width.
test('the sidebar has Home, Connect, Protect, Monitor and the footer (Spec 109 FR-050)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/', 'aside [data-test="sidebar-item-home"]')

  const groups = await page.locator('aside [data-test^="sidebar-group-"]').evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-test')),
  )
  expect(groups).toEqual(['sidebar-group-connect', 'sidebar-group-protect', 'sidebar-group-monitor'])

  const items = await page.locator('aside [data-test^="sidebar-item-"]').evaluateAll((els) =>
    els.map((el) => el.getAttribute('data-test')!.replace('sidebar-item-', '')),
  )
  expect(items).toEqual([
    'home', 'clients', 'servers', 'tools', 'review', 'secrets', 'activity', 'usage',
    'settings', 'docs', 'feedback', 'theme',
  ])

  for (const gone of ['/ui/tokens', '/ui/sessions', '/ui/repositories', '/ui/security']) {
    await expect(page.locator(`aside a[href$="${gone}"]`), `${gone} must not be linked from the sidebar`).toHaveCount(0)
  }
  await expect(page.locator('aside [data-testid="sidebar-version-block"]')).toBeVisible()
})

// FR-054: the palette opens on the keyboard, sends nothing until there is
// text, searches once (debounced, limit 8) and Enter falls through to Tools.
test('the command palette opens on Cmd/Ctrl+K and "/" and searches once (Spec 109 FR-054)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const indexRequests: string[] = []
  page.on('request', (req) => {
    if (req.url().includes('/api/v1/index/search')) indexRequests.push(req.url())
  })
  const servers = await apiGet(page, '/api/v1/servers')
  const first: string | undefined = servers.servers?.[0]?.name
  test.skip(!first, 'no fixture servers; the palette server row cannot be exercised')

  await goto(page, '/', 'header [data-test="header-add-menu"]')
  const palette = page.locator('dialog[data-test="command-palette"]')

  await page.keyboard.press('ControlOrMeta+K')
  await expect(palette).toHaveAttribute('open', '')
  await page.waitForTimeout(500)
  expect(indexRequests, 'opening the palette must not call /index/search').toHaveLength(0)

  await page.keyboard.type(first!)
  await expect(palette.locator('[data-test^="palette-row-servers-"]').first()).toContainText(first!)
  await expect.poll(() => indexRequests.length).toBe(1)
  const params = new URL(indexRequests[0]).searchParams
  expect(params.get('q')).toBe(first)
  expect(params.get('limit')).toBe('8')

  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`/ui/tools\\?q=${encodeURIComponent(first!)}`))
  await expect(palette).not.toHaveAttribute('open', '')

  // "/" from the page body reopens it.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.keyboard.press('/')
  await expect(palette).toHaveAttribute('open', '')
  await page.keyboard.press('Escape')
  await expect(palette).not.toHaveAttribute('open', '')
})

test('the header search field opens the palette on focus (Spec 109 FR-054)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/', 'header [data-test="header-search-input"]')
  await page.locator('[data-test="header-search-input"]').click()
  await expect(page.locator('dialog[data-test="command-palette"]')).toHaveAttribute('open', '')
  await expect(page.locator('dialog[data-test="command-palette"] input')).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[data-test="command-palette"]')).not.toHaveAttribute('open', '')
  // Closing must not bounce focus back into the field and reopen the palette.
  await page.waitForTimeout(250)
  await expect(page.locator('dialog[data-test="command-palette"]')).not.toHaveAttribute('open', '')
})

// FR-052: "+ Add" — Server, Client, Token.
test('the "+ Add" menu opens Server, Client and Token (Spec 109 FR-052)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/activity', 'header [data-test="header-add-menu"]')

  const open = async () => {
    await page.locator('[data-test="header-add-menu"]').click()
    await expect(page.locator('[role="menu"] [role="menuitem"]')).toHaveCount(3)
  }
  await open()
  await expect(page.locator('[data-test="add-menu-server"]')).toBeVisible()
  await expect(page.locator('[data-test="add-menu-client"]')).toBeVisible()
  await expect(page.locator('[data-test="add-menu-token"]')).toBeVisible()
  await expect(page.locator('[data-test="add-menu-profile"]')).toHaveCount(0)

  await page.locator('[data-test="add-menu-server"]').click()
  await expect(page).toHaveURL(/\/ui\/add-server(?:\?|$)/)
  await expect(page.locator('[data-test="add-server-page"] h1')).toHaveText('Add Server')
  await expect(page.locator('[data-test="add-server-tab-catalog"]')).toHaveClass(/tab-active/)

  await open()
  await page.locator('[data-test="add-menu-token"]').click()
  await expect(page).toHaveURL(/\/ui\/clients\?tab=tokens(?!.*create)/)
  await expect(page.locator('dialog[open]', { hasText: 'Create Agent Token' })).toBeVisible()
  await page.keyboard.press('Escape')

  await open()
  await page.locator('[data-test="add-menu-client"]').click()
  await expect(page.locator('dialog[data-test="client-connect-list"][open]')).toBeVisible()
})

// navigation-map.md "Redirects (query kept)".
const REDIRECTS: Array<[string, RegExp]> = [
  ['/overview?x=1', /\/ui\/\?x=1$/],
  ['/tokens?token=t&x=1', /\/ui\/clients\?(?=.*tab=tokens)(?=.*token=t)(?=.*x=1)/],
  ['/sessions?session=s', /\/ui\/activity\?(?=.*view=sessions)(?=.*session=s)/],
  ['/security?x=1', /\/ui\/review\?x=1$/],
  ['/repositories?q=a', /\/ui\/add-server\?(?=.*q=a)(?=.*tab=catalog)/],
  ['/search?q=a', /\/ui\/tools\?q=a$/],
]
for (const [from, expected] of REDIRECTS) {
  test(`${from} redirects and keeps its query (Spec 109 navigation-map)`, async ({ page }) => {
    await page.goto(url(from))
    await expect(page).toHaveURL(expected)
  })
}

// ---------------------------------------------------------------------------
// Link map (url-filter-contract.md, SC-009): each in-app deep link lands on
// the target and the page's own REST requests carry every filter — never an
// unfiltered fetch. The collector is attached BEFORE goto/click.
// ---------------------------------------------------------------------------
function collect(page: Page, pattern: RegExp): string[] {
  const seen: string[] = []
  page.on('request', (req) => {
    const u = new URL(req.url())
    if (pattern.test(u.pathname)) seen.push(req.url())
  })
  return seen
}

function everyCarries(requests: string[], expected: Record<string, string | null>, label: string) {
  expect(requests.length, `${label}: no REST request was issued`).toBeGreaterThan(0)
  for (const req of requests) {
    const params = new URL(req).searchParams
    for (const [key, value] of Object.entries(expected)) {
      if (value === null) expect(params.has(key), `${label}: ${req} lacks ${key}`).toBe(true)
      else expect(params.get(key), `${label}: ${req}`).toBe(value)
    }
  }
}

const STRIP: Array<[string, string, Record<string, string | null>]> = [
  ['usage-strip-calls', '/ui/activity?view=calls&from=-24h', { start_time: null }],
  ['usage-strip-blocked', '/ui/activity?view=calls&from=-24h&status=blocked', { start_time: null, status: 'blocked' }],
  ['usage-strip-errors', '/ui/activity?view=calls&from=-24h&status=error', { start_time: null, status: 'error' }],
]
for (const [id, landing, rest] of STRIP) {
  test(`Home strip ${id} lands on ${landing} with filtered REST (SC-009)`, async ({ page }) => {
    await goto(page, '/', `[data-test="${id}"]`)
    const seen = collect(page, /^\/api\/v1\/activity$/)
    await page.locator(`[data-test="${id}"]`).click()
    await expect(page).toHaveURL(new RegExp(landing.replace(/[?.]/g, '\\$&')))
    await page.locator('main').first().waitFor({ state: 'visible' })
    await page.waitForTimeout(1000)
    everyCarries(seen, rest, id)
  })
}

test('Activity deep links for server and tool carry their REST filters (SC-009)', async ({ page }) => {
  const servers = await apiGet(page, '/api/v1/servers')
  const name: string | undefined = servers.servers?.[0]?.name
  test.skip(!name, 'no fixture servers')

  const seen = collect(page, /^\/api\/v1\/activity$/)
  await goto(page, `/activity?view=calls&server=${encodeURIComponent(name!)}&from=-24h`, 'main')
  await page.waitForTimeout(1000)
  everyCarries(seen, { server: name!, start_time: null }, 'server stats line')

  seen.length = 0
  await goto(page, `/activity?view=calls&tool=${encodeURIComponent(`${name}:echo`)}`, 'main')
  await page.waitForTimeout(1000)
  everyCarries(seen, { server: name!, tool: 'echo' }, 'tool calls link')
})

test('the usage chart bars link to a filtered Activity (SC-009)', async ({ page }) => {
  await goto(page, '/usage', 'main')
  await page.waitForTimeout(500)
  test.skip(true, 'the bars are canvas elements with no DOM link to click; usage-chart-bar-links.spec.ts covers the link shape and this fixture records no calls')
})

test('Review queue rows open the server review (SC-009)', async ({ page }) => {
  await goto(page, '/review', 'main')
  const link = page.locator('[data-test^="servers-review-link-"]').first()
  test.skip((await link.count()) === 0, 'no server is awaiting review in this fixture')
  const name = (await link.getAttribute('data-test'))!.replace('servers-review-link-', '')
  await link.click()
  await expect(page).toHaveURL(new RegExp(`/ui/review/${encodeURIComponent(name)}(?:\\?|$)`))
})

test('Activity sessions rows open their calls (SC-009)', async ({ page }) => {
  await goto(page, '/activity?view=sessions', 'main')
  await page.waitForTimeout(500)
  const row = page.locator('[data-test="sessions-row"]').first()
  test.skip((await row.count()) === 0, 'no MCP sessions recorded on this instance')
})

test('the status pill opens Servers and attention "See all" opens Home (Spec 109 FR-053)', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await goto(page, '/tools', 'header [data-test="header-status-pill"]')
  await page.locator('[data-test="header-status-pill"]').click()
  await expect(page).toHaveURL(/\/ui\/servers(?:\?|$)/)

  const pill = page.locator('[data-test="header-attention-pill-button"]')
  test.skip((await pill.count()) === 0, 'nothing needs attention on this instance')
  await goto(page, '/tools', 'header [data-test="header-attention-pill"]')
  await pill.click()
  await page.locator('[data-test="header-attention-see-all"]').click()
  await expect(page).toHaveURL(/\/ui\/(?:\?|$)/)
})

// Rows that need features.scope_filters stay hidden until the core lists it.
test('client, profile and token scope links stay hidden without scope_filters (Spec 109 FR-080a)', async ({ page }) => {
  await goto(page, '/clients', '[data-test="clients-page"]')
  await page.waitForTimeout(500)
  const rows = page.locator('[data-test="clients-page"] tbody tr')
  if ((await rows.count()) > 0) await rows.first().click()
  for (const key of ['client', 'profile', 'token']) {
    await expect(
      page.locator(`[data-test="clients-page"] a[href*="${key}="]`),
      `a ${key}= link must stay hidden until features.scope_filters lists it`,
    ).toHaveCount(0)
  }
})
