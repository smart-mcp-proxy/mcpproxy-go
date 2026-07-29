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
    mutating func decide(refreshing section: GlanceSection,
                         from state: AppState,
                         now: Date = Date()) -> MenuRebuildDecision {
        guard isMenuOpen else { return .rebuild }
        return decide(structureChanged: !section.updateInPlace(for: state, now: now))
    }
}
