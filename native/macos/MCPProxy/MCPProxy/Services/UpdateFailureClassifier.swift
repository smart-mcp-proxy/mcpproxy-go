// UpdateFailureClassifier.swift
// MCPProxy
//
// Spec 095 FR-007 / FR-008 — the one place an update failure becomes a value.
//
// This file is deliberately free of Sparkle, AppKit and I/O: it maps a failure's
// STRUCTURED identity (which delegate callback fired, the error's domain/code,
// and the domains/codes of its underlying chain) onto four fixed words. Message
// text is never read — it is user data, and the stage is the only thing about a
// failure that is ever allowed to leave the machine (FR-009).
//
// Codes are from Sparkle 2.9.3's SUErrors.h, quoted here rather than imported so
// the classifier stays testable in a plain `swift test` run and so an upstream
// renumbering shows up as a failing test instead of a silent reclassification.

import Foundation

/// The closed set of update-failure stages. The raw values ARE the wire
/// vocabulary of `POST /api/v1/telemetry/update-failure`.
enum UpdateFailureStage: String, CaseIterable {
    case appcast
    case download
    case install
    case other
}

/// A failure's structured identity: everything the classifier is allowed to see.
struct UpdateFailureErrorIdentity: Equatable {

    /// One link of the `NSUnderlyingErrorKey` chain.
    struct Link: Equatable {
        let domain: String
        let code: Int
    }

    let domain: String
    let code: Int

    /// The underlying-error chain, outermost first.
    let underlying: [Link]

    init(domain: String, code: Int, underlying: [Link] = []) {
        self.domain = domain
        self.code = code
        self.underlying = underlying
    }

    /// Flatten an `NSError` and its underlying chain.
    ///
    /// The walk is depth-capped: `NSUnderlyingErrorKey` graphs are supplied by
    /// frameworks we do not control and a cycle here would hang the failure path
    /// itself.
    init(_ error: NSError, maxDepth: Int = 16) {
        var links: [Link] = []
        var current = error.userInfo[NSUnderlyingErrorKey] as? NSError
        while let next = current, links.count < maxDepth {
            links.append(Link(domain: next.domain, code: next.code))
            current = next.userInfo[NSUnderlyingErrorKey] as? NSError
        }
        self.init(domain: error.domain, code: error.code, underlying: links)
    }
}

/// Pure classification of an update-session failure.
enum UpdateFailureClassifier {

    /// Sparkle's error domain, by value (see the file header).
    static let sparkleErrorDomain = "SUSparkleErrorDomain"

    /// Outcomes that are not failures at all: "you are up to date", and the two
    /// ways a user declines an installation (FR-008).
    private static let excludedCodes: Set<Int> = [1001, 4007, 4008]

    /// Feed fetch / parse / configuration.
    private static let appcastCodes: Set<Int> = [1000, 1002, 1004, 3, 4]

    /// Enclosure download.
    private static let downloadCodes: Set<Int> = [2000, 2001]

    /// Extraction, signature/validation, staging, installation, relaunch.
    private static let installCodes: Set<Int> = [
        3000, 3001, 3002,
        4000, 4001, 4002, 4003, 4004, 4005, 4006, 4009, 4010, 4012,
    ]

    /// Map one failure onto its stage, or `nil` when it is not an occurrence.
    ///
    /// - Parameters:
    ///   - downloadProvenance: whether `failedToDownloadUpdate` fired for this
    ///     session. It outranks the abort code (a download failure is often
    ///     re-reported with a generic abort), but not the exclusions.
    ///   - identity: the error's domain/code plus its underlying chain.
    static func classify(
        downloadProvenance: Bool,
        identity: UpdateFailureErrorIdentity
    ) -> UpdateFailureStage? {
        let sparkleCode = firstSparkleCode(in: identity)

        if let sparkleCode, excludedCodes.contains(sparkleCode) {
            return nil
        }
        if downloadProvenance {
            return .download
        }
        guard let sparkleCode else {
            return .other
        }
        if appcastCodes.contains(sparkleCode) { return .appcast }
        if downloadCodes.contains(sparkleCode) { return .download }
        if installCodes.contains(sparkleCode) { return .install }
        return .other
    }

    /// The first Sparkle code in the error, then its underlying chain — which is
    /// how a Sparkle failure wrapped in a URL/Cocoa error still classifies.
    private static func firstSparkleCode(in identity: UpdateFailureErrorIdentity) -> Int? {
        if identity.domain == sparkleErrorDomain {
            return identity.code
        }
        return identity.underlying.first { $0.domain == sparkleErrorDomain }?.code
    }
}
