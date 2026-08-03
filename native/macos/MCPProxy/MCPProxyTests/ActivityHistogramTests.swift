import XCTest
import AppKit
import SwiftUI
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


/// The inline histogram row belongs to `GlanceSection`: it renders directly
/// under the summary line — no submenu — so the day's shape is on screen the
/// moment the menu opens. These tests assert the row's three kinds (loading,
/// failed, chart), the cache that keeps eager builds affordable, and the
/// structural rules `updateInPlace` enforces for it.
///
/// The old "Not updating" marker tests were dropped, not ported: the submenu
/// needed its own stale marker because the summary line was not visible from
/// inside it, while the inline row sits directly under the summary — whose
/// "not updating" segment (`GlanceSectionTests` header tests) already says it.
@MainActor
final class GlanceInlineHistogramTests: XCTestCase {

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    /// A connected core — the block is hidden otherwise.
    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    /// A section whose chart row is stubbed, so these tests assert on block
    /// structure alone, independent of how the chart itself renders.
    private func makeSection() -> GlanceSection {
        let section = makeBareSection()
        section.histogramChartItemFactory = { bars in
            NSMenuItem(title: "CHART:\(bars.count)", action: nil, keyEquivalent: "")
        }
        return section
    }

    /// A section with nothing injected, so the production defaults apply.
    private func makeBareSection() -> GlanceSection {
        GlanceSection(target: Self.clickStub,
                      action: #selector(ClickStub.openGlanceRow(_:)))
    }

    /// The histogram row: always the summary line's neighbour.
    private func histogramRow(_ section: GlanceSection, _ state: AppState) -> NSMenuItem {
        let items = section.items(for: state, now: Fixture.now)
        guard items.count > 1 else {
            XCTFail("the block is hidden, so there is no histogram row")
            return NSMenuItem()
        }
        return items[1]
    }

    // MARK: - The three kinds

    func testLoadingRowWhileTheTimelineIsNil() {
        let row = histogramRow(makeSection(), connectedState())

        XCTAssertEqual(row.title, "Activity (24h) — loading…")
        XCTAssertFalse(row.isEnabled)
        XCTAssertNil(row.submenu, "the histogram renders inline, never behind a submenu")
        let attributes = row.attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    func testErrorRowWhenTheFetchFailedBeforeAnyTimelineArrived() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")

        let row = histogramRow(makeSection(), state)

        XCTAssertEqual(row.title, "Activity (24h) unavailable")
        XCTAssertEqual(row.toolTip, "connection refused")
        XCTAssertFalse(row.isEnabled)
        let attributes = row.attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    /// The headline behaviour: with a timeline loaded, the chart itself is the
    /// second row of the menu — visible on open, no navigation step.
    func testChartRendersInlineWithNoSubmenu() {
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]

        let row = histogramRow(makeSection(), state)

        XCTAssertEqual(row.title, "CHART:24", "the factory receives the shaped 24-hour axis")
        XCTAssertNil(row.submenu)
    }

    /// Real data beats a stale failure.
    func testChartRowWinsOverAStaleFailure() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]

        let row = histogramRow(makeSection(), state)

