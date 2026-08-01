import XCTest
@testable import MCPProxy

/// Who gets to say why the app stopped (GH #862).
///
/// `applicationWillTerminate` is told that the app is going and never why: a
/// quit from the tray menu, a `SIGTERM` from a script and a logout all arrive
/// there identically. So the reason is claimed by whoever starts the stop, and
/// these tests pin the two rules that makes workable — first claim wins, and an
/// unclaimed shutdown says so rather than inventing something plausible.
final class AppLifecycleTests: XCTestCase {

    private var directory: URL!
    private var journal: LifecycleJournal!

    override func setUpWithError() throws {
        directory = URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
            .appendingPathComponent("applifecycle-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        journal = LifecycleJournal(url: directory.appendingPathComponent("tray-lifecycle.jsonl"))
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
    }

    /// The suite drives the real `CoreProcessManager` and the real app
    /// delegate, both of which record lifecycle events through
    /// `AppLifecycle.shared`. If that wrote to the instance root, running the
    /// tests would append cores that never existed to the journal of the
    /// developer's own live install.
    func testTheSharedJournalNeverWritesToTheRealInstanceRootUnderTests() {
        let path = AppLifecycle.defaultJournalURL.path
        XCTAssertFalse(path.hasPrefix(InstancePaths.root.path),
                       "under XCTest the journal must live in a scratch file, got \(path)")
        AppLifecycle.shared.recordUpdateCheck("smoke")
        XCTAssertFalse(
            FileManager.default.fileExists(
                atPath: InstancePaths.root.appendingPathComponent("tray-lifecycle.jsonl").path
            ),
            "…and writing through the shared instance must not create one there either"
        )
    }

    func testAClaimedReasonIsWhatTheShutdownRecords() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.note("user chose Quit in the tray menu")
        lifecycle.recordTermination()

        XCTAssertEqual(journal.events().last?.kind, .appTerminating)
        XCTAssertEqual(journal.events().last?.reason, "user chose Quit in the tray menu")
    }

    /// The outermost initiator is the honest answer. Quitting tears the core
    /// down, and the teardown must not overwrite "the user asked" with its own
    /// mechanical description of what it is doing.
    func testTheFirstClaimWins() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.note("user chose Quit in the tray menu")
        lifecycle.note("core shutdown")
        lifecycle.recordTermination()
        XCTAssertEqual(journal.events().last?.reason, "user chose Quit in the tray menu")
    }

    /// A shutdown nobody claimed is recorded as exactly that. Guessing would be
    /// worse than silence: the next incident would read a confident reason that
    /// nobody gave.
    func testAnUnclaimedShutdownIsRecordedAsUnattributed() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.recordTermination()
        XCTAssertEqual(journal.events().last?.reason,
                       "unattributed (no initiator claimed this shutdown)")
    }

    /// A signal is both an event in its own right and the reason for the
    /// termination that follows it.
    func testASignalIsRecordedAndBecomesTheShutdownReason() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.recordSignal("SIGTERM")
        lifecycle.recordTermination()

        let kinds = journal.events().map(\.kind)
        XCTAssertEqual(kinds, [.signalReceived, .appTerminating])
        XCTAssertEqual(journal.events().last?.reason, "signal SIGTERM")
    }

    /// Every record carries the uptime, because a run that died at 25 hours and
    /// one that died at 25 seconds are different incidents.
    func testEveryRecordCarriesUptimeAndPid() {
        let lifecycle = AppLifecycle(journal: journal,
                                     startedAt: Date().addingTimeInterval(-3600))
        lifecycle.recordTermination()

        guard let record = journal.events().last else { return XCTFail("nothing recorded") }
        XCTAssertEqual(record.pid, ProcessInfo.processInfo.processIdentifier)
        guard let uptime = record.uptimeSeconds else { return XCTFail("no uptime recorded") }
        XCTAssertEqual(uptime, 3600, accuracy: 30)
    }

    // MARK: - Launch

    /// The launch record is what the NEXT launch judges, so it must be written
    /// after the previous ending is read — otherwise every run looks clean.
    func testLaunchReportsAnUncleanPreviousRunAndThenRecordsItself() {
        journal.append(LifecycleEvent(kind: .appLaunched, reason: "launched by user",
                                      at: Date().addingTimeInterval(-90_000),
                                      uptimeSeconds: 0, pid: 1))
        journal.append(LifecycleEvent(kind: .coreLaunched, reason: "autostart",
                                      at: Date().addingTimeInterval(-89_000),
                                      uptimeSeconds: 1000, pid: 2))

        let marker = AppLifecycle(journal: journal).recordLaunch(source: "test")
        XCTAssertNotNil(marker, "the previous run recorded no shutdown reason")
        XCTAssertEqual(journal.events().last?.kind, .appLaunched)
        XCTAssertTrue(journal.events().last?.reason.contains("PREVIOUS RUN") == true,
                      "the marker travels with the launch record, not just to the console")
    }

    func testACleanPreviousRunIsNotFlagged() {
        journal.append(LifecycleEvent(kind: .appLaunched, reason: "launched by user",
                                      at: Date(), uptimeSeconds: 0, pid: 1))
        journal.append(LifecycleEvent(kind: .appTerminating, reason: "user quit",
                                      at: Date(), uptimeSeconds: 10, pid: 1))

        XCTAssertNil(AppLifecycle(journal: journal).recordLaunch(source: "test"))
        XCTAssertEqual(journal.events().last?.reason, "launched by test")
    }

    /// Core transitions are attributable too: "who started this core" and "who
    /// killed it" are the questions a dropped MCP session raises.
    func testCoreTransitionsCarryTheirReason() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.recordCoreLaunched(pid: 4242, reason: "tray autostart")
        lifecycle.recordCoreTerminated(pid: 4242, reason: "retry requested")
        lifecycle.recordCoreExited(pid: 4242, status: 9, reason: "core exited unexpectedly")

        let events = journal.events()
        XCTAssertEqual(events.map(\.kind), [.coreLaunched, .coreTerminated, .coreExited])
        XCTAssertEqual(events[0].pid, 4242)
        XCTAssertEqual(events[1].reason, "retry requested")
        XCTAssertTrue(events[2].reason.contains("exit status 9"),
                      "the status is the difference between a crash and a clean stop")
    }
}
