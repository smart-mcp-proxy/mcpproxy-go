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
func transitionEvent(server string, t types.Transition) (UpstreamEvent, bool) {
	ev := UpstreamEvent{Server: server, At: t.At.UTC()}

	code := t.TerminalCode
	if code == "" && t.Err != nil {
		code = string(diagnostics.Classify(t.Err, diagnostics.ClassifierHints{ServerID: server}))
	}
	if code == string(diagnostics.UnknownUnclassified) {
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
		if strings.HasPrefix(code, "MCPX_STDIO_EXIT") {
			ev.Kind = UpstreamProcessExited
		} else {
			ev.Kind = UpstreamConnectionLost
		}
		return ev, true
	}
	return UpstreamEvent{}, false
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
	// hourStart / hourRows count the rows a server added in the current hour.
	hourStart map[string]time.Time
	hourRows  map[string]int
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
		latest:    make(map[upstreamRowKey]*upstreamRow),
		hourStart: make(map[string]time.Time),
		hourRows:  make(map[string]int),
		down:      make(map[string]bool),
		loginOpen: make(map[upstreamOwnerKey]*upstreamRow),
		loginSeq:  make(map[upstreamOwnerKey]uint64),
	}
}

// place decides which row an event goes into. It returns the row and whether
// the row is new; a nil row means the event is not recorded at all.
func (c *upstreamEventCollapser) place(e UpstreamEvent) (row *upstreamRow, isNew bool) {
	key := upstreamRowKey{e.Server, e.Kind, e.Owner}
	owner := upstreamOwnerKey{e.Server, e.Owner}

	switch e.Kind {
	case UpstreamConnectionRestored:
		if !c.down[e.Server] {
			return nil, false // the first connect, or a recovery nobody saw fail
		}
		c.down[e.Server] = false
		// A reconnect ends every open login requirement of the server.
		for k := range c.loginOpen {
			if k.server == e.Server {
				delete(c.loginOpen, k)
			}
		}
	case UpstreamConnectionLost, UpstreamProcessExited:
		c.down[e.Server] = true
	case UpstreamLoginRequired:
		c.down[e.Server] = true
		stale := e.Seq != 0 && e.Seq < c.loginSeq[owner]
		if !stale {
			if e.Seq > c.loginSeq[owner] {
				c.loginSeq[owner] = e.Seq
			}
			if open := c.loginOpen[owner]; open != nil {
				// The same condition, seen again or from the other source.
				open.count++
				if e.At.After(open.lastAt) {
					open.lastAt = e.At
				}
				return open, false
			}
		}
		row, isNew = c.rowFor(key, e)
		if !stale {
			c.loginOpen[owner] = row
		}
		return row, isNew
	}
	return c.rowFor(key, e)
}

// rowFor applies the per-minute collapse and the hourly bound.
func (c *upstreamEventCollapser) rowFor(key upstreamRowKey, e UpstreamEvent) (*upstreamRow, bool) {
	hour := e.At.Truncate(time.Hour)
	if !c.hourStart[e.Server].Equal(hour) {
		c.hourStart[e.Server] = hour
		c.hourRows[e.Server] = 0
	}
	last := c.latest[key]
	merge := last != nil && (e.At.Sub(last.firstAt) < upstreamEventCollapseWindow ||
		c.hourRows[e.Server] >= MaxUpstreamEventRowsPerHour)
	if merge && !e.At.Before(last.firstAt) {
		last.count++
		if e.At.After(last.lastAt) {
			last.lastAt = e.At
		}
		return last, false
	}
	row := &upstreamRow{id: ulid.Make().String(), firstAt: e.At, lastAt: e.At, count: 1, code: e.Code}
	c.latest[key] = row
	c.hourRows[e.Server]++
	return row, true
}

// noteTokenSaved clears the open login requirement of (server, owner), unless
// the save is older than a failure already applied.
func (c *upstreamEventCollapser) noteTokenSaved(server, owner string, seq uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := upstreamOwnerKey{server, owner}
	if seq != 0 {
		if seq < c.loginSeq[k] {
			return
		}
		c.loginSeq[k] = seq
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
func (s *ActivityService) NoteUpstreamTokenSaved(server, owner string, seq uint64) {
	if s == nil || s.upstreamEvents == nil {
		return
	}
	s.upstreamEvents.noteTokenSaved(server, owner, seq)
}

// EmitOAuthTokenSavedNumbered implements oauth.RefreshOrderEmitter: a saved
// token ends an open login requirement of the server.
func (r *Runtime) EmitOAuthTokenSavedNumbered(serverName string, seq uint64) {
	if r == nil || r.activityService == nil {
		return
	}
	r.activityService.NoteUpstreamTokenSaved(serverName, "", seq)
}

// EmitOAuthLoginRequiredNumbered implements oauth.RefreshOrderEmitter: the
// refresh schedule was given up (terminal rejection, retries exhausted, token
// expired too long), so the server needs a new login. The connection state
// does not change for this, which is why the state observer alone would miss
// it while the server is still connected.
func (r *Runtime) EmitOAuthLoginRequiredNumbered(serverName string, seq uint64) {
	if r == nil || r.activityService == nil {
		return
	}
	r.activityService.RecordUpstreamEvent(UpstreamEvent{
		Server: serverName,
		Kind:   UpstreamLoginRequired,
		At:     time.Now().UTC(),
		Code:   string(diagnostics.OAuthReauthRequired),
		Seq:    seq,
	})
}

// installUpstreamEventObserver records connection loss, recovery, process
// exit and login requirements from the upstream state managers.
func (r *Runtime) installUpstreamEventObserver() {
	if r == nil || r.upstreamManager == nil || r.activityService == nil {
		return
	}
	r.upstreamManager.SetTransitionObserver(func(server string, t types.Transition) {
		if ev, ok := transitionEvent(server, t); ok {
			r.activityService.RecordUpstreamEvent(ev)
		}
	})
	// Every token refresh that was sent and failed, transient ones included.
	oauth.DefaultRefreshCoordinator().SetAuditObserver(func(f oauth.RefreshAttemptFailure) {
		r.activityService.RecordUpstreamEvent(UpstreamEvent{
			Server: f.ServerName,
			Kind:   UpstreamOAuthRefreshFailed,
			At:     f.At.UTC(),
		})
	})
}
