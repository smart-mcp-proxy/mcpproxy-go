package runtime

import (
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/diagnostics"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/types"
)

// Spec 124 D5: upstream events in the activity log.
//
// A failed call says "connection refused". What an operator needs next to it
// is the reason one level up: the server's connection was lost two seconds
// earlier, its process exited, its OAuth refresh failed, it needs a new login.
// These are written as activity rows of type upstream_event, with the time the
// change actually happened, so a session timeline can place them between the
// calls before and after.

// UpstreamEventKind names what happened to an upstream server.
type UpstreamEventKind string

const (
	UpstreamConnectionLost     UpstreamEventKind = "connection_lost"
	UpstreamProcessExited      UpstreamEventKind = "process_exited"
	UpstreamLoginRequired      UpstreamEventKind = "login_required"
	UpstreamConnectionRestored UpstreamEventKind = "connection_restored"
	UpstreamOAuthRefreshFailed UpstreamEventKind = "oauth_refresh_failed"
)

// Metadata keys of an upstream_event row.
const (
	UpstreamEventMetaKind    = "kind"
	UpstreamEventMetaCode    = "code"
	UpstreamEventMetaCount   = "count"
	UpstreamEventMetaFirstAt = "first_at"
	UpstreamEventMetaLastAt  = "last_at"
)

const (
	// upstreamEventCollapseWindow: repeats of one kind for one server within
	// this window of the row's first event are stored in that row (FR-022).
	upstreamEventCollapseWindow = time.Minute
	// MaxUpstreamEventRowsPerHour bounds the rows one server can add in one
	// hour; past it, events merge into the latest row of their kind (FR-022).
	MaxUpstreamEventRowsPerHour = 120
)

// UpstreamEvent is one occurrence to record.
type UpstreamEvent struct {
	Server string
	Kind   UpstreamEventKind
	At     time.Time
	// Code is the diagnostics code (MCPX_*), when one applies. The row carries
	// the code and its fixed catalogue message, never the raw error (FR-021).
	Code string
	// Owner is the user whose upstream credential the event is about (server
	// edition). Empty for an event shared by every user of the server.
	Owner string
	// Seq orders a login_required raised by the OAuth refresh manager against
	// its token saves (see NoteUpstreamTokenSaved). Zero for events derived
	// from the connection state.
	Seq uint64
	// Count is how many occurrences the event stands for; zero means one.
	// Only the replay of a folded transition sets it.
	Count int
}

// occurrences is the event's count, at least one.
func (e UpstreamEvent) occurrences() int {
	if e.Count > 1 {
		return e.Count
	}
	return 1
}

// fixedUpstreamMessages are the row texts for events with no diagnostics code.
var fixedUpstreamMessages = map[UpstreamEventKind]string{
	UpstreamConnectionLost:     "The connection to the upstream server was lost.",
	UpstreamProcessExited:      "The upstream server's process exited.",
	UpstreamLoginRequired:      "The upstream server needs a new login.",
	UpstreamConnectionRestored: "The connection to the upstream server was restored.",
	UpstreamOAuthRefreshFailed: "Refreshing the upstream server's OAuth token failed.",
}

// upstreamEventMessage returns the allow-listed text of an event: the
// catalogue message of its code, or the fixed text of its kind.
func upstreamEventMessage(kind UpstreamEventKind, code string) string {
	if code != "" {
		if entry, ok := diagnostics.Get(diagnostics.Code(code)); ok && entry.UserMessage != "" {
			return entry.UserMessage
		}
	}
	return fixedUpstreamMessages[kind]
}

