// TrayInstanceLock.swift
// MCPProxy
//
// One tray per instance root. A second tray (launched from a second copy of the
// app, the binary run directly, or a login item racing a manual launch) adds a
// second menu-bar icon and fights the first over the core.
//
// An exclusive `flock` on `<root>/mcpproxy-tray.lock`, the same file the Go tray
// (cmd/mcpproxy-tray, singleinstance_darwin.go) locks, so the two exclude each
// other too. The kernel releases the lock when the holder dies, so a crashed
// tray never blocks the next launch.

import Foundation

enum TrayInstanceLock: Equatable {

    /// This process holds the lock. The descriptor stays open for the life of
    /// the process.
    case acquired(Int32)

    /// Another live tray holds it.
    case heldByAnotherTray

    /// The lock could not be tried. Says nothing about another tray, so the
    /// caller must start anyway.
    case unavailable(String)

    static func acquire(at url: URL) -> TrayInstanceLock {
        try? FileManager.default.createDirectory(
            at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        // O_CLOEXEC: a core this tray spawns must not inherit, and so keep, the lock.
        let descriptor = open(url.path, O_RDWR | O_CREAT | O_CLOEXEC, 0o600)
        guard descriptor >= 0 else {
            return .unavailable("cannot open \(url.path) (errno \(errno))")
        }
        if flock(descriptor, LOCK_EX | LOCK_NB) == 0 {
            return .acquired(descriptor)
        }
        let code = errno
        close(descriptor)
        if code == EWOULDBLOCK { return .heldByAnotherTray }
        return .unavailable("flock failed on \(url.path) (errno \(code))")
    }

    enum HandOverOutcome {
        /// The successor was launched; this tray no longer holds the lock.
        case launched
        /// The launch threw. This tray keeps running, holding `relock`.
        case failed(Error, relock: TrayInstanceLock)
    }

    struct LauncherFailed: Error, CustomStringConvertible {
        let status: Int32
        var description: String { "the launcher exited with status \(status)" }
    }

    /// Runs the launcher (`open -n <bundle>`) and waits for it. `run()` alone
    /// only proves the launcher started; `open` reports a bundle it could not
    /// launch through its exit status, and that must count as a failed launch.
    static func runSuccessorLauncher(_ launcher: Process) throws {
        try launcher.run()
        launcher.waitUntilExit()
        guard launcher.terminationStatus == 0 else {
            throw LauncherFailed(status: launcher.terminationStatus)
        }
    }

    /// Starts a successor tray while this one is still running (relaunch into a
    /// replaced bundle). The lock is released first: the successor checks it at
    /// launch, and finding it still held it would exit, leaving no tray at all.
    /// If the launch fails this tray stays, so it takes the lock back.
    static func handOver(_ descriptor: Int32, at url: URL,
                         launch: () throws -> Void) -> HandOverOutcome {
        close(descriptor)
        do {
            try launch()
            return .launched
        } catch {
            return .failed(error, relock: acquire(at: url))
        }
    }
}
