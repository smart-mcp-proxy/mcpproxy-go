package server

import (
	"sync"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
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
	setBundleRefreshObserver    = scanner.SetBundleRefreshObserver
	bundleDoctorWarnings        = scanner.BundleDoctorWarnings
	recordBundleRefresh         = telemetry.RecordTPABundleRefreshOn
)

// mapRefreshClass maps the scanner's refresh-cycle class to the telemetry enum
// (internal/telemetry must not import scanner, so the mapping lives here). An
// unknown class counts as unchanged rather than inventing a bucket.
func mapRefreshClass(class scanner.RefreshOutcomeClass) telemetry.TPABundleRefreshClass {
	switch class {
	case scanner.RefreshClassActivated:
		return telemetry.TPABundleRefreshActivated
	case scanner.RefreshClassRejected:
		return telemetry.TPABundleRefreshRejected
	case scanner.RefreshClassUnavailable:
		return telemetry.TPABundleRefreshUnavailable
	default:
		return telemetry.TPABundleRefreshUnchanged
	}
}

// setBundleNudger replaces the refresher the config.reloaded listener nudges.
func (s *Server) setBundleNudger(n bundleNudger) {
	s.bundleWiring.mu.Lock()
	defer s.bundleWiring.mu.Unlock()
	s.bundleWiring.nudger = n
}

// bundleNudgerOrNil returns the registered nudger, if any.
func (s *Server) bundleNudgerOrNil() bundleNudger {
	s.bundleWiring.mu.Lock()
	defer s.bundleWiring.mu.Unlock()
	return s.bundleWiring.nudger
}
