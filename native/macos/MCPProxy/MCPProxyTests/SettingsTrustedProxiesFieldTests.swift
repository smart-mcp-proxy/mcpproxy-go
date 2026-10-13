import XCTest
@testable import MCPProxy

/// Spec 107 FR-027 / T058: the edition-neutral `trusted_proxies` key is a
/// []string in the config file and a one-entry-per-line textarea in the Web
/// UI (`listKind: 'lines'`). The native catalogue must carry the same row
/// (scripts/check-settings-parity.py) and must round-trip the ARRAY — a plain
/// string binding would render the list as "" and save a string the core
/// cannot decode into []string.
final class SettingsTrustedProxiesFieldTests: XCTestCase {

    private var field: ConfigField? {
        SettingsCatalog.security.first { $0.key == "trusted_proxies" }
    }

    @MainActor
    private func hydratedStore(_ cfg: [String: Any]) -> ConfigStore {
        let store = ConfigStore(appState: AppState())
        store.hydrate(from: cfg)
        return store
    }

    func testRowExistsAsALinesTextareaInSecurity() {
        XCTAssertNotNil(field, "trusted_proxies must be in the Security & Access section")
        XCTAssertEqual(field?.control, .textarea)
        XCTAssertTrue(field?.listLines ?? false, "the row must bind a []string, not a scalar")
        XCTAssertTrue(field?.optional ?? false)
        XCTAssertFalse(field?.restart ?? true, "trusted_proxies is hot-reloaded (FR-027)")
    }

    func testParseLinesSplitsOnNewlinesAndCommasAndDropsBlanks() {
        XCTAssertEqual(parseLines("10.0.0.0/8\n 192.168.1.1 \n\n,fd00::/8,"),
                       ["10.0.0.0/8", "192.168.1.1", "fd00::/8"])
        XCTAssertEqual(parseLines("   \n  "), [])
    }

    func testLinesTextRendersOneEntryPerLine() {
        XCTAssertEqual(linesText(["10.0.0.0/8", "fd00::/8"]), "10.0.0.0/8\nfd00::/8")
        XCTAssertEqual(linesText(nil), "")
        XCTAssertEqual(linesText(NSNull()), "")
    }

    @MainActor
    func testBindingReadsTheArrayAndWritesAnArrayBack() {
        let store = hydratedStore(["trusted_proxies": ["10.0.0.0/8"]])
        let binding = store.linesBinding("trusted_proxies")
        XCTAssertEqual(binding.wrappedValue, "10.0.0.0/8")
        XCTAssertFalse(store.isDirty("trusted_proxies"))

        binding.wrappedValue = "10.0.0.0/8\nfd00::/8"
        XCTAssertEqual(store.value("trusted_proxies") as? [String], ["10.0.0.0/8", "fd00::/8"])
        XCTAssertTrue(store.isDirty("trusted_proxies"))

        // The PATCH payload carries the array, never a joined string.
        let partial = buildPartial(["trusted_proxies": store.value("trusted_proxies") as Any], ["trusted_proxies"])
        XCTAssertEqual(partial["trusted_proxies"] as? [String], ["10.0.0.0/8", "fd00::/8"])
    }

    /// Clearing a list the core DID send must save an empty list (trust
    /// nobody), not "unset" — the Web UI sends [] for the same edit.
    @MainActor
    func testClearingAPresentListSavesAnEmptyList() {
        let store = hydratedStore(["trusted_proxies": ["10.0.0.0/8"]])
        store.linesBinding("trusted_proxies").wrappedValue = "\n"
        XCTAssertEqual(store.value("trusted_proxies") as? [String], [])
        XCTAssertTrue(store.isDirty("trusted_proxies"))
    }

    /// A key the core never sent must not read as dirty after the textarea is
    /// touched and left blank (the same tri-state as the optional durations).
    @MainActor
    func testBlankOnAnAbsentKeyStaysUndirty() {
        let store = hydratedStore(["listen": "127.0.0.1:8080"])
        XCTAssertFalse(store.isDirty("trusted_proxies"))
        store.linesBinding("trusted_proxies").wrappedValue = "  \n"
        XCTAssertFalse(store.isDirty("trusted_proxies"))
        XCTAssertTrue(store.dirtyKeys(in: SettingsCatalog.allFields).isEmpty)
    }
}
