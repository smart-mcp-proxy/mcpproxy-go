import XCTest
@testable import MCPProxy

/// The row pipeline replayed over the committed reference fixture — spec 090
/// SC-001 and SC-004.
///
/// The hand-written pipeline tests pin the rules one at a time on three or four
/// records. This one asks the question the rules exist to answer: run six weeks
/// of a real working laptop past the menu, one poll at a time, and check what a
/// user would actually have seen. 1,564 events, 52 blocks, 32 errors, and the
/// bursts that motivated grouping in the first place (one run of 100 identical
/// calls, which is the whole poll window).
///
/// The fixture is read from `specs/090-tray-glance-v2/fixtures/` rather than
/// bundled as a package resource: it is 860KB, it is the same file the spec
/// quotes its numbers from, and two copies of it would eventually disagree.
final class GlanceFixtureReplayTests: XCTestCase {

    // MARK: - Fixture

    /// Records in file order — newest first, as the activity API returns them.
    ///
    /// Decoded once for the whole suite. XCTest builds a fresh instance per test
    /// method, so a per-test decode would parse 1,564 JSON objects five times
    /// over for no benefit.
    private static let recordsNewestFirst: [ActivityEntry] = {
        let url = fixtureURL()
        // swiftlint:disable:next force_try
        let text = try! String(contentsOf: url, encoding: .utf8)
        let decoder = JSONDecoder()
        return text
            .split(separator: "\n")
            .filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
            // swiftlint:disable:next force_try
            .map { try! decoder.decode(ActivityEntry.self, from: Data($0.utf8)) }
    }()

    /// Oldest first — the order the events actually happened in, which is the
    /// order a replay has to follow.
    private static var recordsChronological: [ActivityEntry] {
        recordsNewestFirst.reversed()
    }

    /// Locate the fixture by walking up from this source file.
    ///
    /// Anchored on `#filePath`, not the working directory: CI runs `swift test`
    /// from `native/macos/MCPProxy`, a developer may run it from the repo root,
    /// and a path relative to either is a test that passes on one machine.
    private static func fixtureURL() -> URL {
        let relative = "specs/090-tray-glance-v2/fixtures/activity-replay.jsonl"
        var directory = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
        while directory.path != "/" {
            let candidate = directory.appendingPathComponent(relative)
            if FileManager.default.fileExists(atPath: candidate.path) { return candidate }
            directory = directory.deletingLastPathComponent()
        }
        fatalError("could not find \(relative) above \(#filePath)")
    }

    /// The window one poll sees at `step`: the newest 100 records up to and
    /// including that event, newest first (`AppState.glanceActivityPageSize`).
    private static func pollWindow(endingAt step: Int) -> [ActivityEntry] {
        let chronological = recordsChronological
        let start = max(0, step - AppState.glanceActivityPageSize + 1)
        return Array(chronological[start...step].reversed())
    }

    // MARK: - Fixture integrity

    /// The fixture is the evidence behind the spec's numbers, so a swap that
    /// quietly changed its shape would weaken every assertion below without
    /// failing anything.
    func testTheFixtureIsTheOneTheSpecDescribes() {
        let records = Self.recordsNewestFirst
        XCTAssertEqual(records.count, 1564)
        XCTAssertEqual(records.filter { $0.status == "blocked" }.count, 52)
        XCTAssertEqual(records.filter { $0.status == "error" }.count, 32)
        XCTAssertEqual(records.filter { $0.status == "success" }.count, 1480)
        XCTAssertEqual(records.filter { $0.type == ActivityEntry.policyDecisionType }.count, 52)
        XCTAssertGreaterThan(records.first?.timestamp ?? "", records.last?.timestamp ?? "",
                             "the fixture is stored newest-first, like the API's own order")
    }

    // MARK: - SC-001

    /// Two adjacent rows saying the same thing is the failure grouping exists to
    /// prevent, and it is a property of every poll, not of a lucky one — so it
    /// is asserted at all 1,564 steps of the replay.
    func testNoTwoAdjacentRowsEverShareAGroupKey() {
        for step in Self.recordsChronological.indices {
            let rows = GlanceSelection.activityRows(from: Self.pollWindow(endingAt: step))
            XCTAssertLessThanOrEqual(rows.count, GlanceSelection.rowLimit)
            for (upper, lower) in zip(rows, rows.dropFirst()) where upper.key == lower.key {
                XCTFail("step \(step): adjacent rows share \(upper.key)")
            }
        }
    }

