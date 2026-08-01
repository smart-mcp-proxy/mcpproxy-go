// AppLifecycle.swift
// MCPProxy
//
// The tray's shutdown-reason bookkeeping (GH #862).
//
// `LifecycleJournal` is the storage; this is the policy on top of it — what
// counts as a reason, who is allowed to claim one, and what happens when nobody
// does. The rule the issue asks for is one line long: *every* ending names who
// asked for it, and an ending nobody claimed says so explicitly rather than
// looking like an ordinary quit.
//
// Reasons are CLAIMED IN ADVANCE, by whoever initiates the stop (`note(_:)`),
// because by the time `applicationWillTerminate` runs the initiator is long out
// of the call stack: AppKit tells us the app is going, never why. A quit chosen
// from the tray menu, a `SIGTERM` from a script, and a logout all arrive at the
// same delegate method, and telling them apart afterwards is impossible.
//
// Deliberately NOT an actor: the signal source's handler and
// `applicationWillTerminate` are both synchronous contexts with no opportunity
// to await — the process may be gone microseconds later. The state is a string
// and a date behind a lock instead.

import AppKit
import Foundation

/// Records why the tray and its core start and stop.
final class AppLifecycle: @unchecked Sendable {

    /// The instance production uses. Tests build their own with an injected
    /// journal; this one writes to the instance root — except under XCTest, see
    /// `defaultJournalURL`.
    static let shared = AppLifecycle(journal: LifecycleJournal(url: defaultJournalURL))

