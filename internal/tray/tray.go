package tray

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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

// App represents the system tray application
type App struct {
	server   interface{} // placeholder for server interface
	logger   *zap.SugaredLogger
	version  string
	shutdown func()
}

// New creates a new tray application
func New(server interface{}, logger *zap.SugaredLogger, version string, shutdown func()) *App {
	return &App{
		server:   server,
		logger:   logger,
		version:  version,
		shutdown: shutdown,
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

	// Monitor context cancellation and quit systray when needed
	go func() {
		<-ctx.Done()
		a.logger.Info("Context cancelled, quitting systray")
		systray.Quit()
	}()

	// Start systray - this is a blocking call that must run on main thread
	systray.Run(a.onReady, a.onExit)

	return ctx.Err()
}

func (a *App) onReady() {
	a.logger.Info("System tray onReady called")

	systray.SetTitle("mcp")
	systray.SetTooltip("Smart MCP Proxy")

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
	mStatus := systray.AddMenuItem("Status: Running", "Server status")
	mStatus.Disable()

	systray.AddSeparator()

	mUpdate := systray.AddMenuItem("Check for Updates…", "Check for application updates")
	mConfig := systray.AddMenuItem("Open Config", "Open configuration file")

	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Quit mcpproxy")

	// Handle menu clicks
	go func() {
		for {
			select {
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
			}
		}
	}()
}

func (a *App) onExit() {
	a.logger.Info("System tray exiting")
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

	// Apply the update
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
