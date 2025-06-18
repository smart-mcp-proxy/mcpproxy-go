//go:build !nogui && !headless

package tray

import (
	"archive/zip"
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

	"github.com/getlantern/systray"
	"github.com/inconshreveable/go-update"
	"go.uber.org/zap"
	"golang.org/x/mod/semver"
)

const (
	repo = "smart-mcp-proxy/mcpproxy-go" // Actual repository
)

//go:embed icon-mono-32.png
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
}

// App represents the system tray application
type App struct {
	server   ServerInterface
	logger   *zap.SugaredLogger
	version  string
	shutdown func()

	// Menu items for dynamic updates
	statusItem    *systray.MenuItem
	startStopItem *systray.MenuItem
	serversMenu   *systray.MenuItem

	// Context for background operations
	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new tray application
func New(server ServerInterface, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	ctx, cancel := context.WithCancel(context.Background())
	return &App{
		server:   server,
		logger:   logger,
		version:  version,
		shutdown: shutdown,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Run starts the system tray application
func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting system tray application")

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

	// Start background status updater (every 5 seconds)
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
		a.cancel()
		systray.Quit()
	}()

	// Start systray - this is a blocking call that must run on main thread
	systray.Run(a.onReady, a.onExit)

	return ctx.Err()
}

func (a *App) onReady() {
	a.logger.Info("System tray onReady called")

	systray.SetTitle("mcp")
	a.updateTooltip()

	// Debug: Check icon data
	a.logger.Info("Icon data loaded", zap.Int("icon_size_bytes", len(iconData)))

	// Set the tray icon
	if len(iconData) > 0 {
		systray.SetIcon(iconData)
		a.logger.Info("System tray icon set")

		// On macOS, also try setting as template icon for better integration
		if runtime.GOOS == "darwin" {
			systray.SetTemplateIcon(iconData, iconData)
			a.logger.Info("Template icon set for macOS")
		}
	} else {
		a.logger.Error("Icon data is empty - icon not embedded correctly")
	}

	// Create menu items
	a.statusItem = systray.AddMenuItem("Status: Starting...", "Server status")
	a.statusItem.Disable()

	systray.AddSeparator()

	// Start/Stop control
	a.startStopItem = systray.AddMenuItem("Start Server", "Start or stop the proxy server")

	systray.AddSeparator()

	// Upstream servers submenu
	a.serversMenu = systray.AddMenuItem("Upstream Servers", "View upstream server status")
	a.updateServersMenu()

	systray.AddSeparator()

	mUpdate := systray.AddMenuItem("Check for Updates…", "Check for application updates")
	mConfig := systray.AddMenuItem("Open Config", "Open configuration file")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit mcpproxy")

	// Initial status update
	a.updateStatus()

	// Handle menu clicks
	go func() {
		for {
			select {
			case <-a.startStopItem.ClickedCh:
				go a.handleStartStop()
			case <-mUpdate.ClickedCh:
				go a.checkForUpdates()
			case <-mConfig.ClickedCh:
				go a.openConfig()
			case <-mQuit.ClickedCh:
				a.logger.Info("Quit requested from tray menu")
				if a.shutdown != nil {
					a.shutdown()
				}
				systray.Quit()
				return
			case <-a.ctx.Done():
				return
			}
		}
	}()
}

func (a *App) updateTooltip() {
	var tooltip strings.Builder
	tooltip.WriteString("Smart MCP Proxy")

	if a.server != nil {
		if a.server.IsRunning() {
			tooltip.WriteString(" - Running")
			if addr := a.server.GetListenAddress(); addr != "" {
				tooltip.WriteString(fmt.Sprintf("\nURL: http://localhost%s/mcp", addr))
			}
		} else {
			tooltip.WriteString(" - Stopped")
		}

		stats := a.server.GetUpstreamStats()
		if stats != nil {
			if totalServers, ok := stats["total_servers"].(int); ok {
				if connectedServers, ok := stats["connected_servers"].(int); ok {
					tooltip.WriteString(fmt.Sprintf("\nServers: %d/%d connected", connectedServers, totalServers))
				}
			}

			// Count total tools across all servers
			if servers, ok := stats["servers"].(map[string]interface{}); ok {
				totalTools := 0
				for _, serverInfo := range servers {
					if serverMap, ok := serverInfo.(map[string]interface{}); ok {
						if toolCount, ok := serverMap["tool_count"].(int); ok {
							totalTools += toolCount
						}
					}
				}
				if totalTools > 0 {
					tooltip.WriteString(fmt.Sprintf("\nTools: %d available", totalTools))
				}
			}
		}
	}

	systray.SetTooltip(tooltip.String())
}

// updateStatusFromData updates the tray status from real-time status data
func (a *App) updateStatusFromData(statusData interface{}) {
	// Handle different status data structures
	switch status := statusData.(type) {
	case map[string]interface{}:
		message := "Status unknown"
		phase := "Unknown"

		if m, ok := status["message"].(string); ok {
			message = m
		}
		if p, ok := status["phase"].(string); ok {
			phase = p
		}

		// Update status item in the menu
		if a.statusItem != nil {
			var statusText string

			// Handle different phases
			switch phase {
			case "Error":
				statusText = "Status: Error"
				// Update start/stop button to show "Start Server" for retry
				if a.startStopItem != nil {
					a.startStopItem.SetTitle("Start Server")
				}
			case "Starting":
				statusText = "Status: Starting..."
				// Keep button as "Start Server" during startup
				if a.startStopItem != nil {
					a.startStopItem.SetTitle("Start Server")
				}
			case "Stopping":
				statusText = "Status: Stopping..."
				// Keep button as "Stop Server" during shutdown
				if a.startStopItem != nil {
					a.startStopItem.SetTitle("Stop Server")
				}
			default:
				// Use actual server running state for all other phases
				if a.server != nil && a.server.IsRunning() {
					statusText = "Status: Running"
					if addr := a.server.GetListenAddress(); addr != "" {
						statusText += fmt.Sprintf(" (%s)", addr)
					}
					// Update start/stop button to show "Stop Server"
					if a.startStopItem != nil {
						a.startStopItem.SetTitle("Stop Server")
					}
				} else {
					statusText = "Status: Stopped"
					// Update start/stop button to show "Start Server"
					if a.startStopItem != nil {
						a.startStopItem.SetTitle("Start Server")
					}
				}
			}

			a.statusItem.SetTitle(statusText)
			a.statusItem.SetTooltip(message)
		}

		// Update tooltip with detailed info
		a.updateTooltipFromStatusData(status)

		// Update servers menu with connection details
		a.updateServersMenuFromStatusData(status)
	}
}

// updateTooltipFromStatusData updates the tooltip with detailed status information
func (a *App) updateTooltipFromStatusData(status map[string]interface{}) {
	var tooltip strings.Builder
	tooltip.WriteString("Smart MCP Proxy")

	if phase, ok := status["phase"].(string); ok {
		tooltip.WriteString(fmt.Sprintf(" - %s", phase))
	}

	if message, ok := status["message"].(string); ok && message != "" {
		tooltip.WriteString(fmt.Sprintf("\n%s", message))
	}

	if upstreamStats, ok := status["upstream_stats"].(map[string]interface{}); ok {
		if totalServers, ok := upstreamStats["total_servers"].(int); ok {
			if connectedServers, ok := upstreamStats["connected_servers"].(int); ok {
				tooltip.WriteString(fmt.Sprintf("\nServers: %d/%d connected", connectedServers, totalServers))
			}
		}

		if connectingServers, ok := upstreamStats["connecting_servers"].(int); ok && connectingServers > 0 {
			tooltip.WriteString(fmt.Sprintf(" (%d connecting)", connectingServers))
		}

		if totalTools, ok := upstreamStats["total_tools"].(int); ok {
			tooltip.WriteString(fmt.Sprintf("\nTools: %d indexed", totalTools))
		}
	}

	systray.SetTooltip(tooltip.String())
}

// updateServersMenuFromStatusData updates the servers menu with connection status
func (a *App) updateServersMenuFromStatusData(status map[string]interface{}) {
	if a.serversMenu == nil {
		return
	}

	// Update the servers menu title with connection counts
	if upstreamStats, ok := status["upstream_stats"].(map[string]interface{}); ok {
		if totalServers, ok := upstreamStats["total_servers"].(int); ok {
			if connectedServers, ok := upstreamStats["connected_servers"].(int); ok {
				menuTitle := fmt.Sprintf("Upstream Servers (%d/%d)", connectedServers, totalServers)
				a.serversMenu.SetTitle(menuTitle)

				// Also update tooltip with detailed server info
				if servers, ok := upstreamStats["servers"].(map[string]interface{}); ok {
					var serverDetails strings.Builder
					for _, serverInfo := range servers {
						if serverMap, ok := serverInfo.(map[string]interface{}); ok {
							name := "Unknown"
							if n, ok := serverMap["name"].(string); ok {
								name = n
							}

							connected := false
							if c, ok := serverMap["connected"].(bool); ok {
								connected = c
							}

							connecting := false
							if c, ok := serverMap["connecting"].(bool); ok {
								connecting = c
							}

							retryCount := 0
							if rc, ok := serverMap["retry_count"].(int); ok {
								retryCount = rc
							}

							var statusText string
							if connected {
								statusText = "Connected"
							} else if connecting {
								statusText = "Connecting..."
							} else if retryCount > 0 {
								statusText = fmt.Sprintf("Retrying (%d)", retryCount)
							} else {
								statusText = "Disconnected"
							}

							if serverDetails.Len() > 0 {
								serverDetails.WriteString("\n")
							}
							serverDetails.WriteString(fmt.Sprintf("• %s: %s", name, statusText))
						}
					}

					if serverDetails.Len() > 0 {
						a.serversMenu.SetTooltip(serverDetails.String())
					}
				}
			}
		}
	}
}

func (a *App) updateStatus() {
	if a.server == nil || a.statusItem == nil {
		return
	}

	var statusText string
	if a.server.IsRunning() {
		statusText = "Status: Running"
		if addr := a.server.GetListenAddress(); addr != "" {
			statusText += fmt.Sprintf(" (%s)", addr)
		}
		a.startStopItem.SetTitle("Stop Server")
	} else {
		statusText = "Status: Stopped"
		a.startStopItem.SetTitle("Start Server")
	}

	a.statusItem.SetTitle(statusText)
	a.updateTooltip()
	a.updateServersMenu()
}

func (a *App) updateServersMenu() {
	if a.server == nil || a.serversMenu == nil {
		return
	}

	// Clear existing submenu items (this is a limitation of systray - we can't dynamically remove items)
	// So we update the main menu title to include server count
	stats := a.server.GetUpstreamStats()
	if stats != nil {
		totalServers := 0
		connectedServers := 0
		if total, ok := stats["total_servers"].(int); ok {
			totalServers = total
		}
		if connected, ok := stats["connected_servers"].(int); ok {
			connectedServers = connected
		}

		menuTitle := fmt.Sprintf("Upstream Servers (%d/%d)", connectedServers, totalServers)
		a.serversMenu.SetTitle(menuTitle)

		// Since we can't dynamically update submenus easily with systray,
		// we'll update the tooltip to show server details
		var serverDetails strings.Builder
		if servers, ok := stats["servers"].(map[string]interface{}); ok {
			for _, serverInfo := range servers {
				if serverMap, ok := serverInfo.(map[string]interface{}); ok {
					name := "Unknown"
					if n, ok := serverMap["name"].(string); ok {
						name = n
					}

					connected := false
					if c, ok := serverMap["connected"].(bool); ok {
						connected = c
					}

					toolCount := 0
					if tc, ok := serverMap["tool_count"].(int); ok {
						toolCount = tc
					}

					status := "Disconnected"
					if connected {
						status = "Connected"
					}

					if serverDetails.Len() > 0 {
						serverDetails.WriteString("\n")
					}
					serverDetails.WriteString(fmt.Sprintf("• %s: %s (%d tools)", name, status, toolCount))
				}
			}
		}

		if serverDetails.Len() > 0 {
			a.serversMenu.SetTooltip(serverDetails.String())
		}
	}
}

func (a *App) handleStartStop() {
	if a.server == nil {
		return
	}

	if a.server.IsRunning() {
		a.logger.Info("Stopping server from tray")
		if err := a.server.StopServer(); err != nil {
			a.logger.Error("Failed to stop server", zap.Error(err))
		}
	} else {
		a.logger.Info("Starting server from tray")
		if err := a.server.StartServer(a.ctx); err != nil {
			a.logger.Error("Failed to start server", zap.Error(err))
		}
	}

	// Update status immediately
	a.updateStatus()
}

func (a *App) onExit() {
	a.logger.Info("System tray exiting")
	a.cancel()
}

func (a *App) checkForUpdates() {
	a.logger.Info("Checking for updates...")

	if !semver.IsValid(a.version) {
		a.logger.Warn("Invalid version format, cannot check for updates", zap.String("version", a.version))
		return
	}

	// Get latest release from GitHub
	release, err := a.getLatestRelease()
	if err != nil {
		a.logger.Error("Failed to get latest release", zap.Error(err))
		return
	}

	if !semver.IsValid(release.TagName) {
		a.logger.Warn("Invalid release version", zap.String("version", release.TagName))
		return
	}

	// Compare versions
	if semver.Compare(a.version, release.TagName) >= 0 {
		a.logger.Info("No updates found")
		return
	}

	// Find the appropriate asset for this OS/arch
	assetURL, err := a.findAssetURL(release)
	if err != nil {
		a.logger.Error("Failed to find asset for update", zap.Error(err))
		return
	}

	// Download and apply update
	if err := a.downloadAndApplyUpdate(assetURL); err != nil {
		a.logger.Error("Failed to apply update", zap.Error(err))
		return
	}

	a.logger.Info("Updated successfully", zap.String("new_version", release.TagName))
	fmt.Printf("Updated to %s - restarting…\n", release.TagName)

	// Give user time to read the message
	time.Sleep(2 * time.Second)

	// Exit so the system can restart the application
	if a.shutdown != nil {
		a.shutdown()
	}

	// Force kill the process to ensure restart
	exec.Command("pkill", "-f", "mcpproxy").Run()
	os.Exit(0)
}

func (a *App) getLatestRelease() (*GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

func (a *App) findAssetURL(release *GitHubRelease) (string, error) {
	osName := runtime.GOOS
	archName := runtime.GOARCH

	// Convert Go arch names to common release naming
	if archName == "amd64" {
		archName = "x86_64"
	}

	// Special handling for macOS - look for universal binary first
	if osName == "darwin" {
		for _, asset := range release.Assets {
			name := strings.ToLower(asset.Name)
			// Look for macOS universal binary first (from our DMG creation workflow)
			if strings.Contains(name, "macos") && strings.Contains(name, "universal") {
				return asset.BrowserDownloadURL, nil
			}
		}
	}

	// Look for assets that match our OS and architecture
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, osName) && strings.Contains(name, archName) {
			return asset.BrowserDownloadURL, nil
		}
	}

	return "", fmt.Errorf("no asset found for %s/%s", osName, archName)
}

