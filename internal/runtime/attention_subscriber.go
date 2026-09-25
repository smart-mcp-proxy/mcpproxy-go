package runtime

import (
	"context"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/health"
)

// attentionServerState is the subscriber's in-memory record of when a
// server's health.status last changed (data-model.md §4 "StateSince"). It is
// the only piece of state Compute needs that servers.changed itself does not
// carry, and it resets on restart (delaying a time-based item by at most the
// threshold it drives, FR-002).
type attentionServerState struct {
	status string
	since  time.Time
}

// AttentionEventItem is the minimal, wire-stable shape the runtime
// attention.changed event carries per item (data-model.md §4): just enough
// for internal/httpapi to narrow the id set per subscriber (FR-006) without
// re-deriving summaries/fixes on the SSE hot path.
type AttentionEventItem struct {
	ID          string `json:"id"`
	SubjectType string `json:"subject_type"`
	SubjectID   string `json:"subject_id"`
}

// attentionSubscriber recomputes the needs-attention list whenever
// servers.changed fires (debounced), and separately arms a threshold timer
// for the earliest pending time-based crossing (a connecting/error server
// reaching 60s, or — once 109-h wires client presence — a client reaching 5
// minutes unseen), capped at 30s, so GET /attention never under-reports on a
// quiet instance even when no event fires (FR-002, research D2).
type attentionSubscriber struct {
	rt *Runtime

	debounce time.Duration
	ch       chan Event

	// now, serverErrorThreshold, clientNeverSeenThreshold and timerCap default
	// to time.Now and the AttentionXxx package consts; tests shrink the
	// thresholds/cap to exercise FR-002's threshold-timer behaviour without
	// waiting real minutes (attention_threshold_timer_test.go).
	now                      func() time.Time
	serverErrorThreshold     time.Duration
	clientNeverSeenThreshold time.Duration
	timerCap                 time.Duration

	mu      sync.Mutex
	servers []contracts.Server
	clients []AttentionClient // always nil until 109-h wires ClientPresence
	state   map[string]attentionServerState
	lastIDs map[string]struct{}

	snapshot atomic.Pointer[[]contracts.AttentionItem]
}

func newAttentionSubscriber(rt *Runtime, debounce time.Duration) *attentionSubscriber {
	a := &attentionSubscriber{
		rt:                       rt,
		debounce:                 debounce,
		now:                      time.Now,
		serverErrorThreshold:     AttentionServerErrorThreshold,
		clientNeverSeenThreshold: AttentionClientNeverSeenThreshold,
		timerCap:                 AttentionTimerCap,
		state:                    make(map[string]attentionServerState),
		lastIDs:                  make(map[string]struct{}),
	}
	empty := []contracts.AttentionItem{}
	a.snapshot.Store(&empty)
	return a
}

// Items returns the current cached attention list. Safe to call from any
// goroutine (HTTP handlers, CLI-over-socket) concurrently with recompute.
func (a *attentionSubscriber) Items() []contracts.AttentionItem {
	if p := a.snapshot.Load(); p != nil {
		return *p
	}
	return nil
}

// start subscribes to the runtime event bus and launches the debounced
// recompute loop. Lifetime is tied to ctx (appCtx), matching the coalescer
// and scan-notify debouncer this file sits beside.
func (a *attentionSubscriber) start(ctx context.Context) {
	a.ch = a.rt.SubscribeEvents()
	a.primeFromManagement(ctx)
	go a.loop(ctx)
}

