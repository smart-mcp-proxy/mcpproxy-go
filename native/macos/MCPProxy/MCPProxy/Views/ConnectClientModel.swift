import Foundation

// MARK: - Seams

/// The narrow API surface the Connect Client form depends on.
///
/// The form binds to this protocol rather than to `APIClient` so the whole state
/// machine — above all SC-002's "the Connect control cannot exist without a
/// matching rendered preview" — is testable from synthesized responses, with no
/// core, no socket and no client config file involved.
protocol ConnectClientDataSource: Sendable {
    /// Whether this client talks to the core over its private local socket. The
    /// core treats socket callers as administrative, so the mutating controls
    /// are gated on it (research D6).
    var transportKind: APIClient.TransportKind { get }

    /// `GET /api/v1/connect` — existence checks only, no config contents read.
    func connectClients() async throws -> [APIClient.ClientStatus]

    /// `GET /api/v1/connect/{id}` — the one user-initiated content read.
    func clientDetail(_ clientId: String) async throws -> APIClient.ClientStatus

    /// `GET /api/v1/connect/{id}/preview?server_name=…` — no write.
    func connectPreview(_ clientId: String, serverName: String) async throws -> ConnectPreviewModel

    /// `POST /api/v1/connect/{id}` — the only write in the happy path.
    func connect(
        _ clientId: String,
        serverName: String,
        force: Bool,
        preconditionToken: String?
    ) async throws -> APIClient.ConnectResult

    /// `POST /api/v1/connect/{id}/undo`.
    func undoConnect(
        _ clientId: String,
        serverName: String,
        backupName: String?
    ) async throws -> APIClient.ConnectResult

    /// `DELETE /api/v1/connect/{id}`.
    func disconnect(_ clientId: String, serverName: String) async throws -> APIClient.ConnectResult
}

extension APIClient: ConnectClientDataSource {}

/// How the model waits between reachability polls. Injected so tests assert the
/// interval instead of spending it.
typealias ConnectClientSleeper = @MainActor @Sendable (TimeInterval) async -> Void

// MARK: - Model

/// State machine behind the native Connect Client form (spec 091).
///
/// Everything the view renders is derived here, so the two guarantees that
/// matter are structural rather than a matter of view discipline:
///
/// 1. The Connect control EXISTS only while a preview is resolved for exactly
///    the current (client, entry name) pair, and only when the core did not
///    refuse the write and the config was readable (SC-002 / FR-003).
/// 2. A write is bound to that preview's precondition token; an overwrite sends
///    `force` only TOGETHER with the token (FR-005).
@MainActor
final class ConnectClientModel: ObservableObject {

    /// Reachability poll cadence while the core is down (FR-013).
    static let pollInterval: TimeInterval = 2

    // MARK: State

    enum ListState: Equatable {
        case loading
        /// Rows straight from the stat-only aggregate; no config was read.
        case loaded([APIClient.ClientStatus])
        /// Core unreachable; the model keeps polling and populates itself.
        case coreUnreachable(String)
    }

    enum DetailState: Equatable {
        case idle
        case loading
        case resolved(APIClient.ClientStatus)
        case failed(String)
    }

    enum PreviewState: Equatable {
        case idle
        case loading
        case resolved(ConnectPreviewModel)
        case failed(String)
    }

    enum ActionState: Equatable {
        case idle
        case inFlight
        case succeeded(APIClient.ConnectResult)
        /// A discriminated `precondition_failed` conflict: the previewed state
        /// drifted, so the form re-previews instead of retrying.
        case conflict(String)
        case failed(String)
    }

    @Published private(set) var list: ListState = .loading
    @Published private(set) var selection: String?
    @Published private(set) var detail: DetailState = .idle
    @Published private(set) var preview: PreviewState = .idle
    @Published private(set) var action: ActionState = .idle

    /// Entry name written into the client's config; the advanced override.
    ///
    /// Changing it destroys the rendered preview *immediately* — before any
    /// refetch — because the Connect control is bound to a preview of the
    /// current inputs and to nothing else.
    @Published var entryName: String = ConnectPreviewModel.defaultServerName {
        didSet {
            guard oldValue != entryName else { return }
            invalidatePreview()
        }
    }

    private let source: ConnectClientDataSource
    private let sleeper: ConnectClientSleeper

    /// Inputs the currently resolved preview was fetched for.
    private var previewKey: PreviewKey?

    private struct PreviewKey: Equatable {
        let clientId: String
        let entryName: String
    }

    init(
        source: ConnectClientDataSource,
        sleeper: @escaping ConnectClientSleeper = { interval in
            try? await Task.sleep(nanoseconds: UInt64(interval * 1_000_000_000))
        }
    ) {
        self.source = source
        self.sleeper = sleeper
    }

    // MARK: - List

    /// Load the client list, polling every `pollInterval` while the core is
    /// unreachable and populating without user action once it answers (FR-013).
    func loadList() async {
        if case .loaded = list {} else { list = .loading }
        while !Task.isCancelled {
            do {
                list = .loaded(try await source.connectClients())
                return
            } catch {
                list = .coreUnreachable(Self.message(for: error))
                await sleeper(Self.pollInterval)
            }
        }
    }

    // MARK: - Selection

