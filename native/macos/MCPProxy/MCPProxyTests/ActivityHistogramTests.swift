import XCTest
import AppKit
@testable import MCPProxy

/// Shared anchors. All times are UTC: the backend aligns buckets with
/// `rec.Timestamp.UTC().Truncate(time.Hour)`, so the tests use the same grid.
/// A top-level `private` declaration is visible to the whole file, so every
/// test class below shares these.
private enum Fixture {
    /// 2027-01-15 08:35:00 UTC — deliberately mid-hour, so flooring is exercised.
    static let now = Date(timeIntervalSince1970: 1_800_002_100)
    /// 2027-01-15 08:00:00 UTC — the hour containing `now`; the newest bar.
    static let currentHour = Date(timeIntervalSince1970: 1_800_000_000)
    /// 2027-01-15 04:00:00 UTC — four hours back; bar index 19 on a 24-bar axis.
    static let fourHoursAgo = Date(timeIntervalSince1970: 1_799_985_600)
    /// 2027-01-14 09:00:00 UTC — the oldest bar on the axis.
    static let oldestHour = Date(timeIntervalSince1970: 1_799_917_200)
    /// 2027-01-13 06:00:00 UTC — 27 hours before the oldest bar, well off the
    /// left edge of the axis.
    static let offAxis = Date(timeIntervalSince1970: 1_799_820_000)

    static let utc = TimeZone(identifier: "UTC")!

    static func bucket(start: Date, calls: Int, errors: Int) -> UsageBucket {
        UsageBucket(start: start, calls: calls, errors: errors, totalRespBytes: 0)
    }
}

final class ActivityHistogramBarsTests: XCTestCase {

    /// The usage endpoint omits hours with no activity, so the timeline is
    /// sparse. The axis must still be a stable 24 hours, oldest first, ending
    /// at the hour containing `now`.
    func testMissingHoursAreSynthesisedAsZero() {
        let timeline = [
            // 08:20 UTC — must land in the 08:00 bucket, not its own.
            Fixture.bucket(start: Date(timeIntervalSince1970: 1_800_001_200), calls: 10, errors: 3),
            Fixture.bucket(start: Fixture.fourHoursAgo, calls: 4, errors: 0)
        ]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars.count, 24)
        XCTAssertEqual(bars.first?.hourStart, Fixture.oldestHour)
        XCTAssertEqual(bars.last?.hourStart, Fixture.currentHour)
        XCTAssertEqual(bars[19].hourStart, Fixture.fourHoursAgo)
        XCTAssertEqual(bars[19].succeeded, 4)
        XCTAssertEqual(bars[19].errors, 0)
        XCTAssertEqual(bars.filter { $0.total > 0 }.count, 2, "every other hour is a synthesised zero")
    }

    /// A bucket's `calls` ALREADY includes its `errors`. The two stacked
    /// segments must therefore sum to `calls`, never to `calls + errors`.
    func testStackedSegmentsDoNotDoubleCountErrors() {
        let timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 10, errors: 3)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars[23].succeeded, 7)
        XCTAssertEqual(bars[23].errors, 3)
        XCTAssertEqual(bars[23].total, 10)
    }

    /// Defensive: a bucket claiming more errors than calls must not produce a
    /// negative segment — Charts would draw it below the axis.
    func testErrorsExceedingCallsClampSucceededToZero() {
        let timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 2, errors: 5)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars[23].succeeded, 0)
        XCTAssertEqual(bars[23].errors, 5)
    }

    /// Buckets older than the axis are dropped, not folded into the oldest bar
    /// (which would make yesterday's spike look like this morning's).
    func testBucketsOlderThanTheAxisAreDropped() {
        let timeline = [Fixture.bucket(start: Fixture.offAxis, calls: 99, errors: 9)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars.reduce(0) { $0 + $1.total }, 0)
    }
}

final class ActivityHistogramAccessibilityTests: XCTestCase {

