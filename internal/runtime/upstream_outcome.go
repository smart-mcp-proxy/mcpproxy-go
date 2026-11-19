package runtime

import (
	"time"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
)

// ensureInstallAnchor writes the install anchor when config.db held no prior
// data at open (Spec 126 FR-033). It never rewrites an anchor and never
// creates one on an existing store. A failure is logged and startup goes on:
// the install then reports install_origin "existing", never a guess.
func ensureInstallAnchor(sm *storage.Manager, logger *zap.Logger) {
	if sm == nil {
		return
	}
	if _, _, err := sm.EnsureInstallAnchor(time.Now()); err != nil {
		logger.Warn("Failed to record the install anchor", zap.Error(err))
	}
}

// StarterClassifier decides whether an upstream server is the starter server
// (Spec 126 FR-037): the one the starter door added, identified locally by name
// AND incarnation, so a user's own later add of the same catalog entry (a new
// incarnation) is never starter traffic. Names are used only locally; the
// telemetry registry receives a boolean.
//
// PR-A installs no starter, so the default answers false for every server.
// PR-B replaces it through SetStarterClassifier. Implementations must be safe
// for concurrent use and cheap: IsStarter runs once per successful upstream
// call.
type StarterClassifier interface {
	IsStarter(serverName string, incarnation int64) bool
}

// noStarterClassifier is the PR-A default: no server is the starter.
type noStarterClassifier struct{}

func (noStarterClassifier) IsStarter(string, int64) bool { return false }

// starterClassifierHolder lets an interface value live in an atomic.Pointer.
type starterClassifierHolder struct{ c StarterClassifier }

// SetStarterClassifier installs the starter classifier; nil restores the
// default, which answers false.
func (r *Runtime) SetStarterClassifier(c StarterClassifier) {
	if r == nil {
		return
	}
	if c == nil {
		r.starterClassifier.Store(nil)
		return
	}
	r.starterClassifier.Store(&starterClassifierHolder{c: c})
}

// StarterClassifier returns the installed classifier, or the default.
func (r *Runtime) StarterClassifier() StarterClassifier {
	if r != nil {
		if h := r.starterClassifier.Load(); h != nil && h.c != nil {
			return h.c
		}
	}
	return noStarterClassifier{}
}

// RecordUpstreamOutcome records the outcome of ONE logical upstream call that
// dispatched at least one request (Spec 126 FR-035, FR-038). usable is true
// for a usable result: no error, no isError:true, not withheld by an output
// policy. The dispatch paths call it once per logical call, through
// MCPProxyServer.noteUpstreamOutcome; a call refused before dispatch never
// reaches it.
//
// The outcome goes to the reset-on-accept counter window (sent only with
// telemetry on). A usable result also advances the persisted
// success_scope_ever, which is local state recorded whatever the opt-out
// setting. nil-safe, and a no-op before SetTelemetry.
func (r *Runtime) RecordUpstreamOutcome(serverName string, incarnation int64, usable bool) {
	if r == nil || r.telemetryService == nil {
		return
	}
	starter := usable && r.StarterClassifier().IsStarter(serverName, incarnation)
	telemetry.RecordUpstreamOutcomeOn(r.telemetryService.Registry(), usable, starter)
	if !usable {
		return
	}
	// The scope only moves forward, so once this process has seen it reach
	// the target no read is needed (a busy install would otherwise open a
	// bbolt read transaction per usable call).
	target := uint32(telemetry.SuccessScopeNonStarter)
	if starter {
		target = uint32(telemetry.SuccessScopeStarterOnly)
	}
	if r.successScopeReached.Load() >= target {
		return
	}
	if db := r.telemetryService.ActivationDB(); db != nil {
		if err := telemetry.RecordSuccessScope(db, starter); err != nil {
			r.logger.Debug("activation: RecordSuccessScope failed", zap.Error(err))
			return
		}
		for {
			cur := r.successScopeReached.Load()
			if cur >= target || r.successScopeReached.CompareAndSwap(cur, target) {
				break
			}
		}
	}
}

// wireDayTwoTelemetry connects the schema-17 instruments to storage (Spec 126
// FR-034, FR-036): the install-anchor reader for day_two and window_age, and
// the first counter window's coverage, decided from the store's state at open
// and the persisted undelivered-window marker. Called from SetTelemetry after
// the activation store is wired. The anchor itself is written earlier, by
// ensureInstallAnchor in New, right after the store opens (after the Spec 080
// shutdown marker and the Spec 123 OAuth boot sweep).
func (r *Runtime) wireDayTwoTelemetry() {
	sm := r.storageManager
	if sm == nil || r.telemetryService == nil {
		return
	}
	r.telemetryService.SetInstallAnchorProvider(func() (time.Time, bool, error) {
		return sm.InstallAnchor()
	})
	r.telemetryService.InitOutcomeWindow(sm.WasEmptyAtOpen())
}
