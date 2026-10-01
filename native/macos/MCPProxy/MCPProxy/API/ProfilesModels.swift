// ProfilesModels.swift
// MCPProxy
//
// Spec 108-k (FR-050): the native models of the Profiles v3 REST contract
// (specs/108-profiles-v3/contracts/rest-api.md, 108-f §2). JSON names map 1:1
// to the wire names; camelCase is the only rename.
//
// Every enum is a tolerant string enum: a value a newer core sends that this
// build has never heard of decodes into `.unknown(raw)` instead of failing the
// whole response (a list that refuses to decode is worse than a row that
// renders with a generic label). Every optional is decoded with
// `decodeIfPresent`, so an older core that omits a field never breaks a list.

import Foundation

// MARK: - Tolerant string enums

/// A string enum whose unknown values round-trip instead of throwing.
protocol TolerantStringEnum: Codable, Hashable {
    init(wire: String)
    var wire: String { get }
}

extension TolerantStringEnum {
    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        self.init(wire: try container.decode(String.self))
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(wire)
    }
}

/// What a client's connection carries (`credential_state`).
enum CredentialState: TolerantStringEnum {
    case client, adminKey, none, revoked, expired
    /// The literal `unknown` (the list never reads a config) and any value a
    /// newer core adds.
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "client": self = .client
        case "admin_key": self = .adminKey
        case "none": self = .none
        case "revoked": self = .revoked
        case "expired": self = .expired
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .client: return "client"
        case .adminKey: return "admin_key"
        case .none: return "none"
        case .revoked: return "revoked"
        case .expired: return "expired"
        case .unknown(let raw): return raw
        }
    }

    /// Only an active client credential can be rebound (FR-026).
    var canBind: Bool { self == .client }

    /// The badge text of a Clients row.
    var badgeLabel: String {
        switch self {
        case .client: return "Client credential"
        case .adminKey: return "Admin key"
        case .none: return "No credential"
        case .revoked: return "Revoked"
        case .expired: return "Expired"
        case .unknown: return "Unknown"
        }
    }
}

/// How a client credential is bound to its profile.
enum BindingMode: TolerantStringEnum {
    case locked, switchable
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "locked": self = .locked
        case "switchable": self = .switchable
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .locked: return "locked"
        case .switchable: return "switchable"
        case .unknown(let raw): return raw
        }
    }

    /// Terminology: Locked / Switchable (labels.json `binding_mode`).
    var label: String {
        switch self {
        case .locked: return "Locked"
        case .switchable: return "Switchable"
        case .unknown(let raw): return raw
        }
    }
}

/// A tool's tier (`intrinsic_tier`, `profile_tier`).
enum ToolTier: TolerantStringEnum {
    case read, write, destructive, unannotated
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "read": self = .read
        case "write": self = .write
        case "destructive": self = .destructive
        case "unannotated": self = .unannotated
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .read: return "read"
        case .write: return "write"
        case .destructive: return "destructive"
        case .unannotated: return "unannotated"
        case .unknown(let raw): return raw
        }
    }

    var label: String {
        switch self {
        case .read: return "Read"
        case .write: return "Write"
        case .destructive: return "Destructive"
        case .unannotated: return "Unannotated"
        case .unknown(let raw): return raw.isEmpty ? "—" : raw
        }
    }
}

enum WarningSeverity: TolerantStringEnum {
    case warn, info
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "warn": self = .warn
        case "info": self = .info
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .warn: return "warn"
        case .info: return "info"
        case .unknown(let raw): return raw
        }
    }

    /// The severity word (the colour is never the only signal).
    var word: String {
        switch self {
        case .warn: return "Warning"
        case .info: return "Info"
        case .unknown: return "Notice"
        }
    }

    var symbolName: String {
        switch self {
        case .warn: return "exclamationmark.triangle.fill"
        case .info: return "info.circle.fill"
        case .unknown: return "questionmark.circle.fill"
        }
    }
}

/// The action a warning's fix control performs (`action.kind`), and the
/// `fixes[].action` of an access explanation.
enum FixAction: TolerantStringEnum {
    case allowInProfile, classifyInProfile, addServerToProfile
    case moveClient, editToken, enableServer, approveTool
    case changeSetting, reconnectClient
    case upgradeAdminKeyHolders
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "allow_in_profile": self = .allowInProfile
        case "classify_in_profile": self = .classifyInProfile
        case "add_server_to_profile": self = .addServerToProfile
        case "move_client": self = .moveClient
        case "edit_token": self = .editToken
        case "enable_server": self = .enableServer
        case "approve_tool": self = .approveTool
        case "change_setting": self = .changeSetting
        case "reconnect_client": self = .reconnectClient
        case "upgrade_admin_key_holders": self = .upgradeAdminKeyHolders
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .allowInProfile: return "allow_in_profile"
        case .classifyInProfile: return "classify_in_profile"
        case .addServerToProfile: return "add_server_to_profile"
        case .moveClient: return "move_client"
        case .editToken: return "edit_token"
        case .enableServer: return "enable_server"
        case .approveTool: return "approve_tool"
        case .changeSetting: return "change_setting"
        case .reconnectClient: return "reconnect_client"
        case .upgradeAdminKeyHolders: return "upgrade_admin_key_holders"
        case .unknown(let raw): return raw
        }
    }
}

