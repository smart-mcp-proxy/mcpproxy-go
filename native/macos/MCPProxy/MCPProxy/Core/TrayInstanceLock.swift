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
}