// transitionEvent maps one state change to the upstream event it means, if
// any. It decides only from the transition; whether a recovery is worth a row
// (a loss was recorded first) is decided by the collapser, which has the
// history.
func transitionEvent(server, transport string, t types.Transition) (UpstreamEvent, bool) {
	ev := UpstreamEvent{Server: server, At: t.At.UTC()}
	stdio := transport == "stdio"

	code := t.TerminalCode
	if code == "" && t.Err != nil {
		code = string(diagnostics.Classify(t.Err, diagnostics.ClassifierHints{ServerID: server, Transport: transport}))
	}
	if code == string(diagnostics.UnknownUnclassified) {
		code = ""
	}
	// A connection that was established did not "exit before initialize",
	// whatever the classifier makes of the error text.
	if t.From == types.StateReady && code == string(diagnostics.STDIOExitBeforeInitialize) {
		code = ""
	}
	loginCode := code == string(diagnostics.OAuthLoginRequired) || code == string(diagnostics.OAuthReauthRequired)

	switch {
	case t.To == types.StateReady && t.From != types.StateReady:
		ev.Kind = UpstreamConnectionRestored
		return ev, true

	case t.To == types.StatePendingAuth && t.From != types.StatePendingAuth, loginCode && t.To == types.StateError:
		ev.Kind = UpstreamLoginRequired
		if loginCode {
			ev.Code = code
		} else {
			ev.Code = string(diagnostics.OAuthLoginRequired)
		}
		return ev, true

	case t.From == types.StateReady && t.To != types.StateReady:
		ev.Code = code
		switch {
		case strings.HasPrefix(code, "MCPX_STDIO_EXIT"):
			ev.Kind = UpstreamProcessExited
		case stdio && t.Err != nil:
			// An established stdio connection that fails is its process (or
			// its pipes) going away. The error is usually just "transport
			// closed", which carries no exit code.
			ev.Kind = UpstreamProcessExited
		default:
			// Any other transport, and a stdio server that was disconnected
			// on purpose (no error): a lost or closed connection.
			ev.Kind = UpstreamConnectionLost
		}
		return ev, true
	}
	return UpstreamEvent{}, false
}

// transitionEvents is transitionEvent plus the changes a folded transition
// hid (the observer had stalled and the queue was full). The hidden losses,
// recoveries and login requirements are replayed as one event per kind, each
// carrying how many there were, at the record's time and before the change
// the record itself shows. Hidden events carry no error code: the error of
// each was folded away with it.
//
// A folded record keeps the counts and the state it ends in, not the exact
// interleaving of what it hid: where a hidden login requirement fell among
// several hidden losses and recoveries is not known, so a count can be off by
// one in a sequence that mixes all three. This only happens after the
// observer stalled for a whole queue of transitions.
//
// Their order is the one that leaves the right state behind. Losses and
// recoveries alternated, so which of the two came last follows from where
// the record starts and how many there were. A login requirement is replayed
// last when no recovery followed it (it is still open), and first otherwise,
// so that the recovery closes it.
func transitionEvents(server, transport string, t types.Transition) []UpstreamEvent {
	var out []UpstreamEvent
	at := t.At.UTC()
	add := func(kind UpstreamEventKind, code string, count int) {
		if count > 0 {
			out = append(out, UpstreamEvent{Server: server, Kind: kind, At: at, Code: code, Count: count})
		}
	}
	lost, restored, login := t.FoldedLost, t.FoldedRestored, t.FoldedLogin
	loginLast := login > 0 && (t.FoldedLoginLast || restored == 0)
	// Starting in Ready the hidden sequence is loss, recovery, loss, ...;
	// otherwise recovery, loss, recovery, ...
	recoveryLast := restored > lost
	if t.From == types.StateReady {
		recoveryLast = restored >= lost
	}
	if !loginLast {
		add(UpstreamLoginRequired, string(diagnostics.OAuthLoginRequired), login)
	}
	// The first change of each kind is replayed on its own, in the order the
	// two happened, and the rest as weights. A leading recovery with no loss
	// before it is the first connect, which the collapser does not record;
	// replayed apart, it takes none of the later recoveries with it.
	if t.From == types.StateReady {
		if lost > 0 {
			add(UpstreamConnectionLost, "", 1)
			lost--
		}
		if restored > 0 {
			add(UpstreamConnectionRestored, "", 1)
			restored--
		}
	} else {
		if restored > 0 {
			add(UpstreamConnectionRestored, "", 1)
			restored--
		}
		if lost > 0 {
			add(UpstreamConnectionLost, "", 1)
			lost--
		}
	}
	if recoveryLast {
		add(UpstreamConnectionLost, "", lost)
		add(UpstreamConnectionRestored, "", restored)
	} else {
		add(UpstreamConnectionRestored, "", restored)
		add(UpstreamConnectionLost, "", lost)
	}
	if loginLast {
		add(UpstreamLoginRequired, string(diagnostics.OAuthLoginRequired), login)
	}
	if ev, ok := transitionEvent(server, transport, t); ok {
		out = append(out, ev)
	}
	return out
}

