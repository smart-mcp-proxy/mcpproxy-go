import XCTest
@testable import MCPProxy

/// Spec 108-k (K20, T116a): the Settings → Security "Anonymous callers" picker.
@MainActor
final class AnonymousProfileSectionTests: XCTestCase {

    final class StubSource: AnonymousProfileSource, @unchecked Sendable {
        var error: Error?
        private(set) var values: [String] = []

        func patchAnonymousProfile(_ value: String) async throws {
            values.append(value)
            if let error { throw error }
        }
    }

    func testSavingAProfileSendsItsNameAndMarksItSaved() async {
        let source = StubSource()
        let model = AnonymousProfileModel(source: source, current: "")
        model.selection = "work-ro"
        XCTAssertTrue(model.isDirty)
        await model.save()
        XCTAssertEqual(source.values, ["work-ro"])
        XCTAssertFalse(model.isDirty)
        XCTAssertEqual(model.savedNote, "Anonymous callers are now confined to work-ro.")
    }

    func testUnconfinedIsAnEmptyString() async {
        let source = StubSource()
        let model = AnonymousProfileModel(source: source, current: "work-ro")
        model.selection = ""
        await model.save()
        XCTAssertEqual(source.values, [""], "unconfined clears the field: `anonymous_profile: \"\"`")
        XCTAssertEqual(model.savedNote, "Anonymous callers now reach All servers.")
    }

    func testNothingIsSentWhenNothingChanged() async {
        let source = StubSource()
        let model = AnonymousProfileModel(source: source, current: "work-ro")
        await model.save()
        XCTAssertTrue(source.values.isEmpty)
    }

    func testTheExplanationDependsOnRequireMCPAuth() {
        XCTAssertEqual(
            AnonymousProfileModel.explanation(requireMCPAuth: true),
            "Anonymous callers are refused because authentication is required. This setting applies only if you turn authentication off.")
        XCTAssertTrue(AnonymousProfileModel.explanation(requireMCPAuth: false)
            .hasPrefix("Callers that send no credential get this profile."))
        XCTAssertTrue(AnonymousProfileModel.explanation(requireMCPAuth: false).contains("binding guard"))
    }

    /// A fix button PRESELECTS the value; it never saves it.
    func testPreselectingDoesNotSave() {
        let source = StubSource()
        let model = AnonymousProfileModel(source: source, current: "")
        model.preselect("work-ro")
        XCTAssertEqual(model.selection, "work-ro")
        XCTAssertTrue(model.isDirty, "the operator still has to press Save")
        XCTAssertTrue(source.values.isEmpty)
        XCTAssertEqual(model.saved, "")
    }

    func testAGuardRefusalIsKeptForTheGuardView() async {
        let source = StubSource()
        source.error = APIClientError.service(status: 409, body: ServiceErrorBody(
            error: "a client bound to profile work-ro could escape it", code: "binding_bypassable_without_auth",
            bindings: [BindingRef(clientId: "cursor", profile: "work-ro", mode: .locked)],
            fixes: [GuardFix(kind: "require_mcp_auth", target: nil)]))
        let model = AnonymousProfileModel(source: source, current: "work-ro")
        model.selection = "work-full"
        await model.save()
        XCTAssertEqual(model.guardRefusal?.bindings?.first?.clientId, "cursor")
        XCTAssertTrue(model.isDirty, "a refused change stays an unsaved edit")
        XCTAssertNil(model.savedNote)
    }

    func testAnExternalChangeIsFollowedUnlessTheOperatorIsEditing() {
        let model = AnonymousProfileModel(source: StubSource(), current: "a")
        model.adopt(current: "b")
        XCTAssertEqual(model.selection, "b")
        model.selection = "mine"
        model.adopt(current: "c")
        XCTAssertEqual(model.selection, "mine")
        XCTAssertEqual(model.saved, "c")
    }

    func testTheSectionIsMountedOnTheSecurityTabAndIdentified() throws {
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<2 { url.deleteLastPathComponent() }
        let view = try String(contentsOf: url.appendingPathComponent("MCPProxy/Views/AnonymousProfileSection.swift"))
        XCTAssertTrue(view.contains("settings-anonymous-profile"))
    }
}
