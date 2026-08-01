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

    /// The signals this app catches, with the names the journal records.
    static let caughtSignals: [(number: Int32, name: String)] = [
        (SIGTERM, "SIGTERM"), (SIGINT, "SIGINT"), (SIGHUP, "SIGHUP")
    ]

    /// Where a caught signal is reasoned about. Deliberately NOT `.main`.
    ///
    /// Catching a signal suppresses its default action, so a handler that can
    /// only run on the main thread is a promise the app cannot keep: with the
    /// main thread wedged (a deadlock, a hung synchronous call) `pkill -TERM`,
    /// `launchctl kill TERM` and a launchd stop would all become no-ops, where
    /// before any of this existed they simply killed the process. Diagnostics
    /// must not make the thing they diagnose harder to stop.
    private static let signalQueue = DispatchQueue(
        label: "com.smartmcpproxy.mcpproxy.signals"
    )

    /// How long the polite path gets before the signal does what it came to do.
    /// The same order as the core teardown the tray-menu Quit performs.
    static let signalEscalationDelay: TimeInterval = 5.0

    /// Catch the signals that CAN be caught, so a `pkill` or a launchd stop is
    /// attributable instead of silent.
    ///
    /// SIGKILL and a jetsam kill are deliberately absent — they cannot be
    /// caught, which is exactly why the unclean-exit marker at the next launch
    /// exists.
    func installSignalHandlers(
        onTerminate: @escaping @Sendable () -> Void,
        escalateAfter: TimeInterval = AppLifecycle.signalEscalationDelay,
        escalate: @escaping @Sendable (Int32) -> Void = AppLifecycle.restoreDefaultActionAndReraise
    ) {
        for (number, name) in Self.caughtSignals {
            Self.suppressDefaultAction(number)
            let source = DispatchSource.makeSignalSource(signal: number, queue: Self.signalQueue)
            source.setEventHandler { [weak self] in
                self?.respondToSignal(name, number: number, onTerminate: onTerminate,
                                      escalateAfter: escalateAfter, escalate: escalate)
            }
            source.resume()
            lock.lock()
            signalSources.append(source)
            lock.unlock()
        }
    }

    /// Undo `installSignalHandlers`. Only tests need it — the app installs once
    /// and lives with it — but a test that left TERM/INT/HUP rewired would take
    /// the rest of the suite with it.
    func uninstallSignalHandlers() {
        lock.lock()
        let sources = signalSources
        signalSources = []
        lock.unlock()
        for source in sources { source.cancel() }
        for (number, _) in Self.caughtSignals { signal(number, SIG_DFL) }
    }

    /// What a caught signal does: record it, ask for a graceful stop, and stop
    /// asking nicely if the app is still here afterwards.
    ///
    /// Separated from the source plumbing so the policy is reachable from a
    /// test — a real signal delivered to a test process is not.
    func respondToSignal(
        _ name: String,
        number: Int32,
        onTerminate: @escaping @Sendable () -> Void,
        escalateAfter: TimeInterval,
        escalate: @escaping @Sendable (Int32) -> Void
    ) {
        recordSignal(name)
        // Called here, on the signal queue. Whatever needs the main thread —
        // `NSApplication.terminate` does — hops there itself, so that a main
        // thread which never answers delays the shutdown instead of cancelling
        // it.
        onTerminate()
        // The fallback only ever runs in a process that is still alive: a
        // successful termination takes this timer with it.
        Self.signalQueue.asyncAfter(deadline: .now() + escalateAfter) {
            NSLog("[MCPProxy] %@ did not stop the app within %.0fs — "
                  + "restoring its default action", name, escalateAfter)
            escalate(number)
        }
    }

    /// Stop the signal from killing us by default, WITHOUT ignoring it.
    ///
    /// `SIG_IGN` would have been shorter and is wrong twice over. An ignored
    /// disposition survives `execve`, and Go's runtime deliberately preserves an
    /// inherited `SIG_IGN` for SIGHUP and SIGINT (`runtime.initsig`) — so every
    /// core this tray spawns would spend its life unable to be stopped by
    /// `kill -INT` or `kill -HUP`. A real (empty) handler is reset to the
    /// default across `execve`, so children inherit nothing. `EVFILT_SIGNAL`,
    /// which is what `DispatchSourceSignal` is built on, records the signal
    /// whatever the disposition — it only needs the default action gone.
    private static func suppressDefaultAction(_ number: Int32) {
        var action = sigaction()
        action.__sigaction_u.__sa_handler = { _ in }
        sigemptyset(&action.sa_mask)
        action.sa_flags = Int32(SA_RESTART)
        sigaction(number, &action, nil)
    }

    /// Put the signal back the way the system found it and send it again.
    ///
    /// This is the production `escalate`. It is what makes catching a
    /// termination signal safe: the worst case is now a five-second delay, not
    /// a process that only `SIGKILL` can stop.
    static func restoreDefaultActionAndReraise(_ number: Int32) {
        signal(number, SIG_DFL)
        kill(getpid(), number)
    }

    private func event(_ kind: LifecycleEvent.Kind, _ reason: String) -> LifecycleEvent {
        LifecycleEvent(kind: kind, reason: reason, at: Date(), uptimeSeconds: uptime,
                       pid: ProcessInfo.processInfo.processIdentifier)
    }
}
