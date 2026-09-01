// IsolationOverridePatchTests.swift
// MCPProxyTests
//
// GH #1142 — the tray was a data-corruption path, not just a display bug.
//
// The API used to report the RAW per-server `isolation.enabled` override
// flattened to a bool, so a server that inherited global isolation (override
// nil, container actually running) arrived as `enabled: false`. The edit form
// seeded its toggle from that value and then force-added `enabled` to every
// isolation PATCH. Editing an unrelated field — the image — therefore persisted
// an explicit opt-out and silently un-containerised the server.
//
// These tests pin the pure edit-form → PATCH-dict builder over the tri-state.

import XCTest
@testable import MCPProxy

final class IsolationOverridePatchTests: XCTestCase {

    private let unchanged = IsolationEditState(
        image: "python:3.11", networkMode: "", extraArgs: [], workingDir: ""
    )

    // THE regression: changing only the image must not emit `enabled`.
    func testImageOnlyChangeEmitsNoEnabledKey() throws {
        let patch = ServerDetailView.buildIsolationPatch(
            override: .inherit,
            originalOverride: .inherit,
            edited: IsolationEditState(
                image: "python:3.12", networkMode: "", extraArgs: [], workingDir: ""
            ),
            original: unchanged
        )
        let iso = try XCTUnwrap(patch)
        XCTAssertEqual(iso["image"] as? String, "python:3.12")
        XCTAssertNil(
            iso["enabled_override"] as Any?,
            "an untouched isolation toggle must never appear in the PATCH — that is what silently wrote an explicit opt-out"
        )
        XCTAssertNil(
            iso["enabled"] as Any?,
            "`enabled` is the read-only EFFECTIVE state; writing it back is the read-modify-write corruption path"
        )
    }

    // Nothing changed at all → no PATCH.
    func testNoChangeEmitsNoPatch() {
        XCTAssertNil(ServerDetailView.buildIsolationPatch(
            override: .inherit,
            originalOverride: .inherit,
            edited: unchanged,
            original: unchanged
        ))
    }

    // Moving off "inherit" is a deliberate act and must be sent explicitly.
    func testForceOffEmitsExplicitFalse() throws {
        let patch = ServerDetailView.buildIsolationPatch(
            override: .forceOff,
            originalOverride: .inherit,
            edited: unchanged,
            original: unchanged
        )
        let iso = try XCTUnwrap(patch)
        XCTAssertEqual(iso["enabled_override"] as? Bool, false)
        XCTAssertNil(iso["enabled"] as Any?, "the write key is `enabled_override`")
    }

    func testForceOnEmitsExplicitTrue() throws {
        let patch = ServerDetailView.buildIsolationPatch(
            override: .forceOn,
            originalOverride: .inherit,
            edited: unchanged,
            original: unchanged
        )
        let iso = try XCTUnwrap(patch)
        XCTAssertEqual(iso["enabled_override"] as? Bool, true)
        XCTAssertNil(iso["enabled"] as Any?, "the write key is `enabled_override`")
    }

    // Going back to "inherit" must send an explicit null, otherwise the
    // override is unclearable — an omitted key means "leave alone".
    func testBackToInheritEmitsNull() throws {
        let patch = ServerDetailView.buildIsolationPatch(
            override: .inherit,
            originalOverride: .forceOff,
            edited: unchanged,
            original: unchanged
        )
        let iso = try XCTUnwrap(patch)
        XCTAssertTrue(
            iso["enabled_override"] is NSNull,
            "clearing the override back to inherit must send JSON null, not omit the key"
        )
    }

    // A sub-field edit on a server that IS explicitly opted out must still not
    // restate `enabled` — the persisted value is not the form's to re-assert.
    func testSubFieldEditOnExplicitServerDoesNotRestateEnabled() throws {
        let patch = ServerDetailView.buildIsolationPatch(
            override: .forceOff,
            originalOverride: .forceOff,
            edited: IsolationEditState(
                image: "", networkMode: "none", extraArgs: [], workingDir: ""
            ),
            original: IsolationEditState(
                image: "", networkMode: "bridge", extraArgs: [], workingDir: ""
            )
        )
        let iso = try XCTUnwrap(patch)
        XCTAssertEqual(iso["network_mode"] as? String, "none")
        XCTAssertNil(iso["enabled_override"] as Any?)
        XCTAssertNil(iso["enabled"] as Any?)
    }

    // The picker must be seeded from the RAW override, never from the
    // effective state — that conflation is the whole bug.
    func testOverrideSeededFromRawTriState() {
        XCTAssertEqual(IsolationOverride(rawOverride: nil), .inherit)
        XCTAssertEqual(IsolationOverride(rawOverride: true), .forceOn)
        XCTAssertEqual(IsolationOverride(rawOverride: false), .forceOff)
    }
}
