// ProfileEditorModel.swift
// MCPProxy
//
// Spec 108-k (K4, K7, K8): the state behind the profile editor, the rename
// sheet and the delete sheet. Everything the views show or send is derived
// here: the draft and its exact payload, the Allow/Deny/Classify edits, the
// stale-classification marker, the Try-it request (which carries the UNSAVED
// draft), how a 400 lands on a field and a 409 on the guard view, and what a
// rename or a delete moves.

import Foundation

/// The narrow API surface the editor depends on; `APIClient` conforms, a test
/// supplies a stub.
protocol ProfileEditorSource: Sendable {
    func profile(_ name: String) async throws -> ProfileView
    func createProfile(_ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse
    func updateProfile(_ name: String, _ payload: ProfileConfigPayload) async throws -> ProfileWriteResponse
    func renameProfile(_ name: String, newName: String) async throws -> ProfileRenameResponse
    func deleteProfile(_ name: String, reassignTo: String?, force: Bool) async throws -> ProfileDeleteResponse
    func effectiveTools(_ name: String, client: String?, server: String?, reason: String?) async throws -> EffectiveToolsResponse
    func tryProfile(draft: ProfileConfigPayload, query: String, limit: Int?) async throws -> TryResponse
}

extension APIClient: ProfileEditorSource {}

/// What a rename or delete touches: the clients bound to the profile, the
/// tokens pinned to it, the profiles that list it in `switchable_to`, and
/// whether it is the anonymous callers' profile. Shown BEFORE acting (K8).
struct ProfileImpact: Equatable {
    var clients: [UsedByClient]
    var tokens: [String]
    var switchableFrom: [String]
    var anonymous: Bool

    init(clients: [UsedByClient] = [], tokens: [String] = [], switchableFrom: [String] = [], anonymous: Bool = false) {
        self.clients = clients
        self.tokens = tokens
        self.switchableFrom = switchableFrom
        self.anonymous = anonymous
    }

    /// `profile.usedBy` (administrators only) plus the profiles whose
    /// `switchable_to` names it, computed from the list.
    init(profile: ProfileView, all: [ProfileView]) {
        self.init(
            clients: profile.usedBy?.clients ?? [],
            tokens: profile.usedBy?.tokens ?? [],
            switchableFrom: all.filter { $0.name != profile.name && ($0.switchableTo ?? []).contains(profile.name) }
                .map(\.name),
            anonymous: profile.usedBy?.anonymousProfile ?? false)
    }

    /// Fold in what a `409 profile_in_use` reported (the list the app held was stale).
    mutating func merge(_ usedBy: UsedBy) {
        for client in usedBy.clients where !clients.contains(client) { clients.append(client) }
        for token in usedBy.tokens where !tokens.contains(token) { tokens.append(token) }
        anonymous = anonymous || usedBy.anonymousProfile
    }

    var isEmpty: Bool { clients.isEmpty && tokens.isEmpty && switchableFrom.isEmpty && !anonymous }

    /// Whenever anything is listed the delete needs a "Move them to" target
    /// (K8, and the Web UI says the same). The core itself only insists when a
    /// pin or binding would be left dangling; asking for a target also when the
    /// profile is merely named in another profile's "Agent may switch to" keeps
    /// the two surfaces identical and costs one pick.
    var requiresTarget: Bool { !isEmpty }

    /// "Leave them without a profile" is offered only when the anonymous
    /// profile is not involved: the core never lets `force` override that.
    var canForce: Bool { !anonymous }

    /// One human line per moved thing, for the sheets.
    var lines: [String] {
        var out: [String] = []
        for client in clients {
            out.append("Client \(client.id) (\(client.mode == .locked ? "locked" : "switchable"))")
        }
        for token in tokens { out.append("Token \(token)") }
        for name in switchableFrom { out.append("Profile \(name) lists it under “Agent may switch to”") }
        if anonymous { out.append("Anonymous callers") }
        return out
    }
}

@MainActor
final class ProfileEditorModel: ObservableObject {

    /// The exact marker text of a classification whose tool gained annotations
    /// (FR-005).
    static let staleMarkerText = "classification ignored — tool is now annotated"

    // MARK: Draft

    @Published var draft: ProfileConfigPayload
    @Published private(set) var original: ProfileView?
    let isNew: Bool

    // MARK: Results

    @Published private(set) var effective: EffectiveToolsResponse?
    @Published private(set) var tryResult: TryResponse?
    @Published var tryQuery = ""
    @Published private(set) var isSaving = false
    @Published private(set) var isLoading = false
    @Published private(set) var isTrying = false
    @Published private(set) var loadError: String?
    @Published private(set) var notFound = false

