// UpdateMenuState.swift
// MCPProxy
//
// Spec 092 FR-017 — "exactly one source of truth MUST own the update menu item
// at any time".
//
// The tray has two update brains and always has had: Sparkle's appcast (which
// can actually install) and the legacy release check (core `/api/v1/info` and a
// direct GitHub call, which can only open a browser). Before this file they
// were rendered independently, so a single available release could produce two
// items — one of them offering a one-click install and one of them offering a
// download page — and a feed that lagged behind GitHub could offer a one-click
// install of an older version than the browser item advertised.
//
// The rules, from FR-017 — "EXACTLY ONE source of truth owns the update menu
// item at any time":
//   · feed offer present → the feed owns the slot, whatever the legacy check
//     says. Not just for the same-or-lower version: rendering a one-click item
//     AND a browser item because GitHub is a release ahead is two items, which
//     is the thing the requirement forbids, and it asks the user to choose
//     between an install they can do and a download they cannot verify;
//   · legacy result only (feed unreachable, or the feed carries nothing) →
//     browser-download guidance, never a one-click action it cannot perform;
//   · nothing installable in place (FR-016) → the feed's offer degrades to
//     guidance, still one item.
//
// Pure, so the whole matrix is a table test.

import Foundation

/// What the update section of the menu should contain, in order.
enum UpdateMenuEntry: Equatable {
    /// Sparkle can install this. One click does everything (FR-010).
    case oneClick(version: String)

    /// Only the legacy check knows about this version; all the tray can do is
    /// open the download page (FR-017).
    case browserGuidance(version: String)

    /// An in-place update is impossible here (FR-016). Rendered whether or not
    /// an update is known, because the user needs to know before they go
    /// looking for the update item that will never appear.
    case blocked(UpdateBlockedReason)
}

enum UpdateMenuState {

    /// Build the update section.
    ///
    /// - Parameters:
    ///   - feedVersion: version Sparkle is offering, nil when it has none.
    ///   - legacyVersion: version the core / GitHub check advertises, nil when
    ///     there is none.
    ///   - blocked: FR-016 reason, when in-place updating is impossible.
    ///   - nudgesSuppressed: CI / non-interactive — offer nothing unasked.
    static func entries(
        feedVersion: String?,
        legacyVersion: String?,
        blocked: UpdateBlockedReason?,
        nudgesSuppressed: Bool
    ) -> [UpdateMenuEntry] {
        var entries: [UpdateMenuEntry] = []

        // A blocked reason is not a nudge — it is the answer to "why is there no
        // update item?", and suppressing it is how FR-016's "fails silently"
        // happens. It is only worth saying when there is in fact something to
        // install, though; on an up-to-date app it is noise.
        let haveSomething = feedVersion != nil || legacyVersion != nil
        if let blocked, haveSomething {
            entries.append(.blocked(blocked))
        }

        guard !nudgesSuppressed else { return entries }

        // Nothing can be installed in place: offering a one-click item that
        // cannot work is exactly what FR-016 forbids, so the offer degrades to
        // the browser path the blocked message already points at.
        let canInstall = blocked == nil

        if let feed = feedVersion {
            // The feed owns the slot whenever it has an offer — higher, lower
            // or incomparable to what the legacy check found. A feed that lags
            // behind GitHub still installs something real in one click, and the
            // next scheduled check picks the newer version up; two competing
            // items would not.
            entries.append(canInstall
                ? .oneClick(version: feed)
                : .browserGuidance(version: feed))
        } else if let legacy = legacyVersion {
            entries.append(.browserGuidance(version: legacy))
        }

        return entries
    }

    /// Version strings differ across surfaces only by a leading "v".
    static func normalized(_ version: String) -> String {
        version.hasPrefix("v") ? String(version.dropFirst()) : version
    }
}