enum ExplainStatus: TolerantStringEnum {
    case pass, fail, skip
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "pass": self = .pass
        case "fail": self = .fail
        case "skip": self = .skip
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .pass: return "pass"
        case .fail: return "fail"
        case .skip: return "skip"
        case .unknown(let raw): return raw
        }
    }

    /// Symbol and word travel together: colour is never the only signal.
    var symbolName: String {
        switch self {
        case .pass: return "checkmark.circle.fill"
        case .fail: return "xmark.octagon.fill"
        case .skip: return "minus.circle"
        case .unknown: return "questionmark.circle"
        }
    }

    var word: String {
        switch self {
        case .pass: return "Pass"
        case .fail: return "Fail"
        case .skip: return "Skipped"
        case .unknown(let raw): return raw
        }
    }
}

enum ExplainVerdict: TolerantStringEnum {
    case allowed, blocked, hidden
    case unknown(String)

    init(wire: String) {
        switch wire {
        case "allowed": self = .allowed
        case "blocked": self = .blocked
        case "hidden": self = .hidden
        default: self = .unknown(wire)
        }
    }

    var wire: String {
        switch self {
        case .allowed: return "allowed"
        case .blocked: return "blocked"
        case .hidden: return "hidden"
        case .unknown(let raw): return raw
        }
    }
}

// MARK: - Profiles

struct ProfileToolRules: Codable, Equatable {
    var allow: [String]
    var deny: [String]
    var classify: [String: String]

    init(allow: [String] = [], deny: [String] = [], classify: [String: String] = [:]) {
        self.allow = allow
        self.deny = deny
        self.classify = classify
    }

    enum CodingKeys: String, CodingKey { case allow, deny, classify }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        allow = try c.decodeIfPresent([String].self, forKey: .allow) ?? []
        deny = try c.decodeIfPresent([String].self, forKey: .deny) ?? []
        classify = try c.decodeIfPresent([String: String].self, forKey: .classify) ?? [:]
    }

    var isEmpty: Bool { allow.isEmpty && deny.isEmpty && classify.isEmpty }
}

struct ToolCounts: Codable, Equatable {
    var read: Int
    var write: Int
    var destructive: Int
    var unannotatedHidden: Int

    init(read: Int = 0, write: Int = 0, destructive: Int = 0, unannotatedHidden: Int = 0) {
        self.read = read
        self.write = write
        self.destructive = destructive
        self.unannotatedHidden = unannotatedHidden
    }

    enum CodingKeys: String, CodingKey {
        case read, write, destructive
        case unannotatedHidden = "unannotated_hidden"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        read = try c.decodeIfPresent(Int.self, forKey: .read) ?? 0
        write = try c.decodeIfPresent(Int.self, forKey: .write) ?? 0
        destructive = try c.decodeIfPresent(Int.self, forKey: .destructive) ?? 0
        unannotatedHidden = try c.decodeIfPresent(Int.self, forKey: .unannotatedHidden) ?? 0
    }

    var visible: Int { read + write + destructive }
}

struct UsedByClient: Codable, Equatable {
    let id: String
    let mode: BindingMode
}

/// What points at a profile. Administrators only: a non-admin caller never
/// receives the key, so `ProfileView.usedBy == nil` means "not disclosed".
struct UsedBy: Codable, Equatable {
    var clients: [UsedByClient]
    var tokens: [String]
    var anonymousProfile: Bool

    init(clients: [UsedByClient] = [], tokens: [String] = [], anonymousProfile: Bool = false) {
        self.clients = clients
        self.tokens = tokens
        self.anonymousProfile = anonymousProfile
    }

    enum CodingKeys: String, CodingKey {
        case clients, tokens
        case anonymousProfile = "anonymous_profile"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clients = try c.decodeIfPresent([UsedByClient].self, forKey: .clients) ?? []
        tokens = try c.decodeIfPresent([String].self, forKey: .tokens) ?? []
        anonymousProfile = try c.decodeIfPresent(Bool.self, forKey: .anonymousProfile) ?? false
    }

    var isEmpty: Bool { clients.isEmpty && tokens.isEmpty && !anonymousProfile }
}

/// One profile as `GET /api/v1/profiles` returns it (replaces the v2
/// `ProfileSummary`; the v2 payload `{name, servers, tool_count}` still
/// decodes, every v3 field simply stays nil).
struct ProfileView: Codable, Identifiable, Equatable {
    var name: String
    var title: String?
    var description: String?
    var servers: [String]
    var maxTier: String?
    var unannotated: String?
    var tools: ProfileToolRules?
    var codeExecution: Bool?
    var managementTools: Bool?
    /// nil = not set (legacy); `[]` = explicitly "none" (round-trips as `[]`).
    var switchableTo: [String]?
    var effectiveServers: [String]?
    var effectiveUnannotated: String?
    var effectiveCodeExecution: Bool?
    var isLegacy: Bool?
    var toolCounts: ToolCounts?
    var toolCount: Int
    var calls24h: Int?
    var blocked24h: Int?
    var usedBy: UsedBy?

    var id: String { name }

