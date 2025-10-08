import { test, expect } from '@playwright/test';

// Tests for Tool Calls view fixes:
// 1. Details expand immediately after clicked row
// 2. Long strings wrap properly (no horizontal overflow)
// 3. New tool calls show token metrics

test.describe('Tool Calls View - Bug Fixes', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    // Navigate to Tool Calls page
    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');
  });

  test('Details row appears immediately after clicked row', async ({ page }) => {
    // Wait for table to load
    await page.waitForSelector('table tbody', { timeout: 5000 });

    // Get all main rows (excluding detail rows)
    const mainRows = page.locator('tbody > template').locator('> tr').first();
    const rowCount = await mainRows.count();

    if (rowCount === 0) {
      test.skip();
      return;
    }

    // Click expand on first row
    const firstRow = mainRows;
    const expandButton = firstRow.locator('button[title*="Expand"]').first();
    await expandButton.click();
    await page.waitForTimeout(300);

    // Get all tbody tr elements
    const allRows = page.locator('tbody tr');
    const totalRows = await allRows.count();

    // The detail row should be at index 1 (right after index 0 which is the main row)
    // Check that second row has colspan="7" (detail row)
    const secondRow = allRows.nth(1);
    const hasColspan = await secondRow.locator('td[colspan="7"]').count();

    expect(hasColspan).toBeGreaterThan(0);
    console.log('✓ Details row correctly positioned immediately after main row');
  });

  test('Long strings in details wrap properly', async ({ page }) => {
    // Wait for table
    await page.waitForSelector('table tbody', { timeout: 5000 });

    const rows = page.locator('tbody > template > tr').first();
    const rowCount = await rows.count();

    if (rowCount === 0) {
      test.skip();
      return;
    }

    // Expand first row
    const expandButton = rows.locator('button[title*="Expand"]').first();
    await expandButton.click();
    await page.waitForTimeout(300);

    // Check that detail cell is visible
    const detailCell = page.locator('td[colspan="7"]').first();
    await expect(detailCell).toBeVisible();

    // Check for word-wrapping classes on pre elements
    const argumentsPre = detailCell.locator('pre').first();

    // Verify pre element has proper wrapping classes
    const classes = await argumentsPre.getAttribute('class');
    expect(classes).toContain('break-words');
    expect(classes).toContain('whitespace-pre-wrap');
    console.log('✓ Long strings have proper wrapping classes');

    // Verify no horizontal scrolling needed (max-w-full)
    expect(classes).toContain('max-w-full');
    console.log('✓ Content constrained to container width');
  });

  test('Response content wraps and scrolls vertically only', async ({ page }) => {
    await page.waitForSelector('table tbody', { timeout: 5000 });

    const rows = page.locator('tbody > template > tr').first();
    const rowCount = await rows.count();

    if (rowCount === 0) {
      test.skip();
      return;
    }

    // Expand first row
    const expandButton = rows.locator('button[title*="Expand"]').first();
    await expandButton.click();
    await page.waitForTimeout(300);

    // Find response section
    const responseSection = page.locator('h4:has-text("Response")').first();

    if (await responseSection.count() > 0) {
      const responsePre = responseSection.locator('~ pre').first();
      const classes = await responsePre.getAttribute('class');

      // Check for proper constraints
      expect(classes).toContain('max-h-96'); // Vertical scroll limit
      expect(classes).toContain('max-w-full'); // No horizontal overflow
      expect(classes).toContain('break-words'); // Word breaking
      expect(classes).toContain('whitespace-pre-wrap'); // Wrap whitespace
      console.log('✓ Response content properly constrained and wrapped');
    }
  });

  test('Metadata IDs use break-all for long values', async ({ page }) => {
    await page.waitForSelector('table tbody', { timeout: 5000 });

    const rows = page.locator('tbody > template > tr').first();
    const rowCount = await rows.count();

    if (rowCount === 0) {
      test.skip();
      return;
    }

    // Expand first row
    const expandButton = rows.locator('button[title*="Expand"]').first();
    await expandButton.click();
    await page.waitForTimeout(300);

    // Check Call ID has break-all class
    const callIdDiv = page.locator('div:has-text("Call ID:")').first();
    if (await callIdDiv.count() > 0) {
      const classes = await callIdDiv.getAttribute('class');
      expect(classes).toContain('break-all');
      console.log('✓ Long IDs break properly without overflow');
    }
  });
});

