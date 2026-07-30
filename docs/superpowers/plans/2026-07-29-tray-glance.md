# Tray Glance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a compact glance section to the top of the macOS tray menu showing the five most recent tool calls, the active MCP clients, and a 24-hour calls-per-hour histogram behind a submenu.

**Architecture:** All text rows are plain `NSMenuItem`s — custom menu-item views receive mouse but not keyboard events, so only the histogram is a hosted SwiftUI view, and it lives in a submenu. Rendering reads `AppState` exclusively, so opening the menu performs no network I/O; three background feeds (an SSE activity path that consumes event payloads directly, a filtered activity poll, and a usage aggregate) keep that state fresh, and one small Go change adds the server-side `status` filter the client list needs to be truthful.

**Tech Stack:** Go 1.24 (backend, `internal/httpapi` + `internal/storage` + `internal/runtime`); Swift 5.9 / AppKit + SwiftUI with SwiftUI Charts (tray, SwiftPM package at `native/macos/MCPProxy`, no `.xcodeproj`); XCTest for the native suite.

**Design spec:** [`docs/superpowers/specs/2026-07-29-tray-glance-design.md`](../specs/2026-07-29-tray-glance-design.md)

## Global Constraints

Every task inherits these. They come from the design spec and the repo's own rules.

- **Menu open stays network-free.** Spec 048's invariant: `menuWillOpen` renders from in-memory state only. Opening the histogram submenu is also fetch-free — its data arrives on the background loop.
- **Never narrow a shared `AppState` feed.** `recentActivity` backs the Dashboard's full activity log and `recentSessions` backs session-name lookup in `ActivityView` plus the Dashboard's session list. The glance section gets its own `glanceActivity` / `glanceSessions` fields; it never repoints or filters the shared ones.
- **The menu does not restructure while it is open.** While tracking, rows update in place and structural changes are deferred to `menuDidClose`.
- **In-place row updates rewrite the whole row identity** — title, image, tooltip, accessibility label, and `representedObject` — never the title alone.
- **The tray holds no state.** It renders what the core gives it; display policy (selection rules, formatting) is fine, derived or persisted tray-local state is not.
- **UI strings are English**, matching the rest of the menu.
- **No new dependencies.** SwiftUI Charts ships with the platform (`Package.swift` declares `.macOS(.v13)`); nothing is added to `go.mod` or the Swift package.
- **Commit messages:** conventional-commit prefix, `Related #<issue>` (never `Fixes #`), and **no** `Co-Authored-By: Claude` line and no "Generated with Claude Code" footer.
- **Generated artifacts are never hand-edited.** `oas/swagger.yaml` and friends are regenerated; CI verifies they match.

---

## File Structure

**Go — the `status` filter (Task 1)**

| File | Responsibility |
|---|---|
| `internal/storage/manager.go` | `GetRecentSessions` gains a status argument applied *during* the cursor walk, before truncation |
| `internal/runtime/runtime.go` | pass-through of the new argument |
| `internal/server/server.go` | pass-through wrapper |
| `internal/httpapi/server.go` | parse and validate the `status` query param; `ServerController` interface update; swag annotations |
| `internal/httpapi/security_test.go`, `contracts_test.go` | mock controllers updated to the new signature |
| `oas/swagger.yaml` (+ generated siblings) | regenerated, never hand-edited |
| `docs/api/rest-api.md` | the `### Sessions` block documents the new parameter |

**Swift — data layer (Tasks 2–4)**

| File | Responsibility |
|---|---|
| `API/APIClient.swift` | three new calls (`activeSessions`, `glanceActivity`, `usageAggregate`) and the `last_activity` coding-key fix |
| `API/Models.swift` | usage-aggregate response models (`UsageBucket` et al.) |
| `Menu/Glance/GlanceDataSource.swift` | narrow protocol the glance component depends on, so a counting stub can be injected |
| `State/AppState.swift` | `glanceActivity`, `glanceSessions`, `usageTimeline`, `callsThisHour`, `usageError`; update helpers; `clearGlanceState()` |
| `Core/CoreProcessManager.swift` | wires the three fetches into the 30s loop; replaces the dead `case "activity"` with payload-consuming handlers; clears glance state on disconnect |
| `Menu/Glance/GlanceEvent.swift` | adapts an SSE payload envelope into an `ActivityEntry` (Unix seconds → `Date`, `internal_tool_name`/`target_server` mapping, `error_message`, composite provisional id) |

**Swift — presentation (Tasks 5–8)**

| File | Responsibility |
|---|---|
| `Menu/Glance/GlanceSelection.swift` | the ordered selection rules and the `request_id` collapse; active-session filtering |
| `Menu/Glance/GlanceFormatting.swift` | status icon, row label, middle truncation, relative time (salvaged from the deleted `TrayMenu.swift`) |
| `Menu/Glance/GlanceSection.swift` | builds the plain `NSMenuItem`s and the in-place update path |
| `Menu/Glance/MenuRebuildGuard.swift` | tracks whether the menu is open and whether a deferred rebuild is pending |
| `Menu/Glance/GlanceLinks.swift` | builds the authenticated Web UI activity URL for a session |
| `Menu/Glance/ActivityHistogramView.swift` | SwiftUI Charts stacked bar chart + its hosted menu item |
| `MCPProxyApp.swift` | inserts the section into `rebuildMenu()`, applies the tracking guard, adds the deep-link action |
| `Menu/TrayMenu.swift` | **deleted** (511 lines, dead) |

**Tests** live in `native/macos/MCPProxy/MCPProxyTests/`: `APIClientGlanceTests`, `AppStateGlanceTests`, `GlanceEventTests`, `GlanceSelectionTests`, `GlanceSelectionCollapseTests`, `GlanceFormattingTests`, `GlanceSectionTests`, `ActivityHistogramTests`, plus the `CountingGlanceDataSource` and `GlanceStubURLProtocol` helpers. Both SwiftPM targets use path-based globbing, so new files need no target registration.

---

### Task 1: Go — `status` filter on `GET /api/v1/sessions`

The macOS tray's "Clients" glance section needs the sessions that are **active right now**. Today storage walks the sessions bucket newest-first **by start time** and truncates to `limit` (`internal/storage/manager.go:1358`), and only that already-truncated page is re-sorted by last activity in the runtime (`internal/runtime/runtime.go:1340`). A client that connected hours ago but is calling tools this second therefore falls outside any page once enough newer sessions exist — the tray would render "No connected clients" while a client is working. Filtering client-side cannot fix that, because the record never leaves storage. This task pushes the filter down into the cursor walk, **before** truncation.

**Files:**

- Modify: `internal/storage/manager.go` (lines 1338–1372 — `(*Manager).GetRecentSessions`)
- Modify: `internal/runtime/runtime.go` (lines 1309–1314 — `(*Runtime).GetRecentSessions`)
- Modify: `internal/server/server.go` (lines 3075–3078 — the pass-through wrapper)
- Modify: `internal/httpapi/server.go` (lines 108–109 — the `ServerController` interface; lines 4824–4886 — `handleGetSessions` + its swag annotations)
- Modify: `internal/httpapi/security_test.go` (lines 316–318 — `baseController` mock)
- Modify: `internal/httpapi/contracts_test.go` (lines 172–174 — `MockServerController` mock)
- Modify: `docs/api/rest-api.md` (lines 926–934 — the `### Sessions` block)
- Modify (regenerated, never hand-edited): `oas/swagger.yaml`, `oas/docs.go`
- Test: `internal/storage/sessions_filter_test.go` (create)
- Test: `internal/httpapi/sessions_handlers_test.go` (create)

**Interfaces:**

*Consumes:* nothing. This is the first task and depends on no earlier task.

*Produces:*

- HTTP contract — `GET /api/v1/sessions?status=active&limit=25` (auth: `X-API-Key` header) returns `200` with the existing envelope:
  `{"success":true,"data":{"sessions":[…],"total":N,"limit":25,"offset":0}}`.
  `status` accepts exactly `active` or `closed`; omitting it means no filter. Any other value returns `400` with `{"success":false,"error":"Invalid status. Use 'active' or 'closed'"}`. When `status` is set, `total` counts the **matching** sessions, not every stored session.
- `func (m *storage.Manager) GetRecentSessions(limit int, status string) ([]*storage.SessionRecord, int, error)`
- `func (r *runtime.Runtime) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error)`
- `func (s *server.Server) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error)`
- `httpapi.ServerController` method `GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error)`
- Session JSON field names, unchanged and pinned by this task's tests (`internal/contracts/types.go:234-255`): `id`, `client_name`, `client_version`, `status`, `start_time`, `end_time`, `last_activity`, `tool_call_count`, `total_tokens`, `has_roots`, `has_sampling`, `experimental`, `workspace_name`, `work_session_id`. Note the activity timestamp key is **`last_activity`**.

---

- [ ] **Step 1: Write the failing storage test.**

Create `internal/storage/sessions_filter_test.go` with exactly this content. It reuses `setupTestStorageForActivity` (already defined in `internal/storage/activity_test.go:13`, same package), so no new helper is needed.

```go
package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A session that started long ago but is still active must survive a small
// page. Storage walks newest-first by START time and truncates to `limit`, so
// filtering on the client after truncation would drop it entirely — the tray's
// "Clients" section would then say "No connected clients" while a client is
// actively calling tools.
func TestGetRecentSessions_StatusFilterAppliedBeforeTruncation(t *testing.T) {
	manager, cleanup := setupTestStorageForActivity(t)
	defer cleanup()

	base := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)

	// Oldest session is the active one.
	require.NoError(t, manager.CreateSession(&SessionRecord{
		ID:           "session-old-active",
		ClientName:   "claude-code",
		Status:       "active",
		StartTime:    base,
		LastActivity: base.Add(90 * time.Minute),
	}))

	// Four newer sessions, all closed.
	for i := 1; i <= 4; i++ {
		require.NoError(t, manager.CreateSession(&SessionRecord{
			ID:           "session-closed-" + string(rune('a'+i-1)),
			ClientName:   "cursor",
			Status:       "closed",
			StartTime:    base.Add(time.Duration(i) * time.Minute),
			LastActivity: base.Add(time.Duration(i) * time.Minute),
		}))
	}

	t.Run("unfiltered page of 2 misses the active session", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "")
		require.NoError(t, err)
		assert.Equal(t, 5, total, "unfiltered total is the whole bucket")
		require.Len(t, sessions, 2)
		for _, s := range sessions {
			assert.Equal(t, "closed", s.Status)
		}
	})

	t.Run("status=active returns the old active session despite the page size", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(2, "active")
		require.NoError(t, err)
		assert.Equal(t, 1, total, "total counts matching records, not the bucket")
		require.Len(t, sessions, 1)
		assert.Equal(t, "session-old-active", sessions[0].ID)
	})

	t.Run("status=closed returns only closed sessions", func(t *testing.T) {
		sessions, total, err := manager.GetRecentSessions(10, "closed")
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		require.Len(t, sessions, 4)
		for _, s := range sessions {
			assert.Equal(t, "closed", s.Status)
		}
	})
}
```

- [ ] **Step 2: Run the storage test and watch it fail to compile.**

```bash
cd /Users/user/repos/mcpproxy-go
go test ./internal/storage/ -run TestGetRecentSessions_StatusFilterAppliedBeforeTruncation -count=1
```

Expected output (the method takes one argument today):

```
# github.com/smart-mcp-proxy/mcpproxy-go/internal/storage [github.com/smart-mcp-proxy/mcpproxy-go/internal/storage.test]
internal/storage/sessions_filter_test.go:43:56: too many arguments in call to manager.GetRecentSessions
	have (number, string)
	want (int)
internal/storage/sessions_filter_test.go:53:56: too many arguments in call to manager.GetRecentSessions
	have (number, string)
	want (int)
internal/storage/sessions_filter_test.go:61:57: too many arguments in call to manager.GetRecentSessions
	have (number, string)
	want (int)
FAIL	github.com/smart-mcp-proxy/mcpproxy-go/internal/storage [build failed]
FAIL
```

- [ ] **Step 3: Implement the filter inside the storage cursor walk.**

In `internal/storage/manager.go`, replace the whole existing block starting at line 1338 (`// GetRecentSessions returns the most recent sessions`) and ending at the closing `})` of the `View` callback — i.e. replace this exact text:

```go
// GetRecentSessions returns the most recent sessions
func (m *Manager) GetRecentSessions(limit int) ([]*SessionRecord, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*SessionRecord
	var total int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions yet
		}

		// Count total
		total = bucket.Stats().KeyN

		// Iterate in reverse (newest first due to timestamp key prefix)
		c := bucket.Cursor()
		count := 0
		for k, v := c.Last(); k != nil && count < limit; k, v = c.Prev() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				m.logger.Warnw("Failed to unmarshal session", "error", err)
				continue
			}
			sessions = append(sessions, &session)
			count++
		}

		return nil
	})
```

with:

```go
// GetRecentSessions returns the most recent sessions.
//
// status filters on SessionRecord.Status ("active" / "closed"); an empty string
// means no filtering. The filter is applied DURING the cursor walk, before the
// limit truncates the result, so a session that started long ago but is still
// active is never dropped by a page full of newer sessions. Filtering after
// truncation would let the tray report "no connected clients" while a client
// is actively calling tools.
func (m *Manager) GetRecentSessions(limit int, status string) ([]*SessionRecord, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sessions []*SessionRecord
	var total int

	err := m.db.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(SessionsBucket))
		if bucket == nil {
			return nil // No sessions yet
		}

		if status == "" {
			// Unfiltered: total is the whole bucket and the walk can stop early.
			total = bucket.Stats().KeyN

			// Iterate in reverse (newest first due to timestamp key prefix)
			c := bucket.Cursor()
			count := 0
			for k, v := c.Last(); k != nil && count < limit; k, v = c.Prev() {
				var session SessionRecord
				if err := json.Unmarshal(v, &session); err != nil {
					m.logger.Warnw("Failed to unmarshal session", "error", err)
					continue
				}
				sessions = append(sessions, &session)
				count++
			}

			return nil
		}

		// Filtered: walk the whole bucket so `total` honestly counts matching
		// records. Session retention caps this bucket at 100 keys
		// (enforceSessionRetention), so a full walk is bounded and cheap.
		c := bucket.Cursor()
		for k, v := c.Last(); k != nil; k, v = c.Prev() {
			var session SessionRecord
			if err := json.Unmarshal(v, &session); err != nil {
				m.logger.Warnw("Failed to unmarshal session", "error", err)
				continue
			}
			if session.Status != status {
				continue
			}
			total++
			if len(sessions) < limit {
				sessions = append(sessions, &session)
			}
		}

		return nil
	})
```

Leave the trailing `return sessions, total, err` and closing brace untouched. (`var session SessionRecord` is declared inside the loop body, so `&session` is a fresh pointer each iteration — do not hoist it.)

- [ ] **Step 4: Run the storage test and watch it pass.**

```bash
cd /Users/user/repos/mcpproxy-go
go test ./internal/storage/ -run TestGetRecentSessions_StatusFilterAppliedBeforeTruncation -count=1 -v
```

Expected output:

```
=== RUN   TestGetRecentSessions_StatusFilterAppliedBeforeTruncation
=== RUN   TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/unfiltered_page_of_2_misses_the_active_session
=== RUN   TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/status=active_returns_the_old_active_session_despite_the_page_size
=== RUN   TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/status=closed_returns_only_closed_sessions
--- PASS: TestGetRecentSessions_StatusFilterAppliedBeforeTruncation (0.09s)
    --- PASS: TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/unfiltered_page_of_2_misses_the_active_session (0.00s)
    --- PASS: TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/status=active_returns_the_old_active_session_despite_the_page_size (0.00s)
    --- PASS: TestGetRecentSessions_StatusFilterAppliedBeforeTruncation/status=closed_returns_only_closed_sessions (0.00s)
PASS
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/storage	0.422s
```

- [ ] **Step 5: See exactly which call sites the new signature broke.**

```bash
cd /Users/user/repos/mcpproxy-go
go build ./...
```

Expected output:

```
# github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime
internal/runtime/runtime.go:1314:67: not enough arguments in call to r.storageManager.GetRecentSessions
	have (int)
	want (int, string)
```

(`go build` does not compile `_test.go` files, so the two httpapi mocks stay silent until Step 7.)

- [ ] **Step 6: Thread `status` through the runtime and the server wrapper.**

In `internal/runtime/runtime.go`, replace this exact text (line 1309 onward):

```go
// GetRecentSessions returns recent MCP sessions
func (r *Runtime) GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	storageRecords, total, err := r.storageManager.GetRecentSessions(limit)
```

with:

```go
// GetRecentSessions returns recent MCP sessions.
//
// status filters on the session status ("active" / "closed"); an empty string
// means no filtering. The filter is pushed down into the storage cursor walk so
// it is applied before truncation to limit.
func (r *Runtime) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	storageRecords, total, err := r.storageManager.GetRecentSessions(limit, status)
```

Then in `internal/server/server.go`, replace this exact text (line 3075 onward):

```go
// GetRecentSessions retrieves recent MCP sessions
func (s *Server) GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error) {
	return s.runtime.GetRecentSessions(limit)
}
```

with:

```go
// GetRecentSessions retrieves recent MCP sessions, optionally filtered by
// status ("active" / "closed"; empty means no filter).
func (s *Server) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
	return s.runtime.GetRecentSessions(limit, status)
}
```

- [ ] **Step 7: Widen the `ServerController` interface and its two test mocks.**

In `internal/httpapi/server.go`, replace this exact text (line 108):

```go
	// Session management
	GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error)
```

with:

```go
	// Session management. status filters on session status ("active" /
	// "closed"); an empty string means no filter.
	GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error)
```

Still in `internal/httpapi/server.go`, keep the handler compiling by passing an explicit empty filter for now — replace (line 4863):

```go
	sessions, total, err := s.controller.GetRecentSessions(limit)
```

with:

```go
	sessions, total, err := s.controller.GetRecentSessions(limit, "")
```

In `internal/httpapi/security_test.go` (line 316) replace:

```go
func (m *baseController) GetRecentSessions(limit int) ([]*contracts.MCPSession, int, error) {
```

with:

```go
func (m *baseController) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
```

In `internal/httpapi/contracts_test.go` (line 172) replace:

```go
func (m *MockServerController) GetRecentSessions(_ int) ([]*contracts.MCPSession, int, error) {
```

with:

```go
func (m *MockServerController) GetRecentSessions(_ int, _ string) ([]*contracts.MCPSession, int, error) {
```

Do **not** touch `internal/httpapi/code_exec_test.go:113` — that `mockController` is only ever passed to `httpapi.NewCodeExecHandler`, which takes a narrower interface, so its `GetRecentSessions(limit int) (interface{}, int, error)` stub is unrelated and still compiles. These four (`server.Server` plus the three test mocks) are the only `GetRecentSessions` implementations in the repo, including under the `server` build tag.

- [ ] **Step 8: Confirm both editions build clean.**

```bash
cd /Users/user/repos/mcpproxy-go
go build ./... && go build -tags server ./... && echo BUILD_OK
```

Expected output (a single line; anything else means a call site was missed):

```
BUILD_OK
```

- [ ] **Step 9: Write the failing handler test.**

Create `internal/httpapi/sessions_handlers_test.go` with exactly this content. It follows the house pattern from `internal/httpapi/activity_handlers_test.go:24` — a mock embedding `baseController` and overriding `GetCurrentConfig() any`, a real `Server` built with `NewServer(ctrl, logger, nil)`, driven through `srv.ServeHTTP` with the `X-API-Key` header matching the mock's `GetCurrentConfig()`.

```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// mockSessionController records the status argument the handler pushes down, so
// the tests can prove the filter reaches storage rather than being applied (or
// dropped) in the handler.
type mockSessionController struct {
	baseController
	apiKey    string
	sessions  []*contracts.MCPSession
	gotLimit  int
	gotStatus string
	callCount int
}

func (m *mockSessionController) GetCurrentConfig() any {
	return &config.Config{APIKey: m.apiKey}
}

func (m *mockSessionController) GetRecentSessions(limit int, status string) ([]*contracts.MCPSession, int, error) {
	m.callCount++
	m.gotLimit = limit
	m.gotStatus = status

	var out []*contracts.MCPSession
	for _, s := range m.sessions {
		if status == "" || s.Status == status {
			out = append(out, s)
		}
	}
	return out, len(out), nil
}

func testSessions() []*contracts.MCPSession {
	start := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	return []*contracts.MCPSession{
		{ID: "sess-active", ClientName: "claude-code", Status: "active", StartTime: start, LastActivity: start.Add(time.Hour)},
		{ID: "sess-closed", ClientName: "cursor", Status: "closed", StartTime: start.Add(time.Minute), LastActivity: start.Add(2 * time.Minute)},
	}
}

func decodeSessionsResponse(t *testing.T, w *httptest.ResponseRecorder) contracts.GetSessionsResponse {
	t.Helper()
	var resp struct {
		Success bool                          `json:"success"`
		Data    contracts.GetSessionsResponse `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.True(t, resp.Success)
	return resp.Data
}

func TestGetSessions_StatusFilter(t *testing.T) {
	logger := zap.NewNop().Sugar()

	t.Run("status=active is pushed down and narrows the result", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=active&limit=25", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "active", ctrl.gotStatus, "the status filter must reach the controller, not be applied in the handler")
		assert.Equal(t, 25, ctrl.gotLimit)

		data := decodeSessionsResponse(t, w)
		require.Len(t, data.Sessions, 1)
		assert.Equal(t, "sess-active", data.Sessions[0].ID)
		assert.Equal(t, 1, data.Total)
		assert.Equal(t, 25, data.Limit)
	})

	t.Run("no status parameter means no filter", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "", ctrl.gotStatus)
		assert.Equal(t, 10, ctrl.gotLimit, "default limit for sessions is 10")

		data := decodeSessionsResponse(t, w)
		assert.Len(t, data.Sessions, 2)
	})

	t.Run("status=closed selects closed sessions", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=closed", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		data := decodeSessionsResponse(t, w)
		require.Len(t, data.Sessions, 1)
		assert.Equal(t, "sess-closed", data.Sessions[0].ID)
	})

	t.Run("unknown status is rejected and never reaches the controller", func(t *testing.T) {
		ctrl := &mockSessionController{apiKey: "test-key", sessions: testSessions()}
		srv := NewServer(ctrl, logger, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?status=bogus", nil)
		req.Header.Set("X-API-Key", "test-key")
		w := httptest.NewRecorder()

		srv.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Equal(t, 0, ctrl.callCount, "a rejected filter must not fall through to an unfiltered query")

		var errResp contracts.ErrorResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&errResp))
		assert.Contains(t, errResp.Error, "Invalid status")
	})
}
```

- [ ] **Step 10: Run the handler test and watch it fail.**

```bash
cd /Users/user/repos/mcpproxy-go
go test ./internal/httpapi/ -run TestGetSessions_StatusFilter -count=1
```

Expected output (three of the four sub-tests fail; the handler still hardcodes `""` and never validates). testify's `Error Trace:` / `Test:` / `Diff:` lines are elided below for brevity — what matters is *which* assertions fail and where:

```
--- FAIL: TestGetSessions_StatusFilter (0.00s)
    --- FAIL: TestGetSessions_StatusFilter/status=active_is_pushed_down_and_narrows_the_result (0.00s)
        sessions_handlers_test.go:81:
            	Error:      	Not equal:
            	            	expected: "active"
            	            	actual  : ""
            	Messages:   	the status filter must reach the controller, not be applied in the handler
        sessions_handlers_test.go:85:
            	Error:      	"[{sess-active …} {sess-closed …}]" should have 1 item(s), but has 2
    --- FAIL: TestGetSessions_StatusFilter/status=closed_selects_closed_sessions (0.00s)
        sessions_handlers_test.go:121:
            	Error:      	"[{sess-active …} {sess-closed …}]" should have 1 item(s), but has 2
    --- FAIL: TestGetSessions_StatusFilter/unknown_status_is_rejected_and_never_reaches_the_controller (0.00s)
        sessions_handlers_test.go:135:
            	Error:      	Not equal:
            	            	expected: 400
            	            	actual  : 200
        sessions_handlers_test.go:136:
            	Error:      	Not equal:
            	            	expected: 0
            	            	actual  : 1
            	Messages:   	a rejected filter must not fall through to an unfiltered query
        sessions_handlers_test.go:140:
            	Error:      	"" does not contain "Invalid status"
FAIL
FAIL	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	0.515s
FAIL
```

- [ ] **Step 11: Parse and validate `status` in the handler.**

In `internal/httpapi/server.go`, inside `handleGetSessions`, replace this exact text:

```go
	offset := 0
	if offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Get recent sessions from controller
	sessions, total, err := s.controller.GetRecentSessions(limit, "")
```

with:

```go
	offset := 0
	if offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	// Status filter. The domain is closed ("active" / "closed"), so an unknown
	// value is a client bug — rejecting it is honest, where silently ignoring it
	// would return unfiltered sessions that the caller believes are filtered.
	status := r.URL.Query().Get("status")
	if status != "" && status != "active" && status != "closed" {
		s.writeError(w, r, http.StatusBadRequest, "Invalid status. Use 'active' or 'closed'")
		return
	}

	// Get recent sessions from controller
	sessions, total, err := s.controller.GetRecentSessions(limit, status)
```

(`limit` keeps its existing lenient parsing — out-of-range values silently fall back to 10. Only `status` is strict, because unlike a clamped number a wrong enum silently changes *which* records come back.)

- [ ] **Step 12: Add the swag annotations for the new parameter and the new failure mode.**

Still in `internal/httpapi/server.go`, in the `// handleGetSessions godoc` comment block above the function, replace this exact text:

```go
// @Param        limit   query     int                               false  "Maximum number of sessions to return (1-100, default 10)"
// @Param        offset  query     int                               false  "Number of sessions to skip for pagination (default 0)"
// @Success      200     {object}  contracts.GetSessionsResponse     "Sessions retrieved successfully"
// @Failure      401     {object}  contracts.ErrorResponse           "Unauthorized - missing or invalid API key"
```

with:

```go
// @Param        limit   query     int                               false  "Maximum number of sessions to return (1-100, default 10)"
// @Param        offset  query     int                               false  "Number of sessions to skip for pagination (default 0)"
// @Param        status  query     string                            false  "Filter by session status"  Enums(active, closed)
// @Success      200     {object}  contracts.GetSessionsResponse     "Sessions retrieved successfully"
// @Failure      400     {object}  contracts.ErrorResponse           "Invalid status filter"
// @Failure      401     {object}  contracts.ErrorResponse           "Unauthorized - missing or invalid API key"
```

- [ ] **Step 13: Run the handler test and watch it pass.**

```bash
cd /Users/user/repos/mcpproxy-go
go test ./internal/httpapi/ -run TestGetSessions_StatusFilter -count=1 -v
```

Expected output:

```
=== RUN   TestGetSessions_StatusFilter
=== RUN   TestGetSessions_StatusFilter/status=active_is_pushed_down_and_narrows_the_result
=== RUN   TestGetSessions_StatusFilter/no_status_parameter_means_no_filter
=== RUN   TestGetSessions_StatusFilter/status=closed_selects_closed_sessions
=== RUN   TestGetSessions_StatusFilter/unknown_status_is_rejected_and_never_reaches_the_controller
--- PASS: TestGetSessions_StatusFilter (0.00s)
    --- PASS: TestGetSessions_StatusFilter/status=active_is_pushed_down_and_narrows_the_result (0.00s)
    --- PASS: TestGetSessions_StatusFilter/no_status_parameter_means_no_filter (0.00s)
    --- PASS: TestGetSessions_StatusFilter/status=closed_selects_closed_sessions (0.00s)
    --- PASS: TestGetSessions_StatusFilter/unknown_status_is_rejected_and_never_reaches_the_controller (0.00s)
PASS
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	0.468s
```

- [ ] **Step 14: Regenerate the OpenAPI artifacts.**

CI job `verify-oas` (in both `.github/workflows/unit-tests.yml` and `.github/workflows/pr-build.yml`) runs `scripts/verify-oas.sh`, which reruns `make swagger` and fails the build if `oas/swagger.yaml` or `oas/docs.go` come out dirty, so they must be regenerated and committed. `make swagger` expects the binary at `$HOME/go/bin/swag`; if it is missing, install it first with `go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc4`.

```bash
cd /Users/user/repos/mcpproxy-go
make swagger
git diff --stat -- oas/
```

Expected output (tail of `make swagger`, then the diffstat):

```
create docs.go at oas/docs.go
create swagger.yaml at oas/swagger.yaml
✅ OpenAPI 3.1 spec generated: oas/swagger.yaml and oas/docs.go
 oas/docs.go      |  2 +-
 oas/swagger.yaml | 14 ++++++++++++++
 2 files changed, 15 insertions(+), 1 deletion(-)
```

(`oas/docs.go` embeds the whole spec on a single line, so a one-line change there is expected.)

- [ ] **Step 15: Eyeball the generated spec diff.**

```bash
cd /Users/user/repos/mcpproxy-go
git diff -- oas/swagger.yaml
```

Expected output:

```
@@ -6027,6 +6027,14 @@ paths:
         name: offset
         schema:
           type: integer
+      - description: Filter by session status
+        in: query
+        name: status
+        schema:
+          enum:
+          - active
+          - closed
+          type: string
       responses:
         "200":
@@ -6034,6 +6042,12 @@ paths:
               schema:
                 $ref: '#/components/schemas/contracts.GetSessionsResponse'
           description: Sessions retrieved successfully
+        "400":
+          content:
+            application/json:
+              schema:
+                $ref: '#/components/schemas/contracts.ErrorResponse'
+          description: Invalid status filter
         "401":
```

If the diff touches any other path, a stray edit crept in — revert `oas/` and rerun `make swagger` on a clean tree.

- [ ] **Step 16: Document the parameter in the REST API reference.**

In `docs/api/rest-api.md` (the `### Sessions` block, lines 926–934), replace this exact text:

```markdown
#### GET /api/v1/sessions

List active MCP sessions.

#### GET /api/v1/sessions/{id}
```

with:

```markdown
#### GET /api/v1/sessions

List recent MCP sessions.

**Query Parameters:**

| Parameter | Type | Description |
|-----------|------|-------------|
| `limit` | integer | Max sessions (1-100, default: 10) |
| `offset` | integer | Pagination offset (default: 0) |
| `status` | string | Filter by session status: `active`, `closed`. Any other value returns `400`. |

The `status` filter is applied during the storage walk, **before** the `limit`
truncation, so a long-running session that is still active is returned even when
newer sessions would otherwise fill the page. When `status` is set, `total`
counts the matching sessions rather than every stored session.

Caveat (spec 082): handshake-only sessions are not persisted, so a connected but
idle client does not appear until its first tool call.

#### GET /api/v1/sessions/{id}
```

- [ ] **Step 17: Run the full gate for the two touched packages.**

Check formatting on the files this task touched, then run the packages. Do **not** run `gofmt -l internal/` — roughly 29 files in this repo are already unformatted on a clean tree (`internal/tui/model.go`, `internal/tui/styles.go`, `internal/oauth/discovery.go`, `internal/oauth/refresh_manager.go`, `internal/security/pattern.go`, `internal/httpapi/swagger.go`, `internal/httpapi/activity_handlers_test.go`, `internal/configimport/cursor.go`, …), so a package-wide listing is pre-existing noise, not a signal about this change.

```bash
cd /Users/user/repos/mcpproxy-go
gofmt -l internal/storage/manager.go internal/storage/sessions_filter_test.go \
         internal/runtime/runtime.go internal/server/server.go \
         internal/httpapi/server.go internal/httpapi/sessions_handlers_test.go \
         internal/httpapi/security_test.go internal/httpapi/contracts_test.go
go test ./internal/httpapi/ ./internal/storage/ -count=1
```

Expected output (`gofmt -l` prints nothing for the touched files):

```
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/httpapi	0.738s
ok  	github.com/smart-mcp-proxy/mcpproxy-go/internal/storage	6.583s
```

- [ ] **Step 18: Run the strict CI linter.**

CI uses golangci-lint **v2** with `.github/.golangci.yml`, which is stricter than the local `scripts/run-linter.sh`. This takes ~2–4 minutes on a cold cache.

```bash
cd /Users/user/repos/mcpproxy-go
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./internal/httpapi/... ./internal/storage/... ./internal/runtime/... ./internal/server/...
```

Expected output:

```
0 issues.
```

- [ ] **Step 19: Commit.**

```bash
cd /Users/user/repos/mcpproxy-go
git add internal/storage/manager.go internal/storage/sessions_filter_test.go \
        internal/runtime/runtime.go internal/server/server.go \
        internal/httpapi/server.go internal/httpapi/sessions_handlers_test.go \
        internal/httpapi/security_test.go internal/httpapi/contracts_test.go \
        oas/swagger.yaml oas/docs.go docs/api/rest-api.md
git commit -m "$(cat <<'EOF'
feat(api): add status filter to GET /api/v1/sessions

Storage walked the sessions bucket newest-first by START time and truncated
to limit before anyone could filter, so a session that began hours ago but is
still active fell outside every page once enough newer sessions existed. The
macOS tray's client glance would then show "No connected clients" while a
client was actively calling tools, and offset cannot recover it (the handler
parses offset but never passes it down).

Push the filter into the cursor walk so it runs before truncation:

- storage.Manager.GetRecentSessions(limit, status) filters on SessionRecord
  .Status during the walk. When filtering, it walks the whole bucket (capped
  at 100 keys by session retention) so `total` counts matching records.
- runtime.Runtime / server.Server / httpapi.ServerController thread status
  through unchanged.
- The handler validates status against the closed domain (active|closed) and
  returns 400 on anything else rather than silently returning unfiltered
  sessions the caller believes are filtered.

Regenerates the OpenAPI artifacts and documents the parameter.
EOF
)"
```

