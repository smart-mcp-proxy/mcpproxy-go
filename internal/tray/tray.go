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
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"fyne.io/systray"
	"github.com/fsnotify/fsnotify"
	"github.com/inconshreveable/go-update"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"

	"mcpproxy-go/internal/server"
)

const (
	repo     = "smart-mcp-proxy/mcpproxy-go" // Actual repository
	osDarwin = "darwin"
)

//go:embed icon-mono-44.png
var iconData []byte

// GitHubRelease represents a GitHub release
type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
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

	// Quarantine management methods
	GetQuarantinedServers() ([]map[string]interface{}, error)
	UnquarantineServer(serverName string) error

	// Server management methods for tray menu
	EnableServer(serverName string, enabled bool) error
	QuarantineServer(serverName string, quarantined bool) error
	DeleteServer(serverName string) error
	GetAllServers() ([]map[string]interface{}, error)

	// Config management for file watching
	ReloadConfiguration() error
	GetConfigPath() string

	// Direct notification methods for immediate updates
	ForceMenuUpdate() // Force immediate menu refresh
}

// App represents the system tray application
type App struct {
	server   ServerInterface
	logger   *zap.SugaredLogger
	version  string
	shutdown func()

	// Menu items for dynamic updates
	statusItem          *systray.MenuItem
	startStopItem       *systray.MenuItem
	upstreamServersMenu *systray.MenuItem
	quarantineMenu      *systray.MenuItem

	// Managers for proper synchronization
	stateManager *ServerStateManager
	menuManager  *MenuManager
	syncManager  *SynchronizationManager

	// Config file watching
	configWatcher *fsnotify.Watcher
	configPath    string

	// Context for background operations
	ctx    context.Context
	cancel context.CancelFunc

	// Legacy fields for compatibility during transition
	lastRunningState bool // Track last known server running state

	// Menu tracking fields for dynamic updates
	forceRefresh      bool                         // Force menu refresh flag
	menusInitialized  bool                         // Track if menus have been initialized
	lastServerList    []string                     // Track last known server list for change detection
	serverMenus       map[string]*systray.MenuItem // Track server menu items
	serverActionMenus map[string]*systray.MenuItem // Track server action menu items

	// Quarantine menu tracking fields
	lastQuarantineList    []string                     // Track last known quarantine list for change detection
	quarantineServerMenus map[string]*systray.MenuItem // Track quarantine server menu items
}

// New creates a new tray application
func New(server ServerInterface, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	app := &App{
		server:   server,
		logger:   logger,
		version:  version,
		shutdown: shutdown,
	}

	// Initialize menu tracking maps
	app.serverMenus = make(map[string]*systray.MenuItem)
	app.serverActionMenus = make(map[string]*systray.MenuItem)
	app.quarantineServerMenus = make(map[string]*systray.MenuItem)
	app.lastServerList = []string{}
	app.lastQuarantineList = []string{}

	return app
}

