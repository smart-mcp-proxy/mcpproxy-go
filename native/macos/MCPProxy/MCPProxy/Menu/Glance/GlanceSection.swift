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
// @MainActor as a type: every member here either builds live NSMenuItems or
// reads AppState, so main-thread-only is the truth about this component, and
// stating it structurally beats relying on rebuildMenu() happening to be the
// sole route in.
//
// That does mean the initializer is main-actor-only too, and AppController (the
// NSApplicationDelegate that owns this, MCPProxyApp.swift:15) is NOT
// actor-isolated — this SDK does not infer MainActor from NSApplicationDelegate
// conformance — so its `private lazy var glance = ...` stored property would
// otherwise fail with "call to main actor-isolated initializer
// 'init(target:action:)' in a synchronous nonisolated context". The fix is one
// @MainActor on that stored property, at the construction site.
//
// Do NOT "simplify" this by marking the initializer `nonisolated` instead. It
// compiles and passes, but assigning the isolated `clickTarget` from a
// nonisolated init warns "main actor-isolated property 'clickTarget' can not be
// mutated from a nonisolated context; this is an error in the Swift 6 language
// mode" — i.e. it buys a glance-local diff today at the cost of a blocker when
// this package moves to Swift 6. clickTarget cannot be a `let` either: it is
// deliberately `weak` (AppController owns the section strongly), and `weak`
// requires `var`.

import AppKit

@MainActor
final class GlanceSection {

    // MARK: Click routing

    private weak var clickTarget: AnyObject?
    private let clickAction: Selector

    /// Builds the histogram submenu's single custom item from a shaped 24-hour
    /// axis. Injected so submenu-structure tests are independent of how the
    /// chart renders; it defaults to the real chart, so no wiring step can
    /// forget to set it.
    var histogramChartItemFactory: ([HistogramBar]) -> NSMenuItem = ActivityHistogram.chartMenuItem

    // MARK: Configuration

    /// Character budget for a row label before middle truncation kicks in.
    private static let labelBudget = 34

    // MARK: Owned items (kept so rows can be rewritten in place)

    /// A built activity row together with the identity of the record it is
    /// currently showing, so an update can tell "same record, later clock" from
    /// "this row now represents a different call".
    private struct ActivityRow {
        let item: NSMenuItem
        /// The run's identity — `GlanceRun.identity`, the key of its OLDEST
        /// record. Nil until first rendered.
        var runIdentity: String?
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

    /// Held only so ownership of the submenu is explicit; `updateInPlace`
    /// deliberately never touches it (re-creating it would disturb an open
    /// submenu), so nothing reads this back.
    private var histogramItem: NSMenuItem?

    /// The submenu's delegate, which fills the submenu in when it opens.
    /// `NSMenu.delegate` is a WEAK reference, so without this the delegate
    /// would deallocate the moment `items(for:)` returned and the submenu would
    /// silently open empty forever.
    private var histogramDelegate: HistogramSubmenuDelegate?

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
        guard builtVisible else { return [] }

        var items: [NSMenuItem] = []

        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())

