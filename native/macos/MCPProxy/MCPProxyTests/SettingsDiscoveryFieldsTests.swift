import XCTest
@testable import MCPProxy

/// Regression tests for the three "Tool discovery & health checks" settings
/// bugs (spec 074 duration fields rendered in the native tray):
///
///   1. Save stayed disabled — `.duration` validation rejected an empty value
///      even when the field is `optional` (nil = inherit the default).
///   2. The field hint was a hardcoded "2m" for every duration, instead of the
///      field's real default (30s / 5m), making it look like a value.
///   3. The section showed "2 unsaved changes" on first open — an absent
///      (nil) optional field whose text binding round-trips to "" read as
///      dirty because `valuesEqual("", nil)` was false.
///
/// These pin the tri-state contract (nil = default · "0s" = disabled · value)
/// at the form layer, mirroring the (more correct) Web UI behaviour.
final class SettingsDiscoveryFieldsTests: XCTestCase {

    private func durationField(optional: Bool) -> ConfigField {
        ConfigField(key: "x", label: "X", control: .duration, optional: optional)
    }

    // MARK: Bug 1 — Save disabled

    /// An optional duration left blank means "inherit the default" and MUST
    /// validate cleanly, otherwise the Save button is permanently disabled.
    func testOptionalDurationEmptyIsValid() {
        XCTAssertNil(validateConfigField(durationField(optional: true), ""))
        XCTAssertNil(validateConfigField(durationField(optional: true), NSNull()))
        XCTAssertNil(validateConfigField(durationField(optional: true), nil))
    }

    /// A required duration left blank is still an error (guard against
    /// over-relaxing the validator).
    func testRequiredDurationEmptyStillErrors() {
        XCTAssertNotNil(validateConfigField(durationField(optional: false), ""))
    }

    /// A non-empty but malformed duration is rejected regardless of optional.
    func testMalformedDurationStillErrors() {
        XCTAssertNotNil(validateConfigField(durationField(optional: true), "abc"))
    }

    /// A valid duration passes.
    func testValidDurationPasses() {
        XCTAssertNil(validateConfigField(durationField(optional: true), "30s"))
        XCTAssertNil(validateConfigField(durationField(optional: true), "1h30m"))
        XCTAssertNil(validateConfigField(durationField(optional: true), "0s")) // disabled
    }

    // MARK: Bug 2 — placeholder shows the real default

    func testDiscoveryDurationFieldsExposeDefaultPlaceholders() {
        let section = SettingsCatalog.advanced.first { $0.id == "discovery" }
        XCTAssertNotNil(section, "the discovery section must exist")
        let health = section?.fields.first { $0.key == "health_check_interval" }
        let discovery = section?.fields.first { $0.key == "tool_discovery_interval" }
        XCTAssertEqual(health?.placeholder, "30s",
                       "health-check hint must be its real default, not a generic 2m")
        XCTAssertEqual(discovery?.placeholder, "5m",
                       "tool-discovery hint must be its real default, not a generic 2m")
    }

    // MARK: Bug 3 — absent optional field is not dirty on first open

    /// nil (absent key), NSNull (explicit null) and "" (text binding round-trip
    /// of an empty optional field) are all "blank" and must compare equal, so
    /// an untouched optional field never reads as an unsaved change.
    func testBlankValuesCompareEqual() {
        XCTAssertTrue(valuesEqual(nil, ""))
        XCTAssertTrue(valuesEqual("", nil))
        XCTAssertTrue(valuesEqual(NSNull(), nil))
        XCTAssertTrue(valuesEqual(NSNull(), ""))
        XCTAssertTrue(valuesEqual("   ", nil)) // whitespace-only is blank too
    }

    /// A real value is still different from blank (so a genuine edit, or
    /// clearing a previously-set value, is correctly detected as dirty).
    func testRealValueIsNotEqualToBlank() {
        XCTAssertFalse(valuesEqual("40s", ""))
        XCTAssertFalse(valuesEqual("40s", nil))
        XCTAssertFalse(valuesEqual("30s", "40s"))
    }

    // MARK: emptying an optional field resets it to default (null on the wire)

    /// The text binding maps a blank optional-field string to "unset" (nil →
    /// stored as NSNull → JSON null → backend resets the pointer to its
    /// default), never an empty string (which the backend would fail to parse
    /// as a duration).
    func testOptionalScalarStoredMapsBlankToNil() {
        XCTAssertNil(optionalScalarStored(""))
        XCTAssertNil(optionalScalarStored("   "))
        XCTAssertEqual(optionalScalarStored("5m") as? String, "5m")
    }

    /// End-to-end: an optional field cleared to blank is stored as NSNull and
    /// the PATCH payload encodes it as the literal JSON `null` (reset-to-default),
    /// not "" — matching the RFC-7396 null=delete precedent already pinned in
    /// MergePatchEncodingTests.
    func testEmptiedOptionalDurationEncodesAsNullPatch() throws {
        var working: [String: Any] = [:]
        configSet(&working, "tool_discovery_interval", optionalScalarStored(""))
        let partial = buildPartial(working, ["tool_discovery_interval"])
        let data = try JSONSerialization.data(withJSONObject: partial, options: [.sortedKeys])
        let str = String(data: data, encoding: .utf8)!
        XCTAssertTrue(str.contains("\"tool_discovery_interval\":null"),
                      "emptied optional duration must reset to default via null; got: \(str)")
    }
}
