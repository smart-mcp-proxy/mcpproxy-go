package httpapi

import (
	"fmt"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/storage"
)

// Harness extension for the round-9 cross-review findings: the /servers/{id}
// read subtree (G1), the activity / tool-call / session doors (G2), the token
// stats aggregate (G3) and the status message (G7).
//
// These are methods on the SAME scopeController the #1166/#1167 tests use, so
// every test in the group runs against one two-server fixture (`alpha`, which
// the scoped token may see; `beta`, which it may not) through the production
// chi route table and the real agent-token middleware.

const (
	// Payloads that must never reach a token scoped to alpha. Each is planted
	// in the record type the corresponding door serves.
	betaLogSecret      = "SUPERSECRET_BETA_STDERR_ARGV"
	betaActivityArg    = "SUPERSECRET_BETA_ACTIVITY_ARG"
	betaToolCallArg    = "SUPERSECRET_BETA_TOOLCALL_ARG"
	alphaActivityArg   = "alpha-activity-argument"
	betaActivityID     = "01BETA00000000000000000000"
	alphaActivityID    = "01ALPHA0000000000000000000"
	systemActivityID   = "01SYSTEM000000000000000000"
	betaToolCallID     = "beta-tool-call-1"
	alphaToolCallID    = "alpha-tool-call-1"
	scopeStatusMessage = "Connected to 1/2 servers, retrying..."
)

// scopeActivities is the activity-log fixture: one record per allowed server,
// one per denied server, and one operator-plane record with NO server
// attribution (a config change), which a scoped caller must not see either.
func scopeActivities() []*storage.ActivityRecord {
	// Relative to now, not a fixed date: /activity/summary filters on a period
	// window, and a fixed timestamp silently ages out of it — which would leave
	// the summary test asserting "beta is absent" against an EMPTY summary.
	base := time.Now().UTC().Add(-time.Hour)
	return []*storage.ActivityRecord{
		{
			ID:         alphaActivityID,
			Type:       storage.ActivityTypeToolCall,
			ServerName: "alpha",
			ToolName:   "alpha_tool",
			Status:     storage.ActivityStatusSuccess,
			Timestamp:  base,
			Arguments:  map[string]interface{}{"token": alphaActivityArg},
			Response:   "alpha response",
		},
		{
			ID:         betaActivityID,
			Type:       storage.ActivityTypeToolCall,
			ServerName: "beta",
			ToolName:   "beta_tool",
			Status:     storage.ActivityStatusSuccess,
			Timestamp:  base.Add(time.Minute),
			Arguments:  map[string]interface{}{"token": betaActivityArg},
			Response:   "beta response containing " + betaEnvSecret,
		},
		{
			ID:        systemActivityID,
			Type:      storage.ActivityTypeConfigChange,
			Status:    storage.ActivityStatusSuccess,
			Timestamp: base.Add(2 * time.Minute),
			Arguments: map[string]interface{}{"field": "api_key"},
		},
	}
}

func (c *scopeController) ListActivities(filter storage.ActivityFilter) ([]*storage.ActivityRecord, int, error) {
	// Mirrors storage.Manager.ListActivities: ONE Matches() pass produces both
	// the page and the total, so a filter that leaks shows up in both.
	var matched []*storage.ActivityRecord
	for _, rec := range scopeActivities() {
		if filter.Matches(rec) {
			matched = append(matched, rec)
		}
	}
	total := len(matched)
	if filter.Offset < len(matched) {
		matched = matched[filter.Offset:]
	} else {
		matched = nil
	}
	if filter.Limit > 0 && len(matched) > filter.Limit {
		matched = matched[:filter.Limit]
	}
	return matched, total, nil
}

func (c *scopeController) StreamActivities(filter storage.ActivityFilter) <-chan *storage.ActivityRecord {
	ch := make(chan *storage.ActivityRecord, len(scopeActivities()))
	for _, rec := range scopeActivities() {
		if filter.Matches(rec) {
			ch <- rec
		}
	}
	close(ch)
	return ch
}

func (c *scopeController) GetActivity(id string) (*storage.ActivityRecord, error) {
	for _, rec := range scopeActivities() {
		if rec.ID == id {
			return rec, nil
		}
	}
	return nil, nil
}

// scopeToolCalls is the tool-call fixture, one record per server.
func scopeToolCalls() []*contracts.ToolCallRecord {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	return []*contracts.ToolCallRecord{
		{
			ID:         alphaToolCallID,
			ServerID:   "alpha",
			ServerName: "alpha",
			ToolName:   "alpha_tool",
			Arguments:  map[string]interface{}{"q": "alpha argument"},
			Timestamp:  base,
		},
		{
			ID:         betaToolCallID,
			ServerID:   "beta",
			ServerName: "beta",
			ToolName:   "beta_tool",
			Arguments:  map[string]interface{}{"q": betaToolCallArg},
			Timestamp:  base.Add(time.Minute),
		},
	}
}

