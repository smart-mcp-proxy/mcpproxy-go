// CoreSupersede.swift
// MCPProxy
//
// Spec 092 Phase 0, FR-001 / FR-001a / FR-002 / FR-005 / FR-006 — issue #957
// ("old version App still after upgrade").
//
// After an upgrade the new tray frequently finds an OLD core already running:
// the previous tray spawned it, the previous tray is gone, and nothing in the
// system ever stops it. The tray attaches to it and serves the old version
// indefinitely, silently. This file is the decision that ends that.
//
// The decision is a pure function on purpose. Everything it governs is
// destructive — stopping a process the user may be depending on — so the rules
// have to be readable in one place and testable without spawning anything.
// `CoreProcessManager` supplies the inputs and executes the verdict; it makes
// no policy of its own.

import Foundation
import Darwin

// MARK: - Inputs

/// What the core said about itself in `GET /api/v1/info`.
struct CoreVersionReport: Equatable {
    /// `info.version` — the version the running core reports.
    let runningVersion: String

    /// `info.launched_by` — DURABLE provenance (FR-001a): "tray"/"installer"
    /// when a tray process started this core, "" when the user did or it is
    /// unknown. Durable is the whole point: in-memory ownership dies with the
    /// tray that spawned the core, so every pre-existing core would otherwise
    /// classify as external — defeating FR-001 in exactly the tray-upgrade
    /// scenario it targets.
    let launchedBy: String

    /// `info.pid` — nil when the core is too old to report one. The only stop
    /// mechanism available for a core the tray merely attached to.
    let pid: Int32?
}

// MARK: - Verdict

/// What to do about the core we are connected to.
enum CoreSupersedeAction: Equatable {
    /// Nothing. Also the answer for every ambiguity — see the `reason`.
    case none

    /// The tray spawned this core in THIS session, so it holds a Process
    /// handle: stop it through the normal managed path and relaunch.
    case restartManaged

    /// A tray started this core (possibly a tray generation that no longer
    /// exists) and it is older than the bundled core. FR-001 authorizes
    /// stopping it without asking; the pid is the mechanism.
    case stopAndRespawn(pid: Int32)

    /// User- or externally-launched, or tray-launched with no usable pid.
    /// FR-002: surface an action, never act. `pid == nil` means the action can
    /// only present instructions.
    case askForConsent(pid: Int32?)
}

/// The verdict plus why — the reason is logged for every outcome, including
/// (especially) the silent ones, because "the tray did nothing" is otherwise
/// indistinguishable from "the tray never looked".
struct CoreSupersedeDecision: Equatable {
    let action: CoreSupersedeAction
    let reason: String

    static func none(_ reason: String) -> CoreSupersedeDecision {
        CoreSupersedeDecision(action: .none, reason: reason)
    }
}

/// The consent prompt the menu renders (FR-002). Nil when there is nothing to
/// offer, which is the steady state.
struct StaleCorePrompt: Equatable {
    let runningVersion: String
    let bundledVersion: String
    /// nil → the menu item explains how to stop the core by hand instead of
    /// offering to do it.
    let pid: Int32?

    var menuTitle: String {
        "Old core v\(runningVersion) running — Restart into v\(bundledVersion)"
    }
}

// MARK: - The decision

enum CoreSupersede {

    /// Provenance values that mean "a tray process started this core".
    ///
    /// `installer` is in here deliberately. When the macOS PKG postinstall
    /// launches the app it stamps `MCPPROXY_LAUNCHED_BY=installer`, and
    /// `CoreProcessManager.launchCore` explicitly does NOT overwrite that
    /// marker for the core it spawns (first-run telemetry attribution
    /// outranks "tray"). So an `installer` core is a TRAY-SPAWNED core wearing
    /// a different label, and excluding it would leave the PKG upgrade path —
    /// one of the two paths #957 reports — asking for consent it should not
    /// need.
    static let trayProvenance: Set<String> = ["tray", "installer"]

