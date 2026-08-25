// MCPProxyApp.swift
// MCPProxy
//
// The @main entry point for the MCPProxy macOS tray application.
// Uses AppKit NSStatusItem + NSMenu directly (not SwiftUI MenuBarExtra)
// because MenuBarExtra with .menu style has a known bug where ForEach
// over dynamic arrays appends duplicates to the underlying NSMenu.

import SwiftUI
import Combine
import os

// MARK: - Tray menu host

/// Whatever owns the tray menu.
///
/// In production this is the `NSStatusItem`, and nothing else ever will be. It
/// is a protocol so the menu paths — `menuWillOpen`, `rebuildMenu`,
/// `menuDidClose` — can run in a test without putting an icon in the menu bar of
/// whoever is running the suite. That is not a convenience: the spec-048
/// invariant ("opening the menu performs zero network requests") is only
/// meaningful if the REAL open sequence can be driven, and before this it could
/// not be, so the test that claimed to check it counted a stub nothing was
/// wired to. See `MenuOpenNetworkTests`.
@MainActor
protocol TrayMenuHost: AnyObject {
    var menu: NSMenu? { get set }
}

extension NSStatusItem: TrayMenuHost {}

// MARK: - App Delegate

/// Manages the status bar item, menu, core process, and app lifecycle.
final class AppController: NSObject, NSApplicationDelegate, NSWindowDelegate, NSMenuDelegate {
    let appState = AppState()
    let notificationService = NotificationService()
    /// Owns everything the tray knows about updates (Spec 092 FR-017). A `var`
    /// so a test can substitute a service built around a fixture bundle and a
    /// stub feed updater; production never reassigns it.
    var updateService = UpdateService()
    var coreManager: CoreProcessManager?

    private var statusItem: NSStatusItem?

    /// Where the tray menu lives. Assigned to `statusItem` at launch; injected
    /// by tests that drive the menu paths without a status bar item.
    @MainActor
    private var menuHost: TrayMenuHost?

    /// Set only by the testing initializer; production leaves it nil and falls
    /// back to the live core client.
    private var injectedGlanceDataSource: GlanceDataSource?

    /// The read surface any tray path is allowed to fetch through.
    ///
    /// Nothing in the menu-open sequence may call it — the menu renders from
    /// state already in memory, fed by SSE and the background poll — and it is a
    /// property precisely so that claim is falsifiable: a fetch added to
    /// `menuWillOpen` tomorrow has somewhere to be counted, and
    /// `MenuOpenNetworkTests` counts it.
    @MainActor
    var glanceDataSource: GlanceDataSource? {
        injectedGlanceDataSource ?? appState.apiClient
    }

    private var mainWindow: NSWindow?
    private var settingsWindow: NSWindow?
    /// The Connect Client form. Presented as a sheet on the main window when one
    /// is up, and as its own window otherwise. The lifecycle owns the window and
    /// its model together so BOTH exits — the form's Close button and the
    /// titlebar's red button — tear the form down (see the type's header).
    @MainActor private let connectClientForm = ConnectFormWindowLifecycle()
    private var cancellables = Set<AnyCancellable>()
    private var keyMonitor: Any?

    /// Tray Glance: builds the activity / clients / histogram rows, and keeps
    /// references to them so a refresh landing while the menu is on screen can
    /// rewrite them in place instead of restructuring the menu. Rows call back
    /// into this delegate (see `openActivityForSession`), which opens the
    /// native main window at the Activity section.
    ///
    /// `@MainActor` because `GlanceSection` is an isolated type and this class is
    /// not, so constructing it from a plain stored-property initializer would not
    /// compile. See that type's header for why the alternative — a `nonisolated`
    /// initializer over there — is the wrong trade.
    @MainActor
    private lazy var glance = GlanceSection(
        target: self,
        action: #selector(openActivityForSession(_:))
    )

    /// Suppresses structural rebuilds while the menu is on screen.
    private var rebuildGuard = MenuRebuildGuard()

    /// Scheduler for the debounced `objectWillChange -> rebuildMenu()` sink.
    ///
    /// **Must be a dispatch queue, not `RunLoop.main`.** While an `NSMenu` is
    /// tracking, the main run loop runs in `NSEventTrackingRunLoopMode`, and
    /// Combine's `RunLoop` scheduler installs its timers in `.default` mode
    /// only — so a `RunLoop.main`-scheduled `debounce` is never serviced until
    /// the menu closes. That silently disables the entire open-menu update
    /// path: `GlanceSection.updateInPlace` and `MenuRebuildGuard`'s
    /// update-in-place / defer-until-close branches can only run if something
    /// calls `rebuildMenu()` while the menu is up, and this sink is the only
    /// caller that does. The glance would then be a snapshot frozen at
    /// menu-open time, which is precisely what it must not be.
    ///
    /// The main dispatch queue is drained in every run-loop mode, so this
    /// delivers during tracking. It is the same reason the timers below are
    /// published `in: .common` rather than in the default mode.
    ///
    /// Named (rather than written inline at the subscription) so the tests can
    /// bind to the very scheduler production uses — see
    /// `MenuRefreshSchedulerTests`.
    static let menuRefreshScheduler = DispatchQueue.main

    override init() {
        super.init()
    }

    /// Construct a controller whose tray menu is hosted outside the menu bar and
    /// whose reads are countable — the seam `MenuOpenNetworkTests` drives the
    /// real menu-open sequence through. Production goes through `init()` and
    /// `applicationDidFinishLaunching`, which installs the status item as the
    /// host and leaves the data source resolving to the live core client.
    @MainActor
    convenience init(
        glanceDataSource: GlanceDataSource,
        menuHost: TrayMenuHost,
        updateService: UpdateService? = nil
    ) {
        self.init()
        self.injectedGlanceDataSource = glanceDataSource
        self.menuHost = menuHost
        if let updateService { self.updateService = updateService }
    }

    func applicationWillFinishLaunching(_ notification: Notification) {
        // Prevent focus steal on launch — no Dock icon, no Cmd+Tab entry
        NSApp.setActivationPolicy(.prohibited)

        // Disable macOS automatic text substitutions app-wide (issue #538).
        // Smart-dash substitution rewrites "--" as an em-dash "—", which
        // silently corrupts CLI flags typed into server Command/Arguments/Env
        // fields (e.g. "--flag" → "—flag"), producing broken configs. Done
        // before any window (and thus any NSTextView field editor) is created
        // so every text field inherits the disabled state. See
        // TextSubstitution.disableAutomaticTextSubstitutions.
        TextSubstitution.disableAutomaticTextSubstitutions()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        // Switch to accessory (menu bar only) now that launch is complete
        NSApp.setActivationPolicy(.accessory)

        // Lifecycle bookkeeping FIRST, so a launch that fails later is still on
        // the record — and so an unaccounted-for previous exit is reported now,
        // while somebody is reading the log, rather than never (#862).
        Self.logInstanceRoot()
        AppLifecycle.shared.recordLaunch()
        AppLifecycle.shared.installSignalHandlers {
            // A caught signal goes through the normal quit path so the core is
            // shut down properly. The hop to main is made HERE, not by the
            // signal machinery: reaching this decision must not depend on a
            // main thread that may never answer, and if it does not answer the
            // escalation inside AppLifecycle restores the signal's default
            // action so the process still goes.
            DispatchQueue.main.async { NSApplication.shared.terminate(nil) }
        }
        // Logout, restart and shutdown reach applicationWillTerminate exactly
        // like a Quit does, so the difference has to be claimed here.
        NSWorkspace.shared.notificationCenter.addObserver(
            forName: NSWorkspace.willPowerOffNotification, object: nil, queue: .main
        ) { _ in
            AppLifecycle.shared.note("macOS is logging out, restarting or shutting down")
        }

        // Monitor Cmd+/Cmd-/Cmd+0 globally for text size adjustment.
        // Store the monitor reference to prevent potential deallocation.
        // Match both "+" (Cmd+Shift+=) and "=" (Cmd+=) for zoom in,
        // since the + key on US keyboards is Shift+=.
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: .keyDown) { [weak self] event in
            guard event.modifierFlags.contains(.command) else { return event }
            let key = event.charactersIgnoringModifiers ?? ""
            switch key {
            case "+", "=":
                NSLog("[MCPProxy] Zoom in: key=%@ fontScale=%.1f", key, self?.appState.fontScale ?? 0)
                self?.makeTextBigger()
                return nil
            case "-":
                NSLog("[MCPProxy] Zoom out: key=%@ fontScale=%.1f", key, self?.appState.fontScale ?? 0)
                self?.makeTextSmaller()
                return nil
            case "0":
                NSLog("[MCPProxy] Zoom reset: fontScale=%.1f", self?.appState.fontScale ?? 0)
                self?.makeTextActualSize()
                return nil
            case "n":
                NSLog("[MCPProxy] Cmd+N: show add server")
                self?.showAddServer()
                return nil
            case ",":
                // Intercept ⌘, before SwiftUI's Settings scene sees it, so it
                // opens our config window instead of the (unreliable) scene.
                NSLog("[MCPProxy] Cmd+,: show settings")
                self?.showSettingsWindow()
                return nil
            default:
                return event
            }
        }

        // Set up the app's main menu bar with View > Text Size commands
        setupMainMenu()

        // The SwiftUI Settings scene window is owned by SwiftUI, not us, so it
        // never hits our NSWindowDelegate. Observe all window closes so we can
        // drop back to a menu-bar-only app when the Settings window is dismissed.
        NotificationCenter.default.addObserver(
            forName: NSWindow.willCloseNotification, object: nil, queue: .main
        ) { [weak self] _ in
            DispatchQueue.main.async { self?.restoreAccessoryIfNoVisibleWindows() }
        }

