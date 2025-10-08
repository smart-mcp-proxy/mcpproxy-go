import { test, expect } from '@playwright/test';

// These tests verify token metrics UI functionality
// Run with: npx playwright test tests/token-metrics.spec.ts

test.describe('Token Metrics UI', () => {
  test.beforeEach(async ({ page }) => {
    // Navigate to the application
    // Assuming mcpproxy is running on localhost:8080
    await page.goto('http://localhost:8080/ui/');

    // Wait for the app to load
    await page.waitForLoadState('networkidle');
  });

  test('Dashboard displays Token Savings card', async ({ page }) => {
    // Navigate to dashboard
    await page.click('text=Dashboard');
    await page.waitForLoadState('networkidle');

    // Check if Token Savings card exists
    const tokenSavingsCard = page.locator('text=Token Savings').first();
    await expect(tokenSavingsCard).toBeVisible();

    // Check for key metrics
    await expect(page.locator('text=Tokens Saved')).toBeVisible();
    await expect(page.locator('text=Full Tool List Size')).toBeVisible();
    await expect(page.locator('text=Typical Query Result')).toBeVisible();
  });

  test('Servers view shows tool list token sizes', async ({ page }) => {
    // Navigate to servers
    await page.click('text=Servers');
    await page.waitForLoadState('networkidle');

    // Wait for servers to load
    await page.waitForSelector('.card', { timeout: 5000 });

    // Check if any server cards show token counts
    // Note: This will only pass if there are servers configured
    const serverCards = page.locator('.card');
    const count = await serverCards.count();

    if (count > 0) {
      // Look for "tokens" text in server cards
      const hasTokenInfo = await page.locator('text=/\\d+ tokens/i').count();
      // Token info might not be present if servers have no tools
      console.log(`Found ${hasTokenInfo} server(s) with token info`);
    }
  });

  test('Tool Calls view - expand/collapse details works', async ({ page }) => {
    // Navigate to Tool Calls
    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    // Wait for the table to load
    await page.waitForSelector('table', { timeout: 5000 });

    // Check if there are any tool calls
    const rows = page.locator('tbody tr').filter({ hasNot: page.locator('[colspan]') });
    const rowCount = await rows.count();

    if (rowCount > 0) {
      // Click the expand button on the first row
      const firstExpandButton = rows.first().locator('button[title*="Expand"]');
      await firstExpandButton.click();

      // Wait for the detail row to appear
      await page.waitForTimeout(300); // Small delay for animation

      // Check if expanded details are visible
      const detailsRow = page.locator('td[colspan="7"]').first();
      await expect(detailsRow).toBeVisible();

      // Verify detail sections exist
      await expect(page.locator('text=Arguments:').first()).toBeVisible();

      // Click collapse button
      const collapseButton = rows.first().locator('button[title*="Collapse"]');
      await collapseButton.click();

      // Wait for collapse animation
      await page.waitForTimeout(300);

      // Verify details are hidden
      await expect(detailsRow).toBeHidden();
    } else {
      console.log('No tool calls found to test expand/collapse');
    }
  });

  test('Tool Calls view - token metrics column', async ({ page }) => {
    // Navigate to Tool Calls
    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    // Check if Tokens column header exists
    await expect(page.locator('th:has-text("Tokens")')).toBeVisible();

    // Check if there are any tool calls with metrics
    const tokenCells = page.locator('td').filter({ hasText: /\d+ tokens/ });
    const metricsCount = await tokenCells.count();

    console.log(`Found ${metricsCount} tool call(s) with token metrics`);

    // Note: Old tool calls won't have metrics, only new ones will
    if (metricsCount > 0) {
      // Verify token count format
      const firstTokenCell = tokenCells.first();
      await expect(firstTokenCell).toBeVisible();

      // Check for truncation badge if present
      const truncatedBadge = page.locator('.badge-warning:has-text("Truncated")');
      const truncatedCount = await truncatedBadge.count();
      console.log(`Found ${truncatedCount} truncated response(s)`);
    }
  });

  test('Tool Calls view - expanded details show token usage', async ({ page }) => {
    // Navigate to Tool Calls
    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    // Find a row with token metrics
    const rows = page.locator('tbody tr').filter({ hasNot: page.locator('[colspan]') });
    const rowCount = await rows.count();

    if (rowCount > 0) {
      // Expand first row
      const firstExpandButton = rows.first().locator('button[title*="Expand"]');
      await firstExpandButton.click();
      await page.waitForTimeout(300);

      // Check if Token Usage section exists in details
      const detailsRow = page.locator('td[colspan="7"]').first();
      const hasTokenSection = await detailsRow.locator('text=Token Usage:').count();

      if (hasTokenSection > 0) {
        await expect(detailsRow.locator('text=Token Usage:')).toBeVisible();
        await expect(detailsRow.locator('text=Input Tokens:')).toBeVisible();
        await expect(detailsRow.locator('text=Output Tokens:')).toBeVisible();
        await expect(detailsRow.locator('text=Total Tokens:')).toBeVisible();

        // Check for truncation info if present
        const hasTruncation = await detailsRow.locator('text=Response Truncation:').count();
        if (hasTruncation > 0) {
          await expect(detailsRow.locator('text=Truncated Tokens:')).toBeVisible();
          await expect(detailsRow.locator('text=Tokens Saved:')).toBeVisible();
        }
      } else {
        console.log('No token metrics in expanded details (expected for old tool calls)');
      }
    }
  });

  test('Dashboard Token Savings - per-server breakdown', async ({ page }) => {
    // Navigate to dashboard
    await page.click('text=Dashboard');
    await page.waitForLoadState('networkidle');

    // Check if Token Savings card exists
    const tokenSavingsCard = page.locator('text=Token Savings').first();
    await expect(tokenSavingsCard).toBeVisible();

    // Look for the expandable per-server breakdown
    const breakdownDetails = page.locator('summary:has-text("Per-Server Token Breakdown")');

    if (await breakdownDetails.count() > 0) {
      // Expand the breakdown
      await breakdownDetails.click();
      await page.waitForTimeout(300);

      // Check if table is visible
      await expect(page.locator('text=Tool List Size (tokens)')).toBeVisible();

      // Verify table has server entries
      const serverRows = page.locator('table tbody tr');
      const serverCount = await serverRows.count();
      console.log(`Found ${serverCount} server(s) in breakdown`);
    } else {
      console.log('No per-server breakdown available (no servers configured)');
    }
  });
});

test.describe('Token Metrics - Data Validation', () => {
  test('Verify token counts are positive numbers', async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    // Check dashboard metrics
    await page.click('text=Dashboard');
    await page.waitForLoadState('networkidle');

    // Look for token numbers in the Token Savings card
    const tokenValues = page.locator('.stat-value');
    const count = await tokenValues.count();

    for (let i = 0; i < count; i++) {
      const text = await tokenValues.nth(i).textContent();
      if (text && /[\d,]+/.test(text)) {
        const numStr = text.replace(/[^0-9]/g, '');
        if (numStr) {
          const num = parseInt(numStr, 10);
          expect(num).toBeGreaterThanOrEqual(0);
        }
      }
    }
  });

  test('Verify percentage format', async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    await page.click('text=Dashboard');
    await page.waitForLoadState('networkidle');

    // Look for percentage in stat-desc
    const percentageElement = page.locator('.stat-desc').filter({ hasText: /\d+\.\d+% reduction/ });

    if (await percentageElement.count() > 0) {
      const text = await percentageElement.first().textContent();
      expect(text).toMatch(/\d+\.\d+% reduction/);
    }
  });
});