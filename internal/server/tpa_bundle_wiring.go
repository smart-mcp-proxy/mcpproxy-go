package server

import (
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Production wiring of the TPA bundle refresh lifecycle (Spec 087 / Spec 101
// US3, tasks B13, B16, B17, B19, B21): the activation self-check, the daily
// refresher, the refresh observer that feeds telemetry, the activation observer
// that drives the re-protection sweep, and the doctor warning source.

// bundleNudger is the part of the refresher the config.reloaded listener uses.
type bundleNudger interface {
	Nudge(sec *config.SecurityConfig)
}

// bundleWiring holds the server-side handles of that wiring. The zero value is
// ready to use.
type bundleWiring struct {
	mu          sync.Mutex
	nudger      bundleNudger
	unsubscribe func()
}

// Test seams over the scanner package-level hooks. Production never replaces
// them.
var (
	enableActivationSelfCheck   = scanner.EnableActivationSelfCheck
	addBundleActivationObserver = scanner.AddBundleActivationObserver
)