    init(
        name: String, title: String? = nil, description: String? = nil,
        servers: [String] = [], maxTier: String? = nil, unannotated: String? = nil,
        tools: ProfileToolRules? = nil, codeExecution: Bool? = nil,
        managementTools: Bool? = nil, switchableTo: [String]? = nil,
        effectiveServers: [String]? = nil, effectiveUnannotated: String? = nil,
        effectiveCodeExecution: Bool? = nil, isLegacy: Bool? = nil,
        toolCounts: ToolCounts? = nil, toolCount: Int = 0,
        calls24h: Int? = nil, blocked24h: Int? = nil, usedBy: UsedBy? = nil
    ) {
        self.name = name
        self.title = title
        self.description = description
        self.servers = servers
        self.maxTier = maxTier
        self.unannotated = unannotated
        self.tools = tools
        self.codeExecution = codeExecution
        self.managementTools = managementTools
        self.switchableTo = switchableTo
        self.effectiveServers = effectiveServers
        self.effectiveUnannotated = effectiveUnannotated
        self.effectiveCodeExecution = effectiveCodeExecution
        self.isLegacy = isLegacy
        self.toolCounts = toolCounts
        self.toolCount = toolCount
        self.calls24h = calls24h
        self.blocked24h = blocked24h
        self.usedBy = usedBy
    }

    enum CodingKeys: String, CodingKey {
        case name, title, description, servers, unannotated, tools
        case maxTier = "max_tier"
        case codeExecution = "code_execution"
        case managementTools = "management_tools"
        case switchableTo = "switchable_to"
        case effectiveServers = "effective_servers"
        case effectiveUnannotated = "effective_unannotated"
        case effectiveCodeExecution = "effective_code_execution"
        case isLegacy = "is_legacy"
        case toolCounts = "tool_counts"
        case toolCount = "tool_count"
        case calls24h = "calls_24h"
        case blocked24h = "blocked_24h"
        case usedBy = "used_by"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        title = try c.decodeIfPresent(String.self, forKey: .title)
        description = try c.decodeIfPresent(String.self, forKey: .description)
        servers = try c.decodeIfPresent([String].self, forKey: .servers) ?? []
        maxTier = try c.decodeIfPresent(String.self, forKey: .maxTier)
        unannotated = try c.decodeIfPresent(String.self, forKey: .unannotated)
        tools = try c.decodeIfPresent(ProfileToolRules.self, forKey: .tools)
        codeExecution = try c.decodeIfPresent(Bool.self, forKey: .codeExecution)
        managementTools = try c.decodeIfPresent(Bool.self, forKey: .managementTools)
        switchableTo = try c.decodeIfPresent([String].self, forKey: .switchableTo)
        effectiveServers = try c.decodeIfPresent([String].self, forKey: .effectiveServers)
        effectiveUnannotated = try c.decodeIfPresent(String.self, forKey: .effectiveUnannotated)
        effectiveCodeExecution = try c.decodeIfPresent(Bool.self, forKey: .effectiveCodeExecution)
        isLegacy = try c.decodeIfPresent(Bool.self, forKey: .isLegacy)
        toolCounts = try c.decodeIfPresent(ToolCounts.self, forKey: .toolCounts)
        toolCount = try c.decodeIfPresent(Int.self, forKey: .toolCount) ?? 0
        calls24h = try c.decodeIfPresent(Int.self, forKey: .calls24h)
        blocked24h = try c.decodeIfPresent(Int.self, forKey: .blocked24h)
        usedBy = try c.decodeIfPresent(UsedBy.self, forKey: .usedBy)
    }

    /// The display title: the operator's title, else the slug.
    var displayTitle: String {
        if let title, !title.isEmpty { return title }
        return name
    }

    /// "Work · Read-only" style label used on rows and in the tray.
    var menuLabel: String { displayTitle }

    /// The servers the profile reaches (effective when the core computed it).
    var reachableServers: [String] { effectiveServers ?? servers }

    /// "Servers only" badge: a pre-v3 profile that sets none of the policy fields.
    var isServersOnly: Bool { isLegacy ?? false }

    /// The phrase for `max_tier`.
    var maxTierLabel: String {
        MaxTierText.label(maxTier ?? "") ?? "No cap"
    }
}

/// `GET /api/v1/profiles`.
struct ProfilesListResponse: Codable, Equatable {
    var profiles: [ProfileView]
    /// Administrators only; nil or "" means unconfined ("All servers").
    var anonymousProfile: String?

    init(profiles: [ProfileView] = [], anonymousProfile: String? = nil) {
        self.profiles = profiles
        self.anonymousProfile = anonymousProfile
    }

    enum CodingKeys: String, CodingKey {
        case profiles
        case anonymousProfile = "anonymous_profile"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profiles = try c.decodeIfPresent([ProfileView].self, forKey: .profiles) ?? []
        anonymousProfile = try c.decodeIfPresent(String.self, forKey: .anonymousProfile)
    }
}

/// How a draft says "Agent may switch to" (the tri-state of data-model §1).
enum SwitchableTo: Equatable {
    /// Omit the key: not set.
    case unset
    /// Encode `[]`: explicitly none.
    case none
    case list([String])

    /// Build from the stored `*[]string`.
    init(stored: [String]?) {
        guard let stored else { self = .unset; return }
        self = stored.isEmpty ? .none : .list(stored)
    }

    var stored: [String]? {
        switch self {
        case .unset: return nil
        case .none: return []
        case .list(let names): return names
        }
    }
}

/// The body of `POST /profiles` and `PUT /profiles/{name}` (a full replace).
///
/// A custom `Encodable` so every omission is deliberate (K4):
/// `codeExecution`/`managementTools` are omitted when nil (inherit),
/// `switchableTo` follows the tri-state, `tools` is omitted when all three
/// parts are empty, `maxTier`/`unannotated` are omitted when nil or empty.
struct ProfileConfigPayload: Encodable, Equatable {
    var name: String
    var title: String = ""
    var description: String = ""
    var servers: [String] = []
    var maxTier: String?
    var unannotated: String?
    var tools: ProfileToolRules = ProfileToolRules()
    var codeExecution: Bool?
    var managementTools: Bool?
    var switchableTo: SwitchableTo = .unset

