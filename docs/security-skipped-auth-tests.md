# CRITICAL SECURITY VULNERABILITY: Skipped API Key Tests

## Issue Summary
Security-critical tests that verify TCP connections require API key authentication are being SKIPPED, allowing bugs to slip through.

## Evidence

### Test Output
```
=== RUN   TestE2E_TrayToCore_UnixSocket/TCP_NoAPIKey_Fail
    socket_e2e_test.go:141: TCP port resolution failed - skipping TCP test
=== RUN   TestE2E_TrayToCore_UnixSocket/TCP_WithAPIKey_Success
    socket_e2e_test.go:161: TCP port resolution failed - skipping TCP test
--- PASS: TestE2E_TrayToCore_UnixSocket (0.73s)
    --- SKIP: TestE2E_TrayToCore_UnixSocket/TCP_NoAPIKey_Fail (0.00s)
    --- SKIP: TestE2E_TrayToCore_UnixSocket/TCP_WithAPIKey_Success (0.00s)
```

### Root Cause
File: `internal/server/socket_e2e_test.go:100`
```go
skipTCPTests := (tcpAddr == "" || tcpAddr == "127.0.0.1:0" || tcpAddr == ":0")
```

The server returns `"127.0.0.1:0"` even after starting, causing the critical security tests to be skipped.

### Impact
1. **Old binaries had security bug**: TCP connections were incorrectly tagged as "tray" and bypassed API key auth
2. **Tests didn't catch it**: Security tests were passing because they were skipped
3. **Production risk**: This could have allowed unauthorized API access

## Historical Evidence

User logs from OLD binary show TCP connections bypassing auth:
```
2025-11-28T19:58:50.450+02:00 | DEBUG | http/server.go:2322 | Tray connection - skipping API key validation{path 15 0 /api/v1/servers <nil>} {remote_addr 15 0  <nil>} {source 15 0 tray <nil>}
```

This should have been caught by `TCP_NoAPIKey_Fail` test, but it was skipped.

## Required Fixes

### 1. Fix Test - Make it Fail Rather Than Skip

File: `internal/server/socket_e2e_test.go:100`

```go
// BEFORE (current - WRONG):
skipTCPTests := (tcpAddr == "" || tcpAddr == "127.0.0.1:0" || tcpAddr == ":0")
if skipTCPTests {
    t.Skip("TCP port resolution failed - skipping TCP test")
}

// AFTER (proposed - CORRECT):
require.NotEmpty(t, tcpAddr, "TCP address must be resolved")
require.NotEqual(t, "127.0.0.1:0", tcpAddr, "TCP must bind to actual port, not :0")
require.NotEqual(t, ":0", tcpAddr, "TCP must bind to actual port, not :0")
```

**Why**: Security tests should FAIL, not skip, when they can't run. Skipping gives false confidence.

### 2. Fix Server Address Resolution

File: `internal/server/server.go` (GetListenAddress method)

Ensure `srv.GetListenAddress()` returns the actual bound address after the server starts, not the configuration value.

Current behavior:
```go
tcpAddr := srv.GetListenAddress()  // Returns "127.0.0.1:0" (from config)
```

Expected behavior:
```go
tcpAddr := srv.GetListenAddress()  // Should return "127.0.0.1:52345" (actual port)
```

### 3. Add CI Enforcement

File: `.github/workflows/e2e-tests.yml`

Add check to fail CI if security tests are skipped:

```yaml
- name: Check for skipped security tests
  run: |
    go test -v ./internal/server -run TestE2E_TrayToCore_UnixSocket 2>&1 | tee test.log
    if grep -q "SKIP.*TCP_NoAPIKey_Fail" test.log; then
      echo "::error::CRITICAL: API key security test was skipped!"
      exit 1
    fi
    if grep -q "SKIP.*TCP_WithAPIKey_Success" test.log; then
      echo "::error::CRITICAL: API key security test was skipped!"
      exit 1
    fi
```

### 4. Additional Security Tests Needed

Create dedicated API key middleware tests that don't depend on server lifecycle:

File: `internal/httpapi/api_key_security_test.go` (NEW)

```go
package httpapi

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/stretchr/testify/assert"
    "go.uber.org/zap"
    "mcpproxy-go/internal/transport"
)

// TestAPIKeyMiddleware_TCPRequiresAuth ensures TCP connections ALWAYS require API key
// This test should NEVER skip
func TestAPIKeyMiddleware_TCPRequiresAuth(t *testing.T) {
    tests := []struct {
        name          string
        apiKey        string
        requestHeader string
        expectedCode  int
    }{
        {
            name:          "No API key - should reject",
            apiKey:        "test-key-123",
            requestHeader: "",
            expectedCode:  401,
        },
        {
            name:          "Invalid API key - should reject",
            apiKey:        "test-key-123",
            requestHeader: "wrong-key",
            expectedCode:  401,
        },
        {
            name:          "Valid API key - should accept",
            apiKey:        "test-key-123",
            requestHeader: "test-key-123",
            expectedCode:  200,
        },
        {
            name:          "Empty API key config - should allow (auth disabled)",
            apiKey:        "",
            requestHeader: "",
            expectedCode:  200,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Create mock controller that returns config with API key
            // Create middleware
            // Create test request with TCP source
            // Verify response code
        })
    }
}

// TestAPIKeyMiddleware_TrayBypassesAuth ensures tray connections bypass API key
// This test should NEVER skip
func TestAPIKeyMiddleware_TrayBypassesAuth(t *testing.T) {
    // Create middleware with API key enabled
    // Create request with Tray connection source
    // Verify it bypasses auth (returns 200, not 401)
}
```

## Verification Plan

1. ✅ Identify skipped tests (DONE)
2. ⏳ Fix `GetListenAddress()` to return actual port
3. ⏳ Remove skip logic, make test fail hard
4. ⏳ Run full test suite locally
5. ⏳ Add CI enforcement to prevent skipped security tests
6. ⏳ Add dedicated API key middleware unit tests
7. ⏳ Document security model in CLAUDE.md

## Priority: P0 - CRITICAL

**Severity**: HIGH - Authentication bypass vulnerability

**Impact**:
- API endpoints accessible without authentication
- Tray connection bypass could be exploited if socket permissions are misconfigured
- Silent test failures give false security confidence

**Action Required**:
1. Fix test to FAIL instead of SKIP
2. Add CI enforcement
3. Create GitHub security issue
4. Consider security advisory if any releases were affected

## Questions for Investigation

1. **When was this bug introduced?**
   - Check git history for when skip logic was added
   - Determine which releases are affected

2. **Has this been exploited?**
   - Review access logs for unauthorized API access
   - Check if any production instances had this vulnerability

3. **What about Windows named pipes?**
   - Does the Windows equivalent have the same skip logic?
   - Are Windows security tests also being skipped?
