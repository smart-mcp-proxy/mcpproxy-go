import XCTest
@testable import MCPProxy

/// MCP-902 — macOS mirror of the MCP-867 Web UI registry surface.
/// Covers the pure, testable units the view relies on: provenance/trust
/// derivation, add-source error-code → message mapping, JSON decode of the
/// list + add-source payloads, and the one-time third-party warning ack.
final class RegistryModelsTests: XCTestCase {

    private func decode<T: Decodable>(_ type: T.Type, from jsonString: String) throws -> T {
        let data = jsonString.data(using: .utf8)!
        return try JSONDecoder().decode(T.self, from: data)
    }

    // MARK: - Provenance / trust derivation (mirrors Web UI isCustomRegistry)

    func testRegistryProvenanceConstants() {
        XCTAssertEqual(RegistryProvenance.official, "official/trusted")
        XCTAssertEqual(RegistryProvenance.custom, "custom/unverified")
    }

    func testOfficialRegistryIsNotCustom() throws {
        let json = """
        {"id": "official", "name": "Official MCP Registry",
         "provenance": "official/trusted", "trusted": true}
        """
        let reg = try decode(Registry.self, from: json)
        XCTAssertFalse(reg.isCustom)
    }

    func testCustomProvenanceIsCustom() throws {
        let json = """
        {"id": "acme", "name": "Acme Corp",
         "provenance": "custom/unverified", "trusted": false}
        """
        let reg = try decode(Registry.self, from: json)
        XCTAssertTrue(reg.isCustom)
    }

    func testTrustedFalseAloneIsCustom() throws {
        // Defensive: trusted=false with no provenance still reads as custom.
        let json = """
        {"id": "weird", "name": "Weird", "trusted": false}
        """
        let reg = try decode(Registry.self, from: json)
        XCTAssertTrue(reg.isCustom)
    }

    func testMissingProvenanceTreatedAsOfficial() throws {
        // Older payloads without the field are treated as official/trusted.
        let json = """
        {"id": "legacy", "name": "Legacy"}
        """
        let reg = try decode(Registry.self, from: json)
        XCTAssertFalse(reg.isCustom)
    }

    // MARK: - List decode

    func testDecodeGetRegistriesResponse() throws {
        let json = """
        {
          "registries": [
            {"id": "official", "name": "Official MCP Registry",
             "description": "Primary aggregator", "url": "https://registry.modelcontextprotocol.io",
             "servers_url": "https://registry.modelcontextprotocol.io/v0.1/servers",
             "protocol": "modelcontextprotocol/registry",
             "provenance": "official/trusted", "trusted": true, "count": 1234, "tags": ["x"]},
            {"id": "acme", "name": "Acme", "protocol": "modelcontextprotocol/registry",
             "provenance": "custom/unverified", "trusted": false}
          ],
          "total": 2
        }
        """
        let resp = try decode(GetRegistriesResponse.self, from: json)
        XCTAssertEqual(resp.registries.count, 2)
        XCTAssertEqual(resp.registries[0].id, "official")
        XCTAssertEqual(resp.registries[0].serversURL, "https://registry.modelcontextprotocol.io/v0.1/servers")
        XCTAssertFalse(resp.registries[0].isCustom)
        XCTAssertTrue(resp.registries[1].isCustom)
    }

    // MARK: - Add-source result decode + error mapping

    func testDecodeRegistrySummary() throws {
        let json = """
        {"id": "acme", "name": "Acme Corp", "url": "https://registry.acme.com",
         "servers_url": "https://registry.acme.com/v0.1/servers",
         "protocol": "modelcontextprotocol/registry",
         "provenance": "custom/unverified", "trusted": false}
        """
        let summary = try decode(RegistrySummary.self, from: json)
        XCTAssertEqual(summary.id, "acme")
        XCTAssertEqual(summary.provenance, "custom/unverified")
        XCTAssertEqual(summary.trusted, false)
    }

    func testErrorMessageInvalidURL() {
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "invalid_registry_url", fallback: nil),
            "That URL is not a valid HTTPS registry endpoint."
        )
        // A server-supplied fallback for invalid_registry_url is preferred.
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "invalid_registry_url", fallback: "must be https"),
            "must be https"
        )
    }

    func testErrorMessageRegistriesLocked() {
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "registries_locked", fallback: "ignored"),
            "Adding registries is locked by an administrator on this instance."
        )
    }

    func testErrorMessageShadowsBuiltin() {
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "registry_shadows_builtin", fallback: nil),
            "That id/host collides with a built-in registry. Try a different id."
        )
    }

    func testErrorMessageDuplicate() {
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "duplicate_registry", fallback: nil),
            "A registry with that id is already configured."
        )
    }

    func testErrorMessageUnknownFallsBack() {
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: nil, fallback: "boom"),
            "boom"
        )
        XCTAssertEqual(
            AddRegistrySourceResult.message(code: "totally_unknown", fallback: nil),
            "Failed to add registry."
        )
    }

    // MARK: - One-time third-party warning ack persistence (mirrors localStorage)

    func testThirdPartyAckPersistence() throws {
        let suiteName = "test-thirdparty-ack-\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suiteName)!
        defer { defaults.removePersistentDomain(forName: suiteName) }

        let ack = ThirdPartyRegistryAck(defaults: defaults)
        XCTAssertFalse(ack.hasAcknowledged, "fresh defaults should not be acknowledged")

        ack.acknowledge()
        XCTAssertTrue(ack.hasAcknowledged, "acknowledge() must persist")

        // A new instance over the same defaults sees the persisted ack.
        let ack2 = ThirdPartyRegistryAck(defaults: defaults)
        XCTAssertTrue(ack2.hasAcknowledged)
    }
}
