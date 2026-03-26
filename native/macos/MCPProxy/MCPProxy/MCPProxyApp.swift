// MCPProxyApp.swift
// MCPProxy
//
// The @main entry point for the MCPProxy macOS tray application.
// Uses AppKit NSStatusItem + NSMenu directly (not SwiftUI MenuBarExtra)
// because MenuBarExtra with .menu style has a known bug where ForEach
// over dynamic arrays appends duplicates to the underlying NSMenu.

import SwiftUI
import Combine

// MARK: - App Delegate

/// Manages the status bar item, menu, core process, and app lifecycle.
final class AppController: NSObject, NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate {
    let appState = AppState()
    let notificationService = NotificationService()
    let updateService = UpdateService()
    var coreManager: CoreProcessManager?

    private var statusItem: NSStatusItem!
    private var mainWindow: NSWindow?
    private var cancellables = Set<AnyCancellable>()

    func applicationWillFinishLaunching(_ notification: Notification) {
        // Prevent focus steal on launch — no Dock icon, no Cmd+Tab entry
        NSApp.setActivationPolicy(.prohibited)
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Switch to accessory (menu bar only) now that launch is complete
        NSApp.setActivationPolicy(.accessory)

        // Create the status bar item with the MCPProxy monochrome icon
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = statusItem.button {
            // Load the bundled icon-mono-44.png from the app bundle
            if let iconPath = Bundle.main.path(forResource: "icon-mono-44", ofType: "png"),
               let icon = NSImage(contentsOfFile: iconPath) {
                icon.isTemplate = true  // Adapts to light/dark menu bar
                icon.size = NSSize(width: 18, height: 18)
                button.image = icon
            } else {
                // Fallback to SF Symbol if bundled icon not found
                button.image = NSImage(systemSymbolName: "server.rack",
                                       accessibilityDescription: "MCPProxy")
            }
        }

        // Build initial menu (rebuildMenu creates the NSMenu and sets delegate)
        rebuildMenu()

        // Subscribe to state changes — update icon, menu, and refresh servers periodically
        appState.objectWillChange
            .debounce(for: .milliseconds(500), scheduler: RunLoop.main)
            .sink { [weak self] _ in
                self?.updateStatusIcon()
                self?.rebuildMenu()
            }
            .store(in: &cancellables)

        // Periodic server refresh every 10s to keep health/action data current
        Timer.publish(every: 10, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in
                guard let self, let client = self.appState.apiClient else { return }
                Task {
                    if let servers = try? await client.servers() {
                        await self.appState.updateServers(servers)
                    }
                }
            }
            .store(in: &cancellables)

        // Start core
        Task {
            await startCore()
        }
    }

    // MARK: - NSMenuDelegate

    func menuWillOpen(_ menu: NSMenu) {
        // Fetch fresh server data before building the menu
        // This ensures health.action (login/restart) is current
        if let client = appState.apiClient {
            Task {
                if let servers = try? await client.servers() {
                    await appState.updateServers(servers)
                    await MainActor.run { rebuildMenu() }
                }
            }
        }
        // Build with current data immediately (async fetch updates it shortly after)
        rebuildMenu()
    }

    func applicationWillTerminate(_ notification: Notification) {
        if let process = coreManager?.managedProcess {
            process.terminate()
        }
    }

    // MARK: - Main Window

    /// Show the main application window with SwiftUI content.
    /// If the window already exists, bring it to front. Otherwise create it.
    ///
    /// - Parameter tab: Optional sidebar item to select when the window opens.
    func showMainWindow(tab: SidebarItem? = nil) {
        if let window = mainWindow, window.isVisible {
            NSApp.setActivationPolicy(.regular)
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        // Show in Dock and Cmd+Tab BEFORE presenting the window
        NSApp.setActivationPolicy(.regular)

        // MainWindow reads apiClient from appState, so we create it once.
        // When appState.apiClient is set by CoreProcessManager, all views
        // automatically re-render — no need to replace the NSHostingView.
        let contentView = MainWindow(appState: appState)
        let hostingView = NSHostingView(rootView: contentView)

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 900, height: 600),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "MCPProxy"
        window.contentView = hostingView
        window.center()
        window.setFrameAutosaveName("MCPProxyMainWindow")
        window.isReleasedWhenClosed = false
        // Watch for window close to hide from Dock again
        window.delegate = self
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        mainWindow = window
    }