        XCTAssertEqual(row.title, "CHART:24")
    }

    /// A timeline that arrives while the menu sits closed is charted by the
    /// next rebuild — `menuWillOpen` runs one before the menu is drawn, so the
    /// next open never shows a stale loading row.
    func testATimelineArrivingWhileClosedIsChartedOnTheNextRebuild() {
        let section = makeSection()
        let state = connectedState()
        XCTAssertEqual(histogramRow(section, state).title, "Activity (24h) — loading…")

        state.usageTimeline = []

        XCTAssertEqual(histogramRow(section, state).title, "CHART:24",
                       "an idle timeline is a flat axis, not a loading row")
    }

    // MARK: - The eager-build cache

    /// `items(for:)` runs on every debounced rebuild, menu open or closed; the
    /// chart must not be re-rendered when the shaped axis has not moved.
    func testTheChartItemIsCachedAcrossRebuildsWithUnchangedBars() {
        let section = makeBareSection()
        var factoryCalls = 0
        section.histogramChartItemFactory = { bars in
            factoryCalls += 1
            return NSMenuItem(title: "CHART:\(bars.count)", action: nil, keyEquivalent: "")
        }
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]

        let first = histogramRow(section, state)
        let second = histogramRow(section, state)

        XCTAssertEqual(factoryCalls, 1, "unchanged bars must not re-render the chart")
        XCTAssertTrue(first === second, "the cached item itself is reused")
    }

    func testChangedBarsRebuildTheChartItem() {
        let section = makeBareSection()
        var factoryCalls = 0
        section.histogramChartItemFactory = { bars in
            factoryCalls += 1
            return NSMenuItem(title: "CHART:\(bars.count)", action: nil, keyEquivalent: "")
        }
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]
        _ = histogramRow(section, state)

        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 5, errors: 0)]
        _ = histogramRow(section, state)

        XCTAssertEqual(factoryCalls, 2)
    }

    // MARK: - In-place rules

    /// A text placeholder and a 132 pt chart have different heights, so the
    /// flip cannot happen under the cursor — but it must not freeze the block
    /// either (the timeline loads seconds after launch, exactly when the menu
    /// is likely open). The update succeeds, the placeholder stays, and the
    /// next rebuild — `menuWillOpen` runs one before every display — installs
    /// the chart.
    func testATextToChartFlipKeepsThePlaceholderButNotForever() {
        let section = makeSection()
        let state = connectedState()
        let items = section.items(for: state, now: Fixture.now)
        XCTAssertEqual(items[1].title, "Activity (24h) — loading…")

        state.usageTimeline = []

        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now),
                      "the rest of the block keeps updating in place")
        XCTAssertEqual(items[1].title, "Activity (24h) — loading…",
                       "the row's height cannot change under the cursor")
        XCTAssertEqual(histogramRow(section, state).title, "CHART:24",
                       "the next rebuild installs the chart")
    }

    func testLosingTheTimelineKeepsTheChartUntilTheNextRebuild() {
        let section = makeSection()
        let state = connectedState()
        state.usageTimeline = []
        let row = histogramRow(section, state)
        XCTAssertEqual(row.title, "CHART:24")

        state.usageTimeline = nil

        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now))
        XCTAssertEqual(row.title, "CHART:24",
                       "real (if stale) data stays on screen; the next rebuild decides")
    }

    /// The two text kinds share one-line geometry, so a fetch that fails while
    /// the menu is open replaces "loading…" in place — leaving it up would be
    /// the quiet lie `HistogramState` exists to prevent.
    func testALoadingRowBecomesTheFailureRowInPlace() {
        let section = makeSection()
        let state = connectedState()
        let row = histogramRow(section, state)
        XCTAssertEqual(row.title, "Activity (24h) — loading…")

        state.recordUsageFailure("connection refused")

        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now))
        XCTAssertEqual(row.title, "Activity (24h) unavailable")
        XCTAssertEqual(row.toolTip, "connection refused")
        let attributes = row.attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor,
                       "the in-place rewrite keeps the muted styling")
    }

    /// Within the chart kind, new bars are an ordinary in-place rewrite: the
    /// item keeps its place and its frame, only its hosted view is swapped.
    func testBarsChangingUnderAnOpenMenuSwapTheViewInPlace() {
        let section = makeBareSection()
        section.histogramChartItemFactory = { bars in
            let item = NSMenuItem(title: "CHART:\(bars.count)", action: nil, keyEquivalent: "")
            let view = NSView(frame: NSRect(x: 0, y: 0, width: 288, height: 132))
            view.setAccessibilityLabel("total \(bars.reduce(0) { $0 + $1.total })")
            item.view = view
            return item
        }
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]
        let row = histogramRow(section, state)
        XCTAssertEqual(row.view?.accessibilityLabel(), "total 3")

        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 7, errors: 0)]

        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now))
        XCTAssertEqual(row.view?.accessibilityLabel(), "total 7",
                       "the same row now hosts the fresh chart")
    }

    func testFailureTooltipRefreshesInPlace() {
        let section = makeSection()
        let state = connectedState()
        state.recordUsageFailure("connection refused")
        let row = histogramRow(section, state)

        state.recordUsageFailure("socket closed")

        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now))
        XCTAssertEqual(row.toolTip, "socket closed")
    }

    // MARK: - The real chart

    /// The real chart item, not the stub: `chartItemSize` is otherwise an
    /// unverified constant, and a mismatch with the view's own size shows up as
    /// a band of dead space under the chart.
    func testRealChartItemIsSizedAndLabelled() {
        let bars = ActivityHistogram.bars(from: [], now: Fixture.now)

        let item = ActivityHistogram.chartMenuItem(bars: bars)

        XCTAssertEqual(item.view?.frame.size, ActivityHistogram.chartItemSize)
        XCTAssertEqual(item.view?.fittingSize, ActivityHistogram.chartItemSize,
                       "the hosting view must fit its frame exactly, or the row grows dead space")
        XCTAssertEqual(item.view?.accessibilityLabel(),
                       "Activity over the last 24 hours: no tool calls.")
        XCTAssertFalse(item.isEnabled)
    }

    /// With no factory injected the block must still show a REAL chart. The
    /// seam this replaced was optional and nothing in production ever set it,
    /// so the shipped tray showed a text fallback and never a chart; a default
    /// that already works cannot fail that way.
    func testTheDefaultFactoryProducesTheRealChart() {
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]

        let row = histogramRow(makeBareSection(), state)

        XCTAssertEqual(row.view?.frame.size, ActivityHistogram.chartItemSize)
        XCTAssertNotNil(row.view?.accessibilityLabel())
    }
}

