// StagedCoreBinary.swift
// MCPProxy
//
// Spec 092 FR-030 — the legacy staged core copy at
// `~/Library/Application Support/mcpproxy/bin/mcpproxy`.
//
// ## Which paths still resolve it ahead of the bundled core?
//
// The analysis FR-030 asks for, written down because the conclusion is mostly
// "none", and the tempting action (delete it) is the wrong one.
//
// 1. **This tray — no.** `CoreProcessManager.resolveBinary()` tries, in order:
//    `MCPPROXY_CORE_PATH`, the BUNDLED core at `Contents/Resources/bin/mcpproxy`,
//    then this staged copy, then `~/.mcpproxy/bin`, then Homebrew/`/usr/local`,
//    then `PATH`. The bundled core is checked first, so the staged copy can
//    only ever be reached when the app is not running from a bundle (a
//    `swift run` dev build) — and in that case there IS no bundled core for it
//    to shadow.
//
// 2. **The legacy Go tray (`cmd/mcpproxy-tray`) — yes, but self-healing.**
//    Its `resolveCoreBinary()` calls `ensureManagedCoreBinary()`, which stages
//    the bundled core here and returns THIS path, ahead of everything else.
//    It re-copies whenever the sizes differ or the bundled binary is newer, so
//    the copy it runs tracks the bundle it ships with. It is the only writer
//    that has ever created this file.
//
// 3. **The `/usr/local/bin/mcpproxy` CLI symlink — no.** `SymlinkService`
//    points it at the bundled binary.
//
// 4. **A user's `PATH` — out of scope by construction.** If someone put this
//    directory on their `PATH`, the binary is theirs to manage, and FR-030 is
//    explicit that nothing may be deleted without an analysis that covers it.
//
// ## What is actually done
//
// The residual hazard is not shadowing — it is that a stale executable sits
// there and anything (a legacy tray, a shell alias, a launchd plist written
// years ago) can still run it and serve an old version. So: REFRESH it from
// the bundled core, never remove it, and only when all of these hold:
//
//   - it exists and is a regular file (a symlink is somebody's deliberate
//     wiring and is left exactly as found);
//   - it answers `version -o json`, i.e. it really is an mcpproxy binary;
//   - that version is strictly OLDER than the bundled core's, by SemVer.
//
// Anything unprovable — unreadable version, unparseable version, no bundled
// core — means no action and a logged reason. Refreshing keeps every existing
// user of the path working while removing the "old version still served"
// outcome; deleting would break them for no additional benefit.

import Foundation

enum StagedCoreBinary {

    /// The legacy staged path. Constructed the same way the Go tray built it
    /// (`getManagedBinDir`), and the same way `resolveBinary()` looks for it.
    static func defaultPath(
        home: String = FileManager.default.homeDirectoryForCurrentUser.path
    ) -> String {
        "\(home)/Library/Application Support/mcpproxy/bin/mcpproxy"
    }

    /// What to do about the staged copy.
    enum Action: Equatable {
        case none(reason: String)
        case refresh(from: String, staleVersion: String, freshVersion: String)
    }

    /// The decision, isolated from the file system so every branch is testable.
    static func decide(
        stagedExists: Bool,
        stagedIsSymlink: Bool,
        stagedVersion: String?,
        bundledPath: String?,
        bundledVersion: String?
    ) -> Action {
        guard let bundledPath, let bundledVersion, !bundledVersion.isEmpty else {
            return .none(reason: "no bundled core to refresh from")
        }
        guard stagedExists else {
            // Deliberately NOT created. Creating one would make this tray a
            // second writer of a legacy artifact it does not otherwise need.
            return .none(reason: "no staged copy exists")
        }
        guard !stagedIsSymlink else {
            return .none(reason: "the staged path is a symlink — someone wired it deliberately")
        }
        guard let stagedVersion, !stagedVersion.isEmpty else {
            return .none(reason: "the staged copy did not report a version — not touching it")
        }
        guard let order = SemanticVersion.compare(stagedVersion, bundledVersion) else {
            return .none(reason: "cannot compare staged \(stagedVersion) with bundled \(bundledVersion)")
        }
        guard order < 0 else {
            return .none(reason: "staged copy v\(stagedVersion) is not older than the bundled core")
        }
        return .refresh(from: bundledPath, staleVersion: stagedVersion, freshVersion: bundledVersion)
    }

    /// Inspect the staged copy and refresh it if the rules above allow.
    /// Returns the action taken (`.none` carries the reason), for the log and
    /// for tests.
    @discardableResult
    static func refreshIfStale(
        bundledPath: String? = BundledCore.binaryPath(),
        bundledVersion: String? = nil,
        stagedPath: String = defaultPath()
    ) -> Action {
        let fm = FileManager.default
        let attributes = try? fm.attributesOfItem(atPath: stagedPath)
        let exists = attributes != nil
        let isSymlink = (attributes?[.type] as? FileAttributeType) == .typeSymbolicLink

        // Only pay for the subprocesses when there is something to compare.
        var stagedVersion: String?
        var resolvedBundledVersion = bundledVersion
        if exists, !isSymlink, let bundledPath {
            stagedVersion = CoreBinaryVersion.read(at: stagedPath)
            if resolvedBundledVersion == nil {
                resolvedBundledVersion = CoreBinaryVersion.read(at: bundledPath)
            }
        }

        let action = decide(
            stagedExists: exists,
            stagedIsSymlink: isSymlink,
            stagedVersion: stagedVersion,
            bundledPath: bundledPath,
            bundledVersion: resolvedBundledVersion
        )

        guard case .refresh(let source, let stale, let fresh) = action else {
            if case .none(let reason) = action {
                NSLog("[MCPProxy] Staged core copy: no action (%@)", reason)
            }
            return action
        }

        NSLog("[MCPProxy] Refreshing the staged core copy at %@ from v%@ to v%@",
              stagedPath, stale, fresh)
        do {
            try replace(stagedPath, withContentsOf: source, preserving: attributes)
        } catch {
            NSLog("[MCPProxy] Could not refresh the staged core copy: %@",
                  error.localizedDescription)
            return .none(reason: "refresh failed: \(error.localizedDescription)")
        }
        return action
    }

    /// Copy `source` over `target` through a sibling temp file and a rename.
    ///
    /// Rename rather than write-in-place: a legacy tray may be EXECUTING the
    /// staged binary right now, and overwriting the bytes of a running image
    /// crashes it (ETXTBSY at best). A rename swaps the directory entry and
    /// leaves the running process on its own inode.
    private static func replace(
        _ target: String, withContentsOf source: String, preserving attributes: [FileAttributeKey: Any]?
    ) throws {
        let fm = FileManager.default
        let staging = target + ".new-\(ProcessInfo.processInfo.processIdentifier)"
        try? fm.removeItem(atPath: staging)
        try fm.copyItem(atPath: source, toPath: staging)

        // Keep the mode the staged copy already had (the Go tray used 0755);
        // fall back to 0755 when it could not be read.
        let mode = (attributes?[.posixPermissions] as? NSNumber) ?? NSNumber(value: 0o755)
        try fm.setAttributes([.posixPermissions: mode], ofItemAtPath: staging)

        guard rename(staging, target) == 0 else {
            let code = errno
            try? fm.removeItem(atPath: staging)
            throw NSError(domain: NSPOSIXErrorDomain, code: Int(code), userInfo: [
                NSLocalizedDescriptionKey: "rename(\(staging), \(target)) failed (errno \(code))"
            ])
        }
    }
}
