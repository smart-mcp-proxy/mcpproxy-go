// LifecycleJournal.swift
// MCPProxy
//
// A persistent record of who started and stopped what (GH #862).
//
// The gap it fills: when the app and its core disappeared mid-session, nothing
// on disk said why. Graceful paths log — but only to the running process's log,
// and only while it is running; a SIGKILL-class death (jetsam, `pkill`, a crash
// outside our handlers) leaves the log simply STOPPING, which reads exactly like
// a clean quit once the process is gone. macOS purges the Info/Debug tier of the
// unified log within hours, so by the time anyone looks there is nothing left to
// read either.
//
// Two things follow from that, and they are the whole design:
//
//   * Every ending records a REASON, at the moment it happens. A shutdown that
//     logs nothing is indistinguishable from a kill.
//   * A run that recorded no ending is detectable at the NEXT launch. That is
//     the only way a silent death is ever attributable, because the process that
//     could have described it is gone.
//
// The file is JSON lines rather than a JSON document precisely because it is
// written by a process that may be killed mid-write: a truncated tail costs the
// last record, not the file. Everything here is best-effort — diagnostics must
// never be why the app fails to start.

import Foundation
import os

/// One lifecycle transition.
struct LifecycleEvent: Codable, Equatable {

    enum Kind: String, Codable {
        /// The tray process came up.
        case appLaunched
        /// The tray process is going down, for a reason it can name.
        case appTerminating
        /// A core process was spawned by this tray.
        case coreLaunched
        /// A core this tray owned was asked to stop, with the reason it was.
        case coreTerminated
        /// A core exited on its own — crash, `pkill`, its own shutdown.
        case coreExited
        /// A catchable signal arrived. On its own this is NOT an ending: the
        /// termination record that should follow it is.
        case signalReceived
        /// A periodic update check ran. Cheap, and it makes the updater
        /// trivially includable or excludable in any future incident.
        case updateCheck
    }

    let kind: Kind
    /// Who asked, and why. Free text, because that is what a human reads.
    let reason: String
    let at: Date
    /// How long this tray process had been up when the event happened.
    let uptimeSeconds: TimeInterval?
    let pid: Int32
}

/// How the previous run of the app ended.
enum PreviousRunEnding: Equatable {
    /// No journal, or nothing readable in it.
    case noPreviousRun
    /// The run recorded why it was going.
    case clean(LifecycleEvent)
    /// The run stopped without recording anything. `last` is the final thing it
    /// did record — the only lead an incident has.
    case unclean(last: LifecycleEvent?)
}

/// Append-only lifecycle record, one JSON object per line.
///
/// Not an actor and not `@MainActor`: it is called from a signal-source handler
/// and from `applicationWillTerminate`, neither of which can await anything, and
/// the writes are serialised by an internal lock instead.
final class LifecycleJournal {

    private let url: URL
    private let maxRecords: Int
    private let lock = NSLock()

    /// Notice level, deliberately: `os_log` Info and Debug are purged from the
    /// unified log within hours, which is exactly why the original incident had
    /// no spawn/exit context left by the time it was investigated.
    private static let log = Logger(subsystem: "com.smartmcpproxy.mcpproxy",
                                    category: "lifecycle")

    init(url: URL = InstancePaths.root.appendingPathComponent("tray-lifecycle.jsonl"),
         maxRecords: Int = 500) {
        self.url = url
        self.maxRecords = maxRecords
    }

    /// Record an event: to the journal, and to the unified log at a level that
    /// survives.
    func append(_ event: LifecycleEvent) {
        Self.log.notice("""
            lifecycle \(event.kind.rawValue, privacy: .public): \
            \(event.reason, privacy: .public) \
            [pid \(event.pid, privacy: .public), \
            uptime \(Self.duration(event.uptimeSeconds), privacy: .public)]
            """)
        NSLog("[MCPProxy] lifecycle %@: %@ (uptime %@)",
              event.kind.rawValue, event.reason, Self.duration(event.uptimeSeconds))

        guard let line = try? Self.encoder.encode(event) else { return }
        var data = line
        data.append(0x0A)

        lock.lock()
        defer { lock.unlock() }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(),
                                                 withIntermediateDirectories: true)
        if let handle = try? FileHandle(forWritingTo: url) {
            defer { try? handle.close() }
            handle.seekToEndOfFile()
            handle.write(data)
            return
        }
        try? data.write(to: url, options: .atomic)
    }

    /// Every readable record, oldest first. Unparsable lines are skipped: a
    /// record torn in half by a kill must not cost the ones around it.
    func events() -> [LifecycleEvent] {
        lock.lock()
        let raw = try? Data(contentsOf: url)
        lock.unlock()
        guard let raw, let text = String(data: raw, encoding: .utf8) else { return [] }
        return text.split(separator: "\n").compactMap { line in
            guard let data = line.data(using: .utf8) else { return nil }
            return try? Self.decoder.decode(LifecycleEvent.self, from: data)
        }
    }

    /// Keep only the newest `maxRecords`. Called once at launch, so an
    /// always-on tray does not grow a journal nobody will ever read the middle
    /// of — an incident is read backwards from the end.
    func trim() {
        let kept = events().suffix(maxRecords)
        guard kept.count < events().count else { return }
        let lines = kept.compactMap { try? Self.encoder.encode($0) }
        var data = Data()
        for line in lines {
            data.append(line)
            data.append(0x0A)
        }
        lock.lock()
        defer { lock.unlock() }
        try? data.write(to: url, options: .atomic)
    }

    /// How the previous run ended. MUST be consulted before this run appends
    /// its own launch record, or it reads that record as the previous ending.
    func previousRunEnding() -> PreviousRunEnding {
        let events = events()
        guard let last = events.last else { return .noPreviousRun }
        if last.kind == .appTerminating { return .clean(last) }
        return .unclean(last: last)
    }

    /// The line to log at launch when the previous run never said goodbye, or
    /// nil when it did.
    ///
    /// It names the three facts an incident needs and nothing else: how long
    /// the run lasted, what it was doing last, and when that was. Anything a
    /// reader has to reconstruct is a fact the journal failed to keep.
    static func uncleanExitSummary(for ending: PreviousRunEnding) -> String? {
        guard case .unclean(let last) = ending else { return nil }
        guard let last else {
            return "previous run ended without recording a shutdown reason "
                + "(no events survived) — likely SIGKILL-class termination "
                + "(jetsam, pkill, power loss)"
        }
        let stamp = ISO8601DateFormatter().string(from: last.at)
        return "previous run ended without recording a shutdown reason — "
            + "last event \(last.kind.rawValue) \"\(last.reason)\" at \(stamp) "
            + "after \(duration(last.uptimeSeconds)) of uptime; a graceful stop always "
            + "records one, so this was SIGKILL-class (jetsam, pkill, power loss) "
            + "or a crash outside the app's handlers"
    }

    /// Uptime as something a human reads at a glance.
    static func duration(_ seconds: TimeInterval?) -> String {
        guard let seconds, seconds >= 0 else { return "unknown" }
        let total = Int(seconds.rounded())
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        if hours > 0 { return "\(hours)h\(minutes)m" }
        if minutes > 0 { return "\(minutes)m\(total % 60)s" }
        return "\(total)s"
    }

    private static let encoder: JSONEncoder = {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .iso8601
        // One line per record, always: a pretty-printed record would span
        // several and the line-per-record recovery rule would be a lie.
        encoder.outputFormatting = []
        return encoder
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return decoder
    }()
}