        items.append(disabledItem(titled: "Recent"))
        let runs = GlanceSelection.activityRows(from: state.glanceActivity)
        if runs.isEmpty {
            items.append(disabledItem(titled: "No tool calls yet"))
        } else {
            for run in runs {
                var row = ActivityRow(item: actionableItem())
                apply(run, to: &row, now: now)
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
    /// block's structure changed (visibility or row count) — the caller must
    /// then defer a full rebuild until the menu closes rather than growing or
    /// shrinking a menu the user is reading.
    ///
    /// When a row comes to stand for a different record its *entire identity* is
    /// rewritten, not just its title: with a fixed number of rows every new
    /// event shifts which record each row represents, so refreshing only the
    /// text would leave a row whose click still opened the previous record's
    /// session. See `apply(_:to:now:)` for how "different record" is decided.
    ///
    /// The histogram submenu is deliberately not touched, and does not need to
    /// be: its single row is built by `HistogramSubmenuDelegate` when it opens,
    /// reading `AppState` at that moment. Whether the timeline has loaded
    /// therefore changes nothing about the structure built here, which is why
    /// this no longer reports structural when it flips.
    @discardableResult
    func updateInPlace(for state: AppState, now: Date = Date()) -> Bool {
        guard hasBuilt else { return false }
        guard isVisible(for: state) == builtVisible else { return false }
        guard builtVisible else { return true }

        let runs = GlanceSelection.activityRows(from: state.glanceActivity)
        let clients = GlanceSelection.activeClients(from: state.glanceSessions)
        guard runs.count == activityRows.count,
              clients.count == clientRows.count else { return false }

        let summary = summaryTitle(for: state)
        if summaryItem?.title != summary { summaryItem?.title = summary }
        // `zip`, like the sibling loop below: indexing `entries` by
        // `activityRows.indices` reads out of bounds if the count guard above is
        // ever weakened, and zip cannot.
        for (index, run) in zip(activityRows.indices, runs) {
            apply(run, to: &activityRows[index], now: now)
        }
        for (row, session) in zip(clientRows, clients) { apply(session, to: row, now: now) }
        return true
    }

    // MARK: Row rendering

    /// Identity of the run a row is showing: `GlanceRun.identity`, which is the
    /// `recordKey` (request id, never storage id) of the run's OLDEST record.
    ///
    /// Two reasons it is neither the row's index nor its newest record. First, a
    /// row rendered from a live SSE event carries a *provisional* id of the form
    /// `"<request_id>:<type>"`, which the 30-second reconciling poll replaces
    /// with the storage-assigned ULID for the very same call — keying on `id`
    /// would report a wholesale turnover of every row on every poll. Second, a
    /// run grows at the head, so keying on its newest record would make every
    /// additional call of a burst look like a different row.
    ///
    /// The rule itself lives in `GlanceSelection.recordKey`: the row diff, rule
    /// 4's collapse and `AppState`'s poll/SSE merge must all agree on what "the
    /// same call" means, and copies would be free to drift.

    /// Rewrite an activity row so its title, icon, tooltip, accessibility label
    /// and click payload all describe `run`.
    ///
    /// A row shows a *run* — one or more consecutive records of the same
    /// (server, tool, outcome class) — so what it says is assembled from the
    /// run, not from one record: the clock and the click payload come from the
    /// newest record, the mark and the error clause from the worst/newest
    /// failing one, and the `×N` suffix from how many there are.
    ///
    /// When the row has changed run every one of those is written back
    /// unconditionally: with a fixed set of rows each new event shifts which run
    /// a row stands for, and a row that kept the previous run's click payload or
    /// icon would mislead silently. When it is still the same run — the common
    /// case, since a burst extends at the head and the reconcile only re-ids
    /// records — only what actually differs is written, so a menu the user is
    /// reading is not re-laid-out on every tick. Either way the row ends up
    /// fully describing `run`; the distinction is only how much is written.
    private func apply(_ run: GlanceRun, to row: inout ActivityRow, now: Date) {
        let identity = run.identity
        let sameRun = row.runIdentity == identity
        let item = row.item
        let newest = run.newest

        let fullLabel = GlanceFormatting.rowLabel(for: newest)
        let label = GlanceFormatting.middleTruncated(fullLabel, limit: Self.labelBudget)
        // Suffix and spoken count are the same fact twice: "×3" is compact
        // enough for a menu row but reads as a multiplication sign to VoiceOver,
        // so the label spells it out (FR-025).
        let countSuffix = run.count > 1 ? " ×\(run.count)" : ""
        let spokenCount = run.count > 1 ? ", repeated \(run.count) times" : ""
        let age = GlanceFormatting.relativeTime(run.timestamp, now: now)
        let status = run.worstStatus
        let failed = status != "success"
        let detail = failed ? Self.firstClause(of: run.errorMessage) : nil

        let title: String
        let accessibility: String
        if let detail {
            title = "\(label)\(countSuffix) · \(detail) — \(age)"
            accessibility = "\(fullLabel)\(spokenCount), failed: \(detail), \(age) ago"
        } else {
            title = "\(label)\(countSuffix) — \(age)"
            accessibility = "\(fullLabel)\(spokenCount), "
                + "\(Self.outcomeDescription(forStatus: status)), \(age) ago"
        }

        let toolTip: String
        if let message = run.errorMessage, !message.isEmpty {
            toolTip = "\(fullLabel)\n\(message)"
        } else {
            toolTip = fullLabel
        }

        let symbol = GlanceFormatting.statusSymbolName(forStatus: status)

        if !sameRun || item.title != title { item.title = title }
        if !sameRun || item.accessibilityLabel() != accessibility {
            item.setAccessibilityLabel(accessibility)
        }
        if !sameRun || item.toolTip != toolTip { item.toolTip = toolTip }
        if !sameRun || row.symbolName != symbol {
            item.image = Self.statusImage(forStatus: status)
            row.symbolName = symbol
        }
        if !sameRun || (item.representedObject as? String) != newest.sessionId {
            item.representedObject = newest.sessionId
        }

        row.runIdentity = identity
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
    /// Keyed on the status string, not a record: a row stands for a run, and the
    /// outcome it shows is the run's worst (`GlanceRun.worstStatus`).
    static func statusTint(forStatus status: String) -> NSColor {
        switch status {
        case "success":
            return .systemGreen
        case "error":
            return .systemRed
        default:
            return .systemOrange
        }
    }

    static func statusTint(for entry: ActivityEntry) -> NSColor {
        statusTint(forStatus: entry.status)
    }

    /// Spoken outcome for VoiceOver. Three-valued so a call that is still
    /// running is not announced as a failure.
    static func outcomeDescription(forStatus status: String) -> String {
        switch status {
        case "success":
            return "succeeded"
        case "error":
            return "failed"
        default:
            return "in progress"
        }
    }

    static func outcomeDescription(for entry: ActivityEntry) -> String {
        outcomeDescription(forStatus: entry.status)
    }

    /// The row icon: an SF Symbol whose shape carries the outcome, tinted to
    /// carry it a second time.
    ///
    /// The image must be non-template — AppKit recolours a template menu image
    /// to the menu's own text colour, which would silently discard the tint.
    private static func statusImage(forStatus status: String) -> NSImage? {
        symbolImage(named: GlanceFormatting.statusSymbolName(forStatus: status),
                    tint: statusTint(forStatus: status),
                    description: outcomeDescription(forStatus: status))
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

    /// The "Activity (24h)" item and its (initially empty) submenu.
    ///
    /// The submenu's single row is built by its delegate when it opens, not
    /// here: `items(for:)` runs on every `rebuildMenu()` — which itself runs on
    /// every debounced `objectWillChange`, menu open or closed — and building
    /// eagerly would render a SwiftUI Chart on every state change, including
    /// for a menu nobody has opened.
    ///
    /// The submenu carries its OWN delegate rather than the tray menu's. That
    /// is what keeps opening it off `AppController.menuWillOpen`, which
    /// rebuilds the whole menu; a submenu opening under the cursor must not
    /// restructure the menu it hangs from.
    private func makeHistogramItem(for state: AppState) -> NSMenuItem {
        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        let submenu = NSMenu(title: "Activity (24h)")
        // Nothing in here is actionable, and AppKit's automatic enabling runs
        // its own validation at display time. Turning it off makes the row's
        // disabled state ours — and makes what the tests assert the same thing
        // the user sees.
        submenu.autoenablesItems = false

        let delegate = HistogramSubmenuDelegate(appState: state,
                                                chartItemFactory: histogramChartItemFactory)
        histogramDelegate = delegate
        submenu.delegate = delegate

        item.submenu = submenu
        return item
    }

    // MARK: Header

    /// The header line, plus an admission when the numbers in it have stopped
    /// arriving.
    ///
    /// Without the marker a dead core is indistinguishable from a healthy idle
    /// one, and worse than frozen: the refresh loop bumps `activityVersion`
    /// whether or not any fetch succeeded, so the rows re-render with a fresh
    /// clock every 30 seconds and the block ticks along presenting a previous
    /// core's numbers as live. A frozen menu is a hint that something is wrong;
    /// a ticking one is not.
    private func summaryTitle(for state: AppState) -> String {
        var parts: [String] = []
        if let calls = state.callsThisHour {
            parts.append(calls == 1 ? "1 call this hour" : "\(calls) calls this hour")
        }
        let clients = GlanceSelection.activeClients(from: state.glanceSessions, limit: Int.max).count
        parts.append(clients == 1 ? "1 client" : "\(clients) clients")
        if state.glanceStale { parts.append("not updating") }
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
