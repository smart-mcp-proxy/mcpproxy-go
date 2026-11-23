import XCTest
@testable import MCPProxy

/// Spec 132 PR-3 (T068): the Connect form offers "API key" or "Sign in with
/// browser" only for a client the core says can sign in that way. The default is
/// what the form always did.
@MainActor
final class ConnectBrowserLoginTests: XCTestCase {

    private func decodeStatus(_ json: String) throws -> APIClient.ClientStatus {
        try JSONDecoder().decode(APIClient.ClientStatus.self, from: Data(json.utf8))
    }

    private func decodePreview(_ json: String) throws -> ConnectPreviewModel {
        try JSONDecoder().decode(ConnectPreviewModel.self, from: Data(json.utf8))
    }

    private func oauthPreview() -> ConnectPreviewModel {
        ConnectPreviewModel(
            client: "claude-code", configPath: "/Users/x/.claude.json", serverName: "mcpproxy",
            entryText: "{\"mcpproxy\":{\"url\":\"http://127.0.0.1:8080/mcp/oauth/claude-code\"}}",
            entryExists: false, containsAPIKey: false, accessState: .accessible,
            preconditionToken: "tok-oauth", credential: nil, profile: "", mode: nil, keyless: false,
            auth: "oauth", authNote: "Connect does not revoke this client's existing API key.")
    }

    private func selected(
        _ source: FakeConnectSource, available: Bool?, name: String = "Claude Code"
    ) async -> ConnectClientModel {
        source.clientsResults = [.success([FakeConnectSource.client(id: "claude-code", name: name)])]
        source.detailResults = [.success(FakeConnectSource.client(
            id: "claude-code", name: name, browserLoginAvailable: available))]
        let model = ConnectClientModel(source: source, sleeper: { _ in })
        await model.loadList()
        await model.select("claude-code")
        return model
    }

    // MARK: Wire

    func testClientStatusDecodesTheBrowserLoginFlags() throws {
        let on = try decodeStatus("""
        {"id":"claude-code","name":"Claude Code","config_path":"/x","exists":true,"connected":false,
         "supported":true,"supports_browser_login":true,"browser_login_available":true}
        """)
        XCTAssertEqual(on.browserLoginAvailable, true)
        XCTAssertEqual(on.supportsBrowserLogin, true)
        let absent = try decodeStatus("""
        {"id":"cursor","name":"Cursor","config_path":"/x","exists":true,"connected":false,"supported":true}
        """)
        XCTAssertNil(absent.browserLoginAvailable)
        XCTAssertNil(absent.supportsBrowserLogin)
    }

    func testPreviewDecodesAuthAndNote() throws {
        let p = try decodePreview("""
        {"client":"claude-code","config_path":"/x","server_name":"mcpproxy","entry_text":"{}",
         "entry_exists":false,"contains_api_key":false,"access_state":"accessible",
         "precondition_token":"t","auth":"oauth","auth_note":"kept","credential":""}
        """)
        XCTAssertEqual(p.auth, "oauth")
        XCTAssertEqual(p.authNote, "kept")
        XCTAssertTrue(p.isBrowserLogin)
        XCTAssertNil(p.credentialLine)
        let plain = try decodePreview("""
        {"client":"claude-code","config_path":"/x","server_name":"mcpproxy","entry_text":"{}",
         "entry_exists":false,"contains_api_key":false,"access_state":"accessible","precondition_token":"t"}
        """)
        XCTAssertNil(plain.auth)
        XCTAssertFalse(plain.isBrowserLogin)
    }

    func testBindingCarriesAuthOnlyWhenOAuth() {
        XCTAssertEqual(ConnectBinding.unspecified.queryItems, [])
        XCTAssertEqual(ConnectBinding.unspecified.bodyFields.count, 0)
        let b = ConnectBinding(oauth: true)
        XCTAssertEqual(b.queryItems, [URLQueryItem(name: "auth", value: "oauth")])
        XCTAssertEqual(b.bodyFields["auth"] as? String, "oauth")
        XCTAssertNotEqual(b, .unspecified)
    }

    // MARK: Model

    func testTheChoiceIsOfferedOnlyWhenTheCoreSaysSo() async {
        let off = await selected(FakeConnectSource(), available: nil)
        XCTAssertFalse(off.browserLoginOffered)
        let no = await selected(FakeConnectSource(), available: false)
        XCTAssertFalse(no.browserLoginOffered)
        let on = await selected(FakeConnectSource(), available: true)
        XCTAssertTrue(on.browserLoginOffered)
    }

