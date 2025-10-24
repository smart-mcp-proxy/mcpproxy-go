//go:build !nogui && !headless && !linux

package tray

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/inconshreveable/go-update"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"mcpproxy-go/internal/config"
	internalRuntime "mcpproxy-go/internal/runtime"
	"mcpproxy-go/internal/server"
	// "mcpproxy-go/internal/upstream/cli" // replaced by in-process OAuth
)

const (
	repo          = "smart-mcp-proxy/mcpproxy-go" // Actual repository
	osDarwin      = "darwin"
	osWindows     = "windows"
	osLinux       = "linux"
	phaseError    = "Error"
	assetZipExt   = ".zip"
	assetTarGzExt = ".tar.gz"
	trueStr       = "true"
)

//go:embed icon-mono-44.png
var iconDataPNG []byte

//go:embed icon-mono-44.ico
var iconDataICO []byte

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// ServerInterface defines the interface for server control
type ServerInterface interface {
	IsRunning() bool
	GetListenAddress() string
	GetUpstreamStats() map[string]interface{}
	StartServer(ctx context.Context) error
	StopServer() error
	GetStatus() interface{}            // Returns server status for display
	StatusChannel() <-chan interface{} // Channel for status updates
	EventsChannel() <-chan internalRuntime.Event

	// Quarantine management methods
	GetQuarantinedServers() ([]map[string]interface{}, error)
	UnquarantineServer(serverName string) error

	// Server management methods for tray menu
	EnableServer(serverName string, enabled bool) error
	QuarantineServer(serverName string, quarantined bool) error
	GetAllServers() ([]map[string]interface{}, error)
	SetListenAddress(addr string, persist bool) error
	SuggestAlternateListen(baseAddr string) (string, error)

	// Config management for file watching
	ReloadConfiguration() error
	GetConfigPath() string
	GetLogDir() string

	// OAuth control
	TriggerOAuthLogin(serverName string) error
}

// App represents the system tray application
type App struct {
	server    ServerInterface
	apiClient interface{ OpenWebUI() error } // API client for web UI access (optional)
	logger    *zap.SugaredLogger
	version   string
	shutdown  func()

	connectionState   ConnectionState
	connectionStateMu sync.RWMutex
	instrumentation   instrumentation

	statusMu      sync.RWMutex
	statusTitle   string
	statusTooltip string

	// Menu items for dynamic updates
	statusItem *systray.MenuItem
	// startStopItem removed - tray doesn't directly control core lifecycle
	upstreamServersMenu *systray.MenuItem
	quarantineMenu      *systray.MenuItem
	portConflictMenu    *systray.MenuItem
	portConflictInfo    *systray.MenuItem
	portConflictRetry   *systray.MenuItem
	portConflictAuto    *systray.MenuItem
	portConflictCopy    *systray.MenuItem
	portConflictConfig  *systray.MenuItem

	// Managers for proper synchronization
	stateManager *ServerStateManager
	menuManager  *MenuManager
	syncManager  *SynchronizationManager

	// Autostart manager
	autostartManager *AutostartManager
	autostartItem    *systray.MenuItem

	// Config path for opening from menu
	configPath string

	// Context for background operations
	ctx    context.Context
	cancel context.CancelFunc

	// Menu tracking fields for dynamic updates
	coreMenusReady    bool                         // Track if core menu items are ready
	lastServerList    []string                     // Track last known server list for change detection
	serverMenus       map[string]*systray.MenuItem // Track server menu items
	serverActionMenus map[string]*systray.MenuItem // Track server action menu items

	// Quarantine menu tracking fields
	lastQuarantineList    []string                     // Track last known quarantine list for change detection
	quarantineServerMenus map[string]*systray.MenuItem // Track quarantine server menu items
	portConflictActive    bool
	portConflictAddress   string
	portConflictSuggested string
}

// New creates a new tray application
func New(server ServerInterface, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	return NewWithAPIClient(server, nil, logger, version, shutdown)
}

// NewWithAPIClient creates a new tray application with an API client for web UI access
func NewWithAPIClient(server ServerInterface, apiClient interface{ OpenWebUI() error }, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	app := &App{
		server:          server,
		apiClient:       apiClient,
		logger:          logger,
		version:         version,
		shutdown:        shutdown,
		connectionState: ConnectionStateInitializing,
		statusTitle:     "Status: Initializing...",
		statusTooltip:   "mcpproxy tray is starting",
	}

	app.instrumentation = newInstrumentation(app)

	// Initialize managers (will be fully set up in onReady)
	app.stateManager = NewServerStateManager(server, logger)

	// Initialize autostart manager
	if autostartManager, err := NewAutostartManager(); err != nil {
		logger.Warn("Failed to initialize autostart manager", zap.Error(err))
	} else {
		app.autostartManager = autostartManager
	}

	// Initialize menu tracking maps
	app.serverMenus = make(map[string]*systray.MenuItem)
	app.serverActionMenus = make(map[string]*systray.MenuItem)
	app.quarantineServerMenus = make(map[string]*systray.MenuItem)
	app.lastServerList = []string{}
	app.lastQuarantineList = []string{}

	return app
}

// SetConnectionState updates the tray's view of the core connectivity status.
func (a *App) SetConnectionState(state ConnectionState) {
	a.connectionStateMu.Lock()
	a.connectionState = state
	a.connectionStateMu.Unlock()
	a.logger.Debug("Updated connection state", zap.String("state", string(state)))
	a.instrumentation.NotifyConnectionState(state)

	// Update sync manager connection state - only sync when fully connected
	if a.syncManager != nil {
		connected := (state == ConnectionStateConnected)
		a.syncManager.SetConnected(connected)
	}

	if !a.coreMenusReady || a.statusItem == nil {
		return
	}

	a.applyConnectionStateToUI(state)
}

// getConnectionState returns the last observed connection state.
func (a *App) getConnectionState() ConnectionState {
	a.connectionStateMu.RLock()
	defer a.connectionStateMu.RUnlock()
	return a.connectionState
}