    @objc private func openMainWindow() {
        showMainWindow()
    }

    @objc private func showAddServer() {
        showMainWindow()
        // Post notification after a short delay so the window and ServersView
        // have time to appear and register their notification observer.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            NotificationCenter.default.post(name: .showAddServer, object: nil)
        }
    }

    // NSWindowDelegate — hide from Dock when window closes
    func windowWillClose(_ notification: Notification) {
        // Return to accessory (menu bar only) when main window closes
        NSApp.setActivationPolicy(.accessory)
    }

    // MARK: - Core Startup

    private func startCore() async {
        await notificationService.setup()
        await MainActor.run {
            appState.autoStartEnabled = AutoStartService.isEnabled
        }

        if SymlinkService.needsSetup() {
            if let bundledBinary = resolveBundledCoreBinary() {
                await SymlinkService.updateSymlinkIfNeeded(bundledBinary: bundledBinary)
            }
        }

        let manager = CoreProcessManager(
            appState: appState,
            notificationService: notificationService
        )
        coreManager = manager
        await manager.start()
    }

    private func resolveBundledCoreBinary() -> String? {
        guard let execPath = Bundle.main.executablePath else { return nil }
        let execURL = URL(fileURLWithPath: execPath)
        let macOSDir = execURL.deletingLastPathComponent()
        let contentsDir = macOSDir.deletingLastPathComponent()
        guard contentsDir.lastPathComponent == "Contents" else { return nil }
        let candidate = contentsDir
            .appendingPathComponent("Resources")
            .appendingPathComponent("bin")
            .appendingPathComponent("mcpproxy")
        if FileManager.default.isExecutableFile(atPath: candidate.path) {
            return candidate.path
        }
        return nil
    }

    // MARK: - Status Icon

    /// Update the status bar icon with a health-colored badge dot.
    private func updateStatusIcon() {
        guard let button = statusItem.button else { return }

        let health = appState.healthLevel
        // Use the MCPProxy monochrome icon (same as initial tray icon)
        let base: NSImage
        if let iconPath = Bundle.main.path(forResource: "icon-mono-44", ofType: "png"),
           let bundledIcon = NSImage(contentsOfFile: iconPath) {
            base = bundledIcon
        } else if let sfIcon = NSImage(systemSymbolName: "server.rack", accessibilityDescription: "MCPProxy") {
            base = sfIcon
        } else {
            return
        }

        // Compose the icon with a badge dot
        let size = NSSize(width: 18, height: 18)
        let composed = NSImage(size: size, flipped: false) { rect in
            // Draw base icon
            base.draw(in: rect)

            // Draw badge dot in bottom-right
            let dotSize: CGFloat = 6
            let dotRect = NSRect(
                x: rect.width - dotSize - 0.5,
                y: 0.5,
                width: dotSize,
                height: dotSize
            )

            let dotColor: NSColor
            switch health {
            case .healthy: dotColor = .systemGreen
            case .degraded: dotColor = .systemYellow
            case .unhealthy: dotColor = .systemRed
            case .disconnected: dotColor = .systemGray
            }

            // White outline for visibility
            NSColor.white.setFill()
            NSBezierPath(ovalIn: dotRect.insetBy(dx: -1, dy: -1)).fill()
            // Colored dot
            dotColor.setFill()
            NSBezierPath(ovalIn: dotRect).fill()

            return true
        }
        composed.isTemplate = false // Has colors, can't be template
        button.image = composed
    }

    // MARK: - Menu Building (AppKit NSMenu — no SwiftUI)

    /// Rebuild the entire NSMenu from current appState.
    /// Clears and rebuilds in-place to avoid replacing the menu object
    /// (which would close an already-open menu and lose the delegate).
    private func rebuildMenu() {
        let menu: NSMenu
        if let existing = statusItem.menu {
            existing.removeAllItems()
            menu = existing
        } else {
            menu = NSMenu()
            menu.delegate = self
            statusItem.menu = menu
        }

        // Header
        let ver = appState.version.hasPrefix("v") ? appState.version : "v\(appState.version)"
        let title = appState.version.isEmpty ? "MCPProxy" : "MCPProxy \(ver)"
        let titleItem = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        titleItem.isEnabled = false
        let font = NSFont.boldSystemFont(ofSize: 13)
        titleItem.attributedTitle = NSAttributedString(string: title, attributes: [.font: font])
        menu.addItem(titleItem)

        let summary = NSMenuItem(title: appState.statusSummary, action: nil, keyEquivalent: "")
        summary.isEnabled = false
        menu.addItem(summary)

        // Error state
        if case .error(let coreError) = appState.coreState {
            let errorItem = NSMenuItem(title: "Error: \(coreError.userMessage)", action: nil, keyEquivalent: "")
            errorItem.isEnabled = false
            menu.addItem(errorItem)

            let hintItem = NSMenuItem(title: coreError.remediationHint, action: nil, keyEquivalent: "")
            hintItem.isEnabled = false
            menu.addItem(hintItem)

            if coreError.isRetryable {
                let retryItem = NSMenuItem(title: "Retry", action: #selector(retryCore), keyEquivalent: "")
                retryItem.target = self
                menu.addItem(retryItem)
            }
        }

        menu.addItem(.separator())

        // Needs Attention — only auth required, connection errors, quarantine (NOT disabled)
        let attentionServers = appState.serversNeedingAttention
        if !attentionServers.isEmpty {
            let header = NSMenuItem(title: "Needs Attention (\(attentionServers.count))", action: nil, keyEquivalent: "")
            header.isEnabled = false
            menu.addItem(header)

            for server in attentionServers {
                let action = server.health?.action ?? ""
                let summary = server.health?.summary ?? ""
                let icon = actionIcon(for: action)

                let title = "\(server.name) — \(summary.isEmpty ? actionDisplayName(for: action) : summary)"
                let item = NSMenuItem(title: title, action: #selector(handleAttentionAction(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = server
                item.image = NSImage(systemSymbolName: icon, accessibilityDescription: action)
                menu.addItem(item)
            }
            menu.addItem(.separator())
        }

        // Quarantine
        if appState.quarantinedToolsCount > 0 {
            let quarantineItem = NSMenuItem(
                title: "\(appState.quarantinedToolsCount) quarantined server(s)",
                action: nil, keyEquivalent: "")
            quarantineItem.isEnabled = false
            quarantineItem.image = NSImage(systemSymbolName: "shield.lefthalf.filled",
                                            accessibilityDescription: "quarantine")
            menu.addItem(quarantineItem)
            menu.addItem(.separator())
        }

        // Servers — as a SUBMENU (not flat list)
        if !appState.servers.isEmpty {
            let serversMenuItem = NSMenuItem(title: "Servers (\(appState.servers.count))", action: nil, keyEquivalent: "")
            let serversSubmenu = NSMenu()

            for server in appState.servers {
                let item = NSMenuItem(title: server.name, action: nil, keyEquivalent: "")

                // Status icon: colored dot + auth indicator
                let needsAuth = server.health?.action == "login"
                let dotColor = serverStatusColor(for: server)

                let iconSize = NSSize(width: 16, height: 16)
                let icon = NSImage(size: iconSize, flipped: false) { rect in
                    // Draw health dot
                    let dotRect = NSRect(x: 2, y: 4, width: 8, height: 8)
                    dotColor.setFill()
                    NSBezierPath(ovalIn: dotRect).fill()

                    // Draw auth lock icon overlay if needed
                    if needsAuth {
                        let lockRect = NSRect(x: 9, y: 0, width: 7, height: 7)
                        NSColor.systemRed.setFill()
                        NSBezierPath(ovalIn: lockRect).fill()
                    }
                    return true
                }
                item.image = icon

                // Per-server submenu with actions
                let sub = NSMenu()
                let statusText = server.health?.summary ?? (server.connected ? "Connected" : server.enabled ? "Disconnected" : "Disabled")
                let statusLine = NSMenuItem(title: statusText, action: nil, keyEquivalent: "")
                statusLine.isEnabled = false
                sub.addItem(statusLine)

                // Protocol info
                let protoLine = NSMenuItem(title: "Protocol: \(server.protocol)", action: nil, keyEquivalent: "")
                protoLine.isEnabled = false
                sub.addItem(protoLine)

                sub.addItem(.separator())

                // Auth login button — prominently first if needed
                if needsAuth {
                    let login = NSMenuItem(title: "Log In (Opens Browser)", action: #selector(loginServer(_:)), keyEquivalent: "")
                    login.target = self
                    login.representedObject = server.name
                    login.image = NSImage(systemSymbolName: "person.badge.key", accessibilityDescription: "login")
                    sub.addItem(login)
                    sub.addItem(.separator())
                }

                if server.enabled {
                    let disable = NSMenuItem(title: "Disable", action: #selector(disableServer(_:)), keyEquivalent: "")
                    disable.target = self
                    disable.representedObject = server.name
                    sub.addItem(disable)
                } else {
                    let enable = NSMenuItem(title: "Enable", action: #selector(enableServer(_:)), keyEquivalent: "")
                    enable.target = self
                    enable.representedObject = server.name
                    sub.addItem(enable)
                }

                let restart = NSMenuItem(title: "Restart", action: #selector(restartServer(_:)), keyEquivalent: "")
                restart.target = self
                restart.representedObject = server.name
                sub.addItem(restart)

                sub.addItem(.separator())

                let logs = NSMenuItem(title: "View Logs", action: #selector(viewServerLogs(_:)), keyEquivalent: "")
                logs.target = self
                logs.representedObject = server.name
                sub.addItem(logs)

                item.submenu = sub
                serversSubmenu.addItem(item)
            }

            serversMenuItem.submenu = serversSubmenu
            menu.addItem(serversMenuItem)
            menu.addItem(.separator())
        }

        // Recent Activity (deduplicated — show unique entries only)
        if !appState.recentActivity.isEmpty {
            let activityHeader = NSMenuItem(title: "Recent Activity", action: nil, keyEquivalent: "")
            activityHeader.isEnabled = false
            menu.addItem(activityHeader)

            // Deduplicate by (serverName + toolName + type) — keep first occurrence
            var seen = Set<String>()
            var uniqueEntries: [ActivityEntry] = []
            for entry in appState.recentActivity {
                let key = "\(entry.serverName ?? ""):\(entry.toolName ?? ""):\(entry.type)"
                if !seen.contains(key) {
                    seen.insert(key)
                    uniqueEntries.append(entry)
                }
            }

            for entry in uniqueEntries.prefix(5) {
                var text = ""
                if let sn = entry.serverName, !sn.isEmpty {
                    text = sn
                    if let tn = entry.toolName, !tn.isEmpty { text += ":\(tn)" }
                } else {
                    text = entry.type
                }

                // Add relative timestamp
                let timeAgo = relativeTimeString(from: entry.timestamp)
                if !timeAgo.isEmpty { text += " · \(timeAgo)" }

                let item = NSMenuItem(title: text, action: nil, keyEquivalent: "")
                item.isEnabled = false
                let iconName: String
                switch entry.status {
                case "error": iconName = "xmark.circle"
                case "blocked": iconName = "hand.raised"
                default: iconName = "checkmark.circle"
                }
                item.image = NSImage(systemSymbolName: iconName, accessibilityDescription: entry.status)
                menu.addItem(item)
            }
            menu.addItem(.separator())
        }

        // Actions
        let addServer = NSMenuItem(title: "Add Server...", action: #selector(showAddServer), keyEquivalent: "n")
        addServer.target = self
        menu.addItem(addServer)

        let openApp = NSMenuItem(title: "Open MCPProxy...", action: #selector(openMainWindow), keyEquivalent: ",")
        openApp.target = self
        menu.addItem(openApp)

        let webUI = NSMenuItem(title: "Open Web UI", action: #selector(openWebUI), keyEquivalent: "")
        webUI.target = self
        menu.addItem(webUI)

        let configFile = NSMenuItem(title: "Open Config File", action: #selector(openConfigFile), keyEquivalent: "")
        configFile.target = self
        menu.addItem(configFile)

        let logsDir = NSMenuItem(title: "Open Logs Directory", action: #selector(openLogsDirectory), keyEquivalent: "")
        logsDir.target = self
        menu.addItem(logsDir)

        menu.addItem(.separator())

        // Settings
        let autoStart = NSMenuItem(title: "Run at Startup", action: #selector(toggleAutoStart(_:)), keyEquivalent: "")
        autoStart.target = self
        autoStart.state = appState.autoStartEnabled ? .on : .off
        menu.addItem(autoStart)

        let checkUpdates = NSMenuItem(title: "Check for Updates", action: #selector(checkForUpdates), keyEquivalent: "")
        checkUpdates.target = self
        checkUpdates.isEnabled = updateService.canCheckForUpdates
        menu.addItem(checkUpdates)

        // Show update from either appState (from core /api/v1/info) or UpdateService (GitHub check)
        let updateVersion = appState.updateAvailable ?? updateService.latestVersion
        if let available = updateVersion {
            let updateNote = NSMenuItem(title: "Update available: v\(available)", action: #selector(openDownloadPage), keyEquivalent: "")
            updateNote.target = self
            menu.addItem(updateNote)
        }

        menu.addItem(.separator())

        // Quit
        let quit = NSMenuItem(title: "Quit MCPProxy", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

    }

    // MARK: - Menu Actions

    @objc private func retryCore() {
        Task { await coreManager?.retry() }
    }

    @objc private func handleAttentionAction(_ sender: NSMenuItem) {
        guard let server = sender.representedObject as? ServerStatus else { return }
        guard let action = server.health?.action else { return }
        Task {
            switch action {
            case "login": try? await appState.apiClient?.loginServer(server.id)
            case "restart": try? await appState.apiClient?.restartServer(server.id)
            case "enable": try? await appState.apiClient?.enableServer(server.id)
            default: openWebUI()
            }
        }
    }

    @objc private func enableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await appState.apiClient?.enableServer(id) }
    }

    @objc private func disableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await appState.apiClient?.disableServer(id) }
    }

    @objc private func restartServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        Task { try? await appState.apiClient?.restartServer(id) }
    }

    @objc private func loginServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else {
            NSLog("[MCPProxy] loginServer: no server ID in representedObject")
            return
        }
        NSLog("[MCPProxy] loginServer: triggering login for %@", id)
        // Use appState.apiClient directly (already on main thread, no async needed)
        if let client = appState.apiClient {
            Task {
                do {
                    try await client.loginServer(id)
                    NSLog("[MCPProxy] loginServer: API call succeeded for %@", id)
                } catch {
                    NSLog("[MCPProxy] loginServer: API call failed: %@", error.localizedDescription)
                }
            }
        } else {
            NSLog("[MCPProxy] loginServer: no apiClient available")
        }
    }

    @objc private func viewServerLogs(_ sender: NSMenuItem) {
        guard let name = sender.representedObject as? String else { return }
        let home = FileManager.default.homeDirectoryForCurrentUser
        let logFile = home.appendingPathComponent("Library/Logs/mcpproxy/server-\(name).log")
        if FileManager.default.fileExists(atPath: logFile.path) {
            NSWorkspace.shared.open(logFile)
        } else {
            openLogsDirectory()
        }
    }

    @objc private func openWebUI() {
        Task {
            let apiKey = await coreManager?.currentAPIKey ?? ""
            let urlString = apiKey.isEmpty
                ? "http://127.0.0.1:8080/ui/"
                : "http://127.0.0.1:8080/ui/?apikey=\(apiKey)"
            if let url = URL(string: urlString) {
                NSWorkspace.shared.open(url)
            }
        }
    }

    @objc private func openConfigFile() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        NSWorkspace.shared.open(home.appendingPathComponent(".mcpproxy/mcp_config.json"))
    }

    @objc private func openLogsDirectory() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        NSWorkspace.shared.open(home.appendingPathComponent("Library/Logs/mcpproxy"))
    }

    @objc private func toggleAutoStart(_ sender: NSMenuItem) {
        do {
            if appState.autoStartEnabled {
                try AutoStartService.disable()
                appState.autoStartEnabled = false
            } else {
                try AutoStartService.enable()
                appState.autoStartEnabled = true
            }
        } catch {}
        rebuildMenu()
    }

    @objc private func checkForUpdates() {
        updateService.currentVersion = appState.version
        updateService.checkForUpdates()
    }

    @objc private func openDownloadPage() {
        updateService.openDownloadPage()
    }

    @objc private func quitApp() {
        Task {
            await coreManager?.shutdown()
            try? await Task.sleep(nanoseconds: 200_000_000)
            NSApplication.shared.terminate(nil)
        }
    }

    // MARK: - Helpers

    private func serverStatusColor(for server: ServerStatus) -> NSColor {
        if server.quarantined { return .systemOrange }
        if let health = server.health {
            switch health.level {
            case "healthy": return .systemGreen
            case "degraded": return .systemYellow
            case "unhealthy": return .systemRed
            default: return server.connected ? .systemGreen : .systemGray
            }
        }
        return server.connected ? .systemGreen : .systemGray
    }

    private func actionIcon(for action: String) -> String {
        switch action {
        case "login": return "person.badge.key"
        case "restart": return "arrow.clockwise"
        case "enable": return "power"
        case "approve": return "checkmark.shield"
        default: return "exclamationmark.circle"
        }
    }

    private func actionDisplayName(for action: String) -> String {
        switch action {
        case "login": return "Login Required"
        case "restart": return "Restart Needed"
        case "enable": return "Disabled"
        case "approve": return "Approval Needed"
        case "set_secret": return "Secret Missing"
        case "configure": return "Configuration Needed"
        case "view_logs": return "Check Logs"
        default: return "Action Needed"
        }
    }

    private func relativeTimeString(from timestamp: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        var date = formatter.date(from: timestamp)
        if date == nil {
            formatter.formatOptions = [.withInternetDateTime]
            date = formatter.date(from: timestamp)
        }
        guard let d = date else { return "" }

        let elapsed = -d.timeIntervalSinceNow
        if elapsed < 60 { return "just now" }
        if elapsed < 3600 { return "\(Int(elapsed / 60))m ago" }
        if elapsed < 86400 { return "\(Int(elapsed / 3600))h ago" }
        return "\(Int(elapsed / 86400))d ago"
    }
}

// MARK: - App

// MARK: - Notification Names

extension Notification.Name {
    /// Posted by tray menu "Add Server..." to trigger the sheet in ServersView.
    static let showAddServer = Notification.Name("MCPProxy.showAddServer")
}

@main
struct MCPProxyApp: App {
    @NSApplicationDelegateAdaptor(AppController.self) var controller

    var body: some Scene {
        // No SwiftUI scenes — the tray menu is pure AppKit (NSStatusItem + NSMenu).
        // This avoids the MenuBarExtra .menu style bug where ForEach duplicates items.
        // A Settings scene can be added here for Spec B (main window).
        Settings {
            EmptyView()
        }
    }
}
