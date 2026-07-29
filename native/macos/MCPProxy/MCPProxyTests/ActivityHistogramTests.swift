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
