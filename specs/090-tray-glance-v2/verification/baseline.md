# T001 — Pre-change baseline on branch `090-tray-glance-v2`

Recorded before any spec-090 code change, so a later red test can be attributed
to this feature rather than to something that was already broken.

## Swift tray — `cd native/macos/MCPProxy && swift test`

- Date: 2026-07-31
- Result: **GREEN**

```text
Test Suite 'MCPProxyPackageTests.xctest' passed
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.353 (34.375) seconds
Test Suite 'All tests' passed
	 Executed 502 tests, with 0 failures (0 unexpected) in 34.353 (34.381) seconds
✔ Test run with 0 tests in 0 suites passed (swift-testing side, no swift-testing tests exist yet)
```

**Pre-existing Swift failures: none.** Any Swift failure from here on is ours.
