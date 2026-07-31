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
        XCTAssertEqual(items.first?.title, "12 calls this hour · 1 client")
        XCTAssertFalse(items[0].isEnabled, "the header is a muted, non-clickable line")
    }

    func testHeaderOmitsCallCountUntilUsageLoads() {
        let state = Self.busyState()
        state.callsThisHour = nil
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).first?.title, "1 client")
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
                       "12 calls this hour · 1 client · not updating")
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
        XCTAssertEqual(items[0].title, "12 calls this hour · 1 client")
    }

    // MARK: - Recent section

    func testRecentSectionRendersQualifyingRows() {
        let section = Self.makeSection()
        let titles = section.items(for: Self.busyState(), now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.prefix(6)), [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…"
        ])
    }

    func testActivityRowCarriesFullIdentity() {
        let section = Self.makeSection()
        let failed = section.items(for: Self.busyState(), now: Self.now)[4]
        XCTAssertEqual(failed.title, "jira:get_issue · auth failed — 2m")
        XCTAssertEqual(failed.representedObject as? String, "sess-b")
        XCTAssertEqual(failed.image?.accessibilityDescription, "failed")
        XCTAssertEqual(failed.toolTip, "jira:get_issue\nauth failed: token expired. retry after refresh")
        XCTAssertEqual(failed.accessibilityLabel(), "jira:get_issue, failed: auth failed, 2m ago")
        XCTAssertNotNil(failed.action)
    }

    func testOpenActivityRowHasNoSessionPayload() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items[5].title, "Open Activity…")
        XCTAssertNil(items[5].representedObject)
        XCTAssertNotNil(items[5].action)
    }

    func testNoActivityShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceActivity = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]
        XCTAssertEqual(row.title, "No tool calls yet")
        XCTAssertFalse(row.isEnabled)
    }

    func testFirstClauseKeepsOnlyTheLeadingClause() {
        XCTAssertEqual(GlanceSection.firstClause(of: "auth failed: token expired"), "auth failed")
        XCTAssertEqual(GlanceSection.firstClause(of: "dial tcp 127.0.0.1"), "dial tcp 127")
        XCTAssertEqual(GlanceSection.firstClause(of: "  boom  "), "boom")
        XCTAssertNil(GlanceSection.firstClause(of: "   "))
        XCTAssertNil(GlanceSection.firstClause(of: nil))
    }

    // MARK: - Grouped rows (spec 090 US1)

    /// A burst of one tool is one row, and the rest of the page still gets rows.
    func testARunOfIdenticalCallsRendersOneRowWithACountSuffix() {
        let state = Self.burstState()
        let section = Self.makeSection()
        let titles = section.items(for: state, now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.dropFirst(3).prefix(2)), [
            "jira:get_issue ×3 — 30s",
            "github:create_issue — 3m"
        ])
    }

    func testTheRowsAgeComesFromTheNewestRecordOfTheRun() {
        let section = Self.makeSection()
        let row = section.items(for: Self.burstState(), now: Self.now)[3]
        XCTAssertTrue(row.title.hasSuffix("— 30s"),
                      "the run's clock is its newest record, not its oldest")
    }

    func testASingleCallRunCarriesNoCountSuffix() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[3]
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
        let row = section.items(for: state, now: Self.now)[3]

        XCTAssertEqual(row.title, "jira:get_issue ×3 · auth failed — 30s")
        XCTAssertEqual(row.image?.accessibilityDescription, "failed")
        XCTAssertEqual(row.toolTip, "jira:get_issue\nauth failed: token expired")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, repeated 3 times, failed: auth failed, 30s ago")
    }

    func testTheRepeatCountIsSpokenNotJustDrawn() {
        let section = Self.makeSection()
        let row = section.items(for: Self.burstState(), now: Self.now)[3]
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
        let iconBefore = items[3].image

        state.glanceActivity.insert(
            Self.entry(id: "j0", server: "jira", tool: "get_issue",
                       timestamp: "2027-01-15T07:59:50Z", session: "sess-j0"),
            at: 0)

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[3].title, "jira:get_issue ×4 — 10s")
        XCTAssertTrue(items[3].image === iconBefore,
                      "the same run must keep its icon; only the count and clock moved")
        XCTAssertEqual(items[3].representedObject as? String, "sess-j0",
                       "the click payload follows the run's newest record")
    }

    /// …and when the row really does come to stand for a different run — a new
    /// run whose oldest record is not the previous one — the whole identity is
    /// rewritten, icon and click payload included.
    func testADifferentRunInTheSameSlotRewritesTheRowIdentity() {
        let state = Self.burstState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let iconBefore = items[3].image

        state.glanceActivity = [
            Self.entry(id: "o1", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-o1"),
            Self.entry(id: "o2", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:50Z", session: "sess-o2"),
            state.glanceActivity[3]
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[3].title, "obsidian:search_notes ×2 — 5s")
        XCTAssertFalse(items[3].image === iconBefore,
                       "a different run must rewrite the row's entire identity, icon included")
        XCTAssertEqual(items[3].representedObject as? String, "sess-o1")
    }

    /// A late status correction on the run's newest record still lands: "same
    /// run" must not mean "skip the update".
    func testASameRunStillPicksUpALateFailure() {
        let state = Self.burstState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.glanceActivity[0] = Self.entry(
            id: "j1", server: "jira", tool: "get_issue", status: "error",
            error: "rate limited: try later",
            timestamp: "2027-01-15T07:59:30Z", session: "sess-j1")

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[3].title, "jira:get_issue ×3 · rate limited — 30s")
        XCTAssertEqual(items[3].image?.accessibilityDescription, "failed")
    }

    // MARK: - Reason subtitles (spec 090 US2)

    /// The reason is the row's standard subtitle — a subdued second line of the
    /// SAME menu item, not a view-backed row (FR-005/FR-009), so keyboard
    /// navigation and VoiceOver are untouched.
    func testARowWithAReasonRendersItAsTheSubtitle() {
        let state = Self.reasonState()
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]

        XCTAssertEqual(row.title, "jira:get_issue — 30s", "the reason never joins the title line")
        XCTAssertEqual(Self.subtitle(of: row), "Verify the ticket is still open")
    }

    /// FR-006: the subtitle has its OWN 60-character budget, and the full text
    /// stays reachable in the tooltip.
    func testALongReasonIsTailTruncatedToTheSubtitleBudget() {
        let long = "Handoff: move the ticket to review per the user's request and notify the reporter"
        let state = Self.reasonState(reason: long)
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]

        let subtitle = Self.subtitle(of: row)
        XCTAssertEqual(subtitle?.count, GlanceFormatting.reasonBudget)
        XCTAssertEqual(subtitle?.hasSuffix("\u{2026}"), true)
        XCTAssertEqual(row.toolTip, "jira:get_issue\n\(long)",
                       "the tooltip carries the reason in full")
    }

    /// FR-007: no reason means one line, not an empty second one.
    func testARowWithoutAReasonHasNoSubtitle() {
        let section = Self.makeSection()
        let row = section.items(for: Self.busyState(), now: Self.now)[3]
        XCTAssertNil(Self.subtitle(of: row))
    }

    /// Below macOS 14.4 the subtitle mechanism does not exist, so the row is
    /// single-line — but the reason must NOT be lost: tooltip and VoiceOver
    /// carry it on every macOS version (FR-005/FR-006, US2 scenario 7).
    func testWithoutTheSubtitleMechanismTheRowIsSingleLineAndKeepsTheReason() {
        let state = Self.reasonState()
        let section = Self.makeSection()
        section.supportsRowSubtitles = false
        let row = section.items(for: state, now: Self.now)[3]

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
        let row = section.items(for: state, now: Self.now)[3]

        XCTAssertEqual(row.title, "jira:get_issue ×3 — 30s")
        XCTAssertEqual(Self.subtitle(of: row), "Check the ticket after the failed transition")
    }

    /// FR-011a: on a failed row the error joins the TITLE and the reason keeps
    /// the subtitle — the error never displaces the reason.
    func testAFailedRowShowsTheErrorOnTheTitleAndKeepsTheReasonAsSubtitle() {
        let state = Self.reasonState(status: "error", error: "auth failed: token expired")
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]

        XCTAssertEqual(row.title, "jira:get_issue · auth failed — 30s")
        XCTAssertEqual(Self.subtitle(of: row), "Verify the ticket is still open")
        XCTAssertEqual(row.accessibilityLabel(),
                       "jira:get_issue, failed: auth failed, 30s ago, "
                       + "reason: Verify the ticket is still open")
    }

    /// FR-011a truncation precedence: the error clause is cut to its own
    /// 40-character budget, the label keeps its 34-character middle-truncation
    /// budget (it is never tightened to make room), and the age is never cut.
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
        let title = section.items(for: state, now: Self.now)[3].title

        let label = String(title.prefix(while: { $0 != "·" })).trimmingCharacters(in: .whitespaces)
        let clause = title
            .components(separatedBy: " · ")[1]
            .components(separatedBy: " — ")[0]
        XCTAssertEqual(label.count, 34, "the label budget must not be tightened by a long error")
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
        let row = section.items(for: state, now: Self.now)[3]

        XCTAssertEqual(row.toolTip,
                       "jira:get_issue\nVerify the ticket is still open\n"
                       + "auth failed: token expired. retry after refresh")
    }

    // MARK: - Clients section and histogram

    func testClientRowCarriesSessionIdentity() {
        let section = Self.makeSection()
        let client = section.items(for: Self.busyState(), now: Self.now)[8]
        XCTAssertEqual(client.title, "Claude Code — 8 calls · 1m")
        XCTAssertEqual(client.representedObject as? String, "sess-a")
        XCTAssertEqual(client.toolTip, "Claude Code 2.1.0")
        XCTAssertEqual(client.accessibilityLabel(), "Claude Code, 8 calls, last active 1m ago")
    }

    func testNoClientsShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceSessions = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[8]
        XCTAssertEqual(row.title, "No connected clients")
        XCTAssertFalse(row.isEnabled)
    }

    /// The submenu's row is built by its delegate when it opens, so these two
    /// tests fire `menuNeedsUpdate` where they previously read the row straight
    /// out of `items(for:)`. What they assert is unchanged.
    private func open(_ menu: NSMenu?) {
        guard let menu, let delegate = menu.delegate else {
            return XCTFail("the histogram submenu has no delegate, so opening it builds nothing")
        }
        delegate.menuNeedsUpdate?(menu)
    }

    func testHistogramSubmenuShowsLoadingUntilUsageArrives() {
        let section = Self.makeSection()
        let histogram = section.items(for: Self.busyState(), now: Self.now)[10]
        XCTAssertEqual(histogram.title, "Activity (24h)")
        open(histogram.submenu)
        XCTAssertEqual(histogram.submenu?.item(at: 0)?.title, "Loading…")
    }

    func testHistogramSubmenuUsesInjectedViewWhenAvailable() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        // The seam now takes the shaped 24-hour axis and returns the whole item,
        // rather than taking raw buckets and returning a view — so the count
        // below is the axis width, not the timeline length.
        section.histogramChartItemFactory = { bars in
            let item = NSMenuItem(title: "", action: nil, keyEquivalent: "")
            let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 90))
            view.setAccessibilityLabel("\(bars.count) bars")
            item.view = view
            return item
        }
        let submenu = section.items(for: state, now: Self.now)[10].submenu
        open(submenu)
        let chart = submenu?.item(at: 0)
        XCTAssertNotNil(chart?.view)
        XCTAssertEqual(chart?.view?.accessibilityLabel(), "24 bars")
    }

    // `testHistogramSubmenuFallsBackToTextWithoutABuilder` was REMOVED here, not
    // ported: it asserted the text row shown when no view builder was injected,
    // and there is no longer a builder-less mode to assert. The seam is
    // non-optional and defaults to the real chart, precisely because the old
    // optional seam was never set in production and the tray therefore shipped
    // that text fallback and never a chart. Its successor —
    // "with nothing injected, the submenu shows a real chart" — is
    // `GlanceHistogramSubmenuTests.testTheDefaultFactoryProducesTheRealChart`.

    func testBlockLayoutOrder() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        let titles = items.map { $0.isSeparatorItem ? "—" : $0.title }
        XCTAssertEqual(titles, [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…",
            "—",
            "Clients",
            "Claude Code — 8 calls · 1m",
            "—",
            "Activity (24h)",
            "—"
        ])
    }

    // MARK: - In-place updates

    func testUpdateInPlaceRewritesTheEntireRowIdentity() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let row = items[3]

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            Self.entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.callsThisHour = 13

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls this hour · 1 client")
        XCTAssertEqual(row.title, "obsidian:search_notes — 5s")
        XCTAssertEqual(row.representedObject as? String, "sess-c",
                       "the click payload must follow the title, or the row opens the previous record's session")
        XCTAssertEqual(row.image?.accessibilityDescription, "succeeded")
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
        XCTAssertEqual(items[3].title, "github:create_issue — 30s", "rows must be left untouched")
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

        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        state.callsThisHour = 13

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls this hour · 1 client")

        // And it is still updating a cycle later: the freeze was for the rest
        // of the session, not for one tick.
        state.callsThisHour = 14
        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "14 calls this hour · 1 client")
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
        let iconBefore = items[3].image

        state.glanceActivity = [
            Self.entry(id: "01JQ8Z0000000000000000001", server: "github", tool: "create_issue",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-a", request: "req-a"),
            Self.entry(id: "01JQ8Z0000000000000000002", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b", request: "req-b")
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertTrue(items[3].image === iconBefore,
                      "same request id means the same record, so the row's icon must be left alone")
        XCTAssertEqual(items[3].title, "github:create_issue — 30s")
        XCTAssertEqual(items[3].representedObject as? String, "sess-a")
    }

    func testDifferentRecordInTheSameSlotRewritesTheIcon() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let iconBefore = items[3].image
        let previousFailure = state.glanceActivity[1]

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            previousFailure
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertFalse(items[3].image === iconBefore,
                       "a different record must rewrite the row's entire identity, icon included")
        XCTAssertEqual(items[3].representedObject as? String, "sess-c")
    }

    /// "Same record" must not mean "skip the update": the final status arrives
    /// on a record whose request id has not changed — the reason
    /// `AppState.updateGlanceActivity` fingerprints status rather than ids.
    func testSameRecordStillPicksUpALateStatusCorrection() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let previousFailure = state.glanceActivity[1]

        state.glanceActivity = [
            Self.entry(id: "a", server: "github", tool: "create_issue", status: "error",
                       error: "rate limited: try later",
                       timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            previousFailure
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[3].title, "github:create_issue · rate limited — 30s")
        XCTAssertEqual(items[3].image?.accessibilityDescription, "failed")
        XCTAssertEqual(items[3].toolTip, "github:create_issue\nrate limited: try later")
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
        let row = section.items(for: state, now: Self.now)[8]
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
                      "the connected dot is a constant — an identical poll must not allocate a new one")
        XCTAssertEqual(titleWrites, 0,
                       "an identical poll must not rewrite the title of a row in an open menu")
        XCTAssertEqual(row.title, "Claude Code — 8 calls · 1m")
    }

    /// …and the guards must not freeze the row: the live call count is the whole
    /// reason `MCPSession` became `Equatable`.
    func testChangedClientCallCountStillRewritesTheRow() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[8]

        state.glanceSessions = [
            Self.session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                         calls: 40, lastActivity: "2027-01-15T07:59:00Z")
        ]

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(row.title, "Claude Code — 40 calls · 1m")
        XCTAssertEqual(row.accessibilityLabel(), "Claude Code, 40 calls, last active 1m ago")
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
        XCTAssertEqual(items[3].image?.isTemplate, false,
                       "a template image is recoloured by the menu, which would drop the status tint")
        XCTAssertEqual(items[4].image?.isTemplate, false)
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
        state.callsThisHour = 12
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
        reason: String? = nil
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
        // which is what the projection whitelist keeps and what the SSE adapter
        // writes, so the row pipeline reads it the same way in both cases.
        if let reason { json["metadata"] = ["intent": ["reason": reason]] }
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
