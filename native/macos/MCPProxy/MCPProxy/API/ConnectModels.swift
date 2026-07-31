import Foundation

// MARK: - Access state

/// How readable the core found a client's config during an on-demand read
/// (Spec 075's `access_state`, consumed here by the native Connect form).
///
/// Decoding is deliberately total: a core newer than this app can name a state
/// this build has never heard of, and a client list that fails to decode is a
/// far worse outcome than one unknown row.
enum ConnectAccessState: String, Codable, Equatable {
    case accessible
    case absent
    case malformed
    case denied
    /// Anything the core reports that this build does not recognise.
    case unknown

    init(from decoder: Decoder) throws {
        let raw = try decoder.singleValueContainer().decode(String.self)
        self = ConnectAccessState(rawValue: raw) ?? .unknown
    }
}

// MARK: - Existing-entry summary

/// The sanitized, structural description of the entry a connect would replace.
///
/// Built core-side from non-secret projections ONLY (entry name, transport type,
/// query+userinfo-stripped endpoint, command, and the NAMES of headers and
/// environment variables). Nothing here is ever compared against anything — it
/// is display-only; the precondition token carries the comparison (FR-005).
struct ConnectEntrySummary: Codable, Equatable {
    let entryName: String
    let type: String?
    let endpoint: String?
    let command: String?
    let headerNames: [String]
    let envNames: [String]

    enum CodingKeys: String, CodingKey {
        case entryName = "entry_name"
        case type
        case endpoint
        case command
        case headerNames = "header_names"
        case envNames = "env_names"
    }

    init(
        entryName: String,
        type: String? = nil,
        endpoint: String? = nil,
        command: String? = nil,
        headerNames: [String] = [],
        envNames: [String] = []
    ) {
        self.entryName = entryName
        self.type = type
        self.endpoint = endpoint
        self.command = command
        self.headerNames = headerNames
        self.envNames = envNames
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        entryName = try container.decodeIfPresent(String.self, forKey: .entryName) ?? ""
        type = try container.decodeIfPresent(String.self, forKey: .type)
        endpoint = try container.decodeIfPresent(String.self, forKey: .endpoint)
        command = try container.decodeIfPresent(String.self, forKey: .command)
        // Go emits `null` for an empty slice; a nil array would force every
        // rendering site to special-case the same thing.
        headerNames = try container.decodeIfPresent([String].self, forKey: .headerNames) ?? []
        envNames = try container.decodeIfPresent([String].self, forKey: .envNames) ?? []
    }
}

// MARK: - Change classification

/// What the pending connect would do to the client's config file.
///
/// Derived from the preview alone (data-model): the form never inspects a config
/// file itself, and never re-derives this from anything it typed.
enum ConnectChangeKind: Equatable {
    /// The core would refuse the write regardless of user intent; the string is
    /// its verbatim reason (e.g. a non-create-capable client with no config).
    case refused(String)
    /// The config could not be read (malformed / denied): no pending entry, no
    /// Connect control.
    case blockedByAccess(ConnectAccessState)
    /// No config file: the write creates one, and Undo removes it.
    case create
    /// Readable config without the entry: the write adds it after a backup.
    case add
    /// The entry (possibly adopted under another key) exists and is replaced.
    case replace(entryName: String)

    /// Whether a Connect control may exist for this classification (SC-002).
    var allowsConnect: Bool {
        switch self {
        case .refused, .blockedByAccess:
            return false
        case .create, .add, .replace:
            return true
        }
    }

    /// A replace is the only kind that overwrites, so it is the only one that
    /// sends `force` — always together with the precondition token (FR-005).
    var requiresForce: Bool {
        if case .replace = self { return true }
        return false
    }
}

// MARK: - Connect preview

/// The core's no-write preview of a connect (`GET /api/v1/connect/{id}/preview`),
/// including the spec-091 additions: the sanitized existing-entry summary, the
/// opaque precondition token, and the connect refusal.
struct ConnectPreviewModel: Codable, Equatable {
    let client: String?
    let configPath: String
    let serverName: String
    let entryText: String
    let entryExists: Bool
    let containsAPIKey: Bool
    let accessState: ConnectAccessState
    let existingEntrySummary: ConnectEntrySummary?
    /// Opaque, keyed, single-session token binding this preview to the raw
    /// pre-write state AND the pending entry. Nil only against a core older than
    /// spec 091, where the write falls back to its legacy behaviour.
    let preconditionToken: String?
    /// Verbatim refusal from the same guard the write runs; presence means
    /// "Connect unavailable" (contracts §1).
    let connectRefusal: String?

