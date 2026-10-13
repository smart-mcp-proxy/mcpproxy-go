// TelemetryNotice.swift
// MCPProxy
//
// Spec 109 FR-044a (codex first-run user test F-03): the effective telemetry
// state decides what the first-run welcome and Settings say. An environment
// opt-out (MCPPROXY_TELEMETRY=false, DO_NOT_TRACK, CI) is stated, not
// disclosed; an opt-out in the user's own config shows no notice; an unknown
// state keeps the standard notice (failing toward disclosure).
//
// The strings are byte-identical to frontend/src/utils/telemetryState.ts and
// both are asserted against internal/telemetry/testdata/effective_state_cases.json.

import Foundation

/// `GET /api/v1/status` → `telemetry`.
struct TelemetryStateDTO: Codable, Equatable {
    let enabled: Bool
    /// `env` | `config` | `default`.
    let source: String
    /// Present only when `source == "env"`: `DO_NOT_TRACK`, `CI` or
    /// `MCPPROXY_TELEMETRY=false`.
    let disabledBy: String?

    enum CodingKeys: String, CodingKey {
        case enabled
        case source
        case disabledBy = "disabled_by"
    }
}

enum TelemetryNoticeMode: String, Equatable {
    case notice
    case offEnv = "off_env"
    case hidden
}

enum TelemetryNotice {
    /// Swift port of Go's `telemetry.IsDisabledByEnv`: precedence DO_NOT_TRACK
    /// (any non-empty, non-"0"), then CI (true/1), then MCPPROXY_TELEMETRY=false.
    /// Values are trimmed and case-folded like the Go side.
    static func envDisabledReason(environment: [String: String]) -> String? {
        func value(_ key: String) -> String {
            (environment[key] ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        }
        let doNotTrack = value("DO_NOT_TRACK")
        if !doNotTrack.isEmpty && doNotTrack != "0" { return "DO_NOT_TRACK" }
        let ci = value("CI").lowercased()
        if ci == "true" || ci == "1" { return "CI" }
        if value("MCPPROXY_TELEMETRY").lowercased() == "false" { return "MCPPROXY_TELEMETRY=false" }
        return nil
    }

    static func mode(state: TelemetryStateDTO?) -> TelemetryNoticeMode {
        guard let state, !state.enabled else { return .notice }
        return state.source == "env" ? .offEnv : .hidden
    }

    static func offLine(state: TelemetryStateDTO) -> String {
        "Anonymous usage telemetry is off — disabled by \(state.disabledBy ?? "") in the environment. Nothing is sent."
    }

    /// The reason a locked Settings toggle shows; nil unless an environment
    /// variable forces telemetry off.
    static func settingLock(state: TelemetryStateDTO?) -> String? {
        guard let state, !state.enabled, state.source == "env" else { return nil }
        return "Off — disabled by \(state.disabledBy ?? "") in the environment. Unset it and restart MCPProxy to change this setting."
    }
}

/// What the modal first-run welcome says about telemetry. It is presented from
/// `applicationDidFinishLaunching`, before any core answers, so it cannot ask
/// the core: it reads the app's own environment. A tray-spawned core inherits
/// that environment, so the two agree; with an attached core (dev rig) the
/// core's environment can differ, and Settings shows the core's truth.
enum FirstRunTelemetryNotice: Equatable {
    case notice
    case offEnv(String)

    static func resolve(
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> FirstRunTelemetryNotice {
        if let reason = TelemetryNotice.envDisabledReason(environment: environment) {
            return .offEnv(reason)
        }
        return .notice
    }
}
