// SemanticVersion.swift
// MCPProxy
//
// SemVer 2.0 precedence (Spec 092 FR-006), shared by every version comparison
// that drives a decision: the update nudge in `UpdateService` and the
// stale-core supersede in `CoreSupersede`.
//
// It exists because the tray's previous comparison sorted prerelease
// identifiers as plain strings, which makes `rc.10` *older* than `rc.2` — so a
// tray on an RC would have offered a downgrade, and the supersede logic built
// on top of it would have killed a newer core to start an older one. That is
// destructive, not merely cosmetic, which is why the corrected comparison is a
// type of its own with its own tests rather than a patch to one call site.

import Foundation

/// A parsed SemVer 2.0 version.
///
/// Build metadata is parsed and then DISCARDED: §10 of the spec says it must
/// be ignored when determining precedence, so `1.2.3+abc` and `1.2.3+def` are
/// the same version. Keeping it in the type would invite an `Equatable`
/// conformance that disagrees with `<` and `>`.
struct SemanticVersion: Equatable, Comparable, CustomStringConvertible {

    let major: Int
    let minor: Int
    let patch: Int

    /// Dot-separated prerelease identifiers, empty for a release version.
    let prerelease: [String]

    var isPrerelease: Bool { !prerelease.isEmpty }

    var description: String {
        let core = "\(major).\(minor).\(patch)"
        return prerelease.isEmpty ? core : core + "-" + prerelease.joined(separator: ".")
    }

    // MARK: - Parsing

    /// Parse a version string, or return nil when it is not a version.
    ///
    /// Deliberately tolerant in exactly two ways, because both appear in real
    /// payloads the tray reads:
    /// - a leading `v` (GitHub tags, `mcpproxy --version`),
    /// - a two-component core (`0.54` → `0.54.0`).
    ///
    /// Deliberately strict about everything else. `nil` is the signal that no
    /// comparison is possible, and every caller must treat it as "no decision"
    /// — a lenient parse that turns `development` into `0.0.0` would make a
    /// dev build look older than every release and invite the supersede logic
    /// to kill it.
    static func parse(_ raw: String) -> SemanticVersion? {
        var text = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }

        if text.hasPrefix("v") || text.hasPrefix("V") {
            text = String(text.dropFirst())
        }

        // §10: build metadata is ignored for precedence. Drop it before
        // anything else so `1.2.3+build.5` and `1.2.3-rc.1+build.5` both work.
        if let plus = text.firstIndex(of: "+") {
            let build = text[text.index(after: plus)...]
            // An empty or non-identifier build section makes the whole string
            // malformed rather than "the same version with junk on the end".
            guard isValidDotSeparatedIdentifiers(String(build), numericMayHaveLeadingZeros: true) else {
                return nil
            }
            text = String(text[..<plus])
        }

        // The first hyphen separates the core from the prerelease. A hyphen
        // with nothing after it ("1.2.3-") promises identifiers that are not
        // there, and the validator below rejects it — deliberately, rather
        // than silently reading it as a plain release.
        var prereleaseText: String?
        if let hyphen = text.firstIndex(of: "-") {
            prereleaseText = String(text[text.index(after: hyphen)...])
            text = String(text[..<hyphen])
        }

        let coreParts = text.split(separator: ".", omittingEmptySubsequences: false)
        guard (1...3).contains(coreParts.count) else { return nil }
        var numbers: [Int] = []
        for part in coreParts {
            guard !part.isEmpty, part.allSatisfy({ $0.isASCII && $0.isNumber }),
                  let value = Int(part) else { return nil }
            numbers.append(value)
        }
        while numbers.count < 3 { numbers.append(0) }

        var prerelease: [String] = []
        if let prereleaseText {
            // §9 bans leading zeros in a NUMERIC prerelease identifier, and the
            // ban is load-bearing here: "rc.01" and "rc.1" would otherwise be
            // two spellings of one version, and whichever way they compared,
            // one of them would be wrong. Malformed means no decision (FR-006).
            guard isValidDotSeparatedIdentifiers(prereleaseText, numericMayHaveLeadingZeros: false) else {
                return nil
            }
            prerelease = prereleaseText.split(separator: ".").map(String.init)
        }

