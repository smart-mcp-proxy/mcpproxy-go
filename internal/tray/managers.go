//go:build !nogui && !headless && !linux

package tray

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"go.uber.org/zap"

)

const (
	actionEnable  = "enable"
	actionDisable = "disable"
	textEnable    = "Enable"
	textDisable   = "Disable"
)

// Icon file paths relative to executable
const (
	iconConnected    = "assets/status/green-circle.ico"
	iconDisconnected = "assets/status/red-circle.ico"
	iconPaused       = "assets/status/pause.ico"
	iconLocked       = "assets/status/locked.ico"
)

// Icon cache to avoid loading the same icon multiple times
var (
	iconCache     = make(map[string][]byte)
	iconCacheMu   sync.RWMutex
	useIconsCache = runtime.GOOS == osWindows // Icons work best on Windows
)

// loadIcon loads an icon from file and caches it
func loadIcon(iconPath string) []byte {
	if !useIconsCache {
		return nil
	}

	// Check cache first
	iconCacheMu.RLock()
	if data, ok := iconCache[iconPath]; ok {
		iconCacheMu.RUnlock()
		return data
	}
	iconCacheMu.RUnlock()

	// Get executable directory
	exePath, err := os.Executable()
	if err != nil {
		return nil
	}
	exeDir := filepath.Dir(exePath)

	// Build full path
	fullPath := filepath.Join(exeDir, iconPath)

	// Load icon file
	data, err := os.ReadFile(fullPath)
	if err != nil {
		// Try relative to current directory as fallback
		data, err = os.ReadFile(iconPath)
		if err != nil {
			return nil
		}
	}

	// Cache the icon
	iconCacheMu.Lock()
	iconCache[iconPath] = data
	iconCacheMu.Unlock()

	return data
}

// ServerStateManager manages server state synchronization between storage, config, and menu
type ServerStateManager struct {
	server ServerInterface
	logger *zap.SugaredLogger
	mu     sync.RWMutex

	// Current state tracking
	allServers           []map[string]interface{}
	quarantinedServers   []map[string]interface{}
	lastUpdate           time.Time
	lastQuarantineUpdate time.Time // Separate timestamp for quarantine data
}

// NewServerStateManager creates a new server state manager
func NewServerStateManager(server ServerInterface, logger *zap.SugaredLogger) *ServerStateManager {
	return &ServerStateManager{
		server: server,
		logger: logger,
	}
}

// RefreshState forces a refresh of server state from the server
func (m *ServerStateManager) RefreshState() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get all servers
	allServers, err := m.server.GetAllServers()
	if err != nil {
		m.logger.Error("RefreshState failed to get all servers", zap.Error(err))
		return fmt.Errorf("failed to get all servers: %w", err)
	}

	// Get quarantined servers
	quarantinedServers, err := m.server.GetQuarantinedServers()
	if err != nil {
		m.logger.Error("RefreshState failed to get quarantined servers", zap.Error(err))
		return fmt.Errorf("failed to get quarantined servers: %w", err)
	}

	m.allServers = allServers
	m.quarantinedServers = quarantinedServers
	m.lastUpdate = time.Now()
	m.lastQuarantineUpdate = time.Now()

	m.logger.Debug("Server state refreshed",
		zap.Int("all_servers", len(allServers)),
		zap.Int("quarantined_servers", len(quarantinedServers)))

	return nil
}

// GetAllServers returns cached or fresh server list
func (m *ServerStateManager) GetAllServers() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return cached data if available and recent (only if THIS data has been loaded before)
	if time.Since(m.lastUpdate) < 2*time.Second && !m.lastUpdate.IsZero() && m.allServers != nil {
		return m.allServers, nil
	}

	// Get fresh data but handle database errors gracefully
	servers, err := m.server.GetAllServers()
	if err != nil {
		// If database is closed, return cached data if available
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			if len(m.allServers) > 0 {
				m.logger.Debug("Database not available, returning cached server data")
				return m.allServers, nil
			}
			// No cached data available, return error
			m.logger.Debug("Database not available and no cached data, preserving UI state")
			return nil, fmt.Errorf("database not available and no cached data: %w", err)
		}
		// API error - return error without fallback to enforce tray/core separation
		m.logger.Error("Failed to get fresh all servers data", zap.Error(err))
		return nil, err
	}

	// Only update cache if we got valid data (non-empty or intentionally empty)
	// This prevents overwriting good cached data with temporary empty results
	if servers != nil {
		m.allServers = servers
		m.lastUpdate = time.Now()
	}
	return servers, nil
}

