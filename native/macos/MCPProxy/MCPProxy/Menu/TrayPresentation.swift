// TrayPresentation.swift
// MCPProxy
//
// Pure presentation rules for the status-bar menu. Everything here is a value
// -> value function so the menu's *decisions* can be tested without AppKit,
// leaving `rebuildMenu()` to do nothing but hang the results on NSMenuItems.
//
// Introduced by the 2026-08 macOS tray UX audit (F1, F3, F11, F12, F15).

import Foundation

// MARK: - F1 · What the menu-bar icon says

/// The one badge the status-bar button carries, in priority order.
///
/// Before the audit the icon badged only *core stopped* and *core error*: with
/// five servers needing attention the menu bar looked exactly like all-healthy,
/// which defeats the point of a status item. `severity` restores the Spec 044
/// red/orange dot that `TrayIcon.swift` was written for and never delivered
/// (that view was never instantiated).
enum TrayIconBadge: Equatable {
    /// Everything is fine — the plain template icon, nothing beside it.
    case none
    /// The core is not running.
    case stopped
    /// The core reported an error.
    case coreError
    /// At least one enabled server has an attached warn/error diagnostic.
    case severity(TrayIconSeverity)
}

enum TrayIconSeverity: String, Equatable {
    case warn
    case error
}

enum TrayStatusIcon {
    /// Decide the badge from the three inputs the app delegate already has.
    ///
    /// Core state wins over server severity: a stopped or erroring core makes
    /// every server verdict stale, so showing an amber server dot on top of it
    /// would be a second, quieter story about the same outage.
    static func badge(isStopped: Bool, hasCoreError: Bool, worstDiagnosticSeverity: String?) -> TrayIconBadge {
        if isStopped { return .stopped }
        if hasCoreError { return .coreError }
        switch worstDiagnosticSeverity {
        case "error": return .severity(.error)
        case "warn": return .severity(.warn)
        default: return .none
        }
    }

    /// The glyph drawn beside the template icon. Empty for `.none`.
    ///
    /// Deliberately text, not a composited image: an NSStatusItem image must
    /// stay `isTemplate` to follow the light/dark menu bar, and a template
    /// image is re-rendered monochrome — a coloured dot drawn into it would
    /// come back black. The button's attributed title is the one place a
    /// colour survives, and the existing ⏹ / ⚠ states already live there.
    static func glyph(for badge: TrayIconBadge) -> String {
        switch badge {
        case .none: return ""
        case .stopped: return "⏹"
        case .coreError: return "⚠"
        case .severity: return "●"
        }
    }

    /// F2 · What VoiceOver and the tooltip say. `summary` is
    /// `AppState.statusSummary` ("13/29 servers, 942 tools").
    static func accessibilityLabel(for badge: TrayIconBadge, summary: String, attentionCount: Int) -> String {
        var parts = ["MCPProxy"]
        if !summary.isEmpty { parts.append(summary) }
        switch badge {
        case .none:
            break
        case .stopped:
            parts.append("core stopped")
        case .coreError:
            parts.append("core error")
        case .severity(let severity):
            let noun = severity == .error ? "error" : "warning"
            parts.append(attentionCount == 1
                ? "1 server \(noun)"
                : "\(attentionCount) server \(noun)s")
        }
        return parts.joined(separator: " — ")
    }
}

// MARK: - F12 · Transport names users recognise

enum TrayProtocolDisplay {
    /// `Protocol: streamable-http` beside `Protocol: http` and `Protocol: sse`
    /// exposed three wire spellings of what users experience as one transport
    /// family. Normalise for display only — the config value is untouched.
    static func label(for raw: String) -> String {
        switch raw.lowercased() {
        case "stdio": return "stdio"
        case "http": return "HTTP"
        case "streamable-http", "streamable_http", "http-stream": return "HTTP (streamable)"
        case "sse": return "HTTP (SSE)"
        case "auto", "": return "auto-detect"
        default: return raw
        }
    }
}

