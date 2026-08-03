import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class GlanceSectionTests: XCTestCase {

    /// Fixed clock so relative ages are deterministic.
    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    // MARK: - Visibility

    func testBlockIsHiddenWhenCoreIsNotConnected() {
        let state = Self.busyState()
        state.coreState = .idle
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    func testBlockIsHiddenWhenUserStoppedTheCore() {
        let state = Self.busyState()
        state.isStopped = true
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    // MARK: - Header

    func testHeaderShowsCallsThisHourAndClientCount() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items.first?.title, "12 calls in the last 24h · 1 active")
        XCTAssertFalse(items[0].isEnabled, "the header is a muted, non-clickable line")
    }

    func testHeaderOmitsCallCountUntilUsageLoads() {
        let state = Self.busyState()
        state.usageTimeline = nil
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).first?.title, "1 active")
    }

    /// When the feeds stop arriving the header says so. The block is otherwise
    /// indistinguishable from a healthy one: the rows keep re-rendering with a
    /// fresh clock every 30 seconds, so a dead core presents as a live, ticking
    /// display of numbers that stopped being true minutes ago.
    func testHeaderAdmitsWhenTheFeedsHaveStoppedArriving() {
        let state = Self.busyState()
        for _ in 0..<AppState.glanceStaleFailureThreshold {
            state.recordGlanceFailure(.activity, "connection refused")
        }
        let section = Self.makeSection()

        XCTAssertEqual(section.items(for: state, now: Self.now).first?.title,
                       "12 calls in the last 24h · 1 active · not updating")
    }

    /// …and stops saying so once the feeds recover, without a rebuild: the
    /// header is one of the rows `updateInPlace` rewrites.
    func testTheHeaderDropsTheMarkerInPlaceWhenTheFeedsRecover() {
        let state = Self.busyState()
        for _ in 0..<AppState.glanceStaleFailureThreshold {
            state.recordGlanceFailure(.activity, "connection refused")
        }
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.clearGlanceFailure(.activity)

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "12 calls in the last 24h · 1 active")
    }

    // MARK: - Recent section

    func testRecentSectionRendersQualifyingRows() {
        let section = Self.makeSection()
        let titles = section.items(for: Self.busyState(), now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.prefix(7)), [
            "12 calls in the last 24h · 1 active",
            "Activity (24h)",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue — 2m",
            "Open Activity…"
        ])
    }

    func testActivityRowCarriesFullIdentity() {
        let section = Self.makeSection()
        let failed = section.items(for: Self.busyState(), now: Self.now)[5]
        XCTAssertEqual(failed.title, "jira:get_issue — 2m")
        XCTAssertEqual(failed.representedObject as? String, "sess-b")
        XCTAssertEqual(failed.image?.accessibilityDescription, "failed")
        XCTAssertEqual(failed.toolTip, "jira:get_issue\nauth failed: token expired. retry after refresh")
        XCTAssertEqual(failed.accessibilityLabel(), "jira:get_issue, failed: auth failed, 2m ago")
        XCTAssertNotNil(failed.action)
    }

    func testOpenActivityRowHasNoSessionPayload() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items[6].title, "Open Activity…")
        XCTAssertNil(items[6].representedObject)
        XCTAssertNotNil(items[6].action)
    }

    /// One frame everywhere: with nothing inside the 24 hours the Recent
    /// header would explain an empty list, so the section is just the door to
    /// the full log.
    func testNoQualifyingActivityHidesTheRecentSection() {
        let state = Self.busyState()
        state.glanceActivity = []
        let section = Self.makeSection()
        let titles = section.items(for: state, now: Self.now).map(\.title)
        XCTAssertFalse(titles.contains("Recent"))
        XCTAssertFalse(titles.contains("No tool calls yet"))
        XCTAssertTrue(titles.contains("Open Activity…"),
                      "the door to the full log stays")
    }

    /// The frame in action: a record the log retains from days ago must not
    /// sit beside a chart that says the day was quiet.
    func testRowsOlderThanTheFrameAreNotShown() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "old", server: "github", tool: "create_issue",
                       timestamp: "2027-01-05T08:00:00Z", session: "sess-old")
        ]
        let section = Self.makeSection()
        let titles = section.items(for: state, now: Self.now).map(\.title)
        XCTAssertFalse(titles.contains { $0.hasPrefix("github:create_issue") })
        XCTAssertFalse(titles.contains("Recent"))
    }

    func testFirstClauseKeepsOnlyTheLeadingClause() {
        XCTAssertEqual(GlanceSection.firstClause(of: "auth failed: token expired"), "auth failed")
        XCTAssertEqual(GlanceSection.firstClause(of: "  boom  "), "boom")
        XCTAssertEqual(GlanceSection.firstClause(of: "timed out.\nretrying"), "timed out")
        XCTAssertEqual(GlanceSection.firstClause(of: "not found."), "not found")
        XCTAssertNil(GlanceSection.firstClause(of: "   "))
        XCTAssertNil(GlanceSection.firstClause(of: nil))
    }

    /// A separator only ends a clause when something separates because of it
    /// (GH #934). mcpproxy names tools `server:tool` and hosts `127.0.0.1`, and
    /// cutting at a bare `:` or `.` lands inside both — the observed rendering
    /// of `invalid arguments for memory:create_entities: …` was "invalid
    /// arguments for memory", which reads as an accusation against the server.
    func testFirstClauseDoesNotCutInsideAnIdentifier() {
        XCTAssertEqual(
            GlanceSection.firstClause(
                of: "invalid arguments for memory:create_entities: at '/entities': "
                    + "got string, want array"
            ),
            "invalid arguments for memory:create_entities"
        )
        XCTAssertEqual(GlanceSection.firstClause(of: "dial tcp 127.0.0.1"), "dial tcp 127.0.0.1")
        XCTAssertEqual(GlanceSection.firstClause(of: "dial tcp 127.0.0.1:8080: refused"),
                       "dial tcp 127.0.0.1:8080")
        XCTAssertEqual(GlanceSection.firstClause(of: "read file a.txt failed: EACCES"),
                       "read file a.txt failed")
    }

    // MARK: - Grouped rows (spec 090 US1)

    /// A burst of one tool is one row, and the rest of the page still gets rows.
    func testARunOfIdenticalCallsRendersOneRowWithACountSuffix() {
        let state = Self.burstState()
        let section = Self.makeSection()
        let titles = section.items(for: state, now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.dropFirst(4).prefix(2)), [
            "jira:get_issue ×3 — 30s",
            "github:create_issue — 3m"
        ])
    }

    func testTheRowsAgeComesFromTheNewestRecordOfTheRun() {
        let section = Self.makeSection()
        let row = section.items(for: Self.burstState(), now: Self.now)[4]
        XCTAssertTrue(row.title.hasSuffix("— 30s"),
                      "the run's clock is its newest record, not its oldest")
    }

    func testASingleCallRunCarriesNoCountSuffix() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[4]
        XCTAssertEqual(row.title, "github:create_issue — 30s")
    }

    /// FR-004: one failure marks the whole run, with the NEWEST failure's clause.
    func testAFailureInsideARunMarksTheRowWithTheNewestErrorClause() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "n1", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-n1"),
            Self.entry(id: "n2", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired",
                       timestamp: "2027-01-15T07:59:00Z", session: "sess-n2"),
            Self.entry(id: "n3", server: "jira", tool: "get_issue", status: "error",
                       error: "older failure: ignore me",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-n3")
        ]
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.title, "jira:get_issue ×3 — 30s")
        XCTAssertEqual(Self.subtitle(of: row), "auth failed",
                       "the newest failure's clause takes the second line")
        XCTAssertEqual(row.image?.accessibilityDescription, "failed")
        XCTAssertEqual(row.toolTip, "jira:get_issue\nauth failed: token expired")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, repeated 3 times, failed: auth failed, 30s ago")
    }

    func testTheRepeatCountIsSpokenNotJustDrawn() {
        let section = Self.makeSection()
        let row = section.items(for: Self.burstState(), now: Self.now)[4]
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, repeated 3 times, succeeded, 30s ago")
    }

    /// The run's identity is its OLDEST record (FR-024), so a burst growing at
    /// the head is the same row with a bigger count — not a turnover. A turnover
    /// would rewrite the icon and click payload of a row under the user's cursor
    /// on every single call of a 19-call burst.
    func testARunGrowingAtTheHeadUpdatesInPlaceWithoutTurnover() {
        let state = Self.burstState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let iconBefore = items[4].image

        state.glanceActivity.insert(
            Self.entry(id: "j0", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:59:50Z", session: "sess-j0"),
            at: 0)

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[4].title, "jira:get_issue ×4 — 10s")
        XCTAssertTrue(items[4].image === iconBefore,
                      "the same run must keep its icon; only the count and clock moved")
        XCTAssertEqual(items[4].representedObject as? String, "sess-j0",
                       "the click payload follows the run's newest record")
    }

    /// …and when the row really does come to stand for a different run — a new
    /// run whose oldest record is not the previous one — the whole identity is
    /// rewritten, icon and click payload included.
    /// The replacement run is a failing one on purpose: with failure-only marks
    /// a successful row has no icon at all, so success→success could not show
    /// that the icon is rewritten with the rest of the identity.
    func testADifferentRunInTheSameSlotRewritesTheRowIdentity() {
        let state = Self.burstState()
        let section = Self.makeSection()
        // One-line rows on purpose: with subtitles, the failing replacement
        // would also gain a line (structural, deferred); the turnover itself
        // is what this test pins.
        section.supportsRowSubtitles = false
        let items = section.items(for: state, now: Self.now)
        let iconBefore = items[4].image
        XCTAssertNil(iconBefore, "precondition: the successful burst row is unmarked")

        state.glanceActivity = [
            Self.entry(id: "o1", server: "obsidian", tool: "search_notes", status: "error",
                       error: "vault locked", timestamp: "2027-01-15T07:59:55Z", session: "sess-o1"),
            Self.entry(id: "o2", server: "obsidian", tool: "search_notes", status: "error",
                       error: "vault locked", timestamp: "2027-01-15T07:59:50Z", session: "sess-o2"),
            state.glanceActivity[3]
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[4].title, "obsidian:search_notes ×2 · vault locked — 5s")
        XCTAssertEqual(items[4].image?.accessibilityDescription, "failed",
                       "a different run must rewrite the row's entire identity, icon included")
        XCTAssertEqual(items[4].representedObject as? String, "sess-o1")
    }

    /// A late status correction on the run's newest record still lands: "same
    /// run" must not mean "skip the update".
    func testASameRunStillPicksUpALateFailure() {
        let state = Self.burstState()
        let section = Self.makeSection()
        // One-line rows: with subtitles the late failure would gain a line,
        // which is the structural case the next test pins.
        section.supportsRowSubtitles = false
        let items = section.items(for: state, now: Self.now)

        state.glanceActivity[0] = Self.entry(
            id: "j1", server: "jira", tool: "get_issue", status: "error",
            error: "rate limited: try later",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-j1")

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[4].title, "jira:get_issue ×3 · rate limited — 30s")
        XCTAssertEqual(items[4].image?.accessibilityDescription, "failed")
    }

    /// With subtitles available, a late failure ADDS the error line — a row
    /// growing a line resizes the menu, so it is structural and waits for
    /// close (FR-023), exactly like a run gaining its reason.
    func testALateFailureGainingItsErrorLineIsStructural() {
        let state = Self.burstState()
        let section = Self.makeSection()
        _ = section.items(for: state, now: Self.now)

        state.glanceActivity[0] = Self.entry(
            id: "j1", server: "jira", tool: "get_issue", status: "error",
            error: "rate limited: try later",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-j1")

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now))
    }

    // MARK: - Reason subtitles (spec 090 US2)

    /// The reason is the row's standard subtitle — a subdued second line of the
    /// SAME menu item, not a view-backed row (FR-005/FR-009), so keyboard
    /// navigation and VoiceOver are untouched.
    func testARowWithAReasonRendersItAsTheSubtitle() {
        let state = Self.reasonState()
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.title, "jira:get_issue — 30s", "the reason never joins the title line")
        XCTAssertEqual(Self.subtitle(of: row), "Verify the ticket is still open")
    }

    /// FR-006: the subtitle has its OWN 60-character budget, and the full text
    /// stays reachable in the tooltip.
    func testALongReasonIsTailTruncatedToTheSubtitleBudget() {
        let long = "Handoff: move the ticket to review per the user's request and notify the reporter"
        let state = Self.reasonState(reason: long)
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        let subtitle = Self.subtitle(of: row)
        XCTAssertEqual(subtitle?.count, GlanceFormatting.reasonBudget)
        XCTAssertEqual(subtitle?.hasSuffix("\u{2026}"), true)
        XCTAssertEqual(row.toolTip, "jira:get_issue\n\(long)",
                       "the tooltip carries the reason in full")
    }

    /// FR-007: no reason means one line, not an empty second one.
    func testARowWithoutAReasonHasNoSubtitle() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[4]
        XCTAssertNil(Self.subtitle(of: row))
    }

    /// Below macOS 14.4 the subtitle mechanism does not exist, so the row is
    /// single-line — but the reason must NOT be lost: tooltip and VoiceOver
    /// carry it on every macOS version (FR-005/FR-006, US2 scenario 7).
    func testWithoutTheSubtitleMechanismTheRowIsSingleLineAndKeepsTheReason() {
        let state = Self.reasonState()
        let section = Self.makeSection()
        section.supportsRowSubtitles = false
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertNil(Self.subtitle(of: row))
        XCTAssertEqual(row.title, "jira:get_issue — 30s")
        XCTAssertEqual(row.toolTip, "jira:get_issue\nVerify the ticket is still open")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, succeeded, 30s ago, reason: Verify the ticket is still open")
    }

    /// US2 scenario 4: the run's reason is the NEWEST record that has one — a
    /// later call that omitted its intent must not blank the row out.
    func testAGroupedRowShowsTheNewestReasonInTheRun() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "r1", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-r1"),
            Self.entry(id: "r2", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:59:00Z", session: "sess-r2",
                       reason: "Check the ticket after the failed transition"),
            Self.entry(id: "r3", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:58:30Z", session: "sess-r3",
                       reason: "An older reason nobody should see")
        ]
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.title, "jira:get_issue ×3 — 30s")
        XCTAssertEqual(Self.subtitle(of: row), "Check the ticket after the failed transition")
    }

    /// FR-011a (compact revision): one fact per line. On a failed row the
    /// error clause takes the second line — "how it went" outranks "why it was
    /// attempted" once something is wrong — and the reason stays reachable in
    /// the tooltip and spoken by VoiceOver.
    func testAFailedRowShowsTheErrorAsTheSubtitleAndKeepsTheReasonInTheTooltip() {
        let state = Self.reasonState(status: "error", error: "auth failed: token expired")
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.title, "jira:get_issue — 30s",
                       "error prose never widens the title line")
        XCTAssertEqual(Self.subtitle(of: row), "auth failed")
        XCTAssertTrue(row.toolTip?.contains("Verify the ticket is still open") == true,
                      "the displaced reason stays in the tooltip")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, failed: auth failed, 30s ago, "
                       + "reason: Verify the ticket is still open")
    }

    /// Truncation precedence on the pre-14.4 fallback (the only path where the
    /// clause still shares the title): the clause is cut to its own budget,
    /// the label keeps its middle-truncation budget, and the age is never cut.
    func testTheErrorClauseIsCutToItsOwnBudgetWhileTheLabelKeepsIts() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "e1",
                       server: "atlassian-jira-cloud",
                       tool: "transition_issue_with_fields",
                       status: "error",
                       error: "the upstream server rejected the transition because the field is required",
                       timestamp: "2027-01-15T07:59:30Z",
                       session: "sess-e1")
        ]
        let section = Self.makeSection()
        section.supportsRowSubtitles = false
        let title = section.items(for: state, now: Self.now)[4].title

        let label = String(title.prefix(while: { $0 != "·" })).trimmingCharacters(in: .whitespaces)
        let clause = title
            .components(separatedBy: " · ")[1]
            .components(separatedBy: " — ")[0]
        XCTAssertEqual(label.count, 30, "the label budget must not be tightened by a long error")
        XCTAssertEqual(clause.count, GlanceFormatting.errorClauseBudget)
        XCTAssertTrue(clause.hasSuffix("\u{2026}"))
        XCTAssertTrue(title.hasSuffix(" — 30s"), "the age is never truncated")
    }

    /// The tooltip is the row's full text: label, reason and the whole error
    /// message, none of them truncated (FR-011a).
    func testTheTooltipCarriesTheFullLabelReasonAndErrorMessage() {
        let state = Self.reasonState(status: "error",
                                     error: "auth failed: token expired. retry after refresh")
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.toolTip,
                       "jira:get_issue\nVerify the ticket is still open\n"
                       + "auth failed: token expired. retry after refresh")
    }

    // MARK: - Clients section and histogram

    func testClientRowCarriesSessionIdentity() {
        let section = Self.makeSection()
        let client = section.items(for: Self.busyState(), now: Self.now)[9]
        XCTAssertEqual(client.title, "Claude Code — 8 calls")
        XCTAssertEqual(client.representedObject as? String, "sess-a")
        XCTAssertEqual(client.toolTip, "Claude Code 2.1.0")
        XCTAssertEqual(client.accessibilityLabel(),
                       "Claude Code, 8 calls, active, last active 1m ago")
    }

    /// FR-020: the placeholder is a statement about the last 24 hours, not about
    /// this instant — which is the whole fix. It appeared every time the last
    /// session timed out, saying "nothing is connected" about a proxy three
    /// clients had used that morning.
    func testThePlaceholderAppearsOnlyWhenNothingIsInsideTheLookback() {
        let state = Self.busyState()
        state.glanceSessions = [
            Self.session(id: "sess-old", name: "Claude Code", version: "2.1.0",
                         calls: 8, lastActivity: "2027-01-13T08:00:00Z")
        ]
        let section = Self.makeSection()

        let row = section.items(for: state, now: Self.now)[9]
        XCTAssertEqual(row.title, "No clients in the last 24h")
        XCTAssertFalse(row.isEnabled)

        state.glanceSessions = [
            Self.session(id: "sess-yesterday", name: "Claude Code", version: "2.1.0",
                         calls: 8, lastActivity: "2027-01-15T05:00:00Z")
        ]
        XCTAssertEqual(section.items(for: state, now: Self.now)[9].title,
                       "Claude Code — 8 calls · seen 3h",
                       "a client from three hours ago is a row, not an empty state")
    }

    func testNoSessionsAtAllShowsThePlaceholder() {
        let state = Self.busyState()
        state.glanceSessions = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[9]
        XCTAssertEqual(row.title, "No clients in the last 24h")
        XCTAssertFalse(row.isEnabled)
    }

    /// FR-018: each presence state carries its own indicator and, once the
    /// client has gone quiet, the time since it was last heard from. The
    /// indicators differ in SHAPE, not only in colour — a greyscale display or a
    /// red-green deficiency must not collapse three states into one.
    func testEachPresenceStateGetsItsOwnIndicatorAndAge() {
        let state = Self.busyState()
        state.glanceSessions = [
            Self.session(id: "s-active", name: "Claude Code", version: "2.1.0",
                         calls: 8, lastActivity: "2027-01-15T07:59:00Z"),
            Self.session(id: "s-idle", name: "Cursor", version: "1.0.0",
                         calls: 3, lastActivity: "2027-01-15T07:40:00Z"),
            Self.session(id: "s-seen", name: "Codex", version: "0.9.0",
                         calls: 1, lastActivity: "2027-01-15T05:00:00Z")
        ]
        let section = Self.makeSection()
        let rows = Array(section.items(for: state, now: Self.now)[9...11])

        XCTAssertEqual(rows.map(\.title), [
            "Claude Code — 8 calls",
            "Cursor — 3 calls · idle 20m",
            "Codex — 1 call · seen 3h"
        ])
        XCTAssertEqual(rows.map { $0.image?.accessibilityDescription },
                       ["active", "idle", "seen"])
        XCTAssertEqual(rows.map { $0.accessibilityLabel() }, [
            "Claude Code, 8 calls, active, last active 1m ago",
            "Cursor, 3 calls, idle, last active 20m ago",
            "Codex, 1 call, seen, last active 3h ago"
        ])
        XCTAssertEqual(Set(ClientPresence.allCases.map(GlanceSection.presenceSymbolName)).count, 3,
                       "shape alone must separate the three presence states")
    }

    /// The rows are ordered by activity, and a client that reconnected several
    /// times is one row — the section describes clients, not sockets.
    func testClientRowsAreDedupedAndOrderedByRecency() {
        let state = Self.busyState()
        state.glanceSessions = [
            Self.session(id: "old", name: "Claude Code", version: "2.1.0",
                         calls: 2, lastActivity: "2027-01-15T07:30:00Z"),
            Self.session(id: "new", name: "Claude Code", version: "2.1.0",
                         calls: 9, lastActivity: "2027-01-15T07:59:30Z"),
            Self.session(id: "cursor", name: "Cursor", version: "1.0.0",
                         calls: 4, lastActivity: "2027-01-15T07:59:00Z")
        ]
        let section = Self.makeSection()
        let rows = Array(section.items(for: state, now: Self.now)[9...10])

        XCTAssertEqual(rows.map { $0.representedObject as? String }, ["new", "cursor"])
        XCTAssertEqual(rows.map(\.title), ["Claude Code — 9 calls", "Cursor — 4 calls"])
    }

    /// FR-019: "seen" clients keep their rows but stay out of the headline, so
    /// this summary has no client segment at all.
    func testASeenOnlyFeedLeavesTheSummaryWithoutAClientSegment() {
        let state = Self.busyState()
        state.glanceSessions = [
            Self.session(id: "s-seen", name: "Codex", version: "0.9.0",
                         calls: 1, lastActivity: "2027-01-15T05:00:00Z")
        ]
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        XCTAssertEqual(items[0].title, "12 calls in the last 24h")
        XCTAssertEqual(items[9].title, "Codex — 1 call · seen 3h")
    }

    /// The histogram renders inline: until the usage feed arrives its row is a
    /// muted placeholder, read straight out of `items(for:)` — there is no
    /// submenu to open any more.
    func testHistogramShowsLoadingUntilUsageArrives() {
        let state = Self.busyState()
        state.usageTimeline = nil
        let section = Self.makeSection()
        let histogram = section.items(for: state, now: Self.now)[1]
        XCTAssertEqual(histogram.title, "Activity (24h) — loading…")
        XCTAssertNil(histogram.submenu)
    }

    func testHistogramUsesInjectedViewWhenAvailable() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        // The seam takes the shaped 24-hour axis and returns the whole item —
        // so the count below is the axis width, not the timeline length.
        section.histogramChartItemFactory = { bars in
            let item = NSMenuItem(title: "", action: nil, keyEquivalent: "")
            let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 90))
            view.setAccessibilityLabel("\(bars.count) bars")
            item.view = view
            return item
        }
        let chart = section.items(for: state, now: Self.now)[1]
        XCTAssertNotNil(chart.view)
        XCTAssertEqual(chart.view?.accessibilityLabel(), "24 bars")
    }

    // `testHistogramSubmenuFallsBackToTextWithoutABuilder` was REMOVED here, not
    // ported: it asserted the text row shown when no view builder was injected,
    // and there is no longer a builder-less mode to assert. The seam is
    // non-optional and defaults to the real chart, precisely because the old
    // optional seam was never set in production and the tray therefore shipped
    // that text fallback and never a chart. Its successor —
    // "with nothing injected, the submenu shows a real chart" — is
    // `GlanceHistogramSubmenuTests.testTheDefaultFactoryProducesTheRealChart`.

    /// FR-021: the day-level picture comes before the call-level detail. The
    /// histogram is the best single answer to "what has been happening?", and it
    /// used to sit at the very bottom, below every row it summarises — the user
    /// had to read past the detail to reach the overview.
    func testBlockLayoutOrder() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        let titles = items.map { $0.isSeparatorItem ? "—" : $0.title }
        XCTAssertEqual(titles, [
            "12 calls in the last 24h · 1 active",
            "Activity (24h)",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue — 2m",
            "Open Activity…",
            "—",
            "Clients",
            "Claude Code — 8 calls",
            "—"
        ])
    }

    /// The histogram sits with the summary, above the separator that opens the
    /// detail — "directly below the summary line and above the Recent header"
    /// is a statement about neighbours, not merely about relative order.
    func testTheHistogramIsTheSummarysNeighbour() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)

        XCTAssertEqual(items[0].title, "12 calls in the last 24h · 1 active")
        XCTAssertEqual(items[1].title, "Activity (24h)",
                       "busyState's loaded timeline puts the real chart here")
        XCTAssertNil(items[1].submenu, "the histogram renders inline, not behind a submenu")
        XCTAssertTrue(items[2].isSeparatorItem)
        XCTAssertEqual(items[3].title, "Recent")
    }

    // MARK: - In-place updates

    func testUpdateInPlaceRewritesTheEntireRowIdentity() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let row = items[4]

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            Self.entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 13, errors: 0, totalRespBytes: 0)]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls in the last 24h · 1 active")
        XCTAssertEqual(row.title, "obsidian:search_notes — 5s")
        XCTAssertEqual(row.representedObject as? String, "sess-c",
                       "the click payload must follow the title, or the row opens the previous record's session")
        XCTAssertNil(row.image, "a successful row carries no mark")
        XCTAssertEqual(row.toolTip, "obsidian:search_notes")
        XCTAssertEqual(row.accessibilityLabel(), "obsidian:search_notes, succeeded, 5s ago")
    }

    func testUpdateInPlaceRefusesStructuralChange() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.glanceActivity = [state.glanceActivity[0]]

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now),
                       "a row-count change must defer a rebuild, not mutate an open menu")
        XCTAssertEqual(items[4].title, "github:create_issue — 30s", "rows must be left untouched")
    }

    /// The first usage fetch landing while the menu is open must NOT report
    /// structural. The submenu is filled in by its delegate when it opens, so a
    /// timeline that arrives afterwards changes nothing about the built
    /// structure — and reporting structural here cost the user live rows for
    /// the whole menu session, because the deferral it triggered suppressed the
    /// one call (`items(for:)`) that could clear it. Opening the menu shortly
    /// after launch or reconnect hit exactly that.
    func testTheTimelineArrivingWhileTheMenuIsOpenKeepsRowsUpdating() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.usageTimeline = [UsageBucket(start: Self.now, calls: 13, errors: 0, totalRespBytes: 0)]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls in the last 24h · 1 active")

        // And it is still updating a cycle later: the freeze was for the rest
        // of the session, not for one tick.
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 14, errors: 0, totalRespBytes: 0)]
        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "14 calls in the last 24h · 1 active")
    }

    func testUpdateInPlaceBeforeFirstBuildReportsStructural() {
        XCTAssertFalse(Self.makeSection().updateInPlace(for: Self.busyState(), now: Self.now))
    }

    // MARK: - Rows are keyed on request id, not id

    /// A row rendered from a live SSE event carries a provisional id
    /// (`"<request_id>:<type>"`); the 30-second reconciling poll replaces it
    /// with the storage-assigned ULID for the very same call. Keyed on `id`,
    /// every poll would look like a wholesale turnover of all five rows.
    func testReconcileIdTurnoverIsNotARecordTurnover() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let iconBefore = items[4].image

        state.glanceActivity = [
            Self.entry(id: "01JQ8Z0000000000000000001", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-a", request: "req-a"),
            Self.entry(id: "01JQ8Z0000000000000000002", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b", request: "req-b")
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertTrue(items[4].image === iconBefore,
                      "same request id means the same record, so the row's icon must be left alone")
        XCTAssertEqual(items[4].title, "github:create_issue — 30s")
        XCTAssertEqual(items[4].representedObject as? String, "sess-a")
    }

    func testDifferentRecordInTheSameSlotRewritesTheIcon() {
        let state = Self.busyState()
        let section = Self.makeSection()
        // One-line rows: the failing replacement would otherwise also gain a
        // line (structural, deferred); the identity rewrite is what this pins.
        section.supportsRowSubtitles = false
        let items = section.items(for: state, now: Self.now)
        let previousFailure = state.glanceActivity[1]
        XCTAssertNil(items[4].image, "precondition: the successful row is unmarked")

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes", status: "error",
                       error: "vault locked", timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            previousFailure
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[4].image?.accessibilityDescription, "failed",
                       "a different record must rewrite the row's entire identity, icon included")
        XCTAssertEqual(items[4].representedObject as? String, "sess-c")
    }

    /// "Same record" must not mean "skip the update": the final status arrives
    /// on a record whose request id has not changed — the reason
    /// `AppState.updateGlanceActivity` fingerprints status rather than ids.
    func testSameRecordStillPicksUpALateStatusCorrection() {
        let state = Self.busyState()
        let section = Self.makeSection()
        // One-line rows, so the late failure rewrites in place instead of
        // gaining a line (the structural case has its own test).
        section.supportsRowSubtitles = false
        let items = section.items(for: state, now: Self.now)
        let previousFailure = state.glanceActivity[1]

        state.glanceActivity = [
            Self.entry(id: "a", server: "github", tool: "create_issue", status: "error",
                       error: "rate limited: try later",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            previousFailure
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[4].title, "github:create_issue · rate limited — 30s")
        XCTAssertEqual(items[4].image?.accessibilityDescription, "failed")
        XCTAssertEqual(items[4].toolTip, "github:create_issue\nrate limited: try later")
    }

    // MARK: - Client rows are not rewritten when nothing changed

    /// `updateInPlace` fires on nearly every 30s poll for a busy proxy — and
    /// under an open menu, where each write is a re-layout. A poll that returns
    /// the same session byte for byte must therefore write nothing at all, and
    /// in particular must not allocate a fresh tinted icon: the connected dot is
    /// a constant.
    func testIdenticalClientPollWritesNothing() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[9]
        let dotBefore = row.image

        var titleWrites = 0
        let observation = row.observe(\.title, options: [.new]) { _, _ in titleWrites += 1 }
        state.glanceSessions = [
            Self.session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                         calls: 8, lastActivity: "2027-01-15T07:59:00Z")
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        observation.invalidate()

        XCTAssertTrue(row.image === dotBefore,
                      "the presence dot is a constant per state — an identical poll must not allocate a new one")
        XCTAssertEqual(titleWrites, 0,
                       "an identical poll must not rewrite the title of a row in an open menu")
        XCTAssertEqual(row.title, "Claude Code — 8 calls")
    }

    /// …and the guards must not freeze the row: the live call count is the whole
    /// reason `MCPSession` became `Equatable`.
    func testChangedClientCallCountStillRewritesTheRow() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[9]

        state.glanceSessions = [
            Self.session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                         calls: 40, lastActivity: "2027-01-15T07:59:00Z")
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(row.title, "Claude Code — 40 calls")
        XCTAssertEqual(row.accessibilityLabel(),
                       "Claude Code, 40 calls, active, last active 1m ago")
    }

    // MARK: - Only failures are marked (spec 090 US3)

    /// FR-010: 95% of rows succeed, so a green tick on nearly every one carries
    /// no information — it only dilutes the marks that matter. The outcome is
    /// still announced, so nothing is lost to VoiceOver.
    func testASuccessfulRowCarriesNoStatusIcon() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[4]

        XCTAssertNil(row.image, "a quiet row is the whole point of failure-only marks")
        XCTAssertEqual(row.accessibilityLabel(), "github:create_issue, succeeded, 30s ago")
    }

    /// FR-011: a failure keeps its red cross and its error clause.
    func testAFailedRowCarriesTheFailureMark() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[5]

        XCTAssertEqual(row.image?.accessibilityDescription, "failed")
        XCTAssertEqual(GlanceFormatting.statusSymbolName(forStatus: "error"), "xmark.circle")
        XCTAssertEqual(GlanceSection.statusTint(forStatus: "error"), .systemRed)
    }

    /// FR-011/FR-012: a block is a different event from a failure and carries a
    /// different SHAPE, not merely a different colour — and its reason is the
    /// policy's, on the row's second line.
    func testABlockedRowCarriesADistinctMarkAndTheBlockReason() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "p1", type: "policy_decision", server: "jira", tool: "delete_issue",
                       status: "blocked", timestamp: "2027-01-15T07:59:30Z",
                       decision: "blocked", blockReason: "Intent rejected: destructive operation")
        ]
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[4]

        XCTAssertEqual(row.title, "jira:delete_issue — 30s")
        XCTAssertEqual(Self.subtitle(of: row), "Intent rejected: destructive operation")
        XCTAssertEqual(row.image?.accessibilityDescription, "blocked")
        XCTAssertEqual(GlanceFormatting.statusSymbolName(forStatus: "blocked"),
                       "exclamationmark.triangle")
        XCTAssertNotEqual(GlanceFormatting.statusSymbolName(forStatus: "blocked"),
                          GlanceFormatting.statusSymbolName(forStatus: "error"),
                          "shape, not colour, must separate a block from a failure")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:delete_issue, blocked, 30s ago, "
                       + "reason: Intent rejected: destructive operation")
    }

    /// SC-003: in a feed that is mostly successful, the failure is the only
    /// marked row on screen.
    func testTheFailureIsTheOnlyMarkedRowInAMostlySuccessfulFeed() {
        let state = Self.busyState()
        state.glanceActivity = [
            Self.entry(id: "s1", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z"),
            Self.entry(id: "f1", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed", timestamp: "2027-01-15T07:59:00Z"),
            Self.entry(id: "s2", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:58:30Z")
        ]
        let section = Self.makeSection()
        let rows = Array(section.items(for: state, now: Self.now)[4...6])

        XCTAssertEqual(rows.map { $0.image == nil }, [true, false, true])
    }

    /// US3 scenario 5: a burst of blocked attempts is one row with its count,
    /// and it never merges with calls to the same tool.
    func testABurstOfBlockedAttemptsIsOneRowSeparateFromTheCalls() {
        let state = Self.busyState()
        var records = (0..<27).map { index in
            Self.entry(id: "b\(index)", type: "policy_decision", server: "jira", tool: "get_issue",
                       status: "blocked",
                       timestamp: "2027-01-15T07:59:\(String(format: "%02d", 59 - index))Z",
                       decision: "blocked", blockReason: "Quarantined server")
        }
        records.append(Self.entry(id: "c1", server: "jira", tool: "get_issue",
                                  timestamp: "2027-01-15T07:50:00Z"))
        state.glanceActivity = records
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        XCTAssertEqual(items[4].title, "jira:get_issue ×27 — 1s")
        XCTAssertEqual(items[4].image?.accessibilityDescription, "blocked")
        XCTAssertEqual(items[5].title, "jira:get_issue — 10m")
        XCTAssertNil(items[5].image, "the successful calls are a separate, unmarked row")
    }

    // MARK: - Status is carried by shape AND colour

    func testStatusIsEncodedByShapeAndColourNotColourAlone() {
        let stamp = "2027-01-15T07:59:30Z"
        let succeeded = Self.entry(id: "s", server: "a", tool: "t", timestamp: stamp)
        let failed = Self.entry(id: "f", server: "a", tool: "t", status: "error", timestamp: stamp)
        let pending = Self.entry(id: "p", server: "a", tool: "t", status: "running", timestamp: stamp)

        let shapes = [succeeded, failed, pending].map(GlanceFormatting.statusSymbolName(for:))
        XCTAssertEqual(Set(shapes).count, 3, "shape alone must separate the three outcomes")

        XCTAssertNotEqual(GlanceSection.statusTint(for: succeeded), GlanceSection.statusTint(for: failed))
        XCTAssertNotEqual(GlanceSection.statusTint(for: failed), GlanceSection.statusTint(for: pending))
        XCTAssertNotEqual(GlanceSection.statusTint(for: succeeded), GlanceSection.statusTint(for: pending))

        XCTAssertEqual(GlanceSection.outcomeDescription(for: pending), "in progress",
                       "a call still running must not be announced as failed")
    }

    func testStatusIconKeepsItsTintInTheMenu() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        // Only marked rows have an image to keep a tint — the successful row
        // above has none at all (FR-010).
        XCTAssertEqual(items[5].image?.isTemplate, false,
                       "a template image is recoloured by the menu, which would drop the status tint")
    }

    // MARK: - Helpers

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    private static func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
    }

    /// A connected core with two qualifying calls and one active client.
    private static func busyState() -> AppState {
        let state = AppState()
        // coreState first: its didSet clears the glance feeds on any non-connected state.
        state.coreState = .connected
        state.usageTimeline = [UsageBucket(start: now, calls: 12, errors: 0, totalRespBytes: 0)]
        state.glanceActivity = [
            entry(id: "a", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                  error: "auth failed: token expired. retry after refresh",
                  timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.glanceSessions = [
            session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                    calls: 8, lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    /// A connected core whose feed holds a three-call burst of one tool
    /// followed by a single call to another — two runs, so two rows.
    private static func burstState() -> AppState {
        let state = busyState()
        state.glanceActivity = [
            entry(id: "j1", server: "jira", tool: "get_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-j1"),
            entry(id: "j2", server: "jira", tool: "get_issue",
                  timestamp: "2027-01-15T07:59:00Z", session: "sess-j2"),
            entry(id: "j3", server: "jira", tool: "get_issue",
                  timestamp: "2027-01-15T07:58:30Z", session: "sess-j3"),
            entry(id: "g1", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:57:00Z", session: "sess-g1")
        ]
        return state
    }

    /// A connected core whose single call declared an intent reason.
    private static func reasonState(
        reason: String = "Verify the ticket is still open",
        status: String = "success",
        error: String? = nil
    ) -> AppState {
        let state = busyState()
        state.glanceActivity = [
            entry(id: "r", server: "jira", tool: "get_issue", status: status, error: error,
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-r", reason: reason)
        ]
        return state
    }

    /// `NSMenuItem.subtitle` exists only on macOS 14.4+, so reading it needs the
    /// same availability gate the writer has. Below that it is nil by
    /// definition — which is exactly what the single-line fallback asserts.
    private static func subtitle(of item: NSMenuItem) -> String? {
        if #available(macOS 14.4, *) {
            return item.subtitle
        }
        return nil
    }

    private static func entry(
        id: String,
        type: String = "tool_call",
        server: String? = nil,
        tool: String? = nil,
        status: String = "success",
        error: String? = nil,
        timestamp: String,
        session: String? = nil,
        request: String? = nil,
        reason: String? = nil,
        decision: String? = nil,
        blockReason: String? = nil
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": timestamp,
            // Defaults to a request id derived from `id`; pass `request:`
            // explicitly to model the reconcile, which re-ids the same record.
            "request_id": request ?? "req-\(id)"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        if let error { json["error_message"] = error }
        if let session { json["session_id"] = session }
        // Shaped like the wire: a call's reason lives under `metadata.intent`,
        // while a policy decision's own reason sits at the top of metadata
        // beside the decision. Both are what the projection whitelist keeps and
        // what the SSE adapter writes, so the row pipeline reads them the same
        // way whether the record was polled or streamed.
        var metadata: [String: Any] = [:]
        if let reason { metadata["intent"] = ["reason": reason] }
        if let decision { metadata["decision"] = decision }
        if let blockReason { metadata["reason"] = blockReason }
        if !metadata.isEmpty { json["metadata"] = metadata }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func session(
        id: String,
        name: String,
        version: String,
        calls: Int,
        lastActivity: String
    ) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": name,
            "client_version": version,
            "tool_call_count": calls,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
