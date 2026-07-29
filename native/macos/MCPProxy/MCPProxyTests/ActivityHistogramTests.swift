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


/// The histogram submenu belongs to `GlanceSection` — there is exactly one, and
/// it builds its single row when it opens rather than on every `rebuildMenu()`.
/// These tests drive it through the delegate the section installs, which is the
/// same path AppKit uses.
@MainActor
final class GlanceHistogramSubmenuTests: XCTestCase {

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    /// Every section built during a test, kept alive for its whole duration.
    /// The section is the only strong reference to the submenu delegate — the
    /// menu's own is weak — so letting one die mid-test empties the submenu.
    private var sections: [GlanceSection] = []

    override func tearDown() {
        sections = []
        super.tearDown()
    }

    /// A connected core — the block is hidden otherwise.
    private func connectedState() -> AppState {
        let state = AppState()
        state.coreState = .connected
        return state
    }

    /// A section whose chart row is stubbed, so these tests assert on submenu
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
        let section = GlanceSection(target: Self.clickStub,
                                    action: #selector(ClickStub.openGlanceRow(_:)))
        sections.append(section)
        return section
    }

    /// The "Activity (24h)" item, wherever it sits in the block.
    private func histogramItem(_ section: GlanceSection, _ state: AppState) -> NSMenuItem {
        let items = section.items(for: state, now: Fixture.now)
        guard let item = items.first(where: { $0.title == "Activity (24h)" }) else {
            XCTFail("no Activity (24h) item in the block")
            return NSMenuItem()
        }
        return item
    }

    /// Fire the delegate the way AppKit does, through the menu's own reference,
    /// so a delegate that was never installed fails the test.
    private func open(_ menu: NSMenu) {
        guard let delegate = menu.delegate else {
            return XCTFail("the submenu has no delegate, so opening it would build nothing")
        }
        delegate.menuNeedsUpdate?(menu)
    }

    /// Nothing is built until the submenu opens. `rebuildMenu()` runs on every
    /// debounced state change, menu open or closed, so building the chart there
    /// would render a SwiftUI Chart nobody is looking at.
    func testSubmenuIsEmptyUntilItOpens() {
        let item = histogramItem(makeSection(), connectedState())

        XCTAssertEqual(item.submenu?.numberOfItems, 0)
    }

    /// `NSMenu.delegate` is a WEAK reference: if the section does not retain the
    /// delegate it deallocates the moment `items(for:)` returns, and the submenu
    /// silently opens empty forever. Nothing but a test catches that.
    func testTheDelegateOutlivesTheBuildCall() {
        let section = makeSection()
        let item = histogramItem(section, connectedState())

        XCTAssertNotNil(item.submenu?.delegate,
                        "the section must retain the submenu delegate")
    }

    /// The submenu delegate must be its own object, not the section's owner:
    /// `AppController.menuWillOpen` rebuilds the whole tray menu, and having it
    /// fire for a submenu opening under the cursor is exactly the
    /// restructuring-while-open the design forbids.
    func testTheSubmenuHasItsOwnDelegateNotTheTrayMenusOwner() {
        let section = makeSection()
        let item = histogramItem(section, connectedState())

        let delegate = item.submenu?.delegate
        XCTAssertNotNil(delegate)
        XCTAssertFalse(delegate === Self.clickStub)
        XCTAssertFalse(delegate === section as AnyObject)
    }

    func testLoadingRowWhileTheTimelineIsNil() {
        let menu = histogramItem(makeSection(), connectedState()).submenu!

        open(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "Loading…")
        XCTAssertFalse(menu.items[0].isEnabled)
        let attributes = menu.items[0].attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    func testErrorRowWhenTheFetchFailedBeforeAnyTimelineArrived() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")
        let menu = histogramItem(makeSection(), state).submenu!

        open(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "Usage unavailable")
        XCTAssertEqual(menu.items[0].toolTip, "connection refused")
        XCTAssertFalse(menu.items[0].isEnabled)
        let attributes = menu.items[0].attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    /// Real data beats a stale failure.
    func testChartRowWinsOverAStaleFailure() {
        let state = connectedState()
        state.recordUsageFailure("connection refused")
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]
        let menu = histogramItem(makeSection(), state).submenu!

        open(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "CHART:24")
    }

