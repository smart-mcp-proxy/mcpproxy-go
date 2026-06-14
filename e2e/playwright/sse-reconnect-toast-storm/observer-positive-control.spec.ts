import { test, expect } from '@playwright/test';
const BASE = 'http://127.0.0.1:18215';
const KEY = 'uitest2215';

// Positive control: prove the MutationObserver actually counts a toast node
// rendered exactly the way ToastContainer.vue renders one (div.alert inside
// .toast). If this catches it, the storm test's 0 is a true negative.
test('observer catches a real toast node', async ({ page }) => {
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
  await page.goto(`${BASE}/ui/?apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');
  // Render a toast node identical to ToastContainer's output.
  await page.evaluate(() => {
    const c = document.querySelector('.toast') || (() => {
      const d = document.createElement('div'); d.className = 'toast toast-end';
      document.body.appendChild(d); return d;
    })();
    const a = document.createElement('div');
    a.className = 'alert alert-success';
    a.innerHTML = '<div class="font-bold">CONTROL TOAST</div>';
    c.appendChild(a);
  });
  await page.waitForTimeout(300);
  const count = await page.evaluate(() => (window as any).__toastCount);
  const texts = await page.evaluate(() => (window as any).__toastTexts);
  console.log('CONTROL_COUNT=', count, 'TEXTS=', JSON.stringify(texts));
  expect(count).toBeGreaterThanOrEqual(1);
});
