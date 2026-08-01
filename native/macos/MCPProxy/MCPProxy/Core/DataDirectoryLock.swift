// DataDirectoryLock.swift
// MCPProxy
//
// "Is a core alive?", asked WITHOUT going through the socket (GH #933).
//
// Every other liveness signal the tray has is a socket probe, and a socket probe
// cannot distinguish the two situations that matter most:
//
//   * a file a dead core left behind, which refuses every connection, and
//   * a LIVE core whose listen backlog is full, which also refuses every
//     connection (on macOS a full backlog is ECONNREFUSED, not EAGAIN).
//
// Accumulated evidence closes most of the gap — a core that accepted even once
// during the episode is never launched over — but not the case where the core
// was already saturated when the episode began. It accepts nothing, so
// `onlyEverRefused` stays true, and after the deadline the tray escalated to a
// launch over a healthy core. Reproduced live: four doomed cores in 39 seconds,
// each dying on the lock this file asks about, ending in a retry-exhaustion
// error about a core that was answering `/ready` normally the whole time.
//
// The lock is the missing signal, and it is a good one for one specific reason:
// the kernel releases `flock` when the holding process dies. There is no such
// thing as a stale flock, so "somebody holds it" cannot be faked by a dead core
// the way a leftover socket FILE can. The core takes it (bbolt, via
// `internal/storage/bbolt.go`) before it creates its listener, so it is held for
// strictly longer than the socket exists.

import Foundation

/// What the data directory's database says about a core owning it.
enum DataDirectoryLock: Equatable {

    /// A live process holds the exclusive lock. Something IS running here.
    case heldByALiveProcess

    /// The file is there and nobody holds it — the process that did is gone.
    case free

    /// No file, or the probe could not run. Says nothing either way, and must
    /// be treated as such: this is the answer for a socket that is not inside a
    /// data directory at all.
    case unknown(String)

    /// The database beside a core's socket.
    ///
    /// Derived from the socket rather than from the instance root because the
    /// question is about THAT core: a tray pointed at another instance's socket
    /// (`MCPPROXY_SOCKET_PATH`) must ask about that instance's data directory,
    /// not its own. When the socket is not in a data directory the probe simply
    /// answers `.unknown` and nothing changes.
    static func path(forSocket socketPath: String) -> String {
        URL(fileURLWithPath: socketPath)
            .deletingLastPathComponent()
            .appendingPathComponent(InstancePaths.databaseFileName)
            .path
    }

    /// Ask whether a live process holds the database.
    ///
    /// A SHARED, non-blocking lock, released immediately. Shared for a reason:
    /// bbolt's is exclusive, so a shared request still fails against a live core
    /// — while two trays probing at the same moment both succeed instead of each
    /// reporting the other as a running core. Non-blocking so the probe cannot
    /// become the thing that stalls a launch, and released at once so it cannot
    /// delay a core that is starting up.
    static func probe(path: String) -> DataDirectoryLock {
        let descriptor = open(path, O_RDONLY)
        guard descriptor >= 0 else {
            return .unknown("cannot open \(path) (errno \(errno))")
        }
        defer { close(descriptor) }

        if flock(descriptor, LOCK_SH | LOCK_NB) == 0 {
            flock(descriptor, LOCK_UN)
            return .free
        }
        let code = errno
        // EWOULDBLOCK is EAGAIN on Darwin, and it is the only errno that means
        // "somebody else holds it". Anything else is our problem, not evidence.
        if code == EWOULDBLOCK { return .heldByALiveProcess }
        return .unknown("flock failed on \(path) (errno \(code))")
    }
}
