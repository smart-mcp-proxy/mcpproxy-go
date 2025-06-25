//go:build !nogui && !headless && !linux

package tray

import (
	"context"
	"fmt"
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

// ServerStateManager manages server state synchronization between storage, config, and menu
type ServerStateManager struct {
	server ServerInterface
	logger *zap.SugaredLogger
	mu     sync.RWMutex

	// Current state tracking
	allServers         []map[string]interface{}
	quarantinedServers []map[string]interface{}
	lastUpdate         time.Time
}

// NewServerStateManager creates a new server state manager
func NewServerStateManager(server ServerInterface, logger *zap.SugaredLogger) *ServerStateManager {
	return &ServerStateManager{
		server: server,
		logger: logger,
	}
}

// RefreshState loads the current state from the server
func (m *ServerStateManager) RefreshState() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get all servers
	allServers, err := m.server.GetAllServers()
	if err != nil {
		return fmt.Errorf("failed to get all servers: %w", err)
	}

	// Get quarantined servers
	quarantinedServers, err := m.server.GetQuarantinedServers()
	if err != nil {
		return fmt.Errorf("failed to get quarantined servers: %w", err)
	}

	m.allServers = allServers
	m.quarantinedServers = quarantinedServers
	m.lastUpdate = time.Now()

	m.logger.Debug("Server state refreshed",
		zap.Int("all_servers", len(allServers)),
		zap.Int("quarantined_servers", len(quarantinedServers)))

	return nil
}

// GetAllServers returns cached or fresh server list
func (m *ServerStateManager) GetAllServers() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return cached data if available and recent
	if time.Since(m.lastUpdate) < 2*time.Second && len(m.allServers) > 0 {
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
			// No cached data available, return empty slice
			m.logger.Debug("Database not available and no cached data, returning empty server list")
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}

	// Cache the fresh data
	m.allServers = servers
	m.lastUpdate = time.Now()
	return servers, nil
}

