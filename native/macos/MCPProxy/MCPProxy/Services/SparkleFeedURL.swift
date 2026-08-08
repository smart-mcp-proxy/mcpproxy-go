// SparkleFeedURL.swift
// MCPProxy
//
// Spec 092 FR-013, and open decision #4 in the decision report ("per-arch zips
// vs a universal binary").
//
// A Sparkle appcast has NO architecture selector. The release pipeline builds
// one app bundle per architecture, each carrying a per-architecture core in
// Contents/Resources/bin, and both bundles report the same
// CFBundleShortVersionString — so a single feed containing both enclosures
// would hand an Intel user the Apple-Silicon build (or the reverse), and the
// failure would arrive as a mysterious crash after a successful "update".
//
// Until a universal enclosure is built, the pipeline therefore publishes one
// feed per architecture and the tray asks for its own. That rewrite is here,
// as a pure function, so it can be tested without the framework and so the
// contract with `.github/workflows/release.yml` (which names the files) is
// stated in one readable place.

import Foundation

enum SparkleFeedURL {

    /// Base name the release pipeline suffixes: `appcast.xml` →
    /// `appcast-arm64.xml` / `appcast-amd64.xml`, and on the RC channel
    /// `appcast-beta-arm64.xml` / `appcast-beta-amd64.xml`.
    static let defaultFeedFileName = "appcast.xml"

    /// File name `.github/workflows/release.yml` and `prerelease.yml` generate
    /// for a given channel and architecture. The two pipelines are separate
    /// and write separate files; naming them in one function is what keeps the
    /// tray and CI from drifting apart silently.
    static func fileName(channel: UpdateChannel, arch: String) -> String {
        switch channel {
        case .stable: return "appcast-\(arch).xml"
        case .rc: return "appcast-beta-\(arch).xml"
        }
    }

    /// Rewrite a feed URL to the one for this channel and architecture.
    ///
    /// Only the exact default file name is rewritten. An operator who points
    /// `SUFeedURL` at something else has said what they want, and silently
    /// mangling their URL would be worse than fetching it.
    ///
    /// - Parameters:
    ///   - feedURL: the URL from Info.plist (`SUFeedURL`).
    ///   - arch: `"arm64"` or `"amd64"` (see `UpdateService.hostArchToken`).
    ///   - channel: the effective update channel. RC clients must land on the
    ///     prerelease pipeline's `appcast-beta-*` files: the stable feed never
    ///     carries an RC (that is FR-014's whole point), so an RC user pointed
    ///     at it would simply never be offered one.
    static func archSpecific(_ feedURL: String, arch: String, channel: UpdateChannel = .stable) -> String {
        guard var components = URLComponents(string: feedURL) else { return feedURL }
        let last = (components.path as NSString).lastPathComponent
        guard last == defaultFeedFileName else { return feedURL }
        let directory = (components.path as NSString).deletingLastPathComponent
        components.path = (directory as NSString).appendingPathComponent(
            fileName(channel: channel, arch: arch)
        )
        return components.string ?? feedURL
    }
}