    init(name: String) { self.name = name }

    /// An editable copy of a stored profile; saving it back is an identity.
    init(_ view: ProfileView) {
        name = view.name
        title = view.title ?? ""
        description = view.description ?? ""
        servers = view.servers
        maxTier = (view.maxTier ?? "").isEmpty ? nil : view.maxTier
        unannotated = (view.unannotated ?? "").isEmpty ? nil : view.unannotated
        tools = view.tools ?? ProfileToolRules()
        codeExecution = view.codeExecution
        managementTools = view.managementTools
        switchableTo = SwitchableTo(stored: view.switchableTo)
    }

    enum CodingKeys: String, CodingKey {
        case name, title, description, servers, unannotated, tools
        case maxTier = "max_tier"
        case codeExecution = "code_execution"
        case managementTools = "management_tools"
        case switchableTo = "switchable_to"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encode(servers, forKey: .servers)
        if !title.isEmpty { try c.encode(title, forKey: .title) }
        if !description.isEmpty { try c.encode(description, forKey: .description) }
        if let maxTier, !maxTier.isEmpty { try c.encode(maxTier, forKey: .maxTier) }
        if let unannotated, !unannotated.isEmpty { try c.encode(unannotated, forKey: .unannotated) }
        if !tools.isEmpty {
            var t = c.nestedContainer(keyedBy: ProfileToolRules.CodingKeys.self, forKey: .tools)
            if !tools.allow.isEmpty { try t.encode(tools.allow, forKey: .allow) }
            if !tools.deny.isEmpty { try t.encode(tools.deny, forKey: .deny) }
            if !tools.classify.isEmpty { try t.encode(tools.classify, forKey: .classify) }
        }
        if let codeExecution { try c.encode(codeExecution, forKey: .codeExecution) }
        if let managementTools { try c.encode(managementTools, forKey: .managementTools) }
        if let stored = switchableTo.stored { try c.encode(stored, forKey: .switchableTo) }
    }

    /// The encoded JSON object, for tests and for `tryProfile`'s nested body.
    func jsonObject() throws -> [String: Any] {
        let data = try JSONEncoder().encode(self)
        return (try JSONSerialization.jsonObject(with: data) as? [String: Any]) ?? [:]
    }
}

// MARK: - Write responses

struct ProfileWriteResponse: Decodable, Equatable {
    let profile: ProfileView
    let warnings: [String]

    enum CodingKeys: String, CodingKey { case profile, warnings }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profile = try c.decode(ProfileView.self, forKey: .profile)
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings) ?? []
    }
}

struct MovedRefs: Decodable, Equatable {
    let clients: [String]
    let tokens: [String]

    enum CodingKeys: String, CodingKey { case clients, tokens }

    init(clients: [String] = [], tokens: [String] = []) {
        self.clients = clients
        self.tokens = tokens
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clients = try c.decodeIfPresent([String].self, forKey: .clients) ?? []
        tokens = try c.decodeIfPresent([String].self, forKey: .tokens) ?? []
    }
}

struct ProfileRenameResponse: Decodable, Equatable {
    let profile: ProfileView
    let moved: MovedRefs

    enum CodingKeys: String, CodingKey { case profile, moved }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profile = try c.decode(ProfileView.self, forKey: .profile)
        moved = try c.decodeIfPresent(MovedRefs.self, forKey: .moved) ?? MovedRefs()
    }
}

struct ProfileDeleteResponse: Decodable, Equatable {
    let deleted: String
    let moved: MovedRefs
    let anonymousProfileMovedTo: String?

    enum CodingKeys: String, CodingKey {
        case deleted, moved
        case anonymousProfileMovedTo = "anonymous_profile_moved_to"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        deleted = try c.decode(String.self, forKey: .deleted)
        moved = try c.decodeIfPresent(MovedRefs.self, forKey: .moved) ?? MovedRefs()
        anonymousProfileMovedTo = try c.decodeIfPresent(String.self, forKey: .anonymousProfileMovedTo)
    }
}

// MARK: - Effective tools and Try

/// The words for an access `reason` (FR-032): the first step of the chain that
/// failed. Shared by the profile editor's table and the Tools view-as listing.
/// Spec 108-l (L6): these strings are the `access_reason` table of
/// internal/profile/testdata/contract/labels.json, the same words the Web UI
/// shows (ProfilesEnumsLabelsTests pins both).
enum AccessReasonText {
    static func label(_ reason: String) -> String {
        switch reason {
        case "": return "Visible"
        case "server_not_in_profile": return "Server not in profile"
        case "denied_by_rule": return "Denied by rule"
        case "unannotated_hidden": return "Unannotated — classify"
        case "above_tier_cap": return "Above tier cap"
        case "credential": return "Credential revoked or expired"
        case "profile": return "Profile missing — denied everything"
        case "server_in_scope": return "Server outside the token's scope"
        case "token_permission": return "Token permission"
        case "global_gate": return "Blocked by a global setting"
        case "server_state": return "Server disabled or not connected"
        case "tool_approval": return "Needs review"
        default: return reason.replacingOccurrences(of: "_", with: " ").capitalized
        }
    }
}

/// How a call's or session's profile was resolved, in words (`source` table of
/// labels.json). `none` and an unknown value carry no suffix.
enum ProfileSourceText {
    static func label(_ source: String) -> String {
        switch source {
        case "pin": return "locked by credential"
        case "binding": return "switchable"
        case "url": return "from URL"
        case "session": return "switched in session"
        case "anonymous": return "anonymous"
        default: return ""
        }
    }
}