// GetQuarantinedServers returns cached or fresh quarantined server list
func (m *ServerStateManager) GetQuarantinedServers() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return cached data if available and recent (only if data has been loaded before)
	if time.Since(m.lastQuarantineUpdate) < 2*time.Second && !m.lastQuarantineUpdate.IsZero() {
		return m.quarantinedServers, nil
	}

	// Get fresh data but handle database errors gracefully
	servers, err := m.server.GetQuarantinedServers()
	if err != nil {
		// If database is closed, return cached data if available
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			if len(m.quarantinedServers) > 0 {
				m.logger.Debug("Database not available, returning cached quarantine data")
				return m.quarantinedServers, nil
			}
			// No cached data available, return error
			m.logger.Debug("Database not available and no cached data, preserving quarantine UI state")
			return nil, fmt.Errorf("database not available and no cached data: %w", err)
		}
		// API error - return error without fallback to enforce tray/core separation
		m.logger.Error("Failed to get fresh quarantined servers data", zap.Error(err))
		return nil, err
	}

	// Only update cache if we got valid data
	if servers != nil {
		m.quarantinedServers = servers
		m.lastQuarantineUpdate = time.Now()
	}
	return servers, nil
}

// QuarantineServer quarantines a server and ensures all state is synchronized
func (m *ServerStateManager) QuarantineServer(serverName string, quarantined bool) error {
	m.logger.Info("QuarantineServer called",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	// Update the server quarantine status
	if err := m.server.QuarantineServer(serverName, quarantined); err != nil {
		return fmt.Errorf("failed to quarantine server: %w", err)
	}

	// Force state refresh immediately after the change
	if err := m.RefreshState(); err != nil {
		m.logger.Error("Failed to refresh state after quarantine change", zap.Error(err))
		// Don't return error here as the quarantine operation itself succeeded
	}

	m.logger.Info("Server quarantine status updated successfully",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	return nil
}

// UnquarantineServer removes a server from quarantine and ensures all state is synchronized
func (m *ServerStateManager) UnquarantineServer(serverName string) error {
	m.logger.Info("UnquarantineServer called", zap.String("server", serverName))

	// Update the server quarantine status
	if err := m.server.UnquarantineServer(serverName); err != nil {
		return fmt.Errorf("failed to unquarantine server: %w", err)
	}

	// Force state refresh immediately after the change
	if err := m.RefreshState(); err != nil {
		m.logger.Error("Failed to refresh state after unquarantine change", zap.Error(err))
		// Don't return error here as the unquarantine operation itself succeeded
	}

	m.logger.Info("Server unquarantine completed successfully", zap.String("server", serverName))

	return nil
}

// EnableServer enables/disables a server and ensures all state is synchronized
func (m *ServerStateManager) EnableServer(serverName string, enabled bool) error {
	action := actionDisable
	if enabled {
		action = actionEnable
	}

	m.logger.Info("EnableServer called",
		zap.String("server", serverName),
		zap.String("action", action))

	// Update the server enable status
	if err := m.server.EnableServer(serverName, enabled); err != nil {
		return fmt.Errorf("failed to %s server: %w", action, err)
	}

	// Force state refresh immediately after the change
	if err := m.RefreshState(); err != nil {
		m.logger.Error("Failed to refresh state after enable change", zap.Error(err))
		// Don't return error here as the enable operation itself succeeded
	}

	m.logger.Info("Server enable status updated successfully",
		zap.String("server", serverName),
		zap.String("action", action))

	return nil
}

// MenuManager manages tray menu state and prevents duplications
type MenuManager struct {
	logger *zap.SugaredLogger
	mu     sync.RWMutex

	// Menu references
	upstreamServersMenu *systray.MenuItem
	quarantineMenu      *systray.MenuItem

	// Menu tracking to prevent duplicates
	serverMenuItems       map[string]*systray.MenuItem // server name -> menu item
	quarantineMenuItems   map[string]*systray.MenuItem // server name -> menu item
	serverActionItems     map[string]*systray.MenuItem // server name -> enable/disable action menu item
	serverQuarantineItems map[string]*systray.MenuItem // server name -> quarantine action menu item
	serverOAuthItems      map[string]*systray.MenuItem // server name -> OAuth login menu item
	quarantineInfoEmpty   *systray.MenuItem            // "No servers" info item
	quarantineInfoHelp    *systray.MenuItem            // "Click to unquarantine" help item

	// Latest server data snapshots
	latestServers     []map[string]interface{}
	latestQuarantined []map[string]interface{}

	// Event handler callback
	onServerAction func(serverName string, action string) // callback for server actions
}

// NewMenuManager creates a new menu manager
func NewMenuManager(upstreamMenu, quarantineMenu *systray.MenuItem, logger *zap.SugaredLogger) *MenuManager {
	return &MenuManager{
		logger:                logger,
		upstreamServersMenu:   upstreamMenu,
		quarantineMenu:        quarantineMenu,
		serverMenuItems:       make(map[string]*systray.MenuItem),
		quarantineMenuItems:   make(map[string]*systray.MenuItem),
		serverActionItems:     make(map[string]*systray.MenuItem),
		serverQuarantineItems: make(map[string]*systray.MenuItem),
		serverOAuthItems:      make(map[string]*systray.MenuItem),
	}
}

// SetActionCallback sets the callback function for server actions
func (m *MenuManager) SetActionCallback(callback func(serverName string, action string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onServerAction = callback
}

// UpdateUpstreamServersMenu updates the upstream servers menu without duplicates
func (m *MenuManager) UpdateUpstreamServersMenu(servers []map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latestServers = cloneServerData(servers)

	// Stability check: Don't clear existing menus if we get empty servers and we already have servers
	// This prevents UI flickering when database is temporarily unavailable
	if len(servers) == 0 && len(m.serverMenuItems) > 0 {
		m.logger.Debug("Received empty server list but existing menu items present, preserving UI state")
		return
	}

	// --- Update Title ---
	totalServers := len(servers)
	connectedServers := 0
	for _, server := range servers {
		if connected, ok := server["connected"].(bool); ok && connected {
			connectedServers++
		}
	}
	menuTitle := fmt.Sprintf("Upstream Servers (%d/%d)", connectedServers, totalServers)
	if m.upstreamServersMenu != nil {
		m.upstreamServersMenu.SetTitle(menuTitle)
	}

	// --- Create a map for efficient lookup of current servers ---
	currentServerMap := make(map[string]map[string]interface{})
	var currentServerNames []string
	for _, server := range servers {
		if name, ok := server["name"].(string); ok {
			currentServerMap[name] = server
			currentServerNames = append(currentServerNames, name)
		}
	}
	sort.Strings(currentServerNames)

	// --- Check if we need to rebuild the menu (new servers added) ---
	var newServerNames []string
	for serverName := range currentServerMap {
		if _, exists := m.serverMenuItems[serverName]; !exists {
			newServerNames = append(newServerNames, serverName)
		}
	}

	if len(newServerNames) > 0 {
		// New servers detected - rebuild entire menu in sorted order
		m.logger.Info("Rebuilding upstream servers menu in sorted order", zap.Int("new_servers", len(newServerNames)))

		// Hide all existing menu items
		for serverName, menuItem := range m.serverMenuItems {
			menuItem.Hide()
			// Also hide sub-menu items
			if actionItem, ok := m.serverActionItems[serverName]; ok {
				actionItem.Hide()
			}
			if quarantineActionItem, ok := m.serverQuarantineItems[serverName]; ok {
				quarantineActionItem.Hide()
			}
			if oauthItem, ok := m.serverOAuthItems[serverName]; ok {
				oauthItem.Hide()
			}
		}

		// Clear the tracking maps
		m.serverMenuItems = make(map[string]*systray.MenuItem)
		m.serverActionItems = make(map[string]*systray.MenuItem)
		m.serverQuarantineItems = make(map[string]*systray.MenuItem)
		m.serverOAuthItems = make(map[string]*systray.MenuItem)

		// Create all servers in sorted order
		for _, serverName := range currentServerNames {
			serverData := currentServerMap[serverName]
			m.logger.Info("Creating menu item for server", zap.String("server", serverName))
			status, tooltip, iconData := m.getServerStatusDisplay(serverData)
			serverMenuItem := m.upstreamServersMenu.AddSubMenuItem(status, tooltip)

			// Set icon if available (Windows)
			if iconData != nil {
				serverMenuItem.SetIcon(iconData)
			}

			m.serverMenuItems[serverName] = serverMenuItem

			// Create its action submenus
			m.createServerActionSubmenus(serverMenuItem, serverData)
		}
	} else {
		// No new servers - just update existing items
		for _, serverName := range currentServerNames {
			menuItem, exists := m.serverMenuItems[serverName]
			if !exists {
				continue
			}

			serverData := currentServerMap[serverName]
			// Server exists, update its display and ensure it's visible
			status, tooltip, iconData := m.getServerStatusDisplay(serverData)
			menuItem.SetTitle(status)
			menuItem.SetTooltip(tooltip)

			// Update icon if available (Windows)
			if iconData != nil {
				menuItem.SetIcon(iconData)
			}

			m.updateServerActionMenus(serverName, serverData) // Update sub-menu items too
			menuItem.Show()
		}

		// Hide servers that are no longer in the config
		for serverName, menuItem := range m.serverMenuItems {
			if _, exists := currentServerMap[serverName]; !exists {
				m.logger.Debug("Hiding menu item for removed server", zap.String("server", serverName))
				menuItem.Hide()
				// Also hide its sub-menu items if they exist
				if actionItem, ok := m.serverActionItems[serverName]; ok {
					actionItem.Hide()
				}
				if quarantineActionItem, ok := m.serverQuarantineItems[serverName]; ok {
					quarantineActionItem.Hide()
				}
			}
		}
	}
}

// UpdateQuarantineMenu updates the quarantine menu using Hide/Show to prevent duplicates
func (m *MenuManager) UpdateQuarantineMenu(quarantinedServers []map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latestQuarantined = cloneServerData(quarantinedServers)

	// Stability check: Don't clear existing quarantine menus if we get empty quarantine list
	// but we already have quarantine items. This prevents UI flickering.
	if len(quarantinedServers) == 0 && len(m.quarantineMenuItems) > 0 {
		m.logger.Debug("Received empty quarantine list but existing menu items present, preserving UI state")
		// Still update the title to show (0) if no quarantined servers
		if m.quarantineMenu != nil {
			m.quarantineMenu.SetTitle("Security Quarantine (0)")
		}
		return
	}

	// --- Update Title ---
	quarantineCount := len(quarantinedServers)
	menuTitle := fmt.Sprintf("Security Quarantine (%d)", quarantineCount)
	if m.quarantineMenu != nil {
		m.quarantineMenu.SetTitle(menuTitle)
	} else {
		m.logger.Error("Quarantine menu is nil, cannot update title")
		return
	}

	// --- Create Info Items if Needed ---
	if m.quarantineInfoEmpty == nil || m.quarantineInfoHelp == nil {
		m.quarantineInfoEmpty = m.quarantineMenu.AddSubMenuItem("(No servers quarantined)", "No servers are currently quarantined")
		m.quarantineInfoHelp = m.quarantineMenu.AddSubMenuItem("Click to unquarantine", "Click on a quarantined server to remove it from quarantine")
		m.quarantineInfoEmpty.Disable()
		m.quarantineInfoHelp.Disable()
		// Add empty separator for visual separation
		m.quarantineMenu.AddSeparator()
	}

	// --- Update Info Item Visibility ---
	if m.quarantineInfoEmpty != nil {
		if quarantineCount == 0 {
			m.quarantineInfoEmpty.Show()
			m.quarantineInfoHelp.Hide()
		} else {
			m.quarantineInfoEmpty.Hide()
			m.quarantineInfoHelp.Show()
		}
	}

	// --- Create a map for efficient lookup of current quarantined servers ---
	currentQuarantineMap := make(map[string]bool)
	var currentQuarantineNames []string
	for _, server := range quarantinedServers {
		if name, ok := server["name"].(string); ok {
			currentQuarantineMap[name] = true
			currentQuarantineNames = append(currentQuarantineNames, name)
		} else {
			m.logger.Warn("Quarantined server missing name field", zap.Any("server", server))
		}
	}
	sort.Strings(currentQuarantineNames)

	// --- Check if we need to rebuild the quarantine menu (new servers added) ---
	var newQuarantineNames []string
	for serverName := range currentQuarantineMap {
		if _, exists := m.quarantineMenuItems[serverName]; !exists {
			newQuarantineNames = append(newQuarantineNames, serverName)
		}
	}

	if len(newQuarantineNames) > 0 {
		// New quarantined servers detected - rebuild entire menu in sorted order
		m.logger.Info("Rebuilding quarantine menu in sorted order", zap.Int("new_quarantined", len(newQuarantineNames)))

		// Hide all existing quarantine menu items
		for _, menuItem := range m.quarantineMenuItems {
			menuItem.Hide()
		}

		// Clear the tracking map
		m.quarantineMenuItems = make(map[string]*systray.MenuItem)

		// Create all quarantined servers in sorted order
		for _, serverName := range currentQuarantineNames {
			// This is a quarantined server, create its menu item
			if m.quarantineMenu == nil {
				m.logger.Error("Cannot create quarantine menu item - quarantineMenu is nil!", zap.String("server", serverName))
				continue
			}

			// On Windows, use icon instead of emoji
			var displayText string
			if runtime.GOOS == osWindows {
				displayText = serverName
			} else {
				displayText = fmt.Sprintf("🔒 %s", serverName)
			}

			quarantineMenuItem := m.quarantineMenu.AddSubMenuItem(
				displayText,
				fmt.Sprintf("Click to unquarantine %s", serverName),
			)

			if quarantineMenuItem == nil {
				m.logger.Error("Failed to create quarantine menu item", zap.String("server", serverName))
				continue
			}

			// Set icon for Windows
			if runtime.GOOS == osWindows {
				iconData := loadIcon(iconLocked)
				if iconData != nil {
					quarantineMenuItem.SetIcon(iconData)
				}
			}

			m.quarantineMenuItems[serverName] = quarantineMenuItem

			// Set up the one-time click handler
			go func(name string, item *systray.MenuItem) {
				for range item.ClickedCh {
					if m.onServerAction != nil {
						// Run in a new goroutine to avoid blocking the event channel
						go m.onServerAction(name, "unquarantine")
					}
				}
			}(serverName, quarantineMenuItem)
		}
	} else {
		// No new quarantined servers - just update existing items
		for _, serverName := range currentQuarantineNames {
			if menuItem, exists := m.quarantineMenuItems[serverName]; exists {
				// Server is still quarantined, ensure it's visible
				menuItem.Show()
			}
		}

		// Hide servers that are no longer quarantined
		for serverName, menuItem := range m.quarantineMenuItems {
			if _, exists := currentQuarantineMap[serverName]; !exists {
				// Server is no longer quarantined, hide it
				menuItem.Hide()
			}
		}
	}
}

// GetServerMenuItem returns the menu item for a server (for action handling)
func (m *MenuManager) GetServerMenuItem(serverName string) *systray.MenuItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.serverMenuItems[serverName]
}

// GetQuarantineMenuItem returns the quarantine menu item for a server (for action handling)
func (m *MenuManager) GetQuarantineMenuItem(serverName string) *systray.MenuItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.quarantineMenuItems[serverName]
}

// ForceRefresh clears all menu tracking to force recreation (handles systray limitations)
func (m *MenuManager) ForceRefresh() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Warn("ForceRefresh is called, which is deprecated. Check for misuse.")
	// This function is now a no-op to prevent the duplication issue.
	// The new Hide/Show logic should be used instead.
}

