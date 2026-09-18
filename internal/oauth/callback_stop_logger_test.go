package oauth

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// Spec 105 FR-007 (gap FR007-G4, research D8 "subject-bound for shared-service
// producers"): the callback manager serves every server, and each callback
// server records the logger of the server it belongs to at start
// (CallbackServer.logger, a tee into that server's per-server log file).
// StopCallbackServer(name) — the nil-logger path markOAuthComplete uses —
// resolved its logger through adoptLoggerLocked(nil), i.e. whichever server's
// logger was installed LAST, so a's tear-down record (a's name, bind_host,
// port, dropped waiters) landed in b's log and was readable by a b-scoped
// agent through tail_log. The stop record must be written through the
// stopped server's own recorded logger, in both start orders.

// newObservedManager builds a private CallbackServerManager (not the global
// one, so sibling tests cannot install a logger behind this test's back)
// plus one observer per server, mimicking the per-server upstream logger
// shape: every record is stamped `server=<name>`.
func newObservedManager(t *testing.T, names ...string) (*CallbackServerManager, map[string]*observer.ObservedLogs, map[string]*zap.Logger) {
	t.Helper()
	mgr := &CallbackServerManager{
		servers: make(map[string]*CallbackServer),
		logger:  zap.NewNop(),
	}
	observed := make(map[string]*observer.ObservedLogs, len(names))
	loggers := make(map[string]*zap.Logger, len(names))
	for _, name := range names {
		core, logs := observer.New(zap.DebugLevel)
		observed[name] = logs
		loggers[name] = zap.New(core).With(zap.String("server", name))
	}
	t.Cleanup(func() {
		for _, name := range names {
			_ = mgr.StopCallbackServer(name)
		}
	})
	return mgr, observed, loggers
}

// startObserved starts a dynamic-port callback server for name through the
// caller-logger path production uses (StartCallbackServerOnHost with
// CallbackBinding.Logger), parking one waiter so the tear-down has something
// to drop.
func startObserved(t *testing.T, mgr *CallbackServerManager, name string, logger *zap.Logger) *CallbackServer {
	t.Helper()
	cb, err := mgr.StartCallbackServerOnHost(name, CallbackBinding{Port: 0, Logger: logger})
	require.NoError(t, err)
	cb.RegisterState("state-" + name)
	return cb
}

// mentionsServer reports whether any record in logs carries `server=name`
// as a field or names port anywhere — the two things FR-007 forbids leaking
// into another server's log.
func mentionsServer(logs *observer.ObservedLogs, name string, port int) []string {
	var hits []string
	for _, entry := range logs.All() {
		for k, v := range entry.ContextMap() {
			if k == "server" && v == name {
				hits = append(hits, entry.Message+" server="+name)
			}
			if k == "port" && fmt.Sprint(v) == fmt.Sprint(port) {
				hits = append(hits, entry.Message+" port="+fmt.Sprint(port))
			}
		}
	}
	return hits
}

// assertStopRoutedToOwner stops `stopped` via the nil-logger path and asserts
// its tear-down records (stop + dropped waiter) landed only in its own
// observer, never in `other`'s.
func assertStopRoutedToOwner(t *testing.T, mgr *CallbackServerManager, observed map[string]*observer.ObservedLogs, stopped, other string, stoppedPort int) {
	t.Helper()
	require.NoError(t, mgr.StopCallbackServer(stopped))

	own := observed[stopped]
	foreign := observed[other]
	// The serve goroutine ALSO emits "OAuth callback server stopped" through
	// the server's own logger once Serve returns, and it races the manager's
	// record; wait for both so the count below is deterministic: goroutine
	// record + manager record = 2 in the owner's log, 0 anywhere else.
	require.Eventually(t, func() bool {
		return len(own.FilterMessage("OAuth callback server stopped").All()) >= 2
	}, 2*time.Second, 10*time.Millisecond,
		"%s's manager stop record must be written through %s's recorded logger (goroutine record + manager record)", stopped, stopped)
	assert.Len(t, own.FilterMessage("OAuth callback server stopped").All(), 2,
		"%s's manager stop record must be written through %s's recorded logger (goroutine record + manager record)", stopped, stopped)
	assert.Len(t, own.FilterMessage("Stopped OAuth callback server while flows were still waiting").All(), 1,
		"%s's dropped-waiter record must be written through %s's recorded logger", stopped, stopped)

	// The other server's observer keeps its OWN tear-down records from an
	// earlier round; only records about `stopped` are forbidden there.
	aboutStopped := foreign.FilterField(zap.String("server", stopped))
	assert.Empty(t, aboutStopped.FilterMessage("OAuth callback server stopped").All(),
		"%s's stop record landed in %s's log", stopped, other)
	assert.Empty(t, aboutStopped.FilterMessage("Stopped OAuth callback server while flows were still waiting").All(),
		"%s's dropped-waiter record landed in %s's log", stopped, other)
	assert.Empty(t, mentionsServer(foreign, stopped, stoppedPort),
		"%s's name/port written into %s's log", stopped, other)
}