/// One link of the access-explanation chain, in words (`explain_step` table).
enum ExplainStepText {
    static func label(_ step: String) -> String {
        switch step {
        case "credential": return "Credential"
        case "profile": return "Profile"
        case "server_in_scope": return "Server in scope"
        case "tool_rule": return "Tool rule"
        case "tier_cap": return "Tier cap"
        case "token_permission": return "Token permission"
        case "global_gate": return "Global gate"
        case "server_state": return "Server state"
        case "tool_approval": return "Tool approval"
        default:
            let words = step.replacingOccurrences(of: "_", with: " ")
            return words.prefix(1).uppercased() + words.dropFirst()
        }
    }
}

/// `unannotated` handling in words (Terminology: Hide, Treat as write, Treat as read).
enum UnannotatedText {
    static func label(_ value: String) -> String? {
        switch value {
        case "deny": return "Hide"
        case "as_write": return "Treat as write"
        case "as_read": return "Treat as read"
        default: return nil
        }
    }
}

/// `max_tier` in words (Terminology: Read, + Write, + Destructive).
enum MaxTierText {
    static func label(_ value: String) -> String? {
        switch value {
        case "read": return "Read"
        case "write": return "+ Write"
        case "destructive": return "+ Destructive"
        default: return nil
        }
    }
}

struct ToolAccess: Codable, Equatable {
    let visible: Bool
    let callable: Bool
    let reason: String

    enum CodingKeys: String, CodingKey { case visible, callable, reason }

    init(visible: Bool, callable: Bool, reason: String = "") {
        self.visible = visible
        self.callable = callable
        self.reason = reason
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        visible = try c.decodeIfPresent(Bool.self, forKey: .visible) ?? false
        callable = try c.decodeIfPresent(Bool.self, forKey: .callable) ?? false
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
    }
}

/// One row of `GET /profiles/{name}/effective-tools`.
struct EffectiveTool: Codable, Identifiable, Equatable {
    let server: String
    let tool: String
    let intrinsicTier: ToolTier
    let profileTier: ToolTier
    let access: ToolAccess
    let classificationStale: Bool

    var id: String { "\(server):\(tool)" }
    var fullName: String { "\(server):\(tool)" }

    enum CodingKeys: String, CodingKey {
        case server, tool, access
        case intrinsicTier = "intrinsic_tier"
        case profileTier = "profile_tier"
        case classificationStale = "classification_stale"
    }

    init(
        server: String, tool: String, intrinsicTier: ToolTier, profileTier: ToolTier,
        access: ToolAccess, classificationStale: Bool = false
    ) {
        self.server = server
        self.tool = tool
        self.intrinsicTier = intrinsicTier
        self.profileTier = profileTier
        self.access = access
        self.classificationStale = classificationStale
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        server = try c.decode(String.self, forKey: .server)
        tool = try c.decode(String.self, forKey: .tool)
        intrinsicTier = try c.decodeIfPresent(ToolTier.self, forKey: .intrinsicTier) ?? .unannotated
        profileTier = try c.decodeIfPresent(ToolTier.self, forKey: .profileTier) ?? intrinsicTier
        access = try c.decodeIfPresent(ToolAccess.self, forKey: .access)
            ?? ToolAccess(visible: false, callable: false)
        classificationStale = try c.decodeIfPresent(Bool.self, forKey: .classificationStale) ?? false
    }
}

struct EffectiveCounts: Decodable, Equatable {
    let visible: Int
    let hidden: Int
    let callable: Int?
    let byReason: [String: Int]?

    enum CodingKeys: String, CodingKey {
        case visible, hidden, callable
        case byReason = "by_reason"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        visible = try c.decodeIfPresent(Int.self, forKey: .visible) ?? 0
        hidden = try c.decodeIfPresent(Int.self, forKey: .hidden) ?? 0
        callable = try c.decodeIfPresent(Int.self, forKey: .callable)
        byReason = try c.decodeIfPresent([String: Int].self, forKey: .byReason)
    }
}

struct EffectiveToolsResponse: Decodable, Equatable {
    let profile: String
    let tools: [EffectiveTool]
    let counts: EffectiveCounts?
    let staleClassifications: [String]?

    enum CodingKeys: String, CodingKey {
        case profile, tools, counts
        case staleClassifications = "stale_classifications"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        profile = try c.decodeIfPresent(String.self, forKey: .profile) ?? ""
        tools = try c.decodeIfPresent([EffectiveTool].self, forKey: .tools) ?? []
        counts = try c.decodeIfPresent(EffectiveCounts.self, forKey: .counts)
        staleClassifications = try c.decodeIfPresent([String].self, forKey: .staleClassifications)
    }
}

/// One hit of `POST /profiles/try` (a `retrieve_tools` result item).
///
/// The core sends `{score, tool: {name: "server:tool", server_name,
/// description, ...}}` (108-f). A flat `{name, server_name, description}` is
/// accepted too, so a core that flattens the item keeps working.
struct TryHit: Decodable, Identifiable, Equatable {
    let name: String
    let serverName: String
    let description: String

    /// `server:tool`; the name from the core is already canonical, so it is
    /// never prefixed twice.
    var displayName: String {
        name.contains(":") || serverName.isEmpty ? name : "\(serverName):\(name)"
    }

    var id: String { displayName }

