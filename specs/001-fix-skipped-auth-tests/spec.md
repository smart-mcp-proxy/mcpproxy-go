# Feature Specification: Fix Skipped API Key Authentication Tests

**Feature Branch**: `001-fix-skipped-auth-tests`
**Created**: 2025-11-28
**Status**: Draft
**Priority**: P0 - CRITICAL SECURITY
**Input**: User description: "@docs/security-skipped-auth-tests.md required to do all Recommended Actions 1. Immediate: Create GitHub security issue 2. Short term: Fix test to FAIL instead of SKIP 3. Medium term: Add CI enforcement for skipped security tests Create PR for it"

## Executive Summary

**CRITICAL SECURITY VULNERABILITY**: Security-critical E2E tests that verify TCP connections require API key authentication are being SKIPPED instead of running. This allowed a production bug where TCP connections were incorrectly tagged as "tray" connections and bypassed API key authentication.

**Impact**:
- API endpoints were accessible without authentication in affected builds
- Security tests passed with SKIP status, giving false confidence
- Production risk of unauthorized API access

**Root Cause**: Test logic in `internal/server/socket_e2e_test.go:100` skips tests when `GetListenAddress()` returns the unresolved config value `"127.0.0.1:0"` instead of the actual bound port.

## User Scenarios & Testing

### User Story 1 - Security Tests Must Fail When Broken (Priority: P1)

As a developer running the test suite, when critical security tests cannot execute (e.g., TCP port resolution fails), the tests MUST FAIL loudly rather than silently skipping, so I immediately know there's a problem that needs fixing.

**Why this priority**: This is the core security issue. Skipped tests create false confidence and allow vulnerabilities to slip through. MUST be fixed before any other work.

**Independent Test**: Can be tested by running `go test ./internal/server -run TestE2E_TrayToCore_UnixSocket` and verifying that if the server doesn't properly bind to a TCP port, the test FAILS (not SKIP).

**Acceptance Scenarios**:

1. **Given** the test suite runs, **When** TCP port resolution fails, **Then** tests MUST fail with clear error message (not skip)
2. **Given** the server is running with TCP enabled, **When** tests run, **Then** both `TCP_NoAPIKey_Fail` and `TCP_WithAPIKey_Success` tests MUST execute and pass
3. **Given** a TCP connection without API key, **When** request is made to protected endpoint, **Then** server MUST return 401 Unauthorized
4. **Given** a TCP connection with valid API key, **When** request is made to protected endpoint, **Then** server MUST return 200 OK
5. **Given** a Unix socket/tray connection without API key, **When** request is made to protected endpoint, **Then** server MUST return 200 OK (tray bypass is intentional)

---

### User Story 2 - CI Must Prevent Skipped Security Tests (Priority: P2)

As a repository maintainer, when PRs are submitted, the CI pipeline MUST fail if any security-critical tests are skipped, preventing vulnerabilities from being merged.

**Why this priority**: Prevents future regressions. Once P1 fixes the immediate issue, this ensures it can never happen again.

**Independent Test**: Can be tested by temporarily modifying a test to skip, pushing to a PR branch, and verifying that CI fails with a clear error message about skipped security tests.

**Acceptance Scenarios**:

1. **Given** a PR with all security tests passing, **When** CI runs, **Then** build succeeds
2. **Given** a PR where `TCP_NoAPIKey_Fail` test is skipped, **When** CI runs, **Then** build fails with error "CRITICAL: API key security test was skipped"
3. **Given** a PR where `TCP_WithAPIKey_Success` test is skipped, **When** CI runs, **Then** build fails with error "CRITICAL: API key security test was skipped"
4. **Given** CI detects skipped security tests, **When** build fails, **Then** error message clearly identifies which test was skipped and why it's critical

---

### User Story 3 - GitHub Issue Documents Vulnerability (Priority: P1)

As a security auditor or developer reviewing the project's security history, a GitHub security issue MUST document this vulnerability, its impact, the fix, and affected versions for transparency and future reference.

**Why this priority**: Critical for security transparency, vulnerability tracking, and ensuring affected users can identify if they're impacted.

**Independent Test**: Can be tested by navigating to the GitHub Issues page and verifying that a security issue exists with comprehensive documentation of the vulnerability.

**Acceptance Scenarios**:

1. **Given** the vulnerability has been identified, **When** GitHub issue is created, **Then** it MUST include severity (HIGH), impact description, root cause, and affected code locations
2. **Given** the issue is created, **When** developers review it, **Then** it MUST clearly identify which builds/versions are affected
3. **Given** the issue exists, **When** the fix is merged, **Then** issue MUST be linked to the PR with `Related #[issue-number]`
4. **Given** the fix is verified in production, **When** testing is complete, **Then** issue can be manually closed with verification notes