/// The refresh path that makes `usageError` reachable. Without these, the
/// failure state is code nobody can ever see: the submenu would sit on
/// "Loading…" forever after a fetch that never recovers.
@MainActor
final class UsageRefreshWiringTests: XCTestCase {

    /// A data source that fails on demand.
    private final class StubSource: GlanceDataSource {
        var error: Error?
        var timeline: [UsageBucket] = []

        func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse {
            if let error { throw error }
            return UsageAggregateResponse(window: "24h", tokenSource: "bytes", tokensSaved: 0,
                                          tokensSavedPercentage: 0, timeline: timeline)
        }
        func glanceActivity(limit: Int) async throws -> [ActivityEntry] { [] }
        func recentSessions(limit: Int) async throws -> [APIClient.MCPSession] { [] }
    }

    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    func testAFailedRefreshRecordsTheMessage() async {
        let state = connectedState()
        let source = StubSource()
        source.error = APIClientError.httpError(statusCode: 503, message: "core restarting")

        await state.refreshUsage(from: source)

        XCTAssertEqual(state.usageError, "HTTP 503: core restarting")
        XCTAssertNil(state.usageTimeline, "a failed fetch must not fabricate a timeline")
    }

    func testASuccessfulRefreshClearsAPreviousFailure() async {
        let state = connectedState()
        let source = StubSource()
        source.error = APIClientError.notReady
        await state.refreshUsage(from: source)
        XCTAssertNotNil(state.usageError)

        source.error = nil
        source.timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 5, errors: 1)]
        await state.refreshUsage(from: source)

        XCTAssertNil(state.usageError)
        XCTAssertEqual(state.usageTimeline?.count, 1)
    }

    /// A later failure must not throw away data already on screen — the chart
    /// keeps showing real, slightly stale numbers rather than flipping to an
    /// error row.
    func testAFailedRefreshLeavesAnAlreadyLoadedTimelineAlone() async {
        let state = connectedState()
        let source = StubSource()
        source.timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 5, errors: 1)]
        await state.refreshUsage(from: source)

        source.error = APIClientError.noData
        await state.refreshUsage(from: source)

        XCTAssertEqual(state.usageTimeline?.count, 1, "the loaded timeline survives a later failure")
        if case .loaded = ActivityHistogram.state(timeline: state.usageTimeline,
                                                  errorMessage: state.usageError,
                                                  now: Fixture.now) {
            // expected: real data still beats the recorded failure
        } else {
            XCTFail("the submenu must keep charting data it already has")
        }
    }
}

/// The chart's visual encoding. `GlanceSection.statusTint` states the rule these
/// pin: colour is never the only channel separating two outcomes.
///
/// Rasterised with SwiftUI's `ImageRenderer`, NOT `NSHostingView.cacheDisplay`,
/// and at a pinned scale. Both halves come from a CI failure worth recording,
/// because the raster on the GitHub macOS runner is not what "it renders blank"
/// would suggest.
///
/// What the runner actually produced: solid shapes yes — the legend's red
/// swatch came through — axis text apparently not, and all of it at 1x where
/// this machine is 2x. `cacheDisplay`'s output therefore varies with the host in
/// both content and pixel grid. `ImageRenderer` draws offscreen through Core
/// Graphics, takes an explicit `scale`, and matched the on-screen raster here
/// where it counts: identical red and chroma counts, measured both ways before
/// this was changed. Nothing below depends on text rendering.
///
/// The scale mattered independently. The old sampler strode `rep.size`, which is
/// the POINT size at 2x and the PIXEL size at 1x, so one line of code covered a
/// quarter of the image on a Retina machine and all of it on the runner — which
/// is precisely why the legend's red swatch was invisible here and fatal there.
@MainActor
final class ActivityHistogramEncodingTests: XCTestCase {

