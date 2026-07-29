// ActivityHistogramView.swift
// MCPProxy
//
// The 24-hour calls-per-hour bar chart shown in the tray glance's
// "Activity (24h)" submenu, plus the pure bucket-shaping and accessibility
// helpers it renders from.
//
// The chart renders from `AppState.usageTimeline` only — opening the submenu
// performs no network request (spec 048 invariant).

import SwiftUI
import Charts
import AppKit

// MARK: - Bar model

/// One hour of the 24-hour histogram, already split into the two stacked
/// segments the chart draws.
///
/// A `UsageBucket`'s `calls` ALREADY includes its `errors`, so stacking the raw
/// fields would draw every failure twice. `succeeded` is the difference.
struct HistogramBar: Identifiable, Equatable {
    /// Start of the UTC hour this bar covers.
    let hourStart: Date
    /// Calls that did not fail: `calls - errors`, never negative.
    let succeeded: Int
    /// Calls that failed.
    let errors: Int

    var id: Date { hourStart }

    /// Total calls in the hour — the height of the stacked bar.
    var total: Int { succeeded + errors }
}

// MARK: - What the submenu shows

/// What the histogram submenu renders right now.
///
/// `loading` and `failed` are deliberately distinct: both leave the timeline
/// nil, and telling the user "Loading…" forever after a failed fetch is the
/// kind of quiet lie this menu must not tell.
enum HistogramState: Equatable {
    /// No timeline yet, and no failure recorded.
    case loading
    /// The usage refresh failed before any timeline arrived; payload is the message.
    case failed(String)
    /// A full 24-hour axis, oldest hour first. An all-zero axis is a valid
    /// loaded state — the proxy was simply idle.
    case loaded([HistogramBar])
}

// MARK: - Pure helpers

/// Bucket shaping and accessibility copy for the 24-hour histogram.
/// Pure and synchronous, so it is testable without AppKit or a window server.
enum ActivityHistogram {

    /// Bars on the axis. Fixed, so the axis does not resize as traffic starts
    /// and stops.
    static let hourCount = 24

    /// Truncate a date to the start of its UTC hour.
    ///
    /// Delegates to `AppState.floorToHour` rather than reimplementing it: the
    /// header count (`AppState.callsInCurrentHour`) and this axis must agree on
    /// where an hour begins, and two copies of the rule would be free to drift.
    static func floorToHour(_ date: Date) -> Date {
        AppState.floorToHour(date)
    }

    /// Project a sparse timeline onto a dense 24-hour axis ending at the UTC
    /// hour containing `now`, oldest hour first.
    ///
    /// The endpoint returns only hours that exist, so missing hours are
    /// synthesised as zero and buckets older than the axis are dropped.
    static func bars(from timeline: [UsageBucket], now: Date) -> [HistogramBar] {
        var succeededByHour: [Date: Int] = [:]
        var errorsByHour: [Date: Int] = [:]

        for bucket in timeline {
            let hour = floorToHour(bucket.start)
            let errors = max(0, bucket.errors)
            // `calls` includes `errors`; clamp so a malformed bucket where
            // errors > calls cannot produce a negative segment.
            let succeeded = max(0, bucket.calls - errors)
            succeededByHour[hour, default: 0] += succeeded
            errorsByHour[hour, default: 0] += errors
        }

        let currentHour = floorToHour(now)
        return (0..<hourCount).reversed().map { offset in
            let hour = currentHour.addingTimeInterval(TimeInterval(-3600 * offset))
            return HistogramBar(
                hourStart: hour,
                succeeded: succeededByHour[hour] ?? 0,
                errors: errorsByHour[hour] ?? 0
            )
        }
    }

    /// One sentence describing the whole series, because a bar chart is opaque
    /// to VoiceOver. Ties on the peak resolve to the earliest hour.
    ///
    /// - Parameter timeZone: injected so the hour label is deterministic in
    ///   tests; production uses the user's zone.
    static func accessibilitySummary(bars: [HistogramBar], timeZone: TimeZone = .current) -> String {
        let totalCalls = bars.reduce(0) { $0 + $1.total }
        let totalErrors = bars.reduce(0) { $0 + $1.errors }
        guard let first = bars.first, totalCalls > 0 else {
            return "Activity over the last 24 hours: no tool calls."
        }

        var peak = first
        for bar in bars where bar.total > peak.total { peak = bar }

        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timeZone
        formatter.dateFormat = "HH:mm"

        return "Activity over the last 24 hours: \(totalCalls) calls, \(totalErrors) errors. "
            + "Busiest hour \(formatter.string(from: peak.hourStart)) with \(peak.total) calls."
    }

    /// Decide what the submenu shows. A timeline that has loaded wins over a
    /// recorded failure: showing real (if slightly stale) data beats showing an
    /// error row.
    static func state(timeline: [UsageBucket]?, errorMessage: String?, now: Date) -> HistogramState {
        if let timeline {
            return .loaded(bars(from: timeline, now: now))
        }
        if let errorMessage, !errorMessage.isEmpty {
            return .failed(errorMessage)
        }
        return .loading
    }
}

// MARK: - Chart

/// The stacked bar chart itself. Rendering only — it fetches nothing.
///
/// Two `BarMark`s per hour sharing an x value stack automatically; the series
/// are `calls - errors` and `errors`, never the raw fields.
struct ActivityHistogramView: View {
    let bars: [HistogramBar]
    let accessibilitySummary: String