// getServerStatusDisplay returns display text, tooltip, and icon data for a server
func (m *MenuManager) getServerStatusDisplay(server map[string]interface{}) (displayText, tooltip string, iconData []byte) {
	serverName, _ := server["name"].(string)
	enabled, _ := server["enabled"].(bool)
	connected, _ := server["connected"].(bool)
	quarantined, _ := server["quarantined"].(bool)
	toolCount, _ := server["tool_count"].(int)
	lastError, _ := server["last_error"].(string)
	statusValue, _ := server["status"].(string)
	shouldRetry, _ := server["should_retry"].(bool)

	var retryCount int
	switch rc := server["retry_count"].(type) {
	case int:
		retryCount = rc
	case float64:
		retryCount = int(rc)
	}
	lastRetryTime, _ := server["last_retry_time"].(string)

	var statusIcon string
	var statusText string
	var iconPath string

	// Check for OAuth-related errors in last_error (matching web UI logic)
	needsOAuth := lastError != "" && (
		strings.Contains(lastError, "OAuth authentication required") ||
		strings.Contains(lastError, "deferred for tray UI") ||
		strings.Contains(lastError, "authorization") ||
		strings.Contains(lastError, "401") ||
		strings.Contains(lastError, "invalid_token") ||
		strings.Contains(lastError, "Missing or invalid access token"))

	if quarantined {
		statusIcon = "🔒"
		statusText = "quarantined"
		iconPath = iconLocked
	} else if !enabled {
		statusIcon = "⏸️"
		statusText = "disabled"
		iconPath = iconPaused
	} else if needsOAuth {
		// OAuth required - show as info/warning state, not error
		statusIcon = "🔐"
		statusText = "needs auth"
		iconPath = iconDisconnected // Use disconnected icon (could add specific auth icon later)
	} else if st := strings.ToLower(statusValue); st != "" {
		switch st {
		case "ready", "connected":
			statusIcon = "🟢"
			statusText = fmt.Sprintf("connected (%d tools)", toolCount)
			iconPath = iconConnected
		case "connecting":
			statusIcon = "🟠"
			statusText = "connecting"
			iconPath = iconDisconnected
		case "pending auth":
			statusIcon = "⏳"
			statusText = "pending auth"
			iconPath = iconDisconnected // Use disconnected icon for now since we don't have a specific auth icon
		case "error", "disconnected":
			statusIcon = "🔴"
			statusText = "connection error"
			iconPath = iconDisconnected
		case "disabled":
			statusIcon = "⏸️"
			statusText = "disabled"
			iconPath = iconPaused
		default:
			statusIcon = "🔴"
			statusText = st
			iconPath = iconDisconnected
		}
	} else if connected {
		statusIcon = "🟢"
		statusText = fmt.Sprintf("connected (%d tools)", toolCount)
		iconPath = iconConnected
	} else {
		statusIcon = "🔴"
		statusText = "disconnected"
		iconPath = iconDisconnected
	}

	// On Windows, use icons instead of emoji for better visual appearance
	if runtime.GOOS == osWindows {
		displayText = serverName
		iconData = loadIcon(iconPath)
	} else {
		// On other platforms, keep using emoji
		displayText = fmt.Sprintf("%s %s", statusIcon, serverName)
	}

	var tooltipLines []string
	tooltipLines = append(tooltipLines, fmt.Sprintf("%s - %s", serverName, statusText))

	if statusValue != "" && !strings.EqualFold(statusValue, statusText) {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Status: %s", statusValue))
	}

	if lastError != "" {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Last error: %s", lastError))
	}

	if shouldRetry {
		if retryCount > 0 {
			tooltipLines = append(tooltipLines, fmt.Sprintf("Retry scheduled (attempts: %d)", retryCount))
		} else {
			tooltipLines = append(tooltipLines, "Retry scheduled")
		}
	} else if retryCount > 0 {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Retries attempted: %d", retryCount))
	}

	if lastRetryTime != "" {
		tooltipLines = append(tooltipLines, fmt.Sprintf("Last retry: %s", lastRetryTime))
	}

	tooltip = strings.Join(tooltipLines, "\n")

	return
}

