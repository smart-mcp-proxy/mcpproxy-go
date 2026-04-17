// UpdateService.swift
// MCPProxy
//
// Update checking service. Uses GitHub Releases API as a lightweight
// alternative to Sparkle when Sparkle SPM dependency is not available.
// When Sparkle IS linked, it takes precedence for the full update UX
// (download, verify, replace, relaunch).

import Foundation
import AppKit
import Darwin

// MARK: - Update Service

/// Manages software update checks.
///
/// Strategy:
/// 1. If Sparkle framework is linked → use SPUStandardUpdaterController
/// 2. Otherwise → check GitHub Releases API directly (notify only, no auto-install)
final class UpdateService: ObservableObject {

    /// Whether an update check can be performed.
    var canCheckForUpdates: Bool { true }

    /// Whether an update check is currently in progress.
    @Published private(set) var isChecking: Bool = false

    /// Latest available version (nil if current or unknown).
    @Published private(set) var latestVersion: String?

    /// URL to download the latest release.
    @Published private(set) var downloadURL: String?

    /// Release notes for the latest version.
    @Published private(set) var releaseNotes: String?

    /// Whether Sparkle framework is linked.
    private let sparkleAvailable: Bool

    /// GitHub API endpoint for latest release.
    private let githubReleaseURL = "https://api.github.com/repos/smart-mcp-proxy/mcpproxy-go/releases/latest"

    /// Current version from the core (set by AppController).
    var currentVersion: String = ""

    // MARK: - Initialization

    init() {
        self.sparkleAvailable = NSClassFromString("SPUStandardUpdaterController") != nil
    }

    // MARK: - Public API

    /// Check for updates. Uses Sparkle if available, otherwise GitHub API.
    func checkForUpdates() {
        if sparkleAvailable {
            checkWithSparkle()
        } else {
            checkWithGitHub()
        }
    }

    /// Returns the release-asset architecture token for the host machine
    /// ("arm64" on Apple Silicon, "amd64" on Intel). Rosetta-translated
    /// processes report the underlying Apple Silicon machine so the user
    /// is offered the native build.
    static func hostArchToken() -> String {
        // Detect Rosetta: Intel binary running on Apple Silicon.
        var translated: Int32 = 0
        var translatedSize = MemoryLayout<Int32>.size
        if sysctlbyname("sysctl.proc_translated", &translated, &translatedSize, nil, 0) == 0,
           translated == 1 {
            return "arm64"
        }
        // Read hw.machine — "arm64" on Apple Silicon, "x86_64" on Intel.
        var size: size_t = 0
        sysctlbyname("hw.machine", nil, &size, nil, 0)
        if size > 0 {
            var buf = [CChar](repeating: 0, count: size)
            if sysctlbyname("hw.machine", &buf, &size, nil, 0) == 0 {
                let machine = String(cString: buf)
                if machine == "arm64" || machine.hasPrefix("arm") {
                    return "arm64"
                }
            }
        }
        return "amd64"
    }

    /// Compare two semver-ish version strings (no leading "v"). Returns:
    /// - positive if `a` > `b`
    /// - negative if `a` < `b`
    /// - zero if equal or unparseable
    ///
    /// Pre-release identifiers (e.g. `1.2.3-rc.1`) sort *before* the matching
    /// release per semver §11. Anything we can't parse is treated as equal so
    /// the caller falls back to its existing behaviour.
    static func compareSemver(_ a: String, _ b: String) -> Int {
        func parse(_ s: String) -> (core: [Int], pre: String)? {
            let parts = s.split(separator: "-", maxSplits: 1, omittingEmptySubsequences: false)
            let coreParts = parts[0].split(separator: ".")
            var core: [Int] = []
            for p in coreParts {
                guard let n = Int(p) else { return nil }
                core.append(n)
            }
            while core.count < 3 { core.append(0) }
            let pre = parts.count > 1 ? String(parts[1]) : ""
            return (core, pre)
        }
        guard let pa = parse(a), let pb = parse(b) else { return 0 }
        for i in 0..<min(pa.core.count, pb.core.count) {
            if pa.core[i] != pb.core[i] { return pa.core[i] - pb.core[i] }
        }
        // Equal core: a release outranks any pre-release.
        if pa.pre.isEmpty && !pb.pre.isEmpty { return 1 }
        if !pa.pre.isEmpty && pb.pre.isEmpty { return -1 }
        if pa.pre == pb.pre { return 0 }
        return pa.pre < pb.pre ? -1 : 1
    }