// FR007-G4, order a then b: b's logger is the last installed, so on HEAD
// StopCallbackServer("a") logged through b's logger.
func TestCallbackStop_UsesRecordedServerLogger_ABOrder(t *testing.T) {
	mgr, observed, loggers := newObservedManager(t, "a", "b")

	a := startObserved(t, mgr, "a", loggers["a"])
	b := startObserved(t, mgr, "b", loggers["b"])

	assertStopRoutedToOwner(t, mgr, observed, "a", "b", a.Port)
	assertStopRoutedToOwner(t, mgr, observed, "b", "a", b.Port)
}

// FR007-G4, order b then a: a's logger is the last installed, so on HEAD
// StopCallbackServer("b") logged through a's logger.
func TestCallbackStop_UsesRecordedServerLogger_BAOrder(t *testing.T) {
	mgr, observed, loggers := newObservedManager(t, "a", "b")

	b := startObserved(t, mgr, "b", loggers["b"])
	a := startObserved(t, mgr, "a", loggers["a"])

	assertStopRoutedToOwner(t, mgr, observed, "b", "a", b.Port)
	assertStopRoutedToOwner(t, mgr, observed, "a", "b", a.Port)
}

// The explicit-logger stop path (StopCallbackServerWithLogger) is what the
// OAuth failure/cleanup paths use with the server's own logger; it must not
// regress to the manager logger either, and — subject-bound — must still not
// write into the other server's observer.
func TestCallbackStop_WithLogger_StillSubjectBound(t *testing.T) {
	mgr, observed, loggers := newObservedManager(t, "a", "b")

	a := startObserved(t, mgr, "a", loggers["a"])
	startObserved(t, mgr, "b", loggers["b"])

	require.NoError(t, mgr.StopCallbackServerWithLogger("a", loggers["a"]))
	assert.NotEmpty(t, observed["a"].FilterMessage("OAuth callback server stopped").All())
	assert.Empty(t, mentionsServer(observed["b"], "a", a.Port), "a's name/port written into b's log")
}

// Critique round 1, finding C2.5: the recorded server logger already carries
// server, bind_host and port as context fields (StartCallbackServerOnHost),
// so the stop and dropped-waiter records must not add them again — a JSON
// consumer keeps the last duplicate key, and the attributed reader's
// all-values-agree rule only tolerates the duplication. Exactly one
// bind_host and one port per manager stop record; `server` appears once from
// the recorded logger's With (the upstream tee's own stamp, which the
// observer fixture mimics, is a second, agreeing occurrence).
func TestCallbackStop_RecordFieldsNotDuplicated(t *testing.T) {
	mgr, observed, loggers := newObservedManager(t, "a")
	a := startObserved(t, mgr, "a", loggers["a"])

	require.NoError(t, mgr.StopCallbackServer("a"))

	own := observed["a"]
	require.Eventually(t, func() bool {
		return len(own.FilterMessage("OAuth callback server stopped").All()) >= 2
	}, 2*time.Second, 10*time.Millisecond)

	countKey := func(entry observer.LoggedEntry, key string) int {
		n := 0
		for _, f := range entry.Context {
			if f.Key == key {
				n++
			}
		}
		return n
	}
	for _, msg := range []string{"OAuth callback server stopped", "Stopped OAuth callback server while flows were still waiting"} {
		for _, entry := range own.FilterMessage(msg).All() {
			assert.Equal(t, 1, countKey(entry, "bind_host"), "%q: bind_host duplicated: %v", msg, entry.Context)
			assert.Equal(t, 1, countKey(entry, "port"), "%q: port duplicated: %v", msg, entry.Context)
			// fixture stamp (mimics the upstream tee) + recorded logger's With
			assert.Equal(t, 2, countKey(entry, "server"), "%q: server stamped more than by the two loggers: %v", msg, entry.Context)
			assert.Equal(t, int64(a.Port), entry.ContextMap()["port"], "%q must still name the port", msg)
			assert.Equal(t, a.BindHost, entry.ContextMap()["bind_host"], "%q must still name the bind host", msg)
		}
	}
}

// Codex round 2 (PR E), finding 2: the deprecated StartCallbackServer
// supplies no logger, and StartCallbackServerOnHost resolved a nil binding
// logger through adoptLoggerLocked(nil) — whichever server's logger was
// installed LAST. Starting b with loggerB and then a through the deprecated
// API recorded loggerB.With(server=a) as a's logger, so a's start records and
// its stop record (port, dropped waiters) were written into b's log. A
// server started without a logger of its own must record a subject-safe
// logger — never another server's — for its start AND stop records.
func TestCallbackStop_DeprecatedStartNeverAdoptsAnotherServersLogger(t *testing.T) {
	mgr, observed, loggers := newObservedManager(t, "a", "b")

	b := startObserved(t, mgr, "b", loggers["b"])

	a, err := mgr.StartCallbackServer("a", 0)
	require.NoError(t, err)
	a.RegisterState("state-a")

	assert.Empty(t, mentionsServer(observed["b"], "a", a.Port),
		"a's start records (deprecated no-logger API) written through b's logger")

	require.NoError(t, mgr.StopCallbackServer("a"))
	aboutA := observed["b"].FilterField(zap.String("server", "a"))
	assert.Empty(t, aboutA.All(), "a's records landed in b's log")
	assert.Empty(t, mentionsServer(observed["b"], "a", a.Port), "a's name/port written into b's log")

	// b's own tear-down still routes to b.
	assertStopRoutedToOwner(t, mgr, observed, "b", "a", b.Port)
}
