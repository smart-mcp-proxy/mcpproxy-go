// ManagedCoreStopTests.swift
// MCPProxyTests
//
// Spec 092 FR-012 — the synchronous pre-install stop. Everything here runs
// against a fake process table: the real ladder is five seconds long and lives
// inside a Sparkle callback on the main thread, which is exactly why it is
// worth pinning without spawning anything.

import XCTest
@testable import MCPProxy

final class ManagedCoreStopTests: XCTestCase {

    /// A process that dies after `diesAfterSignals` deliveries of any signal.
    private final class FakeProcess {
        var alive = true
        var received: [Int32] = []
        var diesOnSIGTERMAfter: Int   // poll iterations before it exits
        var polls = 0

        init(diesOnSIGTERMAfter: Int = 0) { self.diesOnSIGTERMAfter = diesOnSIGTERMAfter }
    }

    private func stop(
        pid: Int32?,
        process: FakeProcess,
        isCore: Bool = true,
        gracePeriod: TimeInterval = ManagedCoreStop.defaultGracePeriod
    ) -> ManagedCoreStopOutcome {
        ManagedCoreStop.stop(
            pid: pid,
            gracePeriod: gracePeriod,
            isCore: { _ in isCore },
            isRunning: { _ in
                if process.received.contains(SIGTERM) {
                    process.polls += 1
                    if process.polls > process.diesOnSIGTERMAfter { process.alive = false }
                }
                return process.alive
            },
            send: { _, sig in
                process.received.append(sig)
                if sig == SIGKILL { process.alive = false }
            },
            wait: { _ in }   // no real sleeping in tests
        )
    }

    func testNoPidIsNotAnError() {
        let proc = FakeProcess()
        XCTAssertEqual(stop(pid: nil, process: proc), .notRunning)
        XCTAssertTrue(proc.received.isEmpty)
    }

    func testPidZeroAndOneAreRefusedOutright() {
        for pid in Int32(0)...1 {
            let proc = FakeProcess()
            XCTAssertEqual(stop(pid: pid, process: proc), .notRunning,
                           "pid \(pid) must never be signalled")
            XCTAssertTrue(proc.received.isEmpty)
        }
    }

    func testAlreadyDeadProcessIsLeftAlone() {
        let proc = FakeProcess()
        proc.alive = false
        XCTAssertEqual(stop(pid: 4242, process: proc), .notRunning)
        XCTAssertTrue(proc.received.isEmpty)
    }

    func testAPidThatIsNoLongerAnMCPProxyIsNotSignalled() {
        let proc = FakeProcess()
        XCTAssertEqual(stop(pid: 4242, process: proc, isCore: false), .refused,
                       "pids are recycled; a stale one may belong to anything by now")
        XCTAssertTrue(proc.received.isEmpty, "nothing may be signalled after a refusal")
    }

    func testSIGTERMIsEnoughForAWellBehavedCore() {
        let proc = FakeProcess(diesOnSIGTERMAfter: 0)
        XCTAssertEqual(stop(pid: 4242, process: proc), .terminated)
        XCTAssertEqual(proc.received, [SIGTERM])
    }

    func testASlowButCooperativeCoreStillOnlyGetsSIGTERM() {
        let proc = FakeProcess(diesOnSIGTERMAfter: 10)
        XCTAssertEqual(stop(pid: 4242, process: proc), .terminated)
        XCTAssertEqual(proc.received, [SIGTERM])
    }

    func testAStuckCoreIsKilledRatherThanSurvivingTheBundleSwap() {
        // diesOnSIGTERMAfter larger than the number of polls in the grace
        // period: it never exits on its own.
        let proc = FakeProcess(diesOnSIGTERMAfter: 1_000_000)
        XCTAssertEqual(stop(pid: 4242, process: proc), .killed,
                       "a core running from a deleted inode is the #957 failure itself")
        XCTAssertEqual(proc.received, [SIGTERM, SIGKILL])
        XCTAssertFalse(proc.alive)
    }

    func testTheGracePeriodIsBoundedAndFinite() {
        // A zero grace period must still terminate the loop (and escalate),
        // which is what guarantees the main thread is never held indefinitely.
        let proc = FakeProcess(diesOnSIGTERMAfter: 1_000_000)
        XCTAssertEqual(stop(pid: 4242, process: proc, gracePeriod: 0), .killed)
    }
}