// primeFromManagement fetches the current server list directly so a freshly
// started core does not serve an empty attention list until the first
// servers.changed fires. Best-effort: if the management service is not
// wired yet (this subscriber starts at Runtime construction, same as
// coalescer), the next servers.changed event fills a.servers instead.
func (a *attentionSubscriber) primeFromManagement(ctx context.Context) {
	lister, ok := a.rt.managementService.(serversLister)
	if !ok || lister == nil {
		return
	}
	fetchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	servers, _, err := lister.ListServers(fetchCtx)
	if err != nil {
		return
	}
	redacted := make([]contracts.Server, 0, len(servers))
	for _, s := range servers {
		if s != nil {
			redacted = append(redacted, *s)
		}
	}
	a.rt.redactServerSecrets(redacted)
	a.rt.enrichServersWithQuarantineStats(redacted)
	a.mu.Lock()
	a.servers = redacted
	a.mu.Unlock()
	a.recompute()
}

func (a *attentionSubscriber) loop(ctx context.Context) {
	defer a.rt.UnsubscribeEvents(a.ch)

	var debounceTimer, thresholdTimer *time.Timer
	defer stopTimer(debounceTimer)
	defer stopTimer(thresholdTimer)

	for {
		var debounceC, thresholdC <-chan time.Time
		if debounceTimer != nil {
			debounceC = debounceTimer.C
		}
		if thresholdTimer != nil {
			thresholdC = thresholdTimer.C
		}

		select {
		case <-ctx.Done():
			return
		case evt, ok := <-a.ch:
			if !ok {
				return
			}
			if evt.Type != EventTypeServersChanged {
				continue
			}
			if !a.updateFromPayload(evt.Payload) {
				continue
			}
			if debounceTimer == nil {
				debounceTimer = time.NewTimer(a.debounce)
			} else {
				resetTimer(debounceTimer, a.debounce)
			}
		case <-debounceC:
			debounceTimer = nil
			next := a.recompute()
			thresholdTimer = rearmTimer(thresholdTimer, next)
		case <-thresholdC:
			thresholdTimer = nil
			next := a.recompute()
			thresholdTimer = rearmTimer(thresholdTimer, next)
		}
	}
}

// updateFromPayload stores the servers.changed embed for the next recompute.
// Returns false for a notify-only event (ListServers failed upstream when
// the payload was built) — there is nothing new to recompute from, so the
// previous snapshot stands until a full payload arrives.
func (a *attentionSubscriber) updateFromPayload(payload map[string]any) bool {
	servers, ok := payload["servers"].([]contracts.Server)
	if !ok {
		return false
	}
	a.mu.Lock()
	a.servers = servers
	a.mu.Unlock()
	return true
}

// recompute rebuilds the attention list from the latest known servers/clients,
// publishes attention.changed only when the id set changed, and returns the
// duration until the earliest pending threshold crossing (capped at
// AttentionTimerCap), or 0 when there is nothing time-based to wait for.
func (a *attentionSubscriber) recompute() time.Duration {
	now := a.now()

	a.mu.Lock()
	serversCopy := make([]contracts.Server, len(a.servers))
	copy(serversCopy, a.servers)
	clientsCopy := make([]AttentionClient, len(a.clients))
	copy(clientsCopy, a.clients)
	a.mu.Unlock()

	input := AttentionInput{
		Now:                      now,
		Clients:                  clientsCopy,
		ServerErrorThreshold:     a.serverErrorThreshold,
		ClientNeverSeenThreshold: a.clientNeverSeenThreshold,
	}
	for _, s := range serversCopy {
		input.Servers = append(input.Servers, a.buildAttentionServer(s, now))
	}

	items := Compute(input)
	itemsCopy := items
	a.snapshot.Store(&itemsCopy)

	ids := attentionIDSet(items)
	a.mu.Lock()
	changed := !equalStringSets(ids, a.lastIDs)
	if changed {
		a.lastIDs = ids
	}
	a.mu.Unlock()

	if changed {
		a.rt.publishEvent(newEvent(EventTypeAttentionChanged, map[string]any{
			"count": len(items),
			"items": AttentionEventItems(items),
		}))
	}

	return capThreshold(a.nextThreshold(input.Servers, input.Clients, now), a.timerCap)
}

