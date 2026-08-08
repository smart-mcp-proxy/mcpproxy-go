// BundledCore.swift
// MCPProxy
//
// "Which core version would this tray start if it started one right now?"
// (Spec 092 FR-001). The supersede decision is a comparison against THAT
// number, not against the app's own version: restarting a core only helps if
// the binary the tray would launch is genuinely newer.

import Foundation

/// Reads a version out of an mcpproxy binary by asking it.
///
/// Asking is the ground truth. The alternative — assume the bundled core
/// carries the app's `CFBundleShortVersionString` because the build script
/// stamps both from one `--version` argument — is true for every build the
/// pipeline produces and false for exactly the case that matters: a bundle
/// whose core was replaced by hand, or a partially applied update. It is kept
/// as the FALLBACK (a binary that will not answer still tells us nothing), not
/// as the primary source.
enum CoreBinaryVersion {

    /// Run `<binary> version -o json` and return the reported version.
    ///
    /// `version` is a pure print in the core's cobra tree — no config load, no
    /// database open — so this cannot contend with a running core for the
    /// BBolt lock. That property is load-bearing; do not switch this to a
    /// subcommand that initializes anything.
    static func read(at path: String, timeout: TimeInterval = 5.0) -> String? {
        guard FileManager.default.isExecutableFile(atPath: path) else { return nil }

        let process = Process()
        process.executableURL = URL(fileURLWithPath: path)
        process.arguments = ["version", "-o", "json"]
        let stdout = Pipe()
        process.standardOutput = stdout
        process.standardError = FileHandle.nullDevice
        // A core binary must never inherit the tray's socket/home overrides for
        // a question this trivial.
        process.environment = ["PATH": "/usr/bin:/bin"]

        do {
            try process.run()
        } catch {
            NSLog("[MCPProxy] Could not run %@ to read its version: %@",
                  path, error.localizedDescription)
            return nil
        }

        // A binary that hangs must not hang the tray with it.
        let watchdog = DispatchWorkItem { [weak process] in
            guard let process, process.isRunning else { return }
            NSLog("[MCPProxy] %@ did not report a version within %.0fs — killing it", path, timeout)
            process.terminate()
        }
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout, execute: watchdog)

        // Drain BEFORE waiting: a child that fills the pipe buffer while we sit
        // in waitUntilExit deadlocks both sides. The output is tiny today, and
        // this ordering keeps that from being a requirement.
        let data = stdout.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        watchdog.cancel()

        guard process.terminationStatus == 0 else { return nil }
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let version = json["version"] as? String,
              !version.isEmpty else { return nil }
        return version
    }
}

/// The core binary shipped inside this app bundle.
enum BundledCore {

    /// `MCPProxy.app/Contents/Resources/bin/mcpproxy`, or nil when the app is
    /// not running from a bundle (a `swift run` dev build) or ships no core.
    static func binaryPath(bundle: Bundle = .main) -> String? {
        guard let execPath = bundle.executablePath else { return nil }
        let contents = URL(fileURLWithPath: execPath)
            .deletingLastPathComponent()   // Contents/MacOS
            .deletingLastPathComponent()   // Contents
        guard contents.lastPathComponent == "Contents" else { return nil }
        let candidate = contents
            .appendingPathComponent("Resources")
            .appendingPathComponent("bin")
            .appendingPathComponent("mcpproxy")
        return FileManager.default.isExecutableFile(atPath: candidate.path) ? candidate.path : nil
    }

    /// The app's own marketing version, read from the LOADED bundle (i.e. the
    /// version of the code currently executing — see `BundleUpdateWatcher` for
    /// the on-disk counterpart).
    static func appVersion(bundle: Bundle = .main) -> String? {
        (bundle.infoDictionary?["CFBundleShortVersionString"] as? String)
            .flatMap { $0.isEmpty ? nil : $0 }
    }

    /// The version the tray would respawn into, or nil when there is nothing
    /// better to respawn into.
    ///
    /// Nil is returned — and supersede therefore never fires — when:
    /// - the app has no bundled core (dev build, or a bundle built without
    ///   one): restarting would re-resolve to whatever is on `PATH`, which is
    ///   as likely to be the old core as the new one;
    /// - `MCPPROXY_CORE_PATH` points somewhere else: the operator has said
    ///   which binary to run, and a supersede would relaunch that same binary
    ///   in a circle.
    static func respawnVersion(
        bundle: Bundle = .main,
        environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> String? {
        if let override = environment["MCPPROXY_CORE_PATH"], !override.isEmpty {
            return nil
        }
        guard let path = binaryPath(bundle: bundle) else { return nil }
        if let reported = CoreBinaryVersion.read(at: path) {
            return reported
        }
        // The binary is there but would not answer. The build pipeline stamps
        // the bundle and the core from the same version, so the Info.plist is
        // the best remaining estimate — and it is only ever used to decide
        // "is the RUNNING core older", never to claim what got installed.
        NSLog("[MCPProxy] Bundled core at %@ did not report a version — "
              + "falling back to CFBundleShortVersionString", path)
        return appVersion(bundle: bundle)
    }
}