    private enum CodingKeys: String, CodingKey {
        case name, description, tool
        case serverName = "server_name"
    }

    init(from decoder: Decoder) throws {
        let outer = try decoder.container(keyedBy: CodingKeys.self)
        // The tool lives under `tool` on the wire; fall back to the item itself.
        let c = (try? outer.nestedContainer(keyedBy: CodingKeys.self, forKey: .tool)) ?? outer
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        serverName = try c.decodeIfPresent(String.self, forKey: .serverName) ?? ""
        description = try c.decodeIfPresent(String.self, forKey: .description) ?? ""
    }
}

struct TryHidden: Decodable, Equatable {
    let server: String
    let tool: String
    let reason: String
}

struct TryResponse: Decodable, Equatable {
    let results: [TryHit]
    let hiddenByProfile: Int
    let hidden: [TryHidden]
    let hiddenTruncated: Bool

    enum CodingKeys: String, CodingKey {
        case results, hidden
        case hiddenByProfile = "hidden_by_profile"
        case hiddenTruncated = "hidden_truncated"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        results = try c.decodeIfPresent([TryHit].self, forKey: .results) ?? []
        hiddenByProfile = try c.decodeIfPresent(Int.self, forKey: .hiddenByProfile) ?? 0
        hidden = try c.decodeIfPresent([TryHidden].self, forKey: .hidden) ?? []
        hiddenTruncated = try c.decodeIfPresent(Bool.self, forKey: .hiddenTruncated) ?? false
    }
}

// MARK: - Clients

struct WarningAction: Codable, Equatable {
    let kind: FixAction
    let target: String?
}

struct BindingRef: Codable, Equatable {
    let clientId: String
    let tokenName: String
    let profile: String
    let mode: BindingMode

    enum CodingKeys: String, CodingKey {
        case profile, mode
        case clientId = "client_id"
        case tokenName = "token_name"
    }

    init(clientId: String, tokenName: String = "", profile: String, mode: BindingMode) {
        self.clientId = clientId
        self.tokenName = tokenName
        self.profile = profile
        self.mode = mode
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId) ?? ""
        tokenName = try c.decodeIfPresent(String.self, forKey: .tokenName) ?? ""
        profile = try c.decodeIfPresent(String.self, forKey: .profile) ?? ""
        mode = try c.decodeIfPresent(BindingMode.self, forKey: .mode) ?? .switchable
    }
}

/// One remediation of a binding-guard refusal (`fixes[]`): `kind` is
/// `require_mcp_auth` or `set_anonymous_profile`; `target` names the profile
/// for the latter and is absent when no profile would do.
struct GuardFix: Codable, Equatable, Hashable {
    let kind: String
    let target: String?

    static let requireMCPAuth = "require_mcp_auth"
    static let setAnonymousProfile = "set_anonymous_profile"
}

/// One Clients-surface warning (`warnings[]`).
struct ClientWarning: Codable, Identifiable, Equatable {
    let code: String
    let severity: WarningSeverity
    let clientId: String?
    let message: String
    let action: WarningAction?
    let bindings: [BindingRef]?
    let fixes: [GuardFix]?

    var id: String { "\(code):\(clientId ?? ""):\(action?.target ?? "")" }

    enum CodingKeys: String, CodingKey {
        case code, severity, message, action, bindings, fixes
        case clientId = "client_id"
    }

    init(
        code: String, severity: WarningSeverity = .warn, clientId: String? = nil,
        message: String, action: WarningAction? = nil,
        bindings: [BindingRef]? = nil, fixes: [GuardFix]? = nil
    ) {
        self.code = code
        self.severity = severity
        self.clientId = clientId
        self.message = message
        self.action = action
        self.bindings = bindings
        self.fixes = fixes
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        code = try c.decode(String.self, forKey: .code)
        severity = try c.decodeIfPresent(WarningSeverity.self, forKey: .severity) ?? .warn
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId)
        message = try c.decodeIfPresent(String.self, forKey: .message) ?? ""
        action = try c.decodeIfPresent(WarningAction.self, forKey: .action)
        bindings = try c.decodeIfPresent([BindingRef].self, forKey: .bindings)
        fixes = try c.decodeIfPresent([GuardFix].self, forKey: .fixes)
    }

    static let holdsAdminKey = "client_holds_admin_key"
}

struct ClientBindingResponse: Decodable, Equatable {
    let client: ClientPresenceRecord
    let warnings: [ClientWarning]

    enum CodingKeys: String, CodingKey { case client, warnings }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        client = try c.decode(ClientPresenceRecord.self, forKey: .client)
        warnings = try c.decodeIfPresent([ClientWarning].self, forKey: .warnings) ?? []
    }
}

struct BulkAssignSkipped: Decodable, Equatable, Identifiable {
    let clientId: String
    let code: String
    let error: String?

    var id: String { clientId }

    enum CodingKeys: String, CodingKey {
        case code, error
        case clientId = "client_id"
    }

    init(clientId: String, code: String, error: String? = nil) {
        self.clientId = clientId
        self.code = code
        self.error = error
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clientId = try c.decodeIfPresent(String.self, forKey: .clientId) ?? ""
        code = try c.decodeIfPresent(String.self, forKey: .code) ?? ""
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }

    /// The plain-language reason a client was left where it is.
    var reason: String {
        switch code {
        case "no_client_credential":
            return "No client credential — connect it with a profile first"
        case "binding_bypassable_without_auth":
            return "It could escape the profile while authentication is off"
        default:
            return error ?? code
        }
    }
}

