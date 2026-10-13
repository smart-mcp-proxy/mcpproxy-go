// APIClientAddServerFromRegistryTests.swift
// MCPProxyTests
//
// Spec 109 PR-a review round 1 (low): the backend can assign a server a
// DIFFERENT name than the registry entry's display name (falling back to the
// entry id when the entry's own name is empty) and echoes the name it
// actually assigned in the response body (`data.server.name`).
// addServerFromRegistry used to discard that body entirely, so
// ServerBrowseView's "Added checkmark Open" flow keyed off the catalog
// display name instead — the Web UI already gets this right
// (Repositories.vue: `result.server?.name || server.name`).
//
// A name COLLISION is a hard failure (`duplicate_name`, HTTP 409), not a
// rename — round 1's own comment/test here claimed the opposite
// ("de-duplicating on conflict"); that claim was fictional (review round 2,
// finding 7). The success case below models the real cause of a differing
// name instead: the entry's own display name is empty.
//
// Round 2, finding 6: the failure envelope's message is the JSON key
// `error` (writeRegistryAddError in internal/httpapi/server.go), not
// `message` — RegistryAddServerErrorBody decoded the wrong key, so
// `result.message` was always nil against the real backend and every
// failure showed a generic "HTTP 400: ..." instead. The round-1 test below
// used a stub body keyed `message`, which the bug happily "matched" without
// ever asserting on the decoded text — a green test over a broken decode.

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
        // The backend assigned "everything-server" (derived from the entry
        // id because the catalog entry itself had no display name), which
        // differs from whatever display name the catalog card shows.
        ConnectStubURLProtocol.responseBody = ConnectStubURLProtocol.envelope("""
        {"server":{"name":"everything-server","protocol":"stdio","enabled":true,"quarantined":true}}
        """)
        let client = ConnectStubURLProtocol.makeClient()

        let result = await client.addServerFromRegistry(registryID: "testreg", serverID: "everything")

        XCTAssertTrue(result.success)
        XCTAssertEqual(result.serverName, "everything-server")
    }

    func testFailureCarriesNoServerName() async throws {
        ConnectStubURLProtocol.statusCode = 400
        // The real envelope (writeRegistryAddError) keys the message `error`,
        // not `message` — see this file's header comment (review round 2,
        // finding 6).
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"code":"missing_required_input","error":"needs input","missing_inputs":["API_KEY"]}
        """.utf8)
        let client = ConnectStubURLProtocol.makeClient()

        let result = await client.addServerFromRegistry(registryID: "testreg", serverID: "everything")

        XCTAssertFalse(result.success)
        XCTAssertNil(result.serverName)
        XCTAssertEqual(result.missingInputs, ["API_KEY"])
        // The actual regression check: decoding the real `error` key must
        // surface the backend's own reason, not the generic HTTP fallback.
        XCTAssertEqual(result.message, "needs input")
    }

    func testFailureFallsBackToGenericMessageOnlyWhenTheEnvelopeHasNoErrorField() async throws {
        ConnectStubURLProtocol.statusCode = 409
        ConnectStubURLProtocol.responseBody = Data("""
        {"success":false,"code":"duplicate_name"}
        """.utf8)
        let client = ConnectStubURLProtocol.makeClient()

        let result = await client.addServerFromRegistry(registryID: "testreg", serverID: "everything")

        XCTAssertFalse(result.success)
        XCTAssertNotNil(result.message)
        XCTAssertTrue(result.message?.contains("409") ?? false)
    }
}