// serverSupportsOAuth determines if a server supports OAuth authentication
func (m *MenuManager) serverSupportsOAuth(server map[string]interface{}) bool {
	// Get server URL
	serverURL, ok := server["url"].(string)
	if !ok || serverURL == "" {
		return false
	}

	// Check if it's an HTTP/HTTPS server (OAuth is typically used with HTTP-based APIs)
	urlLower := strings.ToLower(serverURL)
	if !strings.HasPrefix(urlLower, "http://") && !strings.HasPrefix(urlLower, "https://") {
		return false
	}

	// Check for OAuth-related URLs patterns
	if strings.Contains(urlLower, "oauth") || strings.Contains(urlLower, "auth") {
		return true
	}

	// For common MCP servers that we know support OAuth
	oauthDomains := []string{
		"sentry.dev",
		"github.com",
		"gitlab.com",
		"google.com",
		"googleapis.com",
		"microsoft.com",
		"oauth.com",
	}

	for _, domain := range oauthDomains {
		if strings.Contains(urlLower, domain) {
			return true
		}
	}

	// For any HTTP/HTTPS server, show OAuth option since it might support it
	// Users can try it and it will fail gracefully if not supported
	return true
}

// createServerActionSubmenus creates action submenus for a server (enable/disable, quarantine, OAuth login)
func (m *MenuManager) createServerActionSubmenus(serverMenuItem *systray.MenuItem, server map[string]interface{}) {
	serverName, _ := server["name"].(string)
	if serverName == "" {
		return
	}

	enabled, _ := server["enabled"].(bool)
	quarantined, _ := server["quarantined"].(bool)
	authenticated, _ := server["authenticated"].(bool)
	connected, _ := server["connected"].(bool)

	// Check if server needs OAuth authentication
	needsOAuth := m.serverSupportsOAuth(server) && !quarantined && !authenticated && enabled && !connected

	// OAuth Login action - show FIRST if server needs authentication
	if needsOAuth {
		oauthItem := serverMenuItem.AddSubMenuItem("🔐 Login (OAuth)", fmt.Sprintf("Authenticate with %s using OAuth", serverName))
		m.serverOAuthItems[serverName] = oauthItem

		// Set up OAuth login click handler
		go func(name string, item *systray.MenuItem) {
			for range item.ClickedCh {
				if m.onServerAction != nil {
					// Run in new goroutines to avoid blocking the event channel
					go m.onServerAction(name, "oauth_login")
				}
			}
		}(serverName, oauthItem)
	}

	// Enable/Disable action
	var enableText string
	if enabled {
		enableText = textDisable
	} else {
		enableText = textEnable
	}
	enableItem := serverMenuItem.AddSubMenuItem(enableText, fmt.Sprintf("%s server %s", enableText, serverName))
	m.serverActionItems[serverName] = enableItem

	// OAuth Login action (for authenticated servers or when not the primary action)
	// Show as secondary option if OAuth is supported but server doesn't currently need auth
	if m.serverSupportsOAuth(server) && !quarantined && !needsOAuth {
		oauthItem := serverMenuItem.AddSubMenuItem("🔐 OAuth Login", fmt.Sprintf("Authenticate with %s using OAuth", serverName))
		m.serverOAuthItems[serverName] = oauthItem

		// Set up OAuth login click handler
		go func(name string, item *systray.MenuItem) {
			for range item.ClickedCh {
				if m.onServerAction != nil {
					// Run in new goroutines to avoid blocking the event channel
					go m.onServerAction(name, "oauth_login")
				}
			}
		}(serverName, oauthItem)
	}

	// Quarantine action (only if not already quarantined)
	if !quarantined {
		quarantineItem := serverMenuItem.AddSubMenuItem("Move to Quarantine", fmt.Sprintf("Quarantine server %s for security review", serverName))
		m.serverQuarantineItems[serverName] = quarantineItem

		// Set up quarantine click handler
		go func(name string, item *systray.MenuItem) {
			for range item.ClickedCh {
				if m.onServerAction != nil {
					// Run in new goroutines to avoid blocking the event channel
					go m.onServerAction(name, "quarantine")
				}
			}
		}(serverName, quarantineItem)
	}

	// Set up enable/disable click handler
	go func(name string, item *systray.MenuItem) {
		for range item.ClickedCh {
			if m.onServerAction != nil {
				// The best approach is to have the sync manager handle the toggle.
				// We send a generic 'toggle_enable' action and let the handler determine the state.
				go m.onServerAction(name, "toggle_enable")
			}
		}
	}(serverName, enableItem)
}

