# Web UI Testing Guide

This document provides guidance for testing the MCPProxy Web UI functionality.

## Overview

The Web UI provides a browser-based interface for managing MCPProxy servers and tools. It communicates with the backend via REST API endpoints and receives real-time updates through Server-Sent Events (SSE).

## Manual Testing Procedure

### Prerequisites

1. **Build and start MCPProxy:**
   ```bash
   go build -o mcpproxy ./cmd/mcpproxy
   ./mcpproxy serve --config=./test/e2e-config.json --tray=false
   ```

2. **Wait for everything server to connect:**
   - Check logs for "Everything server is connected!"
   - Or verify via: `curl http://localhost:8081/api/v1/servers`

3. **Open Web UI:**
   - Navigate to: `http://localhost:8081/ui/`

### Core UI Testing Scenarios

#### 1. Server List Display
- **Expected:** List of configured servers (should show "everything" server)
- **Verify:**
  - Server name, protocol, and status are displayed
  - Connection status shows "Ready" for everything server
  - Enabled/disabled state is correct

#### 2. Real-time Updates (SSE)
- **Test:** Enable/disable a server via API while UI is open
  ```bash
  curl -X POST http://localhost:8081/api/v1/servers/everything/disable
  curl -X POST http://localhost:8081/api/v1/servers/everything/enable
  ```
- **Expected:** UI updates automatically without page refresh
- **Verify:** Status changes reflect immediately in the UI

#### 3. Tool Search Functionality
- **Test:** Search for tools using the search interface
- **Try searches:** "echo", "tool", "random", etc.
- **Expected:** Results appear with tool names, descriptions, and server info
- **Verify:** Search is responsive and results are relevant

#### 4. Server Management
- **Test:** Enable/disable servers through the UI
- **Expected:**
  - Controls are responsive
  - Status updates immediately
  - No page refreshes required

#### 5. Tool Details
- **Test:** View tool details and descriptions
- **Expected:**
  - Tool information is complete and formatted correctly
  - Server attribution is clear

#### 6. Logs Viewing (if implemented)
- **Test:** View server logs through the UI
- **Expected:**
  - Logs are readable and properly formatted
  - Recent logs appear first
  - Auto-refresh if implemented

#### 7. Error Handling
- **Test:** Trigger error conditions
  - Stop mcpproxy server while UI is open
  - Request invalid server operations
- **Expected:**
  - Graceful error handling
  - User-friendly error messages
  - Proper reconnection when server returns

## Browser Compatibility Testing

Test the UI in multiple browsers:
- **Chrome/Chromium** (primary target)
- **Firefox**
- **Safari** (macOS)
- **Edge** (Windows)

### Key areas to verify:
- SSE event handling
- JSON parsing
- CSS styling consistency
- JavaScript functionality

## Performance Testing

### Load Testing
1. **Multiple browser tabs:** Open UI in several tabs, verify all receive updates
2. **Rapid API calls:** Make quick successive API calls, verify UI stays responsive
3. **Long-running session:** Keep UI open for extended periods, verify memory usage

### Network Testing
1. **Slow connections:** Test with throttled network
2. **Connection interruption:** Disconnect and reconnect network
3. **Server restarts:** Restart mcpproxy, verify UI reconnects properly

## Accessibility Testing

### Basic Accessibility
- **Keyboard navigation:** Ensure all interactive elements are keyboard accessible
- **Screen reader compatibility:** Test with screen reader software
- **Color contrast:** Verify sufficient contrast for visually impaired users
- **Alt text:** Check that images have appropriate alt text

## Optional: Playwright MCP Server Testing

**Note:** This is for ad-hoc testing and debugging only, not part of the automated test suite.

If you have access to a Playwright MCP server, you can use it for more advanced UI automation:

### Setup Playwright MCP Server
```json
{
  "mcpServers": {
    "playwright": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-playwright"]
    }
  }
}
```

### Example Playwright-based Tests
```javascript
// Example usage through MCP
await playwright.navigate("http://localhost:8081/ui/");
await playwright.waitForSelector("[data-testid='server-list']");
await playwright.click("[data-testid='enable-everything-server']");
```

## Common Issues and Debugging

### UI Not Loading
1. **Check mcpproxy is running:** `curl http://localhost:8081/api/v1/servers`
2. **Verify UI is embedded:** Look for UI files in the binary
3. **Check browser console:** Look for JavaScript errors
4. **Network tab:** Verify API calls are succeeding

### SSE Not Working
1. **Browser support:** Ensure browser supports Server-Sent Events
2. **Network blocking:** Check if corporate firewalls block SSE
3. **Console errors:** Look for SSE connection errors in browser console

### API Errors
1. **CORS issues:** Check Access-Control headers in network tab
2. **Authentication:** Verify no unexpected auth requirements
3. **Server logs:** Check mcpproxy logs for API errors

## Automated UI Testing (Future)

While the core test suite doesn't include automated UI tests, here's how they could be implemented:

### Potential Tools
- **Playwright:** For full browser automation
- **Selenium:** Cross-browser testing
- **Cypress:** Modern e2e testing framework

### Test Structure
```
test/ui/
├── playwright.config.js
├── tests/
│   ├── server-management.spec.js
│   ├── tool-search.spec.js
│   ├── real-time-updates.spec.js
│   └── error-handling.spec.js
└── fixtures/
    └── test-data.json
```

### Example Test Case
```javascript
test('server enable/disable functionality', async ({ page }) => {
  await page.goto('http://localhost:8081/ui/');

  // Wait for server list to load
  await page.waitForSelector('[data-testid="everything-server"]');

  // Disable server
  await page.click('[data-testid="disable-everything-server"]');
  await expect(page.locator('[data-testid="everything-status"]')).toContainText('Disabled');

  // Re-enable server
  await page.click('[data-testid="enable-everything-server"]');
  await expect(page.locator('[data-testid="everything-status"]')).toContainText('Ready');
});
```

## Test Reporting

Document test results using this template:

### Test Session Report
- **Date:** YYYY-MM-DD
- **MCPProxy Version:** vX.Y.Z
- **Browser:** Chrome vXX.X
- **Everything Server:** Connected/Failed
- **Test Duration:** XX minutes

### Results
| Test Scenario | Status | Notes |
|---------------|--------|-------|
| Server List Display | ✅ Pass | All servers visible |
| Real-time Updates | ✅ Pass | SSE working correctly |
| Tool Search | ❌ Fail | Search timeout after 30s |
| Server Management | ✅ Pass | Enable/disable working |

### Issues Found
1. **Search timeout:** Tool search occasionally times out
   - **Steps to reproduce:** Search for "nonexistent_tool"
   - **Expected:** Empty results
   - **Actual:** 30-second timeout
   - **Severity:** Medium

This manual testing approach ensures the Web UI works correctly with the everything server and provides a good user experience.