    /// The chart is 132 pt tall and the bottom 24 pt of that is the legend —
    /// the band the frame grew to pay for. Everything above it is the plot.
    private static let legendBandHeight = 24.0

    /// What a rasterised region actually painted.
    private struct Pixels {
        /// Sample points with meaningful alpha — i.e. how much was drawn at all.
        ///
        /// The threshold is deliberately low: `Color.secondary` resolves to an
        /// alpha of ~0.5, so the success bars are semi-transparent against the
        /// renderer's transparent backing. A 0.5 cut-off drops the entire
        /// success series and leaves a "blank chart" reading that is not true.
        var drawn = 0
        /// Sample points reading as saturated red.
        var red = 0
        /// Sample points carrying ANY chroma: `max(r,g,b) - min(r,g,b)` above a
        /// threshold. Greys — including `Color.secondary` — score zero.
        var chromatic = 0
    }

    /// Which part of the chart to count.
    private enum Region {
        /// Everything above the legend — the plot area proper.
        case plot
        /// The bottom 24 pt, where the legend sits.
        case legend
        case whole
    }

    /// Rasterise the chart offscreen and count pixels in one region.
    ///
    /// Returns nil ONLY when there is genuinely no image to inspect. It
    /// deliberately does NOT fold a low pixel count into nil: a test that turns
    /// its own failed precondition into a skip reports green while asserting
    /// nothing, and does so exactly where nobody is watching. Callers assert on
    /// `drawn` instead.
    private func pixels(of bars: [HistogramBar], in region: Region) -> Pixels? {
        let renderer = ImageRenderer(content: ActivityHistogramView(bars: bars,
                                                                    accessibilitySummary: ""))
        renderer.scale = 2
        guard let cgImage = renderer.cgImage else { return nil }
        let rep = NSBitmapImageRep(cgImage: cgImage)

        // y grows downward in the rendered image, so the legend is the last rows.
        let bandStart = rep.pixelsHigh - Int((Self.legendBandHeight / ActivityHistogram.chartItemSize.height)
                                             * Double(rep.pixelsHigh))
        let rows: Range<Int>
        switch region {
        case .plot: rows = 0..<bandStart
        case .legend: rows = bandStart..<rep.pixelsHigh
        case .whole: rows = 0..<rep.pixelsHigh
        }

        var counts = Pixels()
        for x in stride(from: 0, to: rep.pixelsWide, by: 2) {
            for y in stride(from: rows.lowerBound, to: rows.upperBound, by: 2) {
                guard let colour = rep.colorAt(x: x, y: y), colour.alphaComponent > 0.1 else { continue }
                counts.drawn += 1

                let r = colour.redComponent, g = colour.greenComponent, b = colour.blueComponent
                if r > 0.5, g < 0.45, b < 0.45 { counts.red += 1 }
                if max(r, max(g, b)) - min(r, min(g, b)) > 0.15 { counts.chromatic += 1 }
            }
        }
        return counts
    }

    /// Rasterise, or FAIL.
    ///
    /// Deliberately not a skip, and this is the third narrowing of the same
    /// hole: first a failed threshold turned into a skip, then a blank raster
    /// did, and both times the shape that survived was "the environment where
    /// these tests assert nothing reports success". There is no environment in
    /// which a macOS build can legitimately fail to produce a CGImage from
    /// `ImageRenderer` and the suite should still pass: the tray draws this
    /// chart with the same machinery, so a host that cannot rasterise it cannot
    /// run the feature either. If that ever becomes false, the honest change is
    /// a named, asserted condition — not a skip that swallows every cause.
    private func measure(_ bars: [HistogramBar], in region: Region = .whole) throws -> Pixels {
        try XCTUnwrap(pixels(of: bars, in: region),
                      "ImageRenderer produced no image; the chart cannot be rasterised here at all")
    }

    /// The success series must actually be painted.
    ///
    /// Nothing else here proves it. `drawn` counts axes, grid lines and tick
    /// labels too, so every other assertion in this file is satisfied by a chart
    /// whose success bars are invisible — setting the success fill to
    /// `Color.clear` left all three of them green. The no-chroma test is the
    /// worst of them: with no bars at all it passes more easily, since the thing
    /// it measures is the absence of colour.
    ///
    /// Measured like for like: the same single-hour axis, one hour with ten
    /// successes and one with none. The axes are identical, so the difference is
    /// the bar.
    func testSuccessSegmentsAreActuallyPainted() throws {
        let hour = Fixture.currentHour

        let withSuccesses = try measure([HistogramBar(hourStart: hour, succeeded: 10, errors: 0)], in: .plot)
        let empty = try measure([HistogramBar(hourStart: hour, succeeded: 0, errors: 0)], in: .plot)

        XCTAssertGreaterThan(
            withSuccesses.drawn, empty.drawn + 1000,
            "ten successes must paint a bar: \(withSuccesses.drawn) samples against \(empty.drawn) for an empty hour"
        )
    }

