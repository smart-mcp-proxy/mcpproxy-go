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

/// A data source that resolves the app's API client at call time.
///
/// The form can be opened before the core is up: until a client exists every
/// call throws `notReady`, which the model renders as the waiting state and
/// re-tries every two seconds, so the form populates itself the moment the core
/// answers (FR-013) — no reopening.
final class DeferredConnectSource: ConnectClientDataSource, @unchecked Sendable {

    /// The tray only ever builds socket-routed clients (`CoreProcessManager`),
    /// so the identity is known before the client exists. It is passed in rather
    /// than assumed so a TCP-configured app still disables its writes.
    let transportKind: APIClient.TransportKind

    private let resolve: @Sendable () async -> APIClient?

    init(
        transportKind: APIClient.TransportKind = .unixSocket,
        resolve: @escaping @Sendable () async -> APIClient?
    ) {
        self.transportKind = transportKind
        self.resolve = resolve
    }

    private func client() async throws -> APIClient {
        guard let client = await resolve() else { throw APIClientError.notReady }
        return client
    }

    func connectClients() async throws -> [APIClient.ClientStatus] {
        try await client().connectClients()
    }

    func clientDetail(_ clientId: String) async throws -> APIClient.ClientStatus {
        try await client().clientDetail(clientId)
    }

    func connectPreview(_ clientId: String, serverName: String) async throws -> ConnectPreviewModel {
        try await client().connectPreview(clientId, serverName: serverName)
    }

    func connect(
        _ clientId: String,
        serverName: String,
        force: Bool,
        preconditionToken: String?
    ) async throws -> APIClient.ConnectResult {
        try await client().connect(
            clientId, serverName: serverName, force: force, preconditionToken: preconditionToken)
    }

    func undoConnect(
        _ clientId: String,
        serverName: String,
        backupName: String?
    ) async throws -> APIClient.ConnectResult {
        try await client().undoConnect(clientId, serverName: serverName, backupName: backupName)
    }