---

### Edge Cases

- **What happens when server binds to port 0 (random port allocation)?** System must still resolve the actual bound port and make it available to tests via `GetListenAddress()`
- **What happens when multiple test runs happen concurrently?** Each test run must get its own ephemeral port, and port resolution must work independently
- **What happens when GetListenAddress() is called before server starts?** Test setup must ensure server is fully started before retrieving address
- **What happens when API key is empty (auth disabled)?** Tests must still verify the middleware logic works correctly (allows all TCP connections when auth is disabled)
- **What happens when a developer adds a new security test?** CI enforcement must automatically include new tests matching the naming pattern `TCP_*_*Auth*`

## Requirements

### Functional Requirements

- **FR-001**: System MUST fail security tests loudly when preconditions cannot be met (e.g., TCP port resolution fails)
- **FR-002**: `GetListenAddress()` method MUST return the actual bound address after server starts, not the config value
- **FR-003**: Test suite MUST verify TCP connections without API key return 401 Unauthorized when auth is enabled
- **FR-004**: Test suite MUST verify TCP connections with valid API key return 200 OK when auth is enabled
- **FR-005**: Test suite MUST verify Unix socket/tray connections bypass API key validation (return 200 OK without key)
- **FR-006**: CI pipeline MUST fail builds when security-critical tests (`TCP_NoAPIKey_Fail`, `TCP_WithAPIKey_Success`) are skipped
- **FR-007**: GitHub security issue MUST document the vulnerability with severity, impact, root cause, and fix details
- **FR-008**: CI enforcement MUST output clear error messages identifying which security test was skipped
- **FR-009**: Test failure messages MUST include diagnostic information (actual address received, expected address format)

### Non-Functional Requirements

- **NFR-001**: Test execution time MUST NOT increase by more than 10% due to address resolution checks
- **NFR-002**: CI enforcement check MUST complete in under 5 seconds
- **NFR-003**: Error messages MUST be actionable (tell developers what to fix)
- **NFR-004**: Documentation MUST be clear enough for security auditors unfamiliar with the codebase

### Key Entities

- **Test Security Gate**: The logic that determines whether a security test can run. Must FAIL when preconditions aren't met.
- **TCP Address Resolution**: The mechanism by which tests obtain the actual bound TCP address after server starts.
- **CI Security Check**: The GitHub Actions workflow step that verifies no security tests were skipped.
- **GitHub Security Issue**: The tracking issue documenting the vulnerability, its impact, and remediation.

## Success Criteria

### Measurable Outcomes

- **SC-001**: Running `go test ./internal/server -run TestE2E_TrayToCore_UnixSocket` MUST execute all 3 sub-tests (UnixSocket_NoAPIKey_Success, TCP_NoAPIKey_Fail, TCP_WithAPIKey_Success) without any SKIP status
- **SC-002**: If TCP port resolution fails, test MUST fail with exit code 1 and clear error message within 2 seconds
- **SC-003**: CI MUST fail within 30 seconds if any security test matching pattern `TCP_*_*Auth*` is skipped
- **SC-004**: GitHub security issue MUST be created and contain minimum 5 sections: Summary, Evidence, Root Cause, Impact, Fix
- **SC-005**: After fix is merged, re-running the old buggy code MUST cause tests to FAIL (proving they would have caught the original bug)

### Verification Steps

1. **Manual Test Execution**: Run `go test ./internal/server -run TestE2E_TrayToCore_UnixSocket -v` and verify all tests execute
2. **Negative Test**: Temporarily break `GetListenAddress()` to return `"127.0.0.1:0"` and verify test fails (not skips)
3. **CI Verification**: Create PR with skip logic intact and verify CI fails with clear error
4. **Issue Review**: Verify GitHub issue contains all required sections and is labeled appropriately
5. **Security Regression Test**: Verify the exact bug from the logs (TCP tagged as tray) would now be caught by tests

## Implementation Plan

### Phase 1: Fix Test Logic (P1)

**File**: `internal/server/socket_e2e_test.go`

**Current Code** (lines 99-103):
```go
skipTCPTests := (tcpAddr == "" || tcpAddr == "127.0.0.1:0" || tcpAddr == ":0")
if skipTCPTests {
    t.Skip("TCP port resolution failed - skipping TCP test")
}
```

