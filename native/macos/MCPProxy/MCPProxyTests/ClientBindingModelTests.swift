import XCTest
@testable import MCPProxy

/// Spec 108-k (K9, K10, K11, K13): the profile controls of a Clients row, the
/// warning and guard-fix navigation, the bulk move, the admin-key upgrade, the
/// custom-client credential and the staged rotation.
@MainActor
final class ClientBindingModelTests: XCTestCase {

    // MARK: Stub

    final class StubSource: ClientBindingSource, @unchecked Sendable {
        var bindingResult: Result<ClientBindingResponse, Error>?
        var bulkResult: Result<BulkAssignResponse, Error> = .success(BulkAssignResponse())
        var customResult: Result<CustomClientResponse, Error>?
        var rotateResult: Result<RotateResponse, Error>?
        var finalizeResult: Result<FinalizeRotationResponse, Error>?
        var previewResults: [Result<UpgradePreview, Error>] = []
        var applyResult: Result<UpgradeResult, Error>?
        var connectPreviewResult: Result<ConnectPreviewModel, Error>?
        var forgetResult: Result<ForgetClientResponse, Error>?
        /// Runs inside `previewAdminKeyUpgrade`, i.e. while the preview is in flight.
        var onPreview: (@MainActor () -> Void)?

        private(set) var bindings: [(id: String, profile: String, mode: BindingMode?)] = []
        private(set) var bulks: [(String, String, BindingMode?)] = []
        private(set) var applies: [String?] = []
        private(set) var appliedProfiles: [String?] = []
        private(set) var previews: [(String?, BindingMode?)] = []
        private(set) var forgets: [(String, Bool)] = []
        private(set) var rotations: [String?] = []
        private(set) var order: [String] = []

