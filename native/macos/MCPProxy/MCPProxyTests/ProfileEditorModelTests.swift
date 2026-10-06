import XCTest
@testable import MCPProxy

/// Spec 108-k (K4, K7, K8): the profile editor's payload, tool-row edits, stale
/// marker, Try-it draft, error routing and the rename / delete impact lists.
@MainActor
final class ProfileEditorModelTests: XCTestCase {

    // MARK: Stub

    final class StubSource: ProfileEditorSource, @unchecked Sendable {
        var profileResult: Result<ProfileView, Error>?
        var createResult: Result<ProfileWriteResponse, Error>?
        var updateResult: Result<ProfileWriteResponse, Error>?
        var renameResult: Result<ProfileRenameResponse, Error>?
        var deleteResult: Result<ProfileDeleteResponse, Error>?
        var effectiveResult: Result<EffectiveToolsResponse, Error>?
        var tryResult: Result<TryResponse, Error>?

        /// Runs while an update is in flight (the user typing during a save).
        var onUpdate: (@MainActor () -> Void)?
        /// Runs while a profile GET is in flight (the user typing during a reload).
        var onProfile: (@MainActor () -> Void)?

        private(set) var created: [ProfileConfigPayload] = []
        private(set) var updated: [(String, ProfileConfigPayload)] = []
        private(set) var renamed: [(String, String)] = []
        private(set) var deleted: [(String, String?, Bool)] = []
        private(set) var tried: [(ProfileConfigPayload, String)] = []