type upstreamRowKey struct {
	server string
	kind   UpstreamEventKind
	owner  string
}

type upstreamOwnerKey struct{ server, owner string }

// upstreamRow is the stored row an event may still be merged into.
type upstreamRow struct {
	id      string
	firstAt time.Time
	lastAt  time.Time
	count   int
	code    string
}

// upstreamEventCollapser turns a stream of events into a bounded number of
// rows. Its state is in memory only: after a restart at most one extra row
// per key is written.
type upstreamEventCollapser struct {
	mu sync.Mutex
	// latest is the most recent row of each (server, kind, owner).
	latest map[upstreamRowKey]*upstreamRow
	// hourRows counts the rows a server added in each hour, keyed by the
	// hour of the EVENT. Events of one server can arrive slightly out of
	// order (the state drain and the refresh observer deliver separately),
	// so a late event of the previous hour must not reset the current one.
	hourRows map[string]map[int64]int
	// seen is when each server last had an event, for cleanup.
	seen   map[string]time.Time
	lastGC time.Time
	// loginSavedAt is when the latest numbered token save of each (server,
	// owner) was applied, to order unnumbered login requirements against it.
	loginSavedAt map[upstreamOwnerKey]time.Time
	// lastSaveGC is when a token save last ran the idle-server cleanup.
	lastSaveGC time.Time
	// now is the clock of token-save notifications, which carry no time.
	now func() time.Time
	// forgottenBefore is the first hour (unix) whose row counters are still
	// kept; cleanup dropped the counters of every hour before it.
	forgottenBefore int64
	// suppressed counts events not recorded because their server was at the
	// hourly bound and had no row of their kind to merge into.
	suppressed int
	// down marks servers with a recorded loss, exit or login requirement that
	// no recovery has followed. Only such a server gets a "restored" row.
	down map[string]bool
	// loginOpen is the open login_required row per (server, owner).
	loginOpen map[upstreamOwnerKey]*upstreamRow
	// loginSeq is the highest refresh-manager notification number applied per
	// (server, owner): a lower-numbered failure does not open the mark and a
	// lower-numbered save does not clear it.
	loginSeq map[upstreamOwnerKey]uint64
}

func newUpstreamEventCollapser() *upstreamEventCollapser {
	return &upstreamEventCollapser{
		latest:       make(map[upstreamRowKey]*upstreamRow),
		hourRows:     make(map[string]map[int64]int),
		seen:         make(map[string]time.Time),
		down:         make(map[string]bool),
		loginOpen:    make(map[upstreamOwnerKey]*upstreamRow),
		loginSeq:     make(map[upstreamOwnerKey]uint64),
		loginSavedAt: make(map[upstreamOwnerKey]time.Time),
		now:          time.Now,
	}
}

