import XCTest
@testable import MCPProxy

/// Spec 108-l (FR-052, L5/L6, T121): the native enums and the words macOS shows
/// are pinned to the SAME goldens as Go and the Web UI:
/// `internal/profile/testdata/contract/enums.json` (generated from contract.go)
/// and `labels.json` (the Terminology table). A value the Go side adds fails
/// here until the tolerant enum learns it; a word that differs from the Web
/// UI's fails here too.
final class ProfilesEnumsLabelsTests: XCTestCase {

    private var contractDirectory: URL {
        // native/macos/MCPProxy/MCPProxyTests/<this file> → repo root.
        var url = URL(fileURLWithPath: #filePath)
        for _ in 0..<5 { url.deleteLastPathComponent() }
        return url.appendingPathComponent("internal/profile/testdata/contract")
    }

    private func golden<T: Decodable>(_ name: String, as type: T.Type) throws -> T {
        let data = try Data(contentsOf: contractDirectory.appendingPathComponent(name))
        return try JSONDecoder().decode(T.self, from: data)
    }

    private func enums() throws -> [String: [String]] { try golden("enums.json", as: [String: [String]].self) }

    private func labelFamily(_ name: String) throws -> [String: String] {
        let all = try golden("labels.json", as: [String: AnyCodableLabel].self)
        guard case .table(let table)? = all[name] else {
            XCTFail("labels.json has no table \(name)")
            return [:]
        }
        return table
    }

    private func isUnknown<E: TolerantStringEnum>(_ value: E) -> Bool {
        String(describing: value).hasPrefix("unknown(")
    }

    // MARK: enums

    /// Every golden value of a tolerant family maps to a KNOWN case and
    /// round-trips its wire spelling.
    private func assertTolerantFamily<E: TolerantStringEnum>(
        _ family: String, _ type: E.Type, literalUnknown: Set<String> = []
    ) throws {
        let values = try XCTUnwrap(try enums()[family], "enums.json has no \(family)")
        XCTAssertFalse(values.isEmpty, family)
        for value in values {
            let decoded = E(wire: value)
            XCTAssertEqual(decoded.wire, value, "\(family).\(value) must round-trip")
            if !literalUnknown.contains(value) {
                XCTAssertFalse(isUnknown(decoded), "\(family).\(value) decodes to .unknown: the Swift enum is missing a case")
            }
        }
        // An unlisted value is tolerated, not dropped.
        XCTAssertEqual(E(wire: "not_a_real_value").wire, "not_a_real_value")
    }

    func testCredentialStateCoversTheGoValues() throws {
        // `unknown` is itself a Go value (the stat-only listing); it decodes
        // into `.unknown("unknown")` by design.
        try assertTolerantFamily("credential_state", CredentialState.self, literalUnknown: ["unknown"])
    }

    func testBindingModeCoversTheGoValues() throws { try assertTolerantFamily("binding_mode", BindingMode.self) }
    func testWarningSeverityCoversTheGoValues() throws { try assertTolerantFamily("warning_severity", WarningSeverity.self) }
    func testFixActionCoversTheGoValues() throws { try assertTolerantFamily("fix_action", FixAction.self) }
    func testExplainVerdictCoversTheGoValues() throws { try assertTolerantFamily("explain_verdict", ExplainVerdict.self) }

    func testTheComparisonCanFail() {
        XCTAssertTrue(isUnknown(FixAction(wire: "brand_new_fix")))
        XCTAssertFalse(isUnknown(FixAction(wire: "move_client")))
    }

    // MARK: labels

    func testAccessReasonWordsAreTheSharedTable() throws {
        let words = try labelFamily("access_reason")
        let reasons = try XCTUnwrap(try enums()["access_reason"])
        XCTAssertEqual(Set(words.keys), Set(reasons))
        for reason in reasons {
            XCTAssertEqual(AccessReasonText.label(reason), words[reason], "access reason \(reason)")
        }
        // Two reasons never collapse into one word (server_not_in_profile and
        // server_in_scope were both "Server not in scope").
        XCTAssertEqual(Set(reasons.map(AccessReasonText.label)).count, reasons.count)
        let none = try golden("labels.json", as: [String: AnyCodableLabel].self)["access_reason_none"]
        guard case .word(let word)? = none else { return XCTFail("labels.json has no access_reason_none") }
        XCTAssertEqual(AccessReasonText.label(""), word)
    }

    func testToolApprovalIsNeverCalledAwaitingApproval() {
        XCTAssertEqual(AccessReasonText.label("tool_approval"), "Needs review")
    }

    func testSourceWordsAreTheSharedTable() throws {
        let words = try labelFamily("source")
        for source in try XCTUnwrap(try enums()["source"]) {
            XCTAssertEqual(ProfileSourceText.label(source), words[source], "source \(source)")
        }
    }

    func testExplainStepWordsAreTheSharedTable() throws {
        let words = try labelFamily("explain_step")
        for step in try XCTUnwrap(try enums()["explain_step"]) {
            XCTAssertEqual(ExplainStepText.label(step), words[step], "step \(step)")
            XCTAssertEqual(ExplainStepRow(step: step, status: .pass).stepLabel, words[step])
        }
    }

    func testCredentialStateWordsAreTheSharedTable() throws {
        let words = try labelFamily("credential_state")
        for state in try XCTUnwrap(try enums()["credential_state"]) {
            XCTAssertEqual(CredentialState(wire: state).badgeLabel, words[state], "credential state \(state)")
        }
    }

    /// Spec 108 D39 (T150): the fix-button words of a row without a usable client
    /// credential are the `credential_cta` table, identical on the Web UI. The
    /// button and menu titles add a trailing "…" (a dialog follows).
    func testCredentialCTAWordsEqualLabels() throws {
        let words = try labelFamily("credential_cta")
        let states = try XCTUnwrap(try enums()["credential_state"])
        XCTAssertEqual(Set(words.keys), Set(states))
        for wire in states {
            let state = CredentialState(wire: wire)
            XCTAssertEqual(state.ctaWord, words[wire], "credential_cta.\(wire)")
            let cta = ClientBindingControlsState(ClientBindingModelTests.record(credential: wire)).cta
            if wire == "client" {
                XCTAssertNil(cta)
            } else {
                let title = try XCTUnwrap(cta).title
                XCTAssertTrue(title.hasSuffix("…"), title)
                XCTAssertEqual(String(title.dropLast()), words[wire], "title of \(wire) minus the ellipsis")
            }
        }
    }

    func testBindingModeWordsAreTheSharedTable() throws {
        let words = try labelFamily("binding_mode")
        for mode in try XCTUnwrap(try enums()["binding_mode"]) {
            XCTAssertEqual(BindingMode(wire: mode).label, words[mode], "mode \(mode)")
        }
    }

    func testUnannotatedWordsAreTheSharedTable() throws {
        let words = try labelFamily("unannotated")
        for value in try XCTUnwrap(try enums()["unannotated"]) {
            XCTAssertEqual(UnannotatedText.label(value), words[value], "unannotated \(value)")
        }
    }

    func testTierCapWordsAreTheSharedTable() throws {
        let words = try labelFamily("max_tier")
        for tier in try XCTUnwrap(try enums()["max_tier"]) {
            XCTAssertEqual(MaxTierText.label(tier), words[tier], "max_tier \(tier)")
        }
    }
}

/// labels.json holds tables of words plus loose scalar words (`access_reason_none`)
/// and a `_comment`.
private enum AnyCodableLabel: Decodable {
    case word(String)
    case table([String: String])

    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if let word = try? c.decode(String.self) { self = .word(word) } else { self = .table(try c.decode([String: String].self)) }
    }
}