struct BulkAssignResponse: Decodable, Equatable {
    let moved: [String]
    let skipped: [BulkAssignSkipped]

    enum CodingKeys: String, CodingKey { case moved, skipped }

    init(moved: [String] = [], skipped: [BulkAssignSkipped] = []) {
        self.moved = moved
        self.skipped = skipped
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        moved = try c.decodeIfPresent([String].self, forKey: .moved) ?? []
        skipped = try c.decodeIfPresent([BulkAssignSkipped].self, forKey: .skipped) ?? []
    }
}

struct ClientSnippet: Decodable, Equatable {
    let genericHTTP: String
    let headerName: String

    enum CodingKeys: String, CodingKey {
        case genericHTTP = "generic_http"
        case headerName = "header_name"
    }
}

struct RotationState: Decodable, Equatable {
    let state: String

    var isPending: Bool { state == "pending" }
}

/// `POST /clients` (a custom client). `credential` is the full secret, shown
/// ONCE: hold it only in the sheet that displays it.
struct CustomClientResponse: Decodable {
    let client: ClientPresenceRecord
    let credential: String
    let snippet: ClientSnippet?
}

/// `POST /clients/{id}/rotate`. A supported client answers `connect`; a
/// custom one answers a fresh `credential` (once) and a pending rotation.
struct RotateResponse: Decodable {
    let client: ClientPresenceRecord
    let connect: APIClient.ConnectResult?
    let credential: String?
    let snippet: ClientSnippet?
    let rotation: RotationState
}

struct FinalizeRotationResponse: Decodable, Equatable {
    let client: ClientPresenceRecord
    let rotation: RotationState
}

struct ForgetClientResponse: Decodable, Equatable {
    let revoked: String
    let disconnected: Bool
    let disconnectError: String?

    enum CodingKeys: String, CodingKey {
        case revoked, disconnected
        case disconnectError = "disconnect_error"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        revoked = try c.decodeIfPresent(String.self, forKey: .revoked) ?? ""
        disconnected = try c.decodeIfPresent(Bool.self, forKey: .disconnected) ?? false
        disconnectError = try c.decodeIfPresent(String.self, forKey: .disconnectError)
    }
}

// MARK: - Admin-key upgrade

struct UpgradeRow: Decodable, Identifiable, Equatable {
    let clientId: String
    let displayName: String
    let displayPath: String?
    let diff: JSONValue?
    let credential: String
    let profile: String
    let mode: BindingMode
    let preconditionToken: String?
    let error: String?

    var id: String { clientId }

    enum CodingKeys: String, CodingKey {
        case diff, credential, profile, mode, error
        case clientId = "client_id"
        case displayName = "display_name"
        case displayPath = "display_path"
        case preconditionToken = "precondition_token"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        clientId = try c.decode(String.self, forKey: .clientId)
        displayName = try c.decodeIfPresent(String.self, forKey: .displayName) ?? clientId
        displayPath = try c.decodeIfPresent(String.self, forKey: .displayPath)
        diff = try c.decodeIfPresent(JSONValue.self, forKey: .diff)
        credential = try c.decodeIfPresent(String.self, forKey: .credential) ?? ""
        profile = try c.decodeIfPresent(String.self, forKey: .profile) ?? ""
        mode = try c.decodeIfPresent(BindingMode.self, forKey: .mode) ?? .switchable
        preconditionToken = try c.decodeIfPresent(String.self, forKey: .preconditionToken)
        error = try c.decodeIfPresent(String.self, forKey: .error)
    }
}

struct UpgradeGuard: Decodable, Equatable {
    let code: String
    let bindings: [BindingRef]
    let fixes: [GuardFix]

    enum CodingKeys: String, CodingKey { case code, bindings, fixes }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        code = try c.decodeIfPresent(String.self, forKey: .code) ?? ""
        bindings = try c.decodeIfPresent([BindingRef].self, forKey: .bindings) ?? []
        fixes = try c.decodeIfPresent([GuardFix].self, forKey: .fixes) ?? []
    }
}

struct UpgradePreview: Decodable, Equatable {
    let preview: [UpgradeRow]
    let preconditionToken: String?
    let guardRefusal: UpgradeGuard?
    let nextStep: String?

    enum CodingKeys: String, CodingKey {
        case preview
        case preconditionToken = "precondition_token"
        case guardRefusal = "guard"
        case nextStep = "next_step"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        preview = try c.decodeIfPresent([UpgradeRow].self, forKey: .preview) ?? []
        preconditionToken = try c.decodeIfPresent(String.self, forKey: .preconditionToken)
        guardRefusal = try c.decodeIfPresent(UpgradeGuard.self, forKey: .guardRefusal)
        nextStep = try c.decodeIfPresent(String.self, forKey: .nextStep)
    }
}

struct UpgradeFailure: Decodable, Equatable, Identifiable {
    let clientId: String
    let error: String

    var id: String { clientId }

    enum CodingKeys: String, CodingKey {
        case error
        case clientId = "client_id"
    }
}

struct UpgradeResult: Decodable, Equatable {
    let upgraded: [String]
    let failed: [UpgradeFailure]
    let nextStep: String?

    enum CodingKeys: String, CodingKey {
        case upgraded, failed
        case nextStep = "next_step"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        upgraded = try c.decodeIfPresent([String].self, forKey: .upgraded) ?? []
        failed = try c.decodeIfPresent([UpgradeFailure].self, forKey: .failed) ?? []
        nextStep = try c.decodeIfPresent(String.self, forKey: .nextStep)
    }
}