Verify the commit landed and that the OAS artifacts are now considered up to date:

```bash
cd /Users/user/repos/mcpproxy-go
git show --stat --oneline HEAD | head -15
make swagger-verify 2>&1 | tail -2
```

Expected output — 11 files in the commit, and:

```
✅ OpenAPI 3.1 spec generated: oas/swagger.yaml and oas/docs.go
✅ OpenAPI artifacts are up to date.
```

---

### Task 2: Swift — API client methods, models, and the glance data-source protocol

**Files:**
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceDataSource.swift`
- Create: `native/macos/MCPProxy/MCPProxyTests/GlanceStubURLProtocol.swift`
- Create: `native/macos/MCPProxy/MCPProxyTests/CountingGlanceDataSource.swift`
- Create: `native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift`
- Modify: `native/macos/MCPProxy/MCPProxy/API/Models.swift` (append after line 1056, end of file)
- Modify: `native/macos/MCPProxy/MCPProxy/API/APIClient.swift` (lines 341, 353 — `MCPSession` field + coding key; insert after line 368 — `activeSessions`; insert before line 378 — `glanceActivity` + `usageAggregate`)
- Modify: `native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift` (lines 481, 482, 494, 495, 553 — `.lastActive` → `.lastActivity`)
- Delete (progressively, Steps 0/9/14): `native/macos/MCPProxy/MCPProxy/API/UsageStub.swift` — the untracked scratch placeholder this task supersedes
- Test: `native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift`

> **No `Package.swift` edit is needed.** `native/macos/MCPProxy/Package.swift` declares both targets with `path:`-based globbing (`path: "MCPProxy"` and `path: "MCPProxyTests"`), so dropping a new `.swift` file into any subdirectory of those paths is enough for `swift test` to pick it up.

> **Working-tree note.** This task lands on top of an uncommitted spike. `MCPProxy/API/UsageStub.swift` (untracked) already declares `UsageBucket`, `UsageAggregateResponse` **and** an `extension APIClient` carrying all three methods below; `MCPProxy/Core/CoreProcessManager.swift` (lines 875, 888, 900) and `MCPProxy/State/AppState.swift` (lines 102, 308, 334) call into them. Step 0 retires that placeholder one shim at a time so that every "watch it fail" step has a real red state and every "watch it pass" step actually compiles. If your checkout has no `UsageStub.swift` and no spike wiring, Step 0 is a no-op and the red states are the test-target compile errors noted in each step.

**Interfaces:**

*Consumes (from Task 1):*
- `GET /api/v1/sessions?status=active&limit=25` — the Go handler + storage cursor filter that returns only sessions whose `Status == "active"`. Response envelope is unchanged: `{"success":true,"data":{"sessions":[…],"total":N,"limit":N,"offset":N}}`.
- Nothing in this task *compiles* against Task 1 — the Swift tests drive a stubbed `URLProtocol`, so Task 2 can be implemented and merged before or after Task 1.

*Produces (relied on by later tasks):*
```swift
// native/macos/MCPProxy/MCPProxy/API/Models.swift
struct UsageBucket: Codable, Equatable {
    let start: Date          // UTC-hour aligned
    let calls: Int           // INCLUDES `errors`
    let errors: Int
    let totalRespBytes: Int
}
extension UsageBucket {
    static func parseRFC3339(_ value: String) -> Date?
    static func rfc3339String(from date: Date) -> String
}
struct UsageAggregateResponse: Codable, Equatable {
    let window: String
    let tokenSource: String?
    let tokensSaved: Int?
    let tokensSavedPercentage: Double?
    let timeline: [UsageBucket]
}

// native/macos/MCPProxy/MCPProxy/API/APIClient.swift  (all actor-isolated → call sites need `await`)
func usageAggregate(window: String = "24h", top: Int = 1) async throws -> UsageAggregateResponse
func glanceActivity(limit: Int = 50) async throws -> [ActivityEntry]
func activeSessions(limit: Int = 25) async throws -> [MCPSession]
// APIClient.MCPSession gains `let lastActivity: String?` (replaces `lastActive`)

// native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceDataSource.swift
protocol GlanceDataSource {
    func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse
    func glanceActivity(limit: Int) async throws -> [ActivityEntry]
    func activeSessions(limit: Int) async throws -> [APIClient.MCPSession]
}
extension APIClient: GlanceDataSource {}

// native/macos/MCPProxy/MCPProxyTests/CountingGlanceDataSource.swift  (test target only)
final class CountingGlanceDataSource: GlanceDataSource {
    private(set) var usageCallCount: Int
    private(set) var activityCallCount: Int
    private(set) var sessionCallCount: Int
    var totalCallCount: Int
    var usageToReturn: UsageAggregateResponse
    var activityToReturn: [ActivityEntry]
    var sessionsToReturn: [APIClient.MCPSession]
}

// native/macos/MCPProxy/MCPProxyTests/GlanceStubURLProtocol.swift  (test target only)
final class GlanceStubURLProtocol: URLProtocol {
    static var requestedURLs: [String]
    static var responseBody: Data
    static var statusCode: Int
    static func reset()
    static func makeClient() -> APIClient
    static func envelope(_ json: String) -> Data
}
```

---

- [ ] **Step 0: Retire the scratch placeholder down to the two shims this task has not replaced yet.**

  First confirm what is actually in the tree:

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && \
  cat MCPProxy/API/UsageStub.swift && \
  grep -n "usageAggregate\|glanceActivity(limit\|activeSessions(limit\|\[UsageBucket\]" \
       MCPProxy/Core/CoreProcessManager.swift MCPProxy/State/AppState.swift
  ```

  Expect `UsageStub.swift` to declare `UsageBucket`, `UsageAggregateResponse` and an `extension APIClient` with all three methods, and the grep to report `CoreProcessManager.swift:875/888/900` plus `AppState.swift:102/308/334`. Those spike call sites are why the placeholder cannot simply be deleted in one go: `glanceActivity` is not reinstated until Step 10 and `activeSessions` not until Step 16, so a wholesale `rm` here would leave the `MCPProxy` module uncompilable through Steps 6 and 11.

  Replace the file with just the shims Task 2 has not reached yet — dropping the two type declarations and the `usageAggregate` shim, which Steps 4 and 5 re-create:

  ```swift
  // UsageStub.swift
  // MCPProxy
  //
  // TRANSITIONAL — deleted by Step 14. Holds the glance API shims that Task 2 has
  // not implemented yet so the CoreProcessManager wiring keeps compiling between
  // TDD cycles. Each shim is removed in the step that replaces it for real.

  import Foundation

  extension APIClient {
      func glanceActivity(limit: Int = 50) async throws -> [ActivityEntry] { _ = limit; return [] }
      func activeSessions(limit: Int = 25) async throws -> [MCPSession] { _ = limit; return [] }
  }
  ```

  The file is untracked, so none of these edits (nor its eventual deletion) need staging in the commits below. Leave `CoreProcessManager.swift` and `AppState.swift` unstaged throughout — they belong to sibling tasks.

- [ ] **Step 1: Create the URLProtocol stub that records request URLs.**

  `APIClient` has an unused test seam — `init(session:baseURL:apiKey:)` at `APIClient.swift:66` (verified: no call sites anywhere in `MCPProxy/` or `MCPProxyTests/`). This stub is what makes it useful: it intercepts every request on an injected `URLSession`, records the absolute URL, and replays a canned JSON body. Create `native/macos/MCPProxy/MCPProxyTests/GlanceStubURLProtocol.swift` with exactly:

  ```swift
  // GlanceStubURLProtocol.swift
  // MCPProxyTests
  //
  // A URLProtocol that records every request URL and replays a canned JSON body,
  // so APIClient's request building and decoding can be tested without a core.

  import Foundation
  @testable import MCPProxy

  final class GlanceStubURLProtocol: URLProtocol {

      /// Absolute URL strings seen by the stub, in request order.
      static var requestedURLs: [String] = []

      /// Body replayed for every request.
      static var responseBody = Data()

      /// Status code replayed for every request.
      static var statusCode = 200

      static func reset() {
          requestedURLs = []
          responseBody = Data()
          statusCode = 200
      }

      /// An APIClient whose traffic is intercepted by this stub.
      static func makeClient() -> APIClient {
          let config = URLSessionConfiguration.ephemeral
          config.protocolClasses = [GlanceStubURLProtocol.self]
          return APIClient(
              session: URLSession(configuration: config),
              baseURL: "http://127.0.0.1:8080",
              apiKey: nil
          )
      }

      /// Wrap a payload in the standard `{"success":true,"data":…}` envelope.
      static func envelope(_ json: String) -> Data {
          Data("{\"success\":true,\"data\":\(json)}".utf8)
      }

      override class func canInit(with request: URLRequest) -> Bool { true }

      override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

      override func startLoading() {
          if let url = request.url {
              GlanceStubURLProtocol.requestedURLs.append(url.absoluteString)
          }
          let response = HTTPURLResponse(
              url: request.url ?? URL(string: "http://127.0.0.1:8080")!,
              statusCode: GlanceStubURLProtocol.statusCode,
              httpVersion: "HTTP/1.1",
              headerFields: ["Content-Type": "application/json"]
          )!
          client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
          client?.urlProtocol(self, didLoad: GlanceStubURLProtocol.responseBody)
          client?.urlProtocolDidFinishLoading(self)
      }

      override func stopLoading() {}
  }
  ```

  Note the stub deliberately does **not** register `SocketURLProtocol`, so the Unix-socket transport (`MCPProxy/Core/SocketTransport.swift:483`) is bypassed entirely.

- [ ] **Step 2: Write the three failing usage-aggregate tests.**

  Create `native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift` with exactly:

  ```swift
  import XCTest
  @testable import MCPProxy

  /// Request-shape and decoding tests for the three tray-glance API calls.
  final class APIClientGlanceTests: XCTestCase {

      override func setUp() {
          super.setUp()
          GlanceStubURLProtocol.reset()
      }

      override func tearDown() {
          GlanceStubURLProtocol.reset()
          super.tearDown()
      }

      // MARK: - Usage aggregate

      func testUsageAggregateRequestsWindowAndTop() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"window":"24h","token_source":"bytes","tokens_saved":184320,
           "tokens_saved_percentage":92.4,"tools":[],"timeline":[]}
          """)
          let client = GlanceStubURLProtocol.makeClient()

          _ = try await client.usageAggregate()

          XCTAssertEqual(
              GlanceStubURLProtocol.requestedURLs,
              ["http://127.0.0.1:8080/api/v1/activity/usage?window=24h&top=1"]
          )
      }

      func testUsageAggregateDecodesTimelineBuckets() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"window":"24h","token_source":"bytes","tokens_saved":0,
           "tokens_saved_percentage":0,"tools":[],"timeline":[
             {"start":"2026-07-29T13:00:00Z","calls":12,"errors":2,"total_resp_bytes":4096}
           ]}
          """)
          let client = GlanceStubURLProtocol.makeClient()

          let usage = try await client.usageAggregate()

          XCTAssertEqual(usage.window, "24h")
          XCTAssertEqual(usage.tokensSaved, 0)
          XCTAssertEqual(usage.timeline.count, 1)
          let bucket = try XCTUnwrap(usage.timeline.first)
          XCTAssertEqual(bucket.calls, 12)
          XCTAssertEqual(bucket.errors, 2)
          XCTAssertEqual(bucket.totalRespBytes, 4096)
          // 2026-07-29T13:00:00Z as seconds since the epoch.
          XCTAssertEqual(bucket.start.timeIntervalSince1970, 1785330000, accuracy: 0.5)
      }

      func testUsageBucketDecodesFractionalSecondTimestamps() throws {
          let json = Data("""
          {"start":"2026-07-29T13:00:00.123Z","calls":1,"errors":0,"total_resp_bytes":0}
          """.utf8)

          let bucket = try JSONDecoder().decode(UsageBucket.self, from: json)

          // Tolerance must stay well under the .123s being asserted, or the test
          // passes even when the fractional-seconds branch is dropped.
          XCTAssertEqual(bucket.start.timeIntervalSince1970, 1785330000.123, accuracy: 0.001)
      }
  }
  ```

- [ ] **Step 3: Run the usage tests and watch them fail to compile.**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "error:" | sort -u
  ```

  Expected output (a Swift compile failure *is* the red state — the symbols do not exist yet). Because Step 0 removed the types and the `usageAggregate` shim, the **main** target fails first, so SwiftPM never gets as far as compiling the test target; these are the errors you see (columns will vary):

  ```
  …/MCPProxy/Core/CoreProcessManager.swift:900: error: value of type 'APIClient' has no member 'usageAggregate'
  …/MCPProxy/State/AppState.swift:102: error: cannot find type 'UsageBucket' in scope
  …/MCPProxy/State/AppState.swift:308: error: cannot find type 'UsageBucket' in scope
  …/MCPProxy/State/AppState.swift:334: error: cannot find type 'UsageBucket' in scope
  error: fatalError
  ```

  On a clean checkout with no spike wiring, the main target builds and you get the test-target errors instead: `no member 'usageAggregate'` at the two call sites, `generic parameter 'T' could not be inferred` at the `XCTUnwrap`, and `cannot find 'UsageBucket' in scope`. Either way the cycle is red.

- [ ] **Step 4: Add the usage models to `Models.swift`.**

  Append to the end of `native/macos/MCPProxy/MCPProxy/API/Models.swift` (after line 1056):

  ```swift

  // MARK: - Usage Aggregate (Spec 069 A3)

  /// One hourly bar of the usage timeline.
  /// Matches Go `contracts.UsageTimeBucket`.
  ///
  /// `calls` INCLUDES `errors` — a stacked chart must plot `calls - errors` and
  /// `errors`, never the two raw fields, or failures are counted twice.
  /// Buckets are UTC-hour aligned and sparse: hours with no activity are omitted.
  struct UsageBucket: Codable, Equatable {
      /// Start of the UTC hour this bucket covers.
      let start: Date
      let calls: Int
      let errors: Int
      let totalRespBytes: Int

      enum CodingKeys: String, CodingKey {
          case start, calls, errors
          case totalRespBytes = "total_resp_bytes"
      }
  }

  // The Codable conformance lives in an extension so the memberwise initialiser
  // is still synthesised (an `init` in the struct body would suppress it).
  extension UsageBucket {
      /// The API emits Go's RFC 3339 rendering (`2026-07-29T13:00:00Z`), which the
      /// shared `JSONDecoder` in `fetchWrapped` cannot parse with its default
      /// `.deferredToDate` strategy. Parsing here keeps the model self-contained
      /// instead of forcing a decoder-wide date strategy onto every other model.
      init(from decoder: Decoder) throws {
          let container = try decoder.container(keyedBy: CodingKeys.self)
          let raw = try container.decode(String.self, forKey: .start)
          guard let parsed = UsageBucket.parseRFC3339(raw) else {
              throw DecodingError.dataCorruptedError(
                  forKey: .start, in: container,
                  debugDescription: "Not an RFC 3339 timestamp: \(raw)"
              )
          }
          let calls = try container.decode(Int.self, forKey: .calls)
          let errors = try container.decode(Int.self, forKey: .errors)
          let bytes = try container.decode(Int.self, forKey: .totalRespBytes)
          self.init(start: parsed, calls: calls, errors: errors, totalRespBytes: bytes)
      }

      func encode(to encoder: Encoder) throws {
          var container = encoder.container(keyedBy: CodingKeys.self)
          try container.encode(UsageBucket.rfc3339String(from: start), forKey: .start)
          try container.encode(calls, forKey: .calls)
          try container.encode(errors, forKey: .errors)
          try container.encode(totalRespBytes, forKey: .totalRespBytes)
      }

      /// Parse an RFC 3339 timestamp, with or without fractional seconds.
      static func parseRFC3339(_ value: String) -> Date? {
          let fractional = ISO8601DateFormatter()
          fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
          if let date = fractional.date(from: value) { return date }
          let plain = ISO8601DateFormatter()
          plain.formatOptions = [.withInternetDateTime]
          return plain.date(from: value)
      }

      /// Render a date the way the API renders bucket starts.
      static func rfc3339String(from date: Date) -> String {
          let formatter = ISO8601DateFormatter()
          formatter.formatOptions = [.withInternetDateTime]
          return formatter.string(from: date)
      }
  }

  /// Response for `GET /api/v1/activity/usage`.
  /// Matches Go `contracts.UsageAggregateResponse`.
  ///
  /// The per-tool rollup (`tools`, `other`) and `generated_at` are deliberately not
  /// decoded: the tray requests `top=1` and renders only the timeline plus the
  /// tokens-saved headline. Unknown keys are ignored by `JSONDecoder`.
  struct UsageAggregateResponse: Codable, Equatable {
      let window: String
      let tokenSource: String?
      let tokensSaved: Int?
      let tokensSavedPercentage: Double?
      /// Global hourly buckets, trimmed to `window`. Never nil; empty when idle.
      let timeline: [UsageBucket]

      enum CodingKeys: String, CodingKey {
          case window, timeline
          case tokenSource = "token_source"
          case tokensSaved = "tokens_saved"
          case tokensSavedPercentage = "tokens_saved_percentage"
      }
  }
  ```

  > The duplicate declarations of these two types were already removed from the scratch `UsageStub.swift` in Step 0. If you skipped Step 0, do it now — two declarations of the same type in one target will not compile.

- [ ] **Step 5: Add `usageAggregate(window:top:)` to `APIClient`.**

  In `native/macos/MCPProxy/MCPProxy/API/APIClient.swift`, insert immediately **before** the line `    /// Fetch the activity summary from `GET /api/v1/activity/summary`.` (currently line 378):

  ```swift
      /// Fetch the usage aggregate from `GET /api/v1/activity/usage`.
      ///
      /// Served from an in-memory snapshot behind a short TTL cache — never a log
      /// scan. `top` trims the per-tool rollup the tray does not render; the
      /// timeline is global and unaffected by it.
      func usageAggregate(window: String = "24h", top: Int = 1) async throws -> UsageAggregateResponse {
          return try await fetchWrapped(path: "/api/v1/activity/usage?window=\(window)&top=\(top)")
      }

  ```

  `fetchWrapped` (private, `APIClient.swift:606`) already unwraps the `{"success":…,"data":…}` envelope and falls back to a bare decode, so no other plumbing is needed. `window=24h` and `top=1` are both accepted by `parseUsageParams` (`internal/httpapi/activity.go:730`), which rejects only a non-integer or `< 1` `top`.

- [ ] **Step 6: Run the usage tests and watch them pass.**

  The `glanceActivity` / `activeSessions` shims left in `UsageStub.swift` by Step 0 keep the main target compiling, so this checkpoint is genuinely green.

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "Test Case|Executed [0-9]+ tests"
  ```

  Expected output:

  ```
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateDecodesTimelineBuckets]' started.
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateDecodesTimelineBuckets]' passed (0.002 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateRequestsWindowAndTop]' started.
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateRequestsWindowAndTop]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageBucketDecodesFractionalSecondTimestamps]' started.
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageBucketDecodesFractionalSecondTimestamps]' passed (0.000 seconds).
  	 Executed 3 tests, with 0 failures (0 unexpected) in 0.002 (0.002) seconds
  ```

- [ ] **Step 7: Commit the usage aggregate call.**

  Stage only these four paths — `UsageStub.swift` is untracked (nothing to stage), and `CoreProcessManager.swift` / `AppState.swift` belong to sibling tasks and must stay unstaged. Do not use `git add -A`.

  ```bash
  cd /Users/user/repos/mcpproxy-go && \
  git add native/macos/MCPProxy/MCPProxy/API/Models.swift \
          native/macos/MCPProxy/MCPProxy/API/APIClient.swift \
          native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift \
          native/macos/MCPProxy/MCPProxyTests/GlanceStubURLProtocol.swift && \
  git commit -m "feat(tray): add usage-aggregate API call and response models

  GET /api/v1/activity/usage?window=24h&top=1 feeds the tray glance header
  and 24h histogram. UsageBucket parses the RFC 3339 bucket start itself so
  the shared fetchWrapped decoder needs no date strategy.

  Adds the first APIClient unit tests, via a URLProtocol stub on the
  existing init(session:) seam."
  ```

- [ ] **Step 8: Write the two failing glance-activity tests.**

  Append inside the `APIClientGlanceTests` class in `native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift`, after `testUsageBucketDecodesFractionalSecondTimestamps`:

  ```swift

      // MARK: - Glance activity

      func testGlanceActivityRequestsToolCallTypesWithOversizedPage() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"activities":[],"total":0,"limit":50,"offset":0}
          """)
          let client = GlanceStubURLProtocol.makeClient()

          _ = try await client.glanceActivity()

          XCTAssertEqual(
              GlanceStubURLProtocol.requestedURLs,
              ["http://127.0.0.1:8080/api/v1/activity?type=tool_call,internal_tool_call&limit=50"]
          )
      }

      func testGlanceActivityDecodesEntries() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"activities":[
            {"id":"01J","type":"tool_call","status":"error","timestamp":"2026-07-29T13:04:05Z",
             "server_name":"jira","tool_name":"get_issue","error_message":"auth failed",
             "request_id":"req-1"}
          ],"total":1,"limit":50,"offset":0}
          """)
          let client = GlanceStubURLProtocol.makeClient()

          let entries = try await client.glanceActivity()

          XCTAssertEqual(entries.count, 1)
          XCTAssertEqual(entries.first?.serverName, "jira")
          XCTAssertEqual(entries.first?.toolName, "get_issue")
          XCTAssertEqual(entries.first?.errorMessage, "auth failed")
      }
  ```

- [ ] **Step 9: Drop the `glanceActivity` shim, then run and watch the tests fail.**

  The shim left by Step 0 would otherwise return `[]` without issuing a request, so remove that line from `MCPProxy/API/UsageStub.swift` first — the file is then down to `activeSessions` alone:

  ```swift
  import Foundation

  extension APIClient {
      func activeSessions(limit: Int = 25) async throws -> [MCPSession] { _ = limit; return [] }
  }
  ```

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "error:" | sort -u
  ```

  Expected output (again the main target fails first, so the test-target errors are not reached):

  ```
  …/MCPProxy/Core/CoreProcessManager.swift:875: error: value of type 'APIClient' has no member 'glanceActivity'
  error: fatalError
  ```

  On a clean checkout without the spike wiring, you instead get `no member 'glanceActivity'` at the two `APIClientGlanceTests.swift` call sites.

- [ ] **Step 10: Add `glanceActivity(limit:)` to `APIClient`.**

  In `native/macos/MCPProxy/MCPProxy/API/APIClient.swift`, insert immediately **before** the `usageAggregate` method added in Step 5:

  ```swift
      /// Fetch the tray glance activity feed from `GET /api/v1/activity`.
      ///
      /// Separate from `recentActivity(limit:)` on purpose: this one carries the
      /// tool-call `type` filter, while `recentActivity` feeds the native Dashboard,
      /// which renders the FULL log (security scans, quarantine changes, OAuth).
      /// The page is deliberately oversized — management built-ins are filtered
      /// client-side, so a small page could be filled entirely by proxy admin calls.
      func glanceActivity(limit: Int = 50) async throws -> [ActivityEntry] {
          let response: ActivityListResponse = try await fetchWrapped(
              path: "/api/v1/activity?type=tool_call,internal_tool_call&limit=\(limit)"
          )
          return response.activities
      }

  ```

  The comma-separated `type` value is parsed server-side by `strings.Split(typeStr, ",")` in `internal/httpapi/activity.go:26-27`; a comma is a legal unescaped sub-delimiter in a URL query, and `URL.absoluteString` preserves it verbatim (asserted by the test in Step 8).