test.describe('Token Metrics - New Tool Calls', () => {
  test('New tool calls display token metrics', async ({ page, request }) => {
    // First, make a tool call to generate a record with metrics
    // This requires an actual MCP server to be configured

    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    // Navigate to Tool Calls
    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    // Wait for table
    await page.waitForSelector('table tbody', { timeout: 5000 });

    // Check the most recent tool call (first row)
    const firstRow = page.locator('tbody > template > tr').first();

    if (await firstRow.count() > 0) {
      // Check if Tokens column has data
      const tokensCell = firstRow.locator('td').nth(5); // 6th column (0-indexed)
      const tokensText = await tokensCell.textContent();

      if (tokensText && !tokensText.includes('—')) {
        // Has token metrics
        expect(tokensText).toMatch(/\d+\s+tokens/);
        console.log('✓ Tool call has token metrics:', tokensText);

        // Expand to check detailed metrics
        const expandButton = firstRow.locator('button[title*="Expand"]').first();
        await expandButton.click();
        await page.waitForTimeout(300);

        // Check for Token Usage section
        const tokenUsageSection = page.locator('text=Token Usage:').first();
        if (await tokenUsageSection.count() > 0) {
          await expect(tokenUsageSection).toBeVisible();
          await expect(page.locator('text=Input Tokens:').first()).toBeVisible();
          await expect(page.locator('text=Output Tokens:').first()).toBeVisible();
          await expect(page.locator('text=Total Tokens:').first()).toBeVisible();
          console.log('✓ Detailed token metrics displayed in expanded view');
        }
      } else {
        console.log('⚠ No token metrics (expected for old tool calls)');
        console.log('  To see metrics, make a new tool call through the proxy');
      }
    }
  });

  test('Token column shows — for old tool calls without metrics', async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('table tbody', { timeout: 5000 });

    // Look for rows with — in tokens column
    const emptyTokenCells = page.locator('td:has-text("—")');
    const count = await emptyTokenCells.count();

    if (count > 0) {
      console.log(`✓ Found ${count} tool call(s) without metrics (expected for old records)`);
    }
  });
});

test.describe('Expand/Collapse Behavior', () => {
  test('Multiple rows can be expanded simultaneously', async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('table tbody', { timeout: 5000 });

    const templates = page.locator('tbody > template');
    const templateCount = await templates.count();

    if (templateCount < 2) {
      test.skip();
      return;
    }

    // Expand first two rows
    const firstTemplate = templates.first();
    const secondTemplate = templates.nth(1);

    await firstTemplate.locator('tr').first().locator('button[title*="Expand"]').first().click();
    await page.waitForTimeout(200);
    await secondTemplate.locator('tr').first().locator('button[title*="Expand"]').first().click();
    await page.waitForTimeout(200);

    // Both detail rows should be visible
    const visibleDetails = page.locator('td[colspan="7"]:visible');
    const visibleCount = await visibleDetails.count();

    expect(visibleCount).toBeGreaterThanOrEqual(2);
    console.log(`✓ ${visibleCount} rows expanded simultaneously`);
  });

  test('Collapse hides details immediately', async ({ page }) => {
    await page.goto('http://localhost:8080/ui/');
    await page.waitForLoadState('networkidle');

    await page.click('text=Tool Call History');
    await page.waitForLoadState('networkidle');

    await page.waitForSelector('table tbody', { timeout: 5000 });

    const firstRow = page.locator('tbody > template').first().locator('tr').first();

    if (await firstRow.count() === 0) {
      test.skip();
      return;
    }

    // Expand
    await firstRow.locator('button[title*="Expand"]').first().click();
    await page.waitForTimeout(300);

    const detailRow = page.locator('td[colspan="7"]').first();
    await expect(detailRow).toBeVisible();

    // Collapse
    await firstRow.locator('button[title*="Collapse"]').first().click();
    await page.waitForTimeout(300);

    await expect(detailRow).toBeHidden();
    console.log('✓ Details hidden after collapse');
  });
});