// ObserveConnectionState wires a channel of connection states into the tray UI.
func (a *App) ObserveConnectionState(ctx context.Context, states <-chan ConnectionState) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case state, ok := <-states:
				if !ok {
					a.SetConnectionState(ConnectionStateDisconnected)
					return
				}
				a.SetConnectionState(state)
			}
		}
	}()
}

// applyConnectionStateToUI mutates tray widgets to reflect the provided connection state.
func (a *App) applyConnectionStateToUI(state ConnectionState) {
	if a.statusItem == nil {
		return
	}

	var statusText string
	var tooltip string

	switch state {
	case ConnectionStateInitializing:
		statusText = "Status: Initializing..."
		tooltip = "mcpproxy tray is starting"
	case ConnectionStateStartingCore:
		statusText = "Status: Launching core..."
		tooltip = "Starting mcpproxy core process"
	case ConnectionStateConnecting:
		statusText = "Status: Connecting to core..."
		tooltip = "Waiting for core API to become reachable"
	case ConnectionStateReconnecting:
		statusText = "Status: Reconnecting..."
		tooltip = "Reconnecting to the core runtime"
	case ConnectionStateDisconnected:
		statusText = "Status: Core unavailable"
		tooltip = "Tray cannot reach the core runtime"
	case ConnectionStateAuthError:
		statusText = "Status: Authentication error"
		tooltip = "Core is running but API key authentication failed"
	// ADD: Detailed error state messages with actionable instructions
	case ConnectionStateErrorPortConflict:
		statusText = "Status: Port conflict"
		tooltip = "Port 8080 already in use. Kill other mcpproxy instance or change port in config."
	case ConnectionStateErrorDBLocked:
		statusText = "Status: Database locked"
		tooltip = "Database locked by another mcpproxy instance. Kill other instance with: pkill mcpproxy"
	case ConnectionStateErrorConfig:
		statusText = "Status: Configuration error"
		tooltip = "Invalid configuration file. Fix ~/.mcpproxy/mcp_config.json and restart."
	case ConnectionStateErrorGeneral:
		statusText = "Status: Core startup failed"
		tooltip = "Core failed to start. Check ~/Library/Logs/mcpproxy/main.log for details."
	case ConnectionStateFailed:
		statusText = "Status: Failed"
		tooltip = "Core failed to start after multiple attempts"
	case ConnectionStateConnected:
		statusText = "Status: Connected"
		tooltip = "Core runtime is responding"
	default:
		statusText = "Status: Unknown"
		tooltip = "Core connection state is unknown"
	}

	a.statusItem.SetTitle(statusText)
	a.statusItem.SetTooltip(tooltip)
	systray.SetTooltip(tooltip)
	a.statusMu.Lock()
	a.statusTitle = statusText
	a.statusTooltip = tooltip
	a.statusMu.Unlock()

	if state != ConnectionStateConnected {
		a.hidePortConflictMenu()
	}

	// Note: startStopItem removed - no longer needed in new architecture

	a.instrumentation.NotifyConnectionState(state)
	a.instrumentation.NotifyStatus()
}

// Run starts the system tray application
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting system tray application")
	a.ctx, a.cancel = context.WithCancel(ctx)
	defer a.cancel()
	a.instrumentation.Start(a.ctx)

	if a.server != nil {
		a.configPath = a.server.GetConfigPath()
	}

	// Start background auto-update checker (daily)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.checkForUpdates()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Start background status updater (every 5 seconds for more responsive UI)
	// Wait for menu initialization to complete before starting updates
	go func() {
		a.logger.Debug("Waiting for core menu items to be initialized...")
		// Wait for menu items to be initialized using the flag
		for !a.coreMenusReady {
			select {
			case <-ctx.Done():
				return
			default:
				time.Sleep(100 * time.Millisecond) // Check every 100ms
			}
		}

		a.logger.Debug("Core menu items ready, starting status updater")
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				a.updateStatus()
			case <-ctx.Done():
				return
			}
		}
	}()

	// Listen for real-time status updates
	if a.server != nil {
		a.logger.Debug("Starting status and runtime event listeners")
		go a.consumeStatusUpdates()

		if eventsCh := a.server.EventsChannel(); eventsCh != nil {
			go a.consumeRuntimeEvents(eventsCh)
		}
	}

	// Monitor context cancellation and quit systray when needed
	go func() {
		<-ctx.Done()
		a.logger.Info("Context cancelled, quitting systray")
		a.cleanup()
		systray.Quit()
	}()

	// Start systray - this is a blocking call that must run on main thread
	systray.Run(a.onReady, a.onExit)

	return ctx.Err()
}

// cleanup performs cleanup operations
func (a *App) cleanup() {
	a.instrumentation.Shutdown()
	a.cancel()
}