    /// `<instance root>/tray-lifecycle.jsonl`, or a scratch file when running
    /// under XCTest.
    ///
    /// The test suite drives the REAL `CoreProcessManager` and the real app
    /// delegate, and both record lifecycle events — so without this the suite
    /// appends to the developer's own `~/.mcpproxy/tray-lifecycle.jsonl` and
    /// pollutes the journal of a live install with events from cores that were
    /// never theirs. A diagnostic that corrupts the thing it diagnoses is worse
    /// than no diagnostic.
    static var defaultJournalURL: URL {
        let name = "tray-lifecycle.jsonl"
        guard ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] == nil,
              NSClassFromString("XCTestCase") == nil else {
            return URL(fileURLWithPath: NSTemporaryDirectory(), isDirectory: true)
                .appendingPathComponent("mcpproxy-tests-\(name)")
        }
        return InstancePaths.root.appendingPathComponent(name)
    }

    private let journal: LifecycleJournal
    private let startedAt: Date
    private let lock = NSLock()
    private var claimedReason: String?
    private var signalSources: [DispatchSourceSignal] = []

    init(journal: LifecycleJournal = LifecycleJournal(), startedAt: Date = Date()) {
        self.journal = journal
        self.startedAt = startedAt
    }

    /// Seconds since this tray process came up. Part of every record: a run
    /// that died at 25 hours and one that died at 25 seconds are different
    /// incidents with the same log line otherwise.
    var uptime: TimeInterval { Date().timeIntervalSince(startedAt) }

    // MARK: - Launch

    /// Record this launch, and report on the previous one.
    ///
    /// Order matters: the previous ending is read BEFORE this run's record is
    /// appended, or the launch we are recording becomes the ending we are
    /// judging.
    ///
    /// Returns the unclean-exit marker when the previous run recorded no
    /// shutdown reason, so the caller can surface it; nil otherwise.
    @discardableResult
    func recordLaunch(source: String = AppLifecycle.launchSource()) -> String? {
        let previous = journal.previousRunEnding()
        let marker = LifecycleJournal.uncleanExitSummary(for: previous)
        if let marker {
            journal.append(event(.appLaunched, "launched by \(source); PREVIOUS RUN: \(marker)"))
        } else {
            journal.append(event(.appLaunched, "launched by \(source)"))
        }
        journal.trim()
        return marker
    }

    /// How this app was started, as far as it can tell. The DMG installer and
    /// the core-spawn path already set `MCPPROXY_LAUNCHED_BY`; a login item is
    /// re-parented to launchd (ppid 1), and anything else is a user opening it.
    static func launchSource() -> String {
        if let declared = ProcessInfo.processInfo.environment["MCPPROXY_LAUNCHED_BY"],
           !declared.isEmpty {
            return declared
        }
        return getppid() == 1 ? "launchd (login item or relaunch)" : "user"
    }

    // MARK: - Shutdown

    /// Claim the reason for the shutdown that is about to happen.
    ///
    /// First claim wins: the outermost initiator is the honest answer. A user
    /// choosing Quit causes the app to tear its core down, and the core's
    /// teardown must not overwrite "user quit" with "shutdown".
    func note(_ reason: String) {
        lock.lock()
        defer { lock.unlock() }
        if claimedReason == nil { claimedReason = reason }
    }

    /// Record that the app is going, with whatever reason was claimed.
    ///
    /// An unclaimed termination is recorded as unattributed rather than as
    /// something plausible. Guessing here would be worse than silence: the next
    /// incident would read a confident reason that nobody actually gave.
    func recordTermination() {
        lock.lock()
        let reason = claimedReason
        lock.unlock()
        journal.append(event(.appTerminating,
                             reason ?? "unattributed (no initiator claimed this shutdown)"))
    }

    /// A catchable signal arrived. Recorded as its own event AND claimed as the
    /// shutdown reason, since the termination that follows is its consequence.
    func recordSignal(_ name: String) {
        journal.append(event(.signalReceived, name))
        note("signal \(name)")
    }

    // MARK: - Core process

    func recordCoreLaunched(pid: Int32, reason: String) {
        journal.append(LifecycleEvent(kind: .coreLaunched, reason: reason, at: Date(),
                                      uptimeSeconds: uptime, pid: pid))
    }

    func recordCoreTerminated(pid: Int32, reason: String) {
        journal.append(LifecycleEvent(kind: .coreTerminated, reason: reason, at: Date(),
                                      uptimeSeconds: uptime, pid: pid))
    }

    func recordCoreExited(pid: Int32, status: Int32, reason: String) {
        journal.append(LifecycleEvent(kind: .coreExited,
                                      reason: "\(reason) (exit status \(status))",
                                      at: Date(), uptimeSeconds: uptime, pid: pid))
    }

    /// One line per periodic update check (issue #862, ask 3). The original
    /// investigation could neither confirm nor exclude the updater because it
    /// logged nothing at all about its hourly work.
    func recordUpdateCheck(_ detail: String) {
        journal.append(event(.updateCheck, detail))
    }

    // MARK: - Signals

    /// Catch the signals that CAN be caught, so a `pkill` or a launchd stop is
    /// attributable instead of silent.
    ///
    /// SIGKILL and a jetsam kill are deliberately absent — they cannot be
    /// caught, which is exactly why the unclean-exit marker at the next launch
    /// exists. Ignoring the default disposition first is required: without it
    /// the process dies before the dispatch source ever runs.
    func installSignalHandlers(onTerminate: @escaping @Sendable () -> Void) {
        for (number, name) in [(SIGTERM, "SIGTERM"), (SIGINT, "SIGINT"), (SIGHUP, "SIGHUP")] {
            signal(number, SIG_IGN)
            let source = DispatchSource.makeSignalSource(signal: number, queue: .main)
            source.setEventHandler { [weak self] in
                self?.recordSignal(name)
                onTerminate()
            }
            source.resume()
            signalSources.append(source)
        }
    }

    private func event(_ kind: LifecycleEvent.Kind, _ reason: String) -> LifecycleEvent {
        LifecycleEvent(kind: kind, reason: reason, at: Date(), uptimeSeconds: uptime,
                       pid: ProcessInfo.processInfo.processIdentifier)
    }
}