        func setBinding(_ clientId: String, profile: String, mode: BindingMode?) async throws -> ClientBindingResponse {
            bindings.append((clientId, profile, mode))
            return try (bindingResult ?? .success(ClientBindingModelTests.bindingResponse(id: clientId, profile: profile, mode: mode ?? .switchable))).get()
        }
        func bulkAssign(from: String, to: String, mode: BindingMode?) async throws -> BulkAssignResponse {
            bulks.append((from, to, mode))
            return try bulkResult.get()
        }
        func addCustomClient(id: String, displayName: String?, profile: String?, mode: BindingMode?, expiresIn: String?) async throws -> CustomClientResponse {
            try (customResult ?? .failure(APIClientError.noData)).get()
        }
        func rotateClient(_ id: String, preconditionToken: String?) async throws -> RotateResponse {
            order.append("rotate")
            rotations.append(preconditionToken)
            return try (rotateResult ?? .failure(APIClientError.noData)).get()
        }
        func finalizeRotation(_ id: String) async throws -> FinalizeRotationResponse {
            order.append("finalize")
            return try (finalizeResult ?? .failure(APIClientError.noData)).get()
        }
        func forgetClient(_ id: String, disconnect: Bool) async throws -> ForgetClientResponse {
            forgets.append((id, disconnect))
            return try (forgetResult ?? .success(ClientBindingModelTests.forgetResponse(#"{"revoked":"client-x","disconnected":false}"#))).get()
        }
        func previewAdminKeyUpgrade(profile: String?, mode: BindingMode?) async throws -> UpgradePreview {
            previews.append((profile, mode))
            await onPreview?()
            return try (previewResults.count > 1 ? previewResults.removeFirst() : previewResults[0]).get()
        }
        func applyAdminKeyUpgrade(profile: String?, mode: BindingMode?, preconditionToken: String?) async throws -> UpgradeResult {
            applies.append(preconditionToken)
            appliedProfiles.append(profile)
            return try (applyResult ?? .failure(APIClientError.noData)).get()
        }
        func connectPreview(_ clientId: String, serverName: String, binding: ConnectBinding) async throws -> ConnectPreviewModel {
            order.append("preview")
            return try (connectPreviewResult ?? .success(FakeConnectSource.preview(token: "row-token"))).get()
        }
    }

    // MARK: Fixtures

    nonisolated static func record(
        id: String = "cursor", kind: String = "supported", credential: String? = "client",
        profile: String = "", mode: String = "switchable", pending: Bool = false
    ) -> ClientPresenceRecord {
        var json: [String: Any] = [
            "id": id, "display_name": id.capitalized, "kind": kind, "state": "connected_seen",
            "installed": true, "connected": true, "active_sessions": 0, "calls_24h": 0,
            "profile": profile, "profile_mode": mode, "rotation_pending": pending,
        ]
        if let credential { json["credential_state"] = credential }
        return try! JSONDecoder().decode(ClientPresenceRecord.self, from: JSONSerialization.data(withJSONObject: json))
    }

    nonisolated static func forgetResponse(_ json: String) -> ForgetClientResponse {
        try! JSONDecoder().decode(ForgetClientResponse.self, from: Data(json.utf8))
    }

    nonisolated static func bindingResponse(id: String, profile: String, mode: BindingMode) -> ClientBindingResponse {
        let object: [String: Any] = [
            "client": [
                "id": id, "display_name": id.capitalized, "kind": "supported", "state": "connected_seen",
                "installed": true, "connected": true, "active_sessions": 0, "calls_24h": 0,
                "credential_state": "client", "profile": profile, "profile_mode": mode.wire,
            ],
            "warnings": [],
        ]
        return try! JSONDecoder().decode(ClientBindingResponse.self, from: JSONSerialization.data(withJSONObject: object))
    }

    private func service(_ status: Int, _ json: String) -> APIClientError {
        .service(status: status, body: try! JSONDecoder().decode(ServiceErrorBody.self, from: Data(json.utf8)))
    }

    // MARK: Controls state (K9)

    func testAnActiveClientCredentialEnablesThePickerAndToggle() {
        let state = ClientBindingControlsState(Self.record(profile: "work-ro", mode: "locked"))
        XCTAssertTrue(state.canBind)
        XCTAssertNil(state.cta)
        XCTAssertTrue(state.isLocked)
        XCTAssertTrue(state.lockEnabled)
        XCTAssertNil(state.lockHelp)
        XCTAssertEqual(state.sourceLabel, "locked by credential")
        XCTAssertEqual(state.credentialBadge, "Client credential")
    }

    func testTheLockToggleIsDisabledOnAllServersWithAHelpText() {
        let state = ClientBindingControlsState(Self.record(profile: ""))
        XCTAssertTrue(state.canBind)
        XCTAssertFalse(state.lockEnabled)
        XCTAssertEqual(state.lockHelp, "Choose a profile to lock")
        XCTAssertEqual(state.sourceLabel, "switchable")
    }

    func testEveryNonClientCredentialStateDisablesTheControlsAndOffersACTA() {
        let expected: [(String?, ClientBindingControlsState.CTA, String)] = [
            ("none", .connect, "No credential"), ("admin_key", .upgrade, "Admin key"),
            ("unknown", .connect, "Unknown"), (nil, .connect, "Unknown"),
            ("revoked", .reconnect, "Revoked"), ("expired", .reconnect, "Expired"),
        ]
        for (credential, cta, badge) in expected {
            let state = ClientBindingControlsState(Self.record(credential: credential))
            XCTAssertFalse(state.canBind, "\(String(describing: credential))")
            XCTAssertEqual(state.cta, cta, "\(String(describing: credential))")
            XCTAssertEqual(state.credentialBadge, badge)
            XCTAssertFalse(state.lockEnabled)
        }
        XCTAssertEqual(ClientBindingControlsState.CTA.connect.title, "Connect…")
        XCTAssertEqual(ClientBindingControlsState.CTA.upgrade.title, "Upgrade to client credential…")
        XCTAssertEqual(ClientBindingControlsState.CTA.reconnect.title, "Reconnect…")
    }

    /// Spec 108 D39 (T150): a client that never connected has nothing to
    /// upgrade; only a known admin-key holder reads "Upgrade".
    func testNoneAndUnknownShowConnect() {
        for credential in ["none", "unknown", nil] as [String?] {
            let state = ClientBindingControlsState(Self.record(credential: credential))
            XCTAssertEqual(state.cta?.title, "Connect…", "\(String(describing: credential))")
        }
        XCTAssertEqual(ClientBindingControlsState(Self.record(credential: "admin_key")).cta?.title,
                       "Upgrade to client credential…")
        XCTAssertEqual(ClientBindingControlsState(Self.record(credential: "revoked")).cta?.title, "Reconnect…")
    }

    func testTheAccessibilityLabelCarriesTheState() {
        let state = ClientBindingControlsState(Self.record(id: "cursor", profile: "work-ro", mode: "locked"))
        XCTAssertTrue(state.accessibilityLabel.contains("Cursor profile"))
        XCTAssertTrue(state.accessibilityLabel.contains("locked"))
    }

    // MARK: Row actions

    func testThePickerSendsNoModeAndTheToggleSendsProfileAndMode() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let row = Self.record(profile: "work-ro", mode: "locked")

        let moved = await model.chooseProfile(row, profile: "work-full")
        XCTAssertEqual(source.bindings.last?.profile, "work-full")
        XCTAssertNil(source.bindings.last?.mode, "the picker keeps the credential's own mode")
        XCTAssertEqual(moved?.boundProfile, "work-full", "the row refreshes from the response")

        _ = await model.setLocked(row, locked: false)
        XCTAssertEqual(source.bindings.last?.profile, "work-ro")
        XCTAssertEqual(source.bindings.last?.mode, .switchable)
        _ = await model.setLocked(Self.record(profile: "work-ro", mode: "switchable"), locked: true)
        XCTAssertEqual(source.bindings.last?.mode, .locked)
    }

    /// FR-026: a row without an ACTIVE client credential never reaches the core.
    func testARowWithoutAClientCredentialNeverCallsSetBinding() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        for credential in ["none", "admin_key", "unknown", "revoked", "expired"] {
            let row = Self.record(credential: credential, profile: "x")
            let picked = await model.chooseProfile(row, profile: "work-ro")
            let locked = await model.setLocked(row, locked: true)
            XCTAssertNil(picked)
            XCTAssertNil(locked)
        }
        XCTAssertTrue(source.bindings.isEmpty, "setBinding must never be called")
    }

