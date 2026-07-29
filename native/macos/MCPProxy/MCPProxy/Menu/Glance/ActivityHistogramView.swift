// ActivityHistogramView.swift
// MCPProxy
//
// The 24-hour calls-per-hour bar chart shown in the tray glance's
// "Activity (24h)" submenu, plus the pure bucket-shaping and accessibility
// helpers it renders from.
//
// The chart renders from `AppState.usageTimeline` only — opening the submenu
// performs no network request (spec 048 invariant).

import Foundation

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
}
