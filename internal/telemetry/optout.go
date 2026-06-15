package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
)

// OptOutEvent is the distinguishing field value carried by the one-time opt-out
// beacon. Receivers route a payload to the opt-out funnel by the presence of
// this `event` value on the existing /heartbeat ingest path — no new endpoint
// is required (MCP-2482).
const OptOutEvent = "telemetry_disabled"

// OptOutBeacon is the minimal payload sent exactly once when a user disables
// telemetry. It carries ONLY the anonymous install ID (for unique opt-out
// counting / dedup) and the distinguishing event marker. It deliberately omits
// every usage field — sending data after an opt-out is only defensible because
// this payload contains nothing but the dedup ID.
type OptOutBeacon struct {
	Event       string `json:"event"`
	AnonymousID string `json:"anonymous_id"`
}

// TelemetryDisableTransition reports whether the resolved telemetry state moved
// from enabled to disabled between prior and next. "Resolved" follows
// Config.IsTelemetryEnabled — a nil Telemetry block or a nil Enabled pointer
// resolves to enabled (telemetry is opt-out), per MCP-2477. This is the single
// source of truth for the enabled->disabled flip, shared by the running daemon
// (REST / reload paths) and the `mcpproxy telemetry disable` CLI.
func TelemetryDisableTransition(prior, next *config.Config) bool {
	if prior == nil || next == nil {
		return false
	}
	return prior.IsTelemetryEnabled() && !next.IsTelemetryEnabled()
}

// SendOptOutBeacon posts a single opt-out beacon to the configured telemetry
// endpoint, reusing the existing /heartbeat ingest path. It is best-effort:
// callers MUST disable telemetry regardless of the returned error, and supply a
// context with a short timeout so the send never blocks a config save.
//
// The destination is taken from the service's own resolved endpoint/config
// (the exact indirection the heartbeat and feedback senders use) rather than a
// caller-supplied URL, so this never sends to an arbitrary, request-derived
// host.
func (s *Service) SendOptOutBeacon(ctx context.Context) error {
	anonID := s.config.GetAnonymousID()
	if anonID == "" {
		// Nothing to dedup on — never send an identity-less beacon.
		return errors.New("opt-out beacon skipped: no anonymous_id")
	}

	beacon := OptOutBeacon{Event: OptOutEvent, AnonymousID: anonID}
	data, err := json.Marshal(beacon)
	if err != nil {
		return fmt.Errorf("marshal opt-out beacon: %w", err)
	}

	// Defense-in-depth: the same anonymity scanner that guards heartbeats also
	// guards the beacon. The payload is a constant + a UUID, so this is belt-
	// and-suspenders, but it keeps a single invariant for everything we emit.
	if scanErr := ScanForPII(data); scanErr != nil {
		return fmt.Errorf("opt-out beacon failed anonymity scan: %w", scanErr)
	}

	url := s.endpoint + "/heartbeat"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build opt-out request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send opt-out beacon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("opt-out beacon rejected with status %d", resp.StatusCode)
	}
	return nil
}

// NotifyConfigChanged informs the telemetry service that the live configuration
// has been swapped. It is the single server-side hook for the opt-out beacon:
// the running daemon calls it from every config-write path (REST apply, disk
// reload) so web UI, macOS app, and CLI-driven changes are all covered by one
// implementation.
//
// On a resolved enabled->disabled transition it:
//  1. immediately marks telemetry opted-out (no further heartbeats are sent),
//  2. fires exactly one fire-and-forget opt-out beacon with a short timeout.
//
// The send is best-effort: a failure does not re-enable telemetry. On a
// disabled->enabled transition it clears the opt-out latch so a later disable
// flip emits its own beacon (exactly once per flip). It never blocks the caller.
func (s *Service) NotifyConfigChanged(newCfg *config.Config) {
	if newCfg == nil {
		return
	}

	s.mu.Lock()
	prior := s.resolvedEnabled
	next := newCfg.IsTelemetryEnabled()
	s.resolvedEnabled = next
	// Keep the service's live config/endpoint current. ApplyConfig swaps the
	// runtime's *config.Config pointer wholesale, so without this the service
	// would read a stale snapshot (see the stale-config-snapshot pitfall).
	s.config = newCfg
	s.endpoint = newCfg.GetTelemetryEndpoint()
	transition := prior && !next
	s.mu.Unlock()

	if !transition {
		// Re-enabling clears the latch so a future disable flip can fire again.
		if !prior && next {
			s.optedOut.Store(false)
		}
		return
	}

	// Stop all further telemetry immediately, before the (slower) network send.
	s.optedOut.Store(true)

	// Dev builds never emit telemetry; don't emit a beacon for them either.
	if !isValidSemver(s.version) {
		return
	}
	if newCfg.GetAnonymousID() == "" {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), optOutBeaconTimeout)
		defer cancel()
		if err := s.SendOptOutBeacon(ctx); err != nil {
			s.logger.Debug("opt-out beacon send failed (telemetry still disabled)", zap.Error(err))
		}
	}()
}
