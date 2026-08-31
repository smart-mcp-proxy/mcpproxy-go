// AddServerIsolationFieldTests.swift
// MCPProxyTests
//
// Docker isolation only applies to local stdio (command-based) servers — there
// is a child process to sandbox. A URL-based transport (http / sse /
// streamable-http) is just a remote endpoint the proxy connects to, so the
// "Docker Isolation" toggle is meaningless and must neither appear nor be
// persisted for those servers. These tests pin the pure gating seam behind the
// Add Server sheet so the rule is enforced without a running app or API client.
// Mirrors the Web UI's `:disabled="formData.type !== 'stdio'"` gating in
// AddServerModal.vue (and its stdio-only `isolation_json` emission).

import XCTest
@testable import MCPProxy

final class AddServerIsolationFieldTests: XCTestCase {

    // (a) stdio → the isolation field is offered.
    func testIsolationFieldVisibleForStdio() {
        XCTAssertTrue(
            ManualServerForm.showsDockerIsolation(forProtocol: "stdio"),
            "Docker isolation applies to local command-based servers — the toggle must show"
        )
    }

    // (b) URL transports → the isolation field is hidden.
    func testIsolationFieldHiddenForURLTransports() {
        for proto in ["http", "sse", "streamable-http"] {
            XCTAssertFalse(
                ManualServerForm.showsDockerIsolation(forProtocol: proto),
                "\(proto) is a remote URL — there is no local process to isolate; the toggle must not show"
            )
        }
    }

    // (c-1) A stdio server config carries the isolation field.
    func testStdioConfigCarriesIsolationField() {
        let config = ManualServerForm.makeServerConfig(
            name: "local-fs",
            selectedProtocol: "stdio",
            enabled: true,
            dockerIsolation: true,
            quarantined: false,
            url: "",
            command: "npx",
            argsText: "@modelcontextprotocol/server-filesystem",
            workingDir: "",
            envText: ""
        )
        XCTAssertEqual(config["command"] as? String, "npx")
        XCTAssertNil(config["url"], "a stdio server must not carry a url")
        let isolation = config["isolation"] as? [String: Any]
        XCTAssertEqual(
            isolation?["enabled"] as? Bool, true,
            "a stdio server records its isolation choice via the backend `isolation` object"
        )
    }

    // (c-1b) The toggle OFF must emit NO isolation key at all (GH #1142).
    // Emitting `["enabled": false]` unconditionally wrote a permanent explicit
    // opt-out onto every stdio server added from the tray, so the server would
    // ignore global Docker isolation forever — a silent security downgrade the
    // user never asked for. "Off" here means "inherit the global setting",
    // matching AddServerModal.vue, which only writes isolation_json when the
    // box is ticked.
    func testStdioToggleOffOmitsIsolation() {
        let config = ManualServerForm.makeServerConfig(
            name: "local-fs",
            selectedProtocol: "stdio",
            enabled: true,
            dockerIsolation: false,
            quarantined: false,
            url: "",
            command: "npx",
            argsText: "@modelcontextprotocol/server-filesystem",
            workingDir: "",
            envText: ""
        )
        XCTAssertEqual(config["command"] as? String, "npx")
        XCTAssertNil(
            config["isolation"],
            "an unticked isolation box means \"inherit the global setting\" — it must not persist an explicit opt-out"
        )
    }

    // (c-2) A brand-new HTTP server config never carries an enabled isolation,
    // even when the (stale) toggle value is still true — i.e. switching an
    // in-progress form from stdio→http must not leak isolation into the config.
    func testHTTPConfigOmitsIsolationEvenWhenToggleTrue() {
        let config = ManualServerForm.makeServerConfig(
            name: "remote-api",
            selectedProtocol: "http",
            enabled: true,
            dockerIsolation: true, // stale value carried over from a stdio selection
            quarantined: false,
            url: "https://api.example.com/mcp",
            command: "",
            argsText: "",
            workingDir: "",
            envText: ""
        )
        XCTAssertEqual(config["url"] as? String, "https://api.example.com/mcp")
        XCTAssertNil(config["command"], "an http server must not carry a command")
        XCTAssertNil(
            config["isolation"],
            "a URL-based server must never persist an isolation flag — there is no local process"
        )
        XCTAssertNil(
            config["docker_isolation"],
            "and never the legacy dead key either"
        )
    }
}
