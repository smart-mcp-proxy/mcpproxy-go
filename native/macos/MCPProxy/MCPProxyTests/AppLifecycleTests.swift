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
        // Signal dispositions are process-wide. Anything a test installed has
        // to come back off, or the rest of the suite runs under it.
        for number in [SIGTERM, SIGINT, SIGHUP] { signal(number, SIG_DFL) }
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

    /// The ORDER production actually writes in. `applicationWillTerminate`
    /// records the app's ending first — so a reason exists whatever happens
    /// next — and only then tears the core down, which appends a
    /// `coreTerminated` record after it. That run said goodbye; a classifier
    /// that only looks at the very last record calls every logout, every
    /// launchd stop and every caught SIGTERM a SIGKILL-class crash at the next
    /// launch, which is a confidently false claim in the one diagnostic #862
    /// asked for.
    func testACleanShutdownIsStillCleanWhenACoreRecordFollowsIt() {
        journal.append(LifecycleEvent(kind: .appLaunched, reason: "launched by user",
                                      at: Date().addingTimeInterval(-3600),
                                      uptimeSeconds: 0, pid: 1))
        journal.append(LifecycleEvent(kind: .appTerminating,
                                      reason: "macOS is logging out, restarting or shutting down",
                                      at: Date().addingTimeInterval(-10),
                                      uptimeSeconds: 3590, pid: 1))
        journal.append(LifecycleEvent(kind: .coreTerminated, reason: "tray is terminating",
                                      at: Date().addingTimeInterval(-9),
                                      uptimeSeconds: 3591, pid: 2))

        XCTAssertNil(AppLifecycle(journal: journal).recordLaunch(source: "test"),
                     "the run recorded why it was going; the core teardown that "
                     + "followed does not un-record it")
    }

    /// …and the previous run's goodbye is not borrowed by the run after it. A
    /// clean quit, then a relaunch that was killed, is still an unclean run.
    func testAnEarlierRunsGoodbyeDoesNotCleanTheRunAfterIt() {
        journal.append(LifecycleEvent(kind: .appLaunched, reason: "launched by user",
                                      at: Date().addingTimeInterval(-7200),
                                      uptimeSeconds: 0, pid: 1))
        journal.append(LifecycleEvent(kind: .appTerminating, reason: "user quit",
                                      at: Date().addingTimeInterval(-7000),
                                      uptimeSeconds: 200, pid: 1))
        journal.append(LifecycleEvent(kind: .appLaunched, reason: "launched by launchd",
                                      at: Date().addingTimeInterval(-6000),
                                      uptimeSeconds: 0, pid: 2))
        journal.append(LifecycleEvent(kind: .coreLaunched, reason: "tray launched core",
                                      at: Date().addingTimeInterval(-5900),
                                      uptimeSeconds: 100, pid: 3))

        XCTAssertNotNil(AppLifecycle(journal: journal).recordLaunch(source: "test"),
                        "the second run never recorded an ending of its own")
    }

    // MARK: - Signals

    /// The tray must not leave TERM/INT/HUP set to `SIG_IGN`.
    ///
    /// Two separate costs, both paid by processes that had nothing to do with
    /// the diagnostic. An ignored disposition SURVIVES `execve`, and Go's
    /// runtime deliberately preserves an inherited `SIG_IGN` for SIGHUP and
    /// SIGINT (`runtime.initsig`) — so every core this tray spawns would go on
    /// ignoring `kill -INT` and `kill -HUP` for its whole life. And in this
    /// process, an ignored signal whose only handler is a dispatch source is a
    /// signal that does nothing at all if that source never runs.
    ///
    /// A no-op `sigaction` handler suppresses the default action just as well,
    /// is what `EVFILT_SIGNAL` needs (kqueue records the signal whatever the
    /// disposition), and is RESET to the default across `execve` — so children
    /// inherit nothing.
    func testCaughtSignalsAreNotLeftIgnoredForChildrenToInherit() {
        let lifecycle = AppLifecycle(journal: journal)
        lifecycle.installSignalHandlers(onTerminate: {})
        defer { lifecycle.uninstallSignalHandlers() }

        for number in [SIGTERM, SIGINT, SIGHUP] {
            XCTAssertNotEqual(Self.handlerBits(number), Self.ignoreBits,
                              "signal \(number) is left ignored; a spawned core inherits that")
            XCTAssertNotEqual(Self.handlerBits(number), Self.defaultBits,
                              "signal \(number) still has its default action; the app would "
                              + "die before the source ever ran")
        }
    }

    /// Delivery must not depend on the main thread. The handler used to run on
    /// `.main` only, so a wedged main thread turned `pkill -TERM`, `launchctl
    /// kill TERM` and a launchd stop into no-ops — the signal was ignored and
    /// nothing was left to act on it, where before this diagnostic existed they
    /// simply killed the process.
    ///
    /// The main thread is the one blocking on the semaphore here, which is
    /// exactly the wedge being described.
    func testASignalIsActedOnEvenWhileTheMainThreadIsBlocked() throws {
        let lifecycle = AppLifecycle(journal: journal)
        let handled = DispatchSemaphore(value: 0)
        lifecycle.installSignalHandlers(onTerminate: { handled.signal() },
                                        escalateAfter: 60,
                                        escalate: { _ in })
        defer { lifecycle.uninstallSignalHandlers() }

        kill(getpid(), SIGHUP)
        XCTAssertEqual(handled.wait(timeout: .now() + 3), .success,
                       "the signal never reached a handler that could act on it")
    }

    /// …and if the graceful stop does not happen, the signal still ends the
    /// process. Ignoring a termination request and then failing to terminate is
    /// strictly worse than never having caught it: `SIGKILL` becomes the only
    /// thing that works, which is precisely the unattributable death this whole
    /// file exists to prevent.
    func testASignalThatDoesNotStopTheAppFallsBackToItsDefaultAction() {
        let lifecycle = AppLifecycle(journal: journal)
        let escalated = expectation(description: "the default action is restored and re-raised")
        var escalatedSignal: Int32 = 0

        lifecycle.respondToSignal("SIGTERM", number: SIGTERM,
                                  onTerminate: { /* wedged: nothing stops the app */ },
                                  escalateAfter: 0.05,
                                  escalate: { number in
                                      escalatedSignal = number
                                      escalated.fulfill()
                                  })

        wait(for: [escalated], timeout: 3)
        XCTAssertEqual(escalatedSignal, SIGTERM)
        XCTAssertEqual(journal.events().first?.kind, .signalReceived,
                       "the signal is on the record before anything is done about it")
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

    // MARK: - Signal-disposition helpers

    /// The installed handler as a bit pattern, so it can be compared with the
    /// two sentinels. `SIG_IGN` and `SIG_DFL` are function-pointer sentinels
    /// (1 and 0), not real functions, so there is nothing else to compare.
    private static func handlerBits(_ number: Int32) -> UInt {
        var action = sigaction()
        guard sigaction(number, nil, &action) == 0 else { return .max }
        return unsafeBitCast(action.__sigaction_u.__sa_handler, to: UInt.self)
    }

    private static var ignoreBits: UInt { unsafeBitCast(SIG_IGN, to: UInt.self) }
    private static var defaultBits: UInt { unsafeBitCast(SIG_DFL, to: UInt.self) }
}