func (c *scopeController) GetToolCalls(limit, offset int, scope storage.ToolCallScope) ([]*contracts.ToolCallRecord, int, error) {
	// Mirrors runtime.GetToolCalls: scope applied BEFORE pagination, so total
	// and page describe the same set.
	var visible []*contracts.ToolCallRecord
	for _, rec := range scopeToolCalls() {
		if scope.Allows(rec.ServerName) {
			visible = append(visible, rec)
		}
	}
	total := len(visible)
	if offset < len(visible) {
		visible = visible[offset:]
	} else {
		visible = nil
	}
	if limit > 0 && len(visible) > limit {
		visible = visible[:limit]
	}
	return visible, total, nil
}

func (c *scopeController) GetToolCallsBySession(_ string, limit, offset int, scope storage.ToolCallScope) ([]*contracts.ToolCallRecord, int, error) {
	return c.GetToolCalls(limit, offset, scope)
}

func (c *scopeController) GetToolCallByID(id string) (*contracts.ToolCallRecord, error) {
	for _, rec := range scopeToolCalls() {
		if rec.ID == id {
			return rec, nil
		}
	}
	return nil, fmt.Errorf("tool call not found: %s", id)
}

func (c *scopeController) GetServerToolCalls(serverName string, _ int) ([]*contracts.ToolCallRecord, error) {
	out := make([]*contracts.ToolCallRecord, 0, 1)
	for _, rec := range scopeToolCalls() {
		if rec.ServerName == serverName {
			out = append(out, rec)
		}
	}
	return out, nil
}

// GetServerLogs plants the sharpest payload in the finding: upstream stderr
// echoing the argv/env the process was launched with.
func (c *scopeController) GetServerLogs(serverName string, _ int) ([]contracts.LogEntry, error) {
	secret := "alpha-log-line"
	if serverName == "beta" {
		secret = betaLogSecret
	}
	return []contracts.LogEntry{{
		Timestamp: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC),
		Level:     "INFO",
		Message:   "spawned: npx beta-server --token=" + secret,
		Server:    serverName,
	}}, nil
}

// GetToolApproval makes /servers/{id}/tools/{tool}/diff a real 200 for an
// admin. MockServerController returns (nil, nil) there, which the handler
// nil-derefs into a recovered 500 — a control that never proves the route
// serves anything.
func (c *scopeController) GetToolApproval(serverName, toolName string) (*storage.ToolApprovalRecord, error) {
	return &storage.ToolApprovalRecord{
		ServerName:   serverName,
		ToolName:     toolName,
		Status:       storage.ToolApprovalStatusChanged,
		ApprovedHash: "hash-old",
		CurrentHash:  "hash-new",
	}, nil
}

// GetTokenSavings carries the per-server map that IS the inventory (G3).
func (c *scopeController) GetTokenSavings() (*contracts.ServerTokenMetrics, error) {
	return &contracts.ServerTokenMetrics{
		TotalServerToolListSize: 1000,
		AverageQueryResultSize:  100,
		SavedTokens:             900,
		SavedTokensPercentage:   90,
		PerServerToolListSizes:  map[string]int{"alpha": 300, "beta": 700},
	}, nil
}

func (c *scopeController) UsageSnapshot() *internalRuntime.UsageAggregate {
	now := time.Now().UTC()
	return &internalRuntime.UsageAggregate{
		UpdatedAt: now,
		Tools: map[string]*internalRuntime.ToolUsage{
			"alpha:alpha_tool": {Server: "alpha", Tool: "alpha_tool", Calls: 3, RespBytesSum: 30, SizedRespCalls: 3, LastUsed: now},
			"beta:beta_tool":   {Server: "beta", Tool: "beta_tool", Calls: 7, RespBytesSum: 70, SizedRespCalls: 7, LastUsed: now},
		},
		Buckets: map[int64]*internalRuntime.TimeBucket{
			now.Truncate(time.Hour).Unix(): {Start: now.Truncate(time.Hour), Calls: 10, Errors: 1, RespBytesSum: 100},
		},
	}
}

func (c *scopeController) GetRecentSessions(_ int, _ string) ([]*contracts.MCPSession, int, error) {
	return []*contracts.MCPSession{{
		ID:            "session-1",
		ClientName:    "claude-code",
		Status:        "active",
		WorkspaceName: "secret-project",
	}}, 1, nil
}

func (c *scopeController) GetSessionByID(id string) (*contracts.MCPSession, error) {
	return &contracts.MCPSession{ID: id, ClientName: "claude-code", WorkspaceName: "secret-project"}, nil
}

// scopeStatusController is scopeController with a status snapshot that carries
// the runtime's real `message` shape — "Connected to %d/%d servers, retrying…",
// a verbatim count of the WHOLE inventory (G7). The base harness returns only
// `phase`, so this is a separate type rather than a change to it: the existing
// #1166 status test asserts against that shape.
type scopeStatusController struct{ *scopeController }

func (c scopeStatusController) GetStatus() interface{} {
	return map[string]interface{}{
		"phase":         "Ready",
		"message":       scopeStatusMessage,
		"tools_indexed": 10,
	}
}

var _ ServerController = (*scopeController)(nil)