// Run starts the system tray application
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting system tray application")
	a.ctx, a.cancel = context.WithCancel(ctx)
	defer a.cancel()

	// Initialize config file watcher
	if err := a.initConfigWatcher(); err != nil {
		a.logger.Warn("Failed to initialize config file watcher", zap.Error(err))
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
	go func() {
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

	// Start config file watcher
	if a.configWatcher != nil {
		go a.watchConfigFile()
	}

	// Listen for real-time status updates
	if a.server != nil {
		go func() {
			statusCh := a.server.StatusChannel()
			for {
				select {
				case status := <-statusCh:
					a.updateStatusFromData(status)
				case <-ctx.Done():
					return
				}
			}
		}()
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

// initConfigWatcher initializes the config file watcher
func (a *App) initConfigWatcher() error {
	if a.server == nil {
		return fmt.Errorf("server interface not available")
	}

	configPath := a.server.GetConfigPath()
	if configPath == "" {
		return fmt.Errorf("config path not available")
	}

	a.configPath = configPath

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create file watcher: %w", err)
	}

	a.configWatcher = watcher

	// Watch the config file
	if err := a.configWatcher.Add(configPath); err != nil {
		a.configWatcher.Close()
		return fmt.Errorf("failed to watch config file %s: %w", configPath, err)
	}

	a.logger.Info("Config file watcher initialized", zap.String("path", configPath))
	return nil
}

// watchConfigFile watches for config file changes and reloads configuration
func (a *App) watchConfigFile() {
	defer a.configWatcher.Close()

	for {
		select {
		case event, ok := <-a.configWatcher.Events:
			if !ok {
				return
			}

			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				a.logger.Info("Config file changed, reloading configuration", zap.String("event", event.String()))

				// Add a small delay to ensure file write is complete
				time.Sleep(500 * time.Millisecond)

				if err := a.server.ReloadConfiguration(); err != nil {
					a.logger.Error("Failed to reload configuration", zap.Error(err))
				} else {
					a.logger.Info("Configuration reloaded successfully")
					// Force a menu refresh after config reload
					a.forceRefresh = true
					a.refreshMenusImmediate()
				}
			}

		case err, ok := <-a.configWatcher.Errors:
			if !ok {
				return
			}
			a.logger.Error("Config file watcher error", zap.Error(err))

		case <-a.ctx.Done():
			return
		}
	}
}

// cleanup performs cleanup operations
func (a *App) cleanup() {
	a.logger.Info("Cleaning up tray application")
	if a.cancel != nil {
		a.cancel()
	}
	if a.configWatcher != nil {
		a.configWatcher.Close()
	}
	if a.syncManager != nil {
		a.syncManager.Stop()
	}
}

func (a *App) onReady() {
	a.logger.Info("Tray is ready")
	systray.SetTemplateIcon(iconData, iconData)

	// Set initial status
	a.statusItem = systray.AddMenuItem("Status: Initializing...", "Proxy server status")
	a.startStopItem = systray.AddMenuItem("Stop Server", "Stop the proxy server")
	systray.AddSeparator()

	// Upstream servers menu (dynamic)
	a.upstreamServersMenu = systray.AddMenuItem("Upstream Servers", "Manage upstream MCP servers")

	// Security quarantine menu (dynamic)
	a.quarantineMenu = systray.AddMenuItem("Security Quarantine", "Manage quarantined servers")
	systray.AddSeparator()

	// Initialize managers now that menu items are created
	a.menuManager = NewMenuManager(a.upstreamServersMenu, a.quarantineMenu, a.logger)
	a.syncManager = NewSynchronizationManager(nil, a.menuManager, a.logger)
	a.stateManager = NewServerStateManager(a.server, a.logger, a.syncManager)
	a.syncManager.SetStateManager(a.stateManager) // Complete the circular dependency

	// Centralized action handler for all menu-driven server actions
	a.menuManager.SetActionCallback(a.handleServerAction)

	// Other menu items
	updateItem := systray.AddMenuItem("Check for Updates...", "Check for new application updates")
	openConfigItem := systray.AddMenuItem("Open Config", "Open the configuration file")
	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Quit", "Quit the application")

	// Start background tasks
	a.syncManager.Start()
	a.updateStatus()

	// Handle clicks
	go func() {
		for {
			select {
			case <-a.startStopItem.ClickedCh:
				a.handleStartStop()
			case <-updateItem.ClickedCh:
				a.checkForUpdates()
			case <-openConfigItem.ClickedCh:
				a.openConfig()
			case <-quitItem.ClickedCh:
				systray.Quit()
			}
		}
	}()

	a.logger.Info("Tray application fully initialized")
	a.menusInitialized = true
}

// updateTooltip updates the tooltip based on the server's running state
func (a *App) updateTooltip() {
	if a.server == nil {
		systray.SetTooltip("mcpproxy is stopped")
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

	// Check if menu items are initialized to prevent nil pointer dereference
	if a.statusItem == nil || a.startStopItem == nil {
		a.logger.Debug("Menu items not initialized yet, skipping status update")
		return
	}

	// Debug logging to track status updates
	running, _ := status["running"].(bool)
	phase, _ := status["phase"].(string)
	serverRunning := a.server != nil && a.server.IsRunning()

	a.logger.Debug("Updating tray status",
		zap.Bool("status_running", running),
		zap.Bool("server_is_running", serverRunning),
		zap.String("phase", phase),
		zap.Any("status_data", status))

	// Use the actual server running state as the authoritative source
	actuallyRunning := serverRunning

	// Update running status and start/stop button
	if actuallyRunning {
		listenAddr, _ := status["listen_addr"].(string)
		if listenAddr != "" {
			a.statusItem.SetTitle(fmt.Sprintf("Status: Running (%s)", listenAddr))
		} else {
			a.statusItem.SetTitle("Status: Running")
		}
		a.startStopItem.SetTitle("Stop Server")
		a.logger.Debug("Set tray to running state")
	} else {
		a.statusItem.SetTitle("Status: Stopped")
		a.startStopItem.SetTitle("Start Server")
		a.logger.Debug("Set tray to stopped state")
	}

	// Update tooltip
	a.updateTooltipFromStatusData(status)

	// Update server menus using the manager
	if a.syncManager != nil {
		if actuallyRunning {
			// Check if this is an upstream server change or force update notification
			if message, ok := status["message"].(string); ok {
				if strings.Contains(message, "upstream servers") || strings.Contains(message, "Upstream servers") ||
					strings.Contains(message, "Force menu update") {
					a.logger.Info("Detected server change or force update, forcing immediate menu sync",
						zap.String("message", message))
					// Force immediate sync for upstream server changes and forced updates
					a.syncManager.SyncNow()
				} else {
					// Normal delayed sync for other changes
					a.syncManager.SyncDelayed()
				}
			} else {
				a.syncManager.SyncDelayed()
			}
		} else {
			// Server is stopped - trigger sync to update menus appropriately
			// (This will clear upstream servers but keep quarantine servers visible)
			a.syncManager.SyncNow()
		}
	}
}

// updateTooltipFromStatusData updates the tray tooltip from status data map
func (a *App) updateTooltipFromStatusData(status map[string]interface{}) {
	running, _ := status["running"].(bool)

	if !running {
		systray.SetTooltip("mcpproxy is stopped")
		return
	}

	// Build comprehensive tooltip for running server
	listenAddr, _ := status["listen_addr"].(string)
	phase, _ := status["phase"].(string)
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
}

// updateServersMenuFromStatusData is a legacy method, functionality is now in MenuManager
func (a *App) updateServersMenuFromStatusData(_ map[string]interface{}) {
	// This function is kept for reference during transition but the primary
	// logic is now handled by the MenuManager and SynchronizationManager.
	// We trigger a sync instead of manually updating here.
	if a.syncManager != nil {
		a.syncManager.SyncDelayed()
	}
}

// updateStatus updates the status menu item and tooltip
func (a *App) updateStatus() {
	if a.server == nil {
		return
	}

	// Check if menu items are initialized
	if a.statusItem == nil {
		a.logger.Debug("Menu items not initialized yet, skipping status update")
		return
	}

	statusData := a.server.GetStatus()
	a.updateStatusFromData(statusData)
}

// updateServersMenu is a legacy method, now triggers a sync
func (a *App) updateServersMenu() {
	if a.syncManager != nil {
		a.syncManager.SyncDelayed()
	}
}

// handleStartStop toggles the server's running state
func (a *App) handleStartStop() {
	if a.server.IsRunning() {
		a.logger.Info("Stopping server from tray")

		// Immediately update UI to show stopping state
		if a.statusItem != nil {
			a.statusItem.SetTitle("Status: Stopping...")
		}
		if a.startStopItem != nil {
			a.startStopItem.SetTitle("Stopping...")
		}

		// Stop the server
		if err := a.server.StopServer(); err != nil {
			a.logger.Error("Failed to stop server", zap.Error(err))
			// Restore UI state on error
			a.updateStatus()
			return
		}

		// Wait for server to fully stop with timeout
		go func() {
			timeout := time.After(10 * time.Second)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeout:
					a.logger.Warn("Timeout waiting for server to stop, updating status anyway")
					a.updateStatus()
					return
				case <-ticker.C:
					if !a.server.IsRunning() {
						a.logger.Info("Server stopped, updating UI")
						a.updateStatus()
						return
					}
				}
			}
		}()
	} else {
		a.logger.Info("Starting server from tray")

		// Immediately update UI to show starting state
		if a.statusItem != nil {
			a.statusItem.SetTitle("Status: Starting...")
		}
		if a.startStopItem != nil {
			a.startStopItem.SetTitle("Starting...")
		}

		// Start the server
		go func() {
			if err := a.server.StartServer(a.ctx); err != nil {
				a.logger.Error("Failed to start server", zap.Error(err))
				// Restore UI state on error
				a.updateStatus()
				return
			}

			// Wait for server to fully start with timeout
			timeout := time.After(10 * time.Second)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeout:
					a.logger.Warn("Timeout waiting for server to start, updating status anyway")
					a.updateStatus()
					return
				case <-ticker.C:
					if a.server.IsRunning() {
						a.logger.Info("Server started, updating UI")
						a.updateStatus()
						return
					}
				}
			}
		}()
	}
}

