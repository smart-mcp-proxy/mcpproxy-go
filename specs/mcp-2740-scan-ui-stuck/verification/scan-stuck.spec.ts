import { test, expect, Page } from '@playwright/test';

// MCP-2740 verification: a finished scan must leave "Scanning…" and enable the
// Report link, even when the completing status poll reports a job id that
// differs from the one the UI started (the exact race from the bug report).

const BASE = 'http://127.0.0.1:18099';
const KEY = 'uitest2740';
const SHOT = '/tmp/uitest-2740/shots';

function json(data: any) {
  return { status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, data }) };
}

const COMPLETED_REPORT = {
  job_id: 'scan-ElevenLabs-DIFFERENT-id',
  risk_score: 0,
  scan_complete: true,
  empty_scan: false,
  scanned_at: '2026-06-17T07:18:28.660+03:00',
  summary: { total: 0, dangerous: 0, warnings: 0, info_level: 0 },
};

// scanState drives the mocked /scan/status responses for scenario 1.
type Phase = 'idle' | 'completed-diff-id';

async function installMocks(page: Page, getPhase: () => Phase, onStart: () => void, reportReady: () => boolean) {
  await page.route('**/api/v1/security/overview', (r) =>
    r.fulfill(json({ docker_available: true, scanners_enabled: 1, scanners_installed: 1, total_scans: 0, findings_by_severity: { total: 0 } })));
  await page.route('**/api/v1/security/scanners', (r) =>
    r.fulfill(json([{ id: 'tpa-descriptions', name: 'TPA Descriptions' }])));
  // Report: 404-ish (no report) until a scan completes.
  await page.route('**/api/v1/servers/ElevenLabs/scan/report', (r) => {
    if (reportReady()) return r.fulfill(json(COMPLETED_REPORT));
    return r.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: false, error: 'no report' }) });
  });
  // Status: idle until a scan starts, then the RACE response — a completed job
  // whose id (job-B) differs from the started job (job-A).
  await page.route('**/api/v1/servers/ElevenLabs/scan/status', (r) => {
    const phase = getPhase();
    if (phase === 'completed-diff-id') {
      return r.fulfill(json({ id: 'job-B', status: 'completed', scan_pass: 1, scanner_statuses: [
        { scanner_id: 'tpa-descriptions', status: 'completed', findings_count: 0 },
      ] }));
    }
    return r.fulfill(json({ id: '', status: 'idle' }));
  });
  // Start scan → returns job-A (a DIFFERENT id than the completing poll).
  await page.route('**/api/v1/servers/ElevenLabs/scan', (r) => {
    if (r.request().method() === 'POST') { onStart(); return r.fulfill(json({ id: 'job-A', status: 'running', scan_pass: 1 })); }
    return r.continue();
  });
}

test('scenario 1: sub-2s scan with mismatched job id finalizes (no stuck spinner)', async ({ page }) => {
  let phase: Phase = 'idle';
  let report = false;
  await installMocks(page, () => phase, () => { /* started */ }, () => report);

  await page.goto(`${BASE}/ui/servers/ElevenLabs?tab=security&apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');

  const btn = page.locator('[data-test="scan-button"]');
  await expect(btn).toBeVisible({ timeout: 15000 });
  await expect(btn).toHaveText(/Scan Now/);
  await expect(btn).toBeEnabled();
  await page.screenshot({ path: `${SHOT}/01-initial.png` });

  // Start the scan, then flip the backend to "completed with a different job id"
  // before the first 2s poll tick — reproducing the fast-scan race.
  await btn.click();
  phase = 'completed-diff-id';
  report = true;
  await expect(btn).toHaveText(/Scanning/, { timeout: 4000 });
  await expect(page.locator('[data-test="scan-progress"]')).toBeVisible();
  await page.screenshot({ path: `${SHOT}/02-scanning.png` });

  // The fix: the next poll derives terminal state from the backend status
  // regardless of the job-id mismatch → spinner clears, Report link enables.
  await expect(btn).toHaveText(/Scan Now/, { timeout: 8000 });
  await expect(btn).toBeEnabled();
  await expect(page.locator('[data-test="scan-report-link"]')).toBeVisible({ timeout: 8000 });
  await page.screenshot({ path: `${SHOT}/03-finalized.png` });
});

test('scenario 2: fresh mount on an already-completed scan shows report, not Scanning', async ({ page }) => {
  // Backend already terminal + report present from the very first fetch.
  await page.route('**/api/v1/security/overview', (r) =>
    r.fulfill(json({ docker_available: true, scanners_enabled: 1, scanners_installed: 1, total_scans: 1, findings_by_severity: { total: 0 } })));
  await page.route('**/api/v1/security/scanners', (r) =>
    r.fulfill(json([{ id: 'tpa-descriptions', name: 'TPA Descriptions' }])));
  await page.route('**/api/v1/servers/ElevenLabs/scan/report', (r) => r.fulfill(json(COMPLETED_REPORT)));
  await page.route('**/api/v1/servers/ElevenLabs/scan/status', (r) =>
    r.fulfill(json({ id: 'job-B', status: 'completed', scan_pass: 1 })));

  await page.goto(`${BASE}/ui/servers/ElevenLabs?tab=security&apikey=${KEY}`);
  await page.waitForLoadState('domcontentloaded');

  const btn = page.locator('[data-test="scan-button"]');
  await expect(btn).toBeVisible({ timeout: 15000 });
  await expect(btn).toHaveText(/Scan Now/);
  await expect(page.locator('[data-test="scan-progress"]')).toHaveCount(0);
  await page.screenshot({ path: `${SHOT}/04-fresh-mount-completed.png` });
});
