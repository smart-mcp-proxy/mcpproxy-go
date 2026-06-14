import { test, expect } from '@playwright/test';
const BASE = 'http://127.0.0.1:18215';
const KEY = 'uitest2215';

// Gold-standard repro: real EventSource drops/reconnects + real fetch
// failures while the backend core restarts repeatedly (the actual ~10s
// re-init loop from main.log). No mocking. A bash loop restarts mcpproxy
// in parallel for ~45s.
test('real backend restart loop produces zero spurious toasts', async ({ page }) => {
  await page.addInitScript(() => {
    (window as any).__toastCount = 0;
    (window as any).__toastTexts = [];
    const obs = new MutationObserver((muts) => {
      for (const m of muts) {
        m.addedNodes.forEach((n) => {
          if (!(n instanceof HTMLElement)) return;
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

  page.on('console', (m) => { if (/error|toast/i.test(m.text())) { /* keep quiet */ } });

  await page.goto(`${BASE}/ui/?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  // Sit across the restart storm, hopping the named surfaces.
  const surfaces = [
    `${BASE}/ui/#/security?apikey=${KEY}`,
    `${BASE}/ui/#/servers/alpha?apikey=${KEY}`,
    `${BASE}/ui/?apikey=${KEY}`,
  ];
  for (let i = 0; i < 18; i++) {
    await page.goto(surfaces[i % surfaces.length]).catch(() => {});
    await page.waitForTimeout(2500);
  }
  const count = await page.evaluate(() => (window as any).__toastCount);
  const texts = await page.evaluate(() => (window as any).__toastTexts);
  console.log('REAL_TOAST_COUNT=', count);
  console.log('REAL_TOAST_TEXTS=', JSON.stringify(texts));
  expect(count, `spurious toasts during real restart loop: ${JSON.stringify(texts)}`).toBe(0);
});