        func profile(_ name: String) async throws -> ProfileView {
            if let hook = onProfile { await MainActor.run { hook() } }
            return try (profileResult ?? .success(ProfileView(name: name))).get()
        }
        func createProfile(_ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse {
            created.append(payload)
            return try (createResult ?? .success(StubSource.write(ProfileView(name: payload.name)))).get()
        }
        func updateProfile(_ name: String, _ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse {
            updated.append((name, payload))
            if let hook = onUpdate { await MainActor.run { hook() } }
            return try (updateResult ?? .success(StubSource.write(ProfileView(name: payload.name)))).get()
        }
        func renameProfile(_ name: String, newName: String) async throws -> ProfileRenameResponse {
            renamed.append((name, newName))
            return try (renameResult ?? .failure(APIClientError.noData)).get()
        }
        func deleteProfile(_ name: String, reassignTo: String?, force: Bool) async throws -> ProfileDeleteResponse {
            deleted.append((name, reassignTo, force))
            return try (deleteResult ?? .failure(APIClientError.noData)).get()
        }
        func effectiveTools(_ name: String, client: String?, server: String?, reason: String?) async throws -> EffectiveToolsResponse {
            try (effectiveResult ?? .success(try! JSONDecoder().decode(EffectiveToolsResponse.self, from: Data(#"{"profile":"p","tools":[]}"#.utf8)))).get()
        }
        func tryProfile(draft: ProfileConfigPayload, query: String, limit: Int?) async throws -> TryResponse {
            tried.append((draft, query))
            return try (tryResult ?? .success(try! JSONDecoder().decode(TryResponse.self, from: Data(#"{"results":[],"hidden_by_profile":0,"hidden":[]}"#.utf8)))).get()
        }

        static func write(_ profile: ProfileView, warnings: [String] = []) -> ProfileWriteResponse {
            let object: [String: Any] = ["profile": ["name": profile.name, "servers": profile.servers], "warnings": warnings]
            return try! JSONDecoder().decode(ProfileWriteResponse.self, from: JSONSerialization.data(withJSONObject: object))
        }
    }

    private func service(_ status: Int, _ json: String) -> APIClientError {
        .service(status: status, body: try! JSONDecoder().decode(ServiceErrorBody.self, from: Data(json.utf8)))
    }

    private func row(_ server: String, _ tool: String, tier: ToolTier, stale: Bool = false) -> EffectiveTool {
        EffectiveTool(server: server, tool: tool, intrinsicTier: tier, profileTier: tier,
                      access: ToolAccess(visible: true, callable: true), classificationStale: stale)
    }

    private func editor(_ profile: ProfileView? = ProfileView(name: "work", servers: ["github"]),
                        source: StubSource = StubSource()) -> (ProfileEditorModel, StubSource) {
        (ProfileEditorModel(source: source, profile: profile), source)
    }

    // MARK: K4: the exact JSON of each tri-state

    func testCodeExecutionAndManagementToolsAreOmittedWhenNil() throws {
        var payload = ProfileConfigPayload(name: "p")
        payload.servers = ["github"]
        let json = try payload.jsonObject()
        XCTAssertNil(json["code_execution"])
        XCTAssertNil(json["management_tools"])
        XCTAssertNil(json["switchable_to"])
        XCTAssertNil(json["tools"])
        XCTAssertNil(json["max_tier"])
        XCTAssertNil(json["unannotated"])

        payload.codeExecution = false
        payload.managementTools = true
        let set = try payload.jsonObject()
        XCTAssertEqual(set["code_execution"] as? Bool, false, "an explicit off is sent, not dropped")
        XCTAssertEqual(set["management_tools"] as? Bool, true)
    }

    func testSwitchableToIsATriState() throws {
        var payload = ProfileConfigPayload(name: "p")
        payload.switchableTo = .unset
        XCTAssertNil(try payload.jsonObject()["switchable_to"], "unset omits the key")

        payload.switchableTo = .none
        let none = try payload.jsonObject()["switchable_to"] as? [String]
        XCTAssertEqual(none, [], "none encodes []")

        payload.switchableTo = .list(["a", "b"])
        XCTAssertEqual(try payload.jsonObject()["switchable_to"] as? [String], ["a", "b"])

        XCTAssertEqual(SwitchableTo(stored: nil), .unset)
        XCTAssertEqual(SwitchableTo(stored: []), .none)
        XCTAssertEqual(SwitchableTo(stored: ["x"]), .list(["x"]))
    }

    func testToolsAreOmittedWhenAllThreePartsAreEmptyAndPartsAreOmittedIndividually() throws {
        var payload = ProfileConfigPayload(name: "p")
        XCTAssertNil(try payload.jsonObject()["tools"])
        payload.tools.deny = ["github:*secret*"]
        let tools = try XCTUnwrap(payload.jsonObject()["tools"] as? [String: Any])
        XCTAssertEqual(tools["deny"] as? [String], ["github:*secret*"])
        XCTAssertNil(tools["allow"])
        XCTAssertNil(tools["classify"])
    }

    func testMaxTierAndUnannotatedAreOmittedWhenNilOrEmpty() throws {
        var payload = ProfileConfigPayload(name: "p")
        payload.maxTier = ""
        payload.unannotated = ""
        let json = try payload.jsonObject()
        XCTAssertNil(json["max_tier"])
        XCTAssertNil(json["unannotated"])
        payload.maxTier = "write"
        payload.unannotated = "as_read"
        let set = try payload.jsonObject()
        XCTAssertEqual(set["max_tier"] as? String, "write")
        XCTAssertEqual(set["unannotated"] as? String, "as_read")
    }

    // MARK: Allow / Deny / Classify

    func testAllowAndDenyEditTheRuleListsExclusively() {
        let (model, _) = editor()
        model.toggle(.allow, for: "github:create_issue")
        XCTAssertEqual(model.draft.tools.allow, ["github:create_issue"])
        XCTAssertEqual(model.ruleState(for: "github:create_issue"), .allow)

        model.toggle(.deny, for: "github:create_issue")
        XCTAssertEqual(model.draft.tools.allow, [])
        XCTAssertEqual(model.draft.tools.deny, ["github:create_issue"])

        model.toggle(.deny, for: "github:create_issue")
        XCTAssertEqual(model.ruleState(for: "github:create_issue"), .none, "toggling the active one clears it")
        XCTAssertTrue(model.isDirty == false || model.draft.tools.isEmpty)
    }

    func testClassifyIsOnlyForUnannotatedTools() {
        let (model, _) = editor()
        let annotated = row("github", "list_issues", tier: .read)
        let unannotated = row("github", "search_code", tier: .unannotated)
        XCTAssertFalse(model.canClassify(annotated))
        XCTAssertTrue(model.canClassify(unannotated))

        XCTAssertFalse(model.classify(annotated, as: .write), "an annotated tool keeps its own tier")
        XCTAssertNil(model.draft.tools.classify["github:list_issues"])

        XCTAssertTrue(model.classify(unannotated, as: .read))
        XCTAssertEqual(model.draft.tools.classify["github:search_code"], "read")
        XCTAssertEqual(model.classification(for: "github:search_code"), .read)

        model.removeClassification(unannotated)
        XCTAssertNil(model.draft.tools.classify["github:search_code"])
    }

    /// A stale classification (the tool is now annotated) can still be REMOVED,
    /// and says why it is ignored, in exactly these words.
    func testTheStaleMarkerTextAndRemoval() {
        let (model, _) = editor()
        XCTAssertEqual(ProfileEditorModel.staleMarkerText, "classification ignored — tool is now annotated")
        let stale = row("github", "list_issues", tier: .read, stale: true)
        XCTAssertEqual(model.staleMarker(for: stale), "classification ignored — tool is now annotated")
        XCTAssertNil(model.staleMarker(for: row("github", "x", tier: .read)))

        model.draft.tools.classify["github:list_issues"] = "write"
        model.removeClassification(stale)
        XCTAssertNil(model.draft.tools.classify["github:list_issues"])
    }

    /// #1451: the server's stale_classification_reasons words each orphan note
    /// like the CLI; an older daemon (no map) stays neutral; a bad map decodes.
    func testStaleOrphanNotesComeFromTheServerReasons() async throws {
        let (model, source) = editor()
        source.effectiveResult = .success(try JSONDecoder().decode(EffectiveToolsResponse.self, from: Data(#"""
        {"profile":"p","tools":[{"server":"github","tool":"list_issues","intrinsic_tier":"read","profile_tier":"read","access":{"visible":true,"callable":true},"classification_stale":true}],
         "stale_classifications":["github:list_issues","github:gone_tool","github:other"],
         "stale_classification_reasons":{"github:list_issues":"annotated","github:gone_tool":"missing"}}
        """#.utf8)))
        await model.loadEffectiveTools()
        let orphans = model.staleOrphans
        XCTAssertEqual(orphans.map(\.id), ["github:gone_tool", "github:other"], "a listed row is not an orphan")
        XCTAssertEqual(orphans.map(\.note), ["classification ignored — tool not found", "classification ignored"])
        model.draft.tools.classify["github:gone_tool"] = "write"
        model.removeClassification(id: "github:gone_tool")
        XCTAssertNil(model.draft.tools.classify["github:gone_tool"])
        XCTAssertEqual(ProfileEditorModel.staleNote(reason: "missing"), "classification ignored — tool not found")
        XCTAssertEqual(ProfileEditorModel.staleNote(reason: "annotated"), "classification ignored — tool is now annotated")
        XCTAssertEqual(ProfileEditorModel.staleNote(reason: nil), "classification ignored")

        let old = try JSONDecoder().decode(EffectiveToolsResponse.self, from: Data(#"{"profile":"p","tools":[],"stale_classifications":["a:b"]}"#.utf8))
        XCTAssertNil(old.staleClassificationReasons)
        let bad = try JSONDecoder().decode(EffectiveToolsResponse.self, from: Data(#"{"profile":"p","tools":[],"stale_classification_reasons":["x"]}"#.utf8))
        XCTAssertNil(bad.staleClassificationReasons)
    }

    // MARK: Try it

    func testTrySendsTheUnsavedDraft() async {
        let (model, source) = editor()
        model.draft.maxTier = "read"
        model.draft.servers = ["github", "notion"]
        model.tryQuery = "issue"
        await model.tryIt()
        XCTAssertEqual(source.tried.count, 1)
        XCTAssertEqual(source.tried[0].1, "issue")
        XCTAssertEqual(source.tried[0].0.maxTier, "read", "the unsaved edit, not the stored profile")
        XCTAssertEqual(source.tried[0].0.servers, ["github", "notion"])
        XCTAssertTrue(source.updated.isEmpty, "Try never saves")
    }

    // MARK: Save and errors

    func testSaveOfAnExistingProfileUpdatesItUnderItsPathName() async {
        let (model, source) = editor()
        model.draft.title = "Work"
        await model.save()
        XCTAssertEqual(source.updated.count, 1)
        XCTAssertEqual(source.updated[0].0, "work")
        XCTAssertEqual(source.updated[0].1.title, "Work")
        XCTAssertTrue(source.created.isEmpty)
        XCTAssertNil(model.errorMessage)
    }

    /// Edits typed while a save is in flight are not thrown away by its response.
    func testEditsMadeDuringASaveSurvive() async {
        let source = StubSource()
        let (model, _) = editor(source: source)
        model.draft.title = "first"
        source.onUpdate = { model.draft.title = "typed during save" }
        await model.save()
        XCTAssertEqual(model.draft.title, "typed during save")
        XCTAssertTrue(model.isDirty)
    }

    func testSaveOfANewProfileCreatesIt() async {
        let (model, source) = editor(nil)
        XCTAssertTrue(model.isNew)
        model.draft.name = "fresh"
        model.draft.servers = ["github"]
        await model.save()
        XCTAssertEqual(source.created.map(\.name), ["fresh"])
        XCTAssertTrue(model.hasBeenCreated)
    }

    func testA400WithAFieldLandsOnThatField() async {
        let source = StubSource()
        source.updateResult = .failure(service(400, #"{"success":false,"error":"invalid max_tier \"huge\"","field":"max_tier"}"#))
        let (model, _) = editor(source: source)
        await model.save()
        XCTAssertEqual(model.fieldError?.field, "max_tier")
        XCTAssertEqual(model.fieldError?.message, "invalid max_tier \"huge\"")
        XCTAssertNil(model.guardRefusal)
        XCTAssertNil(model.errorMessage)
    }

    func testA409GuardRefusalLandsOnTheGuardView() async {
        let source = StubSource()
        source.updateResult = .failure(service(409, """
        {"success":false,"error":"a client bound to profile work could escape it","code":"binding_bypassable_without_auth",
         "bindings":[{"client_id":"cursor","token_name":"client-cursor","profile":"work","mode":"locked"}],
         "fixes":[{"kind":"require_mcp_auth"},{"kind":"set_anonymous_profile","target":"work"}]}
        """))
        let (model, _) = editor(source: source)
        await model.save()
        let refusal = try? XCTUnwrap(model.guardRefusal)
        XCTAssertEqual(refusal?.bindings?.first?.clientId, "cursor")
        XCTAssertEqual(refusal?.fixes?.count, 2)
        XCTAssertNil(model.fieldError)
    }

    func testAnySuccessfulSaveClearsEarlierErrors() async {
        let source = StubSource()
        source.updateResult = .failure(service(400, #"{"error":"bad","field":"title"}"#))
        let (model, _) = editor(source: source)
        await model.save()
        XCTAssertNotNil(model.fieldError)
        source.updateResult = .success(StubSource.write(ProfileView(name: "work")))
        await model.save()
        XCTAssertNil(model.fieldError)
    }

    // MARK: Changed elsewhere (K15)

    func testACleanEditorFollowsAnExternalChange() {
        let (model, _) = editor()
        model.profileChangedElsewhere(ProfileView(name: "work", title: "Renamed elsewhere", servers: ["github"]))
        XCTAssertEqual(model.draft.title, "Renamed elsewhere")
        XCTAssertFalse(model.changedElsewhere)
    }

    func testADirtyEditorKeepsItsDraftAndRaisesTheMarker() {
        let (model, _) = editor()
        model.draft.title = "My edit"
        model.profileChangedElsewhere(ProfileView(name: "work", title: "Theirs", servers: ["github"]))
        XCTAssertEqual(model.draft.title, "My edit", "never overwritten")
        XCTAssertTrue(model.changedElsewhere)
    }

    func testLiveStatsChangingIsNotAChangeElsewhere() {
        let (model, _) = editor()
        var fresh = ProfileView(name: "work", servers: ["github"])
        fresh.calls24h = 99
        fresh.usedBy = UsedBy(clients: [UsedByClient(id: "cursor", mode: .locked)])
        model.draft.title = "dirty"
        model.profileChangedElsewhere(fresh)
        XCTAssertFalse(model.changedElsewhere)
    }

    // MARK: Reload (108-retro-mac R3)

    func testReloadDiscardsTheDraftAndAdoptsTheServerVersion() async {
        let (model, source) = editor()
        model.draft.title = "mine"
        XCTAssertTrue(model.isDirty)
        let fresh = ProfileView(name: "work", servers: ["github", "notion"])
        source.profileResult = .success(fresh)
        model.profileChangedElsewhere(fresh)
        XCTAssertTrue(model.changedElsewhere)

        await model.reload()
        XCTAssertFalse(model.changedElsewhere)
        XCTAssertFalse(model.isDirty)
        XCTAssertEqual(model.draft.servers, ["github", "notion"])
        XCTAssertEqual(model.draft.title, "")
        XCTAssertEqual(model.original?.servers, ["github", "notion"])
    }

    func testEditsTypedDuringAReloadAreKeptAndRaiseTheMarker() async {
        let (model, source) = editor()
        model.draft.title = "mine"
        source.profileResult = .success(ProfileView(name: "work", servers: ["github", "notion"]))
        source.onProfile = { model.draft.title = "typed" }

        await model.reload()
        XCTAssertEqual(model.draft.title, "typed", "a draft is never overwritten without a click")
        XCTAssertTrue(model.changedElsewhere)
    }

    // MARK: Sections (108-retro-mac R2)

    func testANewProfileOffersTryItButNoToolTable() {
        let new = ProfileEditorModel(source: StubSource(), profile: nil)
        XCTAssertEqual(new.sections, [.form, .tryIt])
        let (existing, _) = editor()
        XCTAssertEqual(existing.sections, [.form, .toolTable, .tryIt])
    }

    func testTryOnANewProfileSendsTheUnsavedDraft() async {
        let source = StubSource()
        let model = ProfileEditorModel(source: source, profile: nil)
        model.draft.name = ""
        model.draft.servers = ["github"]
        model.tryQuery = "issue"
        await model.tryIt()
        XCTAssertEqual(source.tried.count, 1)
        XCTAssertEqual(source.tried[0].0.servers, ["github"])
        XCTAssertEqual(source.tried[0].0.name, "")
        XCTAssertNotNil(model.tryResult)
    }

    func testTheEditorRendersTheModelSections() throws {
        let url = URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent()
            .appendingPathComponent("MCPProxy/Views/ProfileEditorView.swift")
        let text = try String(contentsOf: url, encoding: .utf8)
        XCTAssertTrue(text.contains("model.sections"))
        XCTAssertFalse(text.contains("if !model.isNew { Divider(); toolsSection }"))
    }

    // MARK: Rename and delete impact (K8)

    private var listing: [ProfileView] {
        var work = ProfileView(name: "work", servers: ["github"])
        work.usedBy = UsedBy(clients: [UsedByClient(id: "cursor", mode: .locked)], tokens: ["ci"])
        let other = ProfileView(name: "other", servers: [], switchableTo: ["work"])
        let third = ProfileView(name: "third", servers: [])
        return [work, other, third]
    }

    func testTheImpactListsClientsTokensSwitchersAndTheAnonymousProfile() {
        var work = listing[0]
        work.usedBy?.anonymousProfile = true
        let impact = ProfileImpact(profile: work, all: [work, listing[1], listing[2]])
        XCTAssertEqual(impact.clients, [UsedByClient(id: "cursor", mode: .locked)])
        XCTAssertEqual(impact.tokens, ["ci"])
        XCTAssertEqual(impact.switchableFrom, ["other"])
        XCTAssertTrue(impact.anonymous)
        XCTAssertEqual(impact.lines, [
            "Client cursor (locked)", "Token ci",
            "Profile other lists it under “Agent may switch to”", "Anonymous callers",
        ])
    }

    func testDeleteNeedsATargetWhenSomethingPointsAtTheProfile() {
        let impact = ProfileImpact(profile: listing[0], all: listing)
        XCTAssertTrue(impact.requiresTarget)
        XCTAssertTrue(impact.canForce)
        let unused = ProfileImpact(profile: listing[2], all: listing)
        XCTAssertFalse(unused.requiresTarget)
        XCTAssertTrue(unused.isEmpty)
        // Named only by another profile's "Agent may switch to": still listed, so
        // the target is still required (the Web UI behaves the same).
        let switchOnly = ProfileImpact(profile: ProfileView(name: "work"), all: listing)
        XCTAssertEqual(switchOnly.switchableFrom, ["other"])
        XCTAssertTrue(switchOnly.requiresTarget)
    }

    /// `force` is refused by the core for the anonymous profile, so the toggle is hidden.
    func testForceIsHiddenForTheAnonymousProfile() {
        var work = listing[0]
        work.usedBy?.anonymousProfile = true
        XCTAssertFalse(ProfileImpact(profile: work, all: listing).canForce)
    }

    func testTheReassignTargetsExcludeTheProfileItself() {
        let targets = ProfileEditorModel.reassignTargets(excluding: "work", in: listing)
        XCTAssertEqual(targets.map(\.name), ["other", "third"])
        XCTAssertFalse(targets.contains { $0.name.isEmpty }, "there is no All servers target")
    }

    func testARenameMovesTheModelToTheNewName() async {
        let source = StubSource()
        let renamed = try! JSONDecoder().decode(ProfileRenameResponse.self, from: Data(
            #"{"profile":{"name":"work2","servers":["github"]},"moved":{"clients":["cursor"],"tokens":["ci"]}}"#.utf8))
        source.renameResult = .success(renamed)
        let (model, _) = editor(source: source)
        let ok = await model.rename(to: "work2")
        XCTAssertTrue(ok)
        XCTAssertEqual(source.renamed.first?.0, "work")
        XCTAssertEqual(source.renamed.first?.1, "work2")
        XCTAssertEqual(model.original?.name, "work2")
    }

    func testADeleteSendsTheTargetAndForce() async {
        let source = StubSource()
        source.deleteResult = .success(try! JSONDecoder().decode(ProfileDeleteResponse.self, from: Data(
            #"{"deleted":"work","moved":{"clients":["cursor"],"tokens":[]}}"#.utf8)))
        let (model, _) = editor(source: source)
        let ok = await model.delete(reassignTo: "other", force: false)
        XCTAssertTrue(ok)
        XCTAssertEqual(source.deleted.first?.0, "work")
        XCTAssertEqual(source.deleted.first?.1, "other")
        XCTAssertEqual(source.deleted.first?.2, false)
    }

    func testAProfileInUseConflictFoldsItsUsedByIntoTheImpact() async {
        let source = StubSource()
        source.deleteResult = .failure(service(409, """
        {"success":false,"error":"profile in use","code":"profile_in_use",
         "used_by":{"clients":[{"id":"zed","mode":"switchable"}],"tokens":["late"],"anonymous_profile":false}}
        """))
        let (model, _) = editor(source: source)
        let ok = await model.delete(reassignTo: nil, force: false)
        XCTAssertFalse(ok)
        var impact = ProfileImpact()
        impact.merge(try! XCTUnwrap(model.conflictUsedBy))
        XCTAssertEqual(impact.clients.map(\.id), ["zed"])
        XCTAssertEqual(impact.tokens, ["late"])
    }
}
