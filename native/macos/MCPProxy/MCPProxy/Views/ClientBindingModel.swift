// ClientBindingModel.swift
// MCPProxy
//
// Spec 108-k (K9, K10, K11, K13): the logic behind the profile controls on a
// Clients row and the sheets opened from them. Pure where it can be: the state
// of the controls for a row, the warning and fix to navigation mapping, the
// bulk-move result, the admin-key upgrade flow and the one-time credential.
//
// Rules this file keeps structural rather than a matter of view discipline:
//  - A row without an ACTIVE client credential can never reach `setBinding`
//    (FR-026): the controls state says so and the model refuses.
//  - A fix button NAVIGATES; nothing here mutates config on a fix.
//  - A credential a custom client or a rotation returns lives only in the
//    sheet model that displays it, and is zeroed on dismiss. It is never put in
//    `AppState`, `UserDefaults`, the keychain or a log line.

import Foundation

/// The narrow API surface of the client controls; `APIClient` conforms, a test
/// supplies a stub.
protocol ClientBindingSource: Sendable {
    func setBinding(_ clientId: String, profile: String, mode: BindingMode?) async throws -> ClientBindingResponse
    func bulkAssign(from: String, to: String, mode: BindingMode?) async throws -> BulkAssignResponse
    func addCustomClient(id: String, displayName: String?, profile: String?, mode: BindingMode?, expiresIn: String?) async throws -> CustomClientResponse
    func rotateClient(_ id: String, preconditionToken: String?) async throws -> RotateResponse
    func finalizeRotation(_ id: String) async throws -> FinalizeRotationResponse
    func forgetClient(_ id: String, disconnect: Bool) async throws -> ForgetClientResponse
    func previewAdminKeyUpgrade(profile: String?, mode: BindingMode?) async throws -> UpgradePreview
    func applyAdminKeyUpgrade(profile: String?, mode: BindingMode?, preconditionToken: String?) async throws -> UpgradeResult
    func connectPreview(_ clientId: String, serverName: String, binding: ConnectBinding) async throws -> ConnectPreviewModel
}

extension APIClient: ClientBindingSource {}

// MARK: - Controls state of one row

/// What the profile controls of a Clients row show and allow (K9).
struct ClientBindingControlsState: Equatable {
    enum CTA: Equatable {
        /// `none`, `admin_key` or `unknown`: connect it with a client credential.
        case upgrade
        /// `revoked` or `expired`: the credential is dead; reconnect.
        case reconnect

        var title: String {
            switch self {
            case .upgrade: return "Upgrade to client credential…"
            case .reconnect: return "Reconnect…"
            }
        }
    }

    /// The picker and the lock toggle work only with an active client credential.
    let canBind: Bool
    let cta: CTA?
    /// The bound profile; "" is All servers.
    let profile: String
    let isLocked: Bool
    /// Locking All servers is meaningless (only `switchable` is valid there).
    let lockEnabled: Bool
    let lockHelp: String?
    /// `locked by credential` / `switchable`.
    let sourceLabel: String?
    let credentialBadge: String
    /// The row's compact label: `Work · Read-only · locked`; nil without a credential.
    let compactLabel: String?
    /// `Cursor profile, Work Read-only, locked` — the VoiceOver label.
    let accessibilityLabel: String

    init(_ client: ClientPresenceRecord) {
        let state = client.credentialState ?? .unknown("unknown")
        canBind = state.canBind
        switch state {
        case .client: cta = nil
        case .revoked, .expired: cta = .reconnect
        default: cta = .upgrade
        }
        profile = client.boundProfile
        isLocked = client.isLocked
        lockEnabled = canBind && !client.boundProfile.isEmpty
        lockHelp = (canBind && client.boundProfile.isEmpty) ? "Choose a profile to lock" : nil
        switch (canBind, client.profileMode) {
        case (true, .some(.locked)) where !client.boundProfile.isEmpty: sourceLabel = ProfileSourceText.label("pin")
        case (true, _): sourceLabel = ProfileSourceText.label("binding")
        default: sourceLabel = nil
        }
        credentialBadge = state.badgeLabel
        compactLabel = client.bindingLabel
        let bound = client.bindingLabel ?? "no client credential"
        accessibilityLabel = "\(client.displayName) profile, \(bound)"
    }
}

// MARK: - Warnings and fixes

