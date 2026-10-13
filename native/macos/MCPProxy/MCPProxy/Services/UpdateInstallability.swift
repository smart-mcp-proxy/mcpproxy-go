// UpdateInstallability.swift
// MCPProxy
//
// Spec 092 FR-016 — "when the app cannot be updated in place (translocated,
// read-only volume, insufficient permissions), the updater MUST tell the user
// why and offer a fallback path rather than failing silently."
//
// Sparkle's own behaviour in these situations is the reason this file exists:
// it declines to install and the failure surfaces, if at all, in a place a
// menu-bar app never shows. So the tray decides for itself, BEFORE offering a
// one-click item, whether a one-click install could even work — and when it
// cannot, the menu says so in words the user can act on.
//
// The check is a pure function of a URL plus two injected probes, so every
// branch is testable against a temp directory instead of against a DMG.

import Foundation

/// Why an in-place update is impossible, and what the user can do instead.
struct UpdateBlockedReason: Equatable {
    /// Short enough for a menu item.
    let menuTitle: String
    /// The explanation shown when the item is activated.
    let explanation: String
    /// What to do instead.
    let fallback: String

    var message: String { explanation + "\n\n" + fallback }
}

enum UpdateInstallability {

    /// Path marker macOS uses for App Translocation: an app launched from a
    /// quarantined DMG or download folder runs from a read-only, randomly named
    /// mount under this directory. Updating it would replace a disk image copy
    /// that vanishes on quit.
    static let translocationMarker = "/AppTranslocation/"

    /// Evaluate whether the bundle at `bundleURL` can be replaced in place.
    ///
    /// - Parameters:
    ///   - bundleURL: the running app bundle (`Bundle.main.bundleURL`).
    ///   - isReadOnlyVolume: probe for the volume's read-only flag.
    ///   - isWritable: probe for write permission on a path.
    /// - Returns: nil when an in-place update is possible.
    static func evaluate(
        bundleURL: URL,
        isReadOnlyVolume: (URL) -> Bool = UpdateInstallability.volumeIsReadOnly,
        isWritable: (String) -> Bool = { FileManager.default.isWritableFile(atPath: $0) }
    ) -> UpdateBlockedReason? {
        let path = bundleURL.path

        if path.contains(translocationMarker) {
            return UpdateBlockedReason(
                menuTitle: "Can’t update — move MCPProxy to Applications first",
                explanation: "MCPProxy is running from a temporary, read-only copy that macOS "
                    + "created because the app was opened straight from a download or a disk "
                    + "image. Nothing can be updated in that copy — it disappears when the app "
                    + "quits.",
                fallback: "Quit MCPProxy, drag MCPProxy.app into your Applications folder, and "
                    + "open it from there. Updates will work from then on."
            )
        }

        if isReadOnlyVolume(bundleURL) {
            return UpdateBlockedReason(
                menuTitle: "Can’t update — MCPProxy is on a read-only volume",
                explanation: "MCPProxy is running from \(path), which is on a read-only volume "
                    + "(most often a mounted disk image).",
                fallback: "Quit MCPProxy, copy MCPProxy.app to your Applications folder, and open "
                    + "it from there."
            )
        }

        // The parent directory is what has to be writable: the installer
        // replaces the bundle as a whole, it does not edit it in place.
        let parent = bundleURL.deletingLastPathComponent().path
        if !isWritable(parent) {
            return UpdateBlockedReason(
                menuTitle: "Can’t update — no permission to replace MCPProxy",
                explanation: "MCPProxy is installed in \(parent), which this user account cannot "
                    + "write to. Replacing the app there needs an administrator.",
                fallback: "Reinstall MCPProxy from the latest download, or move it into your "
                    + "personal Applications folder (~/Applications) where updates need no "
                    + "administrator."
            )
        }

        return nil
    }

    /// Real volume probe. Anything unreadable answers "not read-only": a failed
    /// probe must not manufacture a blocked state and hide a working updater.
    static func volumeIsReadOnly(_ url: URL) -> Bool {
        (try? url.resourceValues(forKeys: [.volumeIsReadOnlyKey]))?.volumeIsReadOnly ?? false
    }
}