// MARK: - Access explanation

struct ExplainSubject: Codable, Equatable {
    let kind: String
    let name: String?
}

struct ExplainProfile: Codable, Equatable {
    let name: String
    let source: String
}

struct ExplainStepRow: Codable, Identifiable, Equatable {
    let step: String
    let status: ExplainStatus
    let detail: String

    var id: String { step }

    init(step: String, status: ExplainStatus, detail: String = "") {
        self.step = step
        self.status = status
        self.detail = detail
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        step = try c.decode(String.self, forKey: .step)
        status = try c.decodeIfPresent(ExplainStatus.self, forKey: .status) ?? .skip
        detail = try c.decodeIfPresent(String.self, forKey: .detail) ?? ""
    }

    enum CodingKeys: String, CodingKey { case step, status, detail }

    /// "tier_cap" → "Tier cap".
    var stepLabel: String { ExplainStepText.label(step) }
}

struct ExplainFix: Codable, Identifiable, Equatable {
    let step: String
    let action: FixAction
    let target: String
    let label: String

    var id: String { "\(action.wire):\(target)" }

    init(step: String, action: FixAction, target: String, label: String) {
        self.step = step
        self.action = action
        self.target = target
        self.label = label
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        step = try c.decodeIfPresent(String.self, forKey: .step) ?? ""
        action = try c.decode(FixAction.self, forKey: .action)
        target = try c.decodeIfPresent(String.self, forKey: .target) ?? ""
        label = try c.decodeIfPresent(String.self, forKey: .label) ?? ""
    }

    enum CodingKeys: String, CodingKey { case step, action, target, label }
}

/// `GET /api/v1/access/explain`.
struct AccessExplanation: Decodable, Equatable {
    let subject: ExplainSubject
    let tool: String
    let profile: ExplainProfile
    let steps: [ExplainStepRow]
    let verdict: ExplainVerdict
    let firstFailure: String?
    let fixes: [ExplainFix]

    enum CodingKeys: String, CodingKey {
        case subject, tool, profile, steps, verdict, fixes
        case firstFailure = "first_failure"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        subject = try c.decode(ExplainSubject.self, forKey: .subject)
        tool = try c.decodeIfPresent(String.self, forKey: .tool) ?? ""
        profile = try c.decodeIfPresent(ExplainProfile.self, forKey: .profile)
            ?? ExplainProfile(name: "", source: "")
        steps = try c.decodeIfPresent([ExplainStepRow].self, forKey: .steps) ?? []
        verdict = try c.decodeIfPresent(ExplainVerdict.self, forKey: .verdict) ?? .unknown("")
        let first = try c.decodeIfPresent(String.self, forKey: .firstFailure)
        firstFailure = (first?.isEmpty ?? true) ? nil : first
        fixes = try c.decodeIfPresent([ExplainFix].self, forKey: .fixes) ?? []
    }
}

// MARK: - Structured errors (K1)

/// The full body of a non-2xx response from a Profiles v3 route. REST errors
/// are NOT enveloped: `{success:false, error, code?, field?, used_by?, …}`.
struct ServiceErrorBody: Decodable, Equatable {
    let error: String
    let code: String?
    let field: String?
    let usedBy: UsedBy?
    let bindings: [BindingRef]?
    let fixes: [GuardFix]?
    let skipped: [BulkAssignSkipped]?
    let conflictingToken: String?
    let remediation: String?

    enum CodingKeys: String, CodingKey {
        case error, code, field, bindings, fixes, skipped, remediation
        case usedBy = "used_by"
        case conflictingToken = "conflicting_token"
    }

    init(
        error: String, code: String? = nil, field: String? = nil, usedBy: UsedBy? = nil,
        bindings: [BindingRef]? = nil, fixes: [GuardFix]? = nil,
        skipped: [BulkAssignSkipped]? = nil, conflictingToken: String? = nil,
        remediation: String? = nil
    ) {
        self.error = error
        self.code = code
        self.field = field
        self.usedBy = usedBy
        self.bindings = bindings
        self.fixes = fixes
        self.skipped = skipped
        self.conflictingToken = conflictingToken
        self.remediation = remediation
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        error = try c.decodeIfPresent(String.self, forKey: .error) ?? ""
        code = try c.decodeIfPresent(String.self, forKey: .code)
        field = try c.decodeIfPresent(String.self, forKey: .field)
        usedBy = try c.decodeIfPresent(UsedBy.self, forKey: .usedBy)
        bindings = try c.decodeIfPresent([BindingRef].self, forKey: .bindings)
        fixes = try c.decodeIfPresent([GuardFix].self, forKey: .fixes)
        skipped = try c.decodeIfPresent([BulkAssignSkipped].self, forKey: .skipped)
        conflictingToken = try c.decodeIfPresent(String.self, forKey: .conflictingToken)
        remediation = try c.decodeIfPresent(String.self, forKey: .remediation)
    }

    static let bindingBypassable = "binding_bypassable_without_auth"
    static let noClientCredential = "no_client_credential"
    static let profileInUse = "profile_in_use"
    static let profileIsAnonymous = "profile_is_anonymous_profile"
    static let preconditionFailed = "precondition_failed"

    /// A binding-guard refusal (FR-008a): it carries `bindings` and `fixes`.
    var isGuardRefusal: Bool { code == Self.bindingBypassable }
}

/// The row accounting of a non-administrator view-as listing.
struct ViewAsCounts: Codable, Equatable {
    let visible: Int
    let hidden: Int
}