        // Create the status bar item with the MCPProxy monochrome icon
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        statusItem = item
        menuHost = item
        if let button = item.button {
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

        // Subscribe to state changes — update icon, menu, and refresh servers periodically.
        // Merge UpdateService changes so a fresh GitHub check repaints the menu immediately
        // instead of waiting for the next server-poll cycle.
        Publishers.Merge(
            appState.objectWillChange.map { _ in () },
            updateService.objectWillChange.map { _ in () }
        )
            .debounce(for: .milliseconds(500), scheduler: Self.menuRefreshScheduler)
            .sink { [weak self] _ in
                self?.updateStatusIcon()
                self?.rebuildMenu()
            }
            .store(in: &cancellables)

        // Spec 048: dropped the 10 s server-refresh timer. The server list is
        // now SSE-driven via the spec 047 `servers.changed` payload — appState
        // updates within ~50 ms of any state transition with zero round trip.
        // The safety-net below covers the rare case where SSE drops events.
        Timer.publish(every: 300, on: .main, in: .common)   // 5 minutes
            .autoconnect()
            .sink { [weak self] _ in
                guard let self, let core = self.coreManager else { return }
                Task { await core.refreshServersForSafetyNet() }
            }
            .store(in: &cancellables)

        // Spec 092 FR-012: give the updater a synchronous way to stop the core
        // it manages. Installed before the feed updater starts, because a
        // Sparkle session resumed from a previous launch can reach the install
        // hook almost immediately.
        // The Bool it returns is not advisory: `false` makes Sparkle postpone
        // the installation rather than replace the bundle under a live core.
        updateService.stopManagedCore = { [weak self] in
            guard let self else { return true }
            let pid = self.coreManager?.managedProcess?.processIdentifier
            // Mark stop INTENT for THIS pid before terminating: the core's
            // resulting clean (status 0) exit must not pop a spurious "MCPProxy
            // Error" nor race a reconnection against Sparkle's bundle swap.
            // Scoping to the pid (not a global flag) means a later core with a
            // different pid is unaffected even if this intent is never consumed.
            self.appState.stoppedForUpdatePID = pid
            let outcome = ManagedCoreStop.stop(pid: pid)
            if !outcome.coreIsDown {
                // The stop did not bring the core down — Sparkle will postpone
                // the install and the tray keeps managing the still-live core,
                // so clear the intent (that pid's future exit is not ours).
                self.appState.stoppedForUpdatePID = nil
            }
            NSLog("[MCPProxy] Pre-update core stop: pid=%d outcome=%@",
                  pid ?? -1, String(describing: outcome))
            AppLifecycle.shared.note("pre-update core stop: \(outcome)")
            return outcome.coreIsDown
        }

        // Spec 092 FR-015: every tray-side check is governed by the policy the
        // core publishes. Applied before the first check is scheduled.
        appState.$coreUpdatePolicy
            .removeDuplicates()
            .sink { [weak self] policy in
                self?.updateService.applyCorePolicy(policy)
            }
            .store(in: &cancellables)

        // Spec 095: the recovery dialog's AppKit half. The service owns the
        // failure itself — stage, candidates, retry ordering — and only borrows
        // a way to put three buttons on screen and open a browser.
        updateService.presentFailureDialog = { content, respond in
            UpdateFailureAlertPresenter.present(content, respond: respond)
        }
        updateService.openFailureDownload = { UpdateFailureAlertPresenter.openInBrowser($0) }

        // Spec 095 US2: the update service has no API client of its own, and the
        // client is resolved per occurrence — a failure can happen before the
        // core is up, or after it has gone away, and neither is worth a retry.
        //
        // The current client is mirrored into a lock rather than read through
        // MainActor at occurrence time: the recovery dialog runs a modal session
        // on the main actor, and a `MainActor.run` hop would park the recording
        // until the user dismisses the dialog (or lose it entirely if the app
        // quits first) — caught live in the spec-095 rig verification.
        //
        // The mirror never goes nil: before the core connects (and between
        // connections) a socket-pinned fallback client makes the one bounded
        // attempt FR-010 requires — SocketURLProtocol probes the socket per
        // request, so a dead core fails fast as a URLError, one log line.
        let fallbackRecordingClient = APIClient(
            socketPath: InstancePaths.socketPath, requestTimeout: 10
        )
        let recordingClient = OSAllocatedUnfairLock<APIClient>(
            initialState: fallbackRecordingClient
        )
        appState.$apiClient
            .sink { client in recordingClient.withLock { $0 = client ?? fallbackRecordingClient } }
            .store(in: &cancellables)
        updateService.recordUpdateFailure = { stage in
            await recordingClient.withLock({ $0 }).recordUpdateFailure(stage: stage)
        }

        // Spec 092 FR-010: start the feed updater. It gates its own scheduled
        // cycle on the policy above and reports back through UpdateService.
        updateService.startFeedUpdater()

        // Auto-check for a newer release as soon as the core reports its version,
        // and again every hour. Both are UNATTENDED checks, so they go through
        // the policy-gated entry point (FR-015) — unlike the menu's "Check for
        // Updates", which is always allowed.
        appState.$version
            .removeDuplicates()
            .filter { !$0.isEmpty }
            .first()
            .sink { [weak self] version in
                guard let self else { return }
                self.updateService.currentVersion = version
                self.updateService.checkForUpdatesInBackground()
            }
            .store(in: &cancellables)

        Timer.publish(every: 3600, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in
                guard let self, !self.appState.version.isEmpty else { return }
                self.updateService.currentVersion = self.appState.version
                self.updateService.checkForUpdatesInBackground()
            }
            .store(in: &cancellables)

        // Spec 092 FR-003: has the bundle on disk been replaced under us?
        // Checked once at launch (a drag-install over a running app that the
        // user never activates afterwards is still an upgrade that must not
        // leave the old version serving) and every 5 minutes thereafter, plus
        // on every activation — see applicationDidBecomeActive.
        refreshReplacedBundleVersion()
        Timer.publish(every: 300, on: .main, in: .common)
            .autoconnect()
            .sink { [weak self] _ in
                MainActor.assumeIsolated { self?.refreshReplacedBundleVersion() }
            }
            .store(in: &cancellables)

        // Listen for start requests from the core status banner
        NotificationCenter.default.addObserver(
            self, selector: #selector(handleStartCore),
            name: .startCore, object: nil
        )

        // Listen for open web UI requests from dashboard
        NotificationCenter.default.addObserver(
            self, selector: #selector(openWebUI),
            name: .openWebUI, object: nil
        )

        // Spec 091: the one route into the native Connect Client form. Presented
        // directly from here — no delayed notification chains, which is what
        // made the Add Server path fragile.
        NotificationCenter.default.addObserver(
            self, selector: #selector(presentConnectClientForm),
            name: ConnectClientPresentation.route, object: nil
        )

        // Start core
        Task {
            await startCore()
        }

        // Spec 044 (T055+T056): publish current autostart state to the tray
        // sidecar so the core's telemetry can emit autostart_enabled on the
        // very next heartbeat. Then — if we've never done first-run before —
        // present the first-run dialog with "Launch at login" default ON.
        //
        // Order: sidecar refresh first, so even if the user cancels the
        // dialog the core has a non-null reading.
        AutostartSidecarService.refresh()
        DispatchQueue.main.async {
            presentFirstRunDialogIfNeeded()
        }
    }

    // MARK: - NSMenuDelegate

    func menuWillOpen(_ menu: NSMenu) {
        // Only the status-bar menu drives the rebuild guard. NSMenuDelegate
        // callbacks are delivered per menu, and this object is the delegate of
        // more than one: any submenu that ends up sharing it would otherwise run
        // a full rebuild (removeAllItems) on a menu already on screen and
        // re-arm/disarm the guard under the parent — exactly the
        // restructuring-while-open the design forbids.
        guard menu === menuHost?.menu else { return }

        // Spec 048: dropped the per-click `client.servers()` fetch. appState
        // is fed by SSE (spec 047), so it's already current within ~50 ms of
        // the last upstream state change. Rebuild from in-memory state only.
        //
        // The guard is armed AFTER this rebuild: AppKit calls menuWillOpen
        // before the menu is drawn, so restructuring here is safe and hands the
        // user fresh rows. Every rebuild from this point on happens under the
        // cursor and must not add or remove items.
        rebuildMenu()
        rebuildGuard.menuWillOpen()
    }

