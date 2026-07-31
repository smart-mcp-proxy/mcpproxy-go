// FakeConnectSource.swift
// MCPProxyTests/Support
//
// The Connect form's data source, scripted. Every response is synthesized here,
// so the whole ConnectClientModel suite runs without a core, a socket, or a
// client config file anywhere on disk.

import Foundation
@testable import MCPProxy

/// A scripted `ConnectClientDataSource`.
///
/// Each queue is consumed front-to-back; when it runs dry the last element
/// repeats, so a test only scripts the transitions it cares about.
final class FakeConnectSource: ConnectClientDataSource, @unchecked Sendable {

    // MARK: Scripted responses

    var transportKind: APIClient.TransportKind = .unixSocket
    var clientsResults: [Result<[APIClient.ClientStatus], Error>] = [.success([])]
    var detailResults: [Result<APIClient.ClientStatus, Error>] = []
    var previewResults: [Result<ConnectPreviewModel, Error>] = []
    var connectResults: [Result<APIClient.ConnectResult, Error>] = []
    var undoResults: [Result<APIClient.ConnectResult, Error>] = []
    var disconnectResults: [Result<APIClient.ConnectResult, Error>] = []

    /// Run on the main actor while a connect is in flight — the only way to
    /// observe the model's in-flight state from outside without a real clock.
    var whileConnectInFlight: (@MainActor @Sendable () -> Void)?

    // MARK: Recorded calls

    struct ConnectCall: Equatable {
        let clientId: String
        let serverName: String
        let force: Bool
        let preconditionToken: String?
    }

    private(set) var clientsCallCount = 0
    private(set) var detailCalls: [String] = []
    private(set) var previewCalls: [(clientId: String, serverName: String)] = []
    private(set) var connectCalls: [ConnectCall] = []
    private(set) var undoCalls: [(clientId: String, backupName: String?)] = []
    private(set) var disconnectCalls: [String] = []

    // MARK: ConnectClientDataSource

    func connectClients() async throws -> [APIClient.ClientStatus] {
        clientsCallCount += 1
        return try next(&clientsResults, fallback: [])
    }

    func clientDetail(_ clientId: String) async throws -> APIClient.ClientStatus {
        detailCalls.append(clientId)
        return try next(&detailResults, fallback: FakeConnectSource.client(id: clientId))
    }

    func connectPreview(_ clientId: String, serverName: String) async throws -> ConnectPreviewModel {
        previewCalls.append((clientId, serverName))
        return try next(&previewResults, fallback: FakeConnectSource.preview(serverName: serverName))
    }

    func connect(
        _ clientId: String,
        serverName: String,
        force: Bool,
        preconditionToken: String?
    ) async throws -> APIClient.ConnectResult {
        connectCalls.append(
            ConnectCall(clientId: clientId, serverName: serverName,
                        force: force, preconditionToken: preconditionToken))
        if let hook = whileConnectInFlight {
            await MainActor.run { hook() }
        }
        return try next(&connectResults, fallback: FakeConnectSource.result(action: "created"))
    }

    func undoConnect(
        _ clientId: String,
        serverName: String,
        backupName: String?
    ) async throws -> APIClient.ConnectResult {
        undoCalls.append((clientId, backupName))
        return try next(&undoResults, fallback: FakeConnectSource.result(action: "restored"))
    }

    func disconnect(_ clientId: String, serverName: String) async throws -> APIClient.ConnectResult {
        disconnectCalls.append(clientId)
        return try next(&disconnectResults, fallback: FakeConnectSource.result(action: "removed"))
    }

    private func next<T>(_ queue: inout [Result<T, Error>], fallback: @autoclosure () -> T) throws -> T {
        guard !queue.isEmpty else { return fallback() }
        let result = queue.count == 1 ? queue[0] : queue.removeFirst()
        return try result.get()
    }

    // MARK: Fixtures

    static func client(
        id: String,
        name: String? = nil,
        exists: Bool = true,
        connected: Bool = false,
        supported: Bool = true,
        reason: String? = nil,
        accessState: ConnectAccessState? = nil,
        remediation: String? = nil,
        serverName: String? = nil
    ) -> APIClient.ClientStatus {
        let json: [String: Any?] = [
            "id": id,
            "name": name ?? id.capitalized,
            "config_path": "/Users/x/.\(id)/config.json",
            "exists": exists,
            "connected": connected,
            "supported": supported,
            "reason": reason,
            "icon": id,
            "server_name": serverName,
            "access_state": accessState?.rawValue,
            "remediation": remediation
        ]
        let data = try! JSONSerialization.data(
            withJSONObject: json.compactMapValues { $0 })
        return try! JSONDecoder().decode(APIClient.ClientStatus.self, from: data)
    }

    static func preview(
        client: String = "claude-code",
        serverName: String = "mcpproxy",
        entryExists: Bool = false,
        accessState: ConnectAccessState = .accessible,
        containsAPIKey: Bool = false,
        summary: ConnectEntrySummary? = nil,
        token: String? = "tok",
        refusal: String? = nil
    ) -> ConnectPreviewModel {
        ConnectPreviewModel(
            client: client,
            configPath: "/Users/x/.\(client)/config.json",
            serverName: serverName,
            entryText: "\"\(serverName)\": { \"type\": \"http\" }",
            entryExists: entryExists,
            containsAPIKey: containsAPIKey,
            accessState: accessState,
            existingEntrySummary: summary,
            preconditionToken: token,
            connectRefusal: refusal
        )
    }

    static func result(
        action: String,
        backupPath: String? = nil,
        message: String = "done"
    ) -> APIClient.ConnectResult {
        var json: [String: Any] = [
            "success": true,
            "client": "claude-code",
            "config_path": "/Users/x/.claude-code/config.json",
            "server_name": "mcpproxy",
            "action": action,
            "message": message
        ]
        if let backupPath { json["backup_path"] = backupPath }
        let data = try! JSONSerialization.data(withJSONObject: json)
        return try! JSONDecoder().decode(APIClient.ConnectResult.self, from: data)
    }
}