    /// The headline complaint, replayed: when a burst of ≥19 identical calls is
    /// what just happened, it takes ONE of the five rows and the other four
    /// still show the tools around it. Ungrouped, the same window filled every
    /// row with the same tool.
    func testALongBurstOccupiesExactlyOneRow() {
        var burstSteps = 0
        var stepsWhereGroupingFreedRows = 0
        var largest = 0

        for step in Self.recordsChronological.indices {
            let window = Self.pollWindow(endingAt: step)
            let rows = GlanceSelection.activityRows(from: window)
            guard let burst = rows.first, burst.count >= 19 else { continue }
            burstSteps += 1
            largest = max(largest, burst.count)

            // What the menu showed before grouping: the five newest surviving
            // records, every one of them the same tool. That is the complaint.
            let ungrouped = GlanceSelection
                .collapseByRequestID(window.filter(GlanceSelection.qualifies))
                .prefix(GlanceSelection.rowLimit)
            XCTAssertTrue(ungrouped.allSatisfy { GlanceRun.Key(for: $0) == burst.key },
                          "step \(step): precondition — ungrouped, the burst filled every row")

            // Grouped, the whole burst is one row of the five…
            XCTAssertEqual(rows.filter { $0.identity == burst.identity }.count, 1,
                           "step \(step): a run must not be split across rows")
            XCTAssertLessThanOrEqual(rows.count, GlanceSelection.rowLimit)

            // …and whenever the window holds anything else at all, the freed
            // rows go to it, which is the point of freeing them.
            let holdsOtherTools = GlanceSelection
                .collapseByRequestID(window.filter(GlanceSelection.qualifies))
                .contains { GlanceRun.Key(for: $0) != burst.key }
            if holdsOtherTools {
                stepsWhereGroupingFreedRows += 1
                XCTAssertGreaterThan(rows.count, 1,
                                     "step \(step): the burst swallowed the whole menu")
                XCTAssertTrue(rows.dropFirst().contains { $0.key != burst.key },
                              "step \(step): the other tools never got a row")
            }
        }

        XCTAssertGreaterThan(burstSteps, 0, "the fixture is supposed to contain long bursts")
        XCTAssertGreaterThan(stepsWhereGroupingFreedRows, 0,
                             "no step exercised the 'other tools fill the freed rows' case")
        XCTAssertGreaterThanOrEqual(largest, 19)
    }

    /// The other half of the same story: the row says how many, and the count is
    /// capped by the poll window rather than claiming knowledge of records the
    /// menu never fetched (spec Edge Cases).
    func testACountNeverExceedsTheFetchedWindow() {
        for step in Self.recordsChronological.indices {
            for row in GlanceSelection.activityRows(from: Self.pollWindow(endingAt: step)) {
                XCTAssertLessThanOrEqual(row.count, AppState.glanceActivityPageSize,
                                         "step \(step): a run claimed more records than were fetched")
            }
        }
    }

    // MARK: - SC-004

    /// Every one of the 52 policy blocks becomes visible at some point in the
    /// replay. Before this feature that number was zero — the poll's type filter
    /// excluded policy decisions and the row rules would have rejected them
    /// anyway, so six weeks of the proxy protecting the user left no trace in
    /// the menu.
    func testEveryPolicyBlockBecomesVisibleAtSomeStep() {
        var seen = Set<String>()

        for step in Self.recordsChronological.indices {
            let rows = GlanceSelection.activityRows(from: Self.pollWindow(endingAt: step))
            for row in rows where row.key.outcomeClass == .blocked {
                seen.formUnion(row.records.map(GlanceSelection.recordKey))
            }
        }

        let blocked = Self.recordsNewestFirst
            .filter { $0.outcomeClass == .blocked }
            .map(GlanceSelection.recordKey)
        XCTAssertEqual(blocked.count, 52)
        XCTAssertEqual(seen.count, blocked.count)
        XCTAssertTrue(seen.isSuperset(of: blocked),
                      "a block the user never saw is a block that may as well not have happened")
    }

    /// …and a block is visible as a *block*: it never merges into a run of
    /// ordinary calls to the same tool, which would hide it inside a ×N row that
    /// reads as ordinary traffic (FR-002).
    func testBlockedRunsNeverContainCalls() {
        for step in Self.recordsChronological.indices {
            for row in GlanceSelection.activityRows(from: Self.pollWindow(endingAt: step)) {
                let classes = Set(row.records.map(\.outcomeClass))
                XCTAssertEqual(classes.count, 1, "step \(step): a run mixed outcome classes")
            }
        }
    }
}