    func menuDidClose(_ menu: NSMenu) {
        guard menu === menuHost?.menu else { return }

        // Run the structural rebuild that was suppressed while the menu was up.
        // Deferred to the next run-loop turn: AppKit is still tearing the menu
        // down inside this callback, and mutating it here is not safe.
        guard rebuildGuard.menuDidClose() else { return }
        DispatchQueue.main.async { [weak self] in
            self?.rebuildMenu()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        // Written BEFORE the core is torn down: whatever happens next, the
        // journal already carries a reason for this exit. The previous
        // behaviour — terminate the core and say nothing — is what made the
        // original incident unattributable (#862).
        AppLifecycle.shared.recordTermination()
        // Record termination intent BEFORE tearing down the core: the core will
        // exit cleanly (status 0) while the tray is still `.connected`, and only
        // this flag tells handleProcessExit that clean exit was intended rather
        // than an external kill to recover from. Set on MainActor (we are on it)
        // so it is visible before the SIGTERM is even delivered.
        appState.isTerminating = true
        // Spec 095 FR-004: a retry queued behind a terminal signal dies here.
        updateService.discardPendingFailureRetry()
        if let process = coreManager?.managedProcess {
            AppLifecycle.shared.recordCoreTerminated(
                pid: process.processIdentifier,
                reason: "tray is terminating"
            )
            process.terminate()
        }
    }

    // MARK: - Main Window

    /// Show the main application window with SwiftUI content.
    /// If the window already exists, bring it to front. Otherwise create it.
    ///
    /// - Parameter tab: Optional sidebar item to select when the window opens.
    func showMainWindow(tab: SidebarItem? = nil) {
        // isVisible is false for a miniaturized window — falling through to
        // window creation there would leave the original in the Dock and
        // spawn a duplicate, with both subscribed to the tab notifications.
        if let window = mainWindow, window.isVisible || window.isMiniaturized {
            if window.isMiniaturized { window.deminiaturize(nil) }
            NSApp.setActivationPolicy(.regular)
            setupMainMenu() // Reapply our menu when becoming regular app
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            // The window is already live, so its `onReceive` observers are
            // subscribed — a notification is the reliable path to switch tabs.
            if let tab {
                NotificationCenter.default.post(name: .switchToSidebarTab, object: tab.rawValue)
            }
            return
        }

        // Show in Dock and Cmd+Tab BEFORE presenting the window
        NSApp.setActivationPolicy(.regular)

        // Set app icon for Cmd+Tab and Dock
        if let iconPath = Bundle.main.path(forResource: "icon-128", ofType: "png"),
           let icon = NSImage(contentsOfFile: iconPath) {
            NSApp.applicationIconImage = icon
        }

        // MainWindow reads apiClient from appState, so we create it once.
        // When appState.apiClient is set by CoreProcessManager, all views
        // automatically re-render — no need to replace the NSHostingView.
        //
        // A fresh window gets its tab as initial state rather than a
        // notification: the `onReceive` observers only subscribe once the view
        // appears, so a notification posted now would be dropped on the floor.
        let contentView = MainWindow(appState: appState, initialTab: tab ?? .dashboard)
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
        setupMainMenu() // Install our menu bar when window first opens
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        mainWindow = window
    }

    @objc private func openMainWindow() {
        showMainWindow()
    }

    // Our single config window. Both the tray "Settings…" item and the app
    // menu's "Settings…" / ⌘, route here (the latter via the key monitor +
    // menu-item repoint in setupMainMenu) — never the SwiftUI Settings scene,
    // whose programmatic opening proved unreliable from a menu-bar app.
    @objc private func showSettingsWindow() {
        // Reuse the existing window if it's already open.
        if let window = settingsWindow, window.isVisible {
            NSApp.setActivationPolicy(.regular)
            setupMainMenu()
            window.makeKeyAndOrderFront(nil)
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        // A menu-bar (.accessory) app can't make a window key without first
        // becoming a regular app — same dance as showMainWindow().
        NSApp.setActivationPolicy(.regular)

        let hostingView = NSHostingView(rootView: SettingsView(appState: appState))
        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 580, height: 660),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "MCPProxy Settings"
        window.contentView = hostingView
        window.setFrameAutosaveName("MCPProxySettingsWindow")
        window.center()
        window.isReleasedWhenClosed = false
        window.delegate = self
        setupMainMenu()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)

        settingsWindow = window
    }

    /// Called from the SwiftUI Settings scene bridge: open the real config
    /// window, then close the empty SwiftUI scene window SwiftUI just created.
    func openSettingsFromScene() {
        showSettingsWindow()
        DispatchQueue.main.async {
            NSApp.windows
                .first { $0.identifier?.rawValue == "com_apple_SwiftUI_Settings_window" }?
                .close()
        }
    }

    /// Present the native Connect Client form (spec 091).
    ///
    /// Direct presentation, on purpose: the Add Server path posts a notification
    /// and then hopes two `asyncAfter` hops land in the right order, which is a
    /// race dressed as a workflow (research D5). Here the window is built and
    /// shown in this call.
    @MainActor @objc func presentConnectClientForm() {
        if connectClientForm.makeKeyIfPresenting() {
            NSApp.activate(ignoringOtherApps: true)
            return
        }

        // Resolved per call: the form may be opened before the core is up, and
        // must populate itself once it answers rather than needing a reopen.
        let appState = self.appState
        let source = DeferredConnectSource(
            transportKind: appState.apiClient?.transportKind ?? .unixSocket,
            resolve: { await MainActor.run { appState.apiClient } }
        )
        let model = ConnectClientModel(source: source)

        let window = NSWindow(
            contentRect: NSRect(x: 0, y: 0, width: 820, height: 520),
            styleMask: [.titled, .closable, .resizable],
            backing: .buffered,
            defer: false
        )
        window.title = "Connect Client"
        window.isReleasedWhenClosed = false
        window.contentView = NSHostingView(
            rootView: ConnectClientView(model: model, onClose: { [weak self] in
                self?.dismissConnectClientForm()
            })
        )
        // A sheet on the main window when there is one — the form belongs to the
        // app the user is already looking at; a standalone window otherwise,
        // because a menu-bar app often has no window at all.
        if let host = mainWindow, host.isVisible {
            // The host is recorded so that closing IT tears the form down: a
            // sheet gets no close notification of its own, and beginSheet's
            // completion never runs when the parent closes underneath it.
            connectClientForm.adopt(window, model: model, host: host)
            host.beginSheet(window) { [weak self] _ in
                self?.connectClientForm.windowWillClose(window)
            }
            return
        }

        connectClientForm.adopt(window, model: model)

        NSApp.setActivationPolicy(.regular)
        window.center()
        // Owning the delegate is what makes the red button reach the teardown.
        window.delegate = self
        setupMainMenu()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    /// Dismiss the form from its own Close button, whichever way it was shown.
    @MainActor private func dismissConnectClientForm() {
        connectClientForm.dismiss()
    }

    /// The "Add Server..." item, built in one place because it is now rendered
    /// from two: normally as the first entry of the Servers submenu, and at the
    /// top level only when there are no servers and therefore no submenu.
    ///
    /// `target` is set explicitly — an item in a submenu with a nil target would
    /// fall back to the responder chain, and the tray has none while the menu is
    /// open, so the click would silently do nothing.
    func makeAddServerItem() -> NSMenuItem {
        let item = NSMenuItem(title: "Add Server...", action: #selector(showAddServer), keyEquivalent: "n")
        item.target = self
        return item
    }

    @objc private func showAddServer() {
        showMainWindow()
        // First switch to the Servers tab so ServersView is mounted and
        // its .showAddServer notification observer is registered.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) {
            NotificationCenter.default.post(name: .switchToServers, object: nil)
        }
        // Then post the showAddServer notification after the tab switch completes
        // and ServersView has fully registered its notification observer.
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.8) {
            NotificationCenter.default.post(name: .showAddServer, object: AddServerTab.manual)
        }
    }