// updateServerActionMenus updates the action submenu items for an existing server
func (m *MenuManager) updateServerActionMenus(serverName string, server map[string]interface{}) {
	enabled, _ := server["enabled"].(bool)

	// Update enable/disable action menu text
	if actionItem, exists := m.serverActionItems[serverName]; exists {
		var enableText string
		if enabled {
			enableText = textDisable
		} else {
			enableText = textEnable
		}
		actionItem.SetTitle(enableText)
		actionItem.SetTooltip(fmt.Sprintf("%s server %s", enableText, serverName))

		m.logger.Debug("Updated action menu for server",
			zap.String("server", serverName),
			zap.String("action", enableText))
	}
}

// SynchronizationManager coordinates between state manager and menu manager
type SynchronizationManager struct {
	stateManager        *ServerStateManager
	server              ServerInterface // Added to support API mode
	menuManager         *MenuManager
	logger              *zap.SugaredLogger
	onSync              func()
	lastServerCount     int
	lastQuarantineCount int

	// Background sync control
	ctx       context.Context
	cancel    context.CancelFunc
	syncTimer *time.Timer

	// Connection state tracking
	connMu    sync.RWMutex
	connected bool
}

// NewSynchronizationManager creates a new synchronization manager
func NewSynchronizationManager(stateManager *ServerStateManager, server ServerInterface, menuManager *MenuManager, logger *zap.SugaredLogger) *SynchronizationManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &SynchronizationManager{
		stateManager: stateManager,
		server:       server,
		menuManager:  menuManager,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
}