// onExit is called when the application is quitting
func (a *App) onExit() {
	a.logger.Info("Tray is exiting")
	a.cleanup()
	if a.cancel != nil {
		a.cancel()
	}
}

// checkForUpdates checks for new releases on GitHub
func (a *App) checkForUpdates() {
	// Check if auto-update is disabled by environment variable
	if os.Getenv("MCPPROXY_DISABLE_AUTO_UPDATE") == "true" {
		a.logger.Info("Auto-update disabled by environment variable")
		return
	}

	// Check if notification-only mode is enabled
	notifyOnly := os.Getenv("MCPPROXY_UPDATE_NOTIFY_ONLY") == "true"

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

// findAssetURL finds the correct asset URL for the current system
func (a *App) findAssetURL(release *GitHubRelease) (string, error) {
	// Check if this is a Homebrew installation to avoid conflicts
	if a.isHomebrewInstallation() {
		return "", fmt.Errorf("auto-update disabled for Homebrew installations - use 'brew upgrade mcpproxy' instead")
	}

	// Determine file extension based on platform
	var extension string
	switch runtime.GOOS {
	case "windows":
		extension = ".zip"
	default: // macOS, Linux
		extension = ".tar.gz"
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

// downloadAndApplyUpdate downloads and applies the update
func (a *App) downloadAndApplyUpdate(url string) error {
	resp, err := http.Get(url) // #nosec G107 -- URL is from GitHub releases API which is trusted
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if strings.HasSuffix(url, ".zip") {
		return a.applyZipUpdate(resp.Body)
	} else if strings.HasSuffix(url, ".tar.gz") {
		return a.applyTarGzUpdate(resp.Body)
	}

	return update.Apply(resp.Body, update.Options{})
}

// applyZipUpdate extracts and applies an update from a zip archive
func (a *App) applyZipUpdate(body io.Reader) error {
	tmpfile, err := os.CreateTemp("", "update-*.zip")
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
	tmpfile, err := os.CreateTemp("", "update-*.tar.gz")
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
	tmpfile.Seek(0, 0)

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

// openConfig opens the configuration file in the default editor
func (a *App) openConfig() {
	if a.configPath == "" {
		a.logger.Warn("Config path is not set, cannot open")
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", a.configPath)
	case "linux":
		cmd = exec.Command("xdg-open", a.configPath)
	case "windows":
		cmd = exec.Command("cmd", "/C", "start", a.configPath)
	default:
		a.logger.Warn("Unsupported OS for opening config file", zap.String("os", runtime.GOOS))
		return
	}

	if err := cmd.Run(); err != nil {
		a.logger.Error("Failed to open config file", zap.Error(err))
	}
}

// refreshMenusDelayed refreshes menus after a delay using the synchronization manager
func (a *App) refreshMenusDelayed() {
	if a.syncManager != nil {
		a.syncManager.SyncDelayed()
	} else {
		a.logger.Warn("Sync manager not initialized for delayed refresh")
	}
}

// refreshMenusImmediate refreshes menus immediately using the synchronization manager
func (a *App) refreshMenusImmediate() {
	if err := a.syncManager.SyncNow(); err != nil {
		a.logger.Error("Failed to refresh menus immediately", zap.Error(err))
	}
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

	case "quarantine":
		err = a.syncManager.HandleServerQuarantine(serverName, true)

	case "unquarantine":
		err = a.syncManager.HandleServerUnquarantine(serverName)

	case "delete":
		err = a.syncManager.HandleServerDelete(serverName)

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

// stringSlicesEqual compares two string slices for equality
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
