// InstancePaths.swift
// MCPProxy
//
// Every path a tray instance owns, resolved in one place (GH #936).
//
// Why this file exists rather than a `homeDirectoryForCurrentUser` per call
// site: that API IGNORES `$HOME`, so a tray built from a branch and run out of a
// scratch bundle still read and wrote the real `~/.mcpproxy`. Two things went
// wrong repeatedly during live QA. The socket is a machine-wide exclusive
// resource, so two QA runs could not proceed in parallel — one silently attached
// to the other's core and produced garbage results. And
// `AutostartSidecarService.refresh()` wrote the scratch bundle's login-item
// state (always `false`, because a copied bundle is not a registered login item)
// straight over the user's real `{"enabled":true}`.
//
// The override is dev/QA-only IN SPIRIT, not by enforcement: it is an
// environment variable, unset in normal use, and with it unset every path below
// resolves exactly where it always did. `MCPPROXY_SOCKET_PATH` (GH #926) was the
// first half of this and still outranks the root — it points at one specific
// core, which is not always the core that owns the root.
//
// Deliberately NOT relocated: `~/Library/Logs/mcpproxy` (the core's own log
// directory, which follows the core's rules, not the tray's) and the app's
// preferences domain (`cfprefsd` honours neither `$HOME` nor this variable —
// use a distinct bundle id for a dev instance).

import Foundation

/// Resolves the files a tray instance reads and writes.
///
/// Every rule takes `environment` and `home` as parameters so it is testable
/// without mutating the process's real environment; the no-argument
/// conveniences below are what production calls.
enum InstancePaths {

    // MARK: - Names

    /// Relocates the whole instance root — everything that would otherwise live
    /// in `~/.mcpproxy`.
    static let rootEnvVar = "MCPPROXY_HOME"

    /// Points at one core's socket, wherever the root is (GH #926).
    static let socketPathEnvVar = "MCPPROXY_SOCKET_PATH"

    static let defaultRootName = ".mcpproxy"
    static let socketFileName = "mcpproxy.sock"
    static let sidecarFileName = "tray-autostart.json"
    static let configFileName = "mcp_config.json"

    /// The bbolt database the core takes an exclusive `flock` on before it
    /// creates its listener (`internal/storage/bbolt.go`). Named here because
    /// the liveness check in `CoreProcessManager` asks whether a live process
    /// holds it — see `DataDirectoryLock` (GH #933).
    static let databaseFileName = "config.db"

    /// Usable bytes of `sockaddr_un.sun_path` (104 including the terminator).
    static let socketPathByteLimit = 103

    // MARK: - Rules

    /// The instance root: the override, or `~/.mcpproxy`.
    static func root(environment: [String: String], home: URL) -> URL {
        guard let override = nonBlank(environment[rootEnvVar]) else {
            return home.appendingPathComponent(defaultRootName, isDirectory: true)
        }
        return URL(fileURLWithPath: (override as NSString).expandingTildeInPath,
                   isDirectory: true).standardizedFileURL
    }

    /// Whether this instance has been relocated. Production reads it to decide
    /// what to tell the core it spawns, and to log the fact once at launch —
    /// a relocated instance that looks ordinary in the log is how a QA run
    /// convinces itself it tested the real thing.
    static func isOverridden(environment: [String: String]) -> Bool {
        nonBlank(environment[rootEnvVar]) != nil
    }

    /// The core's socket. `MCPPROXY_SOCKET_PATH` first, then the root.
    static func socketPath(environment: [String: String], home: URL) -> String {
        if let explicit = nonBlank(environment[socketPathEnvVar]) { return explicit }
        return root(environment: environment, home: home)
            .appendingPathComponent(socketFileName, isDirectory: false).path
    }

    static func autostartSidecarURL(environment: [String: String], home: URL) -> URL {
        root(environment: environment, home: home)
            .appendingPathComponent(sidecarFileName, isDirectory: false)
    }

    static func configFileURL(environment: [String: String], home: URL) -> URL {
        root(environment: environment, home: home)
            .appendingPathComponent(configFileName, isDirectory: false)
    }

    static func dataLockURL(environment: [String: String], home: URL) -> URL {
        root(environment: environment, home: home)
            .appendingPathComponent(databaseFileName, isDirectory: false)
    }

    /// Arguments for a core this tray spawns.
    ///
    /// Both flags or neither: `--data-dir` alone still loads the default
    /// config file (see `cmd/mcpproxy/cli_config.go`), and a core reading the
    /// real config while writing a scratch data directory is a subtler version
    /// of the accident this override exists to prevent.
    static func coreArguments(environment: [String: String], home: URL) -> [String] {
        guard isOverridden(environment: environment) else { return ["serve"] }
        let root = root(environment: environment, home: home)
        return [
            "serve",
            "--data-dir", root.path,
            "--config", configFileURL(environment: environment, home: home).path
        ]
    }

    /// Why this socket path cannot work, or nil when it can.
    ///
    /// The failure it catches is silent: `bind(2)` copies into a 104-byte
    /// `sun_path`, so a socket under a deep scratch directory is never created
    /// and the tray simply never finds a core. Reported rather than clamped —
    /// there is no shorter path that is still the path the core was told.
    static func socketPathProblem(_ path: String) -> String? {
        let bytes = path.utf8.count
        guard bytes > socketPathByteLimit else { return nil }
        return "socket path is \(bytes) bytes, over the \(socketPathByteLimit)-byte "
            + "sockaddr_un limit — it cannot be bound. Use a shorter \(rootEnvVar) "
            + "(e.g. /tmp/mcpproxy-qa)."
    }

    private static func nonBlank(_ value: String?) -> String? {
        guard let value else { return nil }
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? nil : trimmed
    }

    // MARK: - Production conveniences

    private static var processEnvironment: [String: String] {
        ProcessInfo.processInfo.environment
    }

    /// `homeDirectoryForCurrentUser`, kept in ONE place. It ignores `$HOME`;
    /// `rootEnvVar` is the supported way around that.
    private static var currentHome: URL {
        FileManager.default.homeDirectoryForCurrentUser
    }

    static var root: URL { root(environment: processEnvironment, home: currentHome) }
    static var isOverridden: Bool { isOverridden(environment: processEnvironment) }
    static var socketPath: String { socketPath(environment: processEnvironment, home: currentHome) }
    static var autostartSidecarURL: URL {
        autostartSidecarURL(environment: processEnvironment, home: currentHome)
    }
    static var configFileURL: URL {
        configFileURL(environment: processEnvironment, home: currentHome)
    }
    static var dataLockURL: URL {
        dataLockURL(environment: processEnvironment, home: currentHome)
    }
    static var coreArguments: [String] {
        coreArguments(environment: processEnvironment, home: currentHome)
    }
}