    /// Errors must be drawn in a visually distinct fill, and that fill must be
    /// driven by the DATA — an axis or legend that is red regardless would pass
    /// a "chart contains red" assertion while showing failures in the same
    /// colour as successes.
    ///
    /// Measured over the plot area alone. The legend is red in BOTH renders by
    /// design, and while comparing shares cancels any data-independent red, not
    /// including it says what is meant.
    func testErrorSegmentsAreDrawnDistinctlyFromSuccesses() throws {
        let hour = Fixture.currentHour

        let withErrors = try measure([HistogramBar(hourStart: hour, succeeded: 7, errors: 3)], in: .plot)
        let withoutErrors = try measure([HistogramBar(hourStart: hour, succeeded: 10, errors: 0)], in: .plot)

        // Asserted, never skipped: a blank render must fail loudly, or the
        // shares below are 0/0.
        XCTAssertGreaterThan(withErrors.drawn, 500, "the chart rendered blank; the counts mean nothing")
        XCTAssertGreaterThan(withoutErrors.drawn, 500, "the chart rendered blank; the counts mean nothing")

        let withShare = Double(withErrors.red) / Double(withErrors.drawn)
        let withoutShare = Double(withoutErrors.red) / Double(withoutErrors.drawn)
        XCTAssertGreaterThan(
            withShare, withoutShare + 0.05,
            "an hour with errors must paint materially more of the error colour than one without"
        )
    }

    /// The success fill must not follow `Color.accentColor`, or a user whose
    /// system accent is red sees two near-identical segments.
    ///
    /// The accent cannot be set from a test process, so this measures the
    /// property from the other side: `Color.secondary` is achromatic, an accent
    /// is not. A success-only PLOT must therefore paint no chroma. The failure
    /// direction is safe — a machine whose accent is Graphite renders a grey
    /// accent and gives a false pass, never a false fail.
    ///
    /// Scoped to the plot for a reason CI had to teach us: the legend carries a
    /// red "Errors" swatch in every render, success-only included, so asserting
    /// no chroma over the whole image asserts something false. It passed locally
    /// only because the old sampler happened to miss the legend band, and failed
    /// on the runner — 13 chromatic samples — where the same code covered it.
    func testSuccessFillCarriesNoChromaAndSoCannotBeTheAccentColour() throws {
        let counts = try measure([HistogramBar(hourStart: Fixture.currentHour,
                                               succeeded: 10, errors: 0)], in: .plot)

        XCTAssertGreaterThan(counts.drawn, 500, "the chart rendered blank; the count below means nothing")
        XCTAssertEqual(
            counts.chromatic, 0,
            "a plot with no errors must paint nothing coloured; \(counts.chromatic) of \(counts.drawn) sampled pixels carried chroma"
        )
    }

    /// The legend must actually render, and inside the frame that was grown to
    /// pay for it.
    ///
    /// `.chartLegend(.visible)` was the one user-visible thing on this view that
    /// no test asserted the presence of: flipping it to `.hidden` left the suite
    /// green while silently reclaiming the 20pt the frame grew to fit it — and
    /// `testRealChartItemIsSizedAndLabelled` would then have failed on the size,
    /// inviting whoever reclaimed the space to "fix" that test instead.
    ///
    /// Measured, not asserted from the outside: on an all-zero axis the chart
    /// draws no error segments, so the ONLY red the view can contain is the
    /// legend's "Errors" swatch. Hiding the legend takes it to zero. Both halves
    /// are positive assertions, so a blank raster fails them rather than passing.
    func testTheLegendRendersInsideTheFrameThatPaysForIt() throws {
        let bars = ActivityHistogram.bars(from: [], now: Fixture.now)

        let legend = try measure(bars, in: .legend)
        let plot = try measure(bars, in: .plot)

        XCTAssertGreaterThan(
            legend.red, 0,
            "no legend swatch in the bottom band — the 20pt of frame height buys nothing"
        )
        XCTAssertEqual(
            plot.red, 0,
            "an all-zero axis must paint no red in the plot, or this measures chart data"
        )
    }
}
