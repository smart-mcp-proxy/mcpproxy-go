import Foundation

/// A Web UI deep link. The app opens the Web UI for screens it has no native
/// counterpart for (Spec 125: a server's Skills tab). The API key rides in
/// `?apikey=`, as "Open Web UI in Browser" already sends it; the Web UI moves
/// it into local storage and removes it from the address bar.
enum WebUILink {
    /// `path` is a Web UI route such as `/servers/hf?tab=skills` (the shape of
    /// an attention item's `fix.target`).
    static func url(base: String, path: String, apiKey: String) -> URL? {
        var root = base
        while root.hasSuffix("/") { root.removeLast() }
        let route = path.hasPrefix("/") ? path : "/" + path
        var text = root + "/ui" + route
        if !apiKey.isEmpty {
            var allowed = CharacterSet.alphanumerics
            allowed.insert(charactersIn: "-._~")
            let key = apiKey.addingPercentEncoding(withAllowedCharacters: allowed) ?? ""
            text += (route.contains("?") ? "&" : "?") + "apikey=" + key
        }
        return URL(string: text)
    }

    /// Percent-encodes one path segment the way the Web UI and the core do
    /// (encodeURIComponent / url.PathEscape): a `/` in a server name is %2F.
    static func pathSegment(_ value: String) -> String {
        var allowed = CharacterSet.alphanumerics
        allowed.insert(charactersIn: "-._~")
        return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
    }
}

extension Notification.Name {
    /// Open a Web UI route in the browser; `object` is the route (String).
    static let openWebUIPath = Notification.Name("MCPProxy.openWebUIPath")
}

/// Review-queue row wording (Spec 125 held skills). The sentences follow the
/// Web review queue (frontend/src/utils/reviewQueue.ts); a skill-only row adds
/// where it is reviewed, since the app has no Skills tab.
enum ReviewQueuePresentation {
    static let skillReviewKind = "skill_review"

    static func heldSkills(_ row: ReviewQueueRow) -> Int {
        (row.skillsPending ?? 0) + (row.skillsChanged ?? 0)
    }

    private static func skillsText(_ row: ReviewQueueRow) -> String {
        let total = heldSkills(row)
        guard total > 0 else { return "" }
        let changed = row.skillsChanged ?? 0
        let head = "\(total) \(total == 1 ? "skill" : "skills") awaiting review"
        return changed > 0 ? "\(head) · \(changed) changed" : head
    }

    static func subtitle(_ row: ReviewQueueRow) -> String {
        if row.kind == "server_review" { return "Server awaiting review" }
        if row.kind == skillReviewKind { return skillsText(row) + " · Review in the Web UI" }
        let tools = "\(row.pending ?? 0) new · \(row.changed ?? 0) changed"
        let skills = skillsText(row)
        return skills.isEmpty ? tools : "\(tools) · \(skills)"
    }

    /// The Web UI route a row opens instead of the native review sheet: only a
    /// skill-only row (its review is on the server's Skills tab).
    static func webUIPath(_ row: ReviewQueueRow) -> String? {
        guard row.kind == skillReviewKind else { return nil }
        return "/servers/\(WebUILink.pathSegment(row.server))?tab=skills"
    }
}