// buildAttentionServer maps one contracts.Server row (already redacted and
// quarantine-enriched by the servers.changed producer) into the minimal
// AttentionServer Compute needs, stamping StateSince when health.status
// differs from the last-seen value.
func (a *attentionSubscriber) buildAttentionServer(s contracts.Server, now time.Time) AttentionServer {
	status := ""
	var actions []string
	health := contracts.HealthStatus{}
	if s.Health != nil {
		health = *s.Health
		status = s.Health.Status
		actions = s.Health.Actions
	}
	health.Actions = actions

	a.mu.Lock()
	st, known := a.state[s.Name]
	if !known || st.status != status {
		st = attentionServerState{status: status, since: now}
		a.state[s.Name] = st
	}
	a.mu.Unlock()

	pending, changed := 0, 0
	if s.Quarantine != nil {
		pending = s.Quarantine.PendingCount
		changed = s.Quarantine.ChangedCount
	}

	return AttentionServer{
		Name:        s.Name,
		Enabled:     s.Enabled,
		Quarantined: s.Quarantined,
		Health:      health,
		Pending:     pending,
		Changed:     changed,
		StateSince:  st.since,
		Detail:      attentionServerDetail(s),
	}
}

// attentionServerDetail builds the item detail line from the row: transport
// + URL host only, e.g. "OAuth · api.githubcopilot.com" (data-model.md §4).
// Never a secret, URL path/query or header.
func attentionServerDetail(s contracts.Server) string {
	transport := "stdio"
	switch {
	case s.OAuth != nil:
		transport = "OAuth"
	case s.URL != "":
		transport = "HTTP"
	}

	host := ""
	if s.URL != "" {
		if u, err := url.Parse(s.URL); err == nil {
			host = u.Host
		}
	}
	if host == "" {
		return transport
	}
	return transport + " · " + host
}

// nextThreshold returns the earliest future time-based crossing: a
// connecting/error server reaching AttentionServerErrorThreshold, or a
// connected-never-seen client reaching AttentionClientNeverSeenThreshold.
// Returns 0 when nothing pending is time-based (either already an item, or
// not applicable).
func (a *attentionSubscriber) nextThreshold(servers []AttentionServer, clients []AttentionClient, now time.Time) time.Duration {
	var earliest time.Duration
	have := false

	consider := func(d time.Duration) {
		if d <= 0 {
			return
		}
		if !have || d < earliest {
			earliest = d
			have = true
		}
	}

	for _, s := range servers {
		if !s.Enabled || s.Quarantined {
			continue
		}
		if s.Health.Status == health.StatusConnecting || s.Health.Status == health.StatusError {
			deadline := s.StateSince.Add(a.serverErrorThreshold)
			consider(deadline.Sub(now))
		}
	}
	for _, c := range clients {
		if c.ConnectedAt == nil {
			continue
		}
		if c.LastSeen != nil && !c.LastSeen.Before(*c.ConnectedAt) {
			continue
		}
		deadline := c.ConnectedAt.Add(a.clientNeverSeenThreshold)
		consider(deadline.Sub(now))
	}

	if !have {
		return 0
	}
	return earliest
}

func attentionIDSet(items []contracts.AttentionItem) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		out[it.ID] = struct{}{}
	}
	return out
}

func AttentionEventItems(items []contracts.AttentionItem) []AttentionEventItem {
	out := make([]AttentionEventItem, len(items))
	for i, it := range items {
		out[i] = AttentionEventItem{ID: it.ID, SubjectType: it.Subject.Type, SubjectID: it.Subject.ID}
	}
	return out
}

func equalStringSets(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

func rearmTimer(old *time.Timer, d time.Duration) *time.Timer {
	if old != nil {
		old.Stop()
	}
	if d <= 0 {
		return nil
	}
	return time.NewTimer(d)
}

func capThreshold(d, ceiling time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > ceiling {
		return ceiling
	}
	return d
}
