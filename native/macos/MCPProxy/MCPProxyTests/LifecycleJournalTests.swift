import XCTest
@testable import MCPProxy

/// The tray's persistent record of who started and stopped what (GH #862).
///
/// The issue it answers: an app that exits leaves NOTHING behind saying why, so
/// a silent SIGKILL-class death (jetsam, a `pkill`, a crash outside our
/// handlers) is indistinguishable from a clean quit once the process is gone —
/// and macOS purges the Info tier of the unified log long before anyone looks.
/// The journal is the only artefact that outlives the process, so its two
/// promises are pinned here: a shutdown always records a reason, and a run that
/// ended without one is detectable at the NEXT launch.
final class LifecycleJournalTests: XCTestCase {

    private var directory: URL!
    private var journalURL: URL!

    override func setUpWithError() throws {
        directory = URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
            .appendingPathComponent("lifecycle-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        journalURL = directory.appendingPathComponent("tray-lifecycle.jsonl")
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: directory)
    }

    // MARK: - Round trip

    func testEventsAreAppendedAndReadBackInOrder() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.coreLaunched, "autostart"))
        journal.append(Self.event(.appTerminating, "user quit"))

        let events = journal.events()
        XCTAssertEqual(events.map(\.kind), [.appLaunched, .coreLaunched, .appTerminating])
        XCTAssertEqual(events.map(\.reason), ["launch", "autostart", "user quit"])
    }

    /// One record per line, so a truncated tail (a kill mid-write) costs the
    /// last line and not the file.
    func testAGarbledLineIsSkippedRatherThanLosingTheJournal() throws {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        let handle = try FileHandle(forWritingTo: journalURL)
        handle.seekToEndOfFile()
        handle.write(Data("{\"kind\":\"appTermin".utf8))
        try handle.close()

        XCTAssertEqual(journal.events().map(\.kind), [.appLaunched],
                       "a half-written record must not take the readable ones with it")
    }

    /// A journal that grows without bound is a journal somebody deletes. It is
    /// trimmed at launch, keeping the newest records — the ones an incident is
    /// read backwards from.
    func testTheJournalIsTrimmedToItsNewestRecords() {
        let journal = LifecycleJournal(url: journalURL, maxRecords: 10)
        for index in 0..<25 {
            journal.append(Self.event(.coreLaunched, "run-\(index)"))
        }
        journal.trim()

        let events = journal.events()
        XCTAssertEqual(events.count, 10)
        XCTAssertEqual(events.first?.reason, "run-15")
        XCTAssertEqual(events.last?.reason, "run-24")
    }

    // MARK: - Was the previous run accounted for?

    func testAFirstEverLaunchHasNoPreviousRun() {
        XCTAssertEqual(LifecycleJournal(url: journalURL).previousRunEnding(), .noPreviousRun)
    }

    func testARunThatRecordedItsShutdownIsClean() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.appTerminating, "user quit from the tray menu"))

        guard case .clean(let last) = journal.previousRunEnding() else {
            return XCTFail("a recorded shutdown is a clean ending, got \(journal.previousRunEnding())")
        }
        XCTAssertEqual(last.reason, "user quit from the tray menu")
    }

    /// The case the whole issue is about: the process vanished between two
    /// ordinary records. The next launch must be able to say so, and to name
    /// the last thing that happened before the silence.
    func testARunThatJustStoppedIsReportedUnclean() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.coreLaunched, "autostart", uptime: 90))

        guard case .unclean(let last) = journal.previousRunEnding() else {
            return XCTFail("a run with no shutdown record ended unclean, "
                           + "got \(journal.previousRunEnding())")
        }
        XCTAssertEqual(last?.kind, .coreLaunched)
        XCTAssertEqual(last?.reason, "autostart",
                       "the last thing recorded is the only lead an incident has")
    }

    /// A signal we caught and logged is an accounted-for ending too — the point
    /// is attribution, not tidiness. `signalReceived` on its own is not it:
    /// the terminating record follows, and only that says the app is going.
    func testASignalFollowedByATerminationRecordIsClean() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.signalReceived, "SIGTERM"))
        journal.append(Self.event(.appTerminating, "signal SIGTERM"))

        guard case .clean = journal.previousRunEnding() else {
            return XCTFail("a signal that was caught, logged and acted on is attributable")
        }
    }

    /// A signal record with nothing after it is the opposite: something asked
    /// the app to go and the app never finished going.
    func testASignalWithNoTerminationRecordIsUnclean() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.signalReceived, "SIGKILL is not catchable; SIGTERM is"))

        guard case .unclean(let last) = journal.previousRunEnding() else {
            return XCTFail("a run that logged a signal and then stopped is still unaccounted for")
        }
        XCTAssertEqual(last?.kind, .signalReceived)
    }

    // MARK: - What the next launch says about it

    /// The unclean marker is a log line a human reads a year later, so it
    /// carries the three facts an incident needs: how long the run lasted, when
    /// it stopped, and what it was doing.
    func testTheUncleanMarkerNamesUptimeAndTheLastEvent() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.coreLaunched, "autostart", uptime: 90_061))

        let summary = LifecycleJournal.uncleanExitSummary(for: journal.previousRunEnding())
        guard let summary else {
            return XCTFail("an unclean previous run must produce a marker")
        }
        XCTAssertTrue(summary.contains("coreLaunched"), summary)
        XCTAssertTrue(summary.contains("autostart"), summary)
        XCTAssertTrue(summary.contains("25h"), "uptime is stated in a readable form: \(summary)")
    }

    func testACleanPreviousRunProducesNoMarker() {
        let journal = LifecycleJournal(url: journalURL)
        journal.append(Self.event(.appLaunched, "launch"))
        journal.append(Self.event(.appTerminating, "user quit"))
        XCTAssertNil(LifecycleJournal.uncleanExitSummary(for: journal.previousRunEnding()))
        XCTAssertNil(LifecycleJournal.uncleanExitSummary(for: .noPreviousRun))
    }

    // MARK: - Writing where it cannot write

    /// Best-effort, like the autostart sidecar: diagnostics must never be the
    /// reason the app fails to start.
    func testAnUnwritableJournalIsSurvived() {
        let journal = LifecycleJournal(
            url: URL(fileURLWithPath: "/dev/null/nope/tray-lifecycle.jsonl")
        )
        journal.append(Self.event(.appLaunched, "launch"))
        XCTAssertEqual(journal.events(), [])
        XCTAssertEqual(journal.previousRunEnding(), .noPreviousRun)
    }

    // MARK: - Fixtures

    private static func event(
        _ kind: LifecycleEvent.Kind,
        _ reason: String,
        uptime: TimeInterval? = nil
    ) -> LifecycleEvent {
        LifecycleEvent(kind: kind, reason: reason, at: Date(), uptimeSeconds: uptime, pid: 42)
    }
}