    // MARK: Errors

    /// A 400 `field` and its message, shown on that field (FR-007).
    @Published private(set) var fieldError: (field: String, message: String)?
    /// A 409 guard refusal, rendered by `GuardRefusalView`.
    @Published private(set) var guardRefusal: ServiceErrorBody?
    @Published private(set) var errorMessage: String?
    /// What a `409 profile_in_use` / `profile_is_anonymous_profile` said still
    /// points at the profile, for the delete sheet to fold into its list.
    @Published private(set) var conflictUsedBy: UsedBy?
    @Published private(set) var warnings: [String] = []
    /// Set when the profile changed elsewhere while the draft has unsaved
    /// edits: "Changed elsewhere — Reload"; the draft is never overwritten (K15).
    @Published private(set) var changedElsewhere = false
    /// What the last successful action said, for a VoiceOver announcement.
    @Published private(set) var announcement: String?

    private let source: ProfileEditorSource

    init(source: ProfileEditorSource, profile: ProfileView?) {
        self.source = source
        if let profile {
            original = profile
            draft = ProfileConfigPayload(profile)
            isNew = false
        } else {
            draft = ProfileConfigPayload(name: "")
            isNew = true
        }
    }

    // MARK: Dirty state

    /// What the draft would send for the profile as it stands.
    var savedPayload: ProfileConfigPayload {
        original.map(ProfileConfigPayload.init) ?? ProfileConfigPayload(name: "")
    }

    var isDirty: Bool { draft != savedPayload }

    // MARK: Loading

    func load() async {
        guard let name = original?.name else { return }
        isLoading = true
        loadError = nil
        notFound = false
        defer { isLoading = false }
        do {
            let fresh = try await source.profile(name)
            adopt(fresh)
        } catch let APIClientError.service(status, body) where status == 404 {
            notFound = true
            loadError = body.error
        } catch {
            loadError = Self.message(for: error)
        }
        await loadEffectiveTools()
    }

    /// The saved profile changed on the server. With a clean draft the editor
    /// simply follows; with unsaved edits it only raises the marker.
    func profileChangedElsewhere(_ fresh: ProfileView) {
        guard let original else { return }
        // Only what the editor edits counts: the list row also carries live
        // stats (calls, used_by) that move without anyone editing the profile.
        guard ProfileConfigPayload(fresh) != ProfileConfigPayload(original) else { return }
        if isDirty {
            changedElsewhere = true
        } else {
            adopt(fresh)
        }
    }

    /// The human words for an `access.reason`.
    static func reasonLabel(_ reason: String) -> String { AccessReasonText.label(reason) }

    /// Discard the draft and take the saved version.
    func reload() async {
        changedElsewhere = false
        await load()
    }

    func revert() {
        draft = savedPayload
        fieldError = nil
        guardRefusal = nil
        errorMessage = nil
    }

    private func adopt(_ fresh: ProfileView) {
        original = fresh
        draft = ProfileConfigPayload(fresh)
        changedElsewhere = false
    }

    func loadEffectiveTools() async {
        guard let name = original?.name else { return }
        do {
            effective = try await source.effectiveTools(name, client: nil, server: nil, reason: nil)
        } catch {
            effective = nil
        }
    }

    // MARK: Tool rows

    enum RuleState: Equatable { case none, allow, deny }

    /// The exact-match rule on a `server:tool` row. Glob rules are listed, not
    /// toggled per row.
    func ruleState(for tool: String) -> RuleState {
        if draft.tools.deny.contains(tool) { return .deny }
        if draft.tools.allow.contains(tool) { return .allow }
        return .none
    }

    /// Allow or Deny toggles are exclusive and edit `tools.allow`/`tools.deny`;
    /// toggling the active one clears it.
    func setRule(_ state: RuleState, for tool: String) {
        draft.tools.allow.removeAll { $0 == tool }
        draft.tools.deny.removeAll { $0 == tool }
        switch state {
        case .allow: draft.tools.allow.append(tool)
        case .deny: draft.tools.deny.append(tool)
        case .none: break
        }
    }

    func toggle(_ state: RuleState, for tool: String) {
        setRule(ruleState(for: tool) == state ? .none : state, for: tool)
    }

    /// Classification applies to an UNANNOTATED tool only: an annotated tool's
    /// tier is its own, and a classification of it would be ignored.
    func canClassify(_ row: EffectiveTool) -> Bool { row.intrinsicTier == .unannotated }