    /// The row is read from AppState at OPEN time, not at build time — so a
    /// timeline that arrives while the menu sits closed is shown on the next
    /// open, and reopening replaces the row instead of appending to it.
    func testReopeningRereadsStateAndReplacesTheRow() {
        let state = connectedState()
        let menu = histogramItem(makeSection(), state).submenu!

        open(menu)
        XCTAssertEqual(menu.items[0].title, "Loading…")

        state.usageTimeline = []
        open(menu)

        XCTAssertEqual(menu.numberOfItems, 1, "reopening replaces the row, never appends")
        XCTAssertEqual(menu.items[0].title, "CHART:24", "an idle timeline is a flat axis, not a loading row")
    }

    /// Opening the submenu must not restructure the menu it hangs from. The
    /// whole point of the lazy build is that it touches the submenu and nothing
    /// else — a parent that grew, shrank or re-created its rows while the user
    /// had it open is the irritation `MenuRebuildGuard` exists to prevent.
    func testOpeningTheSubmenuDoesNotRestructureTheParentMenu() {
        let section = makeSection()
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]

        let parent = NSMenu()
        for item in section.items(for: state, now: Fixture.now) { parent.addItem(item) }
        let countBefore = parent.numberOfItems
        let itemsBefore = parent.items

        open(parent.items.first { $0.title == "Activity (24h)" }!.submenu!)

        XCTAssertEqual(parent.numberOfItems, countBefore)
        XCTAssertTrue(zip(parent.items, itemsBefore).allSatisfy { $0 === $1 },
                      "opening the submenu must not replace any row of the parent")
        XCTAssertTrue(section.updateInPlace(for: state, now: Fixture.now),
                      "and must not make the block look structurally different afterwards")
    }

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

    /// With no factory injected the submenu must still show a REAL chart. The
    /// seam this replaced was optional and nothing in production ever set it,
    /// so the shipped tray showed a text fallback and never a chart; a default
    /// that already works cannot fail that way.
    func testTheDefaultFactoryProducesTheRealChart() {
        let state = connectedState()
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]
        let menu = histogramItem(makeBareSection(), state).submenu!

        open(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].view?.frame.size, ActivityHistogram.chartItemSize)
        XCTAssertNotNil(menu.items[0].view?.accessibilityLabel())
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
        func activeSessions(limit: Int) async throws -> [APIClient.MCPSession] { [] }
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
@MainActor
final class ActivityHistogramEncodingTests: XCTestCase {

    /// Fraction of drawn pixels that read as saturated red, plus how much was
    /// drawn at all. Returns nil where nothing can be rasterised, so a machine
    /// that cannot draw skips instead of failing.
    private func redShare(of bars: [HistogramBar]) -> Double? {
        let host = NSHostingView(rootView: ActivityHistogramView(bars: bars, accessibilitySummary: ""))
        host.frame = NSRect(origin: .zero, size: ActivityHistogram.chartItemSize)
        host.layoutSubtreeIfNeeded()
        guard let rep = host.bitmapImageRepForCachingDisplay(in: host.bounds) else { return nil }
        host.cacheDisplay(in: host.bounds, to: rep)

        var drawn = 0
        var red = 0
        for x in stride(from: 0, to: Int(rep.size.width), by: 2) {
            for y in stride(from: 0, to: Int(rep.size.height), by: 2) {
                guard let colour = rep.colorAt(x: x, y: y), colour.alphaComponent > 0.5 else { continue }
                drawn += 1
                if colour.redComponent > 0.5,
                   colour.greenComponent < 0.45,
                   colour.blueComponent < 0.45 {
                    red += 1
                }
            }
        }
        guard drawn > 100 else { return nil }
        return Double(red) / Double(drawn)
    }

    /// Errors must be drawn in a visually distinct fill, and that fill must be
    /// driven by the DATA — an axis or legend that is red regardless would pass
    /// a "chart contains red" assertion while showing failures in the same
    /// colour as successes.
    func testErrorSegmentsAreDrawnDistinctlyFromSuccesses() throws {
        let hour = Fixture.currentHour
        guard let withErrors = redShare(of: [HistogramBar(hourStart: hour, succeeded: 7, errors: 3)]),
              let withoutErrors = redShare(of: [HistogramBar(hourStart: hour, succeeded: 10, errors: 0)])
        else {
            throw XCTSkip("this machine cannot rasterise the chart")
        }

        XCTAssertGreaterThan(
            withErrors, withoutErrors + 0.05,
            "an hour with errors must paint materially more of the error colour than one without"
        )
    }

    // A companion test — "the success fill does not follow the system accent"
    // — was attempted and DROPPED: `NSColor.currentControlTint` is get-only and
    // the accent is not settable from a test process, so there is no honest way
    // to render under a red accent. The guarantee is instead structural: the
    // view no longer references `Color.accentColor` at all. Verified by
    // inspection, not by the suite.
}