- [ ] **Step 11: Run and watch all five tests pass.**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "Executed [0-9]+ tests"
  ```

  Expected output:

  ```
  	 Executed 5 tests, with 0 failures (0 unexpected) in 0.003 (0.003) seconds
  	 Executed 5 tests, with 0 failures (0 unexpected) in 0.003 (0.003) seconds
  	 Executed 5 tests, with 0 failures (0 unexpected) in 0.003 (0.004) seconds
  ```

  (SwiftPM prints the tally three times — once per test suite level: class, bundle, "Selected tests".)

- [ ] **Step 12: Commit the glance-activity call.**

  ```bash
  cd /Users/user/repos/mcpproxy-go && \
  git add native/macos/MCPProxy/MCPProxy/API/APIClient.swift \
          native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift && \
  git commit -m "feat(tray): add type-filtered glance activity API call

  GET /api/v1/activity?type=tool_call,internal_tool_call&limit=50 is a
  separate feed from recentActivity(), which the native Dashboard uses to
  render the full log. The 50-record page is oversized because management
  built-ins are filtered client-side."
  ```

- [ ] **Step 13: Write the failing active-sessions and `last_activity` tests.**

  Append inside the `APIClientGlanceTests` class, after `testGlanceActivityDecodesEntries`:

  ```swift

      // MARK: - Active sessions

      func testActiveSessionsRequestsStatusActive() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"sessions":[],"total":0,"limit":25}
          """)
          let client = GlanceStubURLProtocol.makeClient()

          _ = try await client.activeSessions()

          XCTAssertEqual(
              GlanceStubURLProtocol.requestedURLs,
              ["http://127.0.0.1:8080/api/v1/sessions?status=active&limit=25"]
          )
      }

      /// Regression: the model decoded `last_active`, but the API emits
      /// `last_activity`, so every session's timestamp silently arrived as nil.
      func testMCPSessionDecodesLastActivity() throws {
          let json = Data("""
          {"id":"sess-1","client_name":"Claude Code","status":"active",
           "tool_call_count":8,"start_time":"2026-07-29T12:00:00Z",
           "last_activity":"2026-07-29T13:04:05Z"}
          """.utf8)

          let session = try JSONDecoder().decode(APIClient.MCPSession.self, from: json)

          XCTAssertEqual(session.lastActivity, "2026-07-29T13:04:05Z")
          XCTAssertEqual(session.clientName, "Claude Code")
          XCTAssertEqual(session.toolCallCount, 8)
      }
  ```

- [ ] **Step 14: Delete the scratch placeholder, then run and watch the session tests fail.**

  The last shim goes now — with `activeSessions` removed the file holds nothing but an empty extension, so delete it outright:

  ```bash
  cd /Users/user/repos/mcpproxy-go && rm -f native/macos/MCPProxy/MCPProxy/API/UsageStub.swift
  ```

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "error:" | sort -u
  ```

  Expected output:

  ```
  …/MCPProxy/Core/CoreProcessManager.swift:888: error: value of type 'APIClient' has no member 'activeSessions'
  error: fatalError
  ```

  On a clean checkout without the spike wiring, you instead get `no member 'activeSessions'` and `value of type 'APIClient.MCPSession' has no member 'lastActivity'` from `APIClientGlanceTests.swift`.

- [ ] **Step 15: Fix the `MCPSession` field name and its five call sites.**

  In `native/macos/MCPProxy/MCPProxy/API/APIClient.swift`, replace line 341:

  ```swift
          let lastActive: String?
  ```

  with:

  ```swift
          /// Timestamp of the session's most recent activity. The API field is
          /// `last_activity` (Go `contracts.MCPSession.LastActivity`); decoding
          /// `last_active` silently produced nil for every session.
          let lastActivity: String?
  ```

  and replace line 353:

  ```swift
              case lastActive = "last_active"
  ```

  with:

  ```swift
              case lastActivity = "last_activity"
  ```

  Then update the only consumer — `native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift` has exactly five `.lastActive` references (lines 481, 482, 494, 495, 553):

  ```bash
  cd /Users/user/repos/mcpproxy-go && \
  perl -pi -e 's/\.lastActive\b/.lastActivity/g' native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift && \
  grep -n "lastActivity" native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift
  ```

  Expected output:

  ```
  481:                            let existingTime = existing.lastActivity ?? existing.startTime ?? ""
  482:                            let newTime = s.lastActivity ?? s.startTime ?? ""
  494:                    let lTime = lhs.lastActivity ?? lhs.startTime ?? ""
  495:                    let rTime = rhs.lastActivity ?? rhs.startTime ?? ""
  553:                            Text(sessionRelativeTime(session.lastActivity ?? session.startTime))
  ```

  This is a behavioural fix beyond the tray: `DashboardView` sorts and labels "Recent Sessions" by this field, and it was comparing empty strings for every session.

- [ ] **Step 16: Add `activeSessions(limit:)` to `APIClient`.**

  In `native/macos/MCPProxy/MCPProxy/API/APIClient.swift`, insert immediately **after** the closing brace of `sessions(limit:)` (currently line 368) and before the `// MARK: - Activity` line (currently 370):

  ```swift

      /// Fetch only currently-active MCP sessions, for the tray glance "Clients" rows.
      ///
      /// The `status` filter is applied server-side during the storage cursor walk,
      /// before truncation — a client-side filter over a page would miss a session
      /// that started long ago but is calling tools right now.
      func activeSessions(limit: Int = 25) async throws -> [MCPSession] {
          let response: SessionsResponse = try await fetchWrapped(
              path: "/api/v1/sessions?status=active&limit=\(limit)"
          )
          return response.sessions
      }
  ```

  With this in place the scratch placeholder is fully superseded and the spike wiring in `CoreProcessManager.swift` compiles against the real methods again.

- [ ] **Step 17: Run the whole native suite and watch it stay green.**

  The full suite is needed here, not just the filter: the rename in Step 15 touches `DashboardView.swift`, and `ModelsTests.swift` / `ServerBrowseModelsTests.swift` decode adjacent models.

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test 2>&1 | tail -5
  ```

  Expected output — the baseline on the current working tree is 322 tests, so 322 + the 7 glance tests written so far = 329. The total moves as sibling tasks land; what matters is `0 failures`:

  ```
  Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-29 15:24:07.881.
  	 Executed 329 tests, with 0 failures (0 unexpected) in 0.083 (0.094) seconds
  Test Suite 'All tests' passed at 2026-07-29 15:24:07.881.
  	 Executed 329 tests, with 0 failures (0 unexpected) in 0.083 (0.095) seconds
  ✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
  ```

- [ ] **Step 18: Commit the sessions call and the field-name fix.**

  ```bash
  cd /Users/user/repos/mcpproxy-go && \
  git add native/macos/MCPProxy/MCPProxy/API/APIClient.swift \
          native/macos/MCPProxy/MCPProxy/Views/DashboardView.swift \
          native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift && \
  git commit -m "fix(tray): decode last_activity, add active-session API call

  MCPSession declared last_active, but the API emits last_activity, so the
  field was nil for every session — DashboardView was sorting and labelling
  Recent Sessions off an always-empty string.

  Adds activeSessions(limit:), which asks the server for status=active
  rather than filtering a truncated page client-side."
  ```

- [ ] **Step 19: Write the counting stub and the two failing seam tests.**

  Create `native/macos/MCPProxy/MCPProxyTests/CountingGlanceDataSource.swift`:

  ```swift
  // CountingGlanceDataSource.swift
  // MCPProxyTests
  //
  // A GlanceDataSource that performs no I/O and counts calls. Used to pin the
  // spec-048 invariant: building the tray menu issues zero requests.

  import Foundation
  @testable import MCPProxy

  final class CountingGlanceDataSource: GlanceDataSource {

      private(set) var usageCallCount = 0
      private(set) var activityCallCount = 0
      private(set) var sessionCallCount = 0

      /// Total requests this data source was asked to make.
      var totalCallCount: Int { usageCallCount + activityCallCount + sessionCallCount }

      var usageToReturn = UsageAggregateResponse(
          window: "24h",
          tokenSource: "bytes",
          tokensSaved: 0,
          tokensSavedPercentage: 0,
          timeline: []
      )
      var activityToReturn: [ActivityEntry] = []
      var sessionsToReturn: [APIClient.MCPSession] = []

      func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse {
          usageCallCount += 1
          return usageToReturn
      }

      func glanceActivity(limit: Int) async throws -> [ActivityEntry] {
          activityCallCount += 1
          return activityToReturn
      }

      func activeSessions(limit: Int) async throws -> [APIClient.MCPSession] {
          sessionCallCount += 1
          return sessionsToReturn
      }
  }
  ```

  Then append inside the `APIClientGlanceTests` class, after `testMCPSessionDecodesLastActivity`:

  ```swift

      // MARK: - Data-source seam

      func testAPIClientConformsToGlanceDataSource() async throws {
          GlanceStubURLProtocol.responseBody = GlanceStubURLProtocol.envelope("""
          {"sessions":[],"total":0,"limit":25}
          """)
          let source: any GlanceDataSource = GlanceStubURLProtocol.makeClient()

          _ = try await source.activeSessions(limit: 25)

          XCTAssertEqual(GlanceStubURLProtocol.requestedURLs.count, 1)
      }

      func testCountingStubSatisfiesTheProtocolAndIssuesNoRequests() async throws {
          let stub = CountingGlanceDataSource()
          let source: any GlanceDataSource = stub

          _ = try await source.usageAggregate(window: "24h", top: 1)
          _ = try await source.glanceActivity(limit: 50)
          _ = try await source.activeSessions(limit: 25)

          XCTAssertEqual(stub.totalCallCount, 3)
          XCTAssertTrue(GlanceStubURLProtocol.requestedURLs.isEmpty)
      }
  ```

- [ ] **Step 20: Run and watch the seam tests fail.**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter APIClientGlanceTests 2>&1 | grep -E "error:" | sort -u
  ```

  Expected output — the main target is healthy at this point, so these are genuine test-target errors:

  ```
  …/MCPProxyTests/APIClientGlanceTests.swift:138:25: error: cannot find type 'GlanceDataSource' in scope
  …/MCPProxyTests/APIClientGlanceTests.swift:147:25: error: cannot find type 'GlanceDataSource' in scope
  …/MCPProxyTests/CountingGlanceDataSource.swift:10:39: error: cannot find type 'GlanceDataSource' in scope
  error: emit-module command failed with exit code 1 (use -v to see invocation)
  error: fatalError
  ```

- [ ] **Step 21: Create the `GlanceDataSource` protocol.**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceDataSource.swift` (the `Menu/Glance/` directory already exists; `Package.swift` globs it automatically):

  ```swift
  // GlanceDataSource.swift
  // MCPProxy
  //
  // The narrow read surface the tray glance section depends on.

  import Foundation

  /// The three reads that feed the tray glance section.
  ///
  /// The glance component depends on this protocol rather than on the concrete
  /// `APIClient` actor so a counting stub can be injected in tests. That is what
  /// makes the spec-048 invariant testable: opening the menu must perform zero
  /// network requests, which can only be asserted if the requests are countable.
  protocol GlanceDataSource {
      /// `GET /api/v1/activity/usage?window=<window>&top=<top>`
      func usageAggregate(window: String, top: Int) async throws -> UsageAggregateResponse

      /// `GET /api/v1/activity?type=tool_call,internal_tool_call&limit=<limit>`
      func glanceActivity(limit: Int) async throws -> [ActivityEntry]

      /// `GET /api/v1/sessions?status=active&limit=<limit>`
      func activeSessions(limit: Int) async throws -> [APIClient.MCPSession]
  }

  /// The production data source. `APIClient` already has all three methods with
  /// matching signatures, so the conformance is declaration-only.
  extension APIClient: GlanceDataSource {}
  ```

  Two Swift details worth knowing: an `actor`'s isolated methods satisfy `async` protocol requirements (SE-0306), which is why the empty extension is enough; and default argument values (`window: String = "24h"`) do not change a method's signature, so they do not break the match.

- [ ] **Step 22: Run the whole native suite and watch everything pass.**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test 2>&1 | grep -E "APIClientGlanceTests|Executed [0-9]+ tests" | tail -14
  ```

  Expected output — 9 glance tests, and 322 + 9 = 331 overall on the current working tree. As in Step 17 the grand total moves with sibling tasks; `0 failures` and the 9 named cases are what this step pins. XCTest orders methods by ASCII, so `testAPIClient…` precedes `testActiveSessions…`:

  ```
  Test Suite 'APIClientGlanceTests' started at 2026-07-29 15:22:13.114.
  Test Case '-[MCPProxyTests.APIClientGlanceTests testAPIClientConformsToGlanceDataSource]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testActiveSessionsRequestsStatusActive]' passed (0.006 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testCountingStubSatisfiesTheProtocolAndIssuesNoRequests]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testGlanceActivityDecodesEntries]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testGlanceActivityRequestsToolCallTypesWithOversizedPage]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testMCPSessionDecodesLastActivity]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateDecodesTimelineBuckets]' passed (0.002 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageAggregateRequestsWindowAndTop]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.APIClientGlanceTests testUsageBucketDecodesFractionalSecondTimestamps]' passed (0.000 seconds).
  	 Executed 9 tests, with 0 failures (0 unexpected) in 0.010 (0.010) seconds
  	 Executed 331 tests, with 0 failures (0 unexpected) in 0.084 (0.095) seconds
  	 Executed 331 tests, with 0 failures (0 unexpected) in 0.084 (0.100) seconds
  ```

- [ ] **Step 23: Commit the data-source protocol.**

  ```bash
  cd /Users/user/repos/mcpproxy-go && \
  git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceDataSource.swift \
          native/macos/MCPProxy/MCPProxyTests/CountingGlanceDataSource.swift \
          native/macos/MCPProxy/MCPProxyTests/APIClientGlanceTests.swift && \
  git commit -m "refactor(tray): extract GlanceDataSource protocol over APIClient

  The glance section depends on a three-method protocol instead of the
  concrete APIClient actor, so a counting stub can be injected and the
  spec-048 invariant (menu open issues zero requests) becomes assertable.

  APIClient's conformance is declaration-only — an actor's isolated methods
  already satisfy async protocol requirements."
  ```

---

### Task 3: Swift — AppState glance fields, refresh wiring, and disconnect reset

**Files:**
- Create: `native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift`
- Modify: `native/macos/MCPProxy/MCPProxy/State/AppState.swift` — line 37 (the `coreState` declaration), insert a new block immediately before line 75 (`// MARK: Token metrics (from status response)`), and append new members immediately before the closing brace at line 260
- Modify: `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift` — lines 751–760 (`refreshState()`), and insert three new methods immediately before line 866 (`/// Fetch token metrics from the status endpoint and update appState.`)
- Test: `native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift`

> Line numbers are from `main` at commit `b2642a57` and have been verified against that tree. If Task 4 (the SSE adapter) has already landed its edits to `CoreProcessManager.swift`, the `refreshState()` body and the token-metrics comment will have moved — locate them by the quoted text, not by number. The anchors in `AppState.swift` are untouched by any other task.

**Interfaces:**

*Consumes (produced by Task 2, all in `native/macos/MCPProxy/MCPProxy/API/`):*
```swift
// Models.swift — Codable conformance MUST live in an extension, so the memberwise
// initialiser below IS synthesised and usable from tests. (Declaring init(from:)
// inside the struct body suppresses it and breaks this task's test helper — see
// the recognition note in Step 2.)
struct UsageBucket: Equatable { ... }
extension UsageBucket: Codable { ... }

struct UsageBucket: Codable, Equatable {
    let start: Date          // UTC-hour aligned
    let calls: Int           // INCLUDES errors
    let errors: Int
    let totalRespBytes: Int
}
init(start: Date, calls: Int, errors: Int, totalRespBytes: Int)   // memberwise

struct UsageAggregateResponse: Codable, Equatable {
    let window: String
    let tokenSource: String?
    let tokensSaved: Int?
    let tokensSavedPercentage: Double?
    let timeline: [UsageBucket]   // never nil; empty when idle
}

// APIClient.swift — `actor APIClient`, so every call needs `await`.
func glanceActivity(limit: Int = 50) async throws -> [ActivityEntry]
func activeSessions(limit: Int = 25) async throws -> [MCPSession]   // = APIClient.MCPSession
func usageAggregate(window: String = "24h", top: Int = 1) async throws -> UsageAggregateResponse
```

*Consumes (already on `main`, verified by reading the files at `b2642a57`):*
```swift
// State/AppState.swift
final class AppState: ObservableObject          // NOT @MainActor at class level (line 32)
@Published var recentActivity: [ActivityEntry] = []          // line 64
@Published var recentSessions: [APIClient.MCPSession] = []   // line 65
@MainActor func updateActivity(_ entries: [ActivityEntry])   // line 245
@MainActor func transition(to newState: CoreState)           // line 257
// Core/CoreState.swift
enum CoreState: Equatable { case idle, launching, waitingForCore, connected,
                            reconnecting(attempt: Int), error(CoreError), shuttingDown }
enum CoreError: Error, Equatable { ... case general(String) ... }
// API/Models.swift
struct ActivityEntry: Codable, Identifiable, Equatable   // id/type/status/timestamp are non-optional
// NOTE (line 570): `==` compares `id` ONLY. Array equality on [ActivityEntry] therefore
// cannot detect a changed status or a late-arriving hasSensitiveData flag.
```

*Produces (Task 5's `GlanceSection` and Task 4's SSE adapter depend on these exact symbols):*
```swift
// on AppState
@Published var glanceActivity: [ActivityEntry]            // default []
@Published var glanceSessions: [APIClient.MCPSession]     // default []
@Published var usageTimeline: [UsageBucket]?              // nil = not loaded
@Published var callsThisHour: Int?                        // nil = not loaded
static func floorToHour(_ date: Date) -> Date
static func callsInCurrentHour(_ timeline: [UsageBucket], now: Date = Date()) -> Int
@MainActor func updateGlanceActivity(_ entries: [ActivityEntry])
@MainActor func updateGlanceSessions(_ sessions: [APIClient.MCPSession])
@MainActor func updateUsage(timeline: [UsageBucket], now: Date = Date())
func clearGlanceState()                                   // nonisolated, callable from didSet
// invariant: setting `coreState` to anything other than `.connected` calls clearGlanceState()
```

---

- [ ] **Step 1: Create the failing test file with the two current-hour tests**

  Create `native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift` with exactly this content. (SwiftPM globs the `MCPProxyTests/` directory — `Package.swift:18-22` declares the test target with `path: "MCPProxyTests"`, so no target registration is needed for a new test file.)

  ```swift
  import XCTest
  @testable import MCPProxy

  /// Tray Glance: the four `AppState` glance fields, the current-UTC-hour
  /// derivation behind `callsThisHour`, the disconnect reset, and the guarantee
  /// that the shared Dashboard/ActivityView feeds are not narrowed.
  @MainActor
  final class AppStateGlanceTests: XCTestCase {

      // MARK: - callsThisHour

      /// A sparse timeline whose newest bucket is three hours old must report
      /// zero calls this hour, NOT that bucket's count. Buckets are UTC-hour
      /// aligned and the endpoint omits hours with no activity, so "the most
      /// recent bucket" is a count from the past presented as if it were current.
      func testCallsThisHourIsZeroWhenCurrentHourBucketIsAbsent() throws {
          let now = Self.date("2026-07-29T14:05:00Z")
          let timeline = [
              Self.bucket("2026-07-29T10:00:00Z", calls: 7),
              Self.bucket("2026-07-29T11:00:00Z", calls: 12, errors: 2),
          ]

          let state = AppState()
          state.coreState = .connected
          state.updateUsage(timeline: timeline, now: now)

          XCTAssertEqual(state.callsThisHour, 0)
          XCTAssertEqual(state.usageTimeline?.count, 2)
      }

      /// When the current UTC hour does have a bucket, its `calls` is the headline
      /// number — regardless of where it sits in the array.
      func testCallsThisHourReadsTheCurrentHourBucket() throws {
          let now = Self.date("2026-07-29T11:42:31Z")
          let timeline = [
              Self.bucket("2026-07-29T11:00:00Z", calls: 12, errors: 2),
              Self.bucket("2026-07-29T10:00:00Z", calls: 7),
          ]

          let state = AppState()
          state.updateUsage(timeline: timeline, now: now)

          XCTAssertEqual(state.callsThisHour, 12)
      }

      // MARK: - Helpers

      private static func date(_ iso: String) -> Date {
          let formatter = ISO8601DateFormatter()
          formatter.formatOptions = [.withInternetDateTime]
          // swiftlint:disable:next force_unwrapping
          return formatter.date(from: iso)!
      }

      private static func bucket(_ iso: String, calls: Int, errors: Int = 0) -> UsageBucket {
          UsageBucket(start: date(iso), calls: calls, errors: errors, totalRespBytes: 0)
      }

      private static func activity(id: String, type: String, status: String = "success") throws -> ActivityEntry {
          let json = """
          {"id":"\(id)","type":"\(type)","status":"\(status)","timestamp":"2026-07-29T11:00:00Z"}
          """
          // swiftlint:disable:next force_unwrapping
          return try JSONDecoder().decode(ActivityEntry.self, from: json.data(using: .utf8)!)
      }

      private static func session(id: String, status: String) throws -> APIClient.MCPSession {
          let json = """
          {"id":"\(id)","client_name":"Claude Code","status":"\(status)","tool_call_count":3}
          """
          // swiftlint:disable:next force_unwrapping
          return try JSONDecoder().decode(APIClient.MCPSession.self, from: json.data(using: .utf8)!)
      }
  }
  ```

  The `activity(id:type:status:)` and `session(id:status:)` helpers are unused until Step 4; Swift does not warn on unused private static methods, so this compiles cleanly once the AppState members exist. Both decode from a minimal JSON body on purpose: `ActivityEntry` has exactly four non-optional fields (`id`, `type`, `status`, `timestamp`) and `MCPSession` two (`id`, `status`), so no fixture drift when Task 2 or 4 touches those models.

- [ ] **Step 2: Run the test and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateGlanceTests 2>&1 | tail -20
  ```

  Expected: the build fails before any test runs, with errors on lines 25, 27 and 28 of `AppStateGlanceTests.swift` reading

  ```
  error: value of type 'AppState' has no member 'updateUsage'
  error: value of type 'AppState' has no member 'callsThisHour'
  error: value of type 'AppState' has no member 'usageTimeline'
  ```

  and a trailing `error: fatalError`. Zero tests executed. (Match on the message text; do not gate on the exact column numbers swiftc reports.)

  Two failure modes that are **not** this task's bug — recognise them and fix them where they belong:
  - `cannot find 'UsageBucket' in scope` → Task 2 has not landed. Stop and complete Task 2 first.
  - `missing argument for parameter 'from' in call` on the `Self.bucket` helper → Task 2 declared `init(from decoder:)` **inside** the `UsageBucket` struct body, which suppresses the synthesised memberwise initialiser. Move Task 2's Codable conformance into an `extension UsageBucket: Codable { … }`, per its interface contract. Do not work around it by rewriting this test to decode fixtures.

- [ ] **Step 3: Add the four glance fields and the usage derivation to AppState**

  In `native/macos/MCPProxy/MCPProxy/State/AppState.swift`, insert this block immediately **before** the line `    // MARK: Token metrics (from status response)` (line 75 on a clean tree):

  ```swift
      // MARK: Tray Glance feeds (separate from the shared recentActivity/recentSessions)

      /// Tool-call activity for the tray glance section, fetched with a `type`
      /// filter. Deliberately NOT the same feed as `recentActivity`: the native
      /// Dashboard renders the full activity log (security scans, quarantine
      /// changes, OAuth events) from that one, so narrowing it would gut the view.
      @Published var glanceActivity: [ActivityEntry] = []

      /// Active-only MCP sessions for the tray glance "Clients" rows. Separate from
      /// `recentSessions`, which ActivityView and DashboardView use to resolve
      /// session ids to client names and therefore must keep closed sessions.
      @Published var glanceSessions: [APIClient.MCPSession] = []

      /// Hourly call timeline for the last 24h. `nil` means "not loaded yet";
      /// an empty array means "loaded, and the proxy was idle".
      @Published var usageTimeline: [UsageBucket]?

      /// Calls recorded in the CURRENT UTC hour. `nil` means "not loaded yet".
      @Published var callsThisHour: Int?

  ```

  Then insert this block immediately **after** the closing brace of `func transition(to newState: CoreState)` (line 259) and **before** the final `}` that closes `final class AppState` (line 260 on a clean tree):

  ```swift

      // MARK: Tray Glance helpers

      /// Truncate a date to the start of its UTC hour. Unix time is UTC by
      /// definition, so flooring the epoch seconds needs no Calendar or TimeZone.
      static func floorToHour(_ date: Date) -> Date {
          let seconds = date.timeIntervalSince1970
          return Date(timeIntervalSince1970: (seconds / 3600).rounded(.down) * 3600)
      }

      /// Calls recorded in the UTC hour containing `now`.
      ///
      /// Buckets are UTC-hour aligned and SPARSE — the endpoint omits hours with no
      /// activity. Picking "the newest bucket" would therefore show a count from
      /// hours ago as if it were current, so this matches on the bucket start and
      /// returns 0 when the current hour has no bucket.
      static func callsInCurrentHour(_ timeline: [UsageBucket], now: Date = Date()) -> Int {
          let currentHour = floorToHour(now)
          for bucket in timeline where floorToHour(bucket.start) == currentHour {
              return bucket.calls
          }
          return 0
      }

      /// Store the 24h timeline and derive the current-hour headline count.
      ///
      /// Both assignments are guarded. This file's rule (see `updateServers`) is
      /// "only publish when the data actually differs", because every `@Published`
      /// write feeds the debounced `objectWillChange → rebuildMenu()` sink in
      /// MCPProxyApp — an unguarded write here would rebuild the menu every 30s on
      /// a completely idle proxy. `UsageBucket` is `Equatable`, so the guard is free.
      @MainActor
      func updateUsage(timeline: [UsageBucket], now: Date = Date()) {
          if usageTimeline != timeline { usageTimeline = timeline }
          let calls = AppState.callsInCurrentHour(timeline, now: now)
          if callsThisHour != calls { callsThisHour = calls }
      }
  ```

- [ ] **Step 4: Run the test and watch it pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateGlanceTests 2>&1 | tail -8
  ```

  Expected tail:

  ```
  Test Case '-[MCPProxyTests.AppStateGlanceTests testCallsThisHourIsZeroWhenCurrentHourBucketIsAbsent]' passed (0.002 seconds).
  Test Case '-[MCPProxyTests.AppStateGlanceTests testCallsThisHourReadsTheCurrentHourBucket]' passed (0.000 seconds).
  Test Suite 'AppStateGlanceTests' passed at ...
  	 Executed 2 tests, with 0 failures (0 unexpected) in 0.003 (0.004) seconds
  ```

- [ ] **Step 5: Commit the fields and the derivation**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/State/AppState.swift native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift && git commit -m "feat(tray): AppState glance fields + current-UTC-hour usage derivation

Adds glanceActivity/glanceSessions/usageTimeline/callsThisHour alongside the
shared recentActivity/recentSessions feeds, which stay broad because the native
Dashboard renders the full activity log and ActivityView resolves session ids
through recentSessions.

callsThisHour matches the bucket whose start equals the current UTC hour and
reports 0 when that hour is absent; usage buckets are sparse, so taking the
newest bucket would present an hours-old count as live."
  ```

- [ ] **Step 6: Add the failing tests for the disconnect reset, the reconciling poll, and the shared-feed guarantee**

  In `native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift`, insert this block immediately **before** the line `    // MARK: - Helpers`:

  ```swift
      /// "Loaded, and the proxy was idle" must be expressible: an empty timeline
      /// yields 0, which is deliberately distinct from the nil loading state.
      func testEmptyTimelineYieldsZeroNotNil() {
          let state = AppState()
          XCTAssertNil(state.callsThisHour, "callsThisHour starts nil = not loaded yet")

          state.updateUsage(timeline: [], now: Self.date("2026-07-29T11:42:00Z"))

          XCTAssertEqual(state.callsThisHour, 0)
          XCTAssertEqual(state.usageTimeline, [])
      }

      // MARK: - Disconnect reset

      /// The connect path flips to `.connected` before the first refresh
      /// completes, so glance state from a previous core must be cleared the
      /// moment the core state leaves `.connected`.
      func testGlanceStateClearedOnDisconnect() throws {
          let state = AppState()
          state.coreState = .connected
          state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
          state.updateGlanceSessions([try Self.session(id: "s1", status: "active")])
          state.updateUsage(
              timeline: [Self.bucket("2026-07-29T11:00:00Z", calls: 12)],
              now: Self.date("2026-07-29T11:10:00Z")
          )

          state.coreState = .idle

          XCTAssertTrue(state.glanceActivity.isEmpty)
          XCTAssertTrue(state.glanceSessions.isEmpty)
          XCTAssertNil(state.usageTimeline)
          XCTAssertNil(state.callsThisHour)
      }

      /// Every non-connected state clears, not just `.idle` — a reconnecting or
      /// errored core must not keep showing the old numbers either.
      func testGlanceStateClearedOnReconnectingAndError() throws {
          for target in [CoreState.reconnecting(attempt: 1), .error(.general("boom")), .shuttingDown] {
              let state = AppState()
              state.coreState = .connected
              state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
              state.callsThisHour = 9

              state.coreState = target

              XCTAssertTrue(state.glanceActivity.isEmpty, "\(target) should clear glanceActivity")
              XCTAssertNil(state.callsThisHour, "\(target) should clear callsThisHour")
          }
      }

      /// Staying connected must not wipe the feeds the refresh loop just filled.
      func testConnectedStateDoesNotClearGlanceState() throws {
          let state = AppState()
          state.coreState = .connected
          state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
          state.callsThisHour = 4

          state.coreState = .connected

          XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
          XCTAssertEqual(state.callsThisHour, 4)
      }

      // MARK: - Reconciling poll

      /// The 30s poll exists to reconcile the SSE-fed optimistic list with the
      /// server's canonical records — including the asynchronously-computed
      /// sensitive-data flag and the final status, which arrive on a record whose
      /// id has NOT changed. `ActivityEntry`'s Equatable is id-only
      /// (API/Models.swift:570), so an id-only guard would drop those corrections
      /// forever and the row would lie until it scrolled off.
      func testGlanceActivityUpdatesWhenOnlyStatusChanges() throws {
          let state = AppState()
          state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
          state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call", status: "error")])

          XCTAssertEqual(state.glanceActivity.first?.status, "error")
      }

      // MARK: - Shared feeds are not narrowed

      /// Dashboard non-regression: `recentActivity` still carries non-tool-call
      /// records (security scans, OAuth events) and `recentSessions` still carries
      /// closed sessions after the glance feeds are populated alongside them.
      func testGlanceFeedsDoNotNarrowSharedFeeds() throws {
          let state = AppState()
          state.coreState = .connected

          state.updateActivity([
              try Self.activity(id: "a1", type: "tool_call"),
              try Self.activity(id: "a2", type: "security_scan"),
          ])
          state.recentSessions = [
              try Self.session(id: "s1", status: "active"),
              try Self.session(id: "s2", status: "closed"),
          ]

          state.updateGlanceActivity([try Self.activity(id: "a1", type: "tool_call")])
          state.updateGlanceSessions([try Self.session(id: "s1", status: "active")])

          XCTAssertEqual(state.recentActivity.map(\.type), ["tool_call", "security_scan"])
          XCTAssertEqual(state.recentSessions.map(\.status), ["active", "closed"])
          XCTAssertEqual(state.glanceActivity.map(\.id), ["a1"])
          XCTAssertEqual(state.glanceSessions.map(\.id), ["s1"])
      }

  ```

- [ ] **Step 7: Run and watch the new tests fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateGlanceTests 2>&1 | tail -20
  ```

  Expected: compilation fails with errors reading

  ```
  error: value of type 'AppState' has no member 'updateGlanceActivity'
  error: value of type 'AppState' has no member 'updateGlanceSessions'
  ```

  and a trailing `error: fatalError`. Zero tests executed. (`updateUsage`, `callsThisHour` and `updateActivity` all resolve by now — only the two new helpers are missing.)

- [ ] **Step 8: Add the update helpers, `clearGlanceState()`, and the `coreState` didSet**

  In `native/macos/MCPProxy/MCPProxy/State/AppState.swift`, first replace the `coreState` declaration (line 37 on a clean tree, currently `    @Published var coreState: CoreState = .idle`) — keep the existing `/// Current core process state (uses CoreState from CoreState.swift).` comment line above it and make the whole thing read:

  ```swift
      /// Current core process state (uses CoreState from CoreState.swift).
      ///
      /// Tray Glance: any state other than `.connected` clears the glance feeds.
      /// The connect path flips to `.connected` BEFORE the first refresh completes
      /// (CoreProcessManager.launchAndConnect), so without this reset the menu
      /// would briefly present the previous core's numbers as live. The reset lives
      /// in `didSet` rather than in `transition(to:)` because two call sites assign
      /// `coreState` directly (CoreProcessManager.awaitExternalCore,
      /// MCPProxyApp.stopCore) and would otherwise bypass it.
      @Published var coreState: CoreState = .idle {
          didSet {
              if coreState != .connected { clearGlanceState() }
          }
      }
  ```

  Then append these three members to the `// MARK: Tray Glance helpers` section added in Step 3, immediately after `updateUsage(timeline:now:)` and before the class's final `}`:

  ```swift

      /// Replace the glance activity feed. Leaves `recentActivity` untouched.
      ///
      /// `ActivityEntry`'s Equatable is id-only (API/Models.swift:570), so guarding
      /// on ids alone would drop the reconciling poll's late corrections: the
      /// sensitive-data flag is computed asynchronously and the final status
      /// arrives on a record whose id has not changed. Fingerprint the fields the
      /// glance rows actually render instead — still cheap, still churn-free.
      @MainActor
      func updateGlanceActivity(_ entries: [ActivityEntry]) {
          func fingerprint(_ list: [ActivityEntry]) -> [String] {
              list.map { "\($0.id)|\($0.status)|\($0.hasSensitiveData == true)" }
          }
          if fingerprint(entries) != fingerprint(glanceActivity) {
              glanceActivity = entries
          }
      }

      /// Replace the glance (active-only) session feed. Leaves `recentSessions` untouched.
      @MainActor
      func updateGlanceSessions(_ sessions: [APIClient.MCPSession]) {
          if sessions.map(\.id) != glanceSessions.map(\.id) {
              glanceSessions = sessions
          }
      }

      /// Drop every glance feed. Called from `coreState.didSet` on any state other
      /// than `.connected` so a stopped or reconnecting core never shows the
      /// previous core's numbers as live.
      ///
      /// Deliberately NOT `@MainActor`: a property observer is a nonisolated
      /// context, so an isolated method could not be called from it without
      /// `await`. `AppState` itself is not `@MainActor` either, so a plain method
      /// is already nonisolated. Every real assignment to `coreState` happens on
      /// the main actor anyway — `transition(to:)`, plus the two `MainActor.run`
      /// blocks in CoreProcessManager.awaitExternalCore and MCPProxyApp.stopCore.
      func clearGlanceState() {
          if !glanceActivity.isEmpty { glanceActivity = [] }
          if !glanceSessions.isEmpty { glanceSessions = [] }
          if usageTimeline != nil { usageTimeline = nil }
          if callsThisHour != nil { callsThisHour = nil }
      }
  ```

- [ ] **Step 9: Run the full glance test class and watch all eight pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateGlanceTests 2>&1 | tail -14
  ```

  Expected tail:

  ```
  Test Case '-[MCPProxyTests.AppStateGlanceTests testGlanceActivityUpdatesWhenOnlyStatusChanges]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.AppStateGlanceTests testGlanceFeedsDoNotNarrowSharedFeeds]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.AppStateGlanceTests testGlanceStateClearedOnDisconnect]' passed (0.000 seconds).
  Test Case '-[MCPProxyTests.AppStateGlanceTests testGlanceStateClearedOnReconnectingAndError]' passed (0.000 seconds).
  Test Suite 'AppStateGlanceTests' passed at ...
  	 Executed 8 tests, with 0 failures (0 unexpected) in 0.004 (0.004) seconds
  ```

- [ ] **Step 10: Confirm the pre-existing AppState tests still pass under the new didSet**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateStatusSummaryTests 2>&1 | grep Executed | tail -1 && swift test --filter AppStateOAuthSignInTests 2>&1 | grep Executed | tail -1
  ```

  Expected: `Executed 4 tests, with 0 failures` for `AppStateStatusSummaryTests` and `Executed 8 tests, with 0 failures` for `AppStateOAuthSignInTests` (exact counts may differ if those files grew; the requirement is `0 failures` in both). Note `grep Executed` rather than `tail -4`: `swift test --filter` prints the swift-testing runner's summary last, which would otherwise hide the XCTest counts.

- [ ] **Step 11: Commit the disconnect reset**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/State/AppState.swift native/macos/MCPProxy/MCPProxyTests/AppStateGlanceTests.swift && git commit -m "feat(tray): clear glance state whenever the core leaves .connected

The connect path transitions to .connected before the first refresh completes,
so hiding the glance block is not enough — without a reset the menu would
briefly render the previous core's activity, clients and call count as live.

The reset hangs off coreState's didSet rather than transition(to:) because
CoreProcessManager.awaitExternalCore and MCPProxyApp.stopCore assign coreState
directly and would otherwise bypass it.

updateGlanceActivity fingerprints id+status+hasSensitiveData rather than id
alone: ActivityEntry's Equatable is id-only, so an id guard would discard the
reconciling poll's late status and sensitive-data corrections."
  ```

- [ ] **Step 12: Wire the three glance fetches into the 30-second refresh loop**

  This step has no test of its own: `refreshState()` is `private` on `actor CoreProcessManager` and the package carries no URLSession stub or fake `APIClient`, so there is nothing to assert against without inventing a mock layer no other task uses. It is verified structurally in Step 13 (build + call-site grep) and behaviourally by the AppState tests above.

  In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, replace the body of `refreshState()` (lines 751–760 on a clean tree) so it reads:

  ```swift
      private func refreshState() async {
          await refreshActivity()
          await refreshSessions()
          await refreshGlanceActivity()
          await refreshGlanceSessions()
          await refreshUsage()
          await refreshTokenMetrics()
          await refreshSecurityStatus()
          await refreshProfiles()
          // Bump activityVersion so ActivityView reloads
          // (SSE doesn't emit "activity" events, so periodic refresh is needed)
          await MainActor.run { appState.activityVersion += 1 }
      }
  ```

  Then insert these three methods immediately **before** the line `    /// Fetch token metrics from the status endpoint and update appState.` (line 866 on a clean tree):

  ```swift
      /// Tray Glance: fetch the type-filtered tool-call feed for the menu's
      /// "Recent" rows. Separate from `refreshActivity()` on purpose — that feed
      /// stays broad because the native Dashboard renders the full activity log.
      private func refreshGlanceActivity() async {
          guard let apiClient else { return }
          do {
              let entries = try await apiClient.glanceActivity(limit: 50)
              await appState.updateGlanceActivity(entries)
          } catch {
              // Non-fatal; we'll retry on the next refresh
          }
      }

      /// Tray Glance: fetch active-only sessions for the menu's "Clients" rows.
      /// Separate from `refreshSessions()`, which must keep closed sessions so
      /// ActivityView can resolve session ids to client names.
      private func refreshGlanceSessions() async {
          guard let apiClient else { return }
          do {
              let sessions = try await apiClient.activeSessions(limit: 25)
              await appState.updateGlanceSessions(sessions)
          } catch {
              // Non-fatal; we'll retry on the next refresh
          }
      }

      /// Tray Glance: fetch the 24h usage aggregate that backs both the header
      /// count and the histogram submenu.
      private func refreshUsage() async {
          guard let apiClient else { return }
          do {
              let usage = try await apiClient.usageAggregate(window: "24h", top: 1)
              await appState.updateUsage(timeline: usage.timeline)
          } catch {
              // Non-fatal; the header and histogram stay in their loading state
          }
      }

  ```

  All three ride `refreshState()`, which already runs on connect (`attachToExternalCore` :302, `launchAndConnect` :334), on `attemptReconnection` (:933), on the `config.reloaded` SSE event (:677), and every 30 s from `startPeriodicRefresh()` (:742) — no new timer is introduced, and no fetch is added to the menu-open path.

- [ ] **Step 13: Build and verify the wiring is present**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift build 2>&1 | tail -3 && grep -c "await refreshGlanceActivity()\|await refreshGlanceSessions()\|await refreshUsage()" MCPProxy/Core/CoreProcessManager.swift
  ```

  Expected: the `swift build` output ends with a `Build complete!` line (the preceding `[n/m]` progress lines vary with how much is already cached — an incremental build prints as little as `[0/3] Write swift-version-…`), followed by

  ```
  3
  ```

  A count other than `3` means the calls were not added to `refreshState()`. The three method *definitions* do not match the pattern (`private func refreshGlanceActivity() async` has no `await`), so `3` is exactly the three call sites. If the build reports `value of type 'APIClient' has no member 'glanceActivity'` or `... 'usageAggregate'`, Task 2's APIClient methods are missing — land Task 2 first.

- [ ] **Step 14: Run the whole Swift suite exactly as CI does**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test 2>&1 | grep -E "Executed [0-9]+ tests|error:" | tail -4
  ```

  Expected: a final `Executed N tests, with 0 failures (0 unexpected)` line for `MCPProxyPackageTests.xctest`. Running without the grep also prints the swift-testing runner's `✔ Test run with 0 tests in 0 suites passed` afterwards — no swift-testing tests exist in this package, so that line is normal. Any failure here must be fixed before committing: `.github/workflows/native-tests.yml:83` runs bare `swift test` with an explicit "do not reintroduce `--skip`" comment.

- [ ] **Step 15: Commit the refresh wiring**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift && git commit -m "feat(tray): fetch glance activity, active sessions and 24h usage on the refresh loop

Three additional fetches on the existing 30s CoreProcessManager cycle, each
failing soft like the surrounding refreshers. No new timer and no menu-open
network call: the tray menu still renders entirely from AppState (spec 048)."
  ```

---

### Task 4: Swift — SSE activity adapter (live glance rows, no refetch)

The core emits `activity.tool_call.completed` and `activity.internal_tool_call.completed` (`internal/runtime/events.go:30,42`); it has never emitted a bare `activity` event, so the `case "activity":` branch in the tray's SSE switch is dead code and activity is 30 s-polled today. This task replaces that branch with a real handler that adapts the event payload into an `ActivityEntry` and prepends it to the glance feed — **without** calling `refreshActivity()`, which would fire one REST GET per event.

Three payload facts drive the adapter, all verified against the Go source:

- The wire format is an envelope `{"payload": {...}, "timestamp": 1753800000}` where `timestamp` is **Unix seconds** (`internal/httpapi/server.go:3205-3208`), while `ActivityEntry.timestamp` is an ISO-8601 **string** (`native/macos/MCPProxy/MCPProxy/API/Models.swift:548`).
- Upstream events carry `server_name` / `tool_name` (`internal/runtime/event_bus.go:444-454`); internal events carry `internal_tool_name` plus `target_server` / `target_tool` **only when non-empty** (`internal/runtime/event_bus.go:552-565`). Reading `tool_name` for an internal event yields a row with no tool at all.
- The failure field is `error_message`, not `error` (`event_bus.go:451`, `event_bus.go:557`) — matching `ActivityEntry.errorMessage`.

The provisional id is `request_id + ":" + type`. A bare `request_id` is not safe: a failed upstream call emits **both** events under one request id, and `ActivityEntry` derives `Identifiable`/`Equatable` from `id` alone (`Models.swift:537,570-572`), so the two distinct records would collide in the feed before `GlanceSelection`'s rule 4 ever got to choose between them.

**Files:**
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceEvent.swift` — the `Menu/Glance/` directory already exists (`GlanceFormatting.swift`, `GlanceSelection.swift` live there)
- Test: `native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift`
- Modify: `native/macos/MCPProxy/MCPProxy/State/AppState.swift` — insert the prepend helper immediately after `updateGlanceActivity(_:)` (added by the AppState-fields task)
- Modify: `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift:683-700` — replace the whole `case "activity":` block inside `handleSSEEvent(_:)`

No `Package.swift` edit is needed: both targets glob by path (`.executableTarget(name: "MCPProxy", path: "MCPProxy")`, `.testTarget(name: "MCPProxyTests", path: "MCPProxyTests")`), so a new file in any subdirectory is picked up automatically.

**Interfaces:**

*Consumes* (from earlier tasks / existing code):
- `AppState.glanceActivity` — `@Published var glanceActivity: [ActivityEntry] = []` (AppState glance-fields task)
- `AppState.updateGlanceActivity(_ entries: [ActivityEntry])` — `@MainActor`, the reconciling poll's wholesale replace (AppState glance-fields task); this task's helper sits next to it
- `ActivityEntry` — existing struct, `native/macos/MCPProxy/MCPProxy/API/Models.swift:536-572`; it declares no `init`, so Swift synthesises the internal memberwise initialiser used below, in declaration order: `id, type, source, serverName, toolName, arguments, response, responseTruncated, status, errorMessage, durationMs, timestamp, sessionId, requestId, metadata, hasSensitiveData, detectionTypes, maxSeverity`
- `SSEEvent` — existing struct, `Models.swift:748`; `event: String` is the SSE event name, `data: String` is the raw JSON
- `GlanceSelection.qualifies(_:)` / `.activityRows(from:limit:)` and `GlanceFormatting.parseTimestamp(_:)` / `.relativeTime(_:now:)` — existing, `Menu/Glance/`; the adapter's output must survive both, so the tests assert against them rather than re-implementing their logic

*Produces* (later tasks may rely on these exact symbols):
- `enum GlanceEvent` with `static let upstreamCompleted = "activity.tool_call.completed"`, `static let internalCompleted = "activity.internal_tool_call.completed"`, and `static func adapt(eventName: String, data: Data) -> ActivityEntry?`
- `AppState.glanceActivityCap: Int` (`static let`, value `50`)
- `AppState.prependGlanceActivity(_ entry: ActivityEntry)` — `@MainActor`
- A `case GlanceEvent.upstreamCompleted, GlanceEvent.internalCompleted:` branch in `CoreProcessManager.handleSSEEvent(_:)` that issues zero REST requests **and bumps no counter that other views refetch on**

---

- [ ] **Step 1: Write the failing adapter test file**

  Create `native/macos/MCPProxyTests/GlanceEventTests.swift` — full path `native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift` — with exactly this content. The suite is XCTest (`import XCTest` + `@testable import MCPProxy` in every existing test file; there is no swift-testing usage in the package). `@MainActor` on the class matches `AppStateStatusSummaryTests.swift` and is required by Step 6, which touches `AppState`.

  ```swift
  import XCTest
  @testable import MCPProxy

  /// Tray Glance — SSE activity adapter.
  ///
  /// The core emits `activity.tool_call.completed` /
  /// `activity.internal_tool_call.completed`, never `activity`, and the payload is
  /// an envelope `{payload, timestamp}` whose timestamp is Unix SECONDS while
  /// `ActivityEntry.timestamp` is an ISO-8601 string. These tests pin the mapping,
  /// and assert it against the real consumers (`GlanceSelection`,
  /// `GlanceFormatting`) rather than a locally rebuilt copy of their logic.
  @MainActor
  final class GlanceEventTests: XCTestCase {

      /// Upstream calls carry `server_name` / `tool_name` (event_bus.go
      /// EmitActivityToolCallCompleted, payload literal at :444-454).
      func testUpstreamCompletedPayloadBecomesToolCallEntry() throws {
          let json = """
          {"payload":{"server_name":"github","tool_name":"create_issue",
          "session_id":"sess-1","request_id":"req-1","source":"mcp","status":"success",
          "error_message":"","duration_ms":142},"timestamp":1753800000}
          """
          let entry = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.tool_call.completed",
              data: Data(json.utf8)
          ))

          XCTAssertEqual(entry.type, "tool_call")
          XCTAssertEqual(entry.serverName, "github")
          XCTAssertEqual(entry.toolName, "create_issue")
          XCTAssertEqual(entry.status, "success")
          XCTAssertEqual(entry.durationMs, 142)
          XCTAssertEqual(entry.sessionId, "sess-1")
          XCTAssertEqual(entry.requestId, "req-1")
          XCTAssertNil(entry.errorMessage, "empty error_message must not become a failure detail")
      }

      /// Internal calls carry `internal_tool_name`, and `target_server` only when
      /// non-empty (event_bus.go EmitActivityInternalToolCall, :552-565). Reading
      /// `tool_name` here would produce a row with no tool at all — and the row
      /// must survive the selection rules and the relative-time formatter, not
      /// merely hold the right field values.
      func testInternalCompletedPayloadUsesInternalToolNameAndTargetServer() throws {
          let json = """
          {"payload":{"internal_tool_name":"call_tool_read","target_server":"jira",
          "target_tool":"get_issue","session_id":"sess-2","request_id":"req-2",
          "status":"error","error_message":"auth failed","duration_ms":9},
          "timestamp":1753800000}
          """
          let entry = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.internal_tool_call.completed",
              data: Data(json.utf8)
          ))

          XCTAssertEqual(entry.type, "internal_tool_call")
          XCTAssertEqual(entry.toolName, "call_tool_read")
          XCTAssertEqual(entry.serverName, "jira")
          XCTAssertEqual(entry.status, "error")
          XCTAssertTrue(GlanceSelection.qualifies(entry),
                        "a failed wrapper qualifies under rule 3")
          XCTAssertEqual(GlanceFormatting.relativeTime(
              entry.timestamp,
              now: Date(timeIntervalSince1970: 1_753_800_012)
          ), "12s", "the tray's own parser must accept the timestamp we emit")
      }

      /// `target_server` is omitted for discovery built-ins such as
      /// retrieve_tools, so the row must tolerate its absence.
      func testInternalPayloadWithoutTargetServerHasNilServerName() throws {
          let json = """
          {"payload":{"internal_tool_name":"retrieve_tools","session_id":"sess-3",
          "request_id":"req-3","status":"success","duration_ms":4},
          "timestamp":1753800000}
          """
          let entry = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.internal_tool_call.completed",
              data: Data(json.utf8)
          ))

          XCTAssertEqual(entry.toolName, "retrieve_tools")
          XCTAssertNil(entry.serverName)
      }

      /// The core does not persist started events (activity_service.go), so a row
      /// built from one would never be reconciled by the poll.
      func testStartedEventIsIgnored() {
          let json = """
          {"payload":{"server_name":"github","tool_name":"create_issue",
          "request_id":"req-4"},"timestamp":1753800000}
          """
          XCTAssertNil(GlanceEvent.adapt(
              eventName: "activity.tool_call.started",
              data: Data(json.utf8)
          ))
      }

      /// A failed upstream call emits BOTH events under ONE request id, and
      /// `ActivityEntry` derives identity and equality from `id` alone — so a bare
      /// request id would make the two records collide before rule 4 could pick
      /// the `tool_call` one. The last assertion is the point of the composite id.
      func testPairedEventsUnderOneRequestIdGetDistinctIds() throws {
          let upstream = """
          {"payload":{"server_name":"jira","tool_name":"get_issue",
          "request_id":"req-5","status":"error","error_message":"auth failed"},
          "timestamp":1753800000}
          """
          let wrapper = """
          {"payload":{"internal_tool_name":"call_tool_read","target_server":"jira",
          "request_id":"req-5","status":"error","error_message":"auth failed"},
          "timestamp":1753800000}
          """
          let a = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.tool_call.completed", data: Data(upstream.utf8)))
          let b = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.internal_tool_call.completed", data: Data(wrapper.utf8)))

          XCTAssertEqual(a.id, "req-5:tool_call")
          XCTAssertEqual(b.id, "req-5:internal_tool_call")
          XCTAssertNotEqual(a, b)
          XCTAssertEqual(a.requestId, b.requestId, "the shared request id is what rule 4 collapses on")
          XCTAssertEqual(GlanceSelection.activityRows(from: [a, b]).map(\.id),
                         ["req-5:tool_call"],
                         "rule 4 keeps the record that names the real server:tool")
      }

      /// The payload key is `error_message`, matching `ActivityEntry` — a read
      /// keyed on `error` would render a failed call as if it had no detail.
      func testFailureDetailComesFromErrorMessageKey() throws {
          let json = """
          {"payload":{"server_name":"jira","tool_name":"get_issue","request_id":"req-6",
          "status":"error","error_message":"auth failed: token expired"},
          "timestamp":1753800000}
          """
          let entry = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.tool_call.completed",
              data: Data(json.utf8)
          ))

          XCTAssertEqual(entry.errorMessage, "auth failed: token expired")
      }

      /// The envelope timestamp is Unix seconds; the entry must carry a string the
      /// tray's OWN parser accepts. Rebuilding an ISO8601DateFormatter here would
      /// let the test pass while GlanceFormatting rejected the string.
      func testEnvelopeUnixSecondsBecomeParsableISO8601() throws {
          let json = """
          {"payload":{"server_name":"github","tool_name":"create_issue",
          "request_id":"req-7","status":"success"},"timestamp":1753800000}
          """
          let entry = try XCTUnwrap(GlanceEvent.adapt(
              eventName: "activity.tool_call.completed",
              data: Data(json.utf8)
          ))

          let parsed = try XCTUnwrap(GlanceFormatting.parseTimestamp(entry.timestamp))
          XCTAssertEqual(parsed.timeIntervalSince1970, 1_753_800_000, accuracy: 0.001)
      }
  }
  ```

- [ ] **Step 2: Run the test and watch it fail to build**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceEventTests
  ```

  Expected — the build fails because the type does not exist yet. SwiftPM prints one `error:` line per reference plus a caret continuation line, then its terse summary:

  ```
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift:19:...: error: cannot find 'GlanceEvent' in scope
       |             `- error: cannot find 'GlanceEvent' in scope
  ...
  error: fatalError
  ```

- [ ] **Step 3: Create the adapter**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceEvent.swift` (the directory already exists alongside `GlanceFormatting.swift` and `GlanceSelection.swift`) with exactly this content:

  ```swift
  // GlanceEvent.swift
  // MCPProxy
  //
  // Adapts `activity.*.completed` SSE payloads into `ActivityEntry` values so the
  // tray glance section can render live rows without issuing a REST request per
  // event.

  import Foundation

  /// Maps runtime activity SSE payloads onto `ActivityEntry`.
  enum GlanceEvent {
      /// SSE event name for a completed upstream tool call.
      static let upstreamCompleted = "activity.tool_call.completed"

      /// SSE event name for a completed internal (built-in) tool call.
      static let internalCompleted = "activity.internal_tool_call.completed"

      /// Build an `ActivityEntry` from an SSE envelope.
      ///
      /// Returns nil for any other event name (notably
      /// `activity.tool_call.started`) and for a payload that does not parse.
      static func adapt(eventName: String, data: Data) -> ActivityEntry? {
          let type: String
          switch eventName {
          case upstreamCompleted: type = "tool_call"
          case internalCompleted: type = "internal_tool_call"
          default: return nil
          }

          guard let envelope = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                let payload = envelope["payload"] as? [String: Any] else { return nil }

          let toolName: String?
          let serverName: String?
          if type == "tool_call" {
              toolName = nonEmptyString(payload["tool_name"])
              serverName = nonEmptyString(payload["server_name"])
          } else {
              toolName = nonEmptyString(payload["internal_tool_name"])
              serverName = nonEmptyString(payload["target_server"])
          }

          let requestId = nonEmptyString(payload["request_id"])
          let provisionalId = requestId.map { "\($0):\(type)" } ?? "sse-\(UUID().uuidString):\(type)"

          let seconds = (envelope["timestamp"] as? NSNumber)?.doubleValue
              ?? Date().timeIntervalSince1970
          let timestamp = isoFormatter.string(from: Date(timeIntervalSince1970: seconds))

          return ActivityEntry(
              id: provisionalId,
              type: type,
              source: nonEmptyString(payload["source"]),
              serverName: serverName,
              toolName: toolName,
              arguments: nil,
              response: nil,
              responseTruncated: nil,
              status: nonEmptyString(payload["status"]) ?? "success",
              errorMessage: nonEmptyString(payload["error_message"]),
              durationMs: (payload["duration_ms"] as? NSNumber)?.int64Value,
              timestamp: timestamp,
              sessionId: nonEmptyString(payload["session_id"]),
              requestId: requestId,
              metadata: nil,
              hasSensitiveData: nil,
              detectionTypes: nil,
              maxSeverity: nil
          )
      }

      private static func nonEmptyString(_ value: Any?) -> String? {
          guard let text = value as? String, !text.isEmpty else { return nil }
          return text
      }

      private static let isoFormatter: ISO8601DateFormatter = {
          let formatter = ISO8601DateFormatter()
          formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
          formatter.timeZone = TimeZone(secondsFromGMT: 0)
          return formatter
      }()
  }
  ```

  Notes on three choices that are not arbitrary. `(payload["duration_ms"] as? NSNumber)?.int64Value` is used because `JSONSerialization` produces `NSNumber`, and a direct `as? Int64` bridge is not guaranteed for every numeric representation. `status` defaults to `"success"` when the key is missing so a malformed payload cannot masquerade as a failed call (`GlanceSelection` rule 3 treats "status is not success" as an inclusion signal). The formatter emits fractional seconds, which `GlanceFormatting.parseTimestamp` accepts on its first attempt (it falls back to a non-fractional parser for polled records) — that pairing is what Step 1's last test pins.

- [ ] **Step 4: Run the test and watch it pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceEventTests
  ```

  Expected tail:

  ```
  Test Case '-[MCPProxyTests.GlanceEventTests testUpstreamCompletedPayloadBecomesToolCallEntry]' passed (0.000 seconds).
  Test Suite 'GlanceEventTests' passed at ...
  	 Executed 7 tests, with 0 failures (0 unexpected) in 0.003 (0.003) seconds
  ```

- [ ] **Step 5: Commit the adapter**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceEvent.swift native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift && git commit -m "feat(tray): adapt activity SSE payloads into ActivityEntry rows"
  ```

- [ ] **Step 6: Write the failing prepend/cap test**

  Append this method to `native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift`, immediately after `testEnvelopeUnixSecondsBecomeParsableISO8601()` and before the closing `}` of the class:

  ```swift
      /// SSE rows go in newest-first and the feed is bounded, so a busy agent
      /// cannot grow `glanceActivity` without limit between reconciling polls.
      func testPrependPutsNewestFirstAndCapsTheFeed() throws {
          let state = AppState()
          state.coreState = .connected

          for index in 0..<(AppState.glanceActivityCap + 5) {
              let json = """
              {"payload":{"server_name":"github","tool_name":"create_issue",
              "request_id":"req-\(index)","status":"success"},"timestamp":1753800000}
              """
              let entry = try XCTUnwrap(GlanceEvent.adapt(
                  eventName: "activity.tool_call.completed",
                  data: Data(json.utf8)
              ))
              state.prependGlanceActivity(entry)
          }

          XCTAssertEqual(state.glanceActivity.count, AppState.glanceActivityCap)
          XCTAssertEqual(state.glanceActivity.first?.requestId, "req-\(AppState.glanceActivityCap + 4)")
          XCTAssertTrue(state.recentActivity.isEmpty, "the shared Dashboard feed must not be touched")
      }
  ```

  `state.coreState = .connected` is required: the AppState glance-fields task installs a `didSet` on `coreState` that calls `clearGlanceState()` while the core is not connected, so a default-constructed `AppState` (which starts at `.idle`) would wipe the feed on each prepend.

- [ ] **Step 7: Run the test and watch it fail to build**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceEventTests
  ```

  Expected (two distinct diagnostics, each with a caret continuation line; line numbers depend on where the method landed):

  ```
  .../MCPProxyTests/GlanceEventTests.swift:...: error: type 'AppState' has no member 'glanceActivityCap'
  .../MCPProxyTests/GlanceEventTests.swift:...: error: value of type 'AppState' has no member 'prependGlanceActivity'
  error: fatalError
  ```

- [ ] **Step 8: Add the bounded prepend helper to AppState**

  In `native/macos/MCPProxy/MCPProxy/State/AppState.swift`, insert the following immediately after the closing `}` of `updateGlanceActivity(_:)` — i.e. directly above the doc comment `/// Replace the glance (active-only) session feed.` (anchor on that comment rather than a line number; sibling tasks are editing this file concurrently):

  ```swift
      /// Upper bound on rows kept in `glanceActivity`. Matches the page size the
      /// reconciling poll requests (`apiClient.glanceActivity(limit: 50)`), so SSE
      /// rows and polled rows agree on depth.
      static let glanceActivityCap = 50

      /// Prepend one optimistic row adapted from an SSE payload (newest first).
      /// Bounded so a busy agent cannot grow the feed without limit; the 30s
      /// reconciling poll replaces the list wholesale with canonical records.
      @MainActor
      func prependGlanceActivity(_ entry: ActivityEntry) {
          glanceActivity.insert(entry, at: 0)
          if glanceActivity.count > AppState.glanceActivityCap {
              glanceActivity.removeLast(glanceActivity.count - AppState.glanceActivityCap)
          }
      }
  ```

  This deliberately does **not** use the id-list equality guard that `updateGlanceSessions(_:)` still carries (`if sessions.map(\.id) != glanceSessions.map(\.id)`): a prepend is by definition a change, and that guard exists only to stop redundant `@Published` churn on identical poll results.

- [ ] **Step 9: Run the test and watch it pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceEventTests
  ```

  Expected tail:

  ```
  Test Case '-[MCPProxyTests.GlanceEventTests testPrependPutsNewestFirstAndCapsTheFeed]' passed (0.001 seconds).
  Test Suite 'GlanceEventTests' passed at ...
  	 Executed 8 tests, with 0 failures (0 unexpected) in 0.004 (0.004) seconds
  ```

- [ ] **Step 10: Commit the prepend helper**

  Stage only this task's hunks — sibling tasks have uncommitted edits in `AppState.swift`, and a blanket `git add` of the file would sweep them into this commit:

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add -p native/macos/MCPProxy/MCPProxy/State/AppState.swift && git add native/macos/MCPProxy/MCPProxyTests/GlanceEventTests.swift && git commit -m "feat(tray): bounded prepend for the glance activity feed"
  ```

- [ ] **Step 11: Replace the dead SSE case in CoreProcessManager**

  In `native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift`, inside `handleSSEEvent(_:)` (the `switch event.event` at line 626; this case occupies lines 683-700), delete this entire block:

  ```swift
          case "activity":
              // New activity; refresh and check for sensitive data
              let oldSensitive = await MainActor.run { appState.sensitiveDataAlertCount }
              await refreshActivity()
              await MainActor.run { appState.activityVersion += 1 }
              let newSensitive = await MainActor.run { appState.sensitiveDataAlertCount }
              // Notify on new sensitive data detections
              if newSensitive > oldSensitive {
                  if let latest = await MainActor.run(body: {
                      appState.recentActivity.first(where: { $0.hasSensitiveData == true })
                  }) {
                      await notificationService.sendSensitiveDataAlert(
                          server: latest.serverName ?? "unknown",
                          tool: latest.toolName ?? "unknown",
                          category: "sensitive data"
                      )
                  }
              }
  ```

  Note what this deletion does and does not cost: it removes the only `sendSensitiveDataAlert` call site in `handleSSEEvent`. No behaviour is lost, because the core has never emitted a bare `activity` event, so the branch — and therefore the alert — has never fired. (Sensitive-data notification on a real event stream is out of scope here; the core's own `sensitive_data.detected` event is unhandled by the tray both before and after this change.) `notificationService` stays in use from the `servers.changed` case, so no unused-property warning appears.

  Put this in its place (same indentation — it is a `case` of the same `switch`):

  ```swift
          case GlanceEvent.upstreamCompleted, GlanceEvent.internalCompleted:
              // Tray Glance: adapt the payload into a row and prepend it.
              // Deliberately NO refreshActivity() here — a REST GET per event is
              // network amplification, not push. The 30s reconciling poll
              // (refreshGlanceActivity) replaces these optimistic rows with the
              // storage-assigned records. `activity.tool_call.started` is ignored:
              // the core does not persist started events, so a row built from one
              // would never be reconciled. `activityVersion` is deliberately NOT
              // bumped either — ActivityView reloads on that counter
              // (ActivityView.swift:174 → loadSummary + loadActivities), so a
              // per-event bump is the same amplification through a second door
              // whenever the Activity window is open.
              guard let data = event.data.data(using: .utf8),
                    let entry = GlanceEvent.adapt(eventName: event.event, data: data) else {
                  break
              }
              await appState.prependGlanceActivity(entry)
  ```

  `case GlanceEvent.upstreamCompleted, ...` matches on `static let` string constants — legal in a Swift `switch` over `String` (expression-pattern `~=`) and it keeps the event names in one place. `await appState.prependGlanceActivity(entry)` hops to the main actor exactly like the existing `await appState.updateServers(servers)` call at line 654.

- [ ] **Step 12: Verify no amplification and no dead case remain**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && \
    grep -n 'case "activity":' MCPProxy/Core/CoreProcessManager.swift; \
    grep -c 'await refreshActivity()' MCPProxy/Core/CoreProcessManager.swift; \
    grep -c 'activityVersion' MCPProxy/Core/CoreProcessManager.swift
  ```

  Expected: the first `grep` prints nothing (exit status 1 — the dead case is gone); the second prints `1`; the third prints `2`.

  Counts, not line numbers, are the assertion here: line numbers in this file shift as sibling tasks land. One `refreshActivity()` means the only remaining call is the one in `refreshState()` (the periodic/refresh path), and two `activityVersion` hits mean the `config.reloaded` bump and the `refreshState()` bump — neither of them on the SSE tool-call path. If the third count is 3, the `activityVersion += 1` line was left in the new case and every completed tool call will trigger `loadSummary()` + `loadActivities()` in an open Activity window.

- [ ] **Step 13: Run the whole Swift suite (what CI runs)**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test
  ```

  Baseline before this task is 323 tests, so expect 331 (the total grows further as sibling tasks land; the invariants are zero failures and the 8 `GlanceEventTests` cases among them):

  ```
  	 Executed 8 tests, with 0 failures (0 unexpected) in 0.004 (0.004) seconds
  	 Executed 331 tests, with 0 failures (0 unexpected) in 0.086 (0.103) seconds
  ✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
  ```

  (The trailing swift-testing line is normal — this package has no swift-testing tests, only XCTest.)

- [ ] **Step 14: Commit the SSE handler**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add -p native/macos/MCPProxy/MCPProxy/Core/CoreProcessManager.swift && git commit -m "fix(tray): consume activity.*.completed SSE events without refetching"
  ```

  `-p` again: the AppState-fields/API-client tasks also touch this file (`refreshGlanceActivity`, `refreshGlanceSessions`, `refreshUsage`), and those hunks belong to their own commits.

---

### Task 5: Swift — `GlanceSelection` and `GlanceFormatting` (pure logic, TDD)

The tray glance section's display rules and text formatting, as pure Swift functions with no AppKit and no networking. This is where the bulk of the design's logic tests live. Everything here is synchronous, value-in/value-out, and independently runnable via `swift test`.

**Files:**

- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceFormatting.swift`
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift`
- Test (create): `native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift`
- Test (create): `native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift`
- Test (create): `native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift`
- Read-only (do not edit): `native/macos/MCPProxy/MCPProxy/API/Models.swift` lines 536–572 (`ActivityEntry`), `native/macos/MCPProxy/MCPProxy/API/APIClient.swift` lines 330–355 (`APIClient.MCPSession`)

No `Package.swift` change is needed. `native/macos/MCPProxy/Package.swift` declares both targets with `path:`-based globbing (`path: "MCPProxy"` and `path: "MCPProxyTests"`), which recurses into subdirectories, so dropping files into `MCPProxy/Menu/Glance/` and `MCPProxyTests/` is enough for `swift test` to pick them up.

**Interfaces:**

*Consumes* (all already exist on `main`; this task adds no dependency on any other task in this plan):

- `struct ActivityEntry: Codable, Identifiable, Equatable` — `native/macos/MCPProxy/MCPProxy/API/Models.swift:536`. Fields read here: `id: String`, `type: String`, `serverName: String?` (`server_name`), `toolName: String?` (`tool_name`), `status: String`, `requestId: String?` (`request_id`), `timestamp: String` (RFC3339). Note `==` compares `id` only (`Models.swift:570`), which is why the tests below assert on `.id` and never on whole values.
- `struct APIClient.MCPSession: Codable, Identifiable` — `native/macos/MCPProxy/MCPProxy/API/APIClient.swift:331`. Fields read here: `id: String`, `status: String`. It is *not* `Equatable`, so tests compare `sessions.map(\.id)`.

*Produces* (later tasks — the `GlanceSection` menu builder and the SSE `GlanceEvent` adapter — call exactly these):

```swift
enum GlanceSelection {
    static let managementBuiltIns: Set<String>      // ["upstream_servers", "quarantine_security"]
    static let glanceInternalTools: Set<String>     // ["retrieve_tools", "code_execution", "describe_tool"]
    static let rowLimit: Int                        // 5
    static func qualifies(_ entry: ActivityEntry) -> Bool
    static func collapseByRequestID(_ entries: [ActivityEntry]) -> [ActivityEntry]
    static func activityRows(from entries: [ActivityEntry], limit: Int = rowLimit) -> [ActivityEntry]
    static func activeClients(from sessions: [APIClient.MCPSession], limit: Int = rowLimit) -> [APIClient.MCPSession]
}

enum GlanceFormatting {
    static func statusSymbolName(for entry: ActivityEntry) -> String
    static func rowLabel(for entry: ActivityEntry) -> String
    static func middleTruncated(_ text: String, limit: Int) -> String
    static func parseTimestamp(_ timestamp: String) -> Date?
    static func compactAge(_ seconds: TimeInterval) -> String
    static func relativeTime(_ timestamp: String, now: Date = Date()) -> String
}
```

Also produced, for reuse by sibling test files in the same target: the static fixture factories `GlanceSelectionTests.entry(id:type:server:tool:status:requestId:) -> ActivityEntry` and `GlanceSelectionTests.session(id:status:clientName:) -> APIClient.MCPSession`.

---

- [ ] **Step 1: Create the Glance directory and the failing formatting test**

  Run this first so the directory exists:

  ```bash
  mkdir -p /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxy/Menu/Glance
  ```

  Then create `native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift` with exactly this content:

  ```swift
  import XCTest
  @testable import MCPProxy

  final class GlanceFormattingTests: XCTestCase {

      // MARK: - Status symbol

      func testStatusSymbolDistinguishesSuccessErrorAndOther() {
          XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "success")), "checkmark.circle")
          XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "error")), "xmark.circle")
          XCTAssertEqual(GlanceFormatting.statusSymbolName(for: Self.entry(status: "blocked")), "exclamationmark.circle")
      }

      // MARK: - Row label

      func testUpstreamCallLabelIsServerColonTool() {
          let entry = Self.entry(type: "tool_call", server: "github", tool: "create_issue")
          XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "github:create_issue")
      }

      func testBuiltInLabelIsJustTheToolName() {
          let entry = Self.entry(type: "internal_tool_call", tool: "retrieve_tools")
          XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "retrieve_tools")
      }

      func testAlreadyPrefixedToolNameIsNotDoubled() {
          let entry = Self.entry(type: "tool_call", server: "github", tool: "github:create_issue")
          XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "github:create_issue")
      }

      func testLabelFallsBackToTypeWhenNothingIsNamed() {
          let entry = Self.entry(type: "oauth_event")
          XCTAssertEqual(GlanceFormatting.rowLabel(for: entry), "oauth_event")
      }

      // MARK: - Truncation

      func testShortTextIsNotTruncated() {
          XCTAssertEqual(GlanceFormatting.middleTruncated("github:create", limit: 20), "github:create")
      }

      func testMiddleTruncationKeepsHeadAndTailAtExactlyTheLimit() {
          let result = GlanceFormatting.middleTruncated("github:create_issue_from_template", limit: 12)
          XCTAssertEqual(result.count, 12)
          XCTAssertTrue(result.hasPrefix("github"), "kept the server prefix, got \(result)")
          XCTAssertTrue(result.hasSuffix("late"), "kept the tool tail, got \(result)")
          XCTAssertTrue(result.contains("\u{2026}"))
      }

      func testTruncationToOneCharacterIsJustTheEllipsis() {
          XCTAssertEqual(GlanceFormatting.middleTruncated("abcdef", limit: 1), "\u{2026}")
      }

      // MARK: - Relative time

      func testCompactAgeUnits() {
          XCTAssertEqual(GlanceFormatting.compactAge(0), "0s")
          XCTAssertEqual(GlanceFormatting.compactAge(12), "12s")
          XCTAssertEqual(GlanceFormatting.compactAge(59), "59s")
          XCTAssertEqual(GlanceFormatting.compactAge(60), "1m")
          XCTAssertEqual(GlanceFormatting.compactAge(3599), "59m")
          XCTAssertEqual(GlanceFormatting.compactAge(3600), "1h")
          XCTAssertEqual(GlanceFormatting.compactAge(86_400), "1d")
          XCTAssertEqual(GlanceFormatting.compactAge(-5), "0s")
      }

      func testRelativeTimeParsesFractionalAndPlainTimestamps() {
          let fractional = "2027-01-15T08:00:00.123Z"
          let plain = "2027-01-15T08:00:00Z"
          XCTAssertNotNil(GlanceFormatting.parseTimestamp(fractional))
          XCTAssertNotNil(GlanceFormatting.parseTimestamp(plain))

          let base = GlanceFormatting.parseTimestamp(plain)!
          XCTAssertEqual(GlanceFormatting.relativeTime(plain, now: base.addingTimeInterval(12)), "12s")
          XCTAssertEqual(GlanceFormatting.relativeTime(fractional, now: base.addingTimeInterval(180)), "3m")
      }

      func testUnparseableTimestampFallsBackToTheRawString() {
          XCTAssertEqual(GlanceFormatting.relativeTime("not-a-date"), "not-a-date")
      }

      // MARK: - Helpers

      static func entry(
          id: String = "a1",
          type: String = "tool_call",
          server: String? = nil,
          tool: String? = nil,
          status: String = "success"
      ) -> ActivityEntry {
          var json: [String: Any] = [
              "id": id,
              "type": type,
              "status": status,
              "timestamp": "2027-01-15T08:00:00Z"
          ]
          if let server { json["server_name"] = server }
          if let tool { json["tool_name"] = tool }
          let data = try! JSONSerialization.data(withJSONObject: json)
          // swiftlint:disable:next force_try
          return try! JSONDecoder().decode(ActivityEntry.self, from: data)
      }
  }
  ```

  The fixture builds an `ActivityEntry` by decoding JSON through the real `Codable` path rather than calling a memberwise initialiser. That is the established convention in this test target (see `MCPProxyTests/AppStateStatusSummaryTests.swift:50-66`, the `makeServer` helper under its `// MARK: - Helpers`) and it keeps the fixtures working when fields are added to `ActivityEntry`.

  The `testRelativeTimeParsesFractionalAndPlainTimestamps` `3m` assertion depends on rounding: the fractional timestamp is 0.123 s later than `base`, so the age is 179.877 s, which `compactAge` rounds to 180 before dividing. Do not "simplify" the rounding out of the implementation in Step 3.

- [ ] **Step 2: Run the formatting test and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceFormattingTests
  ```

  Expected output — the build fails because the type does not exist yet (this is the RED state; in Swift a missing symbol is a compile error, not a runtime assertion failure). Swift emits one diagnostic per reference, so the same error repeats down the file:

  ```
  [52/63] Compiling MCPProxyTests GlanceFormattingTests.swift
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift:9:24: error: cannot find 'GlanceFormatting' in scope
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift:10:24: error: cannot find 'GlanceFormatting' in scope
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift:11:24: error: cannot find 'GlanceFormatting' in scope
  ... (one per call site, through line 79) ...
  error: fatalError
  ```

  The `[52/63]` build-progress index varies from run to run and with the number of files in the target — do not treat a different number as a signal. Each diagnostic is also followed by a source-context block (the surrounding lines with a caret), elided above.

  A `warning: 'mcpproxy': found 4 file(s) which are unhandled` line about `Info.plist`, `MCPProxy.entitlements`, `mcpproxy.icns` and `Assets.xcassets` is printed on every build in this package, as is a `MCPProxyApp.swift:1039` "expression is 'async' but is not marked with 'await'" warning. Both are pre-existing and unrelated — ignore them.

- [ ] **Step 3: Implement `GlanceFormatting`**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceFormatting.swift` with exactly this content:

  ```swift
  // GlanceFormatting.swift
  // MCPProxy
  //
  // Pure presentation helpers for the tray glance section: status symbol,
  // row label, middle truncation, and compact relative age.
  // Salvaged from the retired Menu/TrayMenu.swift.

  import Foundation

  /// Pure, AppKit-free formatting helpers for glance rows.
  enum GlanceFormatting {

      // MARK: - Status symbol

      /// SF Symbol name for an activity record's outcome.
      ///
      /// Shape carries the meaning (never colour alone), so a checkmark, a
      /// cross and an exclamation mark are three distinct glyphs.
      static func statusSymbolName(for entry: ActivityEntry) -> String {
          switch entry.status {
          case "success":
              return "checkmark.circle"
          case "error":
              return "xmark.circle"
          default:
              return "exclamationmark.circle"
          }
      }

      // MARK: - Row label

      /// Compose the row's primary label.
      ///
      /// Upstream calls read `server:tool`; discovery/execution built-ins read
      /// just the built-in's name because they have no upstream server.
      static func rowLabel(for entry: ActivityEntry) -> String {
          let tool = entry.toolName ?? ""
          let server = entry.serverName ?? ""

          if entry.type == "tool_call", !server.isEmpty, !tool.isEmpty {
              // Guard against a tool name that already carries the prefix.
              if tool.hasPrefix("\(server):") {
                  return tool
              }
              return "\(server):\(tool)"
          }
          if !tool.isEmpty {
              return tool
          }
          if !server.isEmpty {
              return server
          }
          return entry.type
      }

      // MARK: - Truncation

      /// Middle-truncate `text` to at most `limit` characters, keeping the head
      /// (the server prefix) and the tail (the end of the tool name).
      static func middleTruncated(_ text: String, limit: Int) -> String {
          guard limit > 0 else { return "" }
          guard text.count > limit else { return text }
          let keep = limit - 1                 // one slot for the ellipsis
          let head = keep / 2 + keep % 2
          let tail = keep - head
          let prefix = String(text.prefix(head))
          let suffix = tail > 0 ? String(text.suffix(tail)) : ""
          return prefix + "\u{2026}" + suffix
      }

      // MARK: - Time

      private static let fractionalParser: ISO8601DateFormatter = {
          let f = ISO8601DateFormatter()
          f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
          return f
      }()

      private static let plainParser: ISO8601DateFormatter = {
          let f = ISO8601DateFormatter()
          f.formatOptions = [.withInternetDateTime]
          return f
      }()

      /// Parse an RFC3339/ISO-8601 timestamp with or without fractional seconds.
      static func parseTimestamp(_ timestamp: String) -> Date? {
          if let date = fractionalParser.date(from: timestamp) { return date }
          return plainParser.date(from: timestamp)
      }

      /// Compact, locale-independent age string: `12s`, `3m`, `5h`, `2d`.
      static func compactAge(_ seconds: TimeInterval) -> String {
          let total = max(0, Int(seconds.rounded()))
          if total < 60 { return "\(total)s" }
          let minutes = total / 60
          if minutes < 60 { return "\(minutes)m" }
          let hours = minutes / 60
          if hours < 24 { return "\(hours)h" }
          return "\(hours / 24)d"
      }

      /// Relative age of an activity timestamp, falling back to the raw string
      /// when it cannot be parsed.
      static func relativeTime(_ timestamp: String, now: Date = Date()) -> String {
          guard let date = parseTimestamp(timestamp) else { return timestamp }
          return compactAge(now.timeIntervalSince(date))
      }
  }
  ```

  Three deliberate departures from the salvaged `Menu/TrayMenu.swift` originals (that file is dead code — nothing in the target references `TrayMenu`; the live menu is built imperatively in `MCPProxyApp.swift`).

  First, `statusSymbolName` drops the `hasSensitiveData` branch that `TrayMenu.activityIcon(for:)` had (`Menu/TrayMenu.swift:419-429`) — sensitive-data badges on activity rows are an explicit non-goal of the design (`2026-07-29-tray-glance-design.md:176`).

  Second, `statusSymbolName` is three-way where `activityIcon` was two-way: the original returned `"checkmark.circle"` for *everything* that was not `"error"`, which would have drawn a green tick on a `blocked` or `timeout` record. The new default arm returns `"exclamationmark.circle"`, and `testStatusSymbolDistinguishesSuccessErrorAndOther`'s `"blocked"` case pins it.

  Third, `relativeTime` does **not** use `RelativeDateTimeFormatter` the way `TrayMenu.relativeTime(_:)` did (`Menu/TrayMenu.swift:446-463`): that produces locale-dependent strings like `"12 sec. ago"`, whereas the design's row layout calls for the compact right-aligned `12s` / `3m` / `5h`. `compactAge` is locale-free, which also makes it deterministically testable on any CI runner.

- [ ] **Step 4: Run the formatting test and watch it pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceFormattingTests
  ```

  Expected output (tail):

  ```
  Test Suite 'GlanceFormattingTests' passed at 2026-07-29 15:19:37.808.
  	 Executed 11 tests, with 0 failures (0 unexpected) in 0.003 (0.003) seconds
  Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-29 15:19:37.808.
  	 Executed 11 tests, with 0 failures (0 unexpected) in 0.003 (0.003) seconds
  Test Suite 'Selected tests' passed at 2026-07-29 15:19:37.808.
  	 Executed 11 tests, with 0 failures (0 unexpected) in 0.003 (0.009) seconds
  ◇ Test run started.
  ↳ Testing Library Version: 1902
  ↳ Target Platform: arm64e-apple-macos14.0
  ✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
  ```

  The trailing `Test run with 0 tests in 0 suites passed` block is the swift-testing runner, which executes alongside XCTest. This package has no swift-testing tests, so zero is correct — it is not a failure.

- [ ] **Step 5: Commit `GlanceFormatting`**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceFormatting.swift native/macos/MCPProxy/MCPProxyTests/GlanceFormattingTests.swift && git commit -m "feat(tray): add GlanceFormatting pure helpers for glance rows

  Status symbol, server:tool row label, middle truncation and a compact
  locale-free relative age, salvaged from the dead Menu/TrayMenu.swift.
  Eleven unit tests; no AppKit, no networking."
  ```

- [ ] **Step 6: Write the failing selection test for rules 1-3**

  Create `native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift` with exactly this content:

  ```swift
  import XCTest
  @testable import MCPProxy

  final class GlanceSelectionTests: XCTestCase {

      // MARK: - Rule 2

      func testUpstreamToolCallIsAlwaysIncluded() {
          let entry = Self.entry(id: "1", type: "tool_call", server: "github", tool: "create_issue")
          XCTAssertTrue(GlanceSelection.qualifies(entry))
      }

      // MARK: - Rule 3

      func testDiscoveryAndExecutionBuiltInsAreIncluded() {
          for tool in ["retrieve_tools", "code_execution", "describe_tool"] {
              let entry = Self.entry(id: tool, type: "internal_tool_call", tool: tool)
              XCTAssertTrue(GlanceSelection.qualifies(entry), "\(tool) should qualify")
          }
      }

      func testSuccessfulCallToolWrapperIsExcluded() {
          let entry = Self.entry(id: "w", type: "internal_tool_call", tool: "call_tool_write")
          XCTAssertFalse(GlanceSelection.qualifies(entry))
      }

      func testFailedCallToolWrapperIsIncluded() {
          let entry = Self.entry(id: "w", type: "internal_tool_call", tool: "call_tool_write", status: "error")
          XCTAssertTrue(GlanceSelection.qualifies(entry), "pre-dispatch failures have no upstream record")
      }

      // MARK: - Rule 1 beats rule 3

      func testManagementBuiltInsAreExcludedEvenWhenTheyFail() {
          for tool in ["upstream_servers", "quarantine_security"] {
              let ok = Self.entry(id: "\(tool)-ok", type: "internal_tool_call", tool: tool)
              let bad = Self.entry(id: "\(tool)-bad", type: "internal_tool_call", tool: tool, status: "error")
              XCTAssertFalse(GlanceSelection.qualifies(ok), "\(tool) success must be excluded")
              XCTAssertFalse(GlanceSelection.qualifies(bad), "\(tool) failure must be excluded (rule 1 beats rule 3)")
          }
      }

      func testNonActivityTypesAreExcluded() {
          XCTAssertFalse(GlanceSelection.qualifies(Self.entry(id: "s", type: "security_scan")))
          XCTAssertFalse(GlanceSelection.qualifies(Self.entry(id: "o", type: "oauth_event", status: "error")))
      }

      // MARK: - Helpers

      static func entry(
          id: String,
          type: String,
          server: String? = nil,
          tool: String? = nil,
          status: String = "success",
          requestId: String? = nil
      ) -> ActivityEntry {
          var json: [String: Any] = [
              "id": id,
              "type": type,
              "status": status,
              "timestamp": "2027-01-15T08:00:00Z"
          ]
          if let server { json["server_name"] = server }
          if let tool { json["tool_name"] = tool }
          if let requestId { json["request_id"] = requestId }
          let data = try! JSONSerialization.data(withJSONObject: json)
          // swiftlint:disable:next force_try
          return try! JSONDecoder().decode(ActivityEntry.self, from: data)
      }

      static func session(id: String, status: String, clientName: String = "Claude Code") -> APIClient.MCPSession {
          let json: [String: Any] = [
              "id": id,
              "status": status,
              "client_name": clientName,
              "tool_call_count": 3
          ]
          let data = try! JSONSerialization.data(withJSONObject: json)
          // swiftlint:disable:next force_try
          return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
      }
  }
  ```

  `entry` and `session` are deliberately non-`private` statics: the collapse tests added in Step 11 live in a second file and reuse them. `session` is unused until Step 11 — Swift does not warn about unused methods, so this compiles cleanly now. Every `MCPSession` field other than `id` and `status` is optional (`APIClient.swift:332-341`), so the four-key JSON decodes.

- [ ] **Step 7: Run the selection test and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSelectionTests
  ```

  Expected output (one diagnostic per reference, elided after the first three):

  ```
  [52/63] Compiling MCPProxyTests GlanceSelectionTests.swift
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift:10:23: error: cannot find 'GlanceSelection' in scope
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift:18:27: error: cannot find 'GlanceSelection' in scope
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift:24:24: error: cannot find 'GlanceSelection' in scope
  ...
  error: fatalError
  ```

- [ ] **Step 8: Implement rules 1-3 in `GlanceSelection`**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift` with exactly this content (Step 13 replaces this file with the full version — this is the minimal implementation that turns Step 7's tests green and nothing more):

  ```swift
  // GlanceSelection.swift
  // MCPProxy
  //
  // Display rules for the tray glance section: which activity records qualify
  // as rows, how duplicates collapse, and which sessions count as clients.
  // Pure functions over ActivityEntry / APIClient.MCPSession — no AppKit.

  import Foundation

  /// Presentation policy for the glance section. Pure and synchronous.
  enum GlanceSelection {

      /// Proxy administration built-ins. Never shown, whatever their status.
      static let managementBuiltIns: Set<String> = ["upstream_servers", "quarantine_security"]

      /// Discovery/execution built-ins that are worth a row even on success.
      static let glanceInternalTools: Set<String> = ["retrieve_tools", "code_execution", "describe_tool"]

      // MARK: - Rules 1-3

      /// Whether a single record qualifies for a glance row.
      static func qualifies(_ entry: ActivityEntry) -> Bool {
          let tool = entry.toolName ?? ""

          // Rule 1 — management built-ins are excluded, whatever the status.
          if managementBuiltIns.contains(tool) { return false }

          // Rule 2 — every real upstream call.
          if entry.type == "tool_call" { return true }

          // Rule 3 — discovery/execution built-ins, plus any internal failure
          // (a wrapper that died before dispatch has no upstream record).
          if entry.type == "internal_tool_call" {
              return glanceInternalTools.contains(tool) || entry.status != "success"
          }

          return false
      }
  }
  ```

  The `return false` at the end is what excludes every other activity type — `security_scan`, `oauth_event`, quarantine changes — from the glance rows, while leaving `AppState.recentActivity` (which the native Dashboard renders in full) untouched.

- [ ] **Step 9: Run the selection test and watch it pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSelectionTests
  ```

  Expected output (tail):

  ```
  Test Suite 'GlanceSelectionTests' passed at 2026-07-29 15:19:37.808.
  	 Executed 6 tests, with 0 failures (0 unexpected) in 0.001 (0.001) seconds
  Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-29 15:19:37.808.
  	 Executed 6 tests, with 0 failures (0 unexpected) in 0.001 (0.001) seconds
  Test Suite 'Selected tests' passed at 2026-07-29 15:19:37.808.
  	 Executed 6 tests, with 0 failures (0 unexpected) in 0.001 (0.005) seconds
  ```

- [ ] **Step 10: Commit the qualification rules**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSelectionTests.swift && git commit -m "feat(tray): add GlanceSelection qualification rules 1-3

  Management built-ins (upstream_servers, quarantine_security) are excluded
  whatever their status; every tool_call qualifies; internal_tool_call
  qualifies for the three discovery/execution built-ins or on any failure,
  so a pre-dispatch call_tool_* failure still gets a row."
  ```

- [ ] **Step 11: Write the failing test for rule 4, capping, and the clients helper**

  Create `native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift` with exactly this content:

  ```swift
  import XCTest
  @testable import MCPProxy

  final class GlanceSelectionCollapseTests: XCTestCase {

      // MARK: - Rule 4

      func testPairedFailureCollapsesToTheUpstreamRecord() {
          let entries = [
              GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                         status: "error", requestId: "req-1"),
              GlanceSelectionTests.entry(id: "upstream", type: "tool_call", server: "jira", tool: "get_issue",
                                         status: "error", requestId: "req-1")
          ]
          let rows = GlanceSelection.activityRows(from: entries)
          XCTAssertEqual(rows.count, 1)
          XCTAssertEqual(rows[0].id, "upstream")
          XCTAssertEqual(rows[0].serverName, "jira")
      }

      func testPreDispatchWrapperFailureWithNoPairStillRenders() {
          let entries = [
              GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                         status: "error", requestId: "req-2")
          ]
          let rows = GlanceSelection.activityRows(from: entries)
          XCTAssertEqual(rows.map(\.id), ["wrapper"])
      }

      func testRecordsWithoutRequestIDsAreNeverCollapsed() {
          let entries = [
              GlanceSelectionTests.entry(id: "a", type: "tool_call", server: "s", tool: "t"),
              GlanceSelectionTests.entry(id: "b", type: "tool_call", server: "s", tool: "t")
          ]
          XCTAssertEqual(GlanceSelection.activityRows(from: entries).map(\.id), ["a", "b"])
      }

      func testCollapsedRowKeepsTheGroupsRecencyPosition() {
          let entries = [
              GlanceSelectionTests.entry(id: "newest", type: "tool_call", server: "a", tool: "t", requestId: "r-9"),
              GlanceSelectionTests.entry(id: "wrapper", type: "internal_tool_call", tool: "call_tool_read",
                                         status: "error", requestId: "r-8"),
              GlanceSelectionTests.entry(id: "upstream", type: "tool_call", server: "b", tool: "t",
                                         status: "error", requestId: "r-8")
          ]
          XCTAssertEqual(GlanceSelection.activityRows(from: entries).map(\.id), ["newest", "upstream"])
      }

      // MARK: - Capping over a realistic page

      func testFiveRowsAreSelectedFromAFiftyRecordPageFullOfNoise() {
          var page: [ActivityEntry] = []
          // 40 management-built-in calls arrive first — rule 1 must drop them all.
          for i in 0..<40 {
              page.append(GlanceSelectionTests.entry(
                  id: "mgmt-\(i)", type: "internal_tool_call",
                  tool: i.isMultiple(of: 2) ? "upstream_servers" : "quarantine_security"))
          }
          // 4 successful wrappers — rule 3 drops them.
          for i in 0..<4 {
              page.append(GlanceSelectionTests.entry(
                  id: "wrap-\(i)", type: "internal_tool_call", tool: "call_tool_read"))
          }
          // 6 real calls — only the first five become rows.
          for i in 0..<6 {
              page.append(GlanceSelectionTests.entry(
                  id: "call-\(i)", type: "tool_call", server: "srv", tool: "tool\(i)"))
          }
          XCTAssertEqual(page.count, 50)

          let rows = GlanceSelection.activityRows(from: page)
          XCTAssertEqual(rows.map(\.id), ["call-0", "call-1", "call-2", "call-3", "call-4"])
      }

      func testFewerThanFiveQualifyingRecordsYieldsWhatThereIs() {
          let rows = GlanceSelection.activityRows(from: [
              GlanceSelectionTests.entry(id: "call-0", type: "tool_call", server: "srv", tool: "t")
          ])
          XCTAssertEqual(rows.count, 1)
      }

      func testEmptyInputYieldsNoRows() {
          XCTAssertTrue(GlanceSelection.activityRows(from: []).isEmpty)
      }

      // MARK: - Clients

      func testActiveClientsFiltersClosedSessionsAndCapsAtFive() {
          var sessions = [GlanceSelectionTests.session(id: "closed-1", status: "closed")]
          for i in 0..<7 {
              sessions.append(GlanceSelectionTests.session(id: "active-\(i)", status: "active"))
          }
          sessions.append(GlanceSelectionTests.session(id: "closed-2", status: "closed"))

          let clients = GlanceSelection.activeClients(from: sessions)
          XCTAssertEqual(clients.map(\.id), ["active-0", "active-1", "active-2", "active-3", "active-4"])
      }

      func testActiveClientsIsEmptyWhenEverySessionIsClosed() {
          let sessions = [
              GlanceSelectionTests.session(id: "a", status: "closed"),
              GlanceSelectionTests.session(id: "b", status: "closed")
          ]
          XCTAssertTrue(GlanceSelection.activeClients(from: sessions).isEmpty)
      }
  }
  ```

  `testFiveRowsAreSelectedFromAFiftyRecordPageFullOfNoise` uses 50 records on purpose: that is the page size the production activity poll requests, and 40 leading management-built-in records is the exact burst scenario the design's oversized page exists to survive.

- [ ] **Step 12: Run the collapse test and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSelectionCollapseTests
  ```

  Expected output — `GlanceSelection` exists now, so the errors are missing members rather than a missing type:

  ```
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift:15:36: error: type 'GlanceSelection' has no member 'activityRows'
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift:26:36: error: type 'GlanceSelection' has no member 'activityRows'
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift:27:33: error: cannot infer key path type from context; consider explicitly specifying a root type
  ... (the same pair repeats for every activityRows call site: 35, 46, 68, 73, 80) ...
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift:92:39: error: type 'GlanceSelection' has no member 'activeClients'
  /Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift:98:39: error: type 'GlanceSelection' has no member 'activeClients'
  error: fatalError
  ```

  The odder-looking key-path errors are knock-ons: with `activityRows` / `activeClients` unresolved the compiler cannot type `rows.map(\.id)`. They disappear with the missing members.

- [ ] **Step 13: Implement rule 4, `activityRows` and `activeClients`**

  Replace the entire contents of `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift` with this (rules 1-3 are unchanged; `rowLimit`, `collapseByRequestID`, `activityRows` and `activeClients` are new):

  ```swift
  // GlanceSelection.swift
  // MCPProxy
  //
  // Display rules for the tray glance section: which activity records qualify
  // as rows, how duplicates collapse, and which sessions count as clients.
  // Pure functions over ActivityEntry / APIClient.MCPSession — no AppKit.

  import Foundation

  /// Presentation policy for the glance section. Pure and synchronous.
  enum GlanceSelection {

      /// Proxy administration built-ins. Never shown, whatever their status.
      static let managementBuiltIns: Set<String> = ["upstream_servers", "quarantine_security"]

      /// Discovery/execution built-ins that are worth a row even on success.
      static let glanceInternalTools: Set<String> = ["retrieve_tools", "code_execution", "describe_tool"]

      /// How many rows each list shows.
      static let rowLimit = 5

      // MARK: - Rules 1-3

      /// Whether a single record qualifies for a glance row.
      static func qualifies(_ entry: ActivityEntry) -> Bool {
          let tool = entry.toolName ?? ""

          // Rule 1 — management built-ins are excluded, whatever the status.
          if managementBuiltIns.contains(tool) { return false }

          // Rule 2 — every real upstream call.
          if entry.type == "tool_call" { return true }

          // Rule 3 — discovery/execution built-ins, plus any internal failure
          // (a wrapper that died before dispatch has no upstream record).
          if entry.type == "internal_tool_call" {
              return glanceInternalTools.contains(tool) || entry.status != "success"
          }

          return false
      }

      // MARK: - Rule 4

      /// Collapse records sharing a `request_id`, keeping the `tool_call` one.
      ///
      /// The surviving record is emitted at the position of the first record of
      /// its group so recency ordering is preserved. Records with no request id
      /// are never collapsed.
      static func collapseByRequestID(_ entries: [ActivityEntry]) -> [ActivityEntry] {
          var winners: [String: ActivityEntry] = [:]
          for entry in entries {
              guard let rid = entry.requestId, !rid.isEmpty else { continue }
              guard let existing = winners[rid] else {
                  winners[rid] = entry
                  continue
              }
              if existing.type != "tool_call" && entry.type == "tool_call" {
                  winners[rid] = entry
              }
          }

          var emitted = Set<String>()
          var result: [ActivityEntry] = []
          for entry in entries {
              guard let rid = entry.requestId, !rid.isEmpty else {
                  result.append(entry)
                  continue
              }
              if emitted.contains(rid) { continue }
              emitted.insert(rid)
              result.append(winners[rid] ?? entry)
          }
          return result
      }

      // MARK: - Public entry points

      /// Rules 1-4 applied in order, then the first `limit` survivors.
      static func activityRows(from entries: [ActivityEntry], limit: Int = rowLimit) -> [ActivityEntry] {
          let qualified = entries.filter(qualifies)
          return Array(collapseByRequestID(qualified).prefix(limit))
      }

      /// Sessions currently connected, capped at `limit`, input order preserved.
      static func activeClients(
          from sessions: [APIClient.MCPSession],
          limit: Int = rowLimit
      ) -> [APIClient.MCPSession] {
          Array(sessions.filter { $0.status == "active" }.prefix(limit))
      }
  }
  ```

  The two-pass structure of `collapseByRequestID` is what makes `testCollapsedRowKeepsTheGroupsRecencyPosition` pass: pass one picks the winner per request id, pass two emits it at the *first* position that group occupies. A single-pass "keep the last `tool_call` seen" would reorder rows whenever the wrapper record arrived before its upstream partner, which is exactly the order the two SSE events fire in.

- [ ] **Step 14: Run all three Glance test classes and watch them pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter "GlanceSelection|GlanceFormatting"
  ```

  Expected output (tail) — 11 formatting + 6 qualification + 9 collapse/clients = 26:

  ```
  Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-29 15:20:24.799.
  	 Executed 26 tests, with 0 failures (0 unexpected) in 0.004 (0.006) seconds
  Test Suite 'Selected tests' passed at 2026-07-29 15:20:24.799.
  	 Executed 26 tests, with 0 failures (0 unexpected) in 0.004 (0.011) seconds
  ◇ Test run started.
  ↳ Testing Library Version: 1902
  ↳ Target Platform: arm64e-apple-macos14.0
  ✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
  ```

- [ ] **Step 15: Run the whole native suite — the exact command CI runs**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test
  ```

  Expected output (tail):

  ```
  Test Suite 'MCPProxyPackageTests.xctest' passed at 2026-07-29 15:21:03.913.
  	 Executed 315 tests, with 0 failures (0 unexpected) in 0.073 (0.083) seconds
  Test Suite 'All tests' passed at 2026-07-29 15:21:03.913.
  	 Executed 315 tests, with 0 failures (0 unexpected) in 0.073 (0.088) seconds
  ```

  289 of those are the pre-existing suite and 26 are new here; the total climbs as sibling tasks in this plan land their own tests, so treat `0 failures` as the pass criterion rather than the count. This is verbatim what the `swift-test` job in `.github/workflows/native-tests.yml:83` runs (`working-directory: native/macos/MCPProxy`, `run: swift test`). Do not add `--skip` to make it green — that workflow carries an inline comment forbidding it.

- [ ] **Step 16: Commit rule 4, capping, and the clients helper**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSelectionCollapseTests.swift && git commit -m "feat(tray): collapse paired glance records and cap the row lists

  A failed upstream call persists both a tool_call and a call_tool_* wrapper
  under one request id; rule 4 keeps the tool_call one because it names the
  real server:tool. Adds activityRows() (rules 1-4 then first five) and
  activeClients() (status==active, first five). Nine more unit tests."
  ```

---

### Task 6: Swift — GlanceSection menu items + in-place row updates

**Files:**
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift`
- Test (create): `native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift`
- No existing file is modified. `native/macos/MCPProxy/Package.swift` uses path-based globbing (`.executableTarget(name: "MCPProxy", path: "MCPProxy")`, `.testTarget(name: "MCPProxyTests", path: "MCPProxyTests")`), so **dropping a new `.swift` file into either directory needs no target registration** — the package is SPM-only (there is no `.xcodeproj`; `scripts/build-macos-tray.sh` shells out to `swift build`). Wiring these items into `MCPProxyApp.rebuildMenu()` is a later task; this task ships the component and its tests only.

**Interfaces:**

*Consumes — already on disk, signatures verified verbatim:*
- `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceFormatting.swift`
  - `static func statusSymbolName(for entry: ActivityEntry) -> String`
  - `static func rowLabel(for entry: ActivityEntry) -> String`
  - `static func middleTruncated(_ text: String, limit: Int) -> String`
  - `static func relativeTime(_ timestamp: String, now: Date = Date()) -> String`
  - `static func parseTimestamp(_ timestamp: String) -> Date?`
- `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSelection.swift`
  - `static func activityRows(from entries: [ActivityEntry], limit: Int = rowLimit) -> [ActivityEntry]`
  - `static func activeClients(from sessions: [APIClient.MCPSession], limit: Int = rowLimit) -> [APIClient.MCPSession]`
  - `static let rowLimit = 5`
- `native/macos/MCPProxy/MCPProxy/API/Models.swift` — `struct ActivityEntry: Codable, Identifiable, Equatable` (`:536`) with `id`, `type`, `serverName`, `toolName`, `status`, `errorMessage`, `timestamp`, `sessionId`, `requestId` and the snake_case coding keys the fixtures use (`server_name`, `tool_name`, `error_message`, `session_id`, `request_id`).
- `native/macos/MCPProxy/MCPProxy/State/AppState.swift` — `@Published var glanceActivity: [ActivityEntry]`, `@Published var glanceSessions: [APIClient.MCPSession]`, `@Published var usageTimeline: [UsageBucket]?`, `@Published var callsThisHour: Int?`, `var isConnected: Bool` (`coreState == .connected`), `@Published var isStopped: Bool`, `@Published var coreState: CoreState` whose `didSet` calls `clearGlanceState()` on any state other than `.connected`.

*Consumes — produced by earlier tasks in this plan; do NOT assume it is on disk, run Step 0 first:*
- `struct UsageBucket` with the memberwise init `UsageBucket(start:calls:errors:totalRespBytes:)`. It currently lives in the placeholder `native/macos/MCPProxy/MCPProxy/API/UsageStub.swift` as `struct UsageBucket: Equatable` plus a separate `extension UsageBucket: Codable` — **not** in `Models.swift`. Wherever the API task finally puts it, the type name and that memberwise init must survive; this task depends on nothing else about it.
- `APIClient.MCPSession.lastActivity` (coding key `last_activity`). On disk today `APIClient.swift:341` still declares `let lastActive: String?` with `:353` mapping `last_active`. The design requires the rename ("`APIClient.swift:341,353` — decode `last_activity`, the field the API emits"), and it carries five call sites in `MCPProxy/Views/DashboardView.swift` (`:481`, `:482`, `:494`, `:495`, `:553`). Until that task lands, this task's client-row test decodes `lastActivity` as nil and the implementation does not compile.

*Produces* (what the integration task and the histogram task rely on):
```swift
final class GlanceSection {
    init(target: AnyObject?, action: Selector)
    var histogramViewBuilder: (([UsageBucket]) -> NSView)?
    func isVisible(for state: AppState) -> Bool
    func items(for state: AppState, now: Date = Date()) -> [NSMenuItem]
    @discardableResult func updateInPlace(for state: AppState, now: Date = Date()) -> Bool
    static func firstClause(of message: String?) -> String?
}
```
Contract for the caller:
- `items(for:)` returns `[]` when the core is stopped or not connected, otherwise a 12-item block (fewer/more rows only as the record counts change) that **already ends with `.separator()`** — splice it in where the header separator is today (`MCPProxyApp.swift:591`, i.e. the `menu.addItem(.separator())` that sits after the status/error header and before the "Needs Attention" block) and emit the plain separator only when the returned array is empty.
- Ordering: `[0]` header summary, `[1]` separator, `[2]` "Recent", `[3…]` activity rows (or one "No tool calls yet" row), then "Open Activity…", separator, "Clients", client rows (or "No connected clients"), separator, "Activity (24h)" (submenu owner), separator.
- Every clickable row carries `target`/`action` as passed to `init`, and `representedObject` is the record's session id (`String?`). The "Open Activity…" row's `representedObject` is `nil` — the delegate must treat nil as "open the unfiltered activity log" (a record with no `session_id` degrades to the same behaviour). `GlanceSection` never builds a URL: the API key lives on `CoreProcessManager`.
- `updateInPlace(for:)` returns `false` for any structural change (visibility flip, row-count change, histogram loaded-ness flip) and leaves every row untouched; the caller must then set a dirty flag and run a full `rebuildMenu()` on `menuDidClose`. `true` means every row's title, image, tooltip, accessibility label and `representedObject` were rewritten in place.
- `histogramViewBuilder` is the seam for the SwiftUI chart: while it is nil the submenu renders a plain text line, so the component compiles and tests without SwiftUI Charts.

---

- [ ] **Step 0: Preflight — confirm the prerequisites this task consumes**

This task sits mid-plan and its fixtures reference symbols that earlier tasks introduce. Run the checks before writing anything; if one fails, finish the earlier task first rather than working around it here.

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy
# 1. AppState glance feeds + the disconnect reset (earlier task)
grep -n "glanceActivity\|glanceSessions\|usageTimeline\|callsThisHour\|clearGlanceState" MCPProxy/State/AppState.swift
# 2. The session field rename the design requires — MUST print lastActivity/last_activity, not lastActive/last_active
grep -n "lastActiv" MCPProxy/API/APIClient.swift
# 3. UsageBucket must exist somewhere in the module with a memberwise init
grep -rn "struct UsageBucket" MCPProxy/
# 4. The test target must currently build — otherwise Step 2's failure is unreadable
swift build --build-tests 2>&1 | tail -5
```

If check 2 still prints `lastActive` / `last_active`, the client-row test in Step 11 cannot pass: stop and land the `APIClient.swift:341,353` rename (plus its five `Views/DashboardView.swift` call sites at `:481`, `:482`, `:494`, `:495`, `:553`) first. If check 4 reports errors in files this task does not touch, the same applies — a broken test target makes every "watch it fail" step below meaningless.

- [ ] **Step 1: Create the test file with fixtures, the visibility tests and the header tests**

Create `native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift` with exactly this content:

```swift
import XCTest
import AppKit
@testable import MCPProxy

@MainActor
final class GlanceSectionTests: XCTestCase {

    /// Fixed clock so relative ages are deterministic.
    static let now = GlanceFormatting.parseTimestamp("2027-01-15T08:00:00Z")!

    // MARK: - Visibility

    func testBlockIsHiddenWhenCoreIsNotConnected() {
        let state = Self.busyState()
        state.coreState = .idle
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    func testBlockIsHiddenWhenUserStoppedTheCore() {
        let state = Self.busyState()
        state.isStopped = true
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).count, 0)
    }

    // MARK: - Header

    func testHeaderShowsCallsThisHourAndClientCount() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items.first?.title, "12 calls this hour · 1 client")
        XCTAssertFalse(items[0].isEnabled, "the header is a muted, non-clickable line")
    }

    func testHeaderOmitsCallCountUntilUsageLoads() {
        let state = Self.busyState()
        state.callsThisHour = nil
        let section = Self.makeSection()
        XCTAssertEqual(section.items(for: state, now: Self.now).first?.title, "1 client")
    }

    // MARK: - Helpers

    private final class ClickStub: NSObject {
        @objc func openGlanceRow(_ sender: NSMenuItem) {}
    }

    private static let clickStub = ClickStub()

    private static func makeSection() -> GlanceSection {
        GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
    }

    /// A connected core with two qualifying calls and one active client.
    private static func busyState() -> AppState {
        let state = AppState()
        // coreState first: its didSet clears the glance feeds on any non-connected state.
        state.coreState = .connected
        state.callsThisHour = 12
        state.glanceActivity = [
            entry(id: "a", server: "github", tool: "create_issue",
                  timestamp: "2027-01-15T07:59:30Z", session: "sess-a"),
            entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                  error: "auth failed: token expired. retry after refresh",
                  timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.glanceSessions = [
            session(id: "sess-a", name: "Claude Code", version: "2.1.0",
                    calls: 8, lastActivity: "2027-01-15T07:59:00Z")
        ]
        return state
    }

    private static func entry(
        id: String,
        type: String = "tool_call",
        server: String? = nil,
        tool: String? = nil,
        status: String = "success",
        error: String? = nil,
        timestamp: String,
        session: String? = nil
    ) -> ActivityEntry {
        var json: [String: Any] = [
            "id": id,
            "type": type,
            "status": status,
            "timestamp": timestamp,
            "request_id": "req-\(id)"
        ]
        if let server { json["server_name"] = server }
        if let tool { json["tool_name"] = tool }
        if let error { json["error_message"] = error }
        if let session { json["session_id"] = session }
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(ActivityEntry.self, from: data)
    }

    private static func session(
        id: String,
        name: String,
        version: String,
        calls: Int,
        lastActivity: String
    ) -> APIClient.MCPSession {
        let json: [String: Any] = [
            "id": id,
            "status": "active",
            "client_name": name,
            "client_version": version,
            "tool_call_count": calls,
            "last_activity": lastActivity
        ]
        let data = try! JSONSerialization.data(withJSONObject: json)
        // swiftlint:disable:next force_try
        return try! JSONDecoder().decode(APIClient.MCPSession.self, from: data)
    }
}
```

- [ ] **Step 2: Run the tests and watch them fail to compile**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output — the type does not exist yet, so the test target fails to build. The **first** error is the return type of `makeSection()` on line 51, not the constructor call:

```
/Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift:51:42: error: cannot find type 'GlanceSection' in scope
   51 |     private static func makeSection() -> GlanceSection {
      |                                          `- error: cannot find type 'GlanceSection' in scope
/Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift:52:9: error: cannot find 'GlanceSection' in scope
   52 |         GlanceSection(target: clickStub, action: #selector(ClickStub.openGlanceRow(_:)))
      |         `- error: cannot find 'GlanceSection' in scope
error: fatalError
```

If you also see errors from `AppStateGlanceTests.swift` or other files you did not touch, Step 0's check 4 was skipped — stop and land the earlier task.

- [ ] **Step 3: Create GlanceSection with the visibility rule and the header line**

Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift`:

```swift
// GlanceSection.swift
// MCPProxy
//
// Builds the "glance" block at the top of the tray menu: a one-line summary,
// the most recent qualifying tool calls, the active MCP clients, and the
// 24h histogram submenu.
//
// Every text row is a plain NSMenuItem. Custom (view-backed) menu items receive
// mouse events but NOT keyboard events, so building the rows as hosted SwiftUI
// would silently cost keyboard navigation and VoiceOver. Only the histogram —
// which genuinely needs drawing — is view-backed, and it lives alone inside its
// own submenu.
//
// This component never builds a Web UI URL. It is handed only AppState, whose
// webUIBaseURL is scheme/host/port by design, while the API key lives on the
// core manager. Rows therefore carry a target/action pair plus a
// representedObject holding the record's session id, and the app delegate opens
// the authenticated URL through the same path as every other menu action.
//
// Deliberately NOT @MainActor: AppController (the NSApplicationDelegate that
// will call this, MCPProxyApp.swift:15) is not actor-isolated, and this SDK does
// not infer MainActor from NSApplicationDelegate conformance, so annotating it
// would make rebuildMenu() fail to compile.

import AppKit

final class GlanceSection {

    // MARK: Click routing

    private weak var clickTarget: AnyObject?
    private let clickAction: Selector

    // MARK: Owned items (kept so rows can be rewritten in place)

    private var summaryItem: NSMenuItem?

    init(target: AnyObject?, action: Selector) {
        self.clickTarget = target
        self.clickAction = action
    }

    // MARK: Building

    /// Whether the glance block should appear at all. When the core is stopped
    /// or disconnected the block is hidden entirely, rather than presenting the
    /// previous core's numbers as if they were live.
    func isVisible(for state: AppState) -> Bool {
        state.isConnected && !state.isStopped
    }

    /// Build the whole block, ordered top to bottom and ending with a separator
    /// so the caller can splice it into the menu in one go. Returns an empty
    /// array when the block is hidden.
    func items(for state: AppState, now: Date = Date()) -> [NSMenuItem] {
        summaryItem = nil
        guard isVisible(for: state) else { return [] }

        var items: [NSMenuItem] = []
        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())
        return items
    }

    // MARK: Header

    private func summaryTitle(for state: AppState) -> String {
        var parts: [String] = []
        if let calls = state.callsThisHour {
            parts.append(calls == 1 ? "1 call this hour" : "\(calls) calls this hour")
        }
        let clients = GlanceSelection.activeClients(from: state.glanceSessions, limit: Int.max).count
        parts.append(clients == 1 ? "1 client" : "\(clients) clients")
        return parts.joined(separator: " · ")
    }

    // MARK: Item factories

    private func disabledItem(titled title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        return item
    }
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output (tail):

```
Test Case '-[MCPProxyTests.GlanceSectionTests testHeaderOmitsCallCountUntilUsageLoads]' passed (0.001 seconds).
Test Suite 'GlanceSectionTests' passed at ...
	 Executed 4 tests, with 0 failures (0 unexpected) in 0.034 (0.040) seconds
```

- [ ] **Step 5: Commit the skeleton**

Confirm you are on the feature branch for this plan first — do not commit to `main`:

```bash
cd /Users/user/repos/mcpproxy-go && git rev-parse --abbrev-ref HEAD   # must NOT print "main"
git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift && git commit -m "feat(tray): glance section skeleton — visibility rule and header summary"
```

- [ ] **Step 6: Add the Recent-section tests**

In `native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift`, insert this block immediately before the `// MARK: - Helpers` line:

```swift
    // MARK: - Recent section

    func testRecentSectionRendersQualifyingRows() {
        let section = Self.makeSection()
        let titles = section.items(for: Self.busyState(), now: Self.now).map {
            $0.isSeparatorItem ? "—" : $0.title
        }
        XCTAssertEqual(Array(titles.prefix(6)), [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…"
        ])
    }

    func testActivityRowCarriesFullIdentity() {
        let section = Self.makeSection()
        let failed = section.items(for: Self.busyState(), now: Self.now)[4]
        XCTAssertEqual(failed.title, "jira:get_issue · auth failed — 2m")
        XCTAssertEqual(failed.representedObject as? String, "sess-b")
        XCTAssertEqual(failed.image?.accessibilityDescription, "failed")
        XCTAssertEqual(failed.toolTip, "jira:get_issue\nauth failed: token expired. retry after refresh")
        XCTAssertEqual(failed.accessibilityLabel(), "jira:get_issue, failed: auth failed, 2m ago")
        XCTAssertNotNil(failed.action)
    }

    func testOpenActivityRowHasNoSessionPayload() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        XCTAssertEqual(items[5].title, "Open Activity…")
        XCTAssertNil(items[5].representedObject)
        XCTAssertNotNil(items[5].action)
    }

    func testNoActivityShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceActivity = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[3]
        XCTAssertEqual(row.title, "No tool calls yet")
        XCTAssertFalse(row.isEnabled)
    }

    func testFirstClauseKeepsOnlyTheLeadingClause() {
        XCTAssertEqual(GlanceSection.firstClause(of: "auth failed: token expired"), "auth failed")
        XCTAssertEqual(GlanceSection.firstClause(of: "dial tcp 127.0.0.1"), "dial tcp 127")
        XCTAssertEqual(GlanceSection.firstClause(of: "  boom  "), "boom")
        XCTAssertNil(GlanceSection.firstClause(of: "   "))
        XCTAssertNil(GlanceSection.firstClause(of: nil))
    }

```

- [ ] **Step 7: Run the tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output — the new tests reference a member that does not exist yet:

```
/Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift:89:38: error: type 'GlanceSection' has no member 'firstClause'
   89 |         XCTAssertEqual(GlanceSection.firstClause(of: "auth failed: token expired"), "auth failed")
      |                                      `- error: type 'GlanceSection' has no member 'firstClause'
error: fatalError
```

- [ ] **Step 8: Implement the Recent section and the activity-row renderer**

In `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift` make three edits.

(a) Replace the stored-properties block

```swift
    // MARK: Owned items (kept so rows can be rewritten in place)

    private var summaryItem: NSMenuItem?
```

with

```swift
    // MARK: Configuration

    /// Character budget for a row label before middle truncation kicks in.
    private static let labelBudget = 34

    // MARK: Owned items (kept so rows can be rewritten in place)

    private var summaryItem: NSMenuItem?
    private var activityRows: [NSMenuItem] = []
```

(b) Replace the whole body of `items(for:now:)`

```swift
        summaryItem = nil
        guard isVisible(for: state) else { return [] }

        var items: [NSMenuItem] = []
        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())
        return items
```

with

```swift
        summaryItem = nil
        activityRows = []
        guard isVisible(for: state) else { return [] }

        var items: [NSMenuItem] = []

        let summary = disabledItem(titled: summaryTitle(for: state))
        summaryItem = summary
        items.append(summary)
        items.append(.separator())

        items.append(disabledItem(titled: "Recent"))
        let entries = GlanceSelection.activityRows(from: state.glanceActivity)
        if entries.isEmpty {
            items.append(disabledItem(titled: "No tool calls yet"))
        } else {
            for entry in entries {
                let row = actionableItem()
                apply(entry, to: row, now: now)
                activityRows.append(row)
                items.append(row)
            }
        }

        let openActivity = actionableItem()
        openActivity.title = "Open Activity…"
        openActivity.image = NSImage(systemSymbolName: "list.bullet.rectangle",
                                     accessibilityDescription: "activity log")
        items.append(openActivity)

        return items
```

(c) Insert these members immediately after the closing brace of `items(for:now:)`, before `// MARK: Header`:

```swift
    // MARK: Row rendering

    /// Rewrite an activity row so its title, icon, tooltip, accessibility label
    /// and click payload all describe `entry`.
    private func apply(_ entry: ActivityEntry, to item: NSMenuItem, now: Date) {
        let fullLabel = GlanceFormatting.rowLabel(for: entry)
        let label = GlanceFormatting.middleTruncated(fullLabel, limit: Self.labelBudget)
        let age = GlanceFormatting.relativeTime(entry.timestamp, now: now)
        let failed = entry.status != "success"
        let detail = failed ? Self.firstClause(of: entry.errorMessage) : nil

        if let detail {
            item.title = "\(label) · \(detail) — \(age)"
            item.setAccessibilityLabel("\(fullLabel), failed: \(detail), \(age) ago")
        } else {
            item.title = "\(label) — \(age)"
            item.setAccessibilityLabel("\(fullLabel), \(failed ? "failed" : "succeeded"), \(age) ago")
        }

        item.image = NSImage(systemSymbolName: GlanceFormatting.statusSymbolName(for: entry),
                             accessibilityDescription: failed ? "failed" : "succeeded")

        if let message = entry.errorMessage, !message.isEmpty {
            item.toolTip = "\(fullLabel)\n\(message)"
        } else {
            item.toolTip = fullLabel
        }

        item.representedObject = entry.sessionId
    }

    /// First clause of an error message — everything up to the first newline,
    /// period or colon — so a multi-sentence backend error still fits one row.
    /// The full message stays in the tooltip.
    static func firstClause(of message: String?) -> String? {
        guard let message else { return nil }
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let head = trimmed.components(separatedBy: CharacterSet(charactersIn: ".:\n")).first ?? trimmed
        let clause = head.trimmingCharacters(in: .whitespaces)
        return clause.isEmpty ? trimmed : clause
    }

```

and add this factory next to `disabledItem(titled:)` at the bottom of the class:

```swift
    private func actionableItem() -> NSMenuItem {
        let item = NSMenuItem(title: "", action: clickAction, keyEquivalent: "")
        item.target = clickTarget
        return item
    }
```

- [ ] **Step 9: Run the tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output (tail):

```
Test Case '-[MCPProxyTests.GlanceSectionTests testActivityRowCarriesFullIdentity]' passed (0.049 seconds).
Test Suite 'GlanceSectionTests' passed at ...
	 Executed 9 tests, with 0 failures (0 unexpected) in 0.045 (0.050) seconds
```

- [ ] **Step 10: Commit the Recent section**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift && git commit -m "feat(tray): glance Recent rows with full row identity"
```

- [ ] **Step 11: Add the Clients, histogram and full-layout tests**

In `native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift`, insert this block immediately before the `// MARK: - Helpers` line:

```swift
    // MARK: - Clients section and histogram

    func testClientRowCarriesSessionIdentity() {
        let section = Self.makeSection()
        let client = section.items(for: Self.busyState(), now: Self.now)[8]
        XCTAssertEqual(client.title, "Claude Code — 8 calls · 1m")
        XCTAssertEqual(client.representedObject as? String, "sess-a")
        XCTAssertEqual(client.toolTip, "Claude Code 2.1.0")
        XCTAssertEqual(client.accessibilityLabel(), "Claude Code, 8 calls, last active 1m ago")
    }

    func testNoClientsShowsOneMutedRow() {
        let state = Self.busyState()
        state.glanceSessions = []
        let section = Self.makeSection()
        let row = section.items(for: state, now: Self.now)[8]
        XCTAssertEqual(row.title, "No connected clients")
        XCTAssertFalse(row.isEnabled)
    }

    func testHistogramSubmenuShowsLoadingUntilUsageArrives() {
        let section = Self.makeSection()
        let histogram = section.items(for: Self.busyState(), now: Self.now)[10]
        XCTAssertEqual(histogram.title, "Activity (24h)")
        XCTAssertEqual(histogram.submenu?.item(at: 0)?.title, "Loading…")
    }

    func testHistogramSubmenuUsesInjectedViewWhenAvailable() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        section.histogramViewBuilder = { buckets in
            let view = NSView(frame: NSRect(x: 0, y: 0, width: 240, height: 90))
            view.setAccessibilityLabel("\(buckets.count) buckets")
            return view
        }
        let chart = section.items(for: state, now: Self.now)[10].submenu?.item(at: 0)
        XCTAssertNotNil(chart?.view)
        XCTAssertEqual(chart?.view?.accessibilityLabel(), "1 buckets")
    }

    func testHistogramSubmenuFallsBackToTextWithoutABuilder() {
        let state = Self.busyState()
        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        XCTAssertEqual(items[10].submenu?.item(at: 0)?.title, "12 calls · 1 error (24h)")
    }

    func testBlockLayoutOrder() {
        let section = Self.makeSection()
        let items = section.items(for: Self.busyState(), now: Self.now)
        let titles = items.map { $0.isSeparatorItem ? "—" : $0.title }
        XCTAssertEqual(titles, [
            "12 calls this hour · 1 client",
            "—",
            "Recent",
            "github:create_issue — 30s",
            "jira:get_issue · auth failed — 2m",
            "Open Activity…",
            "—",
            "Clients",
            "Claude Code — 8 calls · 1m",
            "—",
            "Activity (24h)",
            "—"
        ])
    }

```

- [ ] **Step 12: Run the tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output — the injection seam does not exist yet:

```
/Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift:127:17: error: value of type 'GlanceSection' has no member 'histogramViewBuilder'
  127 |         section.histogramViewBuilder = { buckets in
      |                 `- error: value of type 'GlanceSection' has no member 'histogramViewBuilder'
error: fatalError
```

- [ ] **Step 13: Implement the Clients section and the histogram submenu item**

In `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift` make four edits.

(a) Add the injection seam — insert immediately after the `private let clickAction: Selector` line:

```swift

    /// Builds the view for the histogram submenu's single custom item. While
    /// this is nil the submenu falls back to a plain text line, which keeps the
    /// component usable and testable without SwiftUI Charts.
    var histogramViewBuilder: (([UsageBucket]) -> NSView)?
```

(b) Extend the owned-items block — replace

```swift
    private var summaryItem: NSMenuItem?
    private var activityRows: [NSMenuItem] = []
```

with

```swift
    private var summaryItem: NSMenuItem?
    private var activityRows: [NSMenuItem] = []
    private var clientRows: [NSMenuItem] = []
    /// Held only so ownership of the submenu is explicit; `updateInPlace`
    /// deliberately never touches it (re-creating it would disturb an open
    /// submenu), so nothing reads this back.
    private var histogramItem: NSMenuItem?
```

(c) In `items(for:now:)`, replace the reset lines

```swift
        summaryItem = nil
        activityRows = []
        guard isVisible(for: state) else { return [] }
```

with

```swift
        summaryItem = nil
        activityRows = []
        clientRows = []
        histogramItem = nil
        guard isVisible(for: state) else { return [] }
```

and replace the tail of the same method

```swift
        items.append(openActivity)

        return items
```

with

```swift
        items.append(openActivity)
        items.append(.separator())

        items.append(disabledItem(titled: "Clients"))
        let clients = GlanceSelection.activeClients(from: state.glanceSessions)
        if clients.isEmpty {
            items.append(disabledItem(titled: "No connected clients"))
        } else {
            for session in clients {
                let row = actionableItem()
                apply(session, to: row, now: now)
                clientRows.append(row)
                items.append(row)
            }
        }
        items.append(.separator())

        let histogram = makeHistogramItem(for: state)
        histogramItem = histogram
        items.append(histogram)
        items.append(.separator())

        return items
```

(d) Insert these two members immediately after the closing brace of `firstClause(of:)` — i.e. at the end of the `// MARK: Row rendering` section added in Step 8(c), so the two `apply` overloads stay adjacent and ahead of `// MARK: Header`:

```swift
    /// Rewrite a client row so it fully describes `session`.
    private func apply(_ session: APIClient.MCPSession, to item: NSMenuItem, now: Date) {
        let name = session.clientName.flatMap { $0.isEmpty ? nil : $0 } ?? "Unknown client"
        let calls = session.toolCallCount ?? 0
        let callText = calls == 1 ? "1 call" : "\(calls) calls"
        let age = session.lastActivity.map { GlanceFormatting.relativeTime($0, now: now) }

        if let age {
            item.title = "\(name) — \(callText) · \(age)"
            item.setAccessibilityLabel("\(name), \(callText), last active \(age) ago")
        } else {
            item.title = "\(name) — \(callText)"
            item.setAccessibilityLabel("\(name), \(callText)")
        }

        item.image = NSImage(systemSymbolName: "circle.fill", accessibilityDescription: "connected")

        if let version = session.clientVersion, !version.isEmpty {
            item.toolTip = "\(name) \(version)"
        } else {
            item.toolTip = name
        }

        item.representedObject = session.id
    }

    // MARK: Histogram

    private func makeHistogramItem(for state: AppState) -> NSMenuItem {
        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        let submenu = NSMenu()

        if let timeline = state.usageTimeline {
            if let builder = histogramViewBuilder {
                let chart = NSMenuItem(title: "", action: nil, keyEquivalent: "")
                chart.view = builder(timeline)
                submenu.addItem(chart)
            } else {
                let calls = timeline.reduce(0) { $0 + $1.calls }
                let errors = timeline.reduce(0) { $0 + $1.errors }
                let callText = calls == 1 ? "1 call" : "\(calls) calls"
                let errorText = errors == 1 ? "1 error" : "\(errors) errors"
                submenu.addItem(disabledItem(titled: "\(callText) · \(errorText) (24h)"))
            }
        } else {
            submenu.addItem(disabledItem(titled: "Loading…"))
        }

        item.submenu = submenu
        return item
    }

```

(The fallback line reports the bucket totals as the endpoint defines them — a bucket's `calls` already includes its `errors`, so "12 calls · 1 error" means one of the twelve failed. The chart task, not this one, is where the `calls - errors` stacking correction applies.)

- [ ] **Step 14: Run the tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output (tail):

```
Test Case '-[MCPProxyTests.GlanceSectionTests testBlockLayoutOrder]' passed (0.001 seconds).
Test Suite 'GlanceSectionTests' passed at ...
	 Executed 15 tests, with 0 failures (0 unexpected) in 0.048 (0.052) seconds
```

- [ ] **Step 15: Commit the Clients section and histogram item**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift && git commit -m "feat(tray): glance Clients rows and 24h histogram submenu item"
```

- [ ] **Step 16: Add the in-place update tests**

In `native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift`, insert this block immediately before the `// MARK: - Helpers` line:

```swift
    // MARK: - In-place updates

    func testUpdateInPlaceRewritesTheEntireRowIdentity() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)
        let row = items[3]

        state.glanceActivity = [
            Self.entry(id: "c", server: "obsidian", tool: "search_notes",
                       timestamp: "2027-01-15T07:59:55Z", session: "sess-c"),
            Self.entry(id: "b", server: "jira", tool: "get_issue", status: "error",
                       error: "auth failed: token expired. retry after refresh",
                       timestamp: "2027-01-15T07:58:00Z", session: "sess-b")
        ]
        state.callsThisHour = 13

        XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
        XCTAssertEqual(items[0].title, "13 calls this hour · 1 client")
        XCTAssertEqual(row.title, "obsidian:search_notes — 5s")
        XCTAssertEqual(row.representedObject as? String, "sess-c",
                       "the click payload must follow the title, or the row opens the previous record's session")
        XCTAssertEqual(row.image?.accessibilityDescription, "succeeded")
        XCTAssertEqual(row.toolTip, "obsidian:search_notes")
        XCTAssertEqual(row.accessibilityLabel(), "obsidian:search_notes, succeeded, 5s ago")
    }

    func testUpdateInPlaceRefusesStructuralChange() {
        let state = Self.busyState()
        let section = Self.makeSection()
        let items = section.items(for: state, now: Self.now)

        state.glanceActivity = [state.glanceActivity[0]]

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now),
                       "a row-count change must defer a rebuild, not mutate an open menu")
        XCTAssertEqual(items[3].title, "github:create_issue — 30s", "rows must be left untouched")
    }

    func testUpdateInPlaceRefusesWhenHistogramLoadednessFlips() {
        let state = Self.busyState()
        let section = Self.makeSection()
        _ = section.items(for: state, now: Self.now)

        state.usageTimeline = [UsageBucket(start: Self.now, calls: 12, errors: 1, totalRespBytes: 0)]

        XCTAssertFalse(section.updateInPlace(for: state, now: Self.now))
    }

    func testUpdateInPlaceBeforeFirstBuildReportsStructural() {
        XCTAssertFalse(Self.makeSection().updateInPlace(for: Self.busyState(), now: Self.now))
    }

```

- [ ] **Step 17: Run the tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output:

```
/Users/user/repos/mcpproxy-go/native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift:182:31: error: value of type 'GlanceSection' has no member 'updateInPlace'
  182 |         XCTAssertTrue(section.updateInPlace(for: state, now: Self.now))
      |                               `- error: value of type 'GlanceSection' has no member 'updateInPlace'
error: fatalError
```

- [ ] **Step 18: Implement updateInPlace and the structure-tracking flags**

In `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift` make three edits.

(a) Extend the owned-items block — replace

```swift
    private var clientRows: [NSMenuItem] = []
```

with

```swift
    private var clientRows: [NSMenuItem] = []

    /// Snapshot of the structure the current items were built from, so an
    /// in-place update can detect that a full rebuild is required instead.
    private var hasBuilt = false
    private var builtVisible = false
    private var builtWithTimeline = false
```

(leave the `histogramItem` property and its comment where Step 13(b) put them — it follows this block).

(b) In `items(for:now:)`, replace the reset lines

```swift
        summaryItem = nil
        activityRows = []
        clientRows = []
        histogramItem = nil
        guard isVisible(for: state) else { return [] }
```

with

```swift
        summaryItem = nil
        activityRows = []
        clientRows = []
        histogramItem = nil
        hasBuilt = true
        builtVisible = isVisible(for: state)
        builtWithTimeline = state.usageTimeline != nil
        guard builtVisible else { return [] }
```

(c) Insert this method immediately after the closing brace of `items(for:now:)`, before `// MARK: Row rendering`:

```swift
    // MARK: In-place updates

    /// Rewrite the existing rows from `state` without restructuring the menu.
    ///
    /// Returns `true` when every row was updated in place, and `false` when the
    /// block's structure changed (visibility, row count, or histogram
    /// loaded-ness) — the caller must then defer a full rebuild until the menu
    /// closes rather than growing or shrinking a menu the user is reading.
    ///
    /// A row's *entire identity* is rewritten, not just its title: with a fixed
    /// number of rows every new event shifts which record each row represents,
    /// so refreshing only the text would leave a row whose click still opened
    /// the previous record's session. The histogram submenu is deliberately not
    /// touched — re-creating it would disturb an open submenu — so a change in
    /// its loaded-ness reports structural instead.
    @discardableResult
    func updateInPlace(for state: AppState, now: Date = Date()) -> Bool {
        guard hasBuilt else { return false }
        guard isVisible(for: state) == builtVisible else { return false }
        guard builtVisible else { return true }
        guard (state.usageTimeline != nil) == builtWithTimeline else { return false }

        let entries = GlanceSelection.activityRows(from: state.glanceActivity)
        let clients = GlanceSelection.activeClients(from: state.glanceSessions)
        guard entries.count == activityRows.count,
              clients.count == clientRows.count else { return false }

        summaryItem?.title = summaryTitle(for: state)
        for (row, entry) in zip(activityRows, entries) { apply(entry, to: row, now: now) }
        for (row, session) in zip(clientRows, clients) { apply(session, to: row, now: now) }
        return true
    }

```

- [ ] **Step 19: Run the tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceSectionTests
```

Expected output (tail):

```
Test Case '-[MCPProxyTests.GlanceSectionTests testUpdateInPlaceRewritesTheEntireRowIdentity]' passed (0.001 seconds).
Test Suite 'GlanceSectionTests' passed at ...
	 Executed 19 tests, with 0 failures (0 unexpected) in 0.050 (0.056) seconds
```

- [ ] **Step 20: Run the whole native test suite to prove nothing else regressed**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test
```

Expected output (tail) — every existing suite plus the 19 new tests, zero failures. A verification run of this exact task on top of its prerequisites executed **342** tests; the count moves as sibling tasks land, so treat "0 failures" as the assertion and the number as informational. This is exactly what the `swift-test` job in `.github/workflows/native-tests.yml` runs (`working-directory: native/macos/MCPProxy`, `run: swift test`, no `--skip`).

```
Test Suite 'All tests' passed at ...
	 Executed 342 tests, with 0 failures (0 unexpected) in ... seconds
✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
```

(If `swift test` fails with `precompiled file … was compiled with module cache path …`, the `.build` tree was copied in from another directory — `rm -rf .build/arm64-apple-macosx/debug/ModuleCache` and re-run.)

- [ ] **Step 21: Commit the in-place update path**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift native/macos/MCPProxy/MCPProxyTests/GlanceSectionTests.swift && git commit -m "feat(tray): in-place glance row updates with structural-change guard"
```

---

### Task 7: Swift — MCPProxyApp integration, open-menu rebuild guard, session deep link, TrayMenu.swift deletion

**Files:**
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/MenuRebuildGuard.swift`
- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceLinks.swift`
- Modify: `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift` (stored properties, lines 21–25; `menuWillOpen` / NSMenuDelegate section, lines 190–197; `rebuildMenu()` head, lines 523–532; the separator before "Needs Attention", line 591; new action after `openWebUI()`, lines 996–998)
- Delete: `native/macos/MCPProxy/MCPProxy/Menu/TrayMenu.swift` (511 lines, dead — nothing constructs `TrayMenu`; it declares exactly one top-level symbol, `struct TrayMenu: View` at line 9, and its three helpers were salvaged into `Menu/Glance/GlanceFormatting.swift` in Task 5)
- Test: `native/macos/MCPProxy/MCPProxyTests/MenuRebuildGuardTests.swift` (create)
- Test: `native/macos/MCPProxy/MCPProxyTests/GlanceLinksTests.swift` (create)

**Interfaces:**

*Consumes* (must already exist when this task starts):
- From Task 6 — `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceSection.swift`:
  ```swift
  struct GlanceSection {
      init(target: AnyObject?, openActivityAction: Selector)
      /// Plain NSMenuItems (activity rows, client rows, separators) plus the
      /// "Activity (24h)" submenu item. Returns [] when the core is not
      /// connected. Reads AppState only — issues no network request.
      func items(for state: AppState) -> [NSMenuItem]
  }
  ```
- From Task 2 — `native/macos/MCPProxy/MCPProxy/State/AppState.swift`: `@Published var glanceActivity: [ActivityEntry]`, `@Published var glanceSessions: [APIClient.MCPSession]`, `@Published var usageTimeline: [UsageBucket]?`, `@Published var callsThisHour: Int?`.
- Already in the repo (verified): `AppController` in `MCPProxyApp.swift` already conforms to `NSMenuDelegate` (line 15) and sets `menu.delegate = self` (line 530); `CoreProcessManager.currentAPIKey` is `nonisolated var currentAPIKey: String? { get async }` (`Core/CoreProcessManager.swift:76-79`); `AppState.webUIBaseURL` is `@Published var webUIBaseURL: String = "http://127.0.0.1:8080"` (`State/AppState.swift:131`).

*Produces* (later tasks and the QA pass rely on these exact symbols):
- `enum MenuRebuildDecision: Equatable { case rebuild, updateInPlace, deferUntilClose }`
- `struct MenuRebuildGuard` with `var isMenuOpen: Bool { get }`, `var isDirty: Bool { get }`, `mutating func menuWillOpen()`, `mutating func decide(structureChanged: Bool) -> MenuRebuildDecision`, `mutating func menuDidClose() -> Bool`
- `func glanceRowsAreCompatible(installed: [NSMenuItem], fresh: [NSMenuItem]) -> Bool`
- `func applyGlanceRowsInPlace(installed: [NSMenuItem], fresh: [NSMenuItem])`
- `func activityURLString(baseURL: String, apiKey: String, sessionID: String?) -> String`
- `AppController.openActivityForSession(_:)` — the `@objc` selector `GlanceSection` rows must target (`#selector(openActivityForSession(_:))`), reading the session id from `NSMenuItem.representedObject as? String`
- `AppController.menuDidClose(_:)` — new `NSMenuDelegate` method. Both it and the existing `menuWillOpen(_:)` gain a `menu === statusItem.menu` guard so submenu open/close never drives the rebuild guard (see Step 15).

---

Every `swift build` / `swift test` invocation in this repo prints this harmless preamble first; ignore it:

```
warning: 'mcpproxy': found 4 file(s) which are unhandled; explicitly declare them as resources or exclude from the target
    .../MCPProxy/MCPProxy.entitlements
    .../MCPProxy/Assets.xcassets
    .../MCPProxy/Info.plist
    .../MCPProxy/mcpproxy.icns
```

- [ ] **Step 1: Write the failing rebuild-guard test**

  Create `native/macos/MCPProxy/MCPProxyTests/MenuRebuildGuardTests.swift` with exactly this content:

  ```swift
  import XCTest
  import AppKit
  @testable import MCPProxy

  final class MenuRebuildGuardTests: XCTestCase {

      func testClosedMenuAlwaysRebuilds() {
          var guardState = MenuRebuildGuard()
          XCTAssertEqual(guardState.decide(structureChanged: false), .rebuild)
          XCTAssertEqual(guardState.decide(structureChanged: true), .rebuild)
          XCTAssertFalse(guardState.isDirty)
      }

      func testOpenMenuWithSameStructureUpdatesInPlace() {
          var guardState = MenuRebuildGuard()
          guardState.menuWillOpen()
          XCTAssertEqual(guardState.decide(structureChanged: false), .updateInPlace)
          XCTAssertEqual(guardState.decide(structureChanged: false), .updateInPlace)
          XCTAssertFalse(guardState.isDirty, "In-place updates must not owe a rebuild")
          XCTAssertFalse(guardState.menuDidClose(), "No rebuild is owed after in-place updates only")
      }

      func testStructuralChangeWhileOpenIsDeferredAndRunsOnceOnClose() {
          var guardState = MenuRebuildGuard()
          guardState.menuWillOpen()
          XCTAssertEqual(guardState.decide(structureChanged: true), .deferUntilClose)
          XCTAssertEqual(guardState.decide(structureChanged: true), .deferUntilClose)
          XCTAssertTrue(guardState.isDirty)

          XCTAssertTrue(guardState.menuDidClose(), "The deferred rebuild must run on close")
          XCTAssertFalse(guardState.isMenuOpen)
          XCTAssertFalse(guardState.menuDidClose(), "The deferred rebuild runs exactly once")
      }

      func testReopeningClearsAStaleDirtyFlag() {
          var guardState = MenuRebuildGuard()
          guardState.menuWillOpen()
          _ = guardState.decide(structureChanged: true)
          _ = guardState.menuDidClose()
          guardState.menuWillOpen()
          XCTAssertFalse(guardState.isDirty)
      }
  }
  ```

  No target registration is needed: `Package.swift` globs both targets by `path:` with no `sources:` or `exclude:`, so dropping the file into `MCPProxyTests/` is enough.

- [ ] **Step 2: Run it and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter MenuRebuildGuardTests
  ```

  Expected: the build stops before any test runs, with lines like

  ```
  .../MCPProxyTests/MenuRebuildGuardTests.swift:7:29: error: cannot find 'MenuRebuildGuard' in scope
          var guardState = MenuRebuildGuard()
                           ^~~~~~~~~~~~~~~~
  error: fatalError
  ```

  (`Executed 0 tests` — nothing runs.)

- [ ] **Step 3: Implement the guard**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/MenuRebuildGuard.swift`:

  ```swift
  // MenuRebuildGuard.swift
  // MCPProxy
  //
  // Rebuild policy for the status-bar menu while it is on screen.

  import AppKit

  /// What a rebuild request is allowed to do at this moment.
  enum MenuRebuildDecision: Equatable {
      /// The menu is closed — clear it and build every item from scratch.
      case rebuild
      /// The menu is open and the new glance rows line up 1:1 with the installed
      /// ones — rewrite them in place, add and remove nothing.
      case updateInPlace
      /// The menu is open and the structure changed — do nothing now, remember
      /// that a rebuild is owed, and run it when the menu closes.
      case deferUntilClose
  }

  /// Tracks whether the status-bar menu is on screen and whether a structural
  /// rebuild was suppressed while it was.
  ///
  /// Live SSE rows make the debounced `objectWillChange -> rebuildMenu()` sink
  /// fire during active traffic, i.e. potentially while the user is reading the
  /// menu. `removeAllItems()` under the cursor — a menu that grows or shrinks
  /// mid-read, or an open submenu that collapses — is exactly the irritation the
  /// glance design forbids, so structural churn waits for `menuDidClose`.
  struct MenuRebuildGuard {
      /// True between `menuWillOpen()` and `menuDidClose()`.
      private(set) var isMenuOpen = false

      /// True when a structural rebuild was suppressed while the menu was open.
      private(set) var isDirty = false

      /// Arm the guard. Call AFTER the pre-display rebuild in `menuWillOpen`.
      mutating func menuWillOpen() {
          isMenuOpen = true
          isDirty = false
      }

      /// Decide what a rebuild request may do.
      /// - Parameter structureChanged: true when the new glance rows cannot be
      ///   written over the installed ones (different count or layout).
      mutating func decide(structureChanged: Bool) -> MenuRebuildDecision {
          guard isMenuOpen else { return .rebuild }
          if structureChanged {
              isDirty = true
              return .deferUntilClose
          }
          return .updateInPlace
      }

      /// Disarm the guard. Returns true when a rebuild was deferred and is owed.
      mutating func menuDidClose() -> Bool {
          isMenuOpen = false
          let owed = isDirty
          isDirty = false
          return owed
      }
  }
  ```

- [ ] **Step 4: Run the guard tests and watch them pass**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter MenuRebuildGuardTests
  ```

  Expected tail:

  ```
  Test Suite 'MenuRebuildGuardTests' passed at ...
  	 Executed 4 tests, with 0 failures (0 unexpected) in 0.000 (0.000) seconds
  ```

- [ ] **Step 5: Commit the guard**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/MenuRebuildGuard.swift native/macos/MCPProxy/MCPProxyTests/MenuRebuildGuardTests.swift && git commit -m "feat(tray): add MenuRebuildGuard so an open menu never restructures"
  ```

- [ ] **Step 6: Write the failing in-place row-update tests**

  Append this second test class to the end of `native/macos/MCPProxy/MCPProxyTests/MenuRebuildGuardTests.swift` (after the closing `}` of `MenuRebuildGuardTests`):

  ```swift

  final class GlanceRowInPlaceUpdateTests: XCTestCase {

      private func row(title: String, session: String?, symbol: String) -> NSMenuItem {
          let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
          item.representedObject = session
          item.image = NSImage(systemSymbolName: symbol, accessibilityDescription: symbol)
          item.toolTip = "tip:\(title)"
          item.setAccessibilityLabel("a11y:\(title)")
          return item
      }

      func testDifferentRowCountIsIncompatible() {
          let installed = [row(title: "a", session: "s1", symbol: "checkmark.circle")]
          let fresh = [
              row(title: "a", session: "s1", symbol: "checkmark.circle"),
              row(title: "b", session: "s2", symbol: "checkmark.circle"),
          ]
          XCTAssertFalse(glanceRowsAreCompatible(installed: installed, fresh: fresh))
      }

      func testSeparatorLayoutChangeIsIncompatible() {
          let installed = [row(title: "a", session: "s1", symbol: "checkmark.circle"), NSMenuItem.separator()]
          let fresh = [row(title: "a", session: "s1", symbol: "checkmark.circle"),
                       row(title: "b", session: "s2", symbol: "checkmark.circle")]
          XCTAssertFalse(glanceRowsAreCompatible(installed: installed, fresh: fresh))
      }

      func testSubmenuPresenceChangeIsIncompatible() {
          let installed = [row(title: "Activity (24h)", session: nil, symbol: "chart.bar")]
          let withSubmenu = row(title: "Activity (24h)", session: nil, symbol: "chart.bar")
          withSubmenu.submenu = NSMenu()
          XCTAssertFalse(glanceRowsAreCompatible(installed: installed, fresh: [withSubmenu]))
      }

      /// The whole identity moves, not just the text: a row whose title says one
      /// record while its click opens another is worse than a stale row.
      func testInPlaceUpdateRewritesEntireRowIdentity() {
          let installed = [row(title: "github:create_issue", session: "sess-1", symbol: "checkmark.circle")]
          let fresh = [row(title: "jira:get_issue", session: "sess-2", symbol: "xmark.circle")]
          fresh[0].isEnabled = true

          applyGlanceRowsInPlace(installed: installed, fresh: fresh)

          XCTAssertEqual(installed[0].title, "jira:get_issue")
          XCTAssertEqual(installed[0].representedObject as? String, "sess-2")
          XCTAssertEqual(installed[0].toolTip, "tip:jira:get_issue")
          XCTAssertEqual(installed[0].accessibilityLabel(), "a11y:jira:get_issue")
          XCTAssertEqual(installed[0].image?.accessibilityDescription, "xmark.circle")
      }

      func testSubmenuIsNotReplacedByAnInPlaceUpdate() {
          let installedItem = row(title: "Activity (24h)", session: nil, symbol: "chart.bar")
          let installedSubmenu = NSMenu(title: "installed")
          installedItem.submenu = installedSubmenu

          let freshItem = row(title: "Activity (24h)", session: nil, symbol: "chart.bar")
          freshItem.submenu = NSMenu(title: "fresh")

          applyGlanceRowsInPlace(installed: [installedItem], fresh: [freshItem])

          XCTAssertTrue(installedItem.submenu === installedSubmenu,
                        "Replacing the submenu would collapse it while the user has it open")
      }

      func testIncompatibleRowsAreLeftUntouched() {
          let installed = [row(title: "github:create_issue", session: "sess-1", symbol: "checkmark.circle")]
          let fresh = [
              row(title: "jira:get_issue", session: "sess-2", symbol: "xmark.circle"),
              row(title: "extra", session: "sess-3", symbol: "checkmark.circle"),
          ]
          applyGlanceRowsInPlace(installed: installed, fresh: fresh)
          XCTAssertEqual(installed[0].title, "github:create_issue")
          XCTAssertEqual(installed[0].representedObject as? String, "sess-1")
      }
  }
  ```

- [ ] **Step 7: Run the new class and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceRowInPlaceUpdateTests
  ```

  Expected: build failure, no tests executed, with

  ```
  .../MCPProxyTests/MenuRebuildGuardTests.swift:...: error: cannot find 'glanceRowsAreCompatible' in scope
  .../MCPProxyTests/MenuRebuildGuardTests.swift:...: error: cannot find 'applyGlanceRowsInPlace' in scope
  error: fatalError
  ```

- [ ] **Step 8: Implement the two row functions**

  Append to the end of `native/macos/MCPProxy/MCPProxy/Menu/Glance/MenuRebuildGuard.swift`:

  ```swift

  /// True when `fresh` rows can be written over `installed` ones without adding,
  /// removing, or re-typing any menu item.
  func glanceRowsAreCompatible(installed: [NSMenuItem], fresh: [NSMenuItem]) -> Bool {
      guard installed.count == fresh.count else { return false }
      for (old, new) in zip(installed, fresh) {
          if old.isSeparatorItem != new.isSeparatorItem { return false }
          if (old.submenu == nil) != (new.submenu == nil) { return false }
      }
      return true
  }

  /// Rewrite `installed` glance rows from `fresh` ones, leaving the menu's
  /// structure untouched. A no-op when the two are not compatible, so a caller
  /// that mis-sequences the check cannot leave half-updated rows on screen.
  ///
  /// A row's ENTIRE identity is copied, not just its title: once five rows exist
  /// every new event shifts which record each row represents, and refreshing only
  /// the title would leave a row reading like the new record while its click still
  /// opened the previous record's session — a wrong destination the user cannot
  /// detect. `submenu` is deliberately NOT copied: replacing it would collapse the
  /// histogram submenu while the user has it open.
  func applyGlanceRowsInPlace(installed: [NSMenuItem], fresh: [NSMenuItem]) {
      guard glanceRowsAreCompatible(installed: installed, fresh: fresh) else { return }
      for (old, new) in zip(installed, fresh) where !old.isSeparatorItem {
          old.title = new.title
          old.attributedTitle = new.attributedTitle
          old.image = new.image
          old.toolTip = new.toolTip
          old.representedObject = new.representedObject
          old.target = new.target
          old.action = new.action
          old.isEnabled = new.isEnabled
          old.setAccessibilityLabel(new.accessibilityLabel())
      }
  }
  ```

- [ ] **Step 9: Run both classes and watch them pass, then commit**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter "MenuRebuildGuardTests|GlanceRowInPlaceUpdateTests"
  ```

  Expected tail:

  ```
  Test Suite 'GlanceRowInPlaceUpdateTests' passed at ...
  	 Executed 6 tests, with 0 failures (0 unexpected) in 0.053 (0.054) seconds
  Test Suite 'MenuRebuildGuardTests' passed at ...
  	 Executed 4 tests, with 0 failures (0 unexpected) in 0.000 (0.000) seconds
  ```

  Then:

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/MenuRebuildGuard.swift native/macos/MCPProxy/MCPProxyTests/MenuRebuildGuardTests.swift && git commit -m "feat(tray): rewrite glance rows in place, carrying the whole row identity"
  ```

- [ ] **Step 10: Write the failing activity-deep-link tests**

  Create `native/macos/MCPProxy/MCPProxyTests/GlanceLinksTests.swift`:

  ```swift
  import XCTest
  @testable import MCPProxy

  final class GlanceLinksTests: XCTestCase {

      func testSessionAndKeyAreBothAppended() {
          XCTAssertEqual(
              activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "k1", sessionID: "sess-42"),
              "http://127.0.0.1:8080/ui/activity?session=sess-42&apikey=k1"
          )
      }

      func testMissingKeyOmitsTheParameter() {
          XCTAssertEqual(
              activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: "sess-42"),
              "http://127.0.0.1:8080/ui/activity?session=sess-42"
          )
      }

      func testMissingSessionOpensTheUnfilteredLog() {
          XCTAssertEqual(
              activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "k1", sessionID: nil),
              "http://127.0.0.1:8080/ui/activity?apikey=k1"
          )
          XCTAssertEqual(
              activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: ""),
              "http://127.0.0.1:8080/ui/activity"
          )
      }

      func testSessionIDIsPercentEncoded() {
          let url = activityURLString(baseURL: "http://127.0.0.1:8080", apiKey: "", sessionID: "a b&c")
          XCTAssertEqual(url, "http://127.0.0.1:8080/ui/activity?session=a%20b%26c")
          XCTAssertNotNil(URL(string: url))
      }

      func testNonDefaultPortIsPreserved() {
          XCTAssertEqual(
              activityURLString(baseURL: "http://127.0.0.1:18080", apiKey: "k", sessionID: "s"),
              "http://127.0.0.1:18080/ui/activity?session=s&apikey=k"
          )
      }
  }
  ```

  (No `import Foundation` is needed — `import XCTest` re-exports it, which is why `URL` and `Date` resolve in the existing test files that import XCTest alone.)

- [ ] **Step 11: Run it and watch it fail to compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceLinksTests
  ```

  Expected: build failure, no tests executed, with

  ```
  .../MCPProxyTests/GlanceLinksTests.swift:7:13: error: cannot find 'activityURLString' in scope
  error: fatalError
  ```

- [ ] **Step 12: Implement the URL builder**

  Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceLinks.swift`:

  ```swift
  // GlanceLinks.swift
  // MCPProxy
  //
  // Web UI deep links opened from glance rows.

  import Foundation

  /// Build the Web UI activity-log URL, optionally filtered by session.
  ///
  /// `?session=` is the query parameter the Activity view reads on mount
  /// (frontend/src/views/Activity.vue:1334, `route.query.session`), and the Web
  /// UI router is history-based (createWebHistory), so `/ui/activity` is a real
  /// path rather than a fragment. `apikey` travels as a query parameter because a
  /// browser cannot send the `X-API-Key` header — `/ui/` is the one surface that
  /// accepts it, and the Web UI strips only `apikey` from the address bar on load
  /// (services/api.ts:69-80), keeping `session`.
  func activityURLString(baseURL: String, apiKey: String, sessionID: String?) -> String {
      let path = baseURL + "/ui/activity"
      var query: [URLQueryItem] = []
      if let sessionID, !sessionID.isEmpty {
          query.append(URLQueryItem(name: "session", value: sessionID))
      }
      if !apiKey.isEmpty {
          query.append(URLQueryItem(name: "apikey", value: apiKey))
      }
      guard var components = URLComponents(string: path) else { return path }
      components.queryItems = query.isEmpty ? nil : query
      return components.string ?? path
  }
  ```

- [ ] **Step 13: Run the link tests, watch them pass, commit**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter GlanceLinksTests
  ```

  Expected tail:

  ```
  Test Suite 'GlanceLinksTests' passed at ...
  	 Executed 5 tests, with 0 failures (0 unexpected) in 0.001 (0.001) seconds
  ```

  Then:

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceLinks.swift native/macos/MCPProxy/MCPProxyTests/GlanceLinksTests.swift && git commit -m "feat(tray): build the session-filtered Web UI activity link"
  ```

- [ ] **Step 14: Add the glance stored properties to AppController**

  In `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift`, replace lines 24–25:

  ```swift
      private var cancellables = Set<AnyCancellable>()
      private var keyMonitor: Any?
  ```

  with:

  ```swift
      private var cancellables = Set<AnyCancellable>()
      private var keyMonitor: Any?

      /// Tray Glance: builds the activity / clients / histogram rows. Rows call
      /// back into this delegate so the Web UI key handling stays in one place.
      private lazy var glance = GlanceSection(
          target: self,
          openActivityAction: #selector(openActivityForSession(_:))
      )

      /// The glance rows currently installed in the menu, in menu order. Held so
      /// a rebuild that lands while the menu is open can rewrite them in place.
      private var installedGlanceRows: [NSMenuItem] = []

      /// Suppresses structural rebuilds while the menu is on screen.
      private var rebuildGuard = MenuRebuildGuard()
  ```

- [ ] **Step 15: Arm the guard in `menuWillOpen` and add `menuDidClose`**

  In the same file, replace the `menuWillOpen` body at lines 192–197:

  ```swift
      func menuWillOpen(_ menu: NSMenu) {
          // Spec 048: dropped the per-click `client.servers()` fetch. appState
          // is fed by SSE (spec 047), so it's already current within ~50 ms of
          // the last upstream state change. Rebuild from in-memory state only.
          rebuildMenu()
      }
  ```

  with:

  ```swift
      func menuWillOpen(_ menu: NSMenu) {
          // Only the status-bar menu drives the rebuild guard. NSMenuDelegate
          // callbacks are delivered for whichever menu holds the delegate, and the
          // glance histogram submenu builds its chart lazily on open — so it needs
          // a delegate too. Without this check, opening that submenu would run a
          // full rebuild (removeAllItems) on a menu that is on screen, and would
          // re-arm/disarm the guard under the parent menu: exactly the
          // restructuring-while-open the design forbids.
          guard menu === statusItem.menu else { return }

          // Spec 048: dropped the per-click `client.servers()` fetch. appState
          // is fed by SSE (spec 047), so it's already current within ~50 ms of
          // the last upstream state change. Rebuild from in-memory state only.
          //
          // The guard is armed AFTER this rebuild: AppKit calls menuWillOpen
          // before the menu is drawn, so restructuring here is safe and hands the
          // user fresh rows. Every rebuild from this point on happens under the
          // cursor and must not add or remove items.
          rebuildMenu()
          rebuildGuard.menuWillOpen()
      }

      func menuDidClose(_ menu: NSMenu) {
          guard menu === statusItem.menu else { return }

          // Run the structural rebuild that was suppressed while the menu was up.
          // Deferred to the next run-loop turn: AppKit is still tearing the menu
          // down inside this callback, and mutating it here is not safe.
          guard rebuildGuard.menuDidClose() else { return }
          DispatchQueue.main.async { [weak self] in
              self?.rebuildMenu()
          }
      }
  ```

- [ ] **Step 16: Put the guard at the head of `rebuildMenu()`**

  Replace lines 523–524:

  ```swift
      private func rebuildMenu() {
          let menu: NSMenu
  ```

  with:

  ```swift
      private func rebuildMenu() {
          // Tray Glance: decide what we are allowed to do before touching the menu.
          // Building the rows is pure — `items(for:)` reads AppState only and
          // issues no request — so it is safe to do this on every rebuild, even
          // one we are about to discard (spec 048: menu open costs zero network).
          let freshGlanceRows = glance.items(for: appState)
          let compatible = glanceRowsAreCompatible(installed: installedGlanceRows, fresh: freshGlanceRows)
          switch rebuildGuard.decide(structureChanged: !compatible) {
          case .updateInPlace:
              applyGlanceRowsInPlace(installed: installedGlanceRows, fresh: freshGlanceRows)
              return
          case .deferUntilClose:
              return
          case .rebuild:
              break
          }

          let menu: NSMenu
  ```

- [ ] **Step 17: Insert the glance rows between the status header and "Needs Attention"**

  Replace the separator + comment at lines 591–593:

  ```swift
          menu.addItem(.separator())

          // Needs Attention — only auth required, connection errors, quarantine (NOT disabled)
  ```

  with:

  ```swift
          menu.addItem(.separator())

          // Tray Glance — recent tool calls, connected clients, 24h histogram.
          // Hidden entirely when the core is not connected: `items(for:)` returns
          // [] and this loop adds nothing.
          installedGlanceRows = freshGlanceRows
          for row in freshGlanceRows {
              menu.addItem(row)
          }

          // Needs Attention — only auth required, connection errors, quarantine (NOT disabled)
  ```

- [ ] **Step 18: Add the `openActivityForSession` action**

  In the same file, insert this immediately before `@objc private func openConfigFile() {` (line 998, right after `openWebUI()` ends at line 996):

  ```swift
      /// Open the Web UI activity log filtered by a glance row's session.
      ///
      /// Reuses `openWebUI()`'s key path: `webUIBaseURL` is scheme/host/port only
      /// and a first-time browser session needs the API key appended, which only
      /// the core manager holds. A row with no session id (an empty-state row, or
      /// a record the core never attributed) opens the unfiltered log.
      @objc private func openActivityForSession(_ sender: NSMenuItem) {
          let sessionID = sender.representedObject as? String
          Task {
              let apiKey = await coreManager?.currentAPIKey ?? ""
              let baseURL = await MainActor.run { appState.webUIBaseURL }
              let urlString = activityURLString(baseURL: baseURL, apiKey: apiKey, sessionID: sessionID)
              if let url = URL(string: urlString) {
                  NSWorkspace.shared.open(url)
              }
          }
      }

  ```

- [ ] **Step 19: Build the app target and watch it compile**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift build
  ```

  Expected tail — the load-bearing line is the last one; the `[n/m]` indices shift as earlier tasks add files, so do not match on them:

  ```
  [41/43] Linking MCPProxy
  [42/43] Applying MCPProxy
  Build complete! (3.13s)
  ```

- [ ] **Step 20: Delete the dead `Menu/TrayMenu.swift` and prove nothing referenced it**

  ```bash
  cd /Users/user/repos/mcpproxy-go && git rm native/macos/MCPProxy/MCPProxy/Menu/TrayMenu.swift && grep -rn "TrayMenu" native --include="*.swift"
  ```

  Expected: `git rm` prints `rm 'native/macos/MCPProxy/MCPProxy/Menu/TrayMenu.swift'`, and the grep prints **exactly one line and exits 0** — the header comment Task 5 left behind, which is prose, not a reference:

  ```
  native/macos/MCPProxy/MCPProxy/Menu/Glance/GlanceFormatting.swift:6:// Salvaged from the retired Menu/TrayMenu.swift.
  ```

  To prove there is no *code* reference, filter that one file out; this prints nothing and exits 1:

  ```bash
  cd /Users/user/repos/mcpproxy-go && grep -rn "TrayMenu" native --include="*.swift" | grep -v "Menu/Glance/GlanceFormatting.swift:"
  ```

  (`Menu/TrayIcon.swift` is a separate, out-of-scope file and does not mention `TrayMenu`. `TrayMenu.swift` declared exactly one top-level symbol, `struct TrayMenu: View`.)

  Then confirm the target still builds without it:

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift build
  ```

  Expected: ends with `Build complete!`.

- [ ] **Step 21: Run the whole Swift suite exactly as CI does**

  ```bash
  cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test
  ```

  Expected tail (the test count grows as earlier tasks land — 344 was the pre-plan baseline; the load-bearing part is `0 failures`):

  ```
  Test Suite 'MCPProxyPackageTests.xctest' passed at ...
  	 Executed 344 tests, with 0 failures (0 unexpected) in 0.084 (0.095) seconds
  ✔ Test run with 0 tests in 0 suites passed after 0.001 seconds.
  ```

  **If the run dies with `error: fatalError` and 0 tests executed, check whether the failure is in this task's files before debugging them.** A known way for the whole test target to fail to compile is a `UsageBucket` that only has `init(from decoder:)` — a custom decoding initializer suppresses the memberwise init, so `AppStateGlanceTests.swift`'s `UsageBucket(start:calls:errors:totalRespBytes:)` call fails with `missing argument for parameter 'from' in call`. That belongs to Tasks 2–4 (the real `UsageBucket` must declare an explicit memberwise `init`), not to this one, but this step's gate cannot go green until it is fixed.

- [ ] **Step 22: Commit the integration**

  `TrayMenu.swift`'s deletion was already staged by the `git rm` in Step 20 — do **not** pass that path to `git add`. After `git rm`, the path exists neither in the worktree nor in the index, so `git add` on it fails with `fatal: pathspec ... did not match any files` (exit 128) and would abort the `&&` chain before `git commit` ever runs.

  ```bash
  cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift && git commit -m "feat(tray): render the glance section in the menu, guard rebuilds while it is open

Inserts GlanceSection rows between the status header and Needs Attention,
suppresses structural rebuilds while the menu is on screen (rows update in
place, the deferred rebuild runs on menuDidClose), adds the session-filtered
Web UI deep link, and deletes the dead Menu/TrayMenu.swift whose helpers now
live in Menu/Glance/GlanceFormatting.swift."
  ```

  Verify both changes landed in the commit:

  ```bash
  cd /Users/user/repos/mcpproxy-go && git show --stat --name-status HEAD
  ```

  Expected: an `M` line for `native/macos/MCPProxy/MCPProxy/MCPProxyApp.swift` and a `D` line for `native/macos/MCPProxy/MCPProxy/Menu/TrayMenu.swift`.

---

### Task 8: Swift — `ActivityHistogramView` in the "Activity (24h)" submenu

The 24-hour calls-per-hour bar chart. It is the only SwiftUI in the tray menu, hosted in the submenu's single custom item and built when the submenu opens. It renders from `AppState.usageTimeline` and issues **no** network request of its own (spec-048 invariant).

Three correctness traps this task exists to avoid:

1. A `UsageTimeBucket`'s `calls` field **already includes** its `errors`. The type is declared at `internal/contracts/types.go:406`, but the relationship is only visible in the aggregator: `internal/runtime/usage_aggregate.go:237-239` runs `b.Calls++` unconditionally and `b.Errors++` only `if rec.Status == "error"`. Stacking the raw fields draws every failure twice. The two segments must be `calls - errors` and `errors`.
2. The endpoint returns **only hours that exist** — the timeline is sparse. Missing hours must be synthesised as zero so the axis is a stable 24 hours instead of a jumping, variable-width chart.
3. A bar chart is opaque to VoiceOver, so the hosted item carries one accessibility label summarising the whole series (total calls, peak hour, error count).

**Files:**

- Create: `native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift`
- Create (test): `native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift`
- Modify: `native/macos/MCPProxy/MCPProxy/State/AppState.swift` — add `usageError` beside the existing usage fields (lines 102–105 once the AppState-fields task has landed), clear it in `updateUsage` (lines 341–347), clear it in `clearGlanceState` (lines 349–357), add `recordUsageFailure(_:)` after `updateUsage`. **None of these symbols exist in committed `HEAD`** — they arrive with the AppState-fields task listed under *Consumes*, and the line numbers above describe the file only after that task commits. Every step below therefore anchors on declaration text, not on a line number; treat the numbers as orientation only.

No `Package.swift` change is needed. Both targets glob their directory (`path: "MCPProxy"` / `path: "MCPProxyTests"`), so a new file in any subdirectory — including `Menu/Glance/` — is compiled with no registration. Verified: `swift package describe` lists `Menu/Glance/ActivityHistogramView.swift` in the `MCPProxy` target.

**Interfaces:**

*Consumes* (must already exist and be committed before this task starts):

```swift
// From the API-models task, in MCPProxy/API/UsageStub.swift. Note `Int`, not
// `Int64`; a SYNTHESIZED memberwise init (no custom `init` in the struct body);
// and `Codable` conformance in an extension, so the memberwise init stays
// internal-visible to the test target.
struct UsageBucket: Equatable {
    let start: Date
    let calls: Int
    let errors: Int
    let totalRespBytes: Int
}

extension UsageBucket: Codable {
    enum CodingKeys: String, CodingKey {
        case start, calls, errors
        case totalRespBytes = "total_resp_bytes"
    }
}

// From the AppState-fields task, on `final class AppState: ObservableObject`:
@Published var usageTimeline: [UsageBucket]?     // nil == "not loaded yet"
@Published var callsThisHour: Int?
@MainActor func updateUsage(timeline: [UsageBucket], now: Date = Date())
func clearGlanceState()                          // deliberately NOT @MainActor
static func floorToHour(_ date: Date) -> Date
```

*Produces* (later tasks — the menu wiring and the usage-refresh loop — rely on exactly these):

```swift
struct HistogramBar: Identifiable, Equatable {
    let hourStart: Date
    let succeeded: Int
    let errors: Int
    var id: Date { hourStart }
    var total: Int { succeeded + errors }
}

enum HistogramState: Equatable {
    case loading
    case failed(String)
    case loaded([HistogramBar])
}

enum ActivityHistogram {
    static let hourCount: Int                                  // 24
    static let chartItemSize: NSSize                           // 288 x 112 — the hosting view's measured fittingSize
    static func floorToHour(_ date: Date) -> Date              // delegates to AppState.floorToHour
    static func bars(from timeline: [UsageBucket], now: Date) -> [HistogramBar]
    static func state(timeline: [UsageBucket]?, errorMessage: String?, now: Date) -> HistogramState
    static func accessibilitySummary(bars: [HistogramBar], timeZone: TimeZone = .current) -> String
    static func chartMenuItem(bars: [HistogramBar]) -> NSMenuItem
}

struct ActivityHistogramView: View {
    let bars: [HistogramBar]
    let accessibilitySummary: String
}

final class ActivityHistogramSubmenu: NSObject, NSMenuDelegate {
    let menuItem: NSMenuItem                                   // insert THIS into the tray menu
    init(appState: AppState,
         now: @escaping () -> Date = Date.init,
         chartItemFactory: @escaping ([HistogramBar]) -> NSMenuItem = ActivityHistogram.chartMenuItem)
    func menuNeedsUpdate(_ menu: NSMenu)
    func currentItem() -> NSMenuItem
    static func mutedItem(_ title: String) -> NSMenuItem
}

// Added to AppState by this task:
@Published var usageError: String?
@MainActor func recordUsageFailure(_ message: String)
```

**Deliberate extension of the design doc.** `docs/superpowers/specs/2026-07-29-tray-glance-design.md` names four new `AppState` fields (line 46) and its States table (lines 139–144) covers only "not loaded yet" and "loaded but idle". `usageError` / `HistogramState.failed` are a fifth field and a third state, added under the doc's own second constraint ("never lie"): without them a permanently failing refresh sits on "Loading…" forever, which is exactly the quiet lie the design forbids. Flagging it here so review reads it as an argued addition, not scope creep.

---

- [ ] **Step 1: Write the failing bucket-shaping tests**

Create `native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift` with exactly this content:

```swift
import XCTest
import AppKit
@testable import MCPProxy

/// Shared anchors. All times are UTC: the backend aligns buckets with
/// `rec.Timestamp.UTC().Truncate(time.Hour)`, so the tests use the same grid.
/// A top-level `private` declaration is visible to the whole file, so every
/// test class below shares these.
private enum Fixture {
    /// 2027-01-15 08:35:00 UTC — deliberately mid-hour, so flooring is exercised.
    static let now = Date(timeIntervalSince1970: 1_800_002_100)
    /// 2027-01-15 08:00:00 UTC — the hour containing `now`; the newest bar.
    static let currentHour = Date(timeIntervalSince1970: 1_800_000_000)
    /// 2027-01-15 04:00:00 UTC — four hours back; bar index 19 on a 24-bar axis.
    static let fourHoursAgo = Date(timeIntervalSince1970: 1_799_985_600)
    /// 2027-01-14 09:00:00 UTC — the oldest bar on the axis.
    static let oldestHour = Date(timeIntervalSince1970: 1_799_917_200)
    /// 2027-01-13 06:00:00 UTC — 27 hours before the oldest bar, well off the
    /// left edge of the axis.
    static let offAxis = Date(timeIntervalSince1970: 1_799_820_000)

    static let utc = TimeZone(identifier: "UTC")!

    static func bucket(start: Date, calls: Int, errors: Int) -> UsageBucket {
        UsageBucket(start: start, calls: calls, errors: errors, totalRespBytes: 0)
    }
}

final class ActivityHistogramBarsTests: XCTestCase {

    /// The usage endpoint omits hours with no activity, so the timeline is
    /// sparse. The axis must still be a stable 24 hours, oldest first, ending
    /// at the hour containing `now`.
    func testMissingHoursAreSynthesisedAsZero() {
        let timeline = [
            // 08:20 UTC — must land in the 08:00 bucket, not its own.
            Fixture.bucket(start: Date(timeIntervalSince1970: 1_800_001_200), calls: 10, errors: 3),
            Fixture.bucket(start: Fixture.fourHoursAgo, calls: 4, errors: 0)
        ]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars.count, 24)
        XCTAssertEqual(bars.first?.hourStart, Fixture.oldestHour)
        XCTAssertEqual(bars.last?.hourStart, Fixture.currentHour)
        XCTAssertEqual(bars[19].hourStart, Fixture.fourHoursAgo)
        XCTAssertEqual(bars[19].succeeded, 4)
        XCTAssertEqual(bars[19].errors, 0)
        XCTAssertEqual(bars.filter { $0.total > 0 }.count, 2, "every other hour is a synthesised zero")
    }

    /// A bucket's `calls` ALREADY includes its `errors`. The two stacked
    /// segments must therefore sum to `calls`, never to `calls + errors`.
    func testStackedSegmentsDoNotDoubleCountErrors() {
        let timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 10, errors: 3)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars[23].succeeded, 7)
        XCTAssertEqual(bars[23].errors, 3)
        XCTAssertEqual(bars[23].total, 10)
    }

    /// Defensive: a bucket claiming more errors than calls must not produce a
    /// negative segment — Charts would draw it below the axis.
    func testErrorsExceedingCallsClampSucceededToZero() {
        let timeline = [Fixture.bucket(start: Fixture.currentHour, calls: 2, errors: 5)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars[23].succeeded, 0)
        XCTAssertEqual(bars[23].errors, 5)
    }

    /// Buckets older than the axis are dropped, not folded into the oldest bar
    /// (which would make yesterday's spike look like this morning's).
    func testBucketsOlderThanTheAxisAreDropped() {
        let timeline = [Fixture.bucket(start: Fixture.offAxis, calls: 99, errors: 9)]

        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        XCTAssertEqual(bars.reduce(0) { $0 + $1.total }, 0)
    }
}
```

- [ ] **Step 2: Run the new tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramBarsTests 2>&1 | tail -20
```

The test target fails to **compile** — no test executes. Expect lines like:

```
.../MCPProxyTests/ActivityHistogramTests.swift:44:20: error: cannot find 'ActivityHistogram' in scope
   let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)
              `- error: cannot find 'ActivityHistogram' in scope
error: fatalError
```

If instead you see `cannot find 'UsageBucket' in scope`, the API-models task listed under *Consumes* has not landed — stop and resolve that first. Likewise, if step 13's tests later report `AppState` has no `floorToHour`, the AppState-fields task has not landed.

- [ ] **Step 3: Create the file with the bar model and the bucket shaping**

Create `native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift`:

```swift
// ActivityHistogramView.swift
// MCPProxy
//
// The 24-hour calls-per-hour bar chart shown in the tray glance's
// "Activity (24h)" submenu, plus the pure bucket-shaping and accessibility
// helpers it renders from.
//
// The chart renders from `AppState.usageTimeline` only — opening the submenu
// performs no network request (spec 048 invariant).

import Foundation

// MARK: - Bar model

/// One hour of the 24-hour histogram, already split into the two stacked
/// segments the chart draws.
///
/// A `UsageBucket`'s `calls` ALREADY includes its `errors`, so stacking the raw
/// fields would draw every failure twice. `succeeded` is the difference.
struct HistogramBar: Identifiable, Equatable {
    /// Start of the UTC hour this bar covers.
    let hourStart: Date
    /// Calls that did not fail: `calls - errors`, never negative.
    let succeeded: Int
    /// Calls that failed.
    let errors: Int

    var id: Date { hourStart }

    /// Total calls in the hour — the height of the stacked bar.
    var total: Int { succeeded + errors }
}

// MARK: - Pure helpers

/// Bucket shaping and accessibility copy for the 24-hour histogram.
/// Pure and synchronous, so it is testable without AppKit or a window server.
enum ActivityHistogram {

    /// Bars on the axis. Fixed, so the axis does not resize as traffic starts
    /// and stops.
    static let hourCount = 24

    /// Truncate a date to the start of its UTC hour.
    ///
    /// Delegates to `AppState.floorToHour` rather than reimplementing it: the
    /// header count (`AppState.callsInCurrentHour`) and this axis must agree on
    /// where an hour begins, and two copies of the rule would be free to drift.
    static func floorToHour(_ date: Date) -> Date {
        AppState.floorToHour(date)
    }

    /// Project a sparse timeline onto a dense 24-hour axis ending at the UTC
    /// hour containing `now`, oldest hour first.
    ///
    /// The endpoint returns only hours that exist, so missing hours are
    /// synthesised as zero and buckets older than the axis are dropped.
    static func bars(from timeline: [UsageBucket], now: Date) -> [HistogramBar] {
        var succeededByHour: [Date: Int] = [:]
        var errorsByHour: [Date: Int] = [:]

        for bucket in timeline {
            let hour = floorToHour(bucket.start)
            let errors = max(0, bucket.errors)
            // `calls` includes `errors`; clamp so a malformed bucket where
            // errors > calls cannot produce a negative segment.
            let succeeded = max(0, bucket.calls - errors)
            succeededByHour[hour, default: 0] += succeeded
            errorsByHour[hour, default: 0] += errors
        }

        let currentHour = floorToHour(now)
        return (0..<hourCount).reversed().map { offset in
            let hour = currentHour.addingTimeInterval(TimeInterval(-3600 * offset))
            return HistogramBar(
                hourStart: hour,
                succeeded: succeededByHour[hour] ?? 0,
                errors: errorsByHour[hour] ?? 0
            )
        }
    }
}
```

- [ ] **Step 4: Run the tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramBarsTests 2>&1 | tail -12
```

Expect (first run rebuilds the tray target, ~20 s):

```
Test Case '-[MCPProxyTests.ActivityHistogramBarsTests testBucketsOlderThanTheAxisAreDropped]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramBarsTests testErrorsExceedingCallsClampSucceededToZero]' passed (0.000 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramBarsTests testMissingHoursAreSynthesisedAsZero]' passed (0.000 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramBarsTests testStackedSegmentsDoNotDoubleCountErrors]' passed (0.000 seconds).
Executed 4 tests, with 0 failures (0 unexpected) in 0.001 (0.002) seconds
```

- [ ] **Step 5: Commit the bucket shaping**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift && git commit -m "feat(tray): shape sparse usage buckets onto a dense 24h histogram axis

A UsageBucket's calls field already includes its errors, so the stacked
segments are calls-errors and errors. Hours the endpoint omits are
synthesised as zero to keep the axis a stable 24 hours."
```

- [ ] **Step 6: Write the failing accessibility-summary tests**

Append to `native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift`:

```swift
final class ActivityHistogramAccessibilityTests: XCTestCase {

    /// A bar chart is opaque to VoiceOver, so the hosted item carries one
    /// sentence describing the whole series.
    func testSummaryReportsTotalsAndPeakHour() {
        let timeline = [
            Fixture.bucket(start: Fixture.currentHour, calls: 10, errors: 3),
            Fixture.bucket(start: Fixture.fourHoursAgo, calls: 4, errors: 0)
        ]
        let bars = ActivityHistogram.bars(from: timeline, now: Fixture.now)

        let summary = ActivityHistogram.accessibilitySummary(bars: bars, timeZone: Fixture.utc)

        XCTAssertEqual(
            summary,
            "Activity over the last 24 hours: 14 calls, 3 errors. Busiest hour 08:00 with 10 calls."
        )
    }

    /// "Loaded but idle" must read as idle, not as a broken chart.
    func testSummaryForAnIdleTimelineSaysSo() {
        let bars = ActivityHistogram.bars(from: [], now: Fixture.now)

        XCTAssertEqual(
            ActivityHistogram.accessibilitySummary(bars: bars, timeZone: Fixture.utc),
            "Activity over the last 24 hours: no tool calls."
        )
    }
}
```

- [ ] **Step 7: Run the accessibility tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramAccessibilityTests 2>&1 | tail -12
```

Compile failure, no test executes:

```
.../MCPProxyTests/ActivityHistogramTests.swift:NN:44: error: type 'ActivityHistogram' has no member 'accessibilitySummary'
error: fatalError
```

- [ ] **Step 8: Implement `accessibilitySummary`**

In `native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift`, insert this method inside `enum ActivityHistogram`, immediately after the closing brace of `bars(from:now:)` and before the enum's own closing brace:

```swift
    /// One sentence describing the whole series, because a bar chart is opaque
    /// to VoiceOver. Ties on the peak resolve to the earliest hour.
    ///
    /// - Parameter timeZone: injected so the hour label is deterministic in
    ///   tests; production uses the user's zone.
    static func accessibilitySummary(bars: [HistogramBar], timeZone: TimeZone = .current) -> String {
        let totalCalls = bars.reduce(0) { $0 + $1.total }
        let totalErrors = bars.reduce(0) { $0 + $1.errors }
        guard let first = bars.first, totalCalls > 0 else {
            return "Activity over the last 24 hours: no tool calls."
        }

        var peak = first
        for bar in bars where bar.total > peak.total { peak = bar }

        let formatter = DateFormatter()
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = timeZone
        formatter.dateFormat = "HH:mm"

        return "Activity over the last 24 hours: \(totalCalls) calls, \(totalErrors) errors. "
            + "Busiest hour \(formatter.string(from: peak.hourStart)) with \(peak.total) calls."
    }
```

- [ ] **Step 9: Run the accessibility tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramAccessibilityTests 2>&1 | tail -8
```

Expect:

```
Test Case '-[MCPProxyTests.ActivityHistogramAccessibilityTests testSummaryForAnIdleTimelineSaysSo]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramAccessibilityTests testSummaryReportsTotalsAndPeakHour]' passed (0.000 seconds).
Executed 2 tests, with 0 failures (0 unexpected) in 0.001 (0.002) seconds
```

- [ ] **Step 10: Commit the accessibility summary**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift && git commit -m "feat(tray): summarise the 24h histogram in one VoiceOver sentence

Total calls, error count and peak hour, so the chart is not a silent
element for screen-reader users."
```

- [ ] **Step 11: Write the failing `usageError` tests**

`usageTimeline == nil` currently means both "not loaded yet" and "the fetch failed" — the submenu cannot tell them apart, so a permanently failing refresh would show "Loading…" forever. Add a distinct signal.

Append to `native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift`:

```swift
@MainActor
final class AppStateUsageErrorTests: XCTestCase {

    func testRecordUsageFailureStoresTheMessage() {
        let state = AppState()

        XCTAssertNil(state.usageError)
        state.recordUsageFailure("connection refused")
        XCTAssertEqual(state.usageError, "connection refused")
    }

    /// A successful refresh must clear a stale failure, otherwise the submenu
    /// would show the error row forever once a single fetch had failed.
    func testUpdateUsageClearsAPreviousFailure() {
        let state = AppState()
        state.recordUsageFailure("connection refused")

        state.updateUsage(
            timeline: [Fixture.bucket(start: Fixture.currentHour, calls: 1, errors: 0)],
            now: Fixture.now
        )

        XCTAssertNil(state.usageError)
        XCTAssertEqual(state.callsThisHour, 1)
    }

    /// Disconnecting must not leave the previous core's failure on screen.
    func testClearGlanceStateClearsTheFailure() {
        let state = AppState()
        state.recordUsageFailure("connection refused")

        state.clearGlanceState()

        XCTAssertNil(state.usageError)
    }
}
```

- [ ] **Step 12: Run the `usageError` tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateUsageErrorTests 2>&1 | tail -12
```

Compile failure:

```
.../MCPProxyTests/ActivityHistogramTests.swift:NN:23: error: value of type 'AppState' has no member 'usageError'
.../MCPProxyTests/ActivityHistogramTests.swift:NN:15: error: value of type 'AppState' has no member 'recordUsageFailure'
error: fatalError
```

- [ ] **Step 13: Add `usageError` and `recordUsageFailure` to `AppState`**

Three **additive** edits in `native/macos/MCPProxy/MCPProxy/State/AppState.swift`. Each is anchored on a declaration, not a line number, and none rewrites an existing method body — the AppState-fields task's `updateUsage` may guard its assignments (`if usageTimeline != timeline { usageTimeline = timeline }`) and a wholesale replacement would silently revert that.

(a) Add the field. Find this declaration (around line 105) and add the new property directly below it:

```swift
    /// Calls recorded in the CURRENT UTC hour. `nil` means "not loaded yet".
    @Published var callsThisHour: Int?
```

becomes:

```swift
    /// Calls recorded in the CURRENT UTC hour. `nil` means "not loaded yet".
    @Published var callsThisHour: Int?

    /// Last usage-refresh failure, surfaced as a muted row in the histogram
    /// submenu. `nil` means "no failure recorded"; the next successful refresh
    /// clears it. Without this the submenu could not tell "still loading" from
    /// "the fetch failed" — both leave `usageTimeline` nil, and a permanently
    /// failing refresh would sit on "Loading…" forever.
    @Published var usageError: String?
```

(b) Clear it on success and add the recorder. Inside `updateUsage(timeline:now:)`, find its last statement:

```swift
        if callsThisHour != calls { callsThisHour = calls }
    }
```

and extend it to:

```swift
        if callsThisHour != calls { callsThisHour = calls }
        if usageError != nil { usageError = nil }
    }

    /// Record a failed usage refresh so the histogram submenu can say so
    /// instead of showing "Loading…" forever. Called from the usage refresh's
    /// catch block.
    @MainActor
    func recordUsageFailure(_ message: String) {
        if usageError != message { usageError = message }
    }
```

Leave every other line of `updateUsage` exactly as you found it.

(c) Reset it on disconnect. In `clearGlanceState()`, add one line after the `callsThisHour` reset:

```swift
        if callsThisHour != nil { callsThisHour = nil }
        if usageError != nil { usageError = nil }
```

- [ ] **Step 14: Run the `usageError` tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter AppStateUsageErrorTests 2>&1 | tail -8
```

Expect:

```
Test Case '-[MCPProxyTests.AppStateUsageErrorTests testClearGlanceStateClearsTheFailure]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.AppStateUsageErrorTests testRecordUsageFailureStoresTheMessage]' passed (0.000 seconds).
Test Case '-[MCPProxyTests.AppStateUsageErrorTests testUpdateUsageClearsAPreviousFailure]' passed (0.000 seconds).
Executed 3 tests, with 0 failures (0 unexpected) in 0.001 (0.002) seconds
```

- [ ] **Step 15: Commit the failure signal**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/State/AppState.swift native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift && git commit -m "feat(tray): distinguish a failed usage refresh from \"not loaded yet\"

usageTimeline == nil meant both, so a permanently failing refresh would
show \"Loading…\" forever. usageError carries the message; a successful
refresh and a disconnect both clear it."
```

- [ ] **Step 16: Write the SwiftUI chart and the hosted menu item**

Append to `native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift`, and change the file's `import Foundation` at the top to the three imports shown first (SwiftUI and AppKit both re-export Foundation, so `Date`/`TimeInterval` stay available):

```swift
import SwiftUI
import Charts
import AppKit
```

```swift
// MARK: - Chart

/// The stacked bar chart itself. Rendering only — it fetches nothing.
///
/// Two `BarMark`s per hour sharing an x value stack automatically; the series
/// are `calls - errors` and `errors`, never the raw fields.
struct ActivityHistogramView: View {
    let bars: [HistogramBar]
    let accessibilitySummary: String

    var body: some View {
        Chart {
            ForEach(bars) { bar in
                BarMark(
                    x: .value("Hour", bar.hourStart, unit: .hour),
                    y: .value("Calls", bar.succeeded)
                )
                .foregroundStyle(by: .value("Outcome", "Succeeded"))

                BarMark(
                    x: .value("Hour", bar.hourStart, unit: .hour),
                    y: .value("Calls", bar.errors)
                )
                .foregroundStyle(by: .value("Outcome", "Errors"))
            }
        }
        .chartForegroundStyleScale([
            "Succeeded": Color.accentColor,
            "Errors": Color.red
        ])
        // The legend would double the item's height for two self-evident
        // colours; the accessibility label names both series instead.
        .chartLegend(.hidden)
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 3))
        }
        .chartXAxis {
            AxisMarks(values: .stride(by: .hour, count: 6)) { _ in
                AxisGridLine()
                AxisValueLabel(format: .dateTime.hour())
            }
        }
        .frame(width: 260, height: 96)
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
        // One label for the whole chart: VoiceOver reading 48 unlabelled bar
        // marks would be worse than useless.
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Text(accessibilitySummary))
    }
}

extension ActivityHistogram {

    /// Size of the hosted chart item, in points. Menu items do not auto-size a
    /// hosting view, so the frame is explicit — and it must match the view's
    /// own size, or the row grows a band of dead space. 260 + 2*14 = 288 wide,
    /// 96 + 2*8 = 112 tall; measured `NSHostingView.fittingSize` agrees.
    static let chartItemSize = NSSize(width: 288, height: 112)

    /// The submenu's single custom item: an `NSHostingView` wrapping the chart.
    ///
    /// Custom menu-item views receive mouse events but not keyboard events, so
    /// the item is disabled (nothing to activate) and carries the whole series
    /// in one accessibility label on the host view.
    static func chartMenuItem(bars: [HistogramBar]) -> NSMenuItem {
        let summary = accessibilitySummary(bars: bars)
        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        item.isEnabled = false

        let host = NSHostingView(
            rootView: ActivityHistogramView(bars: bars, accessibilitySummary: summary)
        )
        host.frame = NSRect(origin: .zero, size: chartItemSize)
        host.setAccessibilityLabel(summary)
        item.view = host
        return item
    }
}
```

- [ ] **Step 17: Build and confirm the Charts code compiles**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift build 2>&1 | tail -5
```

Expect:

```
[N/N] Compiling MCPProxy ActivityHistogramView.swift
[N/N] Emitting module MCPProxy
Build complete! (X.XXs)
```

`import Charts` needs no `@available` guard: `Package.swift` declares `platforms: [.macOS(.v13)]` and Swift Charts ships from macOS 13.0.

- [ ] **Step 18: Commit the chart**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift && git commit -m "feat(tray): SwiftUI Charts bar chart for the 24h activity histogram

Two stacked BarMarks per hour hosted in an NSHostingView sized for a menu
item, with the series summary as the host view's accessibility label."
```

- [ ] **Step 19: Write the failing submenu tests**

Append to `native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift`:

```swift
@MainActor
final class ActivityHistogramSubmenuTests: XCTestCase {

    /// The chart item is stubbed so no test ever instantiates an
    /// `NSHostingView`, which would want a window server.
    private func makeSubmenu(_ state: AppState) -> ActivityHistogramSubmenu {
        ActivityHistogramSubmenu(
            appState: state,
            now: { Fixture.now },
            chartItemFactory: { bars in
                NSMenuItem(title: "CHART:\(bars.count)", action: nil, keyEquivalent: "")
            }
        )
    }

    /// Nothing is built until the submenu opens — that is the whole point of
    /// hanging the chart off the submenu delegate.
    func testSubmenuIsEmptyUntilItOpens() {
        let submenu = makeSubmenu(AppState())

        XCTAssertEqual(submenu.menuItem.title, "Activity (24h)")
        XCTAssertEqual(submenu.menuItem.submenu?.numberOfItems, 0)
    }

    func testLoadingRowWhileTheTimelineIsNil() {
        let submenu = makeSubmenu(AppState())
        let menu = submenu.menuItem.submenu!

        submenu.menuNeedsUpdate(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "Loading…")
        XCTAssertFalse(menu.items[0].isEnabled)
        let attributes = menu.items[0].attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    func testErrorRowWhenTheFetchFailedBeforeAnyTimelineArrived() {
        let state = AppState()
        state.recordUsageFailure("connection refused")
        let submenu = makeSubmenu(state)
        let menu = submenu.menuItem.submenu!

        submenu.menuNeedsUpdate(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "Usage unavailable")
        XCTAssertEqual(menu.items[0].toolTip, "connection refused")
        XCTAssertFalse(menu.items[0].isEnabled)
        let attributes = menu.items[0].attributedTitle!.attributes(at: 0, effectiveRange: nil)
        XCTAssertEqual(attributes[.foregroundColor] as? NSColor, NSColor.secondaryLabelColor)
    }

    /// Real data beats a stale failure.
    func testChartRowWinsOverAStaleFailure() {
        let state = AppState()
        state.recordUsageFailure("connection refused")
        state.usageTimeline = [Fixture.bucket(start: Fixture.currentHour, calls: 3, errors: 1)]
        let submenu = makeSubmenu(state)
        let menu = submenu.menuItem.submenu!

        submenu.menuNeedsUpdate(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "CHART:24")
    }

    /// "Loaded but idle" is a flat 24-hour axis, deliberately distinct from the
    /// loading row — and reopening replaces the row instead of appending.
    func testReopeningReplacesTheRowAndAnIdleTimelineStillCharts() {
        let state = AppState()
        let submenu = makeSubmenu(state)
        let menu = submenu.menuItem.submenu!

        submenu.menuNeedsUpdate(menu)
        state.usageTimeline = []
        submenu.menuNeedsUpdate(menu)

        XCTAssertEqual(menu.numberOfItems, 1)
        XCTAssertEqual(menu.items[0].title, "CHART:24")
    }

    func testStateResolution() {
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: nil, now: Fixture.now),
            .loading
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: "", now: Fixture.now),
            .loading,
            "an empty message is not a failure"
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: nil, errorMessage: "boom", now: Fixture.now),
            .failed("boom")
        )
        XCTAssertEqual(
            ActivityHistogram.state(timeline: [], errorMessage: nil, now: Fixture.now),
            .loaded(ActivityHistogram.bars(from: [], now: Fixture.now))
        )
    }
}
```

- [ ] **Step 20: Run the submenu tests and watch them fail**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramSubmenuTests 2>&1 | tail -14
```

Compile failure:

```
.../MCPProxyTests/ActivityHistogramTests.swift:NN:9: error: cannot find 'ActivityHistogramSubmenu' in scope
.../MCPProxyTests/ActivityHistogramTests.swift:NN:29: error: type 'ActivityHistogram' has no member 'state'
error: fatalError
```

- [ ] **Step 21: Implement `HistogramState`, `ActivityHistogram.state`, and the submenu controller**

Two additions to `native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift`.

(a) Insert the state enum immediately **above** `enum ActivityHistogram` (i.e. after the `HistogramBar` declaration):

```swift
// MARK: - What the submenu shows

/// What the histogram submenu renders right now.
///
/// `loading` and `failed` are deliberately distinct: both leave the timeline
/// nil, and telling the user "Loading…" forever after a failed fetch is the
/// kind of quiet lie this menu must not tell.
enum HistogramState: Equatable {
    /// No timeline yet, and no failure recorded.
    case loading
    /// The usage refresh failed before any timeline arrived; payload is the message.
    case failed(String)
    /// A full 24-hour axis, oldest hour first. An all-zero axis is a valid
    /// loaded state — the proxy was simply idle.
    case loaded([HistogramBar])
}
```

(b) Add `state(timeline:errorMessage:now:)` inside `enum ActivityHistogram`, after `accessibilitySummary`, and append the controller at the end of the file:

```swift
    /// Decide what the submenu shows. A timeline that has loaded wins over a
    /// recorded failure: showing real (if slightly stale) data beats showing an
    /// error row.
    static func state(timeline: [UsageBucket]?, errorMessage: String?, now: Date) -> HistogramState {
        if let timeline {
            return .loaded(bars(from: timeline, now: now))
        }
        if let errorMessage, !errorMessage.isEmpty {
            return .failed(errorMessage)
        }
        return .loading
    }
```

```swift
// MARK: - Submenu

/// Owns the tray's "Activity (24h)" item and rebuilds its single row when the
/// submenu opens.
///
/// Building on open — rather than on every `rebuildMenu()` — keeps the chart
/// off the main menu's hot path, and means an `NSHostingView` is only ever
/// created when the user actually looks at it. The controller reads `AppState`
/// and nothing else: opening the submenu performs no network request.
///
/// `NSMenu.delegate` is a weak reference, so whoever inserts `menuItem` into
/// the tray menu must also hold on to this object.
final class ActivityHistogramSubmenu: NSObject, NSMenuDelegate {

    /// The item to insert into the tray menu.
    let menuItem: NSMenuItem

    private let appState: AppState
    private let now: () -> Date
    private let chartItemFactory: ([HistogramBar]) -> NSMenuItem

    /// - Parameters:
    ///   - now: injected clock, so the 24-hour axis is deterministic in tests.
    ///   - chartItemFactory: injected so tests can assert on submenu structure
    ///     without instantiating an `NSHostingView`.
    init(appState: AppState,
         now: @escaping () -> Date = Date.init,
         chartItemFactory: @escaping ([HistogramBar]) -> NSMenuItem = ActivityHistogram.chartMenuItem) {
        self.appState = appState
        self.now = now
        self.chartItemFactory = chartItemFactory

        let item = NSMenuItem(title: "Activity (24h)", action: nil, keyEquivalent: "")
        let submenu = NSMenu(title: "Activity (24h)")
        // Nothing in here is actionable, and AppKit's automatic enabling runs
        // its own validation at display time. Turning it off makes the rows'
        // disabled state ours — and makes what the tests assert the same thing
        // the user sees.
        submenu.autoenablesItems = false
        item.submenu = submenu
        self.menuItem = item

        super.init()
        submenu.delegate = self
    }

    // MARK: NSMenuDelegate

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        menu.addItem(currentItem())
    }

    // MARK: Rows

    /// The single row the submenu shows for the current `AppState`.
    func currentItem() -> NSMenuItem {
        switch ActivityHistogram.state(
            timeline: appState.usageTimeline,
            errorMessage: appState.usageError,
            now: now()
        ) {
        case .loading:
            return Self.mutedItem("Loading…")
        case .failed(let message):
            let item = Self.mutedItem("Usage unavailable")
            item.toolTip = message
            return item
        case .loaded(let bars):
            return chartItemFactory(bars)
        }
    }

    /// A disabled, secondary-coloured text row. Setting `attributedTitle`
    /// leaves `title` intact, so the plain string stays available to tests and
    /// to accessibility.
    static func mutedItem(_ title: String) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        item.attributedTitle = NSAttributedString(string: title, attributes: [
            .font: NSFont.menuFont(ofSize: 0),
            .foregroundColor: NSColor.secondaryLabelColor
        ])
        return item
    }
}
```

- [ ] **Step 22: Run the submenu tests and watch them pass**

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test --filter ActivityHistogramSubmenuTests 2>&1 | tail -12
```

Expect:

```
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testChartRowWinsOverAStaleFailure]' passed (0.002 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testErrorRowWhenTheFetchFailedBeforeAnyTimelineArrived]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testLoadingRowWhileTheTimelineIsNil]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testReopeningReplacesTheRowAndAnIdleTimelineStillCharts]' passed (0.001 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testStateResolution]' passed (0.000 seconds).
Test Case '-[MCPProxyTests.ActivityHistogramSubmenuTests testSubmenuIsEmptyUntilItOpens]' passed (0.001 seconds).
Executed 6 tests, with 0 failures (0 unexpected) in 0.006 (0.007) seconds
```

- [ ] **Step 23: Run the whole native suite for regressions**

This is exactly what the `swift-test` job in `.github/workflows/native-tests.yml` runs (`working-directory: native/macos/MCPProxy`, `run: swift test`, no `--skip`).

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy && swift test 2>&1 | tail -6
```

Expect a green run — `0 failures`, and a test count 15 higher than before this task (4 + 2 + 3 + 6):

```
Test Suite 'All tests' passed at ....
	 Executed NNN tests, with 0 failures (0 unexpected) in X.XXX (X.XXX) seconds
Testing Library Version: 1902
Test run with 0 tests in 0 suites passed after 0.001 seconds.
```

(The trailing "0 tests in 0 suites" line is the swift-testing runner; this package has no swift-testing tests, only XCTest. That is normal.)

- [ ] **Step 24: Commit the submenu controller**

```bash
cd /Users/user/repos/mcpproxy-go && git add native/macos/MCPProxy/MCPProxy/Menu/Glance/ActivityHistogramView.swift native/macos/MCPProxy/MCPProxyTests/ActivityHistogramTests.swift && git commit -m "feat(tray): build the Activity (24h) submenu when it opens

An NSMenuDelegate on the submenu builds its single row on menuNeedsUpdate:
the hosted chart when a timeline is loaded, a muted Loading… row before the
first refresh, a muted Usage unavailable row (message in the tooltip) when
the fetch failed. Reads AppState only — no request on open."
```

---

## Verification

Run this end-to-end once every task has landed. Nothing here is optional: each command corresponds to a CI job or to a design constraint that unit tests cannot check.

### 1. Go — build, test, spec, lint

```bash
cd /Users/user/repos/mcpproxy-go

# Both editions compile
go build ./... && go build -tags server ./... && echo BUILD_OK

# Unit + race for the touched packages, then the whole tree
go test ./internal/storage/ ./internal/httpapi/ ./internal/runtime/ ./internal/server/ -count=1
go test -race ./internal/...

# OpenAPI artifacts must be byte-identical to a fresh generation (CI job: verify-oas)
make swagger-verify 2>&1 | tail -2      # expect "✅ OpenAPI artifacts are up to date."

# The strict CI linter (golangci-lint v2, .github/.golangci.yml)
/opt/homebrew/bin/golangci-lint run --config .github/.golangci.yml ./...

# API end-to-end
./scripts/test-api-e2e.sh
```

### 2. Go — the new parameter, against a live core

Use an isolated instance so the tray's core on `:8080` keeps its DB lock:

```bash
cd /Users/user/repos/mcpproxy-go
go build -o /tmp/mcpproxy-glance ./cmd/mcpproxy
/tmp/mcpproxy-glance serve --listen 127.0.0.1:18080 --data-dir /tmp/mcpproxy-glance-data &
KEY=$(jq -r .api_key /tmp/mcpproxy-glance-data/mcp_config.json)

curl -s -H "X-API-Key: $KEY" 'http://127.0.0.1:18080/api/v1/sessions?status=active&limit=25' | jq '.success, .data.total'
curl -s -o /dev/null -w '%{http_code}\n' -H "X-API-Key: $KEY" 'http://127.0.0.1:18080/api/v1/sessions?status=bogus'   # expect 400
curl -s -H "X-API-Key: $KEY" 'http://127.0.0.1:18080/api/v1/activity/usage?window=24h&top=1' | jq '.data.timeline | length'

pkill -f 'mcpproxy-glance serve'
```

### 3. Swift — build and full native suite

```bash
cd /Users/user/repos/mcpproxy-go/native/macos/MCPProxy
swift build
swift test          # exactly what .github/workflows/native-tests.yml runs; no --skip
```

Pass criterion is `0 failures (0 unexpected)`. The trailing `✔ Test run with 0 tests in 0 suites passed` line is the swift-testing runner and is expected — this package is XCTest-only.

### 4. macOS tray — visual and accessibility check

Follow `docs/development/macos-tray.md` (build → replace the app bundle → verify). Screen Recording permission is required for the screenshot tools.

```bash
cd /Users/user/repos/mcpproxy-go
./scripts/build-macos-tray.sh          # then replace /Applications/MCPProxy.app per the doc
open -a MCPProxy
```

Then, with the `mcpproxy-ui-test` MCP tools:

1. `list_running_apps` — confirm `MCPProxy` is up.
2. `read_status_bar` / `list_menu_items` — the glance block appears between the status header and "Needs Attention", in the order: summary · Recent · rows · Open Activity… · Clients · rows · Activity (24h).
3. `screenshot_status_bar_menu` — visually confirm status icons (shape, not colour alone), right-aligned relative ages, middle-truncated long tool names.
4. `click_menu_item` on "Activity (24h)" then `screenshot_status_bar_menu` — the histogram renders; bars are stacked with errors distinct.
5. `send_keypress` (arrow keys) — every text row is keyboard-navigable.
6. `check_accessibility` — activity rows, client rows and the chart all carry labels; the chart's label reads "Activity over the last 24 hours: N calls, M errors. Busiest hour HH:MM with K calls."
7. Drive some traffic through the proxy from an agent while the menu is **open** — rows update in place, the menu does not grow, shrink, or collapse the open submenu.
8. `click_menu_item` on an activity row — the browser opens `/ui/activity?session=<id>&apikey=…` and the Activity view is filtered to that session.
9. Stop the core from the menu — the whole glance block disappears; restart it and confirm the block returns empty/loading rather than showing the previous core's numbers.

### 5. Success criteria checklist (from the design spec's Testing section)

- [ ] **SSE naming regression** — `activity.tool_call.completed` reaches the activity handler (fails on `main` today).
- [ ] **No amplification** — handling one completed event issues zero REST requests; `activityVersion` is not bumped on the SSE tool-call path.
- [ ] **Session decoding regression** — `last_activity` decodes into `APIClient.MCPSession` (nil on `main` today).
- [ ] **Selection** — upstream `tool_call` always included; the three discovery/execution built-ins included; a failed `call_tool_write` record is included; a failed `upstream_servers` record is still excluded (rule 1 beats rule 3); five qualifying records selected from a 50-record page full of noise.
- [ ] **Header bucket** — a sparse timeline whose newest bucket is three hours old shows zero calls this hour, not that bucket's count.
- [ ] **Dashboard non-regression** — `recentActivity` still carries non-tool-call types and `recentSessions` still carries closed sessions after the glance feeds are added.
- [ ] **Deduplication** — a failed upstream call that emitted both a `tool_call` and a `call_tool_read` record under one request id renders exactly one row, the `tool_call` one; a pre-dispatch wrapper failure with no paired record still renders.
- [ ] **Open-menu stability** — a burst of SSE events updates rows in place and performs no structural rebuild; the deferred rebuild runs exactly once on close.
- [ ] **Row identity after in-place update** — `representedObject`, image, tooltip and accessibility label all describe the record the title now shows.
- [ ] **SSE adapter** — an `activity.internal_tool_call.completed` payload (`internal_tool_name`, optional `target_server`, Unix-seconds envelope) becomes an entry that passes selection and renders a correct relative time.
- [ ] **Reconnect hygiene** — after a disconnect, glance state is cleared, so a menu built between `.connected` and the first successful refresh shows empty/loading states.
- [ ] **Optimistic rows** — a row built from an SSE payload carries the failure text from `error_message`.
- [ ] **Chart data** — `calls - errors` and `errors` segments never double-count; missing hours synthesised as zero across a stable 24-hour axis.
- [ ] **Formatting** — relative time, `server:tool` label composition, middle truncation.
- [ ] **Visibility** — block hidden when the core is stopped or disconnected; empty-state rows ("No tool calls yet", "No connected clients") render as single muted rows.
- [ ] **Clients honesty** — an old-but-active session is returned by `?status=active` even when newer sessions would fill the page (storage-level test).
- [ ] **Invariant, spec 048** — opening the main menu, and opening the histogram submenu, issues no network requests; pinned by `CountingGlanceDataSource` and by `GlanceSection`/`ActivityHistogramSubmenu` reading `AppState` only.
- [ ] **Dead code removed** — `Menu/TrayMenu.swift` is deleted and nothing references `TrayMenu`.
- [ ] **VoiceOver pass** — rows and the chart's accessibility label verified live with `check_accessibility`.