**Fixed Code**:
```go
require.NotEmpty(t, tcpAddr, "TCP address must be resolved - GetListenAddress() returned empty")
require.NotEqual(t, "127.0.0.1:0", tcpAddr, "TCP must bind to actual port, not :0 - server may not have started correctly")
require.NotEqual(t, ":0", tcpAddr, "TCP must bind to actual port, not :0 - server may not have started correctly")
```

**Rationale**: Security tests should FAIL, not skip, when they can't run. This makes failures visible and forces fixes.

### Phase 2: Fix Server Address Resolution

**File**: `internal/server/server.go` (GetListenAddress method)

**Current Behavior**:
```go
tcpAddr := srv.GetListenAddress()  // Returns "127.0.0.1:0" (from config)
```

**Expected Behavior**:
```go
tcpAddr := srv.GetListenAddress()  // Should return "127.0.0.1:52345" (actual bound port)
```

**Investigation Required**:
1. Check if `srv.httpServer.Addr` contains the actual bound address after `ListenAndServe()`
2. If not, use `srv.httpServer.Listener.Addr()` to get the actual address
3. Ensure this is called AFTER server has started binding

### Phase 3: Add CI Enforcement (P2)

**File**: `.github/workflows/e2e-tests.yml`

**New Step** (add after test execution):
```yaml
- name: Check for skipped security tests
  run: |
    go test -v ./internal/server -run TestE2E_TrayToCore_UnixSocket 2>&1 | tee test_output.log

    # Check for skipped API key security tests
    if grep -q "SKIP.*TCP_NoAPIKey_Fail" test_output.log; then
      echo "::error::CRITICAL SECURITY: TCP_NoAPIKey_Fail test was skipped!"
      echo "::error::This test verifies TCP connections require API key authentication."
      echo "::error::Skipping this test could allow authentication bypass vulnerabilities."
      exit 1
    fi

    if grep -q "SKIP.*TCP_WithAPIKey_Success" test_output.log; then
      echo "::error::CRITICAL SECURITY: TCP_WithAPIKey_Success test was skipped!"
      echo "::error::This test verifies TCP connections with valid API keys are accepted."
      echo "::error::Skipping this test could allow authentication bypass vulnerabilities."
      exit 1
    fi

    echo "✅ All security tests executed successfully (no skips detected)"
```

### Phase 4: Create GitHub Security Issue (P1)

**Issue Template**:

```markdown
Title: CRITICAL: Skipped E2E Tests Allowed API Key Auth Bypass Bug

Labels: security, P0, bug

## Severity
🔴 HIGH - Authentication bypass vulnerability

## Summary
Security-critical E2E tests that verify TCP connections require API key authentication are being SKIPPED instead of running, allowing bugs to slip through that could expose API endpoints without authentication.

## Evidence

### Test Output
```
=== RUN   TestE2E_TrayToCore_UnixSocket/TCP_NoAPIKey_Fail
    socket_e2e_test.go:141: TCP port resolution failed - skipping TCP test
--- SKIP: TestE2E_TrayToCore_UnixSocket/TCP_NoAPIKey_Fail (0.00s)
```

### Root Cause
File: `internal/server/socket_e2e_test.go:100`
```go
skipTCPTests := (tcpAddr == "" || tcpAddr == "127.0.0.1:0" || tcpAddr == ":0")
```

The server returns `"127.0.0.1:0"` even after starting, causing critical security tests to skip.

## Impact
1. **Historical Bug**: Old binaries incorrectly tagged TCP connections as "tray" and bypassed API key auth
2. **Test Blindness**: Security tests passed with SKIP status, giving false confidence
3. **Production Risk**: Could have allowed unauthorized API access

## Affected Code
- `internal/server/socket_e2e_test.go:100` - Skip logic
- `internal/server/server.go` - GetListenAddress() method
- `.github/workflows/e2e-tests.yml` - Missing CI enforcement

## Fix Plan
1. ✅ Change test to FAIL instead of SKIP when preconditions aren't met
2. ✅ Fix GetListenAddress() to return actual bound port
3. ✅ Add CI enforcement to prevent skipped security tests
4. ⏳ Verify fix prevents the original bug

## Verification
- [ ] All E2E tests execute without SKIP status
- [ ] TCP without API key returns 401 Unauthorized
- [ ] TCP with valid API key returns 200 OK
- [ ] CI fails if security tests are skipped

## Related PRs
- Related #[PR-number] - Fix skipped auth tests

## Questions for Investigation
1. When was this skip logic introduced? (check git history)
2. Which releases are affected?
3. Has this been exploited? (review access logs)
```