    func classification(for tool: String) -> ToolTier? {
        draft.tools.classify[tool].map { ToolTier(wire: $0) }
    }

    /// Set (or, with nil, remove) the classification of an unannotated tool.
    /// Returns false when the row is not classifiable.
    @discardableResult
    func classify(_ row: EffectiveTool, as tier: ToolTier?) -> Bool {
        guard canClassify(row) || tier == nil else { return false }
        switch tier {
        case .some(let tier): draft.tools.classify[row.fullName] = tier.wire
        case .none: draft.tools.classify.removeValue(forKey: row.fullName)
        }
        return true
    }

    /// The marker shown on a row whose classification the server ignores.
    func staleMarker(for row: EffectiveTool) -> String? {
        row.classificationStale ? Self.staleMarkerText : nil
    }

    func removeClassification(_ row: EffectiveTool) { classify(row, as: nil) }

    // MARK: Try it

    /// `POST /profiles/try` with the UNSAVED draft. Nothing is persisted.
    func tryIt() async {
        let query = tryQuery.trimmingCharacters(in: .whitespaces)
        guard !query.isEmpty else { return }
        isTrying = true
        errorMessage = nil
        defer { isTrying = false }
        do {
            tryResult = try await source.tryProfile(draft: draft, query: query, limit: nil)
        } catch {
            tryResult = nil
            apply(error)
        }
    }

    // MARK: Save

    func save() async {
        isSaving = true
        clearErrors()
        defer { isSaving = false }
        do {
            let response: ProfileWriteResponse
            if isNew {
                response = try await source.createProfile(draft)
            } else {
                response = try await source.updateProfile(original?.name ?? draft.name, draft)
            }
            original = response.profile
            draft = ProfileConfigPayload(response.profile)
            warnings = response.warnings
            changedElsewhere = false
            announcement = "Saved profile \(response.profile.displayTitle)"
            await loadEffectiveTools()
        } catch {
            apply(error)
        }
    }

    /// True once a NEW profile has been saved (the view switches to edit mode).
    var hasBeenCreated: Bool { isNew && original != nil }

    private func clearErrors() {
        fieldError = nil
        guardRefusal = nil
        errorMessage = nil
        conflictUsedBy = nil
        warnings = []
    }

    /// Route a failure: a 400 with `field` to the field, a guard 409 to the
    /// guard view, everything else to the inline banner.
    private func apply(_ error: Error) {
        guard case APIClientError.service(_, let body) = error else {
            errorMessage = Self.message(for: error)
            return
        }
        if body.isGuardRefusal {
            guardRefusal = body
        } else if body.code == ServiceErrorBody.profileInUse || body.code == ServiceErrorBody.profileIsAnonymous {
            conflictUsedBy = body.usedBy
            errorMessage = body.error
        } else if let field = body.field, !field.isEmpty {
            fieldError = (field, body.error)
        } else {
            errorMessage = body.error
        }
    }

    // MARK: Rename and delete

    func impact(in all: [ProfileView]) -> ProfileImpact {
        guard let original else { return ProfileImpact() }
        return ProfileImpact(profile: original, all: all)
    }

    /// `POST /profiles/{name}/rename`. On success the model follows the new name.
    @discardableResult
    func rename(to newName: String) async -> Bool {
        guard let name = original?.name else { return false }
        isSaving = true
        clearErrors()
        defer { isSaving = false }
        do {
            let response = try await source.renameProfile(name, newName: newName)
            original = response.profile
            draft = ProfileConfigPayload(response.profile)
            announcement = "Renamed to \(response.profile.name)"
            return true
        } catch {
            apply(error)
            return false
        }
    }

    /// The profiles a delete may reassign to: every other profile. There is no
    /// "All servers" target (a locked client cannot hold an empty pin).
    static func reassignTargets(excluding name: String, in all: [ProfileView]) -> [ProfileView] {
        all.filter { $0.name != name }
    }

    /// `DELETE /profiles/{name}?reassign_to=&force=`.
    @discardableResult
    func delete(reassignTo: String?, force: Bool) async -> Bool {
        guard let name = original?.name else { return false }
        isSaving = true
        clearErrors()
        defer { isSaving = false }
        do {
            let response = try await source.deleteProfile(name, reassignTo: reassignTo, force: force)
            announcement = "Deleted profile \(response.deleted)"
            return true
        } catch {
            apply(error)
            return false
        }
    }

    private static func message(for error: Error) -> String {
        if let localized = error as? LocalizedError, let description = localized.errorDescription {
            return description
        }
        return error.localizedDescription
    }
}
