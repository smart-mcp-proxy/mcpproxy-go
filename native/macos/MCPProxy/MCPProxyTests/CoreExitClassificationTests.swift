// CoreExitClassificationTests.swift
// MCPProxyTests
//
// A benign core shutdown must never surface as an "MCPProxy Error". Two things
// used to go wrong on a normal quit (issue: shutdown error notification):
//
//   1. The app-quit path (`applicationWillTerminate`) terminates the core
//      directly, without first moving to `.shuttingDown`. The core exits
//      cleanly (status 0), but the exit handler only treated status 0 as
//      expected WHEN the state was `.shuttingDown`, so a normal quit was
//      classified as a crash.
//   2. The "error" body was the last captured core stderr line — a benign
//      INFO log line, shown with raw ANSI colour escape codes still in it.
//
// These are pure decisions, pinned here without spawning a process.

import XCTest
@testable import MCPProxy

final class CoreExitClassificationTests: XCTestCase {

    // The real INFO line that reached a user as an "MCPProxy Error" body,
    // ANSI colour codes and all (zap's coloured console encoder).
    private let ansiInfoLine =
        "2026-08-14 07:36:05 \u{1B}[34mINFO\u{1B}[0m managed/client.go:695   "
        + "Successfully retrieved tools via direct call to upstream server   "
        + "{\"upstream_id\": \"jira-abc\"}"

    // MARK: - isExpectedExit (facet 1: don't notify on a clean quit)

    func testCleanExitIsExpectedEvenWhenNotShuttingDown() {
        // The app-termination path never sets `.shuttingDown`, yet a status-0
        // exit there is a normal quit, not a crash.
        XCTAssertTrue(
            CoreError.isExpectedExit(status: 0, userStopped: false, shuttingDown: false)
        )
    }

    func testUserStoppedExitIsExpected() {
        XCTAssertTrue(
            CoreError.isExpectedExit(status: 137, userStopped: true, shuttingDown: false)
        )
    }

    func testShuttingDownExitIsExpected() {
        XCTAssertTrue(
            CoreError.isExpectedExit(status: 143, userStopped: false, shuttingDown: true)
        )
    }

    func testUnexpectedNonZeroCrashIsNotExpected() {
        XCTAssertFalse(
            CoreError.isExpectedExit(status: 1, userStopped: false, shuttingDown: false)
        )
    }

    // MARK: - ANSI stripping

    func testStripANSIRemovesColourCodes() {
        let stripped = CoreError.stripANSICodes(ansiInfoLine)
        XCTAssertFalse(stripped.contains("\u{1B}["), "ANSI escapes must be gone")
        XCTAssertTrue(stripped.contains("INFO"))
        XCTAssertTrue(stripped.contains("Successfully retrieved tools"))
    }

    // MARK: - diagnostic(fromStderr:) (facet 2: never show a benign log line)

    func testBenignInfoLineYieldsNoDiagnostic() {
        XCTAssertNil(CoreError.diagnostic(fromStderr: ansiInfoLine))
    }

    func testOnlyDebugWarnInfoLinesYieldNoDiagnostic() {
        let buffer = """
        2026-08-14 07:36:04 \u{1B}[35mDEBUG\u{1B}[0m runtime/lifecycle.go:120 reindexing
        2026-08-14 07:36:05 \u{1B}[34mINFO\u{1B}[0m managed/client.go:695 Successfully retrieved tools
        2026-08-14 07:36:05 \u{1B}[33mWARN\u{1B}[0m server/mcp.go:42 slow upstream
        """
        XCTAssertNil(CoreError.diagnostic(fromStderr: buffer))
    }

    func testGenuineErrorLineIsKeptAndStripped() {
        let buffer = """
        2026-08-14 07:36:05 \u{1B}[34mINFO\u{1B}[0m managed/client.go:695 Successfully retrieved tools
        2026-08-14 07:36:06 \u{1B}[31mERROR\u{1B}[0m server/mcp.go:88 failed to bind listener
        """
        let diag = CoreError.diagnostic(fromStderr: buffer)
        XCTAssertNotNil(diag)
        XCTAssertFalse(diag!.contains("\u{1B}["), "ANSI escapes must be stripped")
        XCTAssertTrue(diag!.contains("failed to bind listener"))
        XCTAssertFalse(diag!.contains("Successfully retrieved tools"),
                       "benign INFO lines must be dropped")
    }

    func testPlainNonLogMessageIsKept() {
        // A bare stderr message with no recognised log level is a real error.
        XCTAssertEqual(CoreError.diagnostic(fromStderr: "unexpected failure"), "unexpected failure")
    }

    func testGoPanicIsKept() {
        let buffer = "panic: runtime error: invalid memory address\n\tgoroutine 1 [running]:"
        let diag = CoreError.diagnostic(fromStderr: buffer)
        XCTAssertNotNil(diag)
        XCTAssertTrue(diag!.contains("panic: runtime error"))
    }

    // MARK: - fromExitCode wiring

    func testFromExitCodeDoesNotSurfaceBenignInfoLine() {
        // The exact reported failure: a non-zero exit whose only captured
        // stderr is a benign INFO line must fall back to a generic message,
        // never parrot the INFO line.
        let error = CoreError.fromExitCode(1, stderr: ansiInfoLine)
        XCTAssertEqual(error, .general("Exit code 1"))
    }

    func testFromExitCodeStillSurfacesRealError() {
        let error = CoreError.fromExitCode(1, stderr: "unexpected failure")
        XCTAssertEqual(error, .general("unexpected failure"))
    }
}
