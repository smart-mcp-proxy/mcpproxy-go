// Spec 081 T4 (FR-018) — Web UI reader for `release-gate consistency`.
//
// Not a Playwright test: a long-lived reader the Go driver talks to over
// stdin/stdout, one JSON line per command, so ONE page stays open across the
// check's phases (that is how the live phase tests the Web UI's update path
// rather than only its initial fetch).
//
//   load      open /ui/servers and wait for the server grid
//   snapshot  read the rendered cards as they are now (no reload)
//   reload    reload the page, then read the cards
//   quit      close the browser and exit
//
// Reply: {"ok":true,"rows":[{name, chip_class, chip_text, tool_count,
// enabled, quarantined}]} or {"ok":false,"error":"..."}.
//
// Env: MCPPROXY_BASE_URL, MCPPROXY_API_KEY (the instance under test),
// PW_CHROMIUM (optional explicit Chromium binary, as in the sweep config).
import { chromium } from '@playwright/test'
import readline from 'node:readline'

const BASE = (process.env.MCPPROXY_BASE_URL || '').replace(/\/$/, '')
const KEY = process.env.MCPPROXY_API_KEY || ''
if (!BASE) {
  process.stdout.write(JSON.stringify({ ok: false, error: 'MCPPROXY_BASE_URL is not set' }) + '\n')
  process.exit(2)
}
const url = `${BASE}/ui/servers${KEY ? `?apikey=${encodeURIComponent(KEY)}` : ''}`

const browser = await chromium.launch({
  headless: true,
  ...(process.env.PW_CHROMIUM ? { executablePath: process.env.PW_CHROMIUM } : {}),
})
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

async function waitGrid() {
  await page.waitForSelector('[data-test="server-card"], [data-test="servers-first-run-empty"]', { timeout: 30_000 })
}

async function readCards() {
  return page.$$eval('[data-test="server-card"]', (cards) =>
    cards.map((card) => {
      const chip = card.querySelector('[data-test="server-status-chip"]')
      const color = chip
        ? [...chip.classList].find((c) => /^badge-(success|warning|error|neutral|secondary|ghost|info|accent|primary)$/.test(c)) || ''
        : ''
      const tools = card.querySelector('[data-test="server-card-tool-count"]')
      const toolMatch = tools ? /(\d+)/.exec(tools.textContent || '') : null
      const toggle = (card.querySelector('[data-test="server-card-menu-toggle"]')?.textContent || '').trim()
      return {
        name: card.querySelector('[data-test="server-card-title"]')?.getAttribute('title') || '',
        chip_class: color,
        chip_text: (chip?.textContent || '').trim(),
        tool_count: toolMatch ? Number(toolMatch[1]) : null,
        enabled: toggle === 'Disable' ? true : toggle === 'Enable' ? false : null,
        quarantined: !!card.querySelector('[data-test="server-card-menu-review"]'),
      }
    }),
  )
}

const handlers = {
  async load() {
    await page.goto(url)
    await waitGrid()
    return { ok: true, rows: await readCards() }
  },
  async snapshot() {
    return { ok: true, rows: await readCards() }
  },
  async reload() {
    await page.reload()
    await waitGrid()
    return { ok: true, rows: await readCards() }
  },
}

const rl = readline.createInterface({ input: process.stdin })
for await (const raw of rl) {
  const cmd = raw.trim()
  if (!cmd) continue
  if (cmd === 'quit') break
  let reply
  try {
    const h = handlers[cmd]
    reply = h ? await h() : { ok: false, error: `unknown command ${cmd}` }
  } catch (err) {
    reply = { ok: false, error: String(err?.message || err) }
  }
  process.stdout.write(JSON.stringify(reply) + '\n')
}
await browser.close()