    func disconnect(_ clientId: String, serverName: String) async throws -> APIClient.ConnectResult {
        try await client().disconnect(clientId, serverName: serverName)
    }
}

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

    /// The reversibility of the connect performed in THIS form instance.
    ///
    /// The core keeps no cross-session undo state — undo depends on the backup
    /// identity a connect returned — so this is deliberately session-scoped
    /// (FR-006).
    enum UndoState: Equatable {
        case unavailable
        /// `backupName` nil means the connect CREATED the file, and undo removes
        /// it; otherwise it is the bare filename of the backup to restore.
        case available(clientId: String, entryName: String, backupName: String?)
    }

    /// A disconnect the user asked for and has not yet confirmed. Nothing is
    /// sent while this is pending: the confirmation names the file and the entry
    /// first (FR-006, US3 scenario 3).
    struct DisconnectConfirmation: Equatable {
        let clientId: String
        let configPath: String
        let entryName: String

        /// The disclosure itself: there is no diff endpoint for a disconnect, so
        /// naming the file and the entry is the defined and sufficient warning.
        var message: String {
            "Remove the \"\(entryName)\" entry from \(configPath)? "
                + "The client stops seeing MCPProxy until you connect it again."
        }
    }

    /// One rendered client row (US2).
    ///
    /// Its label is the cheap truth from the existence-only aggregate until the
    /// user selects the client; from then on it is the authoritative state the
    /// core resolved. The tray never opens a client config to compute any of it.
    struct ClientRow: Equatable, Identifiable {
        let clientId: String
        let displayName: String
        let symbolName: String
        /// A statement about the config FILE (or, once resolved, the connection)
        /// — never about whether the application is installed.
        let stateLabel: String
        /// Unsupported clients stay visible but unselectable (FR-009).
        let isSelectable: Bool
        /// Extra guidance for the row: the core's remediation when access is
        /// denied, or its caveat for a supported client.
        let note: String?
        let connected: Bool

        var id: String { clientId }
    }

    @Published private(set) var list: ListState = .loading
    @Published private(set) var selection: String?
    @Published private(set) var detail: DetailState = .idle
    @Published private(set) var preview: PreviewState = .idle
    @Published private(set) var action: ActionState = .idle

    @Published private(set) var undoState: UndoState = .unavailable
    @Published private(set) var pendingDisconnect: DisconnectConfirmation?

    /// Authoritative per-client state, keyed by client id, as resolved by the
    /// explicit detail reads. Only clients the user actually selected appear
    /// here — that is what keeps opening the list content-read-free (FR-002).
    @Published private(set) var resolvedDetails: [String: APIClient.ClientStatus] = [:]

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

    /// A mutating request is outstanding.
    ///
    /// Deliberately NOT derived from `action`: this model is @MainActor, so
    /// every `await` is a reentrancy point, and the list, the entry-name field
    /// and the buttons all stay live while a POST is suspended. `action` is the
    /// OUTCOME the pane displays and gets reset whenever the preview is
    /// invalidated (a row click, a name edit) — reading busy off it made the
    /// double-submit protection disappear mid-write, which is how two
    /// concurrent read-modify-write cycles could land on the same config file.
    @Published private(set) var inFlight = false

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

    /// The selection the list may hold, given the row the user just clicked.
    ///
    /// An unsupported client is visible but is NOT a selection (FR-009) — and a
    /// click on it still moves the list's own highlight. Left there, the
    /// highlight names one client while the detail pane, the preview and an
    /// enabled Connect button all still target the previous one, so the form
    /// would write a config the user was not looking at. The view snaps the
    /// highlight back to whatever this returns.
    func listSelection(for candidate: String?) -> String? {
        guard let candidate,
              rows.first(where: { $0.clientId == candidate })?.isSelectable == true
        else { return selection }
        return candidate
    }

    /// Select a client: resolve its authoritative state and a fresh preview.
    /// This is the only place a client config's *contents* are read, and only
    /// because the user explicitly asked for this client (FR-002).
    func select(_ clientId: String) async {
        selection = clientId
        entryName = ConnectPreviewModel.defaultServerName
        invalidatePreview()
        // A confirmation the user walked away from must never survive into
        // another client's config.
        pendingDisconnect = nil
        detail = .idle
        await refreshDetail()
        await refreshPreview()
    }

    func refreshDetail() async {
        guard let clientId = selection else { return }
        await refreshDetail(for: clientId)
    }

    /// Re-read one client's authoritative state. The row keeps it whether or not
    /// that client is still the selected one — it was read for this client and
    /// remains true of it — while the detail PANE is only touched when it is.
    private func refreshDetail(for clientId: String) async {
        if selection == clientId { detail = .loading }
        do {
            let status = try await source.clientDetail(clientId)
            // The row keeps the resolved state even if the selection moved on:
            // it was read for this client and remains true of it.
            resolvedDetails[clientId] = status
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
        // Only a SETTLED outcome is cleared: a request still in flight will
        // publish its own, and erasing its marker here would hide the spinner
        // while the write is still running.
        if !inFlight { action = .idle }
    }

    // MARK: - Rows (US2)

    /// The client list as rendered: stat-only labels, overlaid with whatever the
    /// core has resolved for clients the user selected (FR-002, US2).
    var rows: [ClientRow] {
        guard case .loaded(let clients) = list else { return [] }
        return clients.map(row(for:))
    }

    private func row(for client: APIClient.ClientStatus) -> ClientRow {
        let resolved = resolvedDetails[client.clientId] ?? client
        return ClientRow(
            clientId: client.clientId,
            displayName: client.displayName,
            symbolName: client.symbolName,
            stateLabel: Self.stateLabel(for: resolved),
            isSelectable: client.supported,
            note: Self.note(for: resolved),
            connected: resolved.connected
        )
    }

    /// The row's one-line state. Order matters: platform support first (nothing
    /// else is meaningful for a client that cannot be connected here), then the
    /// access states that block a write, then the connection, then the file.
    private static func stateLabel(for client: APIClient.ClientStatus) -> String {
        if !client.supported {
            let reason = client.reason ?? ""
            return reason.isEmpty ? "Not supported on this platform" : reason
        }
        switch client.accessState {
        case .malformed:
            return "Config unreadable"
        case .denied:
            return "Access not granted"
        case .accessible, .absent, .unknown, .none:
            break
        }
        if client.connected {
            let entry = client.serverName ?? ""
            return entry.isEmpty ? "Connected" : "Connected as \"\(entry)\""
        }
        // The cheap truth: about the config FILE, not about the application.
        return client.exists ? "Config present" : "No config found"
    }

    private static func note(for client: APIClient.ClientStatus) -> String? {
        if client.accessState == .denied, let remediation = client.remediation,
           !remediation.isEmpty {
            return remediation
        }
        if let note = client.note, !note.isEmpty { return note }
        if client.supported, !client.connected, !client.exists,
           client.accessState != .malformed {
            return lookedForDescription(for: client)
        }
        return nil
    }

    /// Names the exact files behind a "No config found" verdict, so a user
    /// whose config lives at a path this build never checks (say, an
    /// opencode.jsonc against a pre-#923 core) can see the mismatch instead of
    /// guessing.
    private static func lookedForDescription(for client: APIClient.ClientStatus) -> String? {
        var paths = client.checkedPaths ?? []
        if paths.isEmpty, !client.configPath.isEmpty { paths = [client.configPath] }
        guard !paths.isEmpty else { return nil }
        let abbreviated = paths.map(abbreviatingHome)
        let dirs = Set(abbreviated.map { ($0 as NSString).deletingLastPathComponent })
        if abbreviated.count > 1, dirs.count == 1, let dir = dirs.first, !dir.isEmpty {
            let names = abbreviated.map { ($0 as NSString).lastPathComponent }
            return "Looked for \(names.joined(separator: " or ")) in \(dir)"
        }
        return "Looked for \(abbreviated.joined(separator: ", "))"
    }

    private static func abbreviatingHome(_ path: String) -> String {
        let home = NSHomeDirectory()
        guard home.count > 1, path.hasPrefix(home + "/") else { return path }
        return "~" + path.dropFirst(home.count)
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
        return !preview.replaceIsBlockedByAMissingToken
    }

    /// Why a resolved preview offers no Connect control, or nil when it does (or
    /// when the core's own refusal already explains it).
    ///
    /// A preview that renders in full with no button and nothing said about it
    /// is a dead end: the user has no way to tell a deliberate safety stop from
    /// a broken form.
    var connectBlockedReason: String? {
        guard let preview = currentPreview, preview.allowsConnect,
              preview.replaceIsBlockedByAMissingToken
        else { return nil }
        return "MCPProxy could not bind this replacement to the preview above, "
            + "so it will not overwrite the existing entry. Reopen the form or "
            + "reselect this client to get a fresh preview."
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
    var isBusy: Bool { inFlight }

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
        beginRequest()

        var outcome: ActionState
        var rePreviewOnly = false
        do {
            let result = try await source.connect(
                clientId,
                serverName: entryName,
                force: preview.changeKind.requiresForce,
                preconditionToken: preview.preconditionToken
            )
            outcome = .succeeded(result)
            // FR-006: undo becomes available for exactly this connect, carrying
            // the identity it returned — no identity means it created the file.
            undoState = .available(
                clientId: clientId,
                entryName: entryName,
                backupName: Self.backupIdentity(from: result.backupPath))
        } catch let error as APIClientError {
            switch error {
            case .connectConflict(let conflictAction, let message)
                where conflictAction == Self.preconditionFailedAction:
                // The previewed state drifted: re-preview ONCE. Never retry the
                // write — that is what would loop.
                outcome = .conflict(message)
                rePreviewOnly = true
            case .connectConflict(_, let message):
                // The legacy `already_exists` conflict cannot occur in this flow
                // (a replace always sends force); if it does it is a plain
                // failure, and re-previewing on it would loop forever.
                outcome = .failed(message)
            default:
                outcome = .failed(Self.message(for: error))
            }
        } catch {
            outcome = .failed(Self.message(for: error))
        }

        // The request is settled BEFORE the refresh: the refresh reads, it does
        // not write, so the controls are usable again while it runs.
        endRequest(with: outcome, for: clientId)

        if rePreviewOnly {
            await refreshPreviewPreservingAction(for: clientId)
        } else if case .succeeded = outcome {
            // The form shows the refreshed state without being reopened.
            await refreshAffectedClient(clientId)
        }
    }

    /// Mark a mutating request as started. `action` carries the marker for the
    /// pane's spinner; `inFlight` is what actually gates the controls.
    private func beginRequest() {
        inFlight = true
        action = .inFlight
    }

    /// Settle a mutating request. The outcome is published ONLY into the pane of
    /// the client it belongs to: the user may have moved on while it was in
    /// flight, and one client's success banner must never appear under another
    /// client's name.
    private func endRequest(with outcome: ActionState, for clientId: String) {
        inFlight = false
        guard selection == clientId else {
            if action == .inFlight { action = .idle }
            return
        }
        action = outcome
    }

    /// The core's discriminator for "the state you previewed has changed".
    static let preconditionFailedAction = "precondition_failed"

    // MARK: - Undo (session-scoped)

    /// FR-006: the undo affordance exists only for a connect performed in this
    /// open form, and only while its client is the one on screen.
    var undoControlExists: Bool {
        guard case .available(let clientId, _, _) = undoState else { return false }
        return clientId == selection
    }

    var isUndoEnabled: Bool {
        undoControlExists && mutatingDisabledReason == nil && !isBusy
    }

    /// Reverse the connect performed in this form: restore its backup, or — when
    /// it created the file — remove that file. The affordance disappears once
    /// used; there is no second undo to give.
    func undo() async {
        guard case .available(let clientId, let entry, let backupName) = undoState,
              isUndoEnabled
        else { return }

        beginRequest()
        var outcome: ActionState
        do {
            let result = try await source.undoConnect(
                clientId, serverName: entry, backupName: backupName)
            undoState = .unavailable
            outcome = .succeeded(result)
        } catch {
            // The connect stands, so the affordance stands: the user can retry.
            outcome = .failed(Self.message(for: error))
        }
        endRequest(with: outcome, for: clientId)
        if case .succeeded = outcome {
            await refreshAffectedClient(clientId)
        }
    }

    /// The core takes the BARE FILENAME of the backup (a path is a 400), while
    /// the connect result reports the full path — so the identity is reduced
    /// here, once, rather than at each call site.
    private static func backupIdentity(from backupPath: String?) -> String? {
        guard let backupPath, !backupPath.isEmpty else { return nil }
        let name = (backupPath as NSString).lastPathComponent
        return name.isEmpty ? nil : name
    }

    /// Called when the form closes: the undo's scope ends with it, and an
    /// unanswered confirmation is abandoned rather than remembered.
    func formWillClose() {
        undoState = .unavailable
        pendingDisconnect = nil
    }

    // MARK: - Disconnect

    /// The entry this client is currently registered under, when it is
    /// connected — the name a disconnect would remove.
    var connectedEntryName: String? {
        guard let clientId = selection, let resolved = resolvedDetails[clientId],
              resolved.connected
        else { return nil }
        let entry = resolved.serverName ?? ""
        return entry.isEmpty ? entryName : entry
    }

    /// Disconnect is offered for any connected client (FR-006).
    var disconnectControlExists: Bool { connectedEntryName != nil }

    var isDisconnectEnabled: Bool {
        disconnectControlExists && mutatingDisabledReason == nil && !isBusy
    }

    /// Ask for the disconnect. This only raises the confirmation — the request
    /// is sent by `confirmDisconnect`, so there is no code path from the button
    /// to the write that skips the disclosure (FR-006).
    func requestDisconnect() {
        guard let clientId = selection,
              let entry = connectedEntryName,
              isDisconnectEnabled
        else { return }

        pendingDisconnect = DisconnectConfirmation(
            clientId: clientId,
            configPath: disconnectConfigPath(for: clientId),
            entryName: entry)
    }

    func cancelDisconnect() {
        pendingDisconnect = nil
    }

    /// Send the confirmed disconnect and refresh only that client (US2 sc. 3).
    func confirmDisconnect() async {
        guard let confirmation = pendingDisconnect else { return }
        pendingDisconnect = nil
        guard selection == confirmation.clientId, isDisconnectEnabled else { return }

        beginRequest()
        var outcome: ActionState
        do {
            let result = try await source.disconnect(
                confirmation.clientId, serverName: confirmation.entryName)
            outcome = .succeeded(result)
        } catch {
            outcome = .failed(Self.message(for: error))
        }
        endRequest(with: outcome, for: confirmation.clientId)
        if case .succeeded = outcome {
            await refreshAffectedClient(confirmation.clientId)
        }
    }

    /// The file the confirmation names: the core's own path for this client,
    /// never one the tray composed itself.
    private func disconnectConfigPath(for clientId: String) -> String {
        if let resolved = resolvedDetails[clientId], !resolved.configPath.isEmpty {
            return resolved.configPath
        }
        return currentPreview?.configPath ?? ""
    }

    /// After a completed action, re-read ONLY the affected client and re-run its
    /// preview, so the form shows the new truth without being reopened and
    /// without refetching (or reading) anything else (US2 scenario 3).
    ///
    /// The client is the one the action was performed on, not whatever is
    /// selected now: its row must show what the write produced even if the user
    /// has moved on — but the PREVIEW pane belongs to the current selection, so
    /// it is only re-run while that is still the same client.
    private func refreshAffectedClient(_ clientId: String) async {
        await refreshDetail(for: clientId)
        guard selection == clientId else { return }
        await refreshPreviewPreservingAction(for: clientId)
    }

    /// Re-run the preview without letting `invalidatePreview`'s action reset
    /// erase the outcome the user is being shown.
    ///
    /// The outcome is restored ONLY if nothing newer took the action's place
    /// while the refetch was in flight. Writing it back unconditionally
    /// resurrected a settled outcome over a request the user had started since
    /// (an Undo clicked during this very refresh), which both hid that
    /// request's own result and re-enabled the button that sent it.
    private func refreshPreviewPreservingAction(for clientId: String) async {
        let outcome = action
        await refreshPreview()
        guard selection == clientId, action == .idle else { return }
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
