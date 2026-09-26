import Foundation

/// Swift port of `internal/secret/refname.go` `RefName` (Spec 109 FR-065),
/// shared verbatim with the Go implementation and the TS port
/// (`frontend/src/utils/secretRef.ts`) via `internal/secret/testdata/ref_names.json`
/// (see `MCPProxyTests/CatalogTests.swift`). Replaces `ServerDetailView`'s old
/// `suggestedSecretName`, which dropped the field KIND (env vs header) and so
/// let an env var and a header of the same name collide on one keyring entry.
enum SecretRefName {
    /// Kind of field a secret name is computed for. Kept as a distinct path
    /// component (rather than folded into the key) so an env var and a
    /// header of the same name never share one keyring entry.
    enum Kind: String {
        case env
        case header
    }

    private static let maxLength = 64

    /// Computes the OS-keyring entry name for one field on one server:
    /// "<server>-<kind>-<key>", lower-cased, with every run of characters
    /// outside [a-z0-9-] collapsed to a single '-', trimmed, and capped at 64
    /// characters.
    ///
    /// `taken` reports whether a candidate name is already present in the
    /// keyring (`GET /secrets/refs`); on a collision this appends -2, -3, …
    /// until it finds a free one, so an add never silently overwrites an
    /// existing secret (D28).
    static func compute(server: String, kind: Kind, key: String, taken: ((String) -> Bool)? = nil) -> String {
        let base = normalize("\(server)-\(kind.rawValue)-\(key)")
        guard let taken else { return base }

        var candidate = base
        var n = 2
        while taken(candidate) {
            let suffix = "-\(n)"
            let maxBase = maxLength - suffix.count
            let trimmedBase = base.count > maxBase ? String(base.prefix(maxBase)) : base
            candidate = trimmedBase + suffix
            n += 1
        }
        return candidate
    }

    private static let allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyz0123456789-")

    private static func normalize(_ s: String) -> String {
        let scrubbed = s.lowercased().unicodeScalars.map { allowed.contains($0) ? Character($0) : "-" }
        var collapsed = ""
        var lastWasDash = false
        for ch in scrubbed {
            if ch == "-" {
                if !lastWasDash { collapsed.append(ch) }
                lastWasDash = true
            } else {
                collapsed.append(ch)
                lastWasDash = false
            }
        }
        var trimmed = collapsed
        while trimmed.hasPrefix("-") { trimmed.removeFirst() }
        while trimmed.hasSuffix("-") { trimmed.removeLast() }
        if trimmed.count > maxLength {
            trimmed = String(trimmed.prefix(maxLength))
            while trimmed.hasSuffix("-") { trimmed.removeLast() }
        }
        return trimmed
    }
}