    var body: some View {
        Chart {
            ForEach(bars) { bar in
                BarMark(
                    x: .value("Hour", bar.hourStart, unit: .hour),
                    y: .value("Calls", bar.succeeded)
                )
                .foregroundStyle(by: .value("Outcome", "Succeeded"))

                BarMark(
                    x: .value("Hour", bar.hourStart, unit: .hour),
                    y: .value("Calls", bar.errors)
                )
                .foregroundStyle(by: .value("Outcome", "Errors"))
            }
        }
        .chartForegroundStyleScale([
            "Succeeded": Color.accentColor,
            "Errors": Color.red
        ])
        // The legend would double the item's height for two self-evident
        // colours; the accessibility label names both series instead.
        .chartLegend(.hidden)
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 3))
        }
        .chartXAxis {
            AxisMarks(values: .stride(by: .hour, count: 6)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.hour())
            }
        }
        .frame(width: 260, height: 96)
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        // One label for the whole chart: VoiceOver reading 48 unlabelled bar
        // marks would be worse than useless.
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(accessibilitySummary))
    }
}

extension ActivityHistogram {

    /// Size of the hosted chart item, in points. Menu items do not auto-size a
    /// hosting view, so the frame is explicit — and it must match the view's
    /// own size, or the row grows a band of dead space. 260 + 2*14 = 288 wide,
    /// 96 + 2*8 = 112 tall; measured `NSHostingView.fittingSize` agrees.
    static let chartItemSize = NSSize(width: 288, height: 112)

    /// The submenu's single custom item: an `NSHostingView` wrapping the chart.
    ///
    /// Custom menu-item views receive mouse events but not keyboard events, so
    /// the item is disabled (nothing to activate) and carries the whole series
    /// in one accessibility label on the host view.
    static func chartMenuItem(bars: [HistogramBar]) -> NSMenuItem {
        let summary = accessibilitySummary(bars: bars)
        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        item.isEnabled = false

        let host = NSHostingView(
            rootView: ActivityHistogramView(bars: bars, accessibilitySummary: summary)
        )
        host.frame = NSRect(origin: .zero, size: chartItemSize)
        host.setAccessibilityLabel(summary)
        item.view = host
        return item
    }
}

// MARK: - Submenu delegate

/// Builds the single row of `GlanceSection`'s "Activity (24h)" submenu when
/// that submenu opens.
///
/// This is NOT a second submenu — `GlanceSection` owns the item and the menu,
/// and this object only fills it in on demand. It is a separate `NSObject`
/// purely because `NSMenuDelegate` requires `NSObjectProtocol`, which
/// `GlanceSection` (a plain `@MainActor final class`) does not conform to.
///
/// Building on open — rather than inside `items(for:)` — keeps the chart off
/// the menu's hot path: `rebuildMenu()` runs on every debounced
/// `objectWillChange`, menu open or closed, so building eagerly would construct
/// an `NSHostingView` and render a SwiftUI Chart on every state change,
/// including for a menu nobody has opened. Reading `AppState` at open time also
/// means a timeline that arrives while the menu sits closed is shown on the
/// next open, with no rebuild of the parent menu.
///
/// It reads `AppState` and nothing else: opening the submenu performs no
/// network request (spec 048 invariant).
///
/// `NSMenu.delegate` is a WEAK reference, so `GlanceSection` must retain this.
final class HistogramSubmenuDelegate: NSObject, NSMenuDelegate {

    private let appState: AppState
    private let chartItemFactory: ([HistogramBar]) -> NSMenuItem

    /// - Parameter chartItemFactory: injected so submenu-structure tests are
    ///   independent of how the chart itself renders. It defaults to the real
    ///   chart: the seam this replaced was optional, nothing in production ever
    ///   set it, and the tray consequently shipped a text row instead of a
    ///   chart. A default that already works cannot fail that way.
    init(appState: AppState,
         chartItemFactory: @escaping ([HistogramBar]) -> NSMenuItem = ActivityHistogram.chartMenuItem) {
        self.appState = appState
        self.chartItemFactory = chartItemFactory
        super.init()
    }

    // MARK: NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        menu.addItem(currentItem())
    }

    // MARK: Rows

    /// The single row the submenu shows for the current `AppState`.
    ///
    /// The clock is read here, at open time, rather than injected: every
    /// assertion about this row is structural (which row, how many), and the
    /// axis contents `now` decides are covered exhaustively by the pure
    /// `ActivityHistogram.bars` tests.
    func currentItem() -> NSMenuItem {
        switch ActivityHistogram.state(
            timeline: appState.usageTimeline,
            errorMessage: appState.usageError,
            now: Date()
        ) {
        case .loading:
            return Self.mutedItem("Loading…")
        case .failed(let message):
            let item = Self.mutedItem("Usage unavailable")
            item.toolTip = message
            return item
        case .loaded(let bars):
            return chartItemFactory(bars)
        }
    }

    /// A disabled, secondary-coloured text row. Setting `attributedTitle`
    /// leaves `title` intact, so the plain string stays available to tests and
    /// to accessibility.
    static func mutedItem(_ title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        item.attributedTitle = NSAttributedString(string: title, attributes: [
            .font: NSFont.menuFont(ofSize: 0),
            .foregroundColor: NSColor.secondaryLabelColor
        ])
        return item
    }
}