        return SemanticVersion(major: numbers[0], minor: numbers[1], patch: numbers[2],
                               prerelease: prerelease)
    }

    /// Every identifier must be non-empty and made of ASCII alphanumerics and
    /// hyphens. `numericMayHaveLeadingZeros` is true only for build metadata,
    /// where §10 places no such restriction and precedence does not depend on
    /// it anyway; prerelease identifiers are held to §9.
    private static func isValidDotSeparatedIdentifiers(
        _ text: String, numericMayHaveLeadingZeros: Bool
    ) -> Bool {
        guard !text.isEmpty else { return false }
        let parts = text.split(separator: ".", omittingEmptySubsequences: false)
        for part in parts {
            guard !part.isEmpty else { return false }
            let ok = part.allSatisfy { ch in
                ch.isASCII && (ch.isLetter || ch.isNumber || ch == "-")
            }
            guard ok else { return false }
            if !numericMayHaveLeadingZeros,
               part.allSatisfy({ $0.isNumber }), part.count > 1, part.hasPrefix("0") {
                return false
            }
        }
        return true
    }

    // MARK: - Precedence (§11)

    static func < (lhs: SemanticVersion, rhs: SemanticVersion) -> Bool {
        compare(lhs, rhs) < 0
    }

    /// -1, 0 or +1 — the ordering the whole file exists for.
    static func compare(_ lhs: SemanticVersion, _ rhs: SemanticVersion) -> Int {
        if lhs.major != rhs.major { return lhs.major < rhs.major ? -1 : 1 }
        if lhs.minor != rhs.minor { return lhs.minor < rhs.minor ? -1 : 1 }
        if lhs.patch != rhs.patch { return lhs.patch < rhs.patch ? -1 : 1 }

        // §11.3: a version WITH a prerelease has lower precedence than the
        // matching release. 1.0.0-rc.1 < 1.0.0.
        switch (lhs.prerelease.isEmpty, rhs.prerelease.isEmpty) {
        case (true, true): return 0
        case (true, false): return 1
        case (false, true): return -1
        case (false, false): break
        }

        // §11.4: compare identifiers left to right.
        for index in 0..<min(lhs.prerelease.count, rhs.prerelease.count) {
            let a = lhs.prerelease[index]
            let b = rhs.prerelease[index]
            if a == b { continue }

            switch (isNumericIdentifier(a), isNumericIdentifier(b)) {
            case (true, true):
                // §11.4.1 — NUMERICALLY. This is the rc.10 vs rc.2 case: as
                // strings "10" < "2", as numbers 10 > 2.
                let order = compareNumericIdentifiers(a, b)
                if order != 0 { return order }
            case (true, false):
                // §11.4.3: numeric identifiers always have lower precedence.
                return -1
            case (false, true):
                return 1
            case (false, false):
                // §11.4.2: ASCII sort order.
                return a < b ? -1 : 1
            }
        }

        // §11.4.4: a larger set of fields, all else equal, has higher
        // precedence. 1.0.0-rc.1 < 1.0.0-rc.1.1.
        if lhs.prerelease.count != rhs.prerelease.count {
            return lhs.prerelease.count < rhs.prerelease.count ? -1 : 1
        }
        return 0
    }

    /// A §9 numeric identifier: ASCII digits only.
    private static func isNumericIdentifier(_ identifier: String) -> Bool {
        !identifier.isEmpty && identifier.allSatisfy { $0.isASCII && $0.isNumber }
    }

    /// Compare two numeric identifiers of ARBITRARY length.
    ///
    /// Not `Int(_:)`: a prerelease identifier is a digit string with no upper
    /// bound, and coercing it to a bounded integer either overflows to nil (so
    /// two huge identifiers fall through to an ASCII comparison that orders
    /// "10000000000000000000" below "9") or silently truncates. Leading zeros
    /// are rejected at parse time, so length-then-lexicographic IS the numeric
    /// order; they are stripped here anyway so the function is correct on its
    /// own terms.
    private static func compareNumericIdentifiers(_ lhs: String, _ rhs: String) -> Int {
        let a = Substring(lhs).drop(while: { $0 == "0" })
        let b = Substring(rhs).drop(while: { $0 == "0" })
        if a.count != b.count { return a.count < b.count ? -1 : 1 }
        if a == b { return 0 }
        return a.lexicographicallyPrecedes(b) ? -1 : 1
    }

    /// Compare two version STRINGS. Returns nil when either side is not a
    /// version — the "no decision, log a reason" case FR-006 requires. Every
    /// caller must handle nil explicitly rather than defaulting it to `0`,
    /// which is how a malformed version becomes "equal, carry on".
    static func compare(_ lhs: String, _ rhs: String) -> Int? {
        guard let a = parse(lhs), let b = parse(rhs) else { return nil }
        return compare(a, b)
    }
}