## Commit Message Conventions

When committing changes for this feature, follow these guidelines:

### Issue References
- ✅ **Use**: `Related #[issue-number]` - Links the commit to the issue without auto-closing
- ❌ **Do NOT use**: `Fixes #[issue-number]`, `Closes #[issue-number]`, `Resolves #[issue-number]` - These auto-close issues on merge

**Rationale**: Issues should only be closed manually after verification and testing in production, not automatically on merge.

### Co-Authorship
- ❌ **Do NOT include**: `Co-Authored-By: Claude <noreply@anthropic.com>`
- ❌ **Do NOT include**: "🤖 Generated with [Claude Code](https://claude.com/claude-code)"

**Rationale**: Commit authorship should reflect the human contributors, not the AI tools used.

### Example Commit Messages

```
test: fail loudly when TCP port resolution fails

Related #[issue-number]

Security-critical tests were silently skipping when GetListenAddress()
returned unresolved port "127.0.0.1:0". This allowed authentication
bypass bugs to slip through undetected.

Changed skip logic to hard failure with clear error messages:
- require.NotEmpty for address resolution
- require.NotEqual for :0 port binding
- Diagnostic messages explain what went wrong

## Changes
- Replaced t.Skip() with require assertions in socket_e2e_test.go
- Tests now fail immediately if TCP port can't be resolved
- Error messages include diagnostic information

## Testing
- Verified all 3 sub-tests execute without SKIP
- Confirmed test fails when GetListenAddress() returns "127.0.0.1:0"
- All assertions provide actionable error messages
```

```
fix: return actual bound port in GetListenAddress()

Related #[issue-number]

GetListenAddress() was returning the config value "127.0.0.1:0"
instead of the actual bound port, causing E2E tests to skip.

## Changes
- Use httpServer.Listener.Addr() to get actual bound address
- Only call after server has started binding
- Return format: "127.0.0.1:[actual-port]"

## Testing
- Verified GetListenAddress() returns actual port after server start
- E2E tests now receive resolved addresses like "127.0.0.1:52345"
- All security tests execute successfully
```

```
ci: enforce no skipped security tests in E2E workflow

Related #[issue-number]

Added CI check to fail builds if critical security tests are skipped.
Prevents authentication bypass vulnerabilities from being merged.

## Changes
- Added "Check for skipped security tests" step to e2e-tests.yml
- Checks for SKIP status in TCP_NoAPIKey_Fail and TCP_WithAPIKey_Success
- Outputs clear error messages identifying which test was skipped
- Fails with exit code 1 if any security test is skipped

## Testing
- Verified workflow fails when test is forcibly skipped
- Confirmed error messages are clear and actionable
- Validated workflow passes when all tests execute
```

## Security Considerations

1. **Authentication Bypass Risk**: This vulnerability could allow unauthorized access to protected API endpoints
2. **Tray Connection Trust Model**: Unix socket/named pipe connections are intentionally trusted (skip API key). This is correct behavior and must remain.
3. **TCP Connection Security**: TCP connections MUST always require API key validation when auth is enabled
4. **False Confidence**: Passing tests with SKIP status are worse than failing tests - they hide problems
5. **Production Impact**: Review access logs for unauthorized API access in affected versions
6. **Disclosure**: Consider security advisory if any public releases were affected

## Future Enhancements

1. **Dedicated Middleware Tests**: Create `internal/httpapi/api_key_security_test.go` with pure middleware tests that don't depend on server lifecycle
2. **Windows Named Pipes**: Verify Windows equivalent has no skip logic and properly tests named pipe authentication bypass
3. **Monitoring**: Add metrics for authentication failures to detect exploitation attempts
4. **Security Test Registry**: Create central registry of all security-critical tests that must never skip
5. **Automated Security Audit**: Periodic scan for test files with `.Skip()` calls in security-critical paths

## Documentation Updates

- [ ] Update `CLAUDE.md` with lessons learned about test skip patterns
- [ ] Document the tray connection trust model in security section
- [ ] Add "Testing" section explaining the importance of failing over skipping
- [ ] Create `docs/testing-guidelines.md` with security test best practices

## References

- **Analysis Document**: `docs/security-skipped-auth-tests.md`
- **Test File**: `internal/server/socket_e2e_test.go:88-185`
- **Middleware**: `internal/httpapi/server.go:135-184`
- **Server Implementation**: `internal/server/server.go` (GetListenAddress method)
- **CI Workflow**: `.github/workflows/e2e-tests.yml`