enum ClientWarningNavigation {
    /// A warning's fix button (K13): `change_setting` opens Settings,
    /// `upgrade_admin_key_holders` the upgrade sheet, `reconnect_client` the
    /// connect sheet, `move_client` the row detail and `edit_token` the Agent
    /// Tokens tab filtered by the token. Never a mutation.
    static func route(for warning: ClientWarning) -> AppRoute? {
        guard let action = warning.action else { return nil }
        let target = action.target ?? ""
        switch action.kind {
        case .changeSetting: return .settings(SettingsFocus(setting: target))
        case .upgradeAdminKeyHolders: return .upgradeAdminKeys
        case .reconnectClient: return .connectSheet(clientId: target.isEmpty ? nil : target)
        case .moveClient: return .clientDetail(id: target)
        case .editToken: return .clients(tab: .tokens, filter: .forToken(target))
        default: return nil
        }
    }

    /// The button title for a warning's action.
    static func buttonTitle(for warning: ClientWarning) -> String? {
        guard let action = warning.action else { return nil }
        switch action.kind {
        case .changeSetting: return "Open Settings…"
        case .upgradeAdminKeyHolders: return "Upgrade clients…"
        case .reconnectClient: return "Reconnect…"
        case .moveClient: return "Choose a profile…"
        case .editToken: return "Show token…"
        default: return nil
        }
    }

    /// A binding-guard refusal's fixes (FR-008a): both only NAVIGATE. The
    /// Anonymous callers fix opens Settings with the picker preselected and
    /// NOT saved.
    static func route(for fix: GuardFix) -> AppRoute? {
        switch fix.kind {
        case GuardFix.requireMCPAuth: return .settings(.requireMCPAuth)
        case GuardFix.setAnonymousProfile: return .settings(.anonymousProfile(preselect: fix.target))
        default: return nil
        }
    }

    static func buttonTitle(for fix: GuardFix) -> String? {
        switch fix.kind {
        case GuardFix.requireMCPAuth:
            return "Require authentication…"
        case GuardFix.setAnonymousProfile:
            if let target = fix.target, !target.isEmpty { return "Set anonymous callers to \(target)…" }
            return "Set anonymous callers to a narrower profile…"
        default:
            return nil
        }
    }
}

// MARK: - Forget and assign results

/// What the Forget sheet says once the core answered (108-retro-mac R1): the
/// same three texts as the Web UI's `ForgetClientDialog`. A `disconnect_error`
/// means the credential IS revoked but the config entry could not be removed.
struct ForgetResult: Equatable {
    let text: String
    let isWarning: Bool

    init(_ response: ForgetClientResponse, displayName: String) {
        if let error = response.disconnectError, !error.isEmpty {
            text = "Credential revoked; the config entry could not be removed: \(error)"
            isWarning = true
        } else if response.disconnected {
            text = "Credential revoked and MCPProxy removed from \(displayName)'s config."
            isWarning = false
        } else {
            text = "Credential revoked."
            isWarning = false
        }
    }
}

/// The result of "Assign to client…" (108-retro-mac R4).
enum AssignOutcome: Equatable {
    /// The core changed the profile and/or the lock; the refreshed row.
    case changed(ClientPresenceRecord)
    /// The client was already on the profile with the wanted lock: nothing sent.
    case unchanged(ClientPresenceRecord)

    /// `Cursor is now on Work, locked.` / `Cursor is already on Work, switchable.`
    func note(displayName: String, title: String, locked: Bool) -> String {
        let mode = locked ? "locked" : "switchable"
        switch self {
        case .changed: return "\(displayName) is now on \(title), \(mode)."
        case .unchanged: return "\(displayName) is already on \(title), \(mode)."
        }
    }
}

// MARK: - Row actions

@MainActor
final class ClientBindingModel: ObservableObject {
    /// The refusal of the last binding change, for `GuardRefusalView`.
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var errorMessage: String?
    @Published private(set) var announcement: String?
    @Published private(set) var busyClientId: String?

    private let source: ClientBindingSource

    init(source: ClientBindingSource) { self.source = source }

    /// The Picker changed: `mode` is omitted so the credential keeps its own
    /// (All servers is switchable on the core's side). Returns the refreshed
    /// row. A row that cannot bind never reaches the network.
    @discardableResult
    func chooseProfile(_ client: ClientPresenceRecord, profile: String) async -> ClientPresenceRecord? {
        guard client.hasClientCredential, profile != client.boundProfile else { return nil }
        return await send(client, profile: profile, mode: nil)
    }