    /// Decide what to do about the running core.
    ///
    /// - Parameters:
    ///   - report: what the core said about itself.
    ///   - respawnVersion: the version the tray would get if it restarted the
    ///     core RIGHT NOW — i.e. the bundled core's version — or nil when the
    ///     tray has no bundled core to offer (dev builds, a
    ///     `MCPPROXY_CORE_PATH` override, a Homebrew core on `PATH`).
    ///     Restarting into the same binary cannot improve anything, and a
    ///     supersede that respawns the identical old version is a loop.
    ///   - ownership: whether the tray holds a Process handle for this core.
    ///   - alreadyAttempted: whether this manager already acted once. One
    ///     attempt per connection episode: if the respawned core still reports
    ///     an old version something is wrong that another kill will not fix
    ///     (FR-005 — no restart loops).
    static func decide(
        report: CoreVersionReport,
        respawnVersion: String?,
        ownership: CoreOwnership,
        alreadyAttempted: Bool
    ) -> CoreSupersedeDecision {
        guard let respawnVersion, !respawnVersion.isEmpty else {
            return .none("no bundled core to supersede into")
        }
        guard !report.runningVersion.isEmpty else {
            return .none("the running core did not report a version")
        }

        // FR-006: malformed on either side is "no decision", never "equal".
        guard let order = SemanticVersion.compare(report.runningVersion, respawnVersion) else {
            return .none(
                "cannot compare running \(report.runningVersion) with bundled \(respawnVersion)"
            )
        }

        if order == 0 {
            // FR-005: the overwhelmingly common case. Silent, no churn.
            return .none("running core v\(report.runningVersion) matches the bundled core")
        }
        if order > 0 {
            // FR-005: a downgrade is never automatic. A user running a newer
            // core than the tray ships (a locally built one, a newer Homebrew
            // core) is doing it on purpose.
            return .none(
                "running core v\(report.runningVersion) is newer than the bundled "
                + "v\(respawnVersion) — not downgrading"
            )
        }

        if alreadyAttempted {
            return .none("already superseded once in this session — not retrying")
        }

        // FR-001: a core this tray spawned this session. The managed path is
        // strictly better than a pid kill — it has the Process handle, the
        // termination generation, and the reaper.
        if ownership == .trayManaged {
            return CoreSupersedeDecision(
                action: .restartManaged,
                reason: "tray-managed core v\(report.runningVersion) is older than the "
                    + "bundled v\(respawnVersion)"
            )
        }

        // FR-001a: durable provenance. This is the #957 case — an old tray's
        // core outliving the tray that made it.
        if trayProvenance.contains(report.launchedBy) {
            guard let pid = report.pid, pid > 1 else {
                return CoreSupersedeDecision(
                    action: .askForConsent(pid: nil),
                    reason: "core v\(report.runningVersion) reports tray provenance but no "
                        + "usable pid — asking instead of guessing"
                )
            }
            return CoreSupersedeDecision(
                action: .stopAndRespawn(pid: pid),
                reason: "core v\(report.runningVersion) (launched_by=\(report.launchedBy)) is "
                    + "older than the bundled v\(respawnVersion)"
            )
        }

        // FR-002: user-launched. Never touched without an explicit click.
        let usablePID = (report.pid.map { $0 > 1 } ?? false) ? report.pid : nil
        return CoreSupersedeDecision(
            action: .askForConsent(pid: usablePID),
            reason: "core v\(report.runningVersion) was not launched by a tray — consent required"
        )
    }

    /// The prompt to publish for a verdict, or nil when there is nothing to
    /// show. Kept next to `decide` so the menu can never disagree with it.
    static func prompt(
        for decision: CoreSupersedeDecision,
        report: CoreVersionReport,
        respawnVersion: String?
    ) -> StaleCorePrompt? {
        guard case .askForConsent(let pid) = decision.action, let respawnVersion else {
            return nil
        }
        return StaleCorePrompt(
            runningVersion: report.runningVersion,
            bundledVersion: respawnVersion,
            pid: pid
        )
    }
}

// MARK: - Killing by pid, safely

/// Answers "is the process behind this pid actually an mcpproxy core?".
///
/// The pid arrives over a trusted channel (the core's own Unix socket), but
/// acting on it means sending a signal to an arbitrary process id, and pids
/// are recycled. A core that died between the `/api/v1/info` response and the
/// signal could have had its pid reused by something else entirely — so the
/// identity is re-checked immediately before the signal, not once at read
/// time.
enum CoreProcessIdentity {

    /// Absolute path of the executable behind `pid`, or nil when the process
    /// is gone or not inspectable (another user's process, in particular).
    static func executablePath(ofPID pid: Int32) -> String? {
        var buffer = [CChar](repeating: 0, count: Int(MAXPATHLEN))
        let length = proc_pidpath(pid, &buffer, UInt32(buffer.count))
        guard length > 0 else { return nil }
        return String(cString: buffer)
    }

    /// Whether it is safe to signal this pid as "the mcpproxy core".
    ///
    /// A name check, not a proof of identity — but it is the difference
    /// between a bounded mistake (signalling a *different* mcpproxy) and an
    /// unbounded one (signalling an unrelated process that inherited the pid).
    static func isMCPProxyCore(pid: Int32) -> Bool {
        guard pid > 1 else { return false }
        guard let path = executablePath(ofPID: pid) else { return false }
        let name = (path as NSString).lastPathComponent
        return name == "mcpproxy" || name.hasPrefix("mcpproxy-")
    }

    /// Whether the process still exists (and we may signal it).
    static func isRunning(pid: Int32) -> Bool {
        guard pid > 1 else { return false }
        if kill(pid, 0) == 0 { return true }
        // EPERM means it exists and belongs to someone else — which is exactly
        // the "a different user owns it" edge case the spec calls out.
        return errno == EPERM
    }
}
