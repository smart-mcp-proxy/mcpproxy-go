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

    /// Tripwire on `[String: String?]` + `JSONEncoder` — the tempting shortcut
    /// for building the patch, and the reason we don't take it.
    ///
    /// How Foundation encodes a nil map value is NOT stable across toolchains:
    /// it used to DROP the key entirely (so a delete silently became "no change"),
    /// and current Foundation emits `null` instead (so the same code would now
    /// mean "delete"). Either way the meaning of a user's edit would be decided by
    /// the Swift runtime rather than by us, and it flipped once already — which is
    /// exactly why `saveEdits()` builds `[String: Any]` with `NSNull()` and encodes
    /// through JSONSerialization, whose rendering is specified.
    ///
    /// This test therefore pins the observed behaviour rather than a preference.
    /// If it fails, Foundation has changed AGAIN: re-audit saveEdits() (it should
    /// still be immune, because it does not use this path) and update the comment.
    func testDefaultJSONEncoderNilMapEncodingIsToolchainDefined() throws {
        struct Body: Encodable {
            let headers: [String: String?]
        }
        let body = Body(headers: ["X-Keep": "v", "X-Stale": nil])
        let str = String(data: try JSONEncoder().encode(body), encoding: .utf8)!

        XCTAssertTrue(str.contains("X-Keep"), "a present value must always survive")

        // Current Foundation: nil is emitted as an explicit null.
        XCTAssertTrue(str.contains("\"X-Stale\":null") || str.contains("\"X-Stale\" : null"),
                      """
                      Foundation's encoding of nil map values changed again (got: \(str)).
                      saveEdits() does not use this path — verify that is still true — and \
                      update this tripwire to the new behaviour.
                      """)
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