    /// The Locked toggle: the current profile with the wanted mode.
    @discardableResult
    func setLocked(_ client: ClientPresenceRecord, locked: Bool) async -> ClientPresenceRecord? {
        guard client.hasClientCredential, !client.boundProfile.isEmpty else { return nil }
        return await send(client, profile: client.boundProfile, mode: locked ? .locked : .switchable)
    }

    /// "Assign to client…": the profile AND the wanted lock in ONE binding
    /// change (FR-026: one service operation, one `assign` record), so it can
    /// also change only the lock of a client already on the profile. Nothing is
    /// sent when the binding already is what was asked for.
    func assign(_ client: ClientPresenceRecord, to profile: String, locked: Bool) async -> AssignOutcome? {
        guard client.hasClientCredential, !profile.isEmpty else { return nil }
        if client.boundProfile == profile && client.isLocked == locked {
            let outcome = AssignOutcome.unchanged(client)
            announcement = outcome.note(displayName: client.displayName, title: profile, locked: locked)
            return outcome
        }
        let row = await send(client, profile: profile, mode: locked ? .locked : .switchable)
        return row.map(AssignOutcome.changed)
    }

    private func send(_ client: ClientPresenceRecord, profile: String, mode: BindingMode?) async -> ClientPresenceRecord? {
        busyClientId = client.id
        guardRefusal = nil
        errorMessage = nil
        defer { busyClientId = nil }
        do {
            let response = try await source.setBinding(client.id, profile: profile, mode: mode)
            announcement = "\(client.displayName): \(response.client.bindingLabel ?? "updated")"
            return response.client
        } catch {
            route(error)
            return nil
        }
    }

    func forget(_ client: ClientPresenceRecord, disconnect: Bool) async -> ForgetClientResponse? {
        busyClientId = client.id
        guardRefusal = nil
        errorMessage = nil
        defer { busyClientId = nil }
        do {
            let response = try await source.forgetClient(client.id, disconnect: disconnect)
            announcement = ForgetResult(response, displayName: client.displayName).text
            return response
        } catch {
            route(error)
            return nil
        }
    }

    func finalizeRotation(_ client: ClientPresenceRecord) async -> ClientPresenceRecord? {
        busyClientId = client.id
        guardRefusal = nil
        errorMessage = nil
        defer { busyClientId = nil }
        do {
            let response = try await source.finalizeRotation(client.id)
            announcement = "Rotation of \(client.displayName) finalized"
            return response.client
        } catch {
            route(error)
            return nil
        }
    }

    func clearError() {
        guardRefusal = nil
        errorMessage = nil
    }

    private func route(_ error: Error) {
        if case APIClientError.service(_, let body) = error, body.isGuardRefusal {
            guardRefusal = body
        } else {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }
}

// MARK: - Bulk move

@MainActor
final class BulkMoveModel: ObservableObject {
    @Published var from = ""
    @Published var to = ""
    /// nil keeps each credential's own mode.
    @Published var mode: BindingMode?
    @Published private(set) var result: BulkAssignResponse?
    @Published private(set) var errorMessage: String?
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var isRunning = false

    private let source: ClientBindingSource

    init(source: ClientBindingSource) { self.source = source }

    var canRun: Bool { from != to && !isRunning }

