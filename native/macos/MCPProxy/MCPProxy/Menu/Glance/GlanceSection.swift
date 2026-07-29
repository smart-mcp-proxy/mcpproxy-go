// GlanceSection.swift
// MCPProxy
//
// Builds the "glance" block at the top of the tray menu: a one-line summary,
// the most recent qualifying tool calls, the active MCP clients, and the
// 24h histogram submenu.
//
// Every text row is a plain NSMenuItem. Custom (view-backed) menu items receive
// mouse events but NOT keyboard events, so building the rows as hosted SwiftUI
// would silently cost keyboard navigation and VoiceOver. Only the histogram —
// which genuinely needs drawing — is view-backed, and it lives alone inside its
// own submenu.
//
// This component never builds a Web UI URL. It is handed only AppState, whose
// webUIBaseURL is scheme/host/port by design, while the API key lives on the
// core manager. Rows therefore carry a target/action pair plus a
// representedObject holding the record's session id, and the app delegate opens
// the authenticated URL through the same path as every other menu action.
//
// Deliberately NOT @MainActor: AppController (the NSApplicationDelegate that
// will call this, MCPProxyApp.swift:15) is not actor-isolated, and this SDK does
// not infer MainActor from NSApplicationDelegate conformance, so annotating it
// would make rebuildMenu() fail to compile.

import AppKit

final class GlanceSection {

    // MARK: Click routing

    private weak var clickTarget: AnyObject?
    private let clickAction: Selector

    /// Builds the view for the histogram submenu's single custom item. While
    /// this is nil the submenu falls back to a plain text line, which keeps the
    /// component usable and testable without SwiftUI Charts.
    var histogramViewBuilder: (([UsageBucket]) -> NSView)?

    // MARK: Configuration

    /// Character budget for a row label before middle truncation kicks in.
    private static let labelBudget = 34

    // MARK: Owned items (kept so rows can be rewritten in place)

    /// A built activity row together with the identity of the record it is
    /// currently showing, so an update can tell "same record, later clock" from
    /// "this row now represents a different call".
    private struct ActivityRow {
        let item: NSMenuItem
        /// The record's key — see `recordKey(for:)`. Nil until first rendered.
        var recordKey: String?
        /// The SF Symbol currently installed, so the icon is rebuilt only when
        /// the glyph really changes.
        var symbolName: String?
    }

    private var summaryItem: NSMenuItem?
    private var activityRows: [ActivityRow] = []
    private var clientRows: [NSMenuItem] = []

    /// Snapshot of the structure the current items were built from, so an
    /// in-place update can detect that a full rebuild is required instead.
    private var hasBuilt = false
    private var builtVisible = false
    private var builtWithTimeline = false

    /// Held only so ownership of the submenu is explicit; `updateInPlace`
    /// deliberately never touches it (re-creating it would disturb an open
    /// submenu), so nothing reads this back.
    private var histogramItem: NSMenuItem?

    init(target: AnyObject?, action: Selector) {
        self.clickTarget = target
        self.clickAction = action
    }

    // MARK: Building

    /// Whether the glance block should appear at all. When the core is stopped
    /// or disconnected the block is hidden entirely, rather than presenting the
    /// previous core's numbers as if they were live.
    func isVisible(for state: AppState) -> Bool {
        state.isConnected && !state.isStopped
    }

    /// Build the whole block, ordered top to bottom and ending with a separator
    /// so the caller can splice it into the menu in one go. Returns an empty
    /// array when the block is hidden.
    func items(for state: AppState, now: Date = Date()) -> [NSMenuItem] {
        summaryItem = nil
        activityRows = []
        clientRows = []
        histogramItem = nil
        hasBuilt = true
        builtVisible = isVisible(for: state)
        builtWithTimeline = state.usageTimeline != nil
        guard builtVisible else { return [] }

        var items: [NSMenuItem] = []

        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())