// SetOnSync registers a callback invoked after successful menu synchronization.
func (m *SynchronizationManager) SetOnSync(cb func()) {
	m.onSync = cb
}

// SetConnected updates the connection state and controls whether syncing is allowed
func (m *SynchronizationManager) SetConnected(connected bool) {
	m.connMu.Lock()
	defer m.connMu.Unlock()

	wasConnected := m.connected
	m.connected = connected

	if connected && !wasConnected {
		m.logger.Info("Core connected - enabling menu synchronization")
		// Trigger immediate sync when transitioning to connected
		go func() {
			if err := m.SyncNow(); err != nil {
				m.logger.Error("Initial sync after connection failed", zap.Error(err))
			}
		}()
	} else if !connected && wasConnected {
		m.logger.Info("Core disconnected - pausing menu synchronization")
	}
}

// isConnected checks if core connection is established
func (m *SynchronizationManager) isConnected() bool {
	m.connMu.RLock()
	defer m.connMu.RUnlock()
	return m.connected
}

// Start begins background synchronization
func (m *SynchronizationManager) Start() {
	go m.syncLoop()
}

// Stop stops background synchronization
func (m *SynchronizationManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	if m.syncTimer != nil {
		m.syncTimer.Stop()
	}
}

// SyncNow performs immediate synchronization
func (m *SynchronizationManager) SyncNow() error {
	m.logger.Debug("Running immediate synchronization")
	return m.performSync()
}