    func testTheDefaultIsAPIKeyAndSendsWhatItAlwaysSent() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        XCTAssertEqual(model.authChoice, .key)
        XCTAssertEqual(model.binding, .unspecified)
        XCTAssertEqual(source.previewBindings, [.unspecified])
        await model.connect()
        XCTAssertEqual(source.connectBindings, [.unspecified])
    }

    func testChoosingTheBrowserDestroysThePreviewAndRefetchesItWithAuth() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        source.previewResults = [.success(oauthPreview())]
        model.chooseAuth(.oauth)
        XCTAssertEqual(model.authChoice, .oauth)
        XCTAssertEqual(model.preview, .idle, "the stale preview is destroyed at once")
        XCTAssertFalse(model.connectControlExists)
        await model.refreshPreview()
        XCTAssertEqual(source.previewBindings.last, ConnectBinding(oauth: true))
        XCTAssertTrue(model.connectControlExists)
        XCTAssertEqual(model.credentialDisclosure,
                       "No credential is written. Claude Code gets only the sign-in address; the first time it connects, your browser opens a page where you approve it.")
        XCTAssertFalse(model.showsManagementNotice)
        XCTAssertFalse(model.showsBindingControls)
    }

    func testTheWriteCarriesAuthAndTheTokenOfTheOAuthPreview() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        source.previewResults = [.success(oauthPreview())]
        model.chooseAuth(.oauth)
        await model.refreshPreview()
        await model.connect()
        XCTAssertEqual(source.connectBindings, [ConnectBinding(oauth: true)])
        XCTAssertEqual(source.connectCalls.last?.preconditionToken, "tok-oauth")
    }

    func testSwitchingBackToTheKeyRestoresTheDefaultRequests() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        model.chooseAuth(.oauth)
        model.chooseAuth(.key)
        XCTAssertEqual(model.binding, .unspecified)
        XCTAssertTrue(model.showsBindingControls)
    }

    func testAChoiceTheCoreDoesNotOfferIsIgnored() async {
        let model = await selected(FakeConnectSource(), available: nil)
        model.chooseAuth(.oauth)
        XCTAssertEqual(model.authChoice, .key)
        XCTAssertEqual(model.binding, .unspecified)
    }

    func testSelectingAnotherClientStartsOnTheKeyAgain() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        model.chooseAuth(.oauth)
        await model.select("claude-code")
        XCTAssertEqual(model.authChoice, .key)
    }

    /// Reselecting the client while its next preview is still in flight: the old
    /// browser preview is gone at once and no Connect control exists for it.
    func testReselectingDuringAPreviewLeavesNothingConfirmable() async {
        let source = FakeConnectSource()
        let model = await selected(source, available: true)
        source.previewResults = [.success(oauthPreview())]
        model.chooseAuth(.oauth)
        await model.refreshPreview()
        XCTAssertTrue(model.connectControlExists)

        var duringExists = true
        var duringChoice = ConnectClientModel.AuthChoice.oauth
        source.duringPreview = { _ in
            await MainActor.run {
                duringExists = model.connectControlExists
                duringChoice = model.authChoice
            }
        }
        source.previewResults = [.success(FakeConnectSource.preview())]
        await model.select("claude-code")
        XCTAssertFalse(duringExists, "no Connect control while the new preview is loading")
        XCTAssertEqual(duringChoice, .key)
        await model.connect()
        XCTAssertEqual(source.connectBindings.last, .unspecified, "the key write carries the key preview's token only")
        XCTAssertEqual(source.connectCalls.last?.preconditionToken, "tok")
    }

    func testBindingControlsCannotOverrideTheBrowserChoice() async {
        let model = await selected(FakeConnectSource(), available: true)
        model.chooseAuth(.oauth)
        model.chooseProfile("work")
        model.setKeyless(true)
        XCTAssertEqual(model.binding, ConnectBinding(oauth: true))
    }

    func testTheChoiceHasUniqueAccessibilityIdentifiers() {
        let ids = ConnectClientAccessibility.allIdentifiers
        XCTAssertEqual(Set(ids).count, ids.count)
        XCTAssertTrue(ids.contains(ConnectClientAccessibility.authChoice))
        XCTAssertTrue(ConnectClientAccessibility.authChoice.hasPrefix("connect-"))
        XCTAssertTrue(ConnectClientAccessibility.authNote.hasPrefix("connect-"))
    }
}
