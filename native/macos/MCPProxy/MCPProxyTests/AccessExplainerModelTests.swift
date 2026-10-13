import XCTest
@testable import MCPProxy

/// Spec 108-k (K19, FR-046): the access explainer's steps, verdict and the
/// fix → navigation mapping. A fix NAVIGATES; nothing here mutates.
@MainActor
final class AccessExplainerModelTests: XCTestCase {

    final class StubSource: AccessExplainSource, @unchecked Sendable {
        var result: Result<AccessExplanation, Error> = .failure(APIClientError.noData)
        var names: [String] = ["github:create_issue", "github:list_issues", "notion:search"]
        private(set) var asked: [(String, APIClient.ExplainSubjectQuery)] = []

        func explain(tool: String, subject: APIClient.ExplainSubjectQuery) async throws -> AccessExplanation {
            asked.append((tool, subject))
            return try result.get()
        }

        func allToolNames() async throws -> [String] { names }
    }

    private func blockedExplanation() throws -> AccessExplanation {
        // The shared fixture (internal/profile/testdata/contract/explain_blocked.json).
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        let data = try Data(contentsOf: url.appendingPathComponent("internal/profile/testdata/contract/explain_blocked.json"))
        return try JSONDecoder().decode(AccessExplanation.self, from: data)
    }

    func testTheStepsAndVerdictComeFromTheFixture() async throws {
        let source = StubSource()
        source.result = .success(try blockedExplanation())
        let model = AccessExplainerModel(source: source, subject: .client("cursor"), tool: "github:create_issue")
        await model.explain()

        XCTAssertEqual(model.explanation?.steps.count, 9)
        XCTAssertEqual(model.explanation?.steps.first { $0.status == .fail }?.step, "tier_cap")
        XCTAssertEqual(model.verdictText, "Hidden at tier_cap")
        XCTAssertEqual(model.explanation?.fixes.count, 2)
        XCTAssertNil(model.errorMessage)
    }

    /// Every step shows a symbol AND a word, so colour is never the only signal.
    func testEveryStatusHasASymbolAndAWord() {
        for status in [ExplainStatus.pass, .fail, .skip] {
            XCTAssertFalse(status.symbolName.isEmpty)
            XCTAssertFalse(status.word.isEmpty)
        }
        XCTAssertEqual(ExplainStatus.pass.word, "Pass")
        XCTAssertEqual(ExplainStatus.fail.word, "Fail")
        XCTAssertEqual(ExplainStatus.skip.word, "Skipped")
    }

    /// Exactly one subject reaches the endpoint, whichever kind is chosen.
    func testExactlyOneSubjectIsSent() async {
        let subjects: [(ExplainerSubject, String, String)] = [
            (.client("cursor"), "client", "cursor"), (.token("ci"), "token", "ci"),
            (.profile("work"), "profile", "work"), (.anonymous, "anonymous", "true"),
        ]
        for (subject, name, value) in subjects {
            let source = StubSource()
            let model = AccessExplainerModel(source: source, subject: subject, tool: "github:create_issue")
            await model.explain()
            XCTAssertEqual(source.asked.count, 1)
            let item = source.asked[0].1.queryItem
            XCTAssertEqual(item.0, name)
            XCTAssertEqual(item.1, value)
        }
    }

    func testAToolWithoutAServerPrefixIsNotSent() async {
        let source = StubSource()
        let model = AccessExplainerModel(source: source, subject: .anonymous, tool: "create_issue")
        XCTAssertFalse(model.toolIsValid)
        XCTAssertFalse(model.canExplain)
        await model.explain()
        XCTAssertTrue(source.asked.isEmpty)
    }

    func testASubjectWithNoNameCannotBeExplained() {
        let model = AccessExplainerModel(source: StubSource(), subject: .client(""), tool: "a:b")
        XCTAssertFalse(model.canExplain)
        model.subject = .anonymous
        XCTAssertTrue(model.canExplain, "anonymous needs no name")
    }

    func testA400A403AndA404AreShownInline() async {
        let source = StubSource()
        let model = AccessExplainerModel(source: source, subject: .client("ghost"), tool: "a:b")
        for (status, text) in [(400, "exactly one of client, token, profile, anonymous is required"),
                               (403, "operation requires admin access"), (404, "client not found")] {
            source.result = .failure(APIClientError.service(status: status, body: ServiceErrorBody(error: text)))
            await model.explain()
            XCTAssertEqual(model.errorMessage, text)
            XCTAssertNil(model.explanation)
        }
    }

    func testToolSuggestionsFilterTheCatalogue() async {
        let source = StubSource()
        let model = AccessExplainerModel(source: source)
        await model.loadToolNames()
        model.tool = "issue"
        XCTAssertEqual(model.suggestions, ["github:create_issue", "github:list_issues"])
        model.tool = ""
        XCTAssertEqual(model.suggestions.count, 3)
    }

    // MARK: Fix → navigation (all nine actions)

    private func fix(_ action: String, target: String = "t") -> ExplainFix {
        try! JSONDecoder().decode(ExplainFix.self, from: Data(
            #"{"step":"s","action":"\#(action)","target":"\#(target)","label":"L"}"#.utf8))
    }

    func testEveryFixActionNavigatesToItsPlace() {
        let tool = "github:create_issue"
        let cases: [(String, String, AppRoute)] = [
            ("allow_in_profile", "work-ro", .profileEditor(name: "work-ro", focusTool: tool)),
            ("classify_in_profile", "work-ro", .profileEditor(name: "work-ro", focusTool: tool)),
            ("add_server_to_profile", "work-ro", .profileEditor(name: "work-ro", focusTool: tool)),
            ("move_client", "cursor", .clientDetail(id: "cursor")),
            ("edit_token", "ci", .clients(tab: .tokens, filter: .forToken("ci"))),
            ("enable_server", "github", .serverDetail(name: "github")),
            ("approve_tool", "github:create_issue", .reviewQueue),
            ("change_setting", "require_mcp_auth", .settings(.requireMCPAuth)),
            ("reconnect_client", "cursor", .connectSheet(clientId: "cursor")),
        ]
        XCTAssertEqual(cases.count, 9)
        for (action, target, expected) in cases {
            XCTAssertEqual(AccessExplainerModel.route(for: fix(action, target: target), tool: tool), expected, action)
        }
    }

    /// #1451-5: editing the tool field after an explanation must not retarget
    /// the fix to a tool that was never explained.
    func testAFixRoutesToTheExplainedToolNotTheEditedField() async throws {
        let source = StubSource()
        source.result = .success(try blockedExplanation())
        let model = AccessExplainerModel(source: source, subject: .client("cursor"), tool: "github:create_issue")
        await model.explain()
        model.tool = "notion:search"
        XCTAssertEqual(model.route(for: fix("allow_in_profile", target: "work-ro")),
                       .profileEditor(name: "work-ro", focusTool: "github:create_issue"))
    }

    func testAnUnknownFixActionHasNoButton() {
        XCTAssertNil(AccessExplainerModel.route(for: fix("future_action"), tool: "a:b"))
    }

    func testAChangeSettingFixThatNamesAnotherSettingOpensSecurityWithoutAScrollTarget() {
        XCTAssertEqual(
            AccessExplainerModel.route(for: fix("change_setting", target: "read_only_mode"), tool: "a:b"),
            .settings(.setting("read_only_mode")))
        XCTAssertEqual(
            AccessExplainerModel.route(for: fix("change_setting", target: "anonymous_profile"), tool: "a:b"),
            .settings(.anonymousProfile(preselect: nil)))
    }
}
