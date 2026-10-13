// BundleUpdateWatcher.swift
// MCPProxy
//
// Spec 092 FR-003 — "MCPProxy was updated to vY — Relaunch".
//
// A drag-install replaces `/Applications/MCPProxy.app` underneath the running
// process. macOS does not notify the app, does not restart it, and does not
// stop it: the old code keeps running (and keeps its old core running) until
// the user notices and quits it by hand. That is the second half of #957.
//
// Detection is a version read from the Info.plist ON DISK. It has to be read
// from disk, deliberately and every time: `Bundle.main.infoDictionary` is the
// dictionary loaded at launch — the version of the code executing right now —
// and comparing it with itself can never be anything but equal.

import Foundation

enum BundleUpdateWatcher {

    /// `CFBundleShortVersionString` from `<bundlePath>/Contents/Info.plist`,
    /// read fresh off disk. Nil when the path is not a bundle, the plist is
    /// unreadable, or it carries no version.
    ///
    /// `Bundle(path:)` is deliberately NOT used: Foundation caches bundles by
    /// path, so a bundle replaced after the first read keeps answering with the
    /// old dictionary — the exact failure this function exists to detect.
    static func onDiskVersion(bundlePath: String) -> String? {
        let plistPath = (bundlePath as NSString)
            .appendingPathComponent("Contents/Info.plist")
        guard let data = FileManager.default.contents(atPath: plistPath) else { return nil }
        guard let plist = try? PropertyListSerialization.propertyList(
            from: data, options: [], format: nil
        ) as? [String: Any] else { return nil }
        guard let version = plist["CFBundleShortVersionString"] as? String,
              !version.isEmpty else { return nil }
        return version
    }

    /// The on-disk version when it is STRICTLY newer than the running one.
    ///
    /// Strictly, and by SemVer precedence (FR-006):
    /// - equal → nil, the steady state, no prompt (FR-005);
    /// - older on disk → nil. Someone reinstalled an older build over a newer
    ///   running one; relaunching into it is a downgrade the tray must not
    ///   propose on its own.
    /// - either side unparseable → nil. A dev build (`CFBundleShortVersionString`
    ///   absent, or a non-version string) must not produce a relaunch offer on
    ///   every activation.
    static func newerVersionOnDisk(runningVersion: String?, onDiskVersion: String?) -> String? {
        guard let runningVersion, let onDiskVersion else { return nil }
        guard let order = SemanticVersion.compare(onDiskVersion, runningVersion) else { return nil }
        return order > 0 ? onDiskVersion : nil
    }

    /// Production entry point: has the bundle this process is running from been
    /// replaced by a newer one?
    ///
    /// `bundle.bundleURL.path` is a PATH, not a handle — after a replacement it
    /// names the new bundle sitting where the old one was, which is precisely
    /// what has to be read. (If the old bundle was moved to the Trash rather
    /// than overwritten, the read fails and the answer is nil: nothing to
    /// relaunch into.)
    static func replacementVersion(bundle: Bundle = .main) -> String? {
        newerVersionOnDisk(
            runningVersion: BundledCore.appVersion(bundle: bundle),
            onDiskVersion: onDiskVersion(bundlePath: bundle.bundleURL.path)
        )
    }
}
