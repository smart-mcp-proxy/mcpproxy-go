import XCTest
@testable import MCPProxy

/// The two serialization knobs (`tool_response_mode`, Spec 085, and
/// `direct_tool_response_mode`, Spec 102) shipped with four ways to set them —
/// the config file, an env var, a `serve` flag and the REST API — and zero UI
/// surfaces. A tray-only operator could not reach `direct_tool_response_mode`
/// at all, which is the whole point of the schema-deferred feature.
///
/// These pin the tray catalogue against the Web UI's fields.ts, which it must
/// mirror 1:1 (SettingsCatalog.swift header contract).
final class SettingsSerializationModeFieldsTests: XCTestCase {

    private func field(_ key: String) throws -> ConfigField {
        try XCTUnwrap(SettingsCatalog.allFields.first { $0.key == key },
                      "no settings field for \(key)")
    }

    // MARK: Catalogue

    func testBothSerializationAxesAreReachable() throws {
        XCTAssertEqual(try field("tool_response_mode").control, .select)
        XCTAssertEqual(try field("direct_tool_response_mode").control, .select)
    }

    /// Only the values `cfg.Validate()` accepts — offering anything else would
    /// hand the operator a 422 from PATCH /api/v1/config.
    func testOptionsMatchTheGoValidator() throws {
        XCTAssertEqual(try field("tool_response_mode").options.map(\.value), ["full", "compact"])
        XCTAssertEqual(try field("direct_tool_response_mode").options.map(\.value), ["full", "deferred"])
    }

    /// internal/runtime/config_hotreload.go appends both keys to ChangedFields
    /// WITHOUT setting RequiresRestart — badging them would demand a restart
    /// for nothing. `routing_mode`, directly above them in the same section,
    /// genuinely does need one, so the distinction has to stay visible.
    func testNeitherClaimsARestartButRoutingModeStillDoes() throws {
        XCTAssertFalse(try field("tool_response_mode").restart)
        XCTAssertFalse(try field("direct_tool_response_mode").restart)
        XCTAssertTrue(try field("routing_mode").restart)
    }

    func testPlacedNextToTheRoutingModeTheyQualify() {
        XCTAssertEqual(Array(SettingsCatalog.general.prefix(3)).map(\.key),
                       ["routing_mode", "tool_response_mode", "direct_tool_response_mode"])
    }

    func testEachLinksToAPageDocumentingThatAxis() throws {
        XCTAssertEqual(try field("tool_response_mode").docs, "/features/search-discovery#tool-response-mode")
        XCTAssertEqual(try field("direct_tool_response_mode").docs,
                       "/features/schema-deferred-direct-mode")
    }

    /// Only one axis is live at a time (it depends on `routing_mode`), so each
    /// help string has to say which mode it applies to.
    func testHelpNamesTheApplicableRoutingMode() throws {
        XCTAssertTrue(try XCTUnwrap(field("tool_response_mode").help).contains("Retrieve mode"))
        XCTAssertTrue(try XCTUnwrap(field("direct_tool_response_mode").help).contains("Direct mode"))
    }

    // MARK: normalizeDefaults

    /// Both keys are `omitempty` on the Go side, so a config that never set one
    /// simply has no key. A SwiftUI Picker whose selection matches no tag
    /// renders blank, which would show the default as "unset".
    func testAbsentModeResolvesToItsRealDefault() {
        let cfg = SettingsCatalog.normalizeDefaults(["listen": "127.0.0.1:8080"])
        XCTAssertEqual(cfg["tool_response_mode"] as? String, "full")
        XCTAssertEqual(cfg["direct_tool_response_mode"] as? String, "full")
    }

    func testEmptyStringIsTreatedAsAbsent() {
        let cfg = SettingsCatalog.normalizeDefaults([
            "tool_response_mode": "",
            "direct_tool_response_mode": "",
        ])
        XCTAssertEqual(cfg["tool_response_mode"] as? String, "full")
        XCTAssertEqual(cfg["direct_tool_response_mode"] as? String, "full")
    }

    func testExplicitNullIsTreatedAsAbsent() {
        let cfg = SettingsCatalog.normalizeDefaults(["tool_response_mode": NSNull()])
        XCTAssertEqual(cfg["tool_response_mode"] as? String, "full")
    }

    func testNeverOverwritesAnOperatorSetValue() {
        let cfg = SettingsCatalog.normalizeDefaults([
            "tool_response_mode": "compact",
            "direct_tool_response_mode": "deferred",
        ])
        XCTAssertEqual(cfg["tool_response_mode"] as? String, "compact")
        XCTAssertEqual(cfg["direct_tool_response_mode"] as? String, "deferred")
    }

    /// The normalized copy genuinely diverges from the raw response — which is
    /// exactly why ConfigStore.load() must normalize `working` AND `original`.
    /// Normalizing only one would make an untouched field read as dirty.
    func testDivergesFromTheRawResponse() {
        let raw: [String: Any] = ["listen": "127.0.0.1:8080"]
        let normalized = SettingsCatalog.normalizeDefaults(raw)
        XCTAssertNil(raw["tool_response_mode"])
        XCTAssertEqual(normalized["tool_response_mode"] as? String, "full")
        // Both copies normalized => every catalogue key compares equal.
        let other = SettingsCatalog.normalizeDefaults(raw)
        for field in SettingsCatalog.allFields {
            XCTAssertTrue(valuesEqual(configGet(normalized, field.key),
                                      configGet(other, field.key)),
                          "\(field.key) differs between two normalized copies")
        }
    }

    func testLeavesFieldsWithoutADefaultUntouched() {
        let cfg = SettingsCatalog.normalizeDefaults([:])
        for field in SettingsCatalog.allFields where field.defaultValue == nil {
            XCTAssertNil(configGet(cfg, field.key),
                         "\(field.key) declares no default but was populated")
        }
    }

    /// Only the fields that opt in carry a default — a blanket fill would
    /// invent values for every unset key in the config.
    func testOnlyTheSerializationModesDeclareADefault() {
        let withDefaults = SettingsCatalog.allFields
            .filter { $0.defaultValue != nil }
            .map(\.key)
            .sorted()
        XCTAssertEqual(withDefaults, ["direct_tool_response_mode", "tool_response_mode"])
    }
}