    /// Open the download page in the browser.
    func openDownloadPage() {
        let urlString = downloadURL ?? "https://github.com/smart-mcp-proxy/mcpproxy-go/releases/latest"
        if let url = URL(string: urlString) {
            NSWorkspace.shared.open(url)
        }
    }

    // MARK: - Sparkle

    private func checkWithSparkle() {
        // When Sparkle is linked:
        // import Sparkle
        // let controller = SPUStandardUpdaterController(startingUpdater: true, ...)
        // controller.checkForUpdates(nil)
        // For now, fall through to GitHub check
        checkWithGitHub()
    }

    // MARK: - GitHub Releases API

    private func checkWithGitHub() {
        guard !isChecking else { return }
        isChecking = true

        Task {
            defer { DispatchQueue.main.async { self.isChecking = false } }

            guard let url = URL(string: githubReleaseURL) else { return }
            var request = URLRequest(url: url)
            request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
            request.timeoutInterval = 10

            do {
                let (data, response) = try await URLSession.shared.data(for: request)
                guard let httpResponse = response as? HTTPURLResponse,
                      httpResponse.statusCode == 200 else { return }

                guard let json = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { return }

                let tagName = json["tag_name"] as? String ?? ""
                let body = json["body"] as? String ?? ""
                let htmlURL = json["html_url"] as? String ?? ""

                // Strip "v" prefix for comparison
                let remoteVersion = tagName.hasPrefix("v") ? String(tagName.dropFirst()) : tagName
                let localVersion = currentVersion.hasPrefix("v") ? String(currentVersion.dropFirst()) : currentVersion

                // Only suggest the remote version when it is *newer* than the running build.
                // String inequality would otherwise allow a downgrade if GitHub `releases/latest`
                // happens to lag behind a freshly published version.
                if !remoteVersion.isEmpty && !localVersion.isEmpty &&
                   Self.compareSemver(remoteVersion, localVersion) > 0 {
                    // Find macOS DMG asset matching the host CPU architecture.
                    // Release assets are published as:
                    //   mcpproxy-<ver>-darwin-arm64.dmg / -amd64.dmg            (unsigned)
                    //   mcpproxy-<ver>-darwin-arm64-installer.dmg / -amd64-installer.dmg  (signed + notarized)
                    let arch = Self.hostArchToken()
                    var dmgURL = htmlURL
                    if let assets = json["assets"] as? [[String: Any]] {
                        let matches: [(name: String, url: String)] = assets.compactMap { asset in
                            guard let name = asset["name"] as? String,
                                  let url = asset["browser_download_url"] as? String,
                                  name.contains("darwin"),
                                  name.contains(arch),
                                  name.hasSuffix(".dmg") else { return nil }
                            return (name, url)
                        }
                        // Prefer signed & notarized installer DMG when available.
                        if let installer = matches.first(where: { $0.name.contains("-installer.dmg") }) {
                            dmgURL = installer.url
                        } else if let first = matches.first {
                            dmgURL = first.url
                        }
                    }

                    DispatchQueue.main.async {
                        self.latestVersion = remoteVersion
                        self.downloadURL = dmgURL
                        self.releaseNotes = body
                    }
                }
            } catch {
                // Silently fail — update checks are non-critical
            }
        }
    }
}