    /// A bar chart is opaque to VoiceOver, so the hosted item carries one
    /// sentence describing the whole series.
    func testSummaryReportsTotalsAndPeakHour() {
        let timeline = [
            Fixture.bucket(start: Fixture.currentHour, calls: 10, errors: 3),
            Fixture.bucket(start: Fixture.fourHoursAgo, calls: 4, errors: 0)
        ]
        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        let summary = ActivityHistogram.accessibilitySummary(bars: bars, timeZone: Fixture.utc)

        XCTAssertEqual(
            summary,
            "Activity over the last 24 hours: 14 calls, 3 errors. Busiest hour 08:00 with 10 calls."
        )
    }

    /// "Loaded but idle" must read as idle, not as a broken chart.
    func testSummaryForAnIdleTimelineSaysSo() {
        let bars = ActivityHistogram.bars(from: [], now: Fixture.now)

        XCTAssertEqual(
            ActivityHistogram.accessibilitySummary(bars: bars, timeZone: Fixture.utc),
            "Activity over the last 24 hours: no tool calls."
        )
    }
}

final class ActivityHistogramStateTests: XCTestCase {

    /// `usageTimeline == nil` alone means both "not loaded yet" and "the fetch
    /// failed", so the two are resolved against `usageError` — and a loaded
    /// timeline beats a recorded failure, because real (if slightly stale) data
    /// is worth more than an error row.
    func testStateResolution() {
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: nil, now: Fixture.now),
            .loading
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: "", now: Fixture.now),
            .loading,
            "an empty message is not a failure"
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: "boom", now: Fixture.now),
            .failed("boom")
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: [], errorMessage: nil, now: Fixture.now),
            .loaded(ActivityHistogram.bars(from: [], now: Fixture.now))
        )
    }

    /// A loaded-but-idle timeline is a flat 24-hour axis, deliberately distinct
    /// from the loading state rather than collapsed into it.
    func testAnIdleTimelineIsLoadedNotLoading() {
        guard case .loaded(let bars) = ActivityHistogram.state(
            timeline: [], errorMessage: "boom", now: Fixture.now
        ) else {
            return XCTFail("an empty timeline is loaded, and beats a stale failure")
        }
        XCTAssertEqual(bars.count, 24)
        XCTAssertEqual(bars.reduce(0) { $0 + $1.total }, 0)
    }
}

@MainActor
final class AppStateUsageErrorTests: XCTestCase {

    /// A connected core. Every glance updater on `AppState` — this one included
    /// — ignores writes unless `coreState == .connected`, so a test that skips
    /// this asserts on a no-op.
    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    func testRecordUsageFailureStoresTheMessage() {
        let state = connectedState()

        XCTAssertNil(state.usageError)
        state.recordUsageFailure("connection refused")
        XCTAssertEqual(state.usageError, "connection refused")
    }

    /// The same reconnect hazard `updateGlanceActivity` guards against, on the
    /// failure path: a usage fetch already past its `guard let apiClient` when
    /// the core dies resolves into its catch block AFTER `clearGlanceState()`.
    /// Without the guard the dead core's failure would outlive it and the
    /// submenu would say "Usage unavailable" where "Loading…" is the truth.
    func testRecordUsageFailureIsIgnoredWhileDisconnected() {
        let state = AppState()

        state.recordUsageFailure("connection refused")

        XCTAssertNil(state.usageError)
    }

    /// A successful refresh must clear a stale failure, otherwise the submenu
    /// would show the error row forever once a single fetch had failed.
    func testUpdateUsageClearsAPreviousFailure() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")

        state.updateUsage(
            timeline: [Fixture.bucket(start: Fixture.currentHour, calls: 1, errors: 0)],
            now: Fixture.now
        )

        XCTAssertNil(state.usageError)
        XCTAssertEqual(state.callsThisHour, 1)
    }

    /// Disconnecting must not leave the previous core's failure on screen.
    func testClearGlanceStateClearsTheFailure() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")

        state.clearGlanceState()

        XCTAssertNil(state.usageError)
    }
}
