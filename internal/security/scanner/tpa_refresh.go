package scanner

import (
	"context"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// The refresh scheduler (Spec 087 FR-003, FR-004, FR-009 to FR-015; Spec 101
// US3 tasks B6 to B8, B19, B20). One BundleRefresher per process runs a cycle
// per interval: the network-free file leg, then the optional fetch leg. Every
// trigger (timer tick, manual refresh, config nudge) that arrives during a
// cycle joins it. The real scheduler lands with T331.

// RefreshClock is the time seam of the scheduler. Tests inject a fake whose
// timers fire on demand; production uses the real clock (nil Clock).
type RefreshClock interface {
	Now() time.Time
	// NewTimer returns a timer that fires once after d.
	NewTimer(d time.Duration) RefreshTimer
}

// RefreshTimer is the timer half of RefreshClock, shaped like *time.Timer.
type RefreshTimer interface {
	// C delivers the fire time.
	C() <-chan time.Time
	Stop() bool
	Reset(d time.Duration) bool
}

// BundleRefresherOptions are the construction-time seams of a BundleRefresher.
// Every field is optional; the zero value selects the production defaults.
type BundleRefresherOptions struct {
	// Clock replaces the real clock and timers.
	Clock RefreshClock
	// Rand returns a uniform value in [0,1) for jitter.
	Rand func() float64
	// HTTPClient, when set, is used as is; otherwise one is built from
	// Transport and the egress rules.
	HTTPClient *http.Client
	// Transport replaces the base transport of the built client.
	Transport http.RoundTripper
	// Resolver replaces DNS for the dial-time egress control.
	Resolver FetchResolver
	// ConfigProvider returns the live security config each cycle. The
	// refresher never takes Runtime.mu to read it.
	ConfigProvider func() *config.SecurityConfig
	// Logger is the injected logger (nil => no-op).
	Logger *zap.Logger
	// Version is the mcpproxy version for the User-Agent header.
	Version string
	// IntervalOverride is a test seam that replaces the configured interval
	// and bypasses the 1h floor; zero means "use the config".
	IntervalOverride time.Duration
}

// BundleRefresher owns the refresh lifecycle. Start/Stop follow the
// telemetry.Service pattern: Stop cancels and joins, a Start after Stop is a
// no-op.
type BundleRefresher struct {
	opts BundleRefresherOptions

	mu      sync.Mutex
	running bool
}

// NewBundleRefresher builds a refresher; it does nothing until Start.
func NewBundleRefresher(opts BundleRefresherOptions) *BundleRefresher {
	return &BundleRefresher{opts: opts}
}

// Start begins the cycle loop, bound to ctx. A second Start, or a Start after
// Stop, is a no-op.
//
// STUB (Spec 101 US3 wave 0): no-op until T331.
func (r *BundleRefresher) Start(ctx context.Context) { _ = ctx }

// Stop cancels any in-flight cycle and returns only after the loop goroutine
// exited. Idempotent.
//
// STUB (Spec 101 US3 wave 0): no-op until T331.
func (r *BundleRefresher) Stop() {}

// Nudge tells the refresher the security config changed: it re-reads the
// fetch and trust settings, bumps the generation (superseding an in-flight
// download whose inputs changed) and re-arms the timer. Non-blocking; a Nudge
// after Stop began is a no-op.
//
// STUB (Spec 101 US3 wave 0): no-op until T331.
func (r *BundleRefresher) Nudge(sec *config.SecurityConfig) { _ = sec }

// Running reports whether the loop goroutine is alive.
//
// STUB (Spec 101 US3 wave 0): always false until T331.
func (r *BundleRefresher) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// Refresh runs one cycle now (trigger "manual"), joining an in-flight cycle if
// there is one, and returns the resulting status.
//
// STUB (Spec 101 US3 wave 0): falls back to ReloadBundle until T331.
func (r *BundleRefresher) Refresh(ctx context.Context, logger *zap.Logger) BundleInfo {
	_ = ctx
	return ReloadBundle(logger)
}

var (
	refresherSlotMu sync.RWMutex
	refresherSlot   *BundleRefresher
)

// SetBundleRefresher installs r as the process's refresher, the one
// RefreshBundle consults. nil clears the slot.
func SetBundleRefresher(r *BundleRefresher) {
	refresherSlotMu.Lock()
	defer refresherSlotMu.Unlock()
	refresherSlot = r
}

// CurrentBundleRefresher returns the installed refresher, or nil. The runtime
// uses it on Close to clear the slot only if it still points at its own
// instance.
func CurrentBundleRefresher() *BundleRefresher {
	refresherSlotMu.RLock()
	defer refresherSlotMu.RUnlock()
	return refresherSlot
}

// RefreshBundle is the manual refresh (`security bundle refresh`, B20): one
// joined cycle (file leg plus fetch leg), trigger "manual". When no running
// refresher is installed it falls back to ReloadBundle, so a manual refresh
// still picks up a dropped file.
//
// STUB (Spec 101 US3 wave 0): always falls back to ReloadBundle until T331.
func RefreshBundle(ctx context.Context, logger *zap.Logger) BundleInfo {
	if r := CurrentBundleRefresher(); r != nil && r.Running() {
		return r.Refresh(ctx, logger)
	}
	return ReloadBundle(logger)
}