func (a *App) downloadAndApplyUpdate(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Check if this is a ZIP file (macOS universal binary)
	if strings.Contains(url, ".zip") {
		return a.applyZipUpdate(resp.Body)
	}

	// Apply the update for regular archives
	err = update.Apply(resp.Body, update.Options{})
	if err != nil {
		// If update fails, try to rollback
		if rollbackErr := update.RollbackError(err); rollbackErr != nil {
			return fmt.Errorf("update failed and rollback failed: %v (rollback: %v)", err, rollbackErr)
		}
		return err
	}

	return nil
}

// applyZipUpdate handles ZIP file updates (for macOS universal binaries)
func (a *App) applyZipUpdate(body io.Reader) error {
	// Create temporary file for ZIP
	tmpFile, err := os.CreateTemp("", "mcpproxy-update-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Copy ZIP content to temp file
	if _, err := io.Copy(tmpFile, body); err != nil {
		return fmt.Errorf("failed to write temp file: %v", err)
	}

	// Close temp file before reading
	tmpFile.Close()

	// Open ZIP file
	zipReader, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		return fmt.Errorf("failed to open ZIP: %v", err)
	}
	defer zipReader.Close()

	// Find the binary in the ZIP
	var binaryFile *zip.File
	for _, file := range zipReader.File {
		if strings.Contains(file.Name, "mcpproxy") && !strings.Contains(file.Name, "/") {
			binaryFile = file
			break
		}
	}

	if binaryFile == nil {
		return fmt.Errorf("binary not found in ZIP")
	}

	// Extract and apply the binary
	reader, err := binaryFile.Open()
	if err != nil {
		return fmt.Errorf("failed to open binary in ZIP: %v", err)
	}
	defer reader.Close()

	// Apply the update
	err = update.Apply(reader, update.Options{})
	if err != nil {
		// If update fails, try to rollback
		if rollbackErr := update.RollbackError(err); rollbackErr != nil {
			return fmt.Errorf("update failed and rollback failed: %v (rollback: %v)", err, rollbackErr)
		}
		return err
	}

	return nil
}

func (a *App) openConfig() {
	// Try to open the config file with the default editor
	configPath := "mcp_config.json"

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", configPath)
	case "linux":
		cmd = exec.Command("xdg-open", configPath)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", configPath)
	default:
		a.logger.Error("Unsupported OS for opening config file")
		return
	}

	if err := cmd.Run(); err != nil {
		a.logger.Error("Failed to open config file", zap.Error(err))
	}
}
