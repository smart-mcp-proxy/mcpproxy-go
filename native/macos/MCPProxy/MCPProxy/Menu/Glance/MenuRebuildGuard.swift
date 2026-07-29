// MenuRebuildGuard.swift
// MCPProxy
//
// Rebuild policy for the status-bar menu while it is on screen.

import AppKit

/// What a rebuild request is allowed to do at this moment.
enum MenuRebuildDecision: Equatable {
    /// The menu is closed — clear it and build every item from scratch.
    case rebuild
    /// The menu is open and the new glance rows line up 1:1 with the installed
    /// ones — rewrite them in place, add and remove nothing.
    case updateInPlace
    /// The menu is open and the structure changed — do nothing now, remember
    /// that a rebuild is owed, and run it when the menu closes.
    case deferUntilClose
}

/// Tracks whether the status-bar menu is on screen and whether a structural
/// rebuild was suppressed while it was.
///
/// Live SSE rows make the debounced `objectWillChange -> rebuildMenu()` sink
/// fire during active traffic, i.e. potentially while the user is reading the
/// menu. `removeAllItems()` under the cursor — a menu that grows or shrinks
/// mid-read, or an open submenu that collapses — is exactly the irritation the
/// glance design forbids, so structural churn waits for `menuDidClose`.
///
/// **The invariant that makes this safe: `menuWillOpen` rebuilds the menu
/// unconditionally before every single display.** So the guard governs only what
/// happens *between* an open and a close, and anything it suppresses is
/// re-applied before the user can next see the menu. Nothing this guard drops
/// can survive into a display — which is why suppressing non-glance sections
/// too (they are not consulted at all while open) is a freeze rather than a
/// staleness bug, and why the deferred rebuild on `menuDidClose` is a belt to
/// that brace rather than the only thing standing between the user and stale
/// rows. `updateStatusIcon()` deliberately sits outside the guard, so the tray
/// icon remains a live alert channel even while the menu is frozen.
struct MenuRebuildGuard {
    /// True between `menuWillOpen()` and `menuDidClose()`.
    private(set) var isMenuOpen = false

    /// True when a structural rebuild was suppressed while the menu was open.
    private(set) var isDirty = false

    /// Arm the guard. Call AFTER the pre-display rebuild in `menuWillOpen`.
    mutating func menuWillOpen() {
        isMenuOpen = true
        isDirty = false
    }

    /// Decide what a rebuild request may do.
    /// - Parameter structureChanged: true when the new glance rows cannot be
    ///   written over the installed ones (different count or layout).
    mutating func decide(structureChanged: Bool) -> MenuRebuildDecision {
        guard isMenuOpen else { return .rebuild }
        if structureChanged {
            isDirty = true
            return .deferUntilClose
        }
        return .updateInPlace
    }

    /// Disarm the guard. Returns true when a rebuild was deferred and is owed.
    mutating func menuDidClose() -> Bool {
        isMenuOpen = false
        let owed = isDirty
        isDirty = false
        return owed
    }
}

extension MenuRebuildGuard {

    /// The whole open-menu policy in one call: while the menu is on screen ask
    /// `section` to rewrite its rows in place, and treat its refusal — which it
    /// reports having mutated nothing — as the structural change that must wait
    /// for `menuDidClose`.
    ///
    /// A closed menu never reaches the section at all: it is about to be rebuilt
    /// from scratch, so updating rows that are on their way to `removeAllItems`
    /// would be wasted work, and would leave the section's row references
    /// pointing at items no longer in the menu.
    ///
    /// This lives here, rather than inline in `rebuildMenu()`, so the policy can
    /// be tested without an `NSStatusItem`.
    ///
    /// `@MainActor` because this is the one call that mutates menu items already
    /// on screen. `GlanceSection` is deliberately not actor-isolated as a type —
    /// annotating it would force `AppController`, which is not isolated either,
    /// to change well beyond the glance code — so the isolation is pinned to the
    /// two members that actually touch live AppKit state: this seam and
    /// `GlanceSection.updateInPlace`.
    ///
    /// Scope of that guarantee at swift-tools-version 5.9 (minimal concurrency
    /// checking): a direct synchronous call from a nonisolated context is a hard
    /// error, but the same call inside a `@Sendable` closure dispatched to
    /// another queue is only a warning (an error under the Swift 6 language
    /// mode). So this makes the common mistake unrepresentable and the async one
    /// loud, rather than catching every shape of it.
    @MainActor
    mutating func decide(refreshing section: GlanceSection,
                         from state: AppState,
                         now: Date = Date()) -> MenuRebuildDecision {
        guard isMenuOpen else { return .rebuild }
        return decide(structureChanged: !section.updateInPlace(for: state, now: now))
    }
}
