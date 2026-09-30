import XCTest
@testable import MCPProxy

/// Spec 108-k (K1, K2): every Profiles v3 method's method, path, query and
/// body, and how a non-2xx answer becomes a structured `.service` error.
final class APIClientProfilesTests: XCTestCase {

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    private var client: APIClient { ConnectStubURLProtocol.makeClient() }

    private var last: ConnectStubURLProtocol.Recorded? { ConnectStubURLProtocol.recorded.last }

    private let clientRow = """
    {"id":"cursor","display_name":"Cursor","kind":"supported","state":"connected_seen","installed":true,
     "connected":true,"active_sessions":0,"calls_24h":0,"credential_state":"client","profile":"ro","profile_mode":"locked"}
    """

    // MARK: Profiles

    func testProfilesV3RequestsTheListAndDecodesUsedBy() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"profiles":[{"name":"ro","servers":["github"],"tool_count":2,"used_by":{"clients":[{"id":"cursor","mode":"locked"}],"tokens":[],"anonymous_profile":false}}],"anonymous_profile":"ro"}
        """)
        let list = try await client.profilesV3()
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles")
        XCTAssertEqual(last?.method, "GET")
        XCTAssertEqual(list.profiles.first?.usedBy?.clients.first?.id, "cursor")
        XCTAssertEqual(list.anonymousProfile, "ro")
    }

    func testCreateProfilePostsTheExactPayload() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"profile":{"name":"ro","servers":["github"]},"warnings":["w"]}"#)
        var payload = ProfileConfigPayload(name: "ro")
        payload.servers = ["github"]
        payload.maxTier = "read"
        let response = try await client.createProfile(payload)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles")
        XCTAssertEqual(last?.method, "POST")
        XCTAssertEqual(last?.json["name"] as? String, "ro")
        XCTAssertEqual(last?.json["max_tier"] as? String, "read")
        XCTAssertNil(last?.json["code_execution"])
        XCTAssertEqual(response.warnings, ["w"])
    }

    func testUpdateProfilePutsToTheEncodedName() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"profile":{"name":"work ro"},"warnings":[]}"#)
        _ = try await client.updateProfile("work ro", ProfileConfigPayload(name: "work ro"))
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/work%20ro")
        XCTAssertEqual(last?.method, "PUT")
    }

    func testRenameProfilePostsNewName() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"profile":{"name":"b"},"moved":{"clients":["cursor"],"tokens":[]}}"#)
        let response = try await client.renameProfile("a", newName: "b")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/a/rename")
        XCTAssertEqual(last?.method, "POST")
        XCTAssertEqual(last?.json["new_name"] as? String, "b")
        XCTAssertEqual(response.moved.clients, ["cursor"])
    }

    func testDeleteProfileSendsReassignToAndForceAsQuery() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"deleted":"a","moved":{"clients":[],"tokens":[]}}"#)
        _ = try await client.deleteProfile("a", reassignTo: "b", force: true)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/a?reassign_to=b&force=true")
        XCTAssertEqual(last?.method, "DELETE")
        _ = try await client.deleteProfile("a")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/a")
    }

    func testEffectiveToolsQuery() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"profile":"a","tools":[],"counts":{"visible":1,"hidden":2}}"#)
        let response = try await client.effectiveTools("a", client: "cursor", server: "github", reason: "above_tier_cap")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/a/effective-tools?client=cursor&server=github&reason=above_tier_cap")
        XCTAssertEqual(response.counts?.hidden, 2)
    }

    func testTryProfilePostsTheDraftAndTheQuery() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"results":[{"name":"create_issue","server_name":"github","description":"d"}],"hidden_by_profile":3,"hidden":[{"server":"github","tool":"x","reason":"above_tier_cap"}]}"#)
        var draft = ProfileConfigPayload(name: "ro")
        draft.maxTier = "read"
        let response = try await client.tryProfile(draft: draft, query: "issue", limit: 5)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/profiles/try")
        XCTAssertEqual(last?.method, "POST")
        XCTAssertEqual(last?.json["query"] as? String, "issue")
        XCTAssertEqual(last?.json["limit"] as? Int, 5)
        XCTAssertEqual((last?.json["profile"] as? [String: Any])?["max_tier"] as? String, "read")
        XCTAssertEqual(response.hiddenByProfile, 3)
        XCTAssertEqual(response.results.first?.id, "github:create_issue")
    }

    // MARK: Clients

    func testClientsV3SendsTheFiltersAndDecodesWarnings() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"clients":[\(clientRow)],"routing":{},"warnings":[{"code":"client_holds_admin_key","severity":"warn","message":"m","action":{"kind":"upgrade_admin_key_holders"}}]}
        """)
        let response = try await client.clientsV3(profile: "ro", client: "cursor")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients?profile=ro&client=cursor")
        XCTAssertEqual(response.clients.first?.profileMode, .locked)
        XCTAssertEqual(response.warnings?.first?.code, "client_holds_admin_key")
    }

    func testSetBindingOmitsTheModeWhenNil() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("{\"client\":\(clientRow),\"warnings\":[]}")
        _ = try await client.setBinding("cursor", profile: "ro")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/cursor/binding")
        XCTAssertEqual(last?.method, "PUT")
        XCTAssertEqual(last?.json["profile"] as? String, "ro")
        XCTAssertNil(last?.json["mode"], "mode omitted = keep the credential's own")

        _ = try await client.setBinding("cursor", profile: "ro", mode: .locked)
        XCTAssertEqual(last?.json["mode"] as? String, "locked")
        _ = try await client.setBinding("cursor", profile: "")
        XCTAssertEqual(last?.json["profile"] as? String, "", "All servers is an explicit empty string")
    }

    func testBulkAssign() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"moved":["cursor"],"skipped":[{"client_id":"codex","code":"no_client_credential","error":"e"}]}"#)
        let response = try await client.bulkAssign(from: "a", to: "", mode: nil)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/bulk-assign")
        XCTAssertEqual(last?.json["from_profile"] as? String, "a")
        XCTAssertEqual(last?.json["to_profile"] as? String, "")
        XCTAssertNil(last?.json["mode"])
        XCTAssertEqual(response.skipped.first?.code, "no_client_credential")
    }

    func testAddCustomClientPostsAndReturnsTheCredentialOnce() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"client":\(clientRow),"credential":"mcp_cli_ABC","snippet":{"generic_http":"curl","header_name":"Authorization"}}
        """)
        let response = try await client.addCustomClient(id: "dev", displayName: "Dev", profile: "ro", mode: .locked, expiresIn: "90d")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients")
        XCTAssertEqual(last?.json["id"] as? String, "dev")
        XCTAssertEqual(last?.json["display_name"] as? String, "Dev")
        XCTAssertEqual(last?.json["profile"] as? String, "ro")
        XCTAssertEqual(last?.json["mode"] as? String, "locked")
        XCTAssertEqual(last?.json["expires_in"] as? String, "90d")
        XCTAssertEqual(response.credential, "mcp_cli_ABC")
    }

    func testRotateAndFinalizeAndForget() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("{\"client\":\(clientRow),\"rotation\":{\"state\":\"finalized\"}}")
        _ = try await client.rotateClient("cursor", preconditionToken: "tok")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/cursor/rotate")
        XCTAssertEqual(last?.json["precondition_token"] as? String, "tok")

        _ = try await client.finalizeRotation("cursor")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/cursor/rotate/finalize")
        XCTAssertEqual(last?.method, "POST")

        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"revoked":"client-cursor","disconnected":true}"#)
        let forgotten = try await client.forgetClient("cursor", disconnect: true)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/cursor?disconnect=true")
        XCTAssertEqual(last?.method, "DELETE")
        XCTAssertTrue(forgotten.disconnected)
    }

    /// Rotating a supported client writes its config, so it never falls back to TCP.
    func testRotateRefusesToRideTcp() async {
        do {
            _ = try await ConnectStubURLProtocol.makeClient(transportKind: .tcp).rotateClient("cursor")
            XCTFail("expected socketRequired")
        } catch APIClientError.socketRequired {
            XCTAssertTrue(ConnectStubURLProtocol.recorded.isEmpty, "nothing was sent")
        } catch {
            XCTFail("\(error)")
        }
    }

    func testUpgradePreviewThenApplySendsTheToken() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"preview":[],"precondition_token":"c","next_step":"rotate_admin_api_key"}"#)
        let preview = try await client.previewAdminKeyUpgrade(profile: "ro", mode: .locked)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/clients/upgrade-admin-key-holders")
        XCTAssertEqual(last?.json["profile"] as? String, "ro")
        XCTAssertNil(last?.json["apply"])
        XCTAssertEqual(preview.nextStep, "rotate_admin_api_key")

        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"upgraded":["cursor"],"failed":[]}"#)
        let result = try await client.applyAdminKeyUpgrade(profile: nil, mode: nil, preconditionToken: "c")
        XCTAssertEqual(last?.json["apply"] as? Bool, true)
        XCTAssertEqual(last?.json["precondition_token"] as? String, "c")
        XCTAssertNil(last?.json["profile"], "no profile: the guard never has a binding to refuse")
        XCTAssertEqual(result.upgraded, ["cursor"])
    }

    // MARK: Explain, scoped reads, tokens

    func testExplainSendsOneSubject() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"subject":{"kind":"client","name":"cursor"},"tool":"github:create_issue","profile":{"name":"ro","source":"pin"},"steps":[],"verdict":"allowed","first_failure":"","fixes":[]}
        """)
        let explanation = try await client.explain(tool: "github:create_issue", subject: .client("cursor"))
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/access/explain?tool=github%3Acreate_issue&client=cursor")
        XCTAssertEqual(explanation.verdict, .allowed)
        XCTAssertNil(explanation.firstFailure)

        _ = try await client.explain(tool: "a:b", subject: .anonymous)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/access/explain?tool=a%3Ab&anonymous=true")
    }

    func testViewAsToolsSendsClientOrProfile() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"tools":[{"name":"t","server_name":"s","access":{"visible":false,"callable":false,"reason":"above_tier_cap"},"profile_tier":"write"}]}"#)
        let response = try await client.viewAsTools(client: "cursor")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/tools?client=cursor")
        XCTAssertEqual(response.tools?.first?.access?.reason, "above_tier_cap")
        XCTAssertEqual(response.tools?.first?.profileTier, "write")
        _ = try await client.viewAsTools(profile: "ro")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/tools?profile=ro")
    }

    func testServersAndTokensFilters() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"servers":[]}"#)
        _ = try await client.servers(profile: "ro")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/servers?profile=ro")
        _ = try await client.servers(profile: nil)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/servers")

        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"tokens":[]}"#)
        _ = try await client.tokens(profile: "-", token: "ci")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/tokens?profile=-&token=ci")
    }

    func testCreateTokenReturnsTheSecretAndSurfacesAFieldError() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"name":"ci","token":"mcp_agt_SECRET"}"#)
        let secret = try await client.createToken(body: ["name": "ci", "profile": "ro", "expires_in": "30d"])
        XCTAssertEqual(secret, "mcp_agt_SECRET")
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/tokens")
        XCTAssertEqual(last?.json["profile"] as? String, "ro")

        ConnectStubURLProtocol.statusCode = 400
        ConnectStubURLProtocol.responseBody = Data(#"{"success":false,"error":"reserved","field":"name"}"#.utf8)
        do {
            _ = try await client.createToken(body: ["name": "client-x"])
            XCTFail("expected a service error")
        } catch APIClientError.service(let status, let body) {
            XCTAssertEqual(status, 400)
            XCTAssertEqual(body.field, "name")
        }
    }

    // MARK: Structured errors (K1)

    func testAGuardConflictDecodesBindingsAndFixes() async {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"could escape","code":"binding_bypassable_without_auth",
         "bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"ro","mode":"locked"}],
         "fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"ro"}]}
        """.utf8)
        do {
            _ = try await client.setBinding("cursor", profile: "ro")
            XCTFail("expected a service error")
        } catch APIClientError.service(let status, let body) {
            XCTAssertEqual(status, 409)
            XCTAssertTrue(body.isGuardRefusal)
            XCTAssertEqual(body.bindings?.first?.clientId, "cursor")
            XCTAssertEqual(body.fixes, [GuardFix(kind: "require_mcp_auth", target: nil),
                                        GuardFix(kind: "set_anonymous_profile", target: "ro")])
        } catch {
            XCTFail("\(error)")
        }
    }

    func testAProfileInUseConflictCarriesUsedBy() async {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"profile in use","code":"profile_in_use",
         "used_by":{"clients":[{"id":"cursor","mode":"locked"}],"tokens":["ci"],"anonymous_profile":false}}
        """.utf8)
        do {
            _ = try await client.deleteProfile("ro")
            XCTFail("expected a service error")
        } catch APIClientError.service(_, let body) {
            XCTAssertEqual(body.code, "profile_in_use")
            XCTAssertEqual(body.usedBy?.clients.first?.id, "cursor")
            XCTAssertEqual(body.usedBy?.tokens, ["ci"])
        } catch {
            XCTFail("\(error)")
        }
    }

    func testAFieldErrorIsDecodedAndTheDescriptionIsTheMessage() async {
        ConnectStubURLProtocol.statusCode = 400
        ConnectStubURLProtocol.responseBody = Data(#"{"success":false,"error":"bad max_tier","field":"max_tier"}"#.utf8)
        do {
            _ = try await client.updateProfile("ro", ProfileConfigPayload(name: "ro"))
            XCTFail("expected a service error")
        } catch let error as APIClientError {
            guard case .service(_, let body) = error else { return XCTFail("\(error)") }
            XCTAssertEqual(body.field, "max_tier")
            XCTAssertEqual(error.errorDescription, "bad max_tier", "the message stays available through localizedDescription")
        } catch {
            XCTFail("\(error)")
        }
    }

    func testANonJsonErrorBodyStillProducesAServiceErrorWithTheStatusText() async {
        ConnectStubURLProtocol.statusCode = 503
        ConnectStubURLProtocol.responseBody = Data("<html>down</html>".utf8)
        do {
            _ = try await client.profilesV3()
            XCTFail("expected a service error")
        } catch APIClientError.service(let status, let body) {
            XCTAssertEqual(status, 503)
            XCTAssertFalse(body.error.isEmpty)
        } catch {
            XCTFail("\(error)")
        }
    }

    // MARK: Headers

    func testEveryRequestCarriesTheTrayClientHeader() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"profiles":[]}"#)
        _ = try await client.profilesV3()
        XCTAssertTrue(last?.headers["X-MCPProxy-Client"]?.hasPrefix("tray/") ?? false,
                      "the core maps this to surface=macos on profile_change records")
    }

    // MARK: Connect with a binding (T042m)

    func testConnectPreviewCarriesTheBindingAsQuery() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"client":"codex","config_path":"/x","server_name":"mcpproxy","entry_text":"e","entry_exists":false,"contains_api_key":false,
         "access_state":"accessible","credential":"mcp_cli_••••","profile":"ro","mode":"locked","keyless":false,"precondition_token":"t"}
        """)
        let preview = try await client.connectPreview(
            "codex", serverName: "mcpproxy", binding: ConnectBinding(profile: "ro", mode: .locked))
        let url = try XCTUnwrap(last?.url)
        XCTAssertTrue(url.contains("/api/v1/connect/codex/preview?"))
        XCTAssertTrue(url.contains("profile=ro"))
        XCTAssertTrue(url.contains("mode=locked"))
        XCTAssertFalse(url.contains("keyless"))
        XCTAssertEqual(preview.credential, "mcp_cli_••••")
        XCTAssertEqual(preview.mode, .locked)
        XCTAssertFalse(preview.containsAPIKey)
    }

    func testAnUnspecifiedBindingSendsNothing() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"client":"c","config_path":"/x","server_name":"mcpproxy","entry_text":"e","entry_exists":false,"contains_api_key":false,"access_state":"accessible"}"#)
        _ = try await client.connectPreview("c", serverName: "mcpproxy", binding: .unspecified)
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/connect/c/preview?server_name=mcpproxy")
    }

    func testConnectWithABindingSendsProfileModeAndKeylessInTheBody() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"success":true,"client":"codex","config_path":"/x","action":"created","message":"ok",
         "credential":"mcp_cli_••••","token_name":"client-codex","profile":"ro","mode":"locked"}
        """)
        let result = try await client.connect(
            "codex", serverName: "mcpproxy", force: false, preconditionToken: "tok",
            binding: ConnectBinding(profile: "ro", mode: .locked))
        XCTAssertEqual(last?.url, "http://127.0.0.1:8080/api/v1/connect/codex")
        XCTAssertEqual(last?.json["profile"] as? String, "ro")
        XCTAssertEqual(last?.json["mode"] as? String, "locked")
        XCTAssertEqual(last?.json["precondition_token"] as? String, "tok")
        XCTAssertNil(last?.json["keyless"])
        XCTAssertEqual(result.credentialLine, "Credential: mcp_cli_•••• (token client-codex, profile ro, locked)")
    }

    func testAnUnboundConnectSendsNoBindingFields() async throws {
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope(#"{"success":true,"action":"created","message":"ok"}"#)
        _ = try await client.connect("c", serverName: "mcpproxy", force: false, preconditionToken: nil, binding: .unspecified)
        XCTAssertNil(last?.json["profile"], "a reconnect must keep an existing binding, never widen it")
        XCTAssertNil(last?.json["mode"])
    }

    func testConnectKeepsTheTypedPreconditionConflictAndDecodesAGuard() async {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data(#"{"success":false,"data":{"success":false,"action":"precondition_failed","message":"changed"}}"#.utf8)
        do {
            _ = try await client.connect("c", serverName: "mcpproxy", force: true, preconditionToken: "t", binding: .unspecified)
            XCTFail("expected a conflict")
        } catch APIClientError.connectConflict(let action, _, _, _) {
            XCTAssertEqual(action, "precondition_failed")
        } catch {
            XCTFail("\(error)")
        }

        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"could escape","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}
        """.utf8)
        do {
            _ = try await client.connect("c", serverName: "mcpproxy", force: false, preconditionToken: nil,
                                         binding: ConnectBinding(profile: "ro", mode: .locked))
            XCTFail("expected a refusal")
        } catch APIClientError.service(_, let body) {
            XCTAssertTrue(body.isGuardRefusal)
        } catch {
            XCTFail("\(error)")
        }
    }

    func testAConflictingTokenIsDecodedWithItsRemediation() async {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"error":"token name client-codex is held","conflicting_token":"client-codex","remediation":"revoke it"}
        """.utf8)
        do {
            _ = try await client.connect("codex", serverName: "mcpproxy", force: false, preconditionToken: nil, binding: .unspecified)
            XCTFail("expected a refusal")
        } catch APIClientError.service(_, let body) {
            XCTAssertEqual(body.conflictingToken, "client-codex")
            XCTAssertEqual(body.remediation, "revoke it")
        } catch {
            XCTFail("\(error)")
        }
    }

    func testConnectRefusesToRideTcp() async {
        do {
            _ = try await ConnectStubURLProtocol.makeClient(transportKind: .tcp).connect(
                "c", serverName: "mcpproxy", force: false, preconditionToken: nil, binding: .unspecified)
            XCTFail("expected socketRequired")
        } catch APIClientError.socketRequired {
            XCTAssertTrue(ConnectStubURLProtocol.recorded.isEmpty)
        } catch {
            XCTFail("\(error)")
        }
    }

    // MARK: Removed methods (FR-039: no first-party caller)

    func testTheV2ActiveProfileSurfaceIsGone() throws {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<2 { url.deleteLastPathComponent() }
        let api = try String(contentsOf: url.appendingPathComponent("MCPProxy/API/APIClient.swift"))
        XCTAssertFalse(api.contains("func setActiveProfile"))
        XCTAssertFalse(api.contains("func activeProfile"))
        XCTAssertFalse(api.contains("/profiles/active"))
    }
}
