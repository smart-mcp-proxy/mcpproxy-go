import XCTest
@testable import MCPProxy

/// Pin the wire format of the JSON Merge Patch (RFC 7396) body the
/// macOS tray sends to PATCH /api/v1/servers/{id}.
///
/// The backend's delete signal is a literal JSON `null` value on the header /
/// env key (`{"headers": {"X-Stale": null}}`), and RFC 7396 makes the
/// distinction load-bearing: an ABSENT key means "leave it alone", `null` means
/// "delete it". Getting that wrong silently destroys or silently preserves a
/// user's setting.
///
/// So the patch is built as `[String: Any]` with `NSNull()` in place of Swift
/// `nil` and encoded via `JSONSerialization`, whose rendering of NSNull as the
/// literal `null` token is specified and stable.
///
/// The tempting shortcut — `[String: String?]` + `JSONEncoder` — is avoided
/// because Foundation does NOT specify how it encodes a nil map value, and the
/// behaviour has already changed once under us: it used to DROP the key (turning
/// a delete into "no change"), and current Foundation emits `null` (turning it
/// into a delete). Same source, opposite meaning, decided by whichever toolchain
/// happens to compile the app. That is the whole argument for the manual path.
///
/// These tests pin:
///   1. NSNull survives the encoder and lands on the wire as `null` (the
///      invariant that actually protects the feature).
///   2. `""` (set-to-empty) stays `""` and is never confused with a delete.
///   3. That `[String: String?]` + `JSONEncoder` remains unfit for purpose —
///      asserted WITHOUT depending on which of the two known behaviours the
///      current toolchain has, so CI cannot go red over a Foundation change that
///      production is immune to.
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

    /// Tripwire on `[String: String?]` + `JSONEncoder` — the tempting shortcut for
    /// building the patch, and the reason we don't take it.
    ///
    /// Deliberately does NOT assert which encoding the current toolchain produces.
    /// Both behaviours have shipped (drop the key; emit `null`), they mean OPPOSITE
    /// things under RFC 7396, and CI runs on a floating `macos-latest` image — so
    /// pinning either one would make the suite go red on an Xcode bump over a
    /// behaviour production does not depend on. Asserting "it is one of the two,
    /// and we cannot tell which" is the honest, stable statement, and it is also
    /// precisely the reason the shortcut is unusable.
    ///
    /// The invariant that actually protects the feature is asserted by the
    /// JSONSerialization/NSNull tests above, which do not depend on the toolchain.
    ///
    /// If this ever fails, Foundation has invented a THIRD behaviour: re-audit
    /// saveEdits() (it should still be immune — it never uses this path) before
    /// touching anything else.
    func testDefaultJSONEncoderIsUnfitForMergePatch() throws {
        struct Body: Encodable {
            let headers: [String: String?]
        }
        let body = Body(headers: ["X-Keep": "v", "X-Stale": nil])
        let str = String(data: try JSONEncoder().encode(body), encoding: .utf8)!

        XCTAssertTrue(str.contains("X-Keep"), "a present value must always survive")

        let dropsTheKey = !str.contains("X-Stale")
        let emitsNull = str.contains("\"X-Stale\":null") || str.contains("\"X-Stale\" : null")

        // Exactly one of the two known behaviours — we just don't get to choose it,
        // which is the point.
        XCTAssertTrue(dropsTheKey != emitsNull,
                      """
                      JSONEncoder produced a THIRD encoding for a nil map value (got: \(str)).
                      Re-audit saveEdits() — it should still be immune, because it builds \
                      [String: Any] with NSNull() and encodes via JSONSerialization — then \
                      update this tripwire.
                      """)
    }

    /// The load-bearing distinction, stated directly: under RFC 7396 an ABSENT key
    /// means "leave it alone" and `null` means "delete it". Our builder must be
    /// able to express both, and JSONSerialization does — no matter the toolchain.
    func testPatchCanExpressBothLeaveAloneAndDelete() throws {
        let body: [String: Any] = ["headers": ["X-Delete": NSNull()]]  // X-Untouched deliberately absent
        let str = String(data: try JSONSerialization.data(withJSONObject: body, options: [.sortedKeys]),
                         encoding: .utf8)!

        XCTAssertTrue(str.contains("\"X-Delete\":null"), "a delete must reach the wire as literal null")
        XCTAssertFalse(str.contains("X-Untouched"), "an untouched key must be absent, not null")
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
