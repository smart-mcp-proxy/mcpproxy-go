import XCTest
@testable import MCPProxy

/// Spec 125 T031/T045: the macOS app labels the two skills activity types like
/// the Web UI (frontend/src/utils/activity.ts), words held skills in the review
/// queue, and sends a skill-only row to the Web UI (the app has no Skills tab).
final class SkillsReviewPresentationTests: XCTestCase {

    // MARK: - Activity types

    func testSkillActivityTypesCarryTheWebLabels() {
        XCTAssertEqual(ActivityTypePresentation.label("skill_quarantine_change"), "Skill Approval Change")
        XCTAssertEqual(ActivityTypePresentation.label("skill_access"), "Skill Access")
        XCTAssertNotEqual(ActivityTypePresentation.icon("skill_quarantine_change"), ActivityTypePresentation.icon("unknown_type"))
        XCTAssertNotEqual(ActivityTypePresentation.icon("skill_access"), ActivityTypePresentation.icon("unknown_type"))
    }

    func testEveryKnownActivityTypeHasALabelOfItsOwn() {
        for type in ScopeFilter.knownTypes {
            XCTAssertNotEqual(ActivityTypePresentation.label(type), type, "\(type) shows its raw name")
            XCTAssertNotEqual(ActivityTypePresentation.icon(type), ActivityTypePresentation.icon("unknown_type"), "\(type) has no icon")
        }
        XCTAssertEqual(ActivityTypePresentation.label("future_type"), "future_type", "an unknown type keeps its raw name")
    }

    func testScopeFilterKnowsTheSkillTypes() {
        XCTAssertTrue(ScopeFilter.knownTypes.contains("skill_quarantine_change"))
        XCTAssertTrue(ScopeFilter.knownTypes.contains("skill_access"))
    }

    // MARK: - Review queue rows

    private func row(_ json: String) throws -> ReviewQueueRow {
        try JSONDecoder().decode(ReviewQueueRow.self, from: Data(json.utf8))
    }

    func testReviewQueueRowDecodesTheSkillCounts() throws {
        let r = try row(#"{"server":"hf","kind":"skill_review","quarantined":false,"skills_pending":3,"skills_changed":1}"#)
        XCTAssertEqual(r.skillsPending, 3)
        XCTAssertEqual(r.skillsChanged, 1)
        let old = try row(#"{"server":"a","kind":"tool_review","quarantined":false,"pending":1}"#)
        XCTAssertNil(old.skillsPending)
    }

    func testReviewQueueRowWording() throws {
        XCTAssertEqual(ReviewQueuePresentation.subtitle(try row(#"{"server":"q","kind":"server_review","quarantined":true}"#)), "Server awaiting review")
        XCTAssertEqual(ReviewQueuePresentation.subtitle(try row(#"{"server":"a","kind":"tool_review","quarantined":false,"pending":1,"changed":1}"#)), "1 new · 1 changed")
        XCTAssertEqual(ReviewQueuePresentation.subtitle(try row(#"{"server":"hf","kind":"skill_review","quarantined":false,"skills_pending":3,"skills_changed":1}"#)),
                       "4 skills awaiting review · 1 changed · Review in the Web UI")
        XCTAssertEqual(ReviewQueuePresentation.subtitle(try row(#"{"server":"a","kind":"tool_review","quarantined":false,"pending":1,"skills_pending":1}"#)),
                       "1 new · 0 changed · 1 skill awaiting review")
    }

    func testOnlyASkillOnlyRowOpensTheWebUI() throws {
        let skillOnly = try row(#"{"server":"io.github.o/hf","kind":"skill_review","quarantined":false,"skills_pending":1}"#)
        XCTAssertEqual(ReviewQueuePresentation.webUIPath(skillOnly), "/servers/io.github.o%2Fhf?tab=skills")
        let tools = try row(#"{"server":"a","kind":"tool_review","quarantined":false,"pending":1,"skills_pending":1}"#)
        XCTAssertNil(ReviewQueuePresentation.webUIPath(tools), "a tool row opens the native review sheet")
    }

    // MARK: - Web UI links

    func testWebUILinkJoinsBasePathAndKey() {
        XCTAssertEqual(WebUILink.url(base: "http://127.0.0.1:8080", path: "/servers/hf?tab=skills", apiKey: "k 1")?.absoluteString,
                       "http://127.0.0.1:8080/ui/servers/hf?tab=skills&apikey=k%201")
        XCTAssertEqual(WebUILink.url(base: "http://127.0.0.1:8080/", path: "/servers/hf", apiKey: "")?.absoluteString,
                       "http://127.0.0.1:8080/ui/servers/hf")
        XCTAssertEqual(WebUILink.url(base: "http://h:1", path: "/review", apiKey: "k")?.absoluteString,
                       "http://h:1/ui/review?apikey=k")
    }

    // MARK: - Home attention

    func testHomeSendsASkillReviewToTheWebUINotTheToolReviewSheet() throws {
        let path = URL(fileURLWithPath: #filePath).deletingLastPathComponent()
            .appendingPathComponent("../MCPProxy/State/HomeAttentionAction.swift").standardized
        let source = try String(contentsOf: path, encoding: .utf8)
        XCTAssertTrue(source.contains("item.kind == \"skill_review\""), "a skill_review item must be told apart from a tool review")
        XCTAssertTrue(source.contains(".openWebUIPath"), "a skill_review item opens its Web UI target")

        let home = try String(contentsOf: path.deletingLastPathComponent().appendingPathComponent("../Views/HomeView.swift").standardized, encoding: .utf8)
        XCTAssertTrue(home.contains("case \"skill_review\":"), "Home needs an icon for skill_review")
    }
}
