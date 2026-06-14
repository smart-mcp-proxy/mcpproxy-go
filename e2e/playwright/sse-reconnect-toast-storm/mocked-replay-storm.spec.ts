import { test, expect } from '@playwright/test';

const BASE = 'http://127.0.0.1:18215';
const KEY = 'uitest2215';

// Build one "replay burst" — the sequence the backend re-emits on every
// reconnect / re-init: a status snapshot, the full server list, a scanner
// status, and a config reload. This is the exact dynamic trigger described
// in MCP-2215 (SSE-reconnect replay storm).
function burst(seq: number): string {
  const servers = ['alpha', 'bravo', 'charlie/remote'].map((name, i) => ({
    id: name, name, protocol: i === 2 ? 'http' : 'stdio',
    enabled: true, quarantined: false,
    connected: seq % 2 === 0, connecting: seq % 2 !== 0,
    status: seq % 2 === 0 ? 'ready' : 'connecting',
    tool_count: seq % 2 === 0 ? 5 : 0,
    oauth_status: i === 2 ? 'expired' : 'none',
    last_error: i === 2 ? 'OAuth authentication required' : '',
  }));
  const status = { running: true, listen_addr: '127.0.0.1:18215', timestamp: 1700000000 + seq };
  const scanner = { scanner_id: 'trivy', status: seq % 2 === 0 ? 'enabled' : 'pulling', error: '' };
  let s = '';
  s += `event: status\ndata: ${JSON.stringify(status)}\n\n`;
  s += `event: servers.changed\ndata: ${JSON.stringify({ payload: { servers } })}\n\n`;
  s += `event: security.scanner_changed\ndata: ${JSON.stringify(scanner)}\n\n`;
  s += `event: config.reloaded\ndata: ${JSON.stringify({ reason: 'reinit' })}\n\n`;
  return s;
}

function stormBody(bursts: number): string {
  let body = '';
  for (let i = 0; i < bursts; i++) body += burst(i);
  return body;
}

test('SSE reconnect-replay storm produces zero spurious toasts', async ({ page }) => {
  const toastTexts: string[] = [];

  // Force a reconnect storm: every /events connection delivers 40 replay
  // bursts then ends, so EventSource reconnects and replays again.
  await page.route('**/events**', async (route) => {
    await route.fulfill({
      status: 200,
      headers: { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' },
      body: stormBody(40),
    });
  });

  // Cumulatively count every toast DOM node ever added (auto-dismiss would
  // otherwise hide them from a point-in-time DOM query).
  await page.addInitScript(() => {
    (window as any).__toastCount = 0;
    (window as any).__toastTexts = [];
    const obs = new MutationObserver((muts) => {
      for (const m of muts) {
        m.addedNodes.forEach((n) => {
          if (!(n instanceof HTMLElement)) return;
          // DaisyUI toast items live inside the .toast container.
          const isToast = n.classList?.contains('alert') && !!n.closest?.('.toast.toast-end');
          if (isToast) {
            (window as any).__toastCount++;
            (window as any).__toastTexts.push((n.textContent || '').trim().slice(0, 80));
          }
        });
      }
    });
    obs.observe(document, { childList: true, subtree: true });
  });

  // 1. Land on the servers list (renders one ServerCard per server).
  await page.goto(`${BASE}/ui/?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(4000); // let storm bursts replay

  // 2. Open the Security page (scanner_changed replay target).
  await page.goto(`${BASE}/ui/#/security?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(4000);

  // 3. Open a server detail / scan-report view.
  await page.goto(`${BASE}/ui/#/servers/alpha?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  await page.waitForTimeout(4000);

  const count = await page.evaluate(() => (window as any).__toastCount);
  const texts = await page.evaluate(() => (window as any).__toastTexts);
  console.log('TOAST_COUNT=', count);
  console.log('TOAST_TEXTS=', JSON.stringify(texts));

  await page.screenshot({ path: 'storm-final.png', fullPage: true });

  expect(count, `spurious toasts during reconnect storm: ${JSON.stringify(texts)}`).toBe(0);
});
