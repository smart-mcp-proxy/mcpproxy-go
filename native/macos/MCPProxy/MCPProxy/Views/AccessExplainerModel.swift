// AccessExplainerModel.swift
// MCPProxy
//
// Spec 108-k (K19, FR-046): the state behind the "Explain access" sheet. One
// subject (a client, a token, a profile or an anonymous caller) and one
// `server:tool`; the answer is the ordered chain of steps, a verdict and a list
// of fixes. A fix NAVIGATES (it opens the place where the fix is made); it never
// mutates anything itself.

import Foundation

/// Whose access is being explained. One case, so "exactly one subject" holds by
/// construction.
enum ExplainerSubject: Equatable {
    case client(String)
    case token(String)
    case profile(String)
    case anonymous

    var kindLabel: String {
        switch self {
        case .client: return "Client"
        case .token: return "Token"
        case .profile: return "Profile"
        case .anonymous: return "Anonymous"
        }
    }

    var name: String? {
        switch self {
        case .client(let v), .token(let v), .profile(let v): return v
        case .anonymous: return nil
        }
    }

    var query: APIClient.ExplainSubjectQuery {
        switch self {
        case .client(let id): return .client(id)
        case .token(let name): return .token(name)
        case .profile(let name): return .profile(name)
        case .anonymous: return .anonymous
        }
    }
}

/// The narrow API surface of the sheet; `APIClient` implements it, a test
/// supplies a stub.
protocol AccessExplainSource: Sendable {
    func explain(tool: String, subject: APIClient.ExplainSubjectQuery) async throws -> AccessExplanation
    /// Every `server:tool` for the autocomplete (`GET /tools`).
    func allToolNames() async throws -> [String]
}

extension APIClient: AccessExplainSource {
    func allToolNames() async throws -> [String] {
        try await allTools().map { "\($0.serverName ?? ""):\($0.name)" }
    }
}

@MainActor
final class AccessExplainerModel: ObservableObject {
    @Published var subject: ExplainerSubject
    @Published var tool: String
    @Published private(set) var toolNames: [String] = []
    @Published private(set) var explanation: AccessExplanation?
    /// The tool the current explanation was computed for; `tool` is live text
    /// the operator may keep editing, so fix routes use this one.
    private(set) var explainedTool: String?
    /// A 400, 403 or 404 (or any failure), shown inline; never an alert.
    @Published private(set) var errorMessage: String?
    @Published private(set) var isLoading = false

    private let source: AccessExplainSource

    init(source: AccessExplainSource, subject: ExplainerSubject = .anonymous, tool: String = "") {
        self.source = source
        self.subject = subject
        self.tool = tool
    }

    /// `server:tool` with both parts: the endpoint answers upstream tools only.
    var toolIsValid: Bool {
        let parts = tool.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
        return parts.count == 2 && !parts[0].isEmpty && !parts[1].isEmpty
    }

    var canExplain: Bool {
        guard toolIsValid, !isLoading else { return false }
        return subject.name.map { !$0.isEmpty } ?? true
    }

    /// Autocomplete: `server:tool` names containing what was typed.
    var suggestions: [String] {
        let typed = tool.trimmingCharacters(in: .whitespaces).lowercased()
        guard !typed.isEmpty else { return Array(toolNames.prefix(8)) }
        return Array(toolNames.filter { $0.lowercased().contains(typed) && $0 != tool }.prefix(8))
    }

    func loadToolNames() async {
        toolNames = ((try? await source.allToolNames()) ?? []).sorted()
    }

    func explain() async {
        guard canExplain else { return }
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }
        let asked = tool
        do {
            explanation = try await source.explain(tool: asked, subject: subject.query)
            explainedTool = asked
        } catch {
            explanation = nil
            explainedTool = nil
            errorMessage = Self.message(for: error)
        }
    }

    // MARK: - Fixes

    /// Where a fix goes (K19). A fix never mutates: it opens the editor, the
    /// row, the tokens list, the server, the review queue, Settings or the
    /// connect sheet where the operator makes the change. An action this build
    /// does not know yields nil, and the sheet shows its label without a button.
    static func route(for fix: ExplainFix, tool: String) -> AppRoute? {
        switch fix.action {
        case .allowInProfile, .classifyInProfile, .addServerToProfile:
            return .profileEditor(name: fix.target, focusTool: tool)
        case .moveClient:
            return .clientDetail(id: fix.target)
        case .editToken:
            return .clients(tab: .tokens, filter: .forToken(fix.target))
        case .enableServer:
            return .serverDetail(name: fix.target)
        case .approveTool:
            return .reviewQueue
        case .changeSetting:
            return .settings(SettingsFocus(setting: fix.target))
        case .reconnectClient:
            return .connectSheet(clientId: fix.target)
        case .upgradeAdminKeyHolders:
            return .upgradeAdminKeys
        case .unknown:
            return nil
        }
    }

    func route(for fix: ExplainFix) -> AppRoute? { Self.route(for: fix, tool: explainedTool ?? tool) }

    /// The headline of the verdict banner.
    var verdictText: String? {
        guard let explanation else { return nil }
        switch explanation.verdict {
        case .allowed: return "Allowed — the tool is visible and callable"
        case .hidden:
            return "Hidden" + failureSuffix(explanation)
        case .blocked:
            return "Blocked" + failureSuffix(explanation)
        case .unknown(let raw): return raw.isEmpty ? nil : raw
        }
    }

    private func failureSuffix(_ explanation: AccessExplanation) -> String {
        guard let step = explanation.firstFailure else { return "" }
        return " at \(step)"
    }

    private static func message(for error: Error) -> String {
        if let localized = error as? LocalizedError, let description = localized.errorDescription {
            return description
        }
        return error.localizedDescription
    }
}

extension SettingsFocus {
    /// `change_setting` names a setting key; the two this build can scroll to
    /// get their own focus, the rest open the Security tab.
    init(setting: String) {
        switch setting {
        case "anonymous_profile": self = .anonymousProfile(preselect: nil)
        case "require_mcp_auth": self = .requireMCPAuth
        default: self = .setting(setting)
        }
    }
}