    func run() async {
        guard canRun else { return }
        isRunning = true
        errorMessage = nil
        guardRefusal = nil
        defer { isRunning = false }
        do {
            result = try await source.bulkAssign(from: from, to: to, mode: mode)
        } catch {
            result = nil
            if case APIClientError.service(_, let body) = error, body.isGuardRefusal {
                guardRefusal = body
            } else {
                errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// "3 clients moved, 1 left where it is": what the result sheet says.
    var summary: String? {
        guard let result else { return nil }
        let moved = "\(result.moved.count) client\(result.moved.count == 1 ? "" : "s") moved"
        guard !result.skipped.isEmpty else { return moved }
        return moved + ", \(result.skipped.count) left where \(result.skipped.count == 1 ? "it is" : "they are")"
    }
}

// MARK: - Admin-key upgrade (K10)

@MainActor
final class AdminKeyUpgradeModel: ObservableObject {
    enum Phase: Equatable {
        case idle
        case loading
        /// The combined preview; Apply is offered unless a guard refuses it.
        case preview(UpgradePreview)
        /// Nothing holds the admin key any more: only the rotate step is left.
        case nothingToUpgrade
        case applying
        case applied(UpgradeResult)
        case failed(String)
    }

    /// What a preview was computed for. Apply sends exactly this (R6).
    struct Request: Equatable {
        var profile: String?
        var mode: BindingMode?
    }

    @Published private(set) var phase: Phase = .idle
    /// nil sends no profile (All servers, switchable): the guard never has a
    /// named binding to refuse. Changing it (or `mode`) after a preview drops
    /// that preview: Apply must never upgrade to a profile nobody previewed.
    @Published var profile: String? {
        didSet { if profile != oldValue { invalidatePreview() } }
    }
    @Published var mode: BindingMode? {
        didSet { if mode != oldValue { invalidatePreview() } }
    }

    private let source: ClientBindingSource
    /// Bumped on every preview start and invalidation, so a preview that
    /// returns after the request changed is dropped.
    private var generation = 0
    private(set) var previewedRequest: Request?

    var currentRequest: Request { Request(profile: profile, mode: mode) }

    init(source: ClientBindingSource, profile: String? = nil) {
        self.source = source
        self.profile = profile
    }

    /// The preview is computed by the core; the sheet only shows it.
    func loadPreview() async {
        let request = currentRequest
        generation += 1
        let mine = generation
        previewedRequest = nil
        phase = .loading
        do {
            let preview = try await source.previewAdminKeyUpgrade(profile: request.profile, mode: request.mode)
            guard mine == generation else { return }
            previewedRequest = request
            if preview.preview.isEmpty {
                phase = .nothingToUpgrade
            } else {
                phase = .preview(preview)
            }
        } catch {
            guard mine == generation else { return }
            phase = .failed((error as? LocalizedError)?.errorDescription ?? error.localizedDescription)
        }
    }

    private func invalidatePreview() {
        generation += 1
        previewedRequest = nil
        switch phase {
        case .preview, .failed, .loading: phase = .idle
        default: break
        }
    }

    /// A guard in the preview means Apply would be refused.
    var guardRefusal: UpgradeGuard? {
        if case .preview(let preview) = phase { return preview.guardRefusal }
        return nil
    }

    /// Apply is offered only for a non-empty preview that no guard refuses.
    var canApply: Bool {
        guard case .preview(let preview) = phase, previewedRequest == currentRequest else { return false }
        return !preview.preview.isEmpty && preview.guardRefusal == nil
    }

    /// Apply sends the preview's combined `precondition_token`.
    func apply() async {
        guard canApply, case .preview(let preview) = phase, let sent = previewedRequest else { return }
        phase = .applying
        do {
            let result = try await source.applyAdminKeyUpgrade(
                profile: sent.profile, mode: sent.mode, preconditionToken: preview.preconditionToken)
            phase = .applied(result)
        } catch {
            if case APIClientError.service(_, let body) = error, body.code == ServiceErrorBody.preconditionFailed {
                // The holders changed since the preview: show the fresh one.
                await loadPreview()
                return
            }
            phase = .failed((error as? LocalizedError)?.errorDescription ?? error.localizedDescription)
        }
    }

    /// The rotate-the-admin-key panel: shown after an apply (once no supported
    /// row still holds the key) or when there was nothing to upgrade. The sheet
    /// never rotates the key itself.
    var showsRotatePanel: Bool {
        switch phase {
        case .nothingToUpgrade: return true
        case .applied(let result): return result.nextStep == "rotate_admin_api_key"
        case .preview(let preview): return preview.preview.isEmpty && preview.nextStep == "rotate_admin_api_key"
        default: return false
        }
    }

    /// The two steps of the rotate panel (the documented procedure: set a new
    /// `api_key` and restart), and the page that documents it.
    static let rotateSteps = [
        "Set a new `api_key` in ~/.mcpproxy/mcp_config.json (leave it empty to have MCPProxy generate one).",
        "Restart MCPProxy, then update anything that still uses the old key, such as scripts.",
    ]
    static let rotateDocsURL = "https://docs.mcpproxy.app/configuration"
}

// MARK: - Other client (K11) and the one-time credential

/// "Other client…": a custom client id, its profile, mode and expiry. The
/// credential the core returns is shown ONCE and lives only here.
@MainActor
final class CustomClientModel: ObservableObject {
    static let expiryChoices: [(label: String, value: String)] = [
        ("30 days", "30d"), ("90 days", "90d"), ("180 days", "180d"), ("365 days", "365d"),
    ]
    /// The id rule of FR-021, shown as help; the core's 400 text is shown
    /// inline when it disagrees.
    static let idHelp = "Lowercase letters, digits, “-” and “_”; starts with a letter or digit; up to 56 characters."

    @Published var id = ""
    @Published var displayName = ""
    @Published var profile = ""
    @Published var locked = true
    @Published var expiresIn = "365d"

    @Published private(set) var idError: String?
    @Published private(set) var errorMessage: String?
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var isCreating = false
    /// The FULL credential, once. Zeroed by `dismiss()`.
    @Published private(set) var credential: String?
    @Published private(set) var snippet: String?
    @Published private(set) var createdClient: ClientPresenceRecord?

    private let source: ClientBindingSource

    init(source: ClientBindingSource) { self.source = source }

    /// `mode` default: locked when a profile is chosen, switchable for All servers.
    var effectiveMode: BindingMode { profile.isEmpty ? .switchable : (locked ? .locked : .switchable) }

    var canCreate: Bool { !id.trimmingCharacters(in: .whitespaces).isEmpty && !isCreating && credential == nil }

    func create() async {
        guard canCreate else { return }
        isCreating = true
        idError = nil
        errorMessage = nil
        guardRefusal = nil
        defer { isCreating = false }
        do {
            let response = try await source.addCustomClient(
                id: id.trimmingCharacters(in: .whitespaces),
                displayName: displayName.trimmingCharacters(in: .whitespaces),
                profile: profile, mode: effectiveMode, expiresIn: expiresIn)
            credential = response.credential
            snippet = response.snippet?.genericHTTP
            createdClient = response.client
        } catch {
            if case APIClientError.service(_, let body) = error {
                if body.isGuardRefusal {
                    guardRefusal = body
                } else if body.field == "id" {
                    idError = body.error
                } else {
                    errorMessage = body.error
                }
            } else {
                errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// Wipe the secret. Called when the sheet closes, however it closes.
    func dismiss() {
        credential = nil
        snippet = nil
    }
}

// MARK: - Rotation (staged, FR-021a)

@MainActor
final class RotationModel: ObservableObject {
    enum Outcome: Equatable {
        case finalized
        case rolledBack
        /// A custom client: a new credential was issued and the old one stays
        /// valid until finalized (or 24 h).
        case pending
    }

    @Published private(set) var outcome: Outcome?
    @Published private(set) var preview: ConnectPreviewModel?
    @Published private(set) var errorMessage: String?
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var isBusy = false
    /// A custom client's new credential, shown once; zeroed by `dismiss()`.
    @Published private(set) var credential: String?
    @Published private(set) var snippet: String?

    private let source: ClientBindingSource
    let client: ClientPresenceRecord

    init(source: ClientBindingSource, client: ClientPresenceRecord) {
        self.source = source
        self.client = client
    }

    /// A supported client rotates through a connect: preview first, then the
    /// rotate is bound to the preview's precondition token. A custom client has
    /// no config file to write, so it rotates directly.
    var isSupportedClient: Bool { client.kind == "supported" }

    func loadPreview() async {
        guard isSupportedClient else { return }
        do {
            preview = try await source.connectPreview(
                client.id, serverName: ConnectPreviewModel.defaultServerName, binding: .unspecified)
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }

    func rotate() async {
        guard !isBusy else { return }
        isBusy = true
        errorMessage = nil
        defer { isBusy = false }
        do {
            let response = try await source.rotateClient(client.id, preconditionToken: preview?.preconditionToken)
            switch response.rotation.state {
            case "finalized": outcome = .finalized
            case "rolled_back": outcome = .rolledBack
            default: outcome = .pending
            }
            credential = response.credential
            snippet = response.snippet?.genericHTTP
        } catch {
            if case APIClientError.service(_, let body) = error, body.isGuardRefusal {
                guardRefusal = body
            } else {
                errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
            }
        }
    }

    /// Drop the old secret of a pending (custom) rotation.
    func finalize() async {
        guard outcome == .pending, !isBusy else { return }
        isBusy = true
        defer { isBusy = false }
        do {
            _ = try await source.finalizeRotation(client.id)
            outcome = .finalized
        } catch {
            errorMessage = (error as? LocalizedError)?.errorDescription ?? error.localizedDescription
        }
    }

    func dismiss() {
        credential = nil
        snippet = nil
    }
}