// place decides which row an event goes into. It returns the row and whether
// the row is new; a nil row means the event is not recorded at all.
func (c *upstreamEventCollapser) place(e UpstreamEvent) (row *upstreamRow, isNew bool) {
	if e.At.After(c.seen[e.Server]) {
		c.seen[e.Server] = e.At
	}
	c.gc(e.At)
	key := upstreamRowKey{e.Server, e.Kind, e.Owner}
	owner := upstreamOwnerKey{e.Server, e.Owner}

	switch e.Kind {
	case UpstreamConnectionRestored:
		if !c.down[e.Server] {
			return nil, false // the first connect, or a recovery nobody saw fail
		}
		// A reconnect ends the open login requirements of the server that
		// precede it. One seen after this recovery happened (the recovery
		// was delivered late) stays open, and the server stays down for it.
		stillOpen := false
		for k, open := range c.loginOpen {
			if k.server != e.Server {
				continue
			}
			if open.lastAt.After(e.At) {
				stillOpen = true
				continue
			}
			delete(c.loginOpen, k)
		}
		c.down[e.Server] = stillOpen
	case UpstreamConnectionLost, UpstreamProcessExited:
		c.down[e.Server] = true
	case UpstreamLoginRequired:
		// A failure older than a save already applied is still recorded as
		// a row, but it says nothing about the server now: it neither opens
		// a login requirement nor marks the server down.
		stale := e.Seq != 0 && e.Seq < c.loginSeq[owner]
		// A requirement read from the connection state carries no number.
		// It is ordered against token saves by time: one captured before a
		// save that has already been applied (its delivery stalled) is as
		// stale as an older numbered failure.
		if saved, ok := c.loginSavedAt[owner]; ok && e.Seq == 0 && e.At.Before(saved) {
			stale = true
		}
		if !stale {
			c.down[e.Server] = true
			if e.Seq > c.loginSeq[owner] {
				c.loginSeq[owner] = e.Seq
			}
			if open := c.loginOpen[owner]; open != nil {
				// The same condition, seen again or from the other source.
				open.count += e.occurrences()
				if e.At.After(open.lastAt) {
					open.lastAt = e.At
				}
				return open, false
			}
		}
		row, isNew = c.rowFor(key, e)
		if !stale && row != nil {
			c.loginOpen[owner] = row
		}
		return row, isNew
	}
	return c.rowFor(key, e)
}

// Bounds of the collapser's own memory.
const (
	// upstreamMergeAt is the number of rows in an hour at which a server's
	// repeats start merging into the latest row of their kind regardless of
	// the minute window. The rows between it and the hard limit are left for
	// kinds and owners that have no row yet.
	upstreamMergeAt = 100
	// collapserGCInterval is how often idle state is dropped.
	collapserGCInterval = 10 * time.Minute
	// collapserRowIdle is how long a row can still be merged into.
	collapserRowIdle = 2 * time.Hour
	// collapserServerIdle is how long a server with no event keeps its
	// down/login state. A notification cannot plausibly be this late, and a
	// removed server's state must not stay for the life of the process.
	collapserServerIdle = 7 * 24 * time.Hour
)

// rowFor applies the per-minute collapse and the hourly bound. It returns a
// nil row when the event cannot be recorded without breaking the bound.
func (c *upstreamEventCollapser) rowFor(key upstreamRowKey, e UpstreamEvent) (*upstreamRow, bool) {
	hour := e.At.Truncate(time.Hour).Unix()
	hours := c.hourRows[e.Server]
	if hours == nil {
		hours = make(map[int64]int)
		c.hourRows[e.Server] = hours
	}
	used := hours[hour]

	last := c.latest[key]
	if last != nil && !e.At.Before(last.firstAt) &&
		(e.At.Sub(last.firstAt) < upstreamEventCollapseWindow || used >= upstreamMergeAt) {
		last.count += e.occurrences()
		if e.At.After(last.lastAt) {
			last.lastAt = e.At
		}
		return last, false
	}
	if hour < c.forgottenBefore {
		// The counter of this hour was dropped by cleanup, so how many rows
		// the hour already has is not known. An event this late (its source
		// stalled for hours) is not given a new row, or the hourly bound
		// would start again from zero.
		c.suppressed++
		return nil, false
	}
	if used >= MaxUpstreamEventRowsPerHour {
		// The hard limit, and nothing of this kind and owner to merge into.
		// Merging into another owner's row would mix two users (FR-025), so
		// the event is not recorded.
		c.suppressed++
		return nil, false
	}
	row := &upstreamRow{id: ulid.Make().String(), firstAt: e.At, lastAt: e.At, count: e.occurrences(), code: e.Code}
	c.latest[key] = row
	hours[hour] = used + 1
	return row, true
}