    func testLockingAllServersIsNotSent() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let result = await model.setLocked(Self.record(profile: ""), locked: true)
        XCTAssertNil(result)
        XCTAssertTrue(source.bindings.isEmpty)
    }

    func testAGuardRefusalOfABindingChangeIsKeptForTheGuardView() async {
        let source = StubSource()
        source.bindingResult = .failure(service(409, """
        {"error":"could escape","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}
        """))
        let model = ClientBindingModel(source: source)
        _ = await model.chooseProfile(Self.record(profile: "a"), profile: "b")
        XCTAssertEqual(model.guardRefusal?.fixes?.first?.kind, "require_mcp_auth")
        XCTAssertNil(model.errorMessage)
    }

    func testForgetClearsAStaleGuardRefusalFromAnEarlierBindingChange() async {
        let source = StubSource()
        source.bindingResult = .failure(service(409, """
        {"error":"could escape","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}
        """))
        source.forgetResult = .failure(APIClientError.notReady)
        let model = ClientBindingModel(source: source)
        _ = await model.chooseProfile(Self.record(profile: "a"), profile: "b")
        XCTAssertNotNil(model.guardRefusal)
        _ = await model.forget(Self.record(), disconnect: false)
        XCTAssertNil(model.guardRefusal, "the Forget sheet must not show the earlier binding refusal")
        XCTAssertNotNil(model.errorMessage)
    }