        items.append(disabledItem(titled: "Recent"))
        let entries = GlanceSelection.activityRows(from: state.glanceActivity)
        if entries.isEmpty {
            items.append(disabledItem(titled: "No tool calls yet"))
        } else {
            for entry in entries {
                var row = ActivityRow(item: actionableItem())
                apply(entry, to: &row, now: now)
                activityRows.append(row)
                items.append(row.item)
            }
        }

        let openActivity = actionableItem()
        openActivity.title = "Open Activity…"
        openActivity.image = NSImage(systemSymbolName: "list.bullet.rectangle",
                                     accessibilityDescription: "activity log")
        items.append(openActivity)
        items.append(.separator())

        items.append(disabledItem(titled: "Clients"))
        let clients = GlanceSelection.activeClients(from: state.glanceSessions)
        if clients.isEmpty {
            items.append(disabledItem(titled: "No connected clients"))
        } else {
            for session in clients {
                let row = actionableItem()
                apply(session, to: row, now: now)
                clientRows.append(row)
                items.append(row)
            }
        }
        items.append(.separator())

        let histogram = makeHistogramItem(for: state)
        histogramItem = histogram
        items.append(histogram)
        items.append(.separator())

        return items
    }

    // MARK: In-place updates

    /// Rewrite the existing rows from `state` without restructuring the menu.
    ///
    /// Returns `true` when every row was updated in place, and `false` when the
    /// block's structure changed (visibility, row count, or histogram
    /// loaded-ness) — the caller must then defer a full rebuild until the menu
    /// closes rather than growing or shrinking a menu the user is reading.
    ///
    /// When a row comes to stand for a different record its *entire identity* is
    /// rewritten, not just its title: with a fixed number of rows every new
    /// event shifts which record each row represents, so refreshing only the
    /// text would leave a row whose click still opened the previous record's
    /// session. See `apply(_:to:now:)` for how "different record" is decided.
    ///
    /// The histogram submenu is deliberately not touched — re-creating it would
    /// disturb an open submenu — so a change in its loaded-ness reports
    /// structural instead.
    @discardableResult
    func updateInPlace(for state: AppState, now: Date = Date()) -> Bool {
        guard hasBuilt else { return false }
        guard isVisible(for: state) == builtVisible else { return false }
        guard builtVisible else { return true }
        guard (state.usageTimeline != nil) == builtWithTimeline else { return false }

        let entries = GlanceSelection.activityRows(from: state.glanceActivity)
        let clients = GlanceSelection.activeClients(from: state.glanceSessions)
        guard entries.count == activityRows.count,
              clients.count == clientRows.count else { return false }

        let summary = summaryTitle(for: state)
        if summaryItem?.title != summary { summaryItem?.title = summary }
        for index in activityRows.indices { apply(entries[index], to: &activityRows[index], now: now) }
        for (row, session) in zip(clientRows, clients) { apply(session, to: row, now: now) }
        return true
    }

    // MARK: Row rendering

    /// Identity of the record a row is showing: its `requestId`, never its `id`.
    ///
    /// A row rendered from a live SSE event carries a *provisional* id of the
    /// form `"<request_id>:<type>"`, which the 30-second reconciling poll
    /// replaces with the storage-assigned ULID for the very same call. Keying on
    /// `id` would therefore report a wholesale turnover of every row on every
    /// poll, needlessly rewriting five rows' icons and click payloads each time.
    /// `requestId` is identical on both sides, and is already what rule 4
    /// (`GlanceSelection.collapseByRequestID`) groups on. Records with no
    /// request id are never collapsed, so their `id` is a safe fallback key.
    private static func recordKey(for entry: ActivityEntry) -> String {
        if let requestId = entry.requestId, !requestId.isEmpty { return requestId }
        return entry.id
    }

    /// Rewrite an activity row so its title, icon, tooltip, accessibility label
    /// and click payload all describe `entry`.
    ///
    /// When the row has changed record every one of those is written back
    /// unconditionally: with a fixed set of rows each new event shifts which
    /// record a row stands for, and a row that kept the previous record's click
    /// payload or icon would mislead silently. When it is still the same record
    /// — the common case, since the reconcile only re-ids it — only what
    /// actually differs is written, so a menu the user is reading is not
    /// re-laid-out on every tick. Either way the row ends up fully describing
    /// `entry`; the distinction is only how much is written to get there.
    private func apply(_ entry: ActivityEntry, to row: inout ActivityRow, now: Date) {
        let key = Self.recordKey(for: entry)
        let sameRecord = row.recordKey == key
        let item = row.item

        let fullLabel = GlanceFormatting.rowLabel(for: entry)
        let label = GlanceFormatting.middleTruncated(fullLabel, limit: Self.labelBudget)
        let age = GlanceFormatting.relativeTime(entry.timestamp, now: now)
        let failed = entry.status != "success"
        let detail = failed ? Self.firstClause(of: entry.errorMessage) : nil

        let title: String
        let accessibility: String
        if let detail {
            title = "\(label) · \(detail) — \(age)"
            accessibility = "\(fullLabel), failed: \(detail), \(age) ago"
        } else {
            title = "\(label) — \(age)"
            accessibility = "\(fullLabel), \(Self.outcomeDescription(for: entry)), \(age) ago"
        }

        let toolTip: String
        if let message = entry.errorMessage, !message.isEmpty {
            toolTip = "\(fullLabel)\n\(message)"
        } else {
            toolTip = fullLabel
        }

        let symbol = GlanceFormatting.statusSymbolName(for: entry)

        if !sameRecord || item.title != title { item.title = title }
        if !sameRecord || item.accessibilityLabel() != accessibility {
            item.setAccessibilityLabel(accessibility)
        }
        if !sameRecord || item.toolTip != toolTip { item.toolTip = toolTip }
        if !sameRecord || row.symbolName != symbol {
            item.image = Self.statusImage(for: entry)
            row.symbolName = symbol
        }
        if !sameRecord || (item.representedObject as? String) != entry.sessionId {
            item.representedObject = entry.sessionId
        }

        row.recordKey = key
    }

    /// First clause of an error message — everything up to the first newline,
    /// period or colon — so a multi-sentence backend error still fits one row.
    /// The full message stays in the tooltip.
    static func firstClause(of message: String?) -> String? {
        guard let message else { return nil }
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let head = trimmed.components(separatedBy: CharacterSet(charactersIn: ".:\n")).first ?? trimmed
        let clause = head.trimmingCharacters(in: .whitespaces)
        return clause.isEmpty ? trimmed : clause
    }

    // MARK: Status iconography

    /// Tint for an activity record's outcome.
    ///
    /// Colour is the *second* channel, never the only one: `GlanceFormatting`
    /// already gives the three outcomes three distinct glyphs, and
    /// `outcomeDescription` gives VoiceOver a third. A red/green pair alone
    /// would be invisible to the ~8% of men with a red-green deficiency, and to
    /// anyone running the display in greyscale.
    ///
    /// This lives here rather than in `GlanceFormatting` because that file is
    /// deliberately AppKit-free (`import Foundation` only) and `NSColor` is not.
    static func statusTint(for entry: ActivityEntry) -> NSColor {
        switch entry.status {
        case "success":
            return .systemGreen
        case "error":
            return .systemRed
        default:
            return .systemOrange
        }
    }

    /// Spoken outcome for VoiceOver. Three-valued so a call that is still
    /// running is not announced as a failure.
    static func outcomeDescription(for entry: ActivityEntry) -> String {
        switch entry.status {
        case "success":
            return "succeeded"
        case "error":
            return "failed"
        default:
            return "in progress"
        }
    }

    /// The row icon: an SF Symbol whose shape carries the outcome, tinted to
    /// carry it a second time.
    ///
    /// The image must be non-template — AppKit recolours a template menu image
    /// to the menu's own text colour, which would silently discard the tint.
    private static func statusImage(for entry: ActivityEntry) -> NSImage? {
        symbolImage(named: GlanceFormatting.statusSymbolName(for: entry),
                    tint: statusTint(for: entry),
                    description: outcomeDescription(for: entry))
    }

    private static func symbolImage(named name: String, tint: NSColor, description: String) -> NSImage? {
        guard let base = NSImage(systemSymbolName: name, accessibilityDescription: description) else {
            return nil
        }
        let tinted = base.withSymbolConfiguration(NSImage.SymbolConfiguration(paletteColors: [tint])) ?? base
        tinted.isTemplate = false
        tinted.accessibilityDescription = description
        return tinted
    }

    /// The client-row bullet. Every client row is an *active* session, so this
    /// glyph is a constant — built once rather than re-tinted per row per poll.
    private static let connectedDot = symbolImage(named: "circle.fill",
                                                  tint: .systemGreen,
                                                  description: "connected")

    /// Rewrite a client row so it fully describes `session`.
    ///
    /// Unlike an activity row this needs no `recordKey`: a session id does not
    /// churn, so a row never comes to stand for a different client without the
    /// row count changing (which `updateInPlace` already reports as structural).
    /// The write guards are still needed, and for a different reason — cost.
    /// `updateInPlace` runs on nearly every 30s poll for a busy proxy, under a
    /// menu the user may have open, where every write is a re-layout; so only
    /// what actually differs is written.
    private func apply(_ session: APIClient.MCPSession, to item: NSMenuItem, now: Date) {
        let name = session.clientName.flatMap { $0.isEmpty ? nil : $0 } ?? "Unknown client"
        let calls = session.toolCallCount ?? 0
        let callText = calls == 1 ? "1 call" : "\(calls) calls"
        let age = session.lastActivity.map { GlanceFormatting.relativeTime($0, now: now) }

        let title: String
        let accessibility: String
        if let age {
            title = "\(name) — \(callText) · \(age)"
            accessibility = "\(name), \(callText), last active \(age) ago"
        } else {
            title = "\(name) — \(callText)"
            accessibility = "\(name), \(callText)"
        }

        let toolTip: String
        if let version = session.clientVersion, !version.isEmpty {
            toolTip = "\(name) \(version)"
        } else {
            toolTip = name
        }

        if item.title != title { item.title = title }
        if item.accessibilityLabel() != accessibility { item.setAccessibilityLabel(accessibility) }
        if item.toolTip != toolTip { item.toolTip = toolTip }
        if item.image !== Self.connectedDot { item.image = Self.connectedDot }
        if (item.representedObject as? String) != session.id { item.representedObject = session.id }
    }

    // MARK: Histogram

    private func makeHistogramItem(for state: AppState) -> NSMenuItem {
        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        let submenu = NSMenu()

        if let timeline = state.usageTimeline {
            if let builder = histogramViewBuilder {
                let chart = NSMenuItem(title: "", action: nil, keyEquivalent: "")
                chart.view = builder(timeline)
                submenu.addItem(chart)
            } else {
                let calls = timeline.reduce(0) { $0 + $1.calls }
                let errors = timeline.reduce(0) { $0 + $1.errors }
                let callText = calls == 1 ? "1 call" : "\(calls) calls"
                let errorText = errors == 1 ? "1 error" : "\(errors) errors"
                submenu.addItem(disabledItem(titled: "\(callText) · \(errorText) (24h)"))
            }
        } else {
            submenu.addItem(disabledItem(titled: "Loading…"))
        }

        item.submenu = submenu
        return item
    }

    // MARK: Header

    private func summaryTitle(for state: AppState) -> String {
        var parts: [String] = []
        if let calls = state.callsThisHour {
            parts.append(calls == 1 ? "1 call this hour" : "\(calls) calls this hour")
        }
        let clients = GlanceSelection.activeClients(from: state.glanceSessions, limit: Int.max).count
        parts.append(clients == 1 ? "1 client" : "\(clients) clients")
        return parts.joined(separator: " · ")
    }

    // MARK: Item factories

    private func disabledItem(titled title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }

    private func actionableItem() -> NSMenuItem {
        let item = NSMenuItem(title: "", action: clickAction, keyEquivalent: "")
        item.target = clickTarget
        return item
    }
}