// gc drops state that can no longer matter: rows too old to merge into, hour
// counters of past hours, and everything about a server that has had no event
// for a long time (a removed server). now is the time of the current event.
func (c *upstreamEventCollapser) gc(now time.Time) {
	if now.Sub(c.lastGC) < collapserGCInterval {
		return
	}
	c.lastGC = now
	for k, row := range c.latest {
		if now.Sub(row.lastAt) > collapserRowIdle {
			delete(c.latest, k)
		}
	}
	oldestHour := now.Add(-2 * time.Hour).Truncate(time.Hour).Unix()
	if oldestHour > c.forgottenBefore {
		c.forgottenBefore = oldestHour
	}
	for server, hours := range c.hourRows {
		for h := range hours {
			if h < oldestHour {
				delete(hours, h)
			}
		}
		if len(hours) == 0 {
			delete(c.hourRows, server)
		}
	}
	c.forgetIdleServers(now)
}

// forgetIdleServers drops everything about a server that has had neither an
// event nor a token save for a long time (a removed server). It is separate
// from the hour counters so that a token save, which has no event time of its
// own, can run it without touching them.
func (c *upstreamEventCollapser) forgetIdleServers(now time.Time) {
	for server, at := range c.seen {
		if now.Sub(at) <= collapserServerIdle {
			continue
		}
		delete(c.seen, server)
		delete(c.down, server)
		for k := range c.loginOpen {
			if k.server == server {
				delete(c.loginOpen, k)
			}
		}
		for k := range c.loginSeq {
			if k.server == server {
				delete(c.loginSeq, k)
				delete(c.loginSavedAt, k)
			}
		}
	}
}

// noteTokenSaved clears the open login requirement of (server, owner), unless
// the save is older than a failure already applied.
func (c *upstreamEventCollapser) noteTokenSaved(server, owner string, seq uint64, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := upstreamOwnerKey{server, owner}
	if seq != 0 {
		if seq < c.loginSeq[k] {
			return
		}
		c.loginSeq[k] = seq
		if at.IsZero() {
			at = c.now()
		}
		if at.After(c.loginSavedAt[k]) {
			c.loginSavedAt[k] = at
		}
		// A save is activity of the server for cleanup: it keeps the number
		// of a server that is saving tokens, and lets cleanup run (and
		// forget servers that are gone) when there are saves and no events.
		now := c.now()
		if now.After(c.seen[server]) {
			c.seen[server] = now
		}
		if now.Sub(c.lastSaveGC) >= collapserGCInterval {
			c.lastSaveGC = now
			c.forgetIdleServers(now)
		}
	}
	// A requirement seen after the save was accepted is newer than the save
	// (this notification was delivered late) and stays open.
	if open := c.loginOpen[k]; open != nil && !at.IsZero() && open.lastAt.After(at) {
		return
	}
	delete(c.loginOpen, k)
}