    enum CodingKeys: String, CodingKey {
        case client
        case configPath = "config_path"
        case serverName = "server_name"
        case entryText = "entry_text"
        case entryExists = "entry_exists"
        case containsAPIKey = "contains_api_key"
        case accessState = "access_state"
        case existingEntrySummary = "existing_entry_summary"
        case preconditionToken = "precondition_token"
        case connectRefusal = "connect_refusal"
    }

    init(
        client: String? = nil,
        configPath: String,
        serverName: String,
        entryText: String,
        entryExists: Bool,
        containsAPIKey: Bool,
        accessState: ConnectAccessState,
        existingEntrySummary: ConnectEntrySummary? = nil,
        preconditionToken: String? = nil,
        connectRefusal: String? = nil
    ) {
        self.client = client
        self.configPath = configPath
        self.serverName = serverName
        self.entryText = entryText
        self.entryExists = entryExists
        self.containsAPIKey = containsAPIKey
        self.accessState = accessState
        self.existingEntrySummary = existingEntrySummary
        self.preconditionToken = preconditionToken
        self.connectRefusal = connectRefusal
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        client = try container.decodeIfPresent(String.self, forKey: .client)
        configPath = try container.decodeIfPresent(String.self, forKey: .configPath) ?? ""
        serverName = try container.decodeIfPresent(String.self, forKey: .serverName)
            ?? ConnectPreviewModel.defaultServerName
        entryText = try container.decodeIfPresent(String.self, forKey: .entryText) ?? ""
        entryExists = try container.decodeIfPresent(Bool.self, forKey: .entryExists) ?? false
        containsAPIKey = try container.decodeIfPresent(Bool.self, forKey: .containsAPIKey) ?? false
        accessState = try container.decodeIfPresent(ConnectAccessState.self, forKey: .accessState)
            ?? .unknown
        existingEntrySummary = try container.decodeIfPresent(
            ConnectEntrySummary.self, forKey: .existingEntrySummary)
        preconditionToken = try container.decodeIfPresent(String.self, forKey: .preconditionToken)
        // An empty string is the Go zero value for an omitted refusal; treat it
        // as "no refusal" so `connect_refusal: ""` cannot disable Connect.
        let refusal = try container.decodeIfPresent(String.self, forKey: .connectRefusal)
        connectRefusal = (refusal?.isEmpty ?? true) ? nil : refusal
    }

    /// The entry name the form defaults to, matching the core's own default.
    static let defaultServerName = "mcpproxy"

    // MARK: Derived

    /// Classification of the pending change (data-model). Refusal outranks
    /// everything: an OpenCode preview with an absent config is refused, NOT a
    /// create, or the form would promise a file it can never write.
    var changeKind: ConnectChangeKind {
        if let connectRefusal, !connectRefusal.isEmpty {
            return .refused(connectRefusal)
        }
        switch accessState {
        case .denied, .malformed:
            return .blockedByAccess(accessState)
        case .absent:
            return .create
        case .accessible, .unknown:
            break
        }
        if entryExists {
            return .replace(entryName: existingEntrySummary?.entryName.isEmpty == false
                            ? existingEntrySummary!.entryName
                            : serverName)
        }
        return .add
    }

    /// Whether a Connect control may exist for this preview (SC-002).
    var allowsConnect: Bool { changeKind.allowsConnect }

    /// The safety net the user is promised BEFORE the action button (FR-003).
    var safetyNetStatement: String? {
        switch changeKind {
        case .create:
            return "This file does not exist; it will be created, and Undo removes it."
        case .add, .replace:
            return "A timestamped backup of this file will be created alongside it."
        case .refused, .blockedByAccess:
            return nil
        }
    }

    /// Disclosure that the pending entry embeds the admin credential (FR-004).
    var credentialNotice: String? {
        guard containsAPIKey else { return nil }
        return "This entry embeds the MCPProxy API key in the client's config file."
    }
}
