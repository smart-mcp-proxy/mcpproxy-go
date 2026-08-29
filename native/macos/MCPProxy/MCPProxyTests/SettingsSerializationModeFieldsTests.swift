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
    /// what makes the "normalize both snapshots" rule load-bearing rather than
    /// decorative.
    func testDivergesFromTheRawResponse() {
        let raw: [String: Any] = ["listen": "127.0.0.1:8080"]
        let normalized = SettingsCatalog.normalizeDefaults(raw)
        XCTAssertNil(raw["tool_response_mode"], "the core omits the key entirely")
        XCTAssertEqual(normalized["tool_response_mode"] as? String, "full")
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

    // MARK: ConfigStore hydration
    //
    // Cross-model review caught that the assertion here used to compare two
    // direct calls to normalizeDefaults — which passes even if ConfigStore
    // normalizes only ONE snapshot, the exact bug it was meant to catch. These
    // drive the real store instead, through the `hydrate(from:)` seam load()
    // uses.

    @MainActor
    private func hydratedStore(_ cfg: [String: Any]) -> ConfigStore {
        let store = ConfigStore(appState: AppState())
        store.hydrate(from: cfg)
        return store
    }

    /// The invariant that matters: after hydrating from a config that omits
    /// both modes, neither field is dirty. Normalizing only `working` (and not
    /// `original`) makes both read as unsaved changes the moment Settings opens.
    @MainActor
    func testHydrationLeavesTheOmittedModesUndirty() {
        let store = hydratedStore(["listen": "127.0.0.1:8080"])
        XCTAssertEqual(store.value("tool_response_mode") as? String, "full")
        XCTAssertEqual(store.value("direct_tool_response_mode") as? String, "full")
        XCTAssertFalse(store.isDirty("tool_response_mode"))
        XCTAssertFalse(store.isDirty("direct_tool_response_mode"))
        XCTAssertTrue(store.dirtyKeys(in: SettingsCatalog.allFields).isEmpty,
                      "a freshly hydrated form must have no unsaved changes")
    }

    /// The Raw tab reads `raw`, not the normalized snapshot, so it must not
    /// show a key the config file does not contain.
    @MainActor
    func testRawTabShowsServerTruthNotTheInventedDefault() {
        let store = hydratedStore(["listen": "127.0.0.1:8080"])
        XCTAssertFalse(store.prettyJSON.contains("tool_response_mode"),
                       "Raw tab must not invent a key the core never sent")
        XCTAssertTrue(store.prettyJSON.contains("listen"))
    }

    /// An explicitly-set mode survives hydration untouched and stays undirty.
    @MainActor
    func testHydrationPreservesAnExplicitMode() {
        let store = hydratedStore([
            "listen": "127.0.0.1:8080",
            "direct_tool_response_mode": "deferred",
        ])
        XCTAssertEqual(store.value("direct_tool_response_mode") as? String, "deferred")
        XCTAssertFalse(store.isDirty("direct_tool_response_mode"))
        XCTAssertTrue(store.prettyJSON.contains("direct_tool_response_mode"))
    }

    /// A genuine edit is still detected — the guards above must not be passing
    /// simply because dirty-tracking is broken.
    @MainActor
    func testAGenuineEditIsStillDirty() {
        let store = hydratedStore(["listen": "127.0.0.1:8080"])
        store.setValue("direct_tool_response_mode", "deferred")
        XCTAssertTrue(store.isDirty("direct_tool_response_mode"))
        XCTAssertFalse(store.isDirty("tool_response_mode"))
    }
}
