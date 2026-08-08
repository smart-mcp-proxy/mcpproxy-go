// ManagedCoreStop.swift
// MCPProxy
//
// Spec 092 FR-012 — "the updater MUST stop the tray-managed core gracefully
// before the app bundle is replaced".
//
// Sparkle's pre-install hooks are SYNCHRONOUS main-thread callbacks: whatever
// stopping happens must be finished by the time the delegate method returns,
// because the installer runs the moment it does. That rules out the tray's
// normal shutdown path — `CoreProcessManager.shutdown()` is an actor method
// that hops to the main actor several times, so blocking the main thread on it
// deadlocks instead of stopping anything.
//
// So the pre-install stop is done here, with nothing but POSIX: signal, poll,
// escalate. No actor, no run loop, no allocation of a new async context. The
// identity re-check from Phase 0 (`CoreProcessIdentity`) still gates the
// signal — a pid that died between reading it and using it can belong to
// anything by now.

import Foundation
import Darwin

/// What happened to the core.
enum ManagedCoreStopOutcome: Equatable {
    /// No pid to act on, or the process was already gone.
    case notRunning
    /// SIGTERM was enough.
    case terminated
    /// SIGTERM was ignored for the whole grace period; SIGKILL was sent AND the
    /// process was then confirmed gone.
    case killed
    /// The pid does not (any longer) belong to an mcpproxy process. Nothing was
    /// signalled — see the type header.
    case refused
    /// The process was still there after SIGKILL and the confirmation wait. The
    /// caller MUST NOT let the bundle be replaced under it.
    case failed

    /// Whether the core is CONFIRMED down — the only basis on which the bundle
    /// may be replaced.
    ///
    /// `refused` is not confirmation. It means the pid is alive but could not
    /// be identified as an mcpproxy process, and `CoreProcessIdentity` cannot
    /// tell "the pid was recycled by something unrelated" apart from "the
    /// process belongs to a user whose executable path we may not read". A stop
    /// that ends in a question mark is a stop that failed.
    var coreIsDown: Bool {
        switch self {
        case .notRunning, .terminated, .killed: return true
        case .refused, .failed: return false
        }
    }
}

enum ManagedCoreStop {

    /// How long the core gets to exit on its own before SIGKILL.
    ///
    /// Bounded on purpose (FR-012 edge case: "in-flight tool calls fail visibly
    /// rather than hanging forever"). Five seconds matches the tray's existing
    /// SIGTERM→SIGKILL ladder in `stopCore`, and the whole budget is spent on
    /// the main thread inside a Sparkle callback, so it cannot grow.
    static let defaultGracePeriod: TimeInterval = 5.0

    /// How long to wait for the process to actually disappear after SIGKILL.
    ///
    /// SIGKILL is not instantaneous — the kernel still has to tear the process
    /// down, and a core blocked in an uninterruptible syscall can outlive the
    /// signal. Returning "killed" the microsecond after sending it is a claim
    /// nobody checked, and the caller uses that claim to decide whether the app
    /// bundle may be replaced.
    static let defaultKillGracePeriod: TimeInterval = 2.0

    /// Poll interval while waiting for the process to disappear.
    static let pollInterval: TimeInterval = 0.05

    /// Stop `pid` synchronously.
    ///
    /// Every dependency is injected so the ladder can be tested without a real
    /// process: the tests drive a fake clock and a fake process table.
    @discardableResult
    static func stop(
        pid: Int32?,
        gracePeriod: TimeInterval = defaultGracePeriod,
        killGracePeriod: TimeInterval = defaultKillGracePeriod,
        isCore: (Int32) -> Bool = CoreProcessIdentity.isMCPProxyCore,
        isRunning: (Int32) -> Bool = CoreProcessIdentity.isRunning,
        send: (Int32, Int32) -> Void = { pid, sig in _ = kill(pid, sig) },
        wait: (TimeInterval) -> Void = { Thread.sleep(forTimeInterval: $0) }
    ) -> ManagedCoreStopOutcome {
        guard let pid, pid > 1 else { return .notRunning }
        guard isRunning(pid) else { return .notRunning }

        // Phase 0's rule, unchanged: never signal a pid we cannot still
        // identify as an mcpproxy process.
        guard isCore(pid) else { return .refused }

        send(pid, SIGTERM)

        if waitForExit(pid: pid, budget: gracePeriod, isRunning: isRunning, wait: wait) {
            return .terminated
        }

        // Still there. The bundle is about to be replaced underneath it; a core
        // running from a deleted inode is the #957 failure mode this whole spec
        // exists to end, so it does not get to survive the swap.
        //
        // The identity is re-checked HERE and not only at the top: the grace
        // period is seconds long, and a core that exited during it can have had
        // its pid handed to something else. SIGTERM to a stranger is rude;
        // SIGKILL to a stranger is unrecoverable.
        guard isCore(pid) else { return .refused }

        send(pid, SIGKILL)
        return waitForExit(pid: pid, budget: killGracePeriod, isRunning: isRunning, wait: wait)
            ? .killed
            : .failed
    }

    /// Poll until `pid` is gone or the budget runs out. Returns whether it went.
    private static func waitForExit(
        pid: Int32,
        budget: TimeInterval,
        isRunning: (Int32) -> Bool,
        wait: (TimeInterval) -> Void
    ) -> Bool {
        var waited: TimeInterval = 0
        while waited < budget {
            if !isRunning(pid) { return true }
            wait(pollInterval)
            waited += pollInterval
        }
        return !isRunning(pid)
    }
}