// RecordUpstreamEvent writes or updates the activity row of an upstream event.
//
// It runs synchronously on the caller's goroutine, like RecordToolCallRejected
// and for the same reason: the event bus drops events for a subscriber that
// has fallen behind, and an outage is exactly when it does. Events of one
// server arrive in order from that server's transition drain; the collapser's
// lock serialises servers against each other.
func (s *ActivityService) RecordUpstreamEvent(e UpstreamEvent) {
	if s == nil || s.storage == nil || e.Server == "" || e.Kind == "" {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	if !s.enterWrite() {
		return
	}
	defer s.workersWG.Done()

	s.upstreamEvents.mu.Lock()
	defer s.upstreamEvents.mu.Unlock()

	row, _ := s.upstreamEvents.place(e)
	if row == nil {
		return
	}
	status := storage.ActivityStatusError
	if e.Kind == UpstreamConnectionRestored {
		status = storage.ActivityStatusSuccess
	}
	record := &storage.ActivityRecord{
		ID:           row.id,
		Type:         storage.ActivityTypeUpstreamEvent,
		Source:       storage.ActivitySourceInternal,
		ServerName:   e.Server,
		Status:       status,
		ErrorMessage: upstreamEventMessage(e.Kind, row.code),
		Timestamp:    row.firstAt,
		UserID:       e.Owner,
		Metadata: map[string]interface{}{
			UpstreamEventMetaKind:    string(e.Kind),
			UpstreamEventMetaCount:   row.count,
			UpstreamEventMetaFirstAt: row.firstAt.Format(time.RFC3339Nano),
			UpstreamEventMetaLastAt:  row.lastAt.Format(time.RFC3339Nano),
		},
	}
	if status == storage.ActivityStatusSuccess {
		record.ErrorMessage = ""
		record.Metadata["message"] = upstreamEventMessage(e.Kind, "")
	}
	if row.code != "" {
		record.Metadata[UpstreamEventMetaCode] = row.code
	}
	// The key is (first time, id), so saving the row again rewrites it in
	// place with the new count and last time.
	if err := s.storage.SaveActivity(record); err != nil {
		s.logger.Error("Failed to save upstream event",
			zap.Error(err), zap.String("server_name", e.Server), zap.String("kind", string(e.Kind)))
	}
}

// NoteUpstreamTokenSaved tells the collapser that a new OAuth token was saved
// for (server, owner), which ends an open login requirement. seq is the
// refresh manager's notification number for the save, or zero.
func (s *ActivityService) NoteUpstreamTokenSaved(server, owner string, seq uint64, at time.Time) {
	if s == nil || s.upstreamEvents == nil {
		return
	}
	s.upstreamEvents.noteTokenSaved(server, owner, seq, at)
}

// EmitOAuthTokenSavedNumbered implements oauth.RefreshOrderEmitter: a saved
// token ends an open login requirement of the server.
func (r *Runtime) EmitOAuthTokenSavedNumbered(serverName string, seq uint64, at time.Time) {
	if r == nil || r.activityService == nil {
		return
	}
	r.activityService.NoteUpstreamTokenSaved(serverName, "", seq, at)
}

// EmitOAuthLoginRequiredNumbered implements oauth.RefreshOrderEmitter: the
// refresh schedule was given up (terminal rejection, retries exhausted, token
// expired too long), so the server needs a new login. The connection state
// does not change for this, which is why the state observer alone would miss
// it while the server is still connected.
func (r *Runtime) EmitOAuthLoginRequiredNumbered(serverName string, seq uint64, at time.Time) {
	if r == nil || r.activityService == nil {
		return
	}
	r.activityService.RecordUpstreamEvent(UpstreamEvent{
		Server: serverName,
		Kind:   UpstreamLoginRequired,
		// The time the failure was accepted, not the time this notification
		// was delivered: a token save and later calls can come in between.
		At:   at.UTC(),
		Code: string(diagnostics.OAuthReauthRequired),
		Seq:  seq,
	})
}

// installUpstreamEventObserver records connection loss, recovery, process
// exit and login requirements from the upstream state managers.
func (r *Runtime) installUpstreamEventObserver() {
	if r == nil || r.upstreamManager == nil || r.activityService == nil {
		return
	}
	r.upstreamManager.SetTransitionObserver(func(server, transport string, t types.Transition) {
		for _, ev := range transitionEvents(server, transport, t) {
			r.activityService.RecordUpstreamEvent(ev)
		}
	})
	// Every token refresh that was sent and failed, transient ones included.
	oauth.DefaultRefreshCoordinator().SetAuditObserver(func(f oauth.RefreshAttemptFailure) {
		r.activityService.RecordUpstreamEvent(UpstreamEvent{
			Server: f.ServerName,
			Kind:   UpstreamOAuthRefreshFailed,
			At:     f.At.UTC(),
			Code:   f.Code,
		})
	})
}