    /// Select a client: resolve its authoritative state and a fresh preview.
    /// This is the only place a client config's *contents* are read, and only
    /// because the user explicitly asked for this client (FR-002).
    func select(_ clientId: String) async {
        selection = clientId
        entryName = ConnectPreviewModel.defaultServerName
        invalidatePreview()
        detail = .idle
        await refreshDetail()
        await refreshPreview()
    }

    func refreshDetail() async {
        guard let clientId = selection else { return }
        detail = .loading
        do {
            let status = try await source.clientDetail(clientId)
            guard selection == clientId else { return }
            detail = .resolved(status)
        } catch {
            guard selection == clientId else { return }
            detail = .failed(Self.message(for: error))
        }
    }

    /// Fetch the no-write preview for the current (client, entry name).
    func refreshPreview() async {
        guard let clientId = selection else { return }
        let key = PreviewKey(clientId: clientId, entryName: entryName)
        preview = .loading
        previewKey = nil
        do {
            let fetched = try await source.connectPreview(clientId, serverName: key.entryName)
            // Inputs may have moved while the request was in flight; a preview
            // for anything but the current inputs must never gate a write.
            guard selection == key.clientId, entryName == key.entryName else { return }
            guard fetched.serverName == key.entryName else {
                preview = .failed(
                    "The core previewed entry \"\(fetched.serverName)\", not \"\(key.entryName)\".")
                return
            }
            preview = .resolved(fetched)
            previewKey = key
        } catch {
            guard selection == key.clientId, entryName == key.entryName else { return }
            preview = .failed(Self.message(for: error))
        }
    }

    private func invalidatePreview() {
        preview = .idle
        previewKey = nil
        action = .idle
    }

    // MARK: - Derived

    /// The currently rendered preview, or nil when none is bound to the current
    /// inputs.
    var currentPreview: ConnectPreviewModel? {
        guard let selection,
              case .resolved(let preview) = preview,
              previewKey == PreviewKey(clientId: selection, entryName: entryName)
        else { return nil }
        return preview
    }

    /// SC-002: whether a Connect control may be rendered at all.
    ///
    /// It requires a preview resolved for exactly these inputs, a change the
    /// core did not refuse, a readable config — and, for an overwrite, a
    /// precondition token, because the form never sends `force` without one.
    var connectControlExists: Bool {
        guard let preview = currentPreview, preview.allowsConnect else { return false }
        if preview.changeKind.requiresForce && (preview.preconditionToken ?? "").isEmpty {
            return false
        }
        return true
    }

    /// Why the mutating controls are disabled, or nil when they are usable.
    /// The list, detail and preview are unaffected — off-socket the form still
    /// explains the state it cannot change.
    var mutatingDisabledReason: String? {
        guard source.transportKind != .unixSocket else { return nil }
        return "Connect, Undo and Disconnect need MCPProxy's private local socket. "
            + "This app is talking to the core over TCP, so they are unavailable."
    }

    /// A request is in flight; every action button disables (double-click
    /// protection, spec Edge Cases).
    var isBusy: Bool { action == .inFlight }

    var isConnectEnabled: Bool {
        connectControlExists && mutatingDisabledReason == nil && !isBusy
    }

    /// The core's verbatim refusal for the rendered preview, when it refused.
    var connectRefusal: String? {
        guard case .refused(let reason)? = currentPreview?.changeKind else { return nil }
        return reason
    }

    // MARK: - Connect

    /// Perform the previewed write: the preview's token always rides along, and
    /// `force` only ever together with it (FR-005).
    func connect() async {
        guard let clientId = selection,
              let preview = currentPreview,
              isConnectEnabled
        else { return }

        let entryName = self.entryName
        action = .inFlight
        do {
            let result = try await source.connect(
                clientId,
                serverName: entryName,
                force: preview.changeKind.requiresForce,
                preconditionToken: preview.preconditionToken
            )
            action = .succeeded(result)
            // The form shows the refreshed state without being reopened.
            await refreshDetail()
            await refreshPreviewPreservingAction()
        } catch let error as APIClientError {
            switch error {
            case .connectConflict(let conflictAction, let message)
                where conflictAction == Self.preconditionFailedAction:
                // The previewed state drifted: re-preview ONCE. Never retry the
                // write — that is what would loop.
                action = .conflict(message)
                await refreshPreviewPreservingAction()
            case .connectConflict(_, let message):
                // The legacy `already_exists` conflict cannot occur in this flow
                // (a replace always sends force); if it does it is a plain
                // failure, and re-previewing on it would loop forever.
                action = .failed(message)
            default:
                action = .failed(Self.message(for: error))
            }
        } catch {
            action = .failed(Self.message(for: error))
        }
    }

    /// The core's discriminator for "the state you previewed has changed".
    static let preconditionFailedAction = "precondition_failed"

    /// Re-run the preview without letting `invalidatePreview`'s action reset
    /// erase the outcome the user is being shown.
    private func refreshPreviewPreservingAction() async {
        let outcome = action
        await refreshPreview()
        action = outcome
    }

    // MARK: - Helpers

    private static func message(for error: Error) -> String {
        if let localized = error as? LocalizedError, let description = localized.errorDescription {
            return description
        }
        return error.localizedDescription
    }
}
