import XCTest
@testable import MCPProxy

/// Pin the wire format of the JSON Merge Patch (RFC 7396) body the
/// macOS tray sends to PATCH /api/v1/servers/{id}.
///
/// The backend's delete signal is a literal JSON `null` value on the
/// header / env key (`{"headers": {"X-Stale": null}}`). Swift's default
/// `JSONEncoder` on `[String: String?]` drops nil entries from the output
/// entirely — so a naive encoding would send `{"headers": {}}` and the
/// backend would interpret that as "no headers changed", silently
/// dropping the user's delete intent.
///
/// The fix is to build the patch dict as `[String: Any]` with `NSNull()`
/// in place of Swift `nil`, and to encode via `JSONSerialization` which
/// faithfully renders NSNull as the literal `null` token. These tests
/// pin both:
///   1. NSNull survives the encoder and lands on the wire as `null`.
///   2. The simpler-but-wrong `[String: String?]` + `JSONEncoder` path
///      DROPS the key, demonstrating why we use the manual approach.
final class MergePatchEncodingTests: XCTestCase {

    /// JSONSerialization with NSNull renders the JSON null token.
    func testJSONSerializationEncodesNSNullAsLiteralNull() throws {
        let body: [String: Any] = [
            "headers": [
                "X-Keep":  "value",
                "X-Stale": NSNull(),
            ],
        ]

        let data = try JSONSerialization.data(withJSONObject: body, options: [.sortedKeys])
        let str = String(data: data, encoding: .utf8)!

        // Sorted keys give a stable representation; we look for the
        // literal `:null` after the deleted key.
        XCTAssertTrue(str.contains("\"X-Stale\":null"),
                      "deleted key must encode as literal null in the PATCH body; got: \(str)")
        XCTAssertTrue(str.contains("\"X-Keep\":\"value\""),
                      "upserted key must encode with its string value; got: \(str)")
    }

    /// Round-trip: a delete-only patch encodes the key as `null` and a
    /// JSON re-parse confirms the value really is NSNull (not an empty
    /// string or omitted entry).
    func testDeleteOnlyPatchRoundTrips() throws {
        let body: [String: Any] = ["headers": ["X-Old": NSNull()]]
        let data = try JSONSerialization.data(withJSONObject: body)

        guard let parsed = try JSONSerialization.jsonObject(with: data) as? [String: Any] else {
            return XCTFail("re-parse failed")
        }
        guard let headers = parsed["headers"] as? [String: Any] else {
            return XCTFail("headers key missing")
        }
        XCTAssertNotNil(headers["X-Old"], "X-Old key must be present in the parsed body — absence means delete intent was lost")
        XCTAssertTrue(headers["X-Old"] is NSNull, "X-Old value must round-trip as NSNull, not be coerced to anything else")
    }

    /// Demonstrate the WRONG path so a future refactor can't tell itself
    /// "JSONEncoder should be fine, let's switch". `[String: String?]`
    /// run through the default encoder strips nil values silently.
    func testDefaultJSONEncoderDropsOptionalNilFromMap() throws {
        struct Body: Encodable {
            let headers: [String: String?]
        }
        let body = Body(headers: ["X-Keep": "v", "X-Stale": nil])
        let data = try JSONEncoder().encode(body)
        let str = String(data: data, encoding: .utf8)!

        // X-Stale must NOT appear in the encoded JSON — which is exactly
        // why we don't use this approach. If a future change makes the
        // encoder start emitting nil as `null`, this test fails and the
        // author has to update both the test and the saveEdits() path.
        XCTAssertFalse(str.contains("X-Stale"),
                       "documented pitfall: default JSONEncoder drops nil map values; if this changes, audit saveEdits()")
        XCTAssertTrue(str.contains("X-Keep"))
    }

    /// Sanity: an empty-string value (set-to-empty) must NOT be treated
    /// as a delete. JSON Merge Patch differentiates `""` (set) from
    /// `null` (delete); the encoder must render both faithfully.
    func testEmptyStringSurvivesAsEmptyStringNotNull() throws {
        let body: [String: Any] = ["headers": ["X-Empty": "", "X-Stale": NSNull()]]
        let data = try JSONSerialization.data(withJSONObject: body, options: [.sortedKeys])
        let str = String(data: data, encoding: .utf8)!

        XCTAssertTrue(str.contains("\"X-Empty\":\"\""),
                      "empty string must encode as \"\" not null; got: \(str)")
        XCTAssertTrue(str.contains("\"X-Stale\":null"),
                      "explicit null must encode as null; got: \(str)")
    }
}
