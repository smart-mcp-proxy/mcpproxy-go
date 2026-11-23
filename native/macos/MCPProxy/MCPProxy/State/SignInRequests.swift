import Foundation

/// A browser sign-in request waiting for the administrator (Spec 132). The core
/// emits `oauth.authorization_pending` when an MCP client asks to sign in and
/// `GET /api/v1/oauth/authorizations` lists what is still waiting. The tray only
/// shows the request and opens the consent page in the Web UI; it never decides
/// one. `clientLabel` is text the client chose, so it is stored already cleaned.
struct SignInRequest: Equatable, Identifiable, Sendable {
    let id: String
    let clientLabel: String
    let expiresAt: Date
}

/// Decoding of the two SSE events and of the list response. Dates in the list
/// are RFC 3339 with fractional seconds, which `JSONDecoder` does not read, so
/// they stay strings until `SignInPresentation.parseDate`.
enum SignInWire {
    struct Pending: Equatable, Sendable {
        let requestID: String
        let clientLabel: String
    }

    struct Decided: Equatable, Sendable {
        let requestID: String
        let status: String
    }

    struct Summary: Decodable, Equatable, Sendable {
        let id: String
        let clientLabel: String?
        let expiresAt: String?

        enum CodingKeys: String, CodingKey {
            case id
            case clientLabel = "client_label"
            case expiresAt = "expires_at"
        }
    }

    /// Events arrive as `{"payload": {...}}`; a bare object is accepted too.
    private static func payload(_ data: String) -> [String: Any]? {
        guard let bytes = data.data(using: .utf8),
              let raw = try? JSONSerialization.jsonObject(with: bytes) as? [String: Any] else { return nil }
        return raw["payload"] as? [String: Any] ?? raw
    }

    static func decodePending(_ data: String) -> Pending? {
        guard let p = payload(data),
              let id = p["request_id"] as? String, !id.isEmpty else { return nil }
        return Pending(requestID: id, clientLabel: p["client_label"] as? String ?? "")
    }

    static func decodeDecided(_ data: String) -> Decided? {
        guard let p = payload(data),
              let id = p["request_id"] as? String, !id.isEmpty else { return nil }
        return Decided(requestID: id, status: p["status"] as? String ?? "")
    }
}

enum SignInPresentation {
    /// How long the core keeps a request (10 minutes). The pending event carries
    /// no expiry, so an event-born entry gets this from the moment it arrived;
    /// the list reload replaces it with the core's own `expires_at`.
    static let defaultLifetime: TimeInterval = 600

    /// Menu rows shown before the rest fold into one "N more" row.
    static let maxMenuRows = 5

    static let labelLimit = 40
    static let fallbackLabel = "an application"

    /// The label is untrusted: another process chose it. Control and format
    /// characters (bidi overrides, zero-width) are dropped, white space runs
    /// (newlines included) collapse to one space, the result is cut to
    /// `labelLimit` characters. The text is only ever set as a plain menu or
    /// notification string, never as markup. Blank input becomes a neutral phrase.
    static func sanitizedLabel(_ raw: String) -> String {
        var scalars = String.UnicodeScalarView()
        for scalar in raw.unicodeScalars {
            switch scalar.properties.generalCategory {
            case .control, .format, .lineSeparator, .paragraphSeparator, .surrogate, .privateUse, .unassigned:
                if scalar == "\n" || scalar == "\t" || scalar == "\r" { scalars.append(" ") }
            default:
                scalars.append(scalar)
            }
        }
        let collapsed = String(scalars).split(whereSeparator: { $0.isWhitespace }).joined(separator: " ")
        guard !collapsed.isEmpty else { return fallbackLabel }
        return GlanceFormatting.tailTruncated(collapsed, limit: labelLimit)
    }

    /// The menu rows for `store` at `now`: expired requests never appear, the
    /// first `maxMenuRows` are shown and the rest fold into a submenu.
    static func menuRows(_ store: SignInRequestStore, now: Date = Date())
        -> (shown: [SignInRequest], overflow: [SignInRequest]) {
        let live = store.live(now: now)
        return (Array(live.prefix(maxMenuRows)), Array(live.dropFirst(maxMenuRows)))
    }

    static func menuTitle(for request: SignInRequest) -> String {
        // A truncated label already ends in an ellipsis; do not stack two.
        request.clientLabel.hasSuffix("\u{2026}")
            ? "Approve sign-in for \(request.clientLabel)"
            : "Approve sign-in for \(request.clientLabel)\u{2026}"
    }

    /// The Web UI route the menu item opens; `WebUILink.url` adds `/ui` and the
    /// API key the way "Open Web UI in Browser" does.
    static func webUIPath(requestID: String) -> String {
        "/authorize?request=\(WebUILink.pathSegment(requestID))"
    }

    private static let fractional: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let plain = ISO8601DateFormatter()

    static func parseDate(_ text: String?) -> Date? {
        guard let text, !text.isEmpty else { return nil }
        return fractional.date(from: text) ?? plain.date(from: text)
    }
}

/// The pending requests, newest first. A value type so the reducer is tested
/// without AppState. Every mutation is idempotent: the same request can arrive
/// by event and by list reload.
struct SignInRequestStore: Equatable, Sendable {
    private(set) var requests: [SignInRequest] = []
    /// Increases on every change of content, so a list reload can tell that an
    /// event landed while its GET was in flight.
    private(set) var revision = 0

    var isEmpty: Bool { requests.isEmpty }

    /// Requests whose deadline has not passed.
    func live(now: Date = Date()) -> [SignInRequest] {
        requests.filter { $0.expiresAt > now }
    }

    /// Adds a request. Returns true when it was not already listed (so the
    /// caller notifies once). An already-expired request is ignored.
    @discardableResult
    mutating func add(_ request: SignInRequest, now: Date = Date()) -> Bool {
        guard request.expiresAt > now else { return false }
        if let index = requests.firstIndex(where: { $0.id == request.id }) {
            requests[index] = request
            revision += 1 // an accepted event, even if it changes nothing visible
            return false
        }
        requests.insert(request, at: 0)
        revision += 1
        return true
    }

    mutating func remove(id: String) {
        requests.removeAll { $0.id == id }
        // An accepted decision advances the revision even when the id is
        // absent (the pending event may have been missed): a reload snapshot
        // taken before it must not be applied afterwards.
        revision += 1
    }

    mutating func expire(now: Date = Date()) {
        let before = requests.count
        requests.removeAll { $0.expiresAt <= now }
        if requests.count != before { revision += 1 }
    }

    /// The core's list is authoritative: what it no longer names is gone
    /// (decided, cancelled or expired while the tray was not listening). The
    /// list is newest first, so its order is kept.
    mutating func replaceAll(with fresh: [SignInRequest], now: Date = Date()) {
        var seen = Set<String>()
        let next = fresh.filter { $0.expiresAt > now && seen.insert($0.id).inserted }
        if next != requests { requests = next; revision += 1 }
    }

    mutating func clear() {
        if !requests.isEmpty { requests = []; revision += 1 }
    }
}
