// APIClientAddServerFromRegistryTests.swift
// MCPProxyTests
//
// Spec 109 PR-a review round 1 (low): the backend can assign a server a
// DIFFERENT name than the registry entry's display name (falling back to the
// entry id when the entry's own name is empty, or de-duplicating on
// conflict) and echoes the name it actually assigned in the response body
// (`data.server.name`). addServerFromRegistry used to discard that body
// entirely, so ServerBrowseView's "Added checkmark Open" flow keyed off the
// catalog display name instead — the Web UI already gets this right
// (Repositories.vue: `result.server?.name || server.name`).

import XCTest
@testable import MCPProxy

final class APIClientAddServerFromRegistryTests: XCTestCase {

    override func setUp() {
        super.setUp()
        ConnectStubURLProtocol.reset()
    }

    override func tearDown() {
        ConnectStubURLProtocol.reset()
        super.tearDown()
    }

    func testSuccessCarriesTheBackendAssignedServerName() async throws {
        // The backend assigned "everything-2" (a conflict suffix), not the
        // catalog entry's own display name.
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"server":{"name":"everything-2","protocol":"stdio","enabled":true,"quarantined":true}}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        let result = await client.addServerFromRegistry(registryID: "testreg", serverID: "everything")

        XCTAssertTrue(result.success)
        XCTAssertEqual(result.serverName, "everything-2")
    }

    func testFailureCarriesNoServerName() async throws {
        ConnectStubURLProtocol.statusCode = 400
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"code":"missing_required_input","message":"needs input","missing_inputs":["API_KEY"]}
        """.utf8)
        let client = ConnectStubURLProtocol.makeClient()

        let result = await client.addServerFromRegistry(registryID: "testreg", serverID: "everything")

        XCTAssertFalse(result.success)
        XCTAssertNil(result.serverName)
        XCTAssertEqual(result.missingInputs, ["API_KEY"])
    }
}
