import XCTest
@testable import MCPProxy

/// Spec 108-k (K16, FR-049, parity row 24): the Create Token body and the token
/// list's presentation.
@MainActor
final class TokensCreateModelTests: XCTestCase {

    final class StubSource: TokenCreateSource, @unchecked Sendable {
        var result: Result<String, Error> = .success("mcp_agt_SECRET")
        private(set) var bodies: [[String: Any]] = []

        func createToken(body: [String: Any]) async throws -> String {
            bodies.append(body)
            return try result.get()
        }
    }

    private func token(_ json: String) -> AgentToken {
        try! JSONDecoder().decode(AgentToken.self, from: Data(json.utf8))
    }

    // MARK: Body

    func testAProfileTokenBodyCarriesNoLegacyScope() async {
        let source = StubSource()
        let model = TokenCreateModel(source: source, presetProfile: "work-ro")
        model.name = "ci"
        model.legacyServers = "github"
        model.legacyPermissions = "read,write"
        let body = model.requestBody()
        XCTAssertEqual(body["name"] as? String, "ci")
        XCTAssertEqual(body["profile"] as? String, "work-ro")
        XCTAssertEqual(body["expires_in"] as? String, "30d", "the CLI default")
        XCTAssertNil(body["allowed_servers"], "scope comes from the profile")
        XCTAssertNil(body["permissions"])
        XCTAssertNil(body["servers"])
        XCTAssertEqual(Set(body.keys), ["name", "profile", "expires_in"])
    }

    func testTheLegacyBodyUsesTheRealFieldNames() {
        let model = TokenCreateModel(source: StubSource(), presetProfile: TokenCreateModel.legacy)
        model.name = "old-style"
        model.legacyServers = "github, gitlab"
        model.legacyPermissions = "read,write"
        model.expiresIn = "90d"
        let body = model.requestBody()
        XCTAssertEqual(body["allowed_servers"] as? [String], ["github", "gitlab"])
        XCTAssertEqual(body["permissions"] as? [String], ["read", "write"])
        XCTAssertEqual(body["expires_in"] as? String, "90d")
        XCTAssertNil(body["profile"])
        XCTAssertNil(body["servers"], "the pre-108 sheet sent `servers`, which the core ignores")
    }

    func testCreateIsDisabledUntilAProfileOrLegacyIsChosen() {
        let model = TokenCreateModel(source: StubSource())
        model.name = "x"
        XCTAssertFalse(model.canCreate, "Choose… is not a choice")
        model.profile = "work-ro"
        XCTAssertTrue(model.canCreate)
        model.name = "  "
        XCTAssertFalse(model.canCreate)
        model.name = "x"
        model.profile = TokenCreateModel.legacy
        XCTAssertTrue(model.canCreate)
        XCTAssertTrue(model.isLegacy)
    }

    func testTheExpiryChoices() {
        XCTAssertEqual(TokenCreateModel.expiryChoices.map(\.value), ["7d", "30d", "90d", "365d"])
        XCTAssertEqual(TokenCreateModel.defaultExpiry, "30d")
    }

    func testAClientPrefixedNameGetsAnInlineNameError() async {
        let source = StubSource()
        source.result = .failure(APIClientError.service(status: 400, body: ServiceErrorBody(
            error: "token names starting with \"client-\" are reserved for client credentials", field: "name")))
        let model = TokenCreateModel(source: source, presetProfile: "work-ro")
        model.name = "client-mine"
        await model.create()
        XCTAssertEqual(model.nameError, "token names starting with \"client-\" are reserved for client credentials")
        XCTAssertNil(model.errorMessage)
        XCTAssertNil(model.secret)
    }

    func testTheSecretIsShownOnceAndWipedOnDismiss() async {
        let source = StubSource()
        let model = TokenCreateModel(source: source, presetProfile: "work-ro")
        model.name = "ci"
        await model.create()
        XCTAssertEqual(model.secret, "mcp_agt_SECRET")
        model.dismiss()
        XCTAssertNil(model.secret)
    }

    // MARK: Decoding the real list shape

    /// `GET /tokens` returns `name` and `allowed_servers` (no `id`): the pre-108
    /// model required `id` and read `servers`, so a real response never decoded.
    func testTheRealTokenListShapeDecodes() throws {
        let json = """
        {"tokens": [{"name":"ci","token_prefix":"mcp_agt_ab","allowed_servers":["*"],"permissions":["read","write","destructive"],
          "expires_at":"2026-12-01T00:00:00Z","created_at":"2026-10-01T00:00:00Z","revoked":false,
          "profile_pin":"work-ro","kind":"agent","legacy_scope":false}]}
        """
        let list = try JSONDecoder().decode(TokensListResponse.self, from: Data(json.utf8))
        XCTAssertEqual(list.tokens.count, 1)
        XCTAssertEqual(list.tokens[0].id, "ci")
        XCTAssertEqual(list.tokens[0].profilePin, "work-ro")
        XCTAssertEqual(list.tokens[0].servers, ["*"])
    }

    // MARK: Row presentation

    func testAProfileTokenShowsItsProfileChip() {
        let row = TokenRowPresentation(token(#"{"name":"ci","profile_pin":"work-ro","kind":"agent","legacy_scope":false,"created_at":"x"}"#))
        XCTAssertEqual(row.profileChip, "work-ro")
        XCTAssertFalse(row.isLegacyScope)
        XCTAssertTrue(row.canRevoke)
        XCTAssertNil(row.migrateHint)
    }

    func testALegacyScopeTokenIsReadOnlyWithAMigrateHint() {
        let row = TokenRowPresentation(token(#"{"name":"old","allowed_servers":["github"],"permissions":["read"],"kind":"agent","legacy_scope":true,"created_at":"x"}"#))
        XCTAssertTrue(row.isLegacyScope)
        XCTAssertNil(row.profileChip)
        XCTAssertEqual(row.migrateHint, "Migrate to a profile")
        XCTAssertTrue(row.canRevoke)
    }

    func testAClientCredentialRowHasNoRevokeAndPointsAtClients() {
        let row = TokenRowPresentation(token(#"{"name":"client-cursor","kind":"client","client_id":"cursor","profile_pin":"work-ro","profile_mode":"locked","legacy_scope":false,"created_at":"x"}"#))
        XCTAssertTrue(row.isClientCredential)
        XCTAssertFalse(row.canRevoke, "revoked with Forget under Clients")
        XCTAssertEqual(row.kindLabel, "Client credential for cursor — manage in Clients")
        XCTAssertEqual(row.modeLabel, "Locked")
        XCTAssertFalse(row.isLegacyScope)
    }

    // MARK: Filters

    func testTheListFilterParamsAreProfileAndToken() {
        var filter = ScopeFilter.forProfile("-")
        filter.token = "ci"
        let request = filter.restRequest(for: .tokens, scopeFiltersAvailable: true)
        let pairs = request?.query.map { "\($0.name)=\($0.value ?? "")" }
        XCTAssertEqual(pairs, ["profile=-", "token=ci"])
        XCTAssertEqual(TokenProfileFilter.unpinned, "-")
    }
}