func (a *App) consumeStatusUpdates() {
	statusCh := a.server.StatusChannel()
	if statusCh == nil {
		return
	}

	a.logger.Debug("Waiting for core menu items before processing real-time status updates...")
	for !a.coreMenusReady {
		select {
		case <-a.ctx.Done():
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	a.logger.Debug("Core menu items ready, starting real-time status updates")
	for {
		select {
		case status, ok := <-statusCh:
			if !ok {
				a.logger.Debug("Status channel closed; stopping status updates listener")
				return
			}
			a.updateStatusFromData(status)
		case <-a.ctx.Done():
			return
		}
	}
}

func (a *App) consumeRuntimeEvents(eventsCh <-chan internalRuntime.Event) {
	if eventsCh == nil {
		return
	}

	a.logger.Debug("Waiting for core menu items before processing runtime events...")
	for !a.coreMenusReady {
		select {
		case <-a.ctx.Done():
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	a.logger.Debug("Core menu items ready, starting runtime event listener")
	for {
		select {
		case evt, ok := <-eventsCh:
			if !ok {
				a.logger.Debug("Runtime events channel closed; stopping listener")
				return
			}
			a.handleRuntimeEvent(evt)
		case <-a.ctx.Done():
			return
		}
	}
}

func (a *App) handleRuntimeEvent(evt internalRuntime.Event) {
	switch evt.Type {
	case internalRuntime.EventTypeServersChanged, internalRuntime.EventTypeConfigReloaded:
		if evt.Payload != nil {
			a.logger.Debug("Processing runtime event",
				zap.String("type", string(evt.Type)),
				zap.Any("payload", evt.Payload))
		} else {
			a.logger.Debug("Processing runtime event", zap.String("type", string(evt.Type)))
		}

		if a.syncManager != nil {
			if err := a.syncManager.SyncNow(); err != nil {
				a.logger.Error("Failed to synchronize menus after runtime event", zap.Error(err))
			}
		}

		a.updateStatus()
	default:
		// Ignore other event types for now but log at debug for visibility
		a.logger.Debug("Ignoring runtime event", zap.String("type", string(evt.Type)))
	}
}

func (a *App) onReady() {
	// Use .ico format on Windows for better compatibility, PNG on other platforms
	var iconData []byte
	if runtime.GOOS == osWindows {
		iconData = iconDataICO
	} else {
		iconData = iconDataPNG
	}

	systray.SetIcon(iconData)
	// On macOS, also set as template icon for better system integration
	if runtime.GOOS == osDarwin {
		systray.SetTemplateIcon(iconData, iconData)
	}
	a.updateTooltip()

	// --- Initialize Menu Items ---
	a.logger.Debug("Initializing tray menu items")
	a.statusItem = systray.AddMenuItem("Status: Initializing...", "Proxy server status")
	a.statusItem.Disable() // Initially disabled as it's just for display
	// Note: startStopItem removed - tray doesn't directly control core lifecycle
	// Users should quit tray to restart (when tray manages core) or use CLI (when core is independent)
	a.applyConnectionStateToUI(a.getConnectionState())

	// Port conflict resolution submenu (hidden until needed)
	a.portConflictMenu = systray.AddMenuItem("Resolve port conflict", "Actions to resolve listen port issues")
	a.portConflictInfo = a.portConflictMenu.AddSubMenuItem("Waiting for status...", "Port conflict details")
	a.portConflictInfo.Disable()
	a.portConflictRetry = a.portConflictMenu.AddSubMenuItem("Retry starting MCPProxy", "Attempt to start MCPProxy on the configured port")
	a.portConflictAuto = a.portConflictMenu.AddSubMenuItem("Use next available port", "Automatically select an available port")
	a.portConflictCopy = a.portConflictMenu.AddSubMenuItem("Copy MCP URL", "Copy the MCP connection URL to the clipboard")
	a.portConflictConfig = a.portConflictMenu.AddSubMenuItem("Open config directory", "Edit the configuration manually")
	a.portConflictMenu.Hide()
	a.portConflictRetry.Disable()
	a.portConflictAuto.Disable()
	a.portConflictCopy.Disable()
	a.portConflictConfig.Disable()

	// Mark core menu items as ready - this will release waiting goroutines
	a.coreMenusReady = true
	a.logger.Debug("Core menu items initialized successfully - background processes can now start")
	// Reapply the last known connection state now that UI widgets exist.
	a.applyConnectionStateToUI(a.getConnectionState())
	systray.AddSeparator()

	// --- Upstream & Quarantine Menus ---
	a.upstreamServersMenu = systray.AddMenuItem("Upstream Servers", "Manage upstream servers")
	a.quarantineMenu = systray.AddMenuItem("Security Quarantine", "Manage quarantined servers")
	systray.AddSeparator()

	// --- Initialize Managers ---
	a.menuManager = NewMenuManager(a.upstreamServersMenu, a.quarantineMenu, a.logger)
	a.syncManager = NewSynchronizationManager(a.stateManager, a.server, a.menuManager, a.logger)
	a.syncManager.SetOnSync(func() {
		a.instrumentation.NotifyMenus()
	})
	a.instrumentation.NotifyMenus()
	a.logger.Info("Tray menus initialized; starting synchronization managers")

	// --- Set Action Callback ---
	// Centralized action handler for all menu-driven server actions
	a.menuManager.SetActionCallback(a.handleServerAction)

	// --- Other Menu Items ---
	updateItem := systray.AddMenuItem("Check for Updates...", "Check for a new version of the proxy")
	openConfigItem := systray.AddMenuItem("Open config dir", "Open the configuration directory")
	openLogsItem := systray.AddMenuItem("Open logs dir", "Open the logs directory")

	// Add Web Control Panel menu item if API client is available
	var openWebUIItem *systray.MenuItem
	if a.apiClient != nil {
		openWebUIItem = systray.AddMenuItem("Open Web Control Panel", "Open the web control panel in your browser")
	}
	systray.AddSeparator()

	// --- Autostart Menu Item (macOS only) ---
	if runtime.GOOS == osDarwin && a.autostartManager != nil {
		a.autostartItem = systray.AddMenuItem("Start at Login", "Start mcpproxy automatically when you log in")
		a.updateAutostartMenuItem()
		systray.AddSeparator()
	}

	quitItem := systray.AddMenuItem("Quit", "Quit the application")

	// --- Set Initial State & Start Sync ---
	a.updateStatus()

	a.syncManager.Start()
	a.logger.Info("Tray synchronization loop started")

	go func() {
		if err := a.syncManager.SyncNow(); err != nil {
			a.logger.Error("Initial menu sync failed", zap.Error(err))
		}
	}()

	// --- Click Handlers ---
	if a.portConflictRetry != nil {
		retryCh := a.portConflictRetry.ClickedCh
		go func() {
			for {
				select {
				case <-retryCh:
					go a.handlePortConflictRetry()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if a.portConflictAuto != nil {
		autoCh := a.portConflictAuto.ClickedCh
		go func() {
			for {
				select {
				case <-autoCh:
					go a.handlePortConflictAuto()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if a.portConflictCopy != nil {
		copyCh := a.portConflictCopy.ClickedCh
		go func() {
			for {
				select {
				case <-copyCh:
					go a.handlePortConflictCopy()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if a.portConflictConfig != nil {
		configCh := a.portConflictConfig.ClickedCh
		go func() {
			for {
				select {
				case <-configCh:
					a.openConfigDir()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if updateItem != nil {
		updateCh := updateItem.ClickedCh
		go func() {
			for {
				select {
				case <-updateCh:
					go a.checkForUpdates()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if openConfigItem != nil {
		openConfigCh := openConfigItem.ClickedCh
		go func() {
			for {
				select {
				case <-openConfigCh:
					a.openConfigDir()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if openLogsItem != nil {
		openLogsCh := openLogsItem.ClickedCh
		go func() {
			for {
				select {
				case <-openLogsCh:
					a.openLogsDir()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	if quitItem != nil {
		quitCh := quitItem.ClickedCh
		go func() {
			for {
				select {
				case <-quitCh:
					a.logger.Info("Quit item clicked, shutting down")
					if a.shutdown != nil {
						a.shutdown()
					}
					// Ensure we exit even if cancellation propagation stalls.
					systray.Quit()
					return
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	// --- Web UI Click Handler (separate goroutine if available) ---
	if openWebUIItem != nil {
		go func() {
			for {
				select {
				case <-openWebUIItem.ClickedCh:
					a.handleOpenWebUI()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	// --- Autostart Click Handler (separate goroutine for macOS) ---
	if runtime.GOOS == osDarwin && a.autostartItem != nil {
		go func() {
			for {
				select {
				case <-a.autostartItem.ClickedCh:
					a.handleAutostartToggle()
				case <-a.ctx.Done():
					return
				}
			}
		}()
	}

	a.logger.Info("System tray is ready - menu items fully initialized")
}

// updateTooltip updates the tooltip based on the server's running state
func (a *App) updateTooltip() {
	if a.getConnectionState() != ConnectionStateConnected {
		// Connection state handler already set an appropriate tooltip.
		return
	}

	if a.server == nil {
		systray.SetTooltip("mcpproxy core not attached")
		return
	}

	// Get full status and use comprehensive tooltip
	statusData := a.server.GetStatus()
	if status, ok := statusData.(map[string]interface{}); ok {
		a.updateTooltipFromStatusData(status)
	} else {
		// Fallback to basic tooltip if status format is unexpected
		if a.server.IsRunning() {
			systray.SetTooltip(fmt.Sprintf("mcpproxy is running on %s", a.server.GetListenAddress()))
		} else {
			systray.SetTooltip("mcpproxy is stopped")
		}
	}
}

// updateStatusFromData updates menu items based on real-time status data from the server
func (a *App) updateStatusFromData(statusData interface{}) {
	// Handle different status data formats
	var status map[string]interface{}
	var ok bool

	switch v := statusData.(type) {
	case map[string]interface{}:
		status = v
		ok = true
	case server.Status:
		// Convert Status struct to map for consistent handling
		status = map[string]interface{}{
			"running":     a.server != nil && a.server.IsRunning(),
			"listen_addr": "",
			"phase":       v.Phase,
			"message":     v.Message,
		}
		if a.server != nil {
			status["listen_addr"] = a.server.GetListenAddress()
		}
		ok = true
	default:
		// Try to handle basic server state even with unexpected format
		a.logger.Debug("Received status data in unexpected format, using fallback",
			zap.String("type", fmt.Sprintf("%T", statusData)))

		// Fallback to basic server state
		if a.server != nil {
			status = map[string]interface{}{
				"running":     a.server.IsRunning(),
				"listen_addr": a.server.GetListenAddress(),
				"phase":       "Unknown",
				"message":     "Status format unknown",
			}
			ok = true
		} else {
			// No server available, can't determine status
			return
		}
	}

	if !ok {
		a.logger.Warn("Unable to process status data, skipping update")
		return
	}

	// Check if core menu items are ready to prevent nil pointer dereference
	if !a.coreMenusReady {
		a.logger.Debug("Core menu items not ready yet, skipping status update from data")
		return
	}

	if a.getConnectionState() != ConnectionStateConnected {
		a.logger.Debug("Skipping runtime status update; core not in connected state")
		return
	}

	// Debug logging to track status updates
	running, _ := status["running"].(bool)
	phase, _ := status["phase"].(string)
	message, _ := status["message"].(string)
	listenAddr, _ := status["listen_addr"].(string)
	serverRunning := a.server != nil && a.server.IsRunning()
	// IMPORTANT: Only use listen_addr from SSE status (source of truth from core /api/v1/info)
	// Do NOT fall back to a.server.GetListenAddress() as it may be stale/wrong

	lowerMessage := strings.ToLower(message)
	portConflict := phase == phaseError && strings.Contains(lowerMessage, "port") && strings.Contains(lowerMessage, "in use")

	a.logger.Debug("Updating tray status",
		zap.Bool("status_running", running),
		zap.Bool("server_is_running", serverRunning),
		zap.String("phase", phase),
		zap.Bool("port_conflict", portConflict),
		zap.Any("status_data", status))

	// Treat either SSE running flag or RPC running flag as authoritative.
	actuallyRunning := running || serverRunning

	if portConflict {
		a.showPortConflictMenu(listenAddr, message)
	} else {
		a.hidePortConflictMenu()
	}

	if actuallyRunning {
		title := "Status: Running"
		if listenAddr != "" {
			title = fmt.Sprintf("Status: Running (%s)", listenAddr)
		}
		a.statusItem.SetTitle(title)
		a.statusMu.Lock()
		a.statusTitle = title
		a.statusMu.Unlock()
		// Note: startStopItem visibility is now managed by applyConnectionStateToUI
		// based on ConnectionState, not server running status
		a.logger.Debug("Set tray to running state")
	} else {
		title := "Status: Stopped"
		if phase == phaseError {
			title = "Status: Error"
			if portConflict && listenAddr != "" {
				title = fmt.Sprintf("Status: Port conflict (%s)", listenAddr)
			}
		}
		a.statusItem.SetTitle(title)
		a.statusMu.Lock()
		a.statusTitle = title
		a.statusMu.Unlock()
		// Note: startStopItem visibility is now managed by applyConnectionStateToUI
		// based on ConnectionState, not server running status
		a.logger.Debug("Set tray to non-running state", zap.String("phase", phase))
	}

	// Update tooltip
	a.updateTooltipFromStatusData(status)
	a.instrumentation.NotifyStatus()

	// Update server menus using the manager (only if server is running)
	if a.syncManager != nil {
		if actuallyRunning {
			a.syncManager.SyncDelayed()
		} else {
			a.logger.Debug("Server stopped, preserving menu state with disconnected status")
		}
	}
}

// updateTooltipFromStatusData updates the tray tooltip from status data map
func (a *App) updateTooltipFromStatusData(status map[string]interface{}) {
	if a.getConnectionState() != ConnectionStateConnected {
		return
	}

	running, _ := status["running"].(bool)
	phase, _ := status["phase"].(string)
	message, _ := status["message"].(string)

	if !running {
		tooltip := "mcpproxy is stopped"
		if phase == phaseError {
			if strings.TrimSpace(message) != "" {
				tooltip = fmt.Sprintf("mcpproxy error: %s", message)
			} else {
				tooltip = "mcpproxy encountered an error while starting"
			}
		}
		systray.SetTooltip(tooltip)
		a.statusMu.Lock()
		a.statusTooltip = tooltip
		a.statusMu.Unlock()
		return
	}

	// Build comprehensive tooltip for running server
	listenAddr, _ := status["listen_addr"].(string)
	toolsIndexed, _ := status["tools_indexed"].(int)

	// Get upstream stats for connected servers and total tools
	upstreamStats, _ := status["upstream_stats"].(map[string]interface{})

	var connectedServers, totalServers, totalTools int
	if upstreamStats != nil {
		if connected, ok := upstreamStats["connected_servers"].(int); ok {
			connectedServers = connected
		}
		if total, ok := upstreamStats["total_servers"].(int); ok {
			totalServers = total
		}
		if tools, ok := upstreamStats["total_tools"].(int); ok {
			totalTools = tools
		}
	}

	// Build multi-line tooltip with comprehensive information
	var tooltipLines []string

	// Main status line
	tooltipLines = append(tooltipLines, fmt.Sprintf("mcpproxy (%s) - %s", phase, listenAddr))

	// Server connection status
	if totalServers > 0 {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Servers: %d/%d connected", connectedServers, totalServers))
	} else {
		tooltipLines = append(tooltipLines, "Servers: none configured")
	}

	// Tool count - this is the key information the user wanted
	if totalTools > 0 {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Tools: %d available", totalTools))
	} else if toolsIndexed > 0 {
		// Fallback to indexed count if total tools not available
		tooltipLines = append(tooltipLines, fmt.Sprintf("Tools: %d indexed", toolsIndexed))
	} else {
		tooltipLines = append(tooltipLines, "Tools: none available")
	}

	tooltip := strings.Join(tooltipLines, "\n")
	systray.SetTooltip(tooltip)
	a.statusMu.Lock()
	a.statusTooltip = tooltip
	a.statusMu.Unlock()
}

// updateStatus updates the status menu item and tooltip
func (a *App) updateStatus() {
	if a.server == nil {
		return
	}

	// Check if core menu items are ready
	if !a.coreMenusReady {
		a.logger.Debug("Core menu items not ready yet, skipping status update")
		return
	}

	statusData := a.server.GetStatus()
	a.updateStatusFromData(statusData)
}

// handleStartStop - REMOVED
// In the new architecture, tray doesn't directly control the core process lifecycle.
// The state machine in cmd/mcpproxy-tray/main.go manages the core process.
// Users should:
// - Quit tray to restart (when tray manages core)
// - Use CLI to restart (when core is independent)

// onExit is called when the application is quitting
func (a *App) onExit() {
	a.logger.Info("Tray application exiting")
	a.cleanup()
	if a.cancel != nil {
		a.cancel()
	}
}

// checkForUpdates checks for new releases on GitHub
func (a *App) checkForUpdates() {
	// Check if auto-update is disabled by environment variable
	if os.Getenv("MCPPROXY_DISABLE_AUTO_UPDATE") == trueStr {
		a.logger.Info("Auto-update disabled by environment variable")
		return
	}

	// Disable auto-update for app bundles by default (DMG installation should be manual)
	if a.isAppBundle() && os.Getenv("MCPPROXY_UPDATE_APP_BUNDLE") != trueStr {
		a.logger.Info("Auto-update disabled for app bundle installations (use DMG for updates)")
		return
	}

	// Check if notification-only mode is enabled
	notifyOnly := os.Getenv("MCPPROXY_UPDATE_NOTIFY_ONLY") == trueStr

	a.statusItem.SetTitle("Checking for updates...")
	defer a.updateStatus() // Restore original status after check

	release, err := a.getLatestRelease()
	if err != nil {
		a.logger.Error("Failed to get latest release", zap.Error(err))
		return
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	if semver.Compare("v"+a.version, "v"+latestVersion) >= 0 {
		a.logger.Info("You are running the latest version", zap.String("version", a.version))
		return
	}

	if notifyOnly {
		a.logger.Info("Update available - notification only mode",
			zap.String("current", a.version),
			zap.String("latest", latestVersion),
			zap.String("url", fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, release.TagName)))

		// You could add desktop notification here if desired
		a.statusItem.SetTitle(fmt.Sprintf("Update available: %s", latestVersion))
		return
	}

	downloadURL, err := a.findAssetURL(release)
	if err != nil {
		a.logger.Error("Failed to find asset for your system", zap.Error(err))
		return
	}

	if err := a.downloadAndApplyUpdate(downloadURL); err != nil {
		a.logger.Error("Update failed", zap.Error(err))
	}
}

// getLatestRelease fetches the latest release information from GitHub
func (a *App) getLatestRelease() (*GitHubRelease, error) {
	// Check if prerelease updates are allowed
	allowPrerelease := os.Getenv("MCPPROXY_ALLOW_PRERELEASE_UPDATES") == trueStr

	if allowPrerelease {
		// Get all releases and find the latest (including prereleases)
		return a.getLatestReleaseIncludingPrereleases()
	}

	// Default behavior: get latest stable release only
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	resp, err := http.Get(url) // #nosec G107 -- URL is constructed from known repo constant
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}
	return &release, nil
}

// getLatestReleaseIncludingPrereleases fetches the latest release including prereleases
func (a *App) getLatestReleaseIncludingPrereleases() (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases", repo)
	resp, err := http.Get(url) // #nosec G107 -- URL is constructed from known repo constant
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var releases []GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	if len(releases) == 0 {
		return nil, fmt.Errorf("no releases found")
	}

	// Return the first release (GitHub returns them sorted by creation date, newest first)
	return &releases[0], nil
}

// findAssetURL finds the correct asset URL for the current system
func (a *App) findAssetURL(release *GitHubRelease) (string, error) {
	// Check if this is a Homebrew installation to avoid conflicts
	if a.isHomebrewInstallation() {
		return "", fmt.Errorf("auto-update disabled for Homebrew installations - use 'brew upgrade mcpproxy' instead")
	}

	// Determine file extension based on platform
	var extension string
	switch runtime.GOOS {
	case osWindows:
		extension = assetZipExt
	default: // macOS, Linux
		extension = assetTarGzExt
	}

	// Try latest assets first (for website integration)
	latestSuffix := fmt.Sprintf("latest-%s-%s%s", runtime.GOOS, runtime.GOARCH, extension)
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, latestSuffix) {
			return asset.BrowserDownloadURL, nil
		}
	}

	// Fallback to versioned assets
	versionedSuffix := fmt.Sprintf("-%s-%s%s", runtime.GOOS, runtime.GOARCH, extension)
	for _, asset := range release.Assets {
		if strings.HasSuffix(asset.Name, versionedSuffix) {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("no suitable asset found for %s-%s (tried %s and %s)",
		runtime.GOOS, runtime.GOARCH, latestSuffix, versionedSuffix)
}

// isHomebrewInstallation checks if this is a Homebrew installation
func (a *App) isHomebrewInstallation() bool {
	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	// Check if running from Homebrew path
	return strings.Contains(execPath, "/opt/homebrew/") ||
		strings.Contains(execPath, "/usr/local/Homebrew/") ||
		strings.Contains(execPath, "/home/linuxbrew/")
}

// isAppBundle checks if running from macOS app bundle
func (a *App) isAppBundle() bool {
	if runtime.GOOS != osDarwin {
		return false
	}

	execPath, err := os.Executable()
	if err != nil {
		return false
	}

	return strings.Contains(execPath, ".app/Contents/MacOS/")
}

// downloadAndApplyUpdate downloads and applies the update
func (a *App) downloadAndApplyUpdate(url string) error {
	resp, err := http.Get(url) // #nosec G107 -- URL is from GitHub releases API which is trusted
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if strings.HasSuffix(url, assetZipExt) {
		return a.applyZipUpdate(resp.Body)
	} else if strings.HasSuffix(url, assetTarGzExt) {
		return a.applyTarGzUpdate(resp.Body)
	}

	return update.Apply(resp.Body, update.Options{})
}

// applyZipUpdate extracts and applies an update from a zip archive
func (a *App) applyZipUpdate(body io.Reader) error {
	tmpfile, err := os.CreateTemp("", fmt.Sprintf("update-*%s", assetZipExt))
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	_, err = io.Copy(tmpfile, body)
	if err != nil {
		return err
	}

	r, err := zip.OpenReader(tmpfile.Name())
	if err != nil {
		return err
	}
	defer r.Close()

	executablePath, err := os.Executable()
	if err != nil {
		return err
	}

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}

		err = update.Apply(rc, update.Options{TargetPath: executablePath})
		rc.Close()
		return err
	}

	return fmt.Errorf("no file found in zip archive to apply")
}

// applyTarGzUpdate extracts and applies an update from a tar.gz archive
func (a *App) applyTarGzUpdate(body io.Reader) error {
	// For tar.gz files, we need to extract and find the binary
	tmpfile, err := os.CreateTemp("", fmt.Sprintf("update-*%s", assetTarGzExt))
	if err != nil {
		return err
	}
	defer os.Remove(tmpfile.Name())
	defer tmpfile.Close()

	_, err = io.Copy(tmpfile, body)
	if err != nil {
		return err
	}

	// Open the tar.gz file and extract the binary
	if _, err := tmpfile.Seek(0, 0); err != nil {
		return fmt.Errorf("failed to seek to beginning of file: %w", err)
	}

	gzr, err := gzip.NewReader(tmpfile)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// Look for the mcpproxy binary (could be mcpproxy or mcpproxy.exe)
		if strings.HasSuffix(header.Name, "mcpproxy") || strings.HasSuffix(header.Name, "mcpproxy.exe") {
			executablePath, err := os.Executable()
			if err != nil {
				return err
			}

			return update.Apply(tr, update.Options{TargetPath: executablePath})
		}
	}

	return fmt.Errorf("no mcpproxy binary found in tar.gz archive")
}

// openConfigDir opens the directory containing the configuration file
func (a *App) openConfigDir() {
	if a.configPath == "" {
		a.logger.Warn("Config path is not set, cannot open")
		return
	}

	configDir := filepath.Dir(a.configPath)
	a.openDirectory(configDir, "config directory")
}

// openLogsDir opens the logs directory
func (a *App) openLogsDir() {
	if a.server == nil {
		a.logger.Warn("Server interface not available, cannot open logs directory")
		return
	}

	logDir := a.server.GetLogDir()
	if logDir == "" {
		a.logger.Warn("Log directory path is not set, cannot open")
		return
	}

	a.openDirectory(logDir, "logs directory")
}

// openDirectory opens a directory using the OS-specific file manager
func (a *App) openDirectory(dirPath, dirType string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case osDarwin:
		cmd = exec.Command("open", dirPath)
	case osLinux:
		cmd = exec.Command("xdg-open", dirPath)
	case osWindows:
		cmd = exec.Command("explorer", dirPath)
	default:
		a.logger.Warn("Unsupported OS for opening directory", zap.String("os", runtime.GOOS))
		return
	}

	if err := cmd.Run(); err != nil {
		a.logger.Error("Failed to open directory", zap.Error(err), zap.String("dir_type", dirType), zap.String("path", dirPath))
	} else {
		a.logger.Info("Successfully opened directory", zap.String("dir_type", dirType), zap.String("path", dirPath))
	}
}

func (a *App) showPortConflictMenu(listenAddr, message string) {
	if a.portConflictMenu == nil {
		return
	}

	if listenAddr == "" && a.server != nil {
		listenAddr = a.server.GetListenAddress()
	}

	a.portConflictActive = true
	a.portConflictAddress = listenAddr

	headline := "Resolve port conflict"
	if listenAddr != "" {
		headline = fmt.Sprintf("Resolve port conflict (%s)", listenAddr)
	}
	a.portConflictMenu.SetTitle(headline)

	info := message
	if strings.TrimSpace(info) == "" {
		info = "Another process is using the configured port."
	}
	if a.portConflictInfo != nil {
		a.portConflictInfo.SetTitle(info)
		a.portConflictInfo.Disable()
	}

	if a.portConflictRetry != nil {
		a.portConflictRetry.Enable()
	}

	if a.portConflictConfig != nil {
		a.portConflictConfig.Enable()
	}

	suggestion := ""
	var err error
	if a.server != nil {
		suggestion, err = a.server.SuggestAlternateListen(listenAddr)
	}
	if err != nil {
		a.logger.Warn("Failed to suggest alternate listen address", zap.Error(err))
		a.portConflictSuggested = ""
		if a.portConflictAuto != nil {
			a.portConflictAuto.SetTitle("Find available port (retry)")
			a.portConflictAuto.Enable()
		}
	} else {
		a.portConflictSuggested = suggestion
		if a.portConflictAuto != nil {
			label := "Use next available port"
			if suggestion != "" {
				label = fmt.Sprintf("Use available port %s", suggestion)
			}
			a.portConflictAuto.SetTitle(label)
			a.portConflictAuto.Enable()
		}
	}

	if a.portConflictCopy != nil {
		connectionURL := a.buildConnectionURL(listenAddr)
		if connectionURL != "" {
			a.portConflictCopy.SetTitle(fmt.Sprintf("Copy MCP URL (%s)", connectionURL))
			a.portConflictCopy.Enable()
			a.portConflictCopy.SetTooltip("Copy the MCP connection URL to the clipboard")
		} else {
			a.portConflictCopy.SetTitle("Copy MCP URL (unavailable)")
			a.portConflictCopy.Disable()
		}
	}

	a.portConflictMenu.Show()
}

func (a *App) hidePortConflictMenu() {
	if !a.portConflictActive {
		return
	}

	a.portConflictActive = false
	a.portConflictAddress = ""
	a.portConflictSuggested = ""

	if a.portConflictMenu != nil {
		a.portConflictMenu.Hide()
		// Reset headline to default for next time
		a.portConflictMenu.SetTitle("Resolve port conflict")
	}

	if a.portConflictInfo != nil {
		a.portConflictInfo.SetTitle("Waiting for status...")
	}

	if a.portConflictRetry != nil {
		a.portConflictRetry.Disable()
	}

	if a.portConflictAuto != nil {
		a.portConflictAuto.Disable()
	}

	if a.portConflictCopy != nil {
		a.portConflictCopy.Disable()
	}

	if a.portConflictConfig != nil {
		a.portConflictConfig.Disable()
	}
}

func (a *App) handlePortConflictRetry() {
	if !a.portConflictActive {
		return
	}
	a.logger.Info("Port conflict retry requested - user should quit and restart MCPProxy")
	// In new architecture, tray doesn't control process lifecycle directly
	// User must quit tray and restart to retry on the configured port
}

func (a *App) handlePortConflictAuto() {
	if a.server == nil {
		a.logger.Warn("Port conflict auto action requested without server interface")
		return
	}

	listen := a.portConflictAddress
	if listen == "" {
		listen = a.server.GetListenAddress()
	}

	suggestion := a.portConflictSuggested
	var err error
	if suggestion == "" {
		suggestion, err = a.server.SuggestAlternateListen(listen)
		if err != nil {
			a.logger.Error("Failed to calculate alternate listen address", zap.Error(err))
			return
		}
	}

	a.logger.Info("Applying alternate listen address",
		zap.String("requested", listen),
		zap.String("alternate", suggestion))

	if err := a.server.SetListenAddress(suggestion, true); err != nil {
		a.logger.Error("Failed to update listen address", zap.Error(err), zap.String("listen", suggestion))
		return
	}

	a.hidePortConflictMenu()

	a.logger.Info("Alternate port configured - user should restart to apply changes",
		zap.String("new_port", suggestion))
	// In new architecture, config changes require manual restart
	// User must quit tray and restart to use the new port
}

func (a *App) handlePortConflictCopy() {
	if !a.portConflictActive {
		return
	}

	listen := a.portConflictAddress
	if listen == "" && a.server != nil {
		listen = a.server.GetListenAddress()
	}

	connectionURL := a.buildConnectionURL(listen)
	if connectionURL == "" {
		a.logger.Warn("Unable to build connection URL for clipboard", zap.String("listen", listen))
		return
	}

	if err := copyToClipboard(connectionURL); err != nil {
		a.logger.Error("Failed to copy connection URL to clipboard",
			zap.String("url", connectionURL),
			zap.Error(err))
		return
	}

	a.logger.Info("Copied connection URL to clipboard", zap.String("url", connectionURL))
}

func (a *App) buildConnectionURL(listenAddr string) string {
	if listenAddr == "" {
		return ""
	}

	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		a.logger.Debug("Failed to parse listen address for connection URL", zap.String("listen", listenAddr), zap.Error(err))
		return ""
	}

	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}

	return fmt.Sprintf("http://%s/mcp", net.JoinHostPort(host, port))
}

func copyToClipboard(text string) error {
	switch runtime.GOOS {
	case osDarwin:
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	case osWindows:
		cmd := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("Set-Clipboard -Value %s", quoteForPowerShell(text)))
		return cmd.Run()
	default:
		return fmt.Errorf("clipboard copy not supported on %s", runtime.GOOS)
	}
}

func quoteForPowerShell(text string) string {
	escaped := strings.ReplaceAll(text, "'", "''")
	return "'" + escaped + "'"
}

// handleServerAction is the centralized handler for all server-related menu actions.
func (a *App) handleServerAction(serverName, action string) {
	var err error
	a.logger.Info("Handling server action", zap.String("server", serverName), zap.String("action", action))

	switch action {
	case "toggle_enable":
		allServers, getErr := a.stateManager.GetAllServers()
		if getErr != nil {
			a.logger.Error("Failed to get servers for toggle action", zap.Error(getErr))
			return
		}

		var serverEnabled bool
		found := false
		for _, server := range allServers {
			if name, ok := server["name"].(string); ok && name == serverName {
				if enabled, ok := server["enabled"].(bool); ok {
					serverEnabled = enabled
					found = true
					break
				}
			}
		}

		if !found {
			a.logger.Error("Server not found for toggle action", zap.String("server", serverName))
			return
		}
		err = a.syncManager.HandleServerEnable(serverName, !serverEnabled)

	case "oauth_login":
		err = a.handleOAuthLogin(serverName)

	case "quarantine":
		err = a.syncManager.HandleServerQuarantine(serverName, true)

	case "unquarantine":
		err = a.syncManager.HandleServerUnquarantine(serverName)

	default:
		a.logger.Warn("Unknown server action requested", zap.String("action", action))
	}

	if err != nil {
		a.logger.Error("Failed to handle server action",
			zap.String("server", serverName),
			zap.String("action", action),
			zap.Error(err))
	}
}

// handleOAuthLogin handles OAuth authentication for a server from the tray menu
func (a *App) handleOAuthLogin(serverName string) error {
	a.logger.Info("Starting OAuth login from tray menu", zap.String("server", serverName))

	// Get server information from the state manager (same source as tray menu)
	allServers, err := a.stateManager.GetAllServers()
	if err != nil {
		a.logger.Error("Failed to get servers for OAuth login",
			zap.String("server", serverName),
			zap.Error(err))
		return fmt.Errorf("failed to get servers: %w", err)
	}

	// Debug: List all available servers
	var availableServerNames []string
	for _, server := range allServers {
		if name, ok := server["name"].(string); ok {
			availableServerNames = append(availableServerNames, name)
		}
	}
	a.logger.Info("Available servers from state manager",
		zap.String("requested_server", serverName),
		zap.Strings("available_servers", availableServerNames))

	// Find the requested server
	var targetServer map[string]interface{}
	for _, server := range allServers {
		if name, ok := server["name"].(string); ok && name == serverName {
			targetServer = server
			break
		}
	}

	if targetServer == nil {
		err := fmt.Errorf("server '%s' not found in available servers", serverName)
		a.logger.Error("Server not found for OAuth login",
			zap.String("server", serverName),
			zap.Strings("available_servers", availableServerNames))
		return err
	}

	a.logger.Info("Found server for OAuth",
		zap.String("server", serverName),
		zap.Any("server_data", targetServer))

	// Load the config file that mcpproxy is using
	configPath := a.server.GetConfigPath()
	if configPath == "" {
		err := fmt.Errorf("config path not available")
		a.logger.Error("Failed to get config path for OAuth login",
			zap.String("server", serverName),
			zap.Error(err))
		return err
	}

	a.logger.Info("Loading config file for OAuth",
		zap.String("server", serverName),
		zap.String("config_path", configPath))

	globalConfig, err := config.LoadFromFile(configPath)
	if err != nil {
		a.logger.Error("Failed to load server configuration for OAuth login",
			zap.String("server", serverName),
			zap.String("config_path", configPath),
			zap.Error(err))
		return fmt.Errorf("failed to load server configuration: %w", err)
	}

	// Debug: Check if server exists in config
	var serverFound bool
	for _, server := range globalConfig.Servers {
		if server.Name == serverName {
			serverFound = true
			break
		}
	}

	a.logger.Info("Server lookup in config",
		zap.String("server", serverName),
		zap.Bool("found_in_config", serverFound),
		zap.String("config_path", configPath))

	a.logger.Info("Config loaded for OAuth",
		zap.String("server", serverName),
		zap.Int("total_servers", len(globalConfig.Servers)))

	// Trigger OAuth inside the running daemon to avoid DB lock conflicts
	a.logger.Info("Triggering in-process OAuth from tray", zap.String("server", serverName))
	if err := a.server.TriggerOAuthLogin(serverName); err != nil {
		return fmt.Errorf("failed to trigger OAuth: %w", err)
	}
	return nil
}

// updateAutostartMenuItem updates the autostart menu item based on current state
func (a *App) updateAutostartMenuItem() {
	if a.autostartItem == nil || a.autostartManager == nil {
		return
	}

	if a.autostartManager.IsEnabled() {
		a.autostartItem.SetTitle("☑️ Start at Login")
		a.autostartItem.SetTooltip("mcpproxy will start automatically when you log in (click to disable)")
	} else {
		a.autostartItem.SetTitle("Start at Login")
		a.autostartItem.SetTooltip("Start mcpproxy automatically when you log in (click to enable)")
	}
}

// handleAutostartToggle handles toggling the autostart functionality
func (a *App) handleAutostartToggle() {
	if a.autostartManager == nil {
		a.logger.Warn("Autostart manager not available")
		return
	}

	a.logger.Info("Toggling autostart functionality")

	if err := a.autostartManager.Toggle(); err != nil {
		a.logger.Error("Failed to toggle autostart", zap.Error(err))
		return
	}

	// Update the menu item to reflect the new state
	a.updateAutostartMenuItem()

	// Log the new state
	if a.autostartManager.IsEnabled() {
		a.logger.Info("Autostart enabled - mcpproxy will start automatically at login")
	} else {
		a.logger.Info("Autostart disabled - mcpproxy will not start automatically at login")
	}
}

// handleOpenWebUI opens the web control panel in the default browser
func (a *App) handleOpenWebUI() {
	if a.apiClient == nil {
		a.logger.Warn("API client not available, cannot open web UI")
		return
	}

	a.logger.Info("Opening web control panel from tray menu")

	if err := a.apiClient.OpenWebUI(); err != nil {
		a.logger.Error("Failed to open web control panel", zap.Error(err))
	} else {
		a.logger.Info("Successfully opened web control panel")
	}
}