// GetQuarantinedServers returns cached or fresh quarantined server list
func (m *ServerStateManager) GetQuarantinedServers() ([]map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return cached data if available and recent
	if time.Since(m.lastUpdate) < 2*time.Second && len(m.quarantinedServers) >= 0 {
		return m.quarantinedServers, nil
	}

	// Get fresh data but handle database errors gracefully
	servers, err := m.server.GetQuarantinedServers()
	if err != nil {
		// If database is closed, return cached data if available
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			if len(m.quarantinedServers) >= 0 {
				m.logger.Debug("Database not available, returning cached quarantine data")
				return m.quarantinedServers, nil
			}
			// No cached data available, return empty slice
			m.logger.Debug("Database not available and no cached data, returning empty quarantine list")
			return []map[string]interface{}{}, nil
		}
		return nil, err
	}

	// Cache the fresh data
	m.quarantinedServers = servers
	m.lastUpdate = time.Now()
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
	quarantineInfoEmpty   *systray.MenuItem            // "No servers" info item
	quarantineInfoHelp    *systray.MenuItem            // "Click to unquarantine" help item

	// State tracking to detect changes
	lastServerNames     []string
	lastQuarantineNames []string
	menusInitialized    bool

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
	for _, server := range servers {
		if name, ok := server["name"].(string); ok {
			currentServerMap[name] = server
		}
	}

	// --- Hide or Update Existing Menu Items ---
	for serverName, menuItem := range m.serverMenuItems {
		if serverData, exists := currentServerMap[serverName]; exists {
			// Server exists, update its display and ensure it's visible
			status, tooltip := m.getServerStatusDisplay(serverData)
			menuItem.SetTitle(status)
			menuItem.SetTooltip(tooltip)
			m.updateServerActionMenus(serverName, serverData) // Update sub-menu items too
			menuItem.Show()
		} else {
			// Server was removed from config, hide it
			m.logger.Info("Hiding menu item for removed server", zap.String("server", serverName))
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

	// --- Create Menu Items for New Servers ---
	for serverName, serverData := range currentServerMap {
		if _, exists := m.serverMenuItems[serverName]; exists {
			continue
		}
		// This is a new server, create its menu item
		m.logger.Info("Creating menu item for new server", zap.String("server", serverName))
		status, tooltip := m.getServerStatusDisplay(serverData)
		serverMenuItem := m.upstreamServersMenu.AddSubMenuItem(status, tooltip)
		m.serverMenuItems[serverName] = serverMenuItem

		// Create its action submenus
		m.createServerActionSubmenus(serverMenuItem, serverData)
	}
}

// UpdateQuarantineMenu updates the quarantine menu using Hide/Show to prevent duplicates
func (m *MenuManager) UpdateQuarantineMenu(quarantinedServers []map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// --- Update Title ---
	quarantineCount := len(quarantinedServers)
	menuTitle := fmt.Sprintf("Security Quarantine (%d)", quarantineCount)
	if m.quarantineMenu != nil {
		m.quarantineMenu.SetTitle(menuTitle)
	}

	// --- Initialize Info Items on First Run ---
	if m.quarantineInfoEmpty == nil && m.quarantineMenu != nil {
		m.quarantineInfoEmpty = m.quarantineMenu.AddSubMenuItem("No servers quarantined", "All servers are approved")
		m.quarantineInfoEmpty.Disable()

		m.quarantineInfoHelp = m.quarantineMenu.AddSubMenuItem("ℹ️ Click server to unquarantine", "Click on a quarantined server to remove it from quarantine")
		m.quarantineInfoHelp.Disable()

		// Add a separator that is always visible
		m.quarantineMenu.AddSubMenuItem("", "")
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
	for _, server := range quarantinedServers {
		if name, ok := server["name"].(string); ok {
			currentQuarantineMap[name] = true
		}
	}

	// --- Hide or Show Existing Menu Items ---
	for serverName, menuItem := range m.quarantineMenuItems {
		if _, exists := currentQuarantineMap[serverName]; exists {
			// Server is still quarantined, ensure it's visible
			menuItem.Show()
		} else {
			// Server is no longer quarantined, hide it
			m.logger.Info("Hiding menu item for unquarantined server", zap.String("server", serverName))
			menuItem.Hide()
		}
	}

	// --- Create Menu Items for Newly Quarantined Servers ---
	for serverName := range currentQuarantineMap {
		if _, exists := m.quarantineMenuItems[serverName]; !exists {
			// This is a newly quarantined server, create its menu item
			m.logger.Info("Creating quarantine menu item for server", zap.String("server", serverName))
			quarantineMenuItem := m.quarantineMenu.AddSubMenuItem(
				fmt.Sprintf("🔒 %s", serverName),
				fmt.Sprintf("Click to unquarantine %s", serverName),
			)
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

// getServerStatusDisplay returns display text and tooltip for a server
func (m *MenuManager) getServerStatusDisplay(server map[string]interface{}) (displayText, tooltip string) {
	serverName, _ := server["name"].(string)
	enabled, _ := server["enabled"].(bool)
	connected, _ := server["connected"].(bool)
	quarantined, _ := server["quarantined"].(bool)
	toolCount, _ := server["tool_count"].(int)

	var statusIcon string
	var statusText string

	if quarantined {
		statusIcon = "🔒"
		statusText = "quarantined"
	} else if !enabled {
		statusIcon = "⏸️"
		statusText = "disabled"
	} else if connected {
		statusIcon = "🟢"
		statusText = fmt.Sprintf("connected (%d tools)", toolCount)
	} else {
		statusIcon = "🔴"
		statusText = "disconnected"
	}

	displayText = fmt.Sprintf("%s %s", statusIcon, serverName)
	tooltip = fmt.Sprintf("%s - %s", serverName, statusText)

	return
}

// createServerActionSubmenus creates action submenus for a server (enable/disable, quarantine)
func (m *MenuManager) createServerActionSubmenus(serverMenuItem *systray.MenuItem, server map[string]interface{}) {
	serverName, _ := server["name"].(string)
	if serverName == "" {
		return
	}

	enabled, _ := server["enabled"].(bool)
	quarantined, _ := server["quarantined"].(bool)

	// Enable/Disable action
	var enableText string
	if enabled {
		enableText = textDisable
	} else {
		enableText = textEnable
	}
	enableItem := serverMenuItem.AddSubMenuItem(enableText, fmt.Sprintf("%s server %s", enableText, serverName))
	m.serverActionItems[serverName] = enableItem

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
	stateManager *ServerStateManager
	menuManager  *MenuManager
	logger       *zap.SugaredLogger

	// Background sync control
	ctx       context.Context
	cancel    context.CancelFunc
	syncTimer *time.Timer
}

// NewSynchronizationManager creates a new synchronization manager
func NewSynchronizationManager(stateManager *ServerStateManager, menuManager *MenuManager, logger *zap.SugaredLogger) *SynchronizationManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &SynchronizationManager{
		stateManager: stateManager,
		menuManager:  menuManager,
		logger:       logger,
		ctx:          ctx,
		cancel:       cancel,
	}
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

// SyncNow forces an immediate synchronization
func (m *SynchronizationManager) SyncNow() error {
	return m.performSync()
}

// SyncDelayed triggers a delayed synchronization (debounces rapid changes)
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
	m.logger.Debug("Performing synchronization")

	// Check if the state manager's server is available and running
	// If not, skip the sync to avoid database errors
	if m.stateManager.server != nil && !m.stateManager.server.IsRunning() {
		m.logger.Debug("Server is stopped, skipping synchronization")
		return nil
	}

	// Get current state with error handling for database issues
	allServers, err := m.stateManager.GetAllServers()
	if err != nil {
		// Check if it's a database closed error and handle gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			m.logger.Debug("Database not available, skipping synchronization")
			return nil
		}
		return fmt.Errorf("failed to get all servers: %w", err)
	}

	quarantinedServers, err := m.stateManager.GetQuarantinedServers()
	if err != nil {
		// Check if it's a database closed error and handle gracefully
		if strings.Contains(err.Error(), "database not open") || strings.Contains(err.Error(), "closed") {
			m.logger.Debug("Database not available for quarantine data, skipping synchronization")
			return nil
		}
		return fmt.Errorf("failed to get quarantined servers: %w", err)
	}

	// Update menus
	m.menuManager.UpdateUpstreamServersMenu(allServers)
	m.menuManager.UpdateQuarantineMenu(quarantinedServers)

	m.logger.Debug("Synchronization completed",
		zap.Int("total_servers", len(allServers)),
		zap.Int("quarantined_servers", len(quarantinedServers)))

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