// SyncDelayed schedules a delayed synchronization to batch updates
func (m *SynchronizationManager) SyncDelayed() {
	if m.syncTimer != nil {
		m.syncTimer.Stop()
	}
	m.syncTimer = time.AfterFunc(1*time.Second, func() {
		if err := m.performSync(); err != nil {
			m.logger.Error("Delayed sync failed", zap.Error(err))
		}
	})
}

// syncLoop runs the background synchronization loop
func (m *SynchronizationManager) syncLoop() {
	ticker := time.NewTicker(3 * time.Second) // Sync every 3 seconds for more responsive updates
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := m.performSync(); err != nil {
				m.logger.Error("Background sync failed", zap.Error(err))
			}
		case <-m.ctx.Done():
			return
		}
	}
}

// performSync performs the actual synchronization
func (m *SynchronizationManager) performSync() error {
	// Only perform sync if core is connected
	if !m.isConnected() {
		m.logger.Debug("Core not connected, skipping synchronization")
		return nil
	}

	// Use server interface to get all servers (works in both local and API mode)
	allServers, err := m.server.GetAllServers()
	if err != nil {
		// Check if it's a database closed error and handle gracefully
		if strings.Contains(err.Error(), "database not available") {
			m.logger.Debug("Database not available, skipping servers menu update to preserve UI state")
			// Don't update servers menu to preserve current state
		} else {
			m.logger.Error("Failed to get all servers", zap.Error(err))
			return fmt.Errorf("failed to get all servers: %w", err)
		}
	} else {
		if len(allServers) != m.lastServerCount {
			if len(allServers) == 0 {
				m.logger.Warn("Synchronization returned zero upstream servers - waiting for core updates")
			} else {
				m.logger.Info("Synchronization refreshed upstream servers",
					zap.Int("count", len(allServers)))
			}
			m.lastServerCount = len(allServers)
		}
		// Only update menu if we have valid data
		m.menuManager.UpdateUpstreamServersMenu(allServers)
	}

	// Use server interface to get quarantined servers (works in both local and API mode)
	quarantinedServers, err := m.server.GetQuarantinedServers()
	if err != nil {
		// Check if it's a database closed error and handle gracefully
		if strings.Contains(err.Error(), "database not available") {
			m.logger.Debug("Database not available, skipping quarantine menu update to preserve UI state")
			// Don't update quarantine menu to preserve current state
		} else {
			m.logger.Error("Failed to get quarantined servers", zap.Error(err))
			return fmt.Errorf("failed to get quarantined servers: %w", err)
		}
	} else {
		if len(quarantinedServers) != m.lastQuarantineCount {
			m.logger.Info("Synchronization refreshed quarantine list",
				zap.Int("count", len(quarantinedServers)))
			m.lastQuarantineCount = len(quarantinedServers)
		}
		// Only update menu if we have valid data
		m.menuManager.UpdateQuarantineMenu(quarantinedServers)
	}

	if m.onSync != nil {
		m.onSync()
	}

	return nil
}