// MARK: - F11 · Profiles that would scope agents to nothing

enum TrayProfileDisplay {
    /// The Web UI's ProfileSwitcher shows "N servers · M tools"; the tray showed
    /// only the tool count, so a profile whose servers are not in the config
    /// ("research (0 tools)") read as merely empty rather than broken. Switching
    /// to it scopes every agent to zero servers.
    ///
    /// `knownServers` is the set of configured server names; a profile
    /// referencing none of them is called out by name.
    static func label(name: String, servers: [String], toolCount: Int, knownServers: Set<String>) -> String {
        let effective = servers.filter { knownServers.contains($0) }.count
        if effective == 0 {
            return "\(name) — no servers"
        }
        let serverWord = effective == 1 ? "server" : "servers"
        return "\(name) (\(effective) \(serverWord) · \(toolCount) tools)"
    }
}

// MARK: - F15 · A Servers submenu that fits on screen

/// How one server is filed in the Servers submenu.
enum TrayServerGroup: Equatable {
    /// Something is wrong or waiting: sign-in, connection error, quarantine.
    case needsAttention
    /// Enabled and behaving.
    case active
    /// Switched off on purpose — the long tail, folded away.
    case disabled
}

enum TrayServerGrouping {
    /// The audit found 29 flat alphabetical entries with 13 disabled and 3
    /// quarantined interleaved, distinguishable only by dot colour, in a
    /// submenu taller than the screen. Attention first, then the working
    /// servers, then the disabled ones behind one row.
    ///
    /// Generic over anything that can answer the three questions, so the
    /// ordering is testable without building `ServerStatus` fixtures.
    static func group(enabled: Bool, needsAttention: Bool) -> TrayServerGroup {
        if !enabled { return .disabled }
        return needsAttention ? .needsAttention : .active
    }

    /// Below this many disabled servers, folding them costs a click and saves
    /// nothing. At or above it the fold is the point.
    static let disabledFoldThreshold = 3
}

// MARK: - F3 · Server actions that fail out loud

/// The five per-server menu actions used to be fired as `try? await …`: a
/// restart that 500s was indistinguishable from one that worked, because the
/// menu simply closed. The Web UI raises a toast and Server Detail shows an
/// inline error, so the tray was the only surface that lied by omission.
enum TrayServerAction: String, Equatable {
    case enable
    case disable
    case restart
    case login
    case approve

    /// Present tense, for the failure title: "Couldn't restart github".
    var verb: String {
        switch self {
        case .enable: return "enable"
        case .disable: return "disable"
        case .restart: return "restart"
        case .login: return "sign in to"
        case .approve: return "approve"
        }
    }

    /// Imperative, for a menu row: the user must be told what a click does.
    var menuTitle: String {
        switch self {
        case .enable: return "Enable"
        case .disable: return "Disable"
        case .restart: return "Restart"
        case .login: return "Sign in"
        case .approve: return "Review quarantine…"
        }
    }

    /// Maps the `health.action` string the core sends to the action the tray
    /// would perform. `approve` is deliberately absent: a quarantine review is
    /// a human decision, never something a menu click does on the user's
    /// behalf.
    static func fromHealthAction(_ action: String) -> TrayServerAction? {
        switch action {
        case "login": return .login
        case "restart": return .restart
        case "enable": return .enable
        default: return nil
        }
    }
}

enum TrayServerActionFailure {
    static func title(action: TrayServerAction, server: String) -> String {
        "Couldn’t \(action.verb) \(server)"
    }

    /// The error as the user can act on it, with the one thing the tray knows
    /// they can do next. Never empty — an alert with no body reads as a bug.
    static func message(action: TrayServerAction, server: String, error: Error) -> String {
        let detail = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        let body = detail.trimmingCharacters(in: .whitespacesAndNewlines)
        let reason = body.isEmpty ? "The core did not say why." : body
        return "\(reason)\n\nOpen \(server) in MCPProxy to see its logs, or try again."
    }
}
