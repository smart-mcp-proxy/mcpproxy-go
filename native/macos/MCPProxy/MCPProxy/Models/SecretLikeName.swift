import Foundation

/// Shared "does this field name look like a credential?" rule (Spec 109
/// research D13), ported verbatim from `internal/secretlike/secretlike.go` /
/// `frontend/src/utils/secretLike.ts` so all three surfaces default the
/// Value/Secret toggle to Secret for the same field names (FR-065).
enum SecretLikeName {
    private static let pattern = try! NSRegularExpression(
        pattern: "(token|secret|password|passwd|api[_-]?key|[_-]key$|auth|credential|private[_-]?key)",
        options: [.caseInsensitive]
    )

    static func looksSecret(_ name: String) -> Bool {
        let range = NSRange(name.startIndex..<name.endIndex, in: name)
        return pattern.firstMatch(in: name, options: [], range: range) != nil
    }
}