// HandleServerQuarantine handles server quarantine with full synchronization
func (m *SynchronizationManager) HandleServerQuarantine(serverName string, quarantined bool) error {
	m.logger.Info("Handling server quarantine",
		zap.String("server", serverName),
		zap.Bool("quarantined", quarantined))

	// Update state
	if err := m.stateManager.QuarantineServer(serverName, quarantined); err != nil {
		return err
	}

	// Force immediate sync
	return m.SyncNow()
}

// HandleServerUnquarantine handles server unquarantine with full synchronization
func (m *SynchronizationManager) HandleServerUnquarantine(serverName string) error {
	m.logger.Info("Handling server unquarantine", zap.String("server", serverName))

	// Update state
	if err := m.stateManager.UnquarantineServer(serverName); err != nil {
		return err
	}

	// Force immediate sync
	return m.SyncNow()
}

// HandleServerEnable handles server enable/disable with full synchronization
func (m *SynchronizationManager) HandleServerEnable(serverName string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	m.logger.Info("Handling server enable/disable",
		zap.String("server", serverName),
		zap.String("action", action))

	// Update state
	if err := m.stateManager.EnableServer(serverName, enabled); err != nil {
		return err
	}

	// Force immediate sync
	return m.SyncNow()
}

// Note: stringSlicesEqual function is defined in tray.go

func cloneServerData(list []map[string]interface{}) []map[string]interface{} {
	if len(list) == 0 {
		return nil
	}

	clone := make([]map[string]interface{}, 0, len(list))
	for _, item := range list {
		if item == nil {
			clone = append(clone, nil)
			continue
		}
		copied := make(map[string]interface{}, len(item))
		for k, v := range item {
			copied[k] = v
		}
		clone = append(clone, copied)
	}
	return clone
}

// LatestServersSnapshot returns a copy of the latest upstream server data used for menu generation.
func (m *MenuManager) LatestServersSnapshot() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneServerData(m.latestServers)
}

// LatestQuarantineSnapshot returns a copy of the latest quarantine data used for menu generation.
func (m *MenuManager) LatestQuarantineSnapshot() []map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneServerData(m.latestQuarantined)
}

