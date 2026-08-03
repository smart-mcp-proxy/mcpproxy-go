// GlanceSection.swift
// MCPProxy
//
// Builds the "glance" block at the top of the tray menu: a one-line summary,
// the inline 24h histogram, the most recent qualifying tool calls, and the
// active MCP clients.
//
// Every text row is a plain NSMenuItem. Custom (view-backed) menu items receive
// mouse events but NOT keyboard events, so building the rows as hosted SwiftUI
// would silently cost keyboard navigation and VoiceOver. Only the histogram —
// which genuinely needs drawing — is view-backed; it renders inline, directly
// under the summary line, so the day's shape is visible the moment the menu
// opens (it used to hide in an "Activity (24h)" submenu, which made the
// overview the only row that required a second navigation step).
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

    /// Builds the inline chart item from a shaped 24-hour axis. Injected so
    /// block-structure tests are independent of how the chart renders; it
    /// defaults to the real chart, so no wiring step can forget to set it.
    var histogramChartItemFactory: ([HistogramBar]) -> NSMenuItem = ActivityHistogram.chartMenuItem

    // MARK: Configuration

    /// Character budget for a row label before middle truncation kicks in.
    ///
    /// Sized so `label ×N — age` sits within the chart block's width — the
    /// chart, not the longest tool name, is what bounds the menu. The age —
    /// the shortest and most perishable part of the row — is never cut at all.
    private static let labelBudget = 30

    /// Whether rows may carry a second line.
    ///
    /// `NSMenuItem.subtitle` is macOS 14.4+, while the app's deployment floor is
    /// macOS 13, so below 14.4 a row is single-line and its reason lives in the
    /// tooltip and the accessibility label only (FR-005, documented degradation).
    ///
    /// It is a settable property rather than a bare `#available` check at the
    /// point of use so both branches are testable on one host: the fallback is
    /// the branch no CI machine runs, and an untested fallback is how a reason
    /// silently disappears on an older Mac.
    var supportsRowSubtitles: Bool = GlanceSection.systemSupportsRowSubtitles

    /// Whether the *running* system has the subtitle mechanism.
    static var systemSupportsRowSubtitles: Bool {
        if #available(macOS 14.4, *) { return true }
        return false
    }

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
        /// The subtitle currently installed, or nil when the row is single-line.
        /// Kept here rather than read back off the item because reading
        /// `NSMenuItem.subtitle` needs an availability gate, and because the
        /// structural preflight has to know a row's line count without touching
        /// the item at all.
        var subtitleText: String?
    }

    private var summaryItem: NSMenuItem?
    private var activityRows: [ActivityRow] = []
    private var clientRows: [NSMenuItem] = []

    /// The "+N more" row under a truncated Clients list, or nil when every
    /// client has a row of its own.
    ///
    /// Owned like the rows are, because N moves without the row COUNT moving:
    /// a twelfth client beyond a cap of five changes nothing structural, and a
    /// row left saying "+6 more" under a header that has already counted twelve
    /// is the very disagreement this row exists to end (GH #934).
    private var clientOverflowItem: NSMenuItem?

    /// Snapshot of the structure the current items were built from, so an
    /// in-place update can detect that a full rebuild is required instead.
    private var hasBuilt = false
    private var builtVisible = false

    /// What kind of row the histogram block was last built with. The three
    /// text kinds share one-line geometry and rewrite each other in place; a
    /// text ↔ chart change is structural (a text row and a 96 pt chart have
    /// different heights).
    private enum HistogramRowKind: Equatable { case loading, failed, idle, chart }
    private var builtHistogramKind: HistogramRowKind?

    /// The histogram row currently installed, and — when it is the chart — the
    /// shaped bars it renders. The pair is the cache that keeps `items(for:)`
    /// from re-rendering a SwiftUI chart on every debounced rebuild of a menu
    /// nobody has opened: same bars, same item, no work.
    private var histogramRow: NSMenuItem?
    private var builtHistogramBars: [HistogramBar]?
    /// Time zone the cached chart's VoiceOver summary was formatted in. The
    /// bars are UTC-keyed and survive a zone change, but "Busiest hour 14:00"
    /// does not — a zone change invalidates the cache even with equal bars.
    private var builtHistogramTimeZoneID: String?

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
        clientOverflowItem = nil
        hasBuilt = true
        builtVisible = isVisible(for: state)
        guard builtVisible else { return [] }

        var items: [NSMenuItem] = []

        let summary = disabledItem(titled: summaryTitle(for: state, now: now))
        summaryItem = summary
        items.append(summary)

        // The day before the minute (FR-021). The histogram answers "what has
        // been happening?" in one glyph, so it belongs beside the summary line
        // it illustrates, above the separator: summary and shape are one block,
        // the rows below are another. It renders inline — no submenu — so the
        // shape is on screen the moment the menu opens.
        items.append(histogramRowItem(for: state, now: now))
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
        let presence = Self.clientList(for: state, now: now)
        if presence.rows.isEmpty {
            items.append(disabledItem(titled: Self.noClientsTitle))
        } else {
            for client in presence.rows {
                let row = actionableItem()
                apply(client, to: row, now: now)
                clientRows.append(row)
                items.append(row)
            }
            if presence.hidden > 0 {
                let overflow = disabledItem(titled: Self.overflowTitle(presence.hidden))
                clientOverflowItem = overflow
                items.append(overflow)
            }
        }
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
    /// The histogram never freezes the block: the timeline loading while the
    /// menu is open must not cost the user live rows for the whole menu
    /// session (see `testTheTimelineArrivingWhileTheMenuIsOpenKeepsRowsUpdating`).
    /// Its two TEXT kinds share one-line geometry and rewrite each other in
    /// place; a text ↔ chart flip DOES change the row's height, so that row
    /// alone stays as built until the next rebuild — `menuWillOpen` runs one
    /// before every display — while everything else keeps updating.
    @discardableResult
    func updateInPlace(for state: AppState, now: Date = Date()) -> Bool {
        guard hasBuilt else { return false }
        guard isVisible(for: state) == builtVisible else { return false }
        guard builtVisible else { return true }

        let runs = GlanceSelection.activityRows(from: state.glanceActivity)
        let presence = Self.clientList(for: state, now: now)
        let clients = presence.rows
        guard runs.count == activityRows.count,
              clients.count == clientRows.count else { return false }
        // Gaining or losing the "+N more" row is gaining or losing a row, which
        // resizes an open menu exactly as an extra client does — structural, so
        // it waits for close (FR-023). A change to N alone is not.
        guard (presence.hidden > 0) == (clientOverflowItem != nil) else { return false }

        // Preflight, before a single write: a row that gains or loses its
        // reason gains or loses a LINE, which resizes the menu just as surely as
        // adding a row does — so it is structural and waits for close (FR-023).
        //
        // It is a separate pass on purpose. Deciding per row inside the write
        // loop would refuse only after having already rewritten the summary and
        // every earlier row, which is a half-updated menu on screen: the exact
        // outcome deferring exists to avoid. Presence is what matters, not text
        // — a reason whose wording changed still occupies one line and is an
        // ordinary in-place rewrite.
        for (index, run) in zip(activityRows.indices, runs)
        where (subtitleText(for: run) == nil) != (activityRows[index].subtitleText == nil) {
            return false
        }

        let summary = summaryTitle(for: state, now: now)
        if summaryItem?.title != summary { summaryItem?.title = summary }
        // `zip`, like the sibling loop below: indexing `entries` by
        // `activityRows.indices` reads out of bounds if the count guard above is
        // ever weakened, and zip cannot.
        for (index, run) in zip(activityRows.indices, runs) {
            apply(run, to: &activityRows[index], now: now)
        }
        for (row, client) in zip(clientRows, clients) { apply(client, to: row, now: now) }
        if let clientOverflowItem {
            let title = Self.overflowTitle(presence.hidden)
            if clientOverflowItem.title != title { clientOverflowItem.title = title }
        }
        refreshHistogramRow(with: ActivityHistogram.state(timeline: state.usageTimeline,
                                                          errorMessage: state.usageError,
                                                          now: now))
        return true
    }

    /// The Clients section's rows and how many clients they leave out.
    ///
    /// Both numbers come from ONE deduplicated set, and the header's own
    /// segment (`AppState.glanceClientSummary`) counts over the same rule — so
    /// "8 active · 3 idle" above five rows is always accompanied by the row
    /// that accounts for the difference.
    private static func clientList(
        for state: AppState, now: Date
    ) -> (rows: [ClientPresenceRow], hidden: Int) {
        let all = GlancePresence.clients(from: state.glanceSessions, now: now, limit: Int.max)
        let shown = Array(all.prefix(GlancePresence.rowLimit))
        return (shown, all.count - shown.count)
    }

    private static func overflowTitle(_ hidden: Int) -> String { "+\(hidden) more" }

    /// The overflow row's current text, for tests that pin it after an in-place
    /// update — the item itself is private, and reaching into the menu to find
    /// it by title would assert nothing about which row was rewritten.
    var clientOverflowTitleForTesting: String? { clientOverflowItem?.title }

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
        let clause = failed ? Self.firstClause(of: run.errorMessage) : nil

        // One fact per line bounds the menu (compact revision of FR-011a): the
        // title is always `label ×N — age`, and on a failed row the error
        // clause takes the second line — the failure mark already flags the row
        // — while the reason retreats to the tooltip. Error prose on the title
        // line was what made the whole menu wider than the chart it opens with.
        //
        // Pre-14.4 there is no second line, so the clause rejoins the title
        // under its own, smaller budget (the documented FR-005 degradation).
        let reason = run.displayReason
        let subtitle = subtitleText(for: run)

        let title: String
        var accessibility: String
        if let clause, !supportsRowSubtitles {
            let detail = GlanceFormatting.tailTruncated(clause, limit: GlanceFormatting.errorClauseBudget)
            title = "\(label)\(countSuffix) · \(detail) — \(age)"
        } else {
            title = "\(label)\(countSuffix) — \(age)"
        }
        // Spoken in full, and spoken on every macOS version — the lines are
        // where facts are *seen*, not where they live (FR-006, FR-025).
        if let clause {
            accessibility = "\(fullLabel)\(spokenCount), failed: \(clause), \(age) ago"
        } else {
            accessibility = "\(fullLabel)\(spokenCount), "
                + "\(Self.outcomeDescription(forStatus: status)), \(age) ago"
        }
        if let reason { accessibility += ", reason: \(reason)" }

        // The tooltip is the row without any budget at all: full label, full
        // reason, full error message.
        var toolTipLines = [fullLabel]
        if let reason { toolTipLines.append(reason) }
        if let message = run.errorMessage, !message.isEmpty { toolTipLines.append(message) }
        let toolTip = toolTipLines.joined(separator: "\n")

        let symbol = GlanceFormatting.statusSymbolName(forStatus: status)

        if !sameRun || item.title != title { item.title = title }
        if !sameRun || row.subtitleText != subtitle {
            Self.setSubtitle(subtitle, on: item)
            row.subtitleText = subtitle
        }
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

    /// The text a row's second line would show, or nil when it has none.
    ///
    /// On a failed row the line belongs to the error clause — "how it went"
    /// outranks "why it was attempted" once something is wrong, and the full
    /// reason stays in the tooltip. Otherwise it is the reason (FR-006/FR-007).
    /// Nil on systems without the subtitle mechanism (FR-005).
    private func subtitleText(for run: GlanceRun) -> String? {
        guard supportsRowSubtitles else { return nil }
        if run.worstStatus != "success", let clause = Self.firstClause(of: run.errorMessage) {
            return GlanceFormatting.tailTruncated(clause, limit: GlanceFormatting.reasonBudget)
        }
        guard let reason = run.displayReason else { return nil }
        return GlanceFormatting.tailTruncated(reason, limit: GlanceFormatting.reasonBudget)
    }

    /// Install (or clear) a row's second line. The availability gate lives here
    /// alone, so every caller reasons in terms of `supportsRowSubtitles`.
    private static func setSubtitle(_ text: String?, on item: NSMenuItem) {
        if #available(macOS 14.4, *) { item.subtitle = text }
    }

    /// First clause of an error message — everything up to the first clause
    /// BOUNDARY — so a multi-sentence backend error still fits one row. The full
    /// message stays in the tooltip.
    ///
    /// A period or colon is a boundary only when whitespace (or the end of the
    /// message) follows it (GH #934). Splitting on a bare `.` or `:` cut inside
    /// the two things mcpproxy errors are most likely to contain: a `server:tool`
    /// identifier and a host:port. `invalid arguments for
    /// memory:create_entities: at '/entities': …` rendered as "invalid arguments
    /// for memory", which reads as though the server named `memory` were at
    /// fault — and that is the common shape of these messages, not an edge case.
    static func firstClause(of message: String?) -> String? {
        guard let message else { return nil }
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let head = clauseBoundary(in: trimmed).map { trimmed[trimmed.startIndex..<$0] }
            ?? Substring(trimmed)
        let clause = head.trimmingCharacters(in: .whitespaces)
        return clause.isEmpty ? trimmed : clause
    }

    /// Index of the first character that ends the leading clause, or nil when
    /// the whole message is one clause. `text` must already be trimmed, so a
    /// trailing separator is genuinely final rather than the start of "…. ".
    private static func clauseBoundary(in text: String) -> String.Index? {
        var index = text.startIndex
        while index < text.endIndex {
            let character = text[index]
            if character.isNewline { return index }
            if character == "." || character == ":" {
                let next = text.index(after: index)
                // Nothing after it, or space after it: a separator. Anything
                // else and it is part of a token — `memory:create_entities`,
                // `127.0.0.1`, `a.txt` — which is the whole point of this rule.
                if next == text.endIndex || text[next].isWhitespace { return index }
            }
            index = text.index(after: index)
        }
        return nil
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
        case _ where ActivityEntry.blockingDecisions.contains(status):
            // Spoken separately from "failed": the call did not go wrong, the
            // proxy stopped it — and success is announced even though it is no
            // longer drawn (FR-010), so VoiceOver loses nothing to the quiet.
            return "blocked"
        default:
            return "in progress"
        }
    }

    static func outcomeDescription(for entry: ActivityEntry) -> String {
        outcomeDescription(forStatus: entry.status)
    }

    /// The row icon: an SF Symbol whose shape carries the outcome, tinted to
    /// carry it a second time — or nothing at all, when the call succeeded.
    ///
    /// Success is deliberately unmarked (FR-010). In a real 6-week export 1,480
    /// of 1,564 outcome-bearing events succeeded, so a green tick appeared on
    /// 95% of rows and told the user nothing; what it did do was bury the 32
    /// errors and 52 blocks among identical-looking rows. A mark now means
    /// "look at this".
    ///
    /// The image must be non-template — AppKit recolours a template menu image
    /// to the menu's own text colour, which would silently discard the tint.
    private static func statusImage(forStatus status: String) -> NSImage? {
        guard status != "success" else { return nil }
        return symbolImage(named: GlanceFormatting.statusSymbolName(forStatus: status),
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

    // MARK: Presence iconography

    /// Text shown when nothing at all falls inside the presence lookback
    /// (FR-020). It says *recent*, not *connected*, because that is the claim
    /// the section can actually stand behind: with a stateless transport there
    /// is no such thing as a currently-connected client, and the old wording
    /// announced "nothing is connected" every time the last session timed out.
    static let noClientsTitle = "No recent clients"

    /// The presence indicator's glyph.
    ///
    /// Three distinct SHAPES, not one shape in three colours (FR-018): a filled
    /// dot for a client working now, a half-filled one for a client gone quiet,
    /// a hollow ring for one last heard from hours ago. Colour repeats the
    /// distinction rather than carrying it, so the states survive greyscale and
    /// a red-green deficiency. (data-model.md sketches idle as a *filled grey*
    /// dot; that separates active from idle by colour alone, which FR-018
    /// forbids, so the fill differs too.)
    static func presenceSymbolName(for presence: ClientPresence) -> String {
        switch presence {
        case .active: return "circle.fill"
        case .idle: return "circle.lefthalf.filled"
        case .seen: return "circle"
        }
    }

    static func presenceTint(for presence: ClientPresence) -> NSColor {
        switch presence {
        case .active: return .systemGreen
        case .idle: return .systemGray
        case .seen: return .tertiaryLabelColor
        }
    }

    /// One image per state, built once.
    ///
    /// `updateInPlace` runs on nearly every 30s poll, under a menu the user may
    /// have open; re-tinting a symbol per row per poll would allocate a fresh
    /// image every time and force a re-layout for a glyph that never changed.
    private static let presenceDots: [ClientPresence: NSImage] = {
        var dots: [ClientPresence: NSImage] = [:]
        for presence in ClientPresence.allCases {
            dots[presence] = symbolImage(named: presenceSymbolName(for: presence),
                                         tint: presenceTint(for: presence),
                                         description: presence.rawValue)
        }
        return dots
    }()

    /// Rewrite a client row so it fully describes `client`.
    ///
    /// Unlike an activity row this needs no `recordKey`: rows are ordered by
    /// activity and a row only comes to stand for a different client when the
    /// row count changes, which `updateInPlace` already reports as structural.
    /// The write guards are still needed, and for a different reason — cost.
    /// `updateInPlace` runs on nearly every 30s poll for a busy proxy, under a
    /// menu the user may have open, where every write is a re-layout; so only
    /// what actually differs is written.
    ///
    /// The age is shown only once the client has gone quiet (FR-018). On an
    /// active row it would be noise — "active" already means "within the last
    /// few minutes" — while on an idle or seen row it is the whole point: "idle
    /// 20m" is what turns a vanished client into a legible one.
    private func apply(_ client: ClientPresenceRow, to item: NSMenuItem, now: Date) {
        let calls = client.session.toolCallCount ?? 0
        let callText = calls == 1 ? "1 call" : "\(calls) calls"
        let age = GlanceFormatting.compactAge(client.age)

        let title: String
        switch client.state {
        case .active:
            title = "\(client.name) — \(callText)"
        case .idle, .seen:
            title = "\(client.name) — \(callText) · \(client.state.rawValue) \(age)"
        }
        // Spoken with the state named and the age always included: VoiceOver has
        // no indicator to read, so what the glyph carries has to be said.
        let accessibility = "\(client.name), \(callText), \(client.state.rawValue), "
            + "last active \(age) ago"

        let toolTip = client.version.map { "\(client.name) \($0)" } ?? client.name
        let dot = Self.presenceDots[client.state] ?? nil

        if item.title != title { item.title = title }
        if item.accessibilityLabel() != accessibility { item.setAccessibilityLabel(accessibility) }
        if item.toolTip != toolTip { item.toolTip = toolTip }
        if item.image !== dot { item.image = dot }
        if (item.representedObject as? String) != client.session.id {
            item.representedObject = client.session.id
        }
    }

    // MARK: Histogram

    /// The inline histogram row: the chart when the timeline has loaded, a
    /// muted placeholder while it is loading or after the fetch failed.
    ///
    /// The chart reads `AppState` and nothing else — building this row
    /// performs no network request (spec 048 invariant). It is view-backed and
    /// eagerly built — but `items(for:)` runs on every `rebuildMenu()`, which
    /// itself runs on every debounced `objectWillChange`, menu open or closed. The
    /// bars cache is what keeps that affordable: the chart item is rebuilt only
    /// when the shaped 24-hour axis actually changed (`HistogramBar` is
    /// Equatable precisely so that is cheap to decide), and every other rebuild
    /// hands back the item it already has. `rebuildMenu` reuses one `NSMenu`
    /// via `removeAllItems()`, so re-adding the cached item is safe.
    private func histogramRowItem(for state: AppState, now: Date) -> NSMenuItem {
        let histogramState = ActivityHistogram.state(timeline: state.usageTimeline,
                                                     errorMessage: state.usageError,
                                                     now: now)
        switch histogramState {
        case .loading:
            builtHistogramKind = .loading
            builtHistogramBars = nil
            let item = Self.mutedItem("Activity (24h) — loading…")
            histogramRow = item
            return item
        case .failed(let message):
            builtHistogramKind = .failed
            builtHistogramBars = nil
            let item = Self.mutedItem("Activity (24h) unavailable")
            item.toolTip = message
            histogramRow = item
            return item
        case .loaded(let bars):
            // A loaded-but-idle day is a sentence, not a chart: 24 empty bars
            // read as a broken widget, while the words say exactly what the
            // flat axis would have implied.
            if bars.allSatisfy({ $0.total == 0 }) {
                builtHistogramKind = .idle
                builtHistogramBars = nil
                let item = Self.mutedItem(Self.idleHistogramTitle)
                histogramRow = item
                return item
            }
            builtHistogramKind = .chart
            if let cached = histogramRow, builtHistogramBars == bars,
               builtHistogramTimeZoneID == TimeZone.current.identifier {
                return cached
            }
            let item = histogramChartItemFactory(bars)
            histogramRow = item
            builtHistogramBars = bars
            builtHistogramTimeZoneID = TimeZone.current.identifier
            return item
        }
    }

    /// The idle row's text — a statement about the last 24 hours, matching the
    /// claim the accessibility summary makes for the same axis.
    static let idleHistogramTitle = "No calls in the last 24h"

    private static func kind(of state: HistogramState) -> HistogramRowKind {
        switch state {
        case .loading: return .loading
        case .failed: return .failed
        case .loaded(let bars):
            return bars.allSatisfy { $0.total == 0 } ? .idle : .chart
        }
    }

    /// Refresh the histogram row without restructuring the menu — never a
    /// resize. Within the chart kind, new bars swap the hosted view (the frame
    /// is a fixed `chartItemSize`). The two TEXT kinds share one-line
    /// geometry, so loading ↔ failed rewrites the row in place — a fetch that
    /// fails while the menu is open must not leave "loading…" on screen
    /// telling a quiet lie. A text ↔ chart flip is the one transition that
    /// would change the row's height; that row alone stays as built, and the
    /// next rebuild installs the right one.
    private func refreshHistogramRow(with histogramState: HistogramState) {
        guard let item = histogramRow, let builtKind = builtHistogramKind else { return }
        let newKind = Self.kind(of: histogramState)
        switch (builtKind == .chart, newKind == .chart) {
        case (true, true):
            guard case .loaded(let bars) = histogramState, builtHistogramBars != bars else { return }
            item.view = histogramChartItemFactory(bars).view
            builtHistogramBars = bars
            builtHistogramTimeZoneID = TimeZone.current.identifier
        case (false, false):
            applyMutedHistogramText(for: histogramState, kind: newKind, to: item)
        default:
            break
        }
    }

    /// Rewrite a text-kind histogram row to describe `histogramState`.
    private func applyMutedHistogramText(
        for histogramState: HistogramState, kind: HistogramRowKind, to item: NSMenuItem
    ) {
        switch kind {
        case .loading:
            Self.setMutedTitle("Activity (24h) — loading…", on: item)
            item.toolTip = nil
        case .failed:
            Self.setMutedTitle("Activity (24h) unavailable", on: item)
            if case .failed(let message) = histogramState, item.toolTip != message {
                item.toolTip = message
            }
        case .idle:
            Self.setMutedTitle(Self.idleHistogramTitle, on: item)
            item.toolTip = nil
        case .chart:
            return
        }
        builtHistogramKind = kind
    }

    /// A disabled, secondary-coloured text row. Setting `attributedTitle`
    /// leaves `title` intact, so the plain string stays available to tests and
    /// to accessibility.
    static func mutedItem(_ title: String) -> NSMenuItem {
        // Created with an empty title so `setMutedTitle`'s no-change guard
        // cannot skip the attributed styling on first install.
        let item = NSMenuItem(title: "", action: nil, keyEquivalent: "")
        item.isEnabled = false
        setMutedTitle(title, on: item)
        return item
    }

    private static func setMutedTitle(_ title: String, on item: NSMenuItem) {
        guard item.title != title else { return }
        item.title = title
        item.attributedTitle = NSAttributedString(string: title, attributes: [
            .font: NSFont.menuFont(ofSize: 0),
            .foregroundColor: NSColor.secondaryLabelColor
        ])
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
    ///
    /// The client segment reports states rather than a total ("2 active · 1
    /// idle"), and disappears entirely when nobody is either (FR-019). A bare
    /// count could not distinguish a proxy three agents are hammering from one
    /// three agents used before lunch, and "0 clients" was the misleading
    /// headline over a section that had rows in it.
    private func summaryTitle(for state: AppState, now: Date = Date()) -> String {
        var parts: [String] = []
        // `glanceCallsThisHour`, not the raw polled `callsThisHour`: the rows
        // below arrive over SSE and the poll is 30 seconds apart, so the raw
        // count sat under rows it had never heard of (GH #934).
        if let calls = state.glanceCallsThisHour(now: now) {
            parts.append(calls == 1 ? "1 call this hour" : "\(calls) calls this hour")
        }
        if let clients = state.glanceClientSummary(now: now) { parts.append(clients) }
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