    func testFinalizeRotationClearsAStaleGuardRefusal() async {
        let source = StubSource()
        source.bindingResult = .failure(service(409, """
        {"error":"could escape","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}
        """))
        source.finalizeResult = .failure(APIClientError.notReady)
        let model = ClientBindingModel(source: source)
        _ = await model.chooseProfile(Self.record(profile: "a"), profile: "b")
        _ = await model.finalizeRotation(Self.record())
        XCTAssertNil(model.guardRefusal)
    }

    // MARK: Assign (108-retro-mac R4)

    func testAssignSendsProfileAndModeInOneRequest() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let outcome = await model.assign(Self.record(profile: "", mode: "switchable"), to: "work", locked: true)
        XCTAssertEqual(source.bindings.count, 1)
        XCTAssertEqual(source.bindings[0].id, "cursor")
        XCTAssertEqual(source.bindings[0].profile, "work")
        XCTAssertEqual(source.bindings[0].mode, .locked)
        guard case .changed(let row)? = outcome else { return XCTFail("\(String(describing: outcome))") }
        XCTAssertTrue(row.isLocked)
    }

    func testAssignChangesOnlyTheLockOfAClientAlreadyOnTheProfile() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let outcome = await model.assign(Self.record(profile: "work", mode: "switchable"), to: "work", locked: true)
        XCTAssertEqual(source.bindings.count, 1)
        XCTAssertEqual(source.bindings[0].profile, "work")
        XCTAssertEqual(source.bindings[0].mode, .locked)
        guard case .changed? = outcome else { return XCTFail("\(String(describing: outcome))") }
    }

    func testAssignOfAnUnchangedBindingSendsNothing() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let outcome = await model.assign(Self.record(profile: "work", mode: "locked"), to: "work", locked: true)
        XCTAssertTrue(source.bindings.isEmpty)
        guard case .unchanged? = outcome else { return XCTFail("\(String(describing: outcome))") }
        XCTAssertEqual(model.announcement, "Cursor is already on work, locked.")
    }

    func testAssignRefusesARowWithoutAClientCredential() async {
        let source = StubSource()
        let model = ClientBindingModel(source: source)
        let outcome = await model.assign(Self.record(credential: "admin_key"), to: "work", locked: true)
        XCTAssertNil(outcome)
        XCTAssertTrue(source.bindings.isEmpty)
    }

    func testAssignKeepsAGuardRefusal() async {
        let source = StubSource()
        source.bindingResult = .failure(service(409, """
        {"error":"could escape","code":"binding_bypassable_without_auth","bindings":[],"fixes":[{"kind":"require_mcp_auth"}]}
        """))
        let model = ClientBindingModel(source: source)
        let outcome = await model.assign(Self.record(profile: "a"), to: "b", locked: true)
        XCTAssertNil(outcome)
        XCTAssertEqual(model.guardRefusal?.fixes?.first?.kind, "require_mcp_auth")
    }

    func testAssignNotesNameTheWantedEndState() {
        let row = Self.record()
        XCTAssertEqual(AssignOutcome.changed(row).note(displayName: "Cursor", title: "Work", locked: true), "Cursor is now on Work, locked.")
        XCTAssertEqual(AssignOutcome.changed(row).note(displayName: "Cursor", title: "Work", locked: false), "Cursor is now on Work, switchable.")
        XCTAssertEqual(AssignOutcome.unchanged(row).note(displayName: "Cursor", title: "Work", locked: false), "Cursor is already on Work, switchable.")
    }

    // MARK: Forget (108-retro-mac R1)

    func testForgetResultWarnsWhenTheConfigEntryCouldNotBeRemoved() {
        let response = Self.forgetResponse(#"{"revoked":"client-cursor","disconnected":false,"disconnect_error":"permission denied"}"#)
        let result = ForgetResult(response, displayName: "Cursor")
        XCTAssertTrue(result.isWarning)
        XCTAssertEqual(result.text, "Credential revoked; the config entry could not be removed: permission denied")
    }

    func testForgetResultSaysTheEntryWasRemoved() {
        let response = Self.forgetResponse(#"{"revoked":"client-cursor","disconnected":true}"#)
        let result = ForgetResult(response, displayName: "Cursor")
        XCTAssertFalse(result.isWarning)
        XCTAssertEqual(result.text, "Credential revoked and MCPProxy removed from Cursor's config.")
    }

    func testForgetResultWithoutDisconnectSaysOnlyRevoked() {
        let response = Self.forgetResponse(#"{"revoked":"client-cursor","disconnected":false}"#)
        let result = ForgetResult(response, displayName: "Cursor")
        XCTAssertFalse(result.isWarning)
        XCTAssertEqual(result.text, "Credential revoked.")
    }

    func testForgetPassesDisconnectAndAnnouncesTheResult() async {
        let source = StubSource()
        let response = Self.forgetResponse(#"{"revoked":"client-cursor","disconnected":false,"disconnect_error":"permission denied"}"#)
        source.forgetResult = .success(response)
        let model = ClientBindingModel(source: source)
        let returned = await model.forget(Self.record(), disconnect: true)
        XCTAssertEqual(returned, response)
        XCTAssertEqual(source.forgets.count, 1)
        XCTAssertEqual(source.forgets[0].0, "cursor")
        XCTAssertTrue(source.forgets[0].1)
        XCTAssertEqual(model.announcement, ForgetResult(response, displayName: "Cursor").text)
    }

    // MARK: Warnings and fixes (K13)

    private func warning(_ kind: String, target: String? = nil, code: String = "c") -> ClientWarning {
        let action: [String: Any] = target.map { ["kind": kind, "target": $0] } ?? ["kind": kind]
        let object: [String: Any] = ["code": code, "severity": "warn", "message": "m", "action": action]
        return try! JSONDecoder().decode(ClientWarning.self, from: JSONSerialization.data(withJSONObject: object))
    }

    func testEachWarningActionMapsToItsDestination() {
        XCTAssertEqual(ClientWarningNavigation.route(for: warning("change_setting", target: "require_mcp_auth")),
                       .settings(.requireMCPAuth))
        XCTAssertEqual(ClientWarningNavigation.route(for: warning("upgrade_admin_key_holders")), .upgradeAdminKeys)
        XCTAssertEqual(ClientWarningNavigation.route(for: warning("reconnect_client", target: "cursor")),
                       .connectSheet(clientId: "cursor"))
        XCTAssertEqual(ClientWarningNavigation.route(for: warning("move_client", target: "cursor")),
                       .clientDetail(id: "cursor"))
        XCTAssertEqual(ClientWarningNavigation.route(for: warning("edit_token", target: "client-cursor")),
                       .clients(tab: .tokens, filter: .forToken("client-cursor")))
        XCTAssertNil(ClientWarningNavigation.route(for: warning("brand_new_action")))
    }

    func testGuardFixesOnlyNavigate() {
        XCTAssertEqual(ClientWarningNavigation.route(for: GuardFix(kind: "require_mcp_auth", target: nil)),
                       .settings(.requireMCPAuth))
        XCTAssertEqual(ClientWarningNavigation.route(for: GuardFix(kind: "set_anonymous_profile", target: "work-ro")),
                       .settings(.anonymousProfile(preselect: "work-ro")),
                       "preselected, never saved")
        XCTAssertEqual(ClientWarningNavigation.buttonTitle(for: GuardFix(kind: "require_mcp_auth", target: nil)),
                       "Require authentication…")
        XCTAssertEqual(ClientWarningNavigation.buttonTitle(for: GuardFix(kind: "set_anonymous_profile", target: "work-ro")),
                       "Set anonymous callers to work-ro…")
        XCTAssertNil(ClientWarningNavigation.route(for: GuardFix(kind: "future_fix", target: nil)))
    }

    // MARK: Bulk move

    func testBulkMoveShowsWhatWasLeftBehind() async {
        let source = StubSource()
        source.bulkResult = .success(try! JSONDecoder().decode(BulkAssignResponse.self, from: Data("""
        {"moved":["cursor","zed"],"skipped":[{"client_id":"codex","code":"no_client_credential","error":"x"},
        {"client_id":"vscode","code":"binding_bypassable_without_auth","error":"y"}]}
        """.utf8)))
        let model = BulkMoveModel(source: source)
        model.from = "a"
        model.to = "b"
        await model.run()
        XCTAssertEqual(source.bulks.first?.0, "a")
        XCTAssertEqual(source.bulks.first?.1, "b")
        XCTAssertNil(source.bulks.first?.2, "the mode is unchanged when omitted")
        XCTAssertEqual(model.summary, "2 clients moved, 2 left where they are")
        XCTAssertEqual(model.result?.skipped.map(\.clientId), ["codex", "vscode"])
        XCTAssertTrue(model.result?.skipped[0].reason.contains("No client credential") ?? false)
        XCTAssertTrue(model.result?.skipped[1].reason.contains("escape") ?? false)
    }

    func testBulkMoveNeedsDifferentProfiles() {
        let model = BulkMoveModel(source: StubSource())
        XCTAssertFalse(model.canRun, "from == to")
        model.to = "b"
        XCTAssertTrue(model.canRun)
    }

    // MARK: Admin-key upgrade (K10)

    private func preview(rows: Int, guardCode: String? = nil, next: String? = nil, token: String? = "combined") -> UpgradePreview {
        var object: [String: Any] = ["preview": (0..<rows).map { i -> [String: Any] in
            ["client_id": "c\(i)", "display_name": "C\(i)", "credential": "mcp_cli_••••",
             "profile": "", "mode": "switchable", "precondition_token": "t\(i)", "diff": ["before": "x"]]
        }]
        if let token { object["precondition_token"] = token }
        if let guardCode {
            object["guard"] = ["code": guardCode, "bindings": [], "fixes": [["kind": "require_mcp_auth"]]]
        }
        if let next { object["next_step"] = next }
        return try! JSONDecoder().decode(UpgradePreview.self, from: JSONSerialization.data(withJSONObject: object))
    }

    func testAGuardInThePreviewDisablesApply() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 2, guardCode: "binding_bypassable_without_auth"))]
        let model = AdminKeyUpgradeModel(source: source, profile: "work-ro")
        await model.loadPreview()
        XCTAssertNotNil(model.guardRefusal)
        XCTAssertFalse(model.canApply)
        await model.apply()
        XCTAssertTrue(source.applies.isEmpty, "Apply is refused client-side too")
    }

    func testApplySendsThePreconditionToken() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 2, token: "combined-token"))]
        source.applyResult = .success(try! JSONDecoder().decode(UpgradeResult.self, from: Data(
            #"{"upgraded":["c0","c1"],"failed":[],"next_step":"rotate_admin_api_key"}"#.utf8)))
        let model = AdminKeyUpgradeModel(source: source)
        await model.loadPreview()
        XCTAssertTrue(model.canApply)
        await model.apply()
        XCTAssertEqual(source.applies, ["combined-token"])
        XCTAssertTrue(model.showsRotatePanel, "the rotate-the-admin-key panel follows")
        if case .applied(let result) = model.phase { XCTAssertEqual(result.upgraded, ["c0", "c1"]) } else { XCTFail("\(model.phase)") }
    }

    func testAnEmptyPreviewGoesStraightToTheRotatePanel() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 0, next: "rotate_admin_api_key"))]
        let model = AdminKeyUpgradeModel(source: source)
        await model.loadPreview()
        XCTAssertEqual(model.phase, .nothingToUpgrade)
        XCTAssertTrue(model.showsRotatePanel)
        XCTAssertFalse(model.canApply)
        XCTAssertEqual(AdminKeyUpgradeModel.rotateSteps.count, 2)
    }

    func testAStalePreconditionReloadsThePreview() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 1, token: "old")), .success(preview(rows: 1, token: "new"))]
        source.applyResult = .failure(service(409, #"{"error":"changed","code":"precondition_failed"}"#))
        let model = AdminKeyUpgradeModel(source: source)
        await model.loadPreview()
        await model.apply()
        if case .preview(let fresh) = model.phase { XCTAssertEqual(fresh.preconditionToken, "new") } else { XCTFail("\(model.phase)") }
    }

    // MARK: Upgrade preview invalidation (108-retro-mac R6)

    func testChangingTheProfileAfterAPreviewDisablesApply() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 2))]
        let model = AdminKeyUpgradeModel(source: source)
        await model.loadPreview()
        XCTAssertTrue(model.canApply)
        model.profile = "ro"
        XCTAssertEqual(model.phase, .idle)
        XCTAssertFalse(model.canApply)
        await model.apply()
        XCTAssertTrue(source.applies.isEmpty, "a stale preview is never applied")
    }

    func testApplySendsTheProfileThatWasPreviewed() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 1))]
        source.applyResult = .success(try! JSONDecoder().decode(UpgradeResult.self, from: Data(
            #"{"upgraded":["c0"],"failed":[]}"#.utf8)))
        let model = AdminKeyUpgradeModel(source: source)
        model.profile = "ro"
        await model.loadPreview()
        await model.apply()
        XCTAssertEqual(source.appliedProfiles, ["ro"])
        XCTAssertEqual(source.previews.first?.0, "ro")
    }

    func testAPreviewThatReturnsAfterTheProfileChangedIsDropped() async {
        let source = StubSource()
        source.previewResults = [.success(preview(rows: 2))]
        let model = AdminKeyUpgradeModel(source: source)
        source.onPreview = { model.profile = "ro" }
        await model.loadPreview()
        XCTAssertEqual(model.phase, .idle)
        XCTAssertFalse(model.canApply)
    }

    // MARK: Other client and the one-time credential (K11)

    private func customResponse(credential: String) -> CustomClientResponse {
        let object: [String: Any] = [
            "client": ["id": "dev-laptop", "display_name": "Dev Laptop", "kind": "other", "state": "other",
                       "installed": false, "connected": false, "active_sessions": 0, "calls_24h": 0],
            "credential": credential,
            "snippet": ["generic_http": "Authorization: Bearer \(credential)", "header_name": "Authorization"],
        ]
        return try! JSONDecoder().decode(CustomClientResponse.self, from: JSONSerialization.data(withJSONObject: object))
    }

    func testTheCustomClientCredentialIsShownOnceAndZeroedOnDismiss() async {
        let source = StubSource()
        source.customResult = .success(customResponse(credential: "mcp_cli_SECRETSECRET"))
        let model = CustomClientModel(source: source)
        model.id = "dev-laptop"
        await model.create()
        XCTAssertEqual(model.credential, "mcp_cli_SECRETSECRET")
        XCTAssertTrue(model.snippet?.contains("mcp_cli_SECRETSECRET") ?? false)
        XCTAssertFalse(model.canCreate, "a second create is not offered over a shown credential")

        model.dismiss()
        XCTAssertNil(model.credential)
        XCTAssertNil(model.snippet)
    }

    /// The secret never reaches `AppState` (and so never a UserDefaults, a
    /// keychain item or a log line: the sheet model is the only holder).
    func testTheCredentialNeverLandsInAppState() async throws {
        let source = StubSource()
        source.customResult = .success(customResponse(credential: "mcp_cli_SECRETSECRET"))
        let appState = AppState()
        let model = CustomClientModel(source: source)
        model.id = "dev-laptop"
        await model.create()
        XCTAssertNotNil(model.credential)

        let mirror = String(reflecting: appState)
        XCTAssertFalse(mirror.contains("mcp_cli_"))
        XCTAssertFalse(appState.clients.contains { $0.tokenName?.hasPrefix("mcp_cli_") == true })
        XCTAssertNil(UserDefaults.standard.dictionaryRepresentation().values.first { "\($0)".contains("mcp_cli_SECRET") })
    }

    func testAWeakReferenceSeesTheModelReleasedAfterDismiss() async {
        let source = StubSource()
        source.customResult = .success(customResponse(credential: "mcp_cli_SECRETSECRET"))
        weak var weakModel: CustomClientModel?
        do {
            let model = CustomClientModel(source: source)
            weakModel = model
            model.id = "dev-laptop"
            await model.create()
            model.dismiss()
            XCTAssertNil(model.credential)
        }
        XCTAssertNil(weakModel, "nothing retains the sheet model after it goes away")
    }

    func testACustomClientIdErrorIsShownInlineOnTheIdField() async {
        let source = StubSource()
        source.customResult = .failure(service(400, #"{"error":"id must match ^[a-z0-9]","field":"id"}"#))
        let model = CustomClientModel(source: source)
        model.id = "Bad Id"
        await model.create()
        XCTAssertEqual(model.idError, "id must match ^[a-z0-9]")
        XCTAssertNil(model.credential)
        XCTAssertNil(model.errorMessage)
    }

    func testTheDefaultModeIsLockedWithAProfileAndSwitchableWithout() {
        let model = CustomClientModel(source: StubSource())
        XCTAssertEqual(model.effectiveMode, .switchable)
        model.profile = "work-ro"
        XCTAssertEqual(model.effectiveMode, .locked)
        model.locked = false
        XCTAssertEqual(model.effectiveMode, .switchable)
        XCTAssertEqual(model.expiresIn, "365d")
        XCTAssertEqual(CustomClientModel.expiryChoices.map(\.value), ["30d", "90d", "180d", "365d"])
    }

    // MARK: Rotation (FR-021a)

    private func rotateResponse(state: String, credential: String? = nil) -> RotateResponse {
        var object: [String: Any] = [
            "client": ["id": "cursor", "display_name": "Cursor", "kind": "supported", "state": "connected_seen",
                       "installed": true, "connected": true, "active_sessions": 0, "calls_24h": 0],
            "rotation": ["state": state],
        ]
        if let credential { object["credential"] = credential }
        return try! JSONDecoder().decode(RotateResponse.self, from: JSONSerialization.data(withJSONObject: object))
    }

    func testASupportedClientRotatesAfterAPreviewAndBoundToItsToken() async {
        let source = StubSource()
        source.rotateResult = .success(rotateResponse(state: "finalized"))
        let model = RotationModel(source: source, client: Self.record(kind: "supported"))
        await model.loadPreview()
        await model.rotate()
        XCTAssertEqual(source.order, ["preview", "rotate"], "preview first, then the rotate")
        XCTAssertEqual(source.rotations, ["row-token"], "bound to the preview's precondition token")
        XCTAssertEqual(model.outcome, .finalized)
    }

    func testARolledBackRotationSaysSo() async {
        let source = StubSource()
        source.rotateResult = .success(rotateResponse(state: "rolled_back"))
        let model = RotationModel(source: source, client: Self.record(kind: "supported"))
        await model.loadPreview()
        await model.rotate()
        XCTAssertEqual(model.outcome, .rolledBack)
    }

    func testACustomClientRotationIsPendingUntilFinalized() async {
        let source = StubSource()
        source.rotateResult = .success(rotateResponse(state: "pending", credential: "mcp_cli_NEW"))
        source.finalizeResult = .success(try! JSONDecoder().decode(FinalizeRotationResponse.self, from: Data("""
        {"client":{"id":"dev","display_name":"Dev","kind":"other","state":"other","installed":false,"connected":false,"active_sessions":0,"calls_24h":0},
         "rotation":{"state":"finalized"}}
        """.utf8)))
        let model = RotationModel(source: source, client: Self.record(id: "dev", kind: "other"))
        await model.loadPreview()
        XCTAssertFalse(source.order.contains("preview"), "a custom client has no config file to preview")
        await model.rotate()
        XCTAssertEqual(model.outcome, .pending)
        XCTAssertEqual(model.credential, "mcp_cli_NEW")
        await model.finalize()
        XCTAssertEqual(model.outcome, .finalized)
        model.dismiss()
        XCTAssertNil(model.credential, "the new secret is zeroed too")
    }
}