    // Inject our View menu items after system menu bar is ready
    func applicationDidBecomeActive(_ notification: Notification) {
        // Delay slightly to let the system finish setting up its menu bar
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.1) { [weak self] in
            self?.setupMainMenu()
        }
        // Spec 092 FR-003. Activation is the natural moment to look: a
        // drag-install is normally followed within seconds by the user
        // returning to the app.
        MainActor.assumeIsolated { refreshReplacedBundleVersion() }
    }

    // NSWindowDelegate — hide from Dock when the last managed window closes.
    func windowWillClose(_ notification: Notification) {
        // The titlebar's red button lands here and nowhere else: without this,
        // closing the Connect form that way left its model and its 2 s
        // reachability poll alive inside a retained, invisible window.
        MainActor.assumeIsolated {
            connectClientForm.windowWillClose(notification.object as? NSWindow)
        }
        // Defer so the closing window has already left the visible set.
        DispatchQueue.main.async { [weak self] in self?.restoreAccessoryIfNoVisibleWindows() }
    }

    // Drop back to a menu-bar-only (.accessory) app once no real window remains.
    // Covers both the AppKit main window (delegate) and the SwiftUI Settings
    // scene window (which we don't own — handled via a global close observer).
    private func restoreAccessoryIfNoVisibleWindows() {
        let anyVisible = NSApp.windows.contains { win in
            win.isVisible && win.styleMask.contains(.titled) && !(win is NSPanel)
        }
        if !anyVisible {
            NSApp.setActivationPolicy(.accessory)
        }
    }

    // MARK: - Main Menu Bar (View > Text Size)

    private func setupMainMenu() {
        guard let mainMenu = NSApp.mainMenu else { return }

        // Route a CLICK on the app-menu "Settings…" item to our config window
        // (the ⌘, keyboard shortcut is intercepted separately in the key
        // monitor). Both bypass the empty SwiftUI `Settings {}` scene.
        if let appMenu = mainMenu.item(at: 0)?.submenu {
            for item in appMenu.items where item.title.hasPrefix("Settings") || item.title.hasPrefix("Preferences") {
                item.target = self
                item.action = #selector(showSettingsWindow)
                item.keyEquivalent = ","
                item.keyEquivalentModifierMask = .command
            }
        }

        // Find or create View menu and add text size items
        let viewMenu: NSMenu
        if let existingViewItem = mainMenu.item(withTitle: "View"),
           let existingMenu = existingViewItem.submenu {
            viewMenu = existingMenu
        } else {
            viewMenu = NSMenu(title: "View")
            let viewMenuItem = NSMenuItem()
            viewMenuItem.submenu = viewMenu
            // Insert before Window menu
            let insertIndex = max(0, mainMenu.numberOfItems - 2)
            mainMenu.insertItem(viewMenuItem, at: insertIndex)
        }

        // Only add our items if not already present
        if viewMenu.item(withTitle: "Make Text Bigger") == nil {
            viewMenu.insertItem(.separator(), at: 0)

            let actualItem = NSMenuItem(title: "Actual Size", action: #selector(makeTextActualSize), keyEquivalent: "0")
            actualItem.keyEquivalentModifierMask = .command
            actualItem.target = self
            viewMenu.insertItem(actualItem, at: 0)

            let smallerItem = NSMenuItem(title: "Make Text Smaller", action: #selector(makeTextSmaller), keyEquivalent: "-")
            smallerItem.keyEquivalentModifierMask = .command
            smallerItem.target = self
            viewMenu.insertItem(smallerItem, at: 0)

            // Use "=" as key equivalent so Cmd+= (without Shift) triggers zoom in.
            // On US keyboards, the + key is Shift+=, so "+" requires Cmd+Shift+=.
            // Using "=" matches the standard macOS zoom-in shortcut (Cmd+=).
            // The local event monitor (above) handles both "+" and "=" as fallback.
            let biggerItem = NSMenuItem(title: "Make Text Bigger", action: #selector(makeTextBigger), keyEquivalent: "=")
            biggerItem.keyEquivalentModifierMask = .command
            biggerItem.target = self
            viewMenu.insertItem(biggerItem, at: 0)
        }

        // Add Edit menu if not present (for Cmd+C copy)
        if mainMenu.item(withTitle: "Edit") == nil {
            let editMenuItem = NSMenuItem()
            let editMenu = NSMenu(title: "Edit")
            editMenu.addItem(withTitle: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c")
            editMenu.addItem(withTitle: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a")
            editMenuItem.submenu = editMenu
            mainMenu.insertItem(editMenuItem, at: 2) // After Apple + App menus
        }
    }

    @objc private func makeTextBigger() {
        appState.fontScale = min(appState.fontScale + 0.1, 2.0)
        NSLog("[MCPProxy] Font scale: %.0f%%", appState.fontScale * 100)
    }

    @objc private func makeTextSmaller() {
        appState.fontScale = max(appState.fontScale - 0.1, 0.6)
        NSLog("[MCPProxy] Font scale: %.0f%%", appState.fontScale * 100)
    }

    @objc private func makeTextActualSize() {
        appState.fontScale = 1.0
        NSLog("[MCPProxy] Font scale: 100%%")
    }

    // MARK: - Core Startup

    /// Bring up the core on app launch.
    ///
    /// GH #410: `maySpawn` is the user's "Start Core when app opens" preference.
    /// When it is off the manager still ATTACHES to a core that is already
    /// running — it just will not start one, and idles watching for one instead.
    private func startCore() async {
        await notificationService.setup()

        let policy = CoreLaunchPolicy()
        await MainActor.run {
            appState.autoStartEnabled = AutoStartService.isEnabled
            // NOTE: do NOT assign appState.startCoreOnLaunch here. AppState already
            // initializes it from CoreLaunchPolicy, and its didSet WRITES to
            // UserDefaults — so a redundant sync would materialize the key on
            // every launch, including for users who only set MCPPROXY_TRAY_SKIP_CORE
            // and never touched the preference.
        }

        if SymlinkService.needsSetup() {
            if let bundledBinary = resolveBundledCoreBinary() {
                await SymlinkService.updateSymlinkIfNeeded(bundledBinary: bundledBinary)
            }
        }

        // Spec 092 FR-030: bring the legacy staged core copy up to date if it
        // is provably older than the bundled one. Detached because it can cost
        // two subprocesses and a ~30 MB copy, and nothing in the startup path
        // depends on it — this tray resolves the bundled core first (see
        // StagedCoreBinary's header for the full path analysis).
        Task.detached(priority: .utility) {
            StagedCoreBinary.refreshIfStale()
        }

        let manager = CoreProcessManager(
            appState: appState,
            notificationService: notificationService,
            socketPath: Self.socketPathOverride
        )
        coreManager = manager
        await manager.start(maySpawn: policy.maySpawnCore)
    }

    /// Dev/QA-only escape hatch: point the app at a non-default core socket
    /// (e.g. an isolated scratch core) without touching ~/.mcpproxy. The
    /// CoreProcessManager initializer has always taken an injectable
    /// socketPath "so a test (or a second app)" can use it; this is the
    /// second-app case. Unset in normal use, so behavior is unchanged.
    ///
    /// Nil now means "whatever the instance root says", not "the real
    /// ~/.mcpproxy": `MCPPROXY_HOME` moves the socket along with everything
    /// else (GH #936), and `InstancePaths` is where the two are reconciled.
    static var socketPathOverride: String? {
        ProcessInfo.processInfo.environment[InstancePaths.socketPathEnvVar]
    }

    /// Announce a relocated instance once, at launch, and say so loudly if the
    /// socket it implies cannot be bound.
    ///
    /// Both lines are diagnostics for a human running a second instance on
    /// purpose. The length check in particular: over 103 bytes the bind fails
    /// silently and the tray simply never finds a core, which costs an hour to
    /// work out from the outside.
    static func logInstanceRoot() {
        guard InstancePaths.isOverridden else { return }
        NSLog("[MCPProxy] Instance root overridden by %@: %@ (socket %@)",
              InstancePaths.rootEnvVar, InstancePaths.root.path, InstancePaths.socketPath)
        if let problem = InstancePaths.socketPathProblem(InstancePaths.socketPath) {
            NSLog("[MCPProxy] %@", problem)
        }
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

    /// Update the status bar icon based on app state.
    ///
    /// The icon itself is always the plain MCPProxy template glyph; the state
    /// rides beside it as a coloured glyph in the button's attributed title.
    /// See `TrayStatusIcon.glyph(for:)` for why the badge cannot be composited
    /// into the image.
    ///
    /// - Running OK: plain MCPProxy icon, no glyph
    /// - Stopped: ⏹
    /// - Core error: ⚠
    /// - Server warn/error diagnostics: an amber/red dot (F1 — before the audit
    ///   the menu bar looked identical to all-healthy with five servers down)
    ///
    /// F2: every pass also publishes the state as text — accessibility
    /// description, accessibility label and tooltip — so a screen-reader user
    /// hears more than "status menu" and the glyphs have a text alternative.
    private func updateStatusIcon() {
        guard let button = statusItem?.button else { return }

        // Always start with the MCPProxy base icon
        let base: NSImage
        if let iconPath = Bundle.main.path(forResource: "icon-mono-44", ofType: "png"),
           let bundledIcon = NSImage(contentsOfFile: iconPath) {
            base = bundledIcon
        } else if let sfIcon = NSImage(systemSymbolName: "server.rack", accessibilityDescription: "MCPProxy") {
            base = sfIcon
        } else { return }

        let isStopped = appState.isStopped
        let hasError: Bool
        if case .error = appState.coreState { hasError = true } else { hasError = false }
        let badge = TrayStatusIcon.badge(
            isStopped: isStopped,
            hasCoreError: hasError,
            worstDiagnosticSeverity: appState.worstDiagnosticSeverity
        )
        // The count must be of the severity being badged, over the same
        // (enabled, non-OAuth) set `worstDiagnosticSeverity` considers.
        let badgedCount: Int
        if case .severity(let severity) = badge {
            badgedCount = appState.diagnosticCount(severity: severity.rawValue)
        } else {
            badgedCount = 0
        }
        let label = TrayStatusIcon.accessibilityLabel(
            for: badge,
            summary: appState.statusSummary,
            attentionCount: badgedCount
        )

        // Always use template icon (pure black, adapts to light/dark menu bar)
        base.isTemplate = true
        base.size = NSSize(width: 18, height: 18)
        base.accessibilityDescription = label
        button.image = base

        // Show state indicator as text next to icon (keeps icon as pure template)
        let glyph = TrayStatusIcon.glyph(for: badge)
        if glyph.isEmpty {
            button.attributedTitle = NSAttributedString(string: "")
            button.title = ""
        } else {
            // Only the severity dot is coloured; ⏹/⚠ keep the menu bar's own
            // label colour so they stay legible in both appearances.
            var attributes: [NSAttributedString.Key: Any] = [
                .font: NSFont.systemFont(ofSize: 11)
            ]
            if case .severity(let severity) = badge {
                attributes[.foregroundColor] = severity == .error ? NSColor.systemRed : NSColor.systemOrange
            }
            button.attributedTitle = NSAttributedString(string: " " + glyph, attributes: attributes)
        }

        button.toolTip = label
        button.setAccessibilityLabel(label)
    }

    // MARK: - Menu Building (AppKit NSMenu — no SwiftUI)

    /// Rebuild the entire NSMenu from current appState.
    /// Clears and rebuilds in-place to avoid replacing the menu object
    /// (which would close an already-open menu and lose the delegate).
    ///
    /// `@MainActor` because Tray Glance rewrites menu items that are already on
    /// screen, which is only safe on the thread AppKit draws them on. Every
    /// caller was already main-thread — the NSMenuDelegate/NSApplicationDelegate
    /// callbacks are isolated by the SDK and the objectWillChange sink debounces
    /// on `menuRefreshScheduler` (the main queue) — so this costs nothing today
    /// and rejects a future off-main caller at compile time (see the note on
    /// `MenuRebuildGuard.decide(refreshing:from:now:)` for how far that reaches
    /// under this package's concurrency checking).
    ///
    /// Internal rather than private only so `MenuOpenNetworkTests` can drive the
    /// real open sequence — `menuWillOpen` → rebuild → in-place update →
    /// `menuDidClose` — instead of re-implementing it beside the code, which is
    /// how the previous version of that test came to assert nothing at all.
    @MainActor
    func rebuildMenu() {
        // Tray Glance: while the menu is on screen its structure must not move
        // under the cursor. The rows are rewritten in place; a structural change
        // is suppressed here and re-run from menuDidClose. Non-glance sections
        // are frozen for the same reason, and menuWillOpen rebuilds the whole
        // menu before it is drawn, so the next open is never stale.
        switch rebuildGuard.decide(refreshing: glance, from: appState) {
        case .updateInPlace, .deferUntilClose:
            return
        case .rebuild:
            break
        }

        guard let menuHost else { return }

        let menu: NSMenu
        if let existing = menuHost.menu {
            existing.removeAllItems()
            menu = existing
        } else {
            menu = NSMenu()
            menu.delegate = self
            menuHost.menu = menu
        }

        // Header with colored status dot
        let ver = appState.version.hasPrefix("v") ? appState.version : "v\(appState.version)"
        let title = appState.version.isEmpty ? "MCPProxy" : "MCPProxy \(ver)"
        let titleItem = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        titleItem.isEnabled = false
        let font = NSFont.boldSystemFont(ofSize: 13)
        titleItem.attributedTitle = NSAttributedString(string: title, attributes: [.font: font])

        // Determine status dot color
        let statusColor: NSColor
        if appState.isStopped {
            statusColor = .systemGray
        } else if case .error = appState.coreState {
            statusColor = .systemRed
        } else if appState.coreState == .connected {
            if appState.serversNeedingAttention.isEmpty {
                statusColor = .systemGreen
            } else {
                statusColor = .systemYellow
            }
        } else {
            // Launching, waitingForCore, reconnecting, idle
            statusColor = .systemYellow
        }

        let dotSize = NSSize(width: 10, height: 10)
        let dot = NSImage(size: dotSize, flipped: false) { rect in
            statusColor.setFill()
            NSBezierPath(ovalIn: rect.insetBy(dx: 1, dy: 1)).fill()
            return true
        }
        titleItem.image = dot
        menu.addItem(titleItem)

        let summary = NSMenuItem(title: appState.statusSummary, action: nil, keyEquivalent: "")
        summary.isEnabled = false
        menu.addItem(summary)

        // Error state
        if case .error(let coreError) = appState.coreState {
            let errorItem = NSMenuItem(title: coreError.userMessage, action: nil, keyEquivalent: "")
            errorItem.isEnabled = false
            errorItem.image = NSImage(systemSymbolName: "exclamationmark.triangle.fill", accessibilityDescription: "error")
            menu.addItem(errorItem)

            let hintItem = NSMenuItem(title: coreError.remediationHint, action: nil, keyEquivalent: "")
            hintItem.isEnabled = false
            menu.addItem(hintItem)

            if coreError.isRetryable {
                let retryItem = NSMenuItem(title: "Retry", action: #selector(retryCore), keyEquivalent: "")
                retryItem.target = self
                retryItem.image = NSImage(systemSymbolName: "arrow.clockwise", accessibilityDescription: "retry")
                menu.addItem(retryItem)
            }
        }

        menu.addItem(.separator())

        // Tray Glance — recent tool calls, connected clients, 24h histogram.
        // Hidden entirely when the core is not connected: `items(for:)` returns
        // [] and this loop adds nothing. GlanceSection keeps references to the
        // rows it hands back, so a later in-place update writes to these very
        // items — which is why they are added, not copied.
        for row in glance.items(for: appState) {
            menu.addItem(row)
        }

        // Needs Attention — only auth required, connection errors, quarantine
        // (NOT disabled). One collapsed row: the count is the glanceable fact,
        // the per-server detail is a hover away, and N servers no longer cost
        // N rows of a menu that opens with a chart. Absent entirely when
        // nothing needs attention.
        let attentionServers = appState.serversNeedingAttention
        if !attentionServers.isEmpty {
            let parent = NSMenuItem(title: "Needs Attention (\(attentionServers.count))",
                                    action: nil, keyEquivalent: "")
            parent.image = NSImage(systemSymbolName: "exclamationmark.triangle",
                                   accessibilityDescription: "needs attention")
            let submenu = NSMenu(title: "Needs Attention")

            for server in attentionServers {
                let action = server.health?.action ?? ""
                let summary = server.health?.summary ?? ""
                let icon = actionIcon(for: action)

                let fullTitle = "\(server.name) — \(summary.isEmpty ? actionDisplayName(for: action) : summary)"
                // Same width discipline as the glance rows: an untruncated core
                // error must not stretch the whole menu past the chart block.
                // The full text stays in the tooltip.
                let title = GlanceFormatting.tailTruncated(
                    fullTitle, limit: GlanceFormatting.reasonBudget)

                // F4: these rows used to run `health.action` on click — a row
                // reading "demo-filesystem — failed to connect" silently
                // RESTARTED the server, and an `enable`-actioned row enabled
                // one. Nothing in the label said so, nothing confirmed it and
                // (F3) nothing reported failure. A row that reads as
                // disclosure now navigates and nothing else; the action moves
                // into a submenu under its own verb, matching the explicit
                // verbs already used under `Servers ▸`.
                let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
                item.toolTip = fullTitle
                // Truncated on screen, spoken in full — tooltips are not read
                // by VoiceOver (same FR-025 discipline as the glance rows).
                item.setAccessibilityLabel(fullTitle)
                item.image = NSImage(systemSymbolName: icon, accessibilityDescription: action)

                if let verb = TrayServerAction.fromHealthAction(action) {
                    let rowMenu = NSMenu(title: server.name)

                    let act = NSMenuItem(title: verb.menuTitle,
                                         action: #selector(performAttentionAction(_:)),
                                         keyEquivalent: "")
                    act.target = self
                    act.representedObject = server
                    act.image = NSImage(systemSymbolName: actionIcon(for: action),
                                        accessibilityDescription: action)
                    rowMenu.addItem(act)
                    rowMenu.addItem(.separator())

                    let details = NSMenuItem(title: "Open Server Details",
                                             action: #selector(showServerDetailFromMenu(_:)),
                                             keyEquivalent: "")
                    details.target = self
                    details.representedObject = server.name
                    rowMenu.addItem(details)

                    item.submenu = rowMenu
                } else {
                    // Nothing to run — quarantine review, a missing secret, a
                    // configuration problem. Straight to the detail view.
                    item.action = #selector(showServerDetailFromMenu(_:))
                    item.target = self
                    item.representedObject = server.name
                }
                submenu.addItem(item)
            }
            parent.submenu = submenu
            menu.addItem(parent)
            menu.addItem(.separator())
        }

        // No standalone quarantine line: "N quarantined server(s)" was a
        // disabled, grey, non-actionable row, and the same fact is already
        // carried — actionably — by the Needs Attention submenu above. The menu
        // had grown long enough to scroll, and an informational row that cannot
        // be clicked is the first thing to spend.

        // Servers — as a SUBMENU (not flat list)
        if !appState.servers.isEmpty {
            let serversMenuItem = NSMenuItem(title: "Servers (\(appState.servers.count))", action: nil, keyEquivalent: "")
            let serversSubmenu = NSMenu()

            // "Add Server..." lives HERE, not at the top level: adding a server
            // is a servers-menu action, and hoisting it out cost a permanent
            // top-level row in a menu that had started to scroll. The Cmd+N key
            // equivalent still works from a submenu — AppKit walks the whole menu
            // tree when matching key equivalents — and still displays beside the
            // title once the submenu is open.
            serversSubmenu.addItem(makeAddServerItem())
            serversSubmenu.addItem(.separator())

            // F15: 29 flat alphabetical entries — 13 of them disabled, 3
            // quarantined, interleaved and told apart only by dot colour —
            // made a submenu taller than the screen. Attention first, then the
            // servers that are working, then the disabled tail behind one row.
            let attentionNames = Set(attentionServers.map(\.name))
            var grouped: [TrayServerGroup: [ServerStatus]] = [:]
            for server in appState.servers {
                let group = TrayServerGrouping.group(
                    enabled: server.enabled,
                    needsAttention: attentionNames.contains(server.name))
                grouped[group, default: []].append(server)
            }

            for server in (grouped[.needsAttention] ?? []) + (grouped[.active] ?? []) {
                serversSubmenu.addItem(makeServerMenuItem(server))
            }

            let disabledServers = grouped[.disabled] ?? []
            if !disabledServers.isEmpty {
                if disabledServers.count >= TrayServerGrouping.disabledFoldThreshold {
                    let fold = NSMenuItem(title: "Disabled (\(disabledServers.count))",
                                          action: nil, keyEquivalent: "")
                    let foldMenu = NSMenu(title: "Disabled")
                    for server in disabledServers {
                        foldMenu.addItem(makeServerMenuItem(server))
                    }
                    fold.submenu = foldMenu
                    serversSubmenu.addItem(.separator())
                    serversSubmenu.addItem(fold)
                } else {
                    // Below the fold threshold the extra click costs more than
                    // the rows it saves.
                    for server in disabledServers {
                        serversSubmenu.addItem(makeServerMenuItem(server))
                    }
                }
            }

            serversMenuItem.submenu = serversSubmenu
            menu.addItem(serversMenuItem)
            menu.addItem(.separator())
        }

        // Profile switcher (Profiles v2 T5) — only shown when profiles are
        // configured. Lists "All servers" (clears the profile) plus each profile
        // with its tool count; the active selection carries a checkmark. Clicking
        // switches the server-level default active profile via REST; a switch made
        // by another client arrives over SSE (`active_profile.changed`) and
        // repaints this submenu.
        if !appState.profiles.isEmpty {
            let activeLabel = appState.activeProfile.isEmpty ? "All servers" : appState.activeProfile
            let profileMenuItem = NSMenuItem(title: "Profile: \(activeLabel)", action: nil, keyEquivalent: "")
            let profileSubmenu = NSMenu()

            let allItem = NSMenuItem(title: "All servers", action: #selector(switchProfile(_:)), keyEquivalent: "")
            allItem.target = self
            allItem.representedObject = ""
            allItem.state = appState.activeProfile.isEmpty ? .on : .off
            profileSubmenu.addItem(allItem)
            profileSubmenu.addItem(.separator())

            // F11: the tray showed only a tool count, so a profile whose
            // servers are not in the config read as "empty" rather than
            // "switching to this scopes every agent to nothing".
            let knownServers = Set(appState.servers.map(\.name))
            for profile in appState.profiles {
                let title = TrayProfileDisplay.label(
                    name: profile.name,
                    servers: profile.servers,
                    toolCount: profile.toolCount,
                    knownServers: knownServers)
                let item = NSMenuItem(title: title,
                                      action: #selector(switchProfile(_:)), keyEquivalent: "")
                item.target = self
                item.representedObject = profile.name
                item.state = profile.name == appState.activeProfile ? .on : .off
                if profile.servers.filter({ knownServers.contains($0) }).isEmpty {
                    item.toolTip = "None of this profile’s servers (\(profile.servers.joined(separator: ", "))) "
                        + "are in the configuration. Switching to it would leave agents with no tools."
                }
                profileSubmenu.addItem(item)
            }

            profileMenuItem.submenu = profileSubmenu
            menu.addItem(profileMenuItem)
            menu.addItem(.separator())
        }

        // Actions
        //
        // "Add Server..." normally lives inside the Servers submenu. The one
        // exception is a proxy with no servers at all: there is no Servers
        // submenu to hold it then, and the very user who most needs to add one
        // would be left without a way to do it from the tray.
        if appState.servers.isEmpty {
            menu.addItem(makeAddServerItem())
        }

        // Spec 091 FR-001: the native connect journey, beside Add Server. The
        // item is built by its router so the item and its routing are tested
        // together (a nil target would make it silently do nothing).
        menu.addItem(ConnectClientMenuRouter.shared.makeMenuItem())

        // F9: "Open MCPProxy..." and "Open Web UI" never said that one is a
        // native window and the other a browser tab.
        let openApp = NSMenuItem(title: "Open MCPProxy Window", action: #selector(openMainWindow), keyEquivalent: "")
        openApp.target = self
        menu.addItem(openApp)

        let settingsItem = NSMenuItem(title: "Settings...", action: #selector(showSettingsWindow), keyEquivalent: ",")
        settingsItem.target = self
        menu.addItem(settingsItem)

        let webUI = NSMenuItem(title: "Open Web UI in Browser", action: #selector(openWebUI), keyEquivalent: "")
        webUI.target = self
        menu.addItem(webUI)

        menu.addItem(.separator())

        // F9: the same AutoStartService setting is called "Launch MCPProxy at
        // login" in Settings → App. One setting, one name — and a tooltip that
        // separates it from "Start MCPProxy Core when the app opens", the
        // genuinely confusable neighbour one row from "Start/Stop MCPProxy
        // Core" (which means *now*, not at launch).
        let autoStart = NSMenuItem(title: "Launch at Login", action: #selector(toggleAutoStart(_:)), keyEquivalent: "")
        autoStart.target = self
        autoStart.state = appState.autoStartEnabled ? .on : .off
        autoStart.toolTip = "Starts the MCPProxy app when you log in. Whether it also starts the core is "
            + "“Start MCPProxy Core when the app opens” in Settings → App."
        menu.addItem(autoStart)

        let checkUpdates = NSMenuItem(title: "Check for Updates", action: #selector(checkForUpdates), keyEquivalent: "")
        checkUpdates.target = self
        checkUpdates.isEnabled = updateService.canCheckForUpdates
        menu.addItem(checkUpdates)

        // Spec 092 FR-017: exactly one source of truth owns the update item.
        // The core's cached result (`appState.updateAvailable`) is merged into
        // the service's legacy version first, so the resolver sees ONE legacy
        // input and one feed input — the two used to be rendered independently,
        // which is how a single release produced two competing menu items.
        updateService.setCoreReportedVersion(appState.updateAvailable)
        for entry in updateService.menuEntries {
            switch entry {
            case .oneClick(let version):
                // FR-010's exact shape: gentle, and honest about what happens.
                let item = NSMenuItem(
                    title: "Update \(version) — ready to restart?",
                    action: #selector(installFeedUpdate), keyEquivalent: ""
                )
                item.target = self
                item.toolTip = "Downloads and verifies the update, stops the core, "
                    + "replaces MCPProxy and relaunches it."
                menu.addItem(item)

            case .browserGuidance(let version):
                let item = NSMenuItem(
                    title: "Update available: v\(version) — Download",
                    action: #selector(openDownloadPage), keyEquivalent: ""
                )
                item.target = self
                item.toolTip = "Opens the download page. This version cannot be installed "
                    + "from here."
                menu.addItem(item)

            case .blocked(let reason):
                // FR-016: never silent. The title says it, the action explains.
                let item = NSMenuItem(
                    title: reason.menuTitle,
                    action: #selector(showUpdateBlockedReason), keyEquivalent: ""
                )
                item.target = self
                item.toolTip = reason.explanation
                menu.addItem(item)
            }
        }

        // Spec 092 FR-003: the app on disk is newer than the one running — a
        // drag-install landed underneath us. Offered, never forced.
        if let replacement = appState.replacedBundleVersion {
            let relaunch = NSMenuItem(
                title: "MCPProxy was updated to v\(replacement) — Relaunch",
                action: #selector(relaunchIntoReplacedBundle), keyEquivalent: ""
            )
            relaunch.target = self
            relaunch.toolTip = "Stops the core, starts the newly installed app, and quits this one."
            menu.addItem(relaunch)
        }

        // Spec 092 FR-002: an older core is running that the tray is not
        // allowed to stop on its own. Activating this item IS the consent.
        if let stale = appState.staleCorePrompt {
            let restart = NSMenuItem(
                title: stale.menuTitle,
                action: #selector(restartStaleCore), keyEquivalent: ""
            )
            restart.target = self
            restart.toolTip = stale.pid == nil
                ? "This core cannot be stopped from here — shows how to stop it by hand."
                : "Stops the old core (PID \(stale.pid!)) and starts the bundled one."
            menu.addItem(restart)
        }

        menu.addItem(.separator())

        // Stop / Start
        // No icons on the lifecycle commands: every other command in the
        // bottom half of the menu (Add Server…, Settings…, Run at Startup,
        // Quit) is a bare title, and a per-item image indents only its own
        // title — an icon here put "Stop MCPProxy Core" on a different
        // leading edge from its neighbours.
        if appState.isStopped {
            let start = NSMenuItem(title: "Start MCPProxy Core", action: #selector(startCoreAction), keyEquivalent: "")
            start.target = self
            menu.addItem(start)
        } else if appState.coreState == .connected || appState.coreState.isOperational {
            // A core we only attached to cannot be stopped by us — we hold no PID
            // for it and the core has no shutdown endpoint. Say "Disconnect", and
            // mean it (#410).
            let ownership = appState.ownership
            let stop = NSMenuItem(title: ownership.stopActionTitle, action: #selector(stopCore), keyEquivalent: "")
            stop.target = self
            if !ownership.shouldTerminateOnShutdown {
                stop.toolTip = "This core was started outside MCPProxy. Disconnecting leaves it running."
            }
            menu.addItem(stop)
        }

        // Help / project links (discussion #948): a running-app user must always
        // have a way back to the homepage, docs, and issue tracker.
        menu.addItem(.separator())

        let docsItem = NSMenuItem(title: "Documentation", action: #selector(openDocumentation), keyEquivalent: "")
        docsItem.target = self
        menu.addItem(docsItem)

        // No "Report an Issue…" item: the menu had grown long enough to scroll
        // on a normal display, and the tracker is still reachable through the
        // GitHub link in the About panel's credits block.

        let aboutItem = NSMenuItem(title: "About MCPProxy", action: #selector(showAboutPanel), keyEquivalent: "")
        aboutItem.target = self
        menu.addItem(aboutItem)

        // Quit
        menu.addItem(.separator())
        let quit = NSMenuItem(title: "Quit MCPProxy", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

    }

    /// One row of the Servers submenu: the server's name, a health dot, and a
    /// submenu of the actions that apply to it.
    ///
    /// Internal so the menu tests can build a row without walking the whole
    /// menu, and because F15's grouping calls it from three places.
    @MainActor
    func makeServerMenuItem(_ server: ServerStatus) -> NSMenuItem {
        let item = NSMenuItem(title: server.name, action: nil, keyEquivalent: "")

        // Status icon: colored dot. The OAuth login-required state is a
        // calm, actionable affordance (MCP-1822) — `menuStatusNSColor`
        // gives it the system accent tint instead of the red error dot +
        // red lock badge that previously framed sign-in as a hard failure.
        let needsAuth = server.isOAuthLoginRequired
        let dotColor = server.menuStatusNSColor

        let iconSize = NSSize(width: 16, height: 16)
        let icon = NSImage(size: iconSize, flipped: false) { _ in
            // Draw health dot
            let dotRect = NSRect(x: 2, y: 4, width: 8, height: 8)
            dotColor.setFill()
            NSBezierPath(ovalIn: dotRect).fill()
            return true
        }
        item.image = icon

        // Per-server submenu with actions
        let sub = NSMenu()
        let statusText = server.health?.summary ?? (server.connected ? "Connected" : server.enabled ? "Disconnected" : "Disabled")
        let statusLine = NSMenuItem(title: statusText, action: nil, keyEquivalent: "")
        statusLine.isEnabled = false
        sub.addItem(statusLine)

        // Protocol info — display-normalised (F12). `streamable-http` beside
        // `http` and `sse` is three wire spellings of one transport family.
        let protoLine = NSMenuItem(title: "Protocol: \(TrayProtocolDisplay.label(for: server.protocol))",
                                   action: nil, keyEquivalent: "")
        protoLine.isEnabled = false
        sub.addItem(protoLine)

        sub.addItem(.separator())

        // OAuth sign-in — calm, actionable affordance shown first when
        // login is required (MCP-1822), not error framing.
        if needsAuth {
            let login = NSMenuItem(title: TrayServerAction.login.menuTitle,
                                   action: #selector(loginServer(_:)), keyEquivalent: "")
            login.target = self
            login.representedObject = server.name
            login.image = NSImage(systemSymbolName: "person.badge.key", accessibilityDescription: "sign in")
            sub.addItem(login)
            sub.addItem(.separator())
        }

        // F8(a): a quarantined server offered only Disable · Restart · View
        // Logs — the one thing it needs is a review, and the menu had no path
        // to it at all. Deep-links to Server Detail, which opens on Tools with
        // the quarantine banner.
        if server.quarantined {
            let review = NSMenuItem(title: TrayServerAction.approve.menuTitle,
                                    action: #selector(showServerDetailFromMenu(_:)), keyEquivalent: "")
            review.target = self
            review.representedObject = server.name
            review.image = NSImage(systemSymbolName: "checkmark.shield", accessibilityDescription: "review quarantine")
            sub.addItem(review)
            sub.addItem(.separator())
        }

        // F7: one config write, one verb pair. `Stop`/`Start` for stdio and
        // `Disable`/`Enable` for everything else put two mental models —
        // transient process control vs. persistent admin state — on the same
        // `enabled` flag, and left submenus reading "Disabled … Start".
        if server.enabled {
            let disable = NSMenuItem(title: TrayServerAction.disable.menuTitle,
                                     action: #selector(disableServer(_:)), keyEquivalent: "")
            disable.target = self
            disable.representedObject = server.name
            sub.addItem(disable)
        } else {
            let enable = NSMenuItem(title: TrayServerAction.enable.menuTitle,
                                    action: #selector(enableServer(_:)), keyEquivalent: "")
            enable.target = self
            enable.representedObject = server.name
            sub.addItem(enable)
        }

        let restart = NSMenuItem(title: TrayServerAction.restart.menuTitle,
                                 action: #selector(restartServer(_:)), keyEquivalent: "")
        restart.target = self
        restart.representedObject = server.name
        sub.addItem(restart)

        sub.addItem(.separator())

        let logs = NSMenuItem(title: "View Logs", action: #selector(viewServerLogs(_:)), keyEquivalent: "")
        logs.target = self
        logs.representedObject = server.name
        sub.addItem(logs)

        item.submenu = sub
        return item
    }

    // MARK: - Menu Actions

    @objc private func retryCore() {
        Task { await coreManager?.retry() }
    }

    @objc private func stopCore() {
        // Only a core WE spawned gets signalled. For an attached core this is a
        // disconnect: tear down our clients and leave the core alone (#410).
        let ownsCore = appState.ownership.shouldTerminateOnShutdown
        NSLog("[MCPProxy] stopCore: ownership=%@", ownsCore ? "tray-managed" : "external-attached")
        appState.isStopped = true

        // Kill the core process directly — most reliable method
        let proc = ownsCore ? coreManager?.managedProcess : nil
        NSLog("[MCPProxy] stopCore: managedProcess=%@, isRunning=%@",
              proc != nil ? "exists" : "nil",
              proc?.isRunning == true ? "yes" : "no")

        if let process = proc, process.isRunning {
            NSLog("[MCPProxy] stopCore: sending SIGTERM to PID %d", process.processIdentifier)
            kill(process.processIdentifier, SIGTERM)

            // Wait up to 5s then SIGKILL
            DispatchQueue.global().asyncAfter(deadline: .now() + 5) {
                if process.isRunning {
                    NSLog("[MCPProxy] stopCore: SIGKILL after 5s timeout")
                    kill(process.processIdentifier, SIGKILL)
                }
            }
        }

        // Also call shutdown for cleanup (SSE, API client, etc.)
        Task {
            await coreManager?.shutdown()
            await MainActor.run {
                appState.coreState = .idle
                appState.servers = []
                appState.connectedCount = 0
                appState.totalServers = 0
                appState.totalTools = 0
                appState.serversLoaded = false
                appState.apiClient = nil
                updateStatusIcon()
                rebuildMenu()
            }
        }
    }

    @objc private func startCoreAction() {
        Task {
            appState.isStopped = false
            // Retire the outgoing manager first. In idle mode it is still polling
            // for a core to attach to, and it would otherwise find the core the
            // NEW manager is about to spawn and label it "external" (#410).
            await coreManager?.supersede()

            let manager = CoreProcessManager(
                appState: appState,
                notificationService: notificationService,
                socketPath: Self.socketPathOverride
            )
            coreManager = manager
            // An explicit "Start MCPProxy Core" always spawns, whatever the
            // autostart preference says — the preference governs app LAUNCH, and
            // the user is asking for a core right now (#410).
            await manager.start(maySpawn: true)
            updateStatusIcon()
        }
    }

    /// Handler for the `.startCore` notification posted by the core status banner.
    @objc private func handleStartCore() {
        startCoreAction()
    }

    // MARK: - Spec 092 Phase 0: superseding stale versions (#957)

    /// Re-read the app bundle from disk and publish whether it has been
    /// replaced by a newer version (FR-003).
    ///
    /// Cheap (one small plist read) and idempotent, which is what lets it run
    /// on both triggers: every activation — the drag-install is usually
    /// followed immediately by clicking the menu bar — and a slow timer for the
    /// user who never activates the app at all.
    @MainActor
    private func refreshReplacedBundleVersion() {
        let replacement = BundleUpdateWatcher.replacementVersion()
        guard appState.replacedBundleVersion != replacement else { return }
        if let replacement {
            NSLog("[MCPProxy] The app bundle on disk is v%@ — this process is v%@",
                  replacement, BundledCore.appVersion() ?? "unknown")
            AppLifecycle.shared.note("app bundle on disk replaced by v\(replacement)")
        }
        appState.replacedBundleVersion = replacement
    }

    /// FR-003: stop the core we manage, launch the newly installed bundle, and
    /// get out of its way.
    ///
    /// `open -n` rather than `open`: without it macOS activates THIS process —
    /// the stale one — which is exactly the reported symptom. Terminating comes
    /// last and only after the core is down, so the new instance does not race
    /// us for the socket and the BBolt lock.
    @objc private func relaunchIntoReplacedBundle() {
        let bundlePath = Bundle.main.bundleURL.path
        Task { [weak self] in
            guard let self else { return }
            AppLifecycle.shared.note("relaunching into the replaced bundle at \(bundlePath)")
            await self.coreManager?.shutdown()

            await MainActor.run {
                let launcher = Process()
                launcher.executableURL = URL(fileURLWithPath: "/usr/bin/open")
                launcher.arguments = ["-n", bundlePath]
                do {
                    try launcher.run()
                } catch {
                    NSLog("[MCPProxy] Could not launch %@: %@", bundlePath, error.localizedDescription)
                    self.presentAlert(
                        title: "Could not start the new version",
                        message: "Open \(bundlePath) manually to finish the upgrade.\n\n"
                            + error.localizedDescription
                    )
                    return
                }
                NSApp.terminate(nil)
            }
        }
    }

    /// FR-002: the user consented to stopping a core the tray did not start.
    ///
    /// When there is no pid to act on — a core too old to report one — the
    /// action must still do something honest, so it explains how to stop the
    /// core by hand rather than failing silently.
    @objc private func restartStaleCore() {
        guard let prompt = appState.staleCorePrompt else { return }
        Task { [weak self] in
            guard let self else { return }
            let acted = await self.coreManager?.supersedeWithConsent() ?? false
            guard !acted else { return }
            await MainActor.run { self.presentStaleCoreInstructions(prompt) }
        }
    }

    @MainActor
    private func presentStaleCoreInstructions(_ prompt: StaleCorePrompt) {
        let pidHint = prompt.pid.map { "\n\nIts process id is \($0)." } ?? ""
        presentAlert(
            title: "Stop the old core to finish upgrading",
            message: "MCPProxy v\(prompt.runningVersion) is still running and this app "
                + "bundles v\(prompt.bundledVersion). MCPProxy could not stop that process "
                + "automatically — it was started outside the app (a terminal, launchd, or "
                + "`brew services`), so stopping it is up to whoever started it."
                + pidHint
                + "\n\nQuit it there, then choose “Start MCPProxy Core” from this menu."
        )
    }

    @MainActor
    private func presentAlert(title: String, message: String) {
        let alert = NSAlert()
        alert.messageText = title
        alert.informativeText = message
        alert.alertStyle = .informational
        alert.addButton(withTitle: "OK")
        NSApp.activate(ignoringOtherApps: true)
        alert.runModal()
    }

    /// Run the remediation a "Needs Attention" row offers — from the row's own
    /// submenu, under its own verb, never as a side effect of clicking the row
    /// (F4).
    @MainActor
    @objc private func performAttentionAction(_ sender: NSMenuItem) {
        guard let server = sender.representedObject as? ServerStatus,
              let verb = TrayServerAction.fromHealthAction(server.health?.action ?? "") else { return }
        perform(verb, on: server.name, id: server.id)
    }

    /// Navigate to a server's detail page. The represented object is the
    /// server NAME (what `.showServerDetail` matches on).
    @objc private func showServerDetailFromMenu(_ sender: NSMenuItem) {
        guard let name = sender.representedObject as? String else { return }
        showMainWindow()
        NotificationCenter.default.post(name: .switchToServers, object: nil)
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
            NotificationCenter.default.post(name: .showServerDetail, object: name)
        }
    }

    /// F3: the one place a per-server menu action is dispatched.
    ///
    /// Every one of these used to be `try? await …`, so a restart that 500s
    /// was indistinguishable from one that worked — the menu simply closed.
    /// The Web UI raises a toast and Server Detail shows an inline error; the
    /// tray was the only surface that lied by omission. Failures are now
    /// user-visible, and the alert is presented only on failure so the happy
    /// path stays silent.
    @MainActor
    private func perform(_ action: TrayServerAction, on name: String, id: String) {
        guard let client = appState.apiClient else {
            presentServerActionFailure(action, server: name, error: TrayActionError.noCore)
            return
        }
        Task { [weak self] in
            do {
                switch action {
                case .enable: try await client.enableServer(id)
                case .disable: try await client.disableServer(id)
                case .restart: try await client.restartServer(id)
                case .login: try await client.loginServer(id)
                case .approve: return  // review is a human decision — never dispatched
                }
                NSLog("[MCPProxy] %@ %@: ok", action.rawValue, name)
            } catch {
                NSLog("[MCPProxy] %@ %@ failed: %@", action.rawValue, name, error.localizedDescription)
                await MainActor.run {
                    self?.presentServerActionFailure(action, server: name, error: error)
                }
            }
        }
    }

    /// A failed menu action, said out loud. Modal because the user just asked
    /// for this and is still looking at the menu bar — and because a
    /// UNUserNotification can be silenced by Focus, which would put us back
    /// where F3 started.
    @MainActor
    private func presentServerActionFailure(_ action: TrayServerAction, server: String, error: Error) {
        let alert = NSAlert()
        alert.messageText = TrayServerActionFailure.title(action: action, server: server)
        alert.informativeText = TrayServerActionFailure.message(action: action, server: server, error: error)
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Open Server Details")
        alert.addButton(withTitle: "Report an Issue")
        alert.addButton(withTitle: "Dismiss")
        NSApp.activate(ignoringOtherApps: true)
        switch alert.runModal() {
        case .alertFirstButtonReturn:
            showMainWindow()
            NotificationCenter.default.post(name: .switchToServers, object: nil)
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.5) {
                NotificationCenter.default.post(name: .showServerDetail, object: server)
            }
        case .alertSecondButtonReturn:
            NSWorkspace.shared.open(ProjectLinks.issues)
        default:
            break
        }
    }

    /// Failure modes the tray itself knows about, before any request is made.
    enum TrayActionError: LocalizedError {
        case noCore
        var errorDescription: String? {
            switch self {
            case .noCore:
                return "MCPProxy is not connected to a running core."
            }
        }
    }

    @MainActor
    @objc private func enableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        perform(.enable, on: id, id: id)
    }

    @MainActor
    @objc private func disableServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        perform(.disable, on: id, id: id)
    }

    @MainActor
    @objc private func restartServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else { return }
        perform(.restart, on: id, id: id)
    }

    /// Switch the server-level default active profile (Profiles v2 T5). The
    /// represented object is the profile slug ("" clears it / all servers). The
    /// explicit refresh gives immediate feedback; the core also emits
    /// `active_profile.changed` over SSE which repaints every client.
    @objc private func switchProfile(_ sender: NSMenuItem) {
        guard let slug = sender.representedObject as? String else { return }
        Task {
            try? await appState.apiClient?.setActiveProfile(slug)
            await coreManager?.refreshProfiles()
        }
    }

    @MainActor
    @objc private func loginServer(_ sender: NSMenuItem) {
        guard let id = sender.representedObject as? String else {
            NSLog("[MCPProxy] loginServer: no server ID in representedObject")
            return
        }
        NSLog("[MCPProxy] loginServer: triggering login for %@", id)
        perform(.login, on: id, id: id)
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
            let baseURL = await MainActor.run { appState.webUIBaseURL }
            let urlString = apiKey.isEmpty
                ? "\(baseURL)/ui/"
                : "\(baseURL)/ui/?apikey=\(apiKey)"
            if let url = URL(string: urlString) {
                NSWorkspace.shared.open(url)
            }
        }
    }

    /// Open the native activity log filtered to a glance row's session.
    ///
    /// Opens the app's own window at the Activity section — the activity log
    /// has a native home, and a tray click should not context-switch the user
    /// into a browser (the Web UI stays reachable via "Open Web UI in
    /// Browser").
    ///
    /// F10: the row's session id (representedObject) used to be thrown away,
    /// so a click on one client's run opened the whole unfiltered log. The id
    /// now rides along on `.activityFilter` and seeds ActivityView's session
    /// filter, which is what makes a glance row parent↔child navigable.
    @objc private func openActivityForSession(_ sender: NSMenuItem) {
        let sessionId = sender.representedObject as? String
        // Published BEFORE the window is built, so a view created by this very
        // click picks the filter up on appear. A delayed notification would be
        // a race: too early and nothing is subscribed, too late and the user
        // has already read an unfiltered log.
        if let sessionId, !sessionId.isEmpty {
            appState.pendingActivitySessionFilter = sessionId
        }
        showMainWindow(tab: Self.glanceActivityDestination)
        guard let sessionId, !sessionId.isEmpty else { return }
        // Covers the already-open window, whose observers are live now.
        NotificationCenter.default.post(name: .activityFilter, object: sessionId)
    }

    /// Where a glance row click lands. A constant so tests can pin the
    /// destination without instantiating the app delegate.
    static let glanceActivityDestination: SidebarItem = .activity

    // `openConfigFile()` used to live here: an @objc selector nothing
    // referenced, for a file the tray deliberately never reads or writes (all
    // config goes through REST). Deleted by the 2026-08 UX audit (F14) rather
    // than wired to a menu row that would contradict the REST-only contract —
    // the effective configuration is in Settings → Raw.

    // MARK: - Project links (discussion #948)

    @objc private func openDocumentation() {
        NSWorkspace.shared.open(ProjectLinks.docs)
    }

    /// Shows the standard About panel (app name + version) with a credits block
    /// that links back to the homepage, source, and docs — so "About MCPProxy"
    /// carries the way back to the project, not just a version string.
    @objc private func showAboutPanel() {
        NSApp.activate(ignoringOtherApps: true)

        let credits = NSMutableAttributedString()
        let body: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 11),
            .foregroundColor: NSColor.secondaryLabelColor,
        ]
        credits.append(NSAttributedString(
            string: "Smart MCP proxy — intelligent tool discovery, token savings, and security quarantine.\n\n",
            attributes: body))
        for (index, link) in AboutPanelLinks.all.enumerated() {
            if index > 0 { credits.append(NSAttributedString(string: "   ", attributes: body)) }
            appendLink(to: credits, label: link.label, url: link.url)
        }

        NSApp.orderFrontStandardAboutPanel(options: [
            .credits: credits
        ])
    }

    private func appendLink(to string: NSMutableAttributedString, label: String, url: URL) {
        string.append(NSAttributedString(string: label, attributes: [
            .link: url,
            .font: NSFont.systemFont(ofSize: 11),
        ]))
    }

    @objc private func openLogsDirectory() {
        let home = FileManager.default.homeDirectoryForCurrentUser
        NSWorkspace.shared.open(home.appendingPathComponent("Library/Logs/mcpproxy"))
    }

    @MainActor
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
        // Spec 044 (T055): publish new state so the core's telemetry reader
        // observes the change within its 1h TTL. We write the effective
        // SMAppService state rather than the optimistic toggle value — that
        // way a registration failure does not poison the sidecar.
        AutostartSidecarService.refresh()
        rebuildMenu()
    }

    @objc private func checkForUpdates() {
        updateService.currentVersion = appState.version
        updateService.checkForUpdates()
    }

    @objc private func openDownloadPage() {
        updateService.openDownloadPage()
    }

    /// Spec 092 FR-010: one click — download, verify, replace, relaunch.
    @objc private func installFeedUpdate() {
        updateService.installFeedUpdate()
    }

    /// Spec 092 FR-016: say why an in-place update is impossible, and what to
    /// do instead. The alternative — an update item that quietly does nothing —
    /// is the failure mode the requirement names.
    @MainActor
    @objc private func showUpdateBlockedReason() {
        guard let reason = updateService.blockedReason else { return }
        presentAlert(title: reason.menuTitle, message: reason.message)
    }

    @objc private func quitApp() {
        // Claimed before anything else runs: the core teardown below would
        // otherwise get there first and record its own mechanical description
        // of what it is doing instead of the fact that the user asked (#862).
        AppLifecycle.shared.note("user chose Quit in the tray menu")
        Task {
            await coreManager?.shutdown()
            try? await Task.sleep(nanoseconds: 200_000_000)
            NSApplication.shared.terminate(nil)
        }
    }

    // MARK: - Helpers

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
        case "login": return "Sign in"
        case "restart": return "Restart Needed"
        case "enable": return "Disabled"
        case "approve": return "Approval Needed"
        case "set_secret": return "Secret Missing"
        case "configure": return "Configuration Needed"
        case "view_logs": return "Check Logs"
        default: return "Action Needed"
        }
    }

}

// MARK: - App

// MARK: - Notification Names

extension Notification.Name {
    /// Posted by tray menu "Add Server..." to trigger the sheet in ServersView.
    static let showAddServer = Notification.Name("MCPProxy.showAddServer")
    /// Posted by the core status banner to start the core.
    static let startCore = Notification.Name("MCPProxy.startCore")
    /// Posted by dashboard "Connect Clients" to open the Web UI.
    static let openWebUI = Notification.Name("MCPProxy.openWebUI")
    /// Posted by dashboard to switch sidebar to Activity Log view.
    static let switchToActivity = Notification.Name("MCPProxy.switchToActivity")
    /// Posted by dashboard to switch sidebar to Servers view.
    static let switchToServers = Notification.Name("MCPProxy.switchToServers")
    /// Posted by tray menu to open the detail view for a specific server (object = server name string).
    static let showServerDetail = Notification.Name("MCPProxy.showServerDetail")
    /// Posted by `showMainWindow(tab:)` to select a sidebar section in an
    /// already-open main window (object = SidebarItem raw value string).
    static let switchToSidebarTab = Notification.Name("MCPProxy.switchToSidebarTab")
    /// Posted by a tray glance row to scope the Activity Log to the session it
    /// came from (object = MCP session id string). F10 — a glance row that
    /// opened the whole unfiltered log was the one place the "parent↔child
    /// navigable" rule was not honoured.
    static let activityFilter = Notification.Name("MCPProxy.activityFilter")
}

@main
struct MCPProxyApp: App {
    @NSApplicationDelegateAdaptor(AppController.self) var controller

    var body: some Scene {
        // The tray menu is pure AppKit (NSStatusItem + NSMenu) — this avoids the
        // MenuBarExtra .menu ForEach-duplication bug. There is ONE config window,
        // the AppKit NSWindow in showSettingsWindow(). The tray "Settings…", the
        // app-menu "Settings…" click, and ⌘, all route there. This SwiftUI
        // Settings scene exists only to own the system "Settings…" slot; it is
        // bridged away so it never actually shows.
        Settings {
            // Safety net only: ⌘, is intercepted by the key monitor and the
            // app-menu "Settings…" click is repointed (both in AppController),
            // so this scene normally never opens. If some path we didn't catch
            // does open it, redirect to the real config window and dismiss this
            // empty scene window — the user must never see a stub.
            SettingsSceneBridge(controller: controller)
        }
    }
}

/// Empty stand-in for the SwiftUI Settings scene that immediately hands off to
/// the AppController's AppKit config window. See `body` above for why.
private struct SettingsSceneBridge: View {
    let controller: AppController
    var body: some View {
        Color.clear
            .frame(width: 1, height: 1)
            .onAppear { controller.openSettingsFromScene() }
    }
}
