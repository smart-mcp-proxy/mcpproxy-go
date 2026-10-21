package server

import (
	"sync"
	"sync/atomic"

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
	observerSeq uint64
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

// refreshObserverOwner identifies the Server whose refresh observer is
// installed, so a Server that shuts down clears the process-wide observer only
// while it is still the owner (tests build several Servers in one process).
var (
	refreshObserverOwner atomic.Uint64
	refreshObserverSeq   atomic.Uint64
)

// wireTPABundleLifecycle registers the server-side hooks of the refresh
// lifecycle: the refresher the config.reloaded listener nudges, the
// refresh-cycle observer that feeds the schema-v16 telemetry counters, and the
// activation observer that drives the re-protection sweep.
func (s *Server) wireTPABundleLifecycle(refresher bundleNudger) {
	s.setBundleNudger(refresher)

	seq := refreshObserverSeq.Add(1)
	refreshObserverOwner.Store(seq)
	rt := s.runtime
	setBundleRefreshObserver(func(class scanner.RefreshOutcomeClass) {
		recordBundleRefresh(rt.TelemetryRegistry(), mapRefreshClass(class))
	})

	unsubscribe := addBundleActivationObserver(s.handleBundleActivation)
	s.bundleWiring.mu.Lock()
	s.bundleWiring.unsubscribe = unsubscribe
	s.bundleWiring.observerSeq = seq
	s.bundleWiring.mu.Unlock()
}

// unwireTPABundleLifecycle undoes wireTPABundleLifecycle at Shutdown: it stops
// activation delivery, clears the refresh observer if this Server still owns
// it, and cancels and joins every re-protection sweep. Idempotent.
func (s *Server) unwireTPABundleLifecycle() {
	s.bundleWiring.mu.Lock()
	unsubscribe := s.bundleWiring.unsubscribe
	s.bundleWiring.unsubscribe = nil
	seq := s.bundleWiring.observerSeq
	s.bundleWiring.observerSeq = 0
	s.bundleWiring.mu.Unlock()

	if unsubscribe != nil {
		unsubscribe()
	}
	if seq != 0 && refreshObserverOwner.CompareAndSwap(seq, 0) {
		setBundleRefreshObserver(nil)
	}
	s.stopBundleReprotect()
}
