package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	internalRuntime "github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security/scanner"
)

// Round 10 of the #1166/#1167 cross-review. Every test here runs the PRODUCTION
// chi route table with the real agent-token middleware against the shared
// two-server fixture: `alpha`, which the scoped token may see, and `beta`,
// which it may not.
//
// The headline finding is P1: GET /api/v1/info handed the GLOBAL ADMIN API KEY
// to a scoped agent token inside web_ui_url, which made every other control in
// this branch decorative — a scoped caller read the admin key and stopped being
// scoped.

// scopeRound10Server wires the fixture with a security controller attached, so
// the /security routes reach their handlers rather than 501.
func scopeRound10Server(t *testing.T, sec *mockSecurityController) (*Server, string) {
	t.Helper()
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})
	if sec != nil {
		srv.SetSecurityController(sec)
	}
	return srv, token
}

// TestInfo_ScopedCallerNeverReceivesTheAdminAPIKey is the P1 regression.
//
// Verified live on a running instance before the fix: a read-only token scoped
// to one server received
//
//	"web_ui_url":"http://127.0.0.1:18413/ui/?apikey=devkey-abc123"
//
// and could authenticate as an admin with it.
//
// The oracle is not "the body does not mention the key" — it is that the query
// carrier is absent for the scoped caller while an ADMIN on the same route,
// same fixture, same request still receives it. Without that second half the
// assertion would pass just as happily against a /info that returned nothing at
// all, or against a fixture whose config had no API key to leak.
func TestInfo_ScopedCallerNeverReceivesTheAdminAPIKey(t *testing.T) {
	srv, token := scopeRound10Server(t, nil)

	adminRec := scopeGet(t, srv, "/api/v1/info", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminRec.Code)
	adminURL, _ := scopeDecodeData(t, adminRec)["web_ui_url"].(string)
	require.Equal(t, "http://127.0.0.1:8080/ui/?apikey="+scopeAdminAPIKey, adminURL,
		"positive control: an authenticated admin must still get the copy-paste URL, "+
			"or this test proves nothing about who was denied")

	agentRec := scopeGet(t, srv, "/api/v1/info", token)
	require.Equal(t, http.StatusOK, agentRec.Code,
		"/info stays readable — only the credential in it is withheld")
	agentData := scopeDecodeData(t, agentRec)
	agentURL, ok := agentData["web_ui_url"].(string)
	require.True(t, ok, "web_ui_url must still be present for a scoped caller: %#v", agentData)

	assert.Equal(t, "http://127.0.0.1:8080/ui/", agentURL,
		"the admin API key must not be handed to a scoped agent token")
	assert.NotContains(t, agentRec.Body.String(), scopeAdminAPIKey,
		"the admin API key must not appear anywhere in the /info payload")
	assert.NotContains(t, agentURL, "apikey=",
		"no credential carrier may survive in the URL")
}

// TestSSE_IdentityBearingFrameWithNoServerNameIsDropped is the P2 regression.
//
// eventVisibleToCaller dropped a frame only when an identity key held a
// NON-EMPTY string. Runtime.EmitActivityInternalToolCall omits `target_server`
// entirely when it is empty — every built-in call not routed to one upstream —
// so those frames reached a scoped subscriber whole, carrying `arguments` and
// `response`, i.e. the fleet inventory the door exists to withhold.
func TestSSE_IdentityBearingFrameWithNoServerNameIsDropped(t *testing.T) {
	ctrl := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	ts := httptest.NewServer(srv)
	defer ts.Close()

	adminBody, adminClose := sseSubscribe(t, ts.URL, scopeAdminAPIKey)
	defer adminClose()
	agentBody, agentClose := sseSubscribe(t, ts.URL, token)
	defer agentClose()

	require.Eventually(t, func() bool { return ctrl.subscriberCount() == 2 }, 5*time.Second, 20*time.Millisecond)

	// An internal tool call with NO target_server at all — the exact shape the
	// producer emits for retrieve_tools — carrying a beta secret in the
	// response it would hand the subscriber.
	leak := internalRuntime.Event{
		Type: internalRuntime.EventTypeActivityInternalToolCall,
		Payload: map[string]any{
			"internal_tool_name": "retrieve_tools",
			"status":             "ok",
			"response":           "tools of every server including " + betaEnvSecret,
			"response_truncated": false,
		},
		Timestamp: time.Now(),
	}
	// A servers.changed published AFTER it: never dropped for anyone, so both
	// readers are guaranteed to see a frame and the agent's read cannot hang
	// waiting for one that will never arrive.
	marker := internalRuntime.Event{
		Type:      internalRuntime.EventTypeServersChanged,
		Payload:   map[string]any{"reason": "round10-marker"},
		Timestamp: time.Now(),
	}
	ctrl.publishToAll(leak)
	ctrl.publishToAll(marker)

	adminFirst := sseReadRuntimeEvent(t, adminBody)
	require.Equal(t, string(internalRuntime.EventTypeActivityInternalToolCall), adminFirst.event,
		"positive control: the frame must actually be published, or the agent's silence means nothing")
	assert.Contains(t, adminFirst.data, betaEnvSecret,
		"positive control: the admin must receive the payload that was planted")

	agentFirst := sseReadRuntimeEvent(t, agentBody)
	assert.Equal(t, string(internalRuntime.EventTypeServersChanged), agentFirst.event,
		"the scoped subscriber must skip straight to the marker: an identity-bearing frame "+
			"that names no server is undecidable and must fail closed")
	assert.NotContains(t, agentFirst.data, betaEnvSecret)
}

// TestSSE_IdentityBearingEventTypesCoverEveryNamingProducer keeps the
// production set honest. sseIdentityEventFixtures is the test's own catalogue
// of event types that name a server; identityBearingEventTypes is the one the
// SHIPPED filter reads. If those two ever drift, a type gains an empty-identity
// hole the moment its producer stops populating the field.
func TestSSE_IdentityBearingEventTypesCoverEveryNamingProducer(t *testing.T) {
	fixtures := map[internalRuntime.EventType]bool{}
	for _, e := range sseIdentityEventFixtures("beta") {
		fixtures[e.Type] = true
	}
	require.NotEmpty(t, fixtures, "no fixtures parsed — the guard would pass vacuously")

	for typ := range fixtures {
		_, ok := identityBearingEventTypes[typ]
		assert.True(t, ok, "event type %q names a server in the fixtures but is missing from "+
			"identityBearingEventTypes, so a frame of that type with an empty name reaches a scoped subscriber", typ)
	}
	for typ := range identityBearingEventTypes {
		assert.True(t, fixtures[typ], "event type %q is declared identity-bearing in production but has no "+
			"fixture proving it is scoped end-to-end", typ)
	}
}

// TestSecurityScanners_ConfiguredEnvSecretsAreRedacted is the P3 regression.
// The struct's own comment claimed "secrets redacted in API"; nothing redacted
// them, so the vendor API keys an operator typed into the scanner config dialog
// came back in the clear on both scanner read doors.
func TestSecurityScanners_ConfiguredEnvSecretsAreRedacted(t *testing.T) {
	const vendorKey = "SUPERSECRET_SCANNER_VENDOR_KEY"
	const keyringRef = "${keyring:scanner/other}"
	sec := &mockSecurityController{
		scanners: []*scanner.ScannerPlugin{{
			ID:     "ramparts",
			Name:   "Ramparts",
			Status: scanner.ScannerStatusConfigured,
			ConfiguredEnv: map[string]string{
				"VENDOR_API_KEY": vendorKey,
				"OTHER_API_KEY":  keyringRef,
			},
		}},
	}
	srv, _ := scopeRound10Server(t, sec)

	for _, path := range []string{"/api/v1/security/scanners", "/api/v1/security/scanners/ramparts/status"} {
		rec := scopeGet(t, srv, path, scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, rec.Code, path)
		body := rec.Body.String()

		assert.Contains(t, body, "ramparts",
			"positive control: the scanner fixture must actually be serialized by %s", path)
		assert.Contains(t, body, "VENDOR_API_KEY",
			"positive control: the configured env must still be reported as SET by %s", path)
		assert.NotContains(t, body, vendorKey,
			"%s must not serialize the literal scanner secret", path)
		assert.Contains(t, body, scanner.RedactedEnvValue,
			"%s must report the value as redacted, not omit the key", path)
		assert.Contains(t, body, keyringRef,
			"a ${keyring:} reference is a pointer, not a secret, and the UI reads it to say "+
				"'stored in keyring' — %s must preserve it verbatim", path)
	}

	// Redaction must not corrupt the registry's own copy: a later scan has to
	// launch the scanner with the REAL value.
	assert.Equal(t, vendorKey, sec.scanners[0].ConfiguredEnv["VENDOR_API_KEY"],
		"redaction must copy, never mutate the controller's record")
}

// TestSecurityFleetDoors_DeniedToScopedCaller is the P4 regression for the two
// deployment-wide security documents. Both were reachable at 200 by a scoped
// token: /security/queue enumerates every queued ServerName, /security/overview
// reports fleet-wide scan and finding counts.
func TestSecurityFleetDoors_DeniedToScopedCaller(t *testing.T) {
	sec := &mockSecurityController{
		overview: &scanner.SecurityOverview{TotalScans: 4, ServersScanned: 2, ScannersEnabled: 1},
		queueProgress: &scanner.QueueProgress{
			BatchID: "batch-1",
			Status:  "running",
			Total:   2,
			Items: []scanner.QueueItem{
				{ServerName: "alpha"},
				{ServerName: "beta"},
			},
		},
	}
	srv, token := scopeRound10Server(t, sec)

	for _, path := range []string{"/api/v1/security/queue", "/api/v1/security/overview"} {
		adminRec := scopeGet(t, srv, path, scopeAdminAPIKey)
		require.Equal(t, http.StatusOK, adminRec.Code,
			"positive control: %s must be a live, reachable route for an admin", path)

		agentRec := scopeGet(t, srv, path, token)
		assert.Equal(t, http.StatusForbidden, agentRec.Code, "%s must be denied to a scoped caller", path)
		assert.Contains(t, agentRec.Body.String(), securityFleetDenialMessage, path)
		assert.NotContains(t, agentRec.Body.String(), "beta",
			"%s must not name a server the caller may not enumerate", path)
	}

	// The queue really did carry the inventory an admin can see, so the denial
	// above withheld something real.
	assert.Contains(t, scopeGet(t, srv, "/api/v1/security/queue", scopeAdminAPIKey).Body.String(), "beta")
}

// TestSecurityScanReportByJobID_ScopedTo404 is the P4 regression for the
// per-server report reached by JOB id, which walked around the /servers/{id}
// subtree gate.
//
// It is SCOPED, not denied: the document is per-server, so a caller entitled to
// the server keeps it. The denial for an unentitled server is the same 404 the
// unknown-job branch already returns, so "not yours" and "no such job" are
// indistinguishable.
func TestSecurityScanReportByJobID_ScopedTo404(t *testing.T) {
	betaReport := &scanner.AggregatedReport{JobID: "scan-beta-1", ServerName: "beta", RiskScore: 91}
	sec := &mockSecurityController{report: betaReport}
	srv, token := scopeRound10Server(t, sec)

	adminRec := scopeGet(t, srv, "/api/v1/security/scans/scan-beta-1/report", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminRec.Code,
		"positive control: the report fixture must be reachable for an admin")
	require.Contains(t, adminRec.Body.String(), "beta")

	agentRec := scopeGet(t, srv, "/api/v1/security/scans/scan-beta-1/report", token)
	assert.Equal(t, http.StatusNotFound, agentRec.Code,
		"a scoped caller must not read another server's scan report by job id")

	// Status parity with a job that genuinely does not exist. The mock returns
	// an error for any id once `report` is cleared, which is the absent case.
	sec.report = nil
	absentRec := scopeGet(t, srv, "/api/v1/security/scans/scan-beta-1/report", token)
	assert.Equal(t, absentRec.Code, agentRec.Code,
		"'not yours' and 'no such job' must be the same status")

	// And the entitled server still resolves, so the gate scopes rather than
	// blanks the route.
	sec.report = &scanner.AggregatedReport{JobID: "scan-alpha-1", ServerName: "alpha"}
	alphaRec := scopeGet(t, srv, "/api/v1/security/scans/scan-alpha-1/report", token)
	assert.Equal(t, http.StatusOK, alphaRec.Code,
		"a scoped caller must still read the report for a server it may see")
}

// TestTelemetryPayload_DeniedToScopedCaller is the P4 regression for the
// heartbeat preview: a fleet-wide census (server_count, connected_server_count,
// tool_count, server_docker_isolated_count) — the exact count oracle this
// branch removed from /status.
//
// No telemetry provider is wired, so the admin's 503 is the positive control:
// it proves the request passed the gate and reached the handler body, i.e. the
// gate is the only difference between the two callers.
func TestTelemetryPayload_DeniedToScopedCaller(t *testing.T) {
	srv, token := scopeRound10Server(t, nil)

	adminRec := scopeGet(t, srv, "/api/v1/telemetry/payload", scopeAdminAPIKey)
	require.Equal(t, http.StatusServiceUnavailable, adminRec.Code,
		"positive control: an admin must reach the handler body (no provider wired here)")

	agentRec := scopeGet(t, srv, "/api/v1/telemetry/payload", token)
	assert.Equal(t, http.StatusForbidden, agentRec.Code)
	assert.Contains(t, agentRec.Body.String(), telemetryPayloadDenialMessage)
}

// TestOnboardingState_DeniedToScopedCaller is the P4 regression for the wizard
// document: configured_server_count over the whole inventory plus
// connected_client_ids, the operator's MCP-client inventory. The branch already
// 403s /sessions for that same class.
func TestOnboardingState_DeniedToScopedCaller(t *testing.T) {
	srv, token := scopeRound10Server(t, nil)

	adminRec := scopeGet(t, srv, "/api/v1/onboarding/state", scopeAdminAPIKey)
	require.Equal(t, http.StatusOK, adminRec.Code)
	adminData := scopeDecodeData(t, adminRec)
	require.EqualValues(t, 2, adminData["configured_server_count"],
		"positive control: the document must really report the whole inventory for an admin")

	agentRec := scopeGet(t, srv, "/api/v1/onboarding/state", token)
	assert.Equal(t, http.StatusForbidden, agentRec.Code)
	assert.Contains(t, agentRec.Body.String(), onboardingDenialMessage)

	// The POST echoes the same recomputed document, so gating the GET alone
	// would have left the disclosure one request away.
	markReq := httptest.NewRequest(http.MethodPost, "/api/v1/onboarding/mark", strings.NewReader(`{"engaged":true}`))
	markReq.Header.Set("X-API-Key", token)
	markRec := httptest.NewRecorder()
	srv.ServeHTTP(markRec, markReq)
	assert.Equal(t, http.StatusForbidden, markRec.Code, "/onboarding/mark must be denied too")
	assert.NotContains(t, markRec.Body.String(), "configured_server_count")
}

// TestSecretsInventory_DeniedToScopedCaller is the P5 regression. Values are
// masked on these routes, so this is a credential INVENTORY rather than a
// disclosure — but it names the secrets of servers the caller cannot see, and
// it is a strictly narrower view of the document /api/v1/config already denies.
//
// No secret resolver is wired, so the admin's 500 is the positive control that
// the request passed the gate and reached the handler body.
func TestSecretsInventory_DeniedToScopedCaller(t *testing.T) {
	srv, token := scopeRound10Server(t, nil)

	for _, path := range []string{"/api/v1/secrets/refs", "/api/v1/secrets/config"} {
		adminRec := scopeGet(t, srv, path, scopeAdminAPIKey)
		require.Equal(t, http.StatusInternalServerError, adminRec.Code,
			"positive control: an admin must reach the handler body of %s", path)

		agentRec := scopeGet(t, srv, path, token)
		assert.Equal(t, http.StatusForbidden, agentRec.Code, "%s must be denied to a scoped caller", path)
		assert.Contains(t, agentRec.Body.String(), secretsInventoryDenialMessage, path)
	}
}

// scopeErrInventoryController fails every inventory read, which is what a
// transient storage error looks like to the subtree gate.
type scopeErrInventoryController struct {
	*scopeController
	fail bool
}

func (c *scopeErrInventoryController) GetAllServers() ([]map[string]interface{}, error) {
	if c.fail {
		return nil, assertInventoryReadFailure
	}
	return c.scopeController.GetAllServers()
}

var assertInventoryReadFailure = errInventoryRead{}

type errInventoryRead struct{}

func (errInventoryRead) Error() string { return "bbolt: transient read failure" }

// TestScopedSubtree_TransientInventoryErrorIs503NotNotFound is the P8
// regression. serverExists folded every controller error into "does not
// exist", so a transient inventory-read failure told an ENTITLED caller that
// its own server had been deleted, on a hot path (/servers/{id}/logs).
func TestScopedSubtree_TransientInventoryErrorIs503NotNotFound(t *testing.T) {
	base := &scopeController{cfg: scopeFixtureConfig(false), servers: scopeFixtureServers(), withManagement: true}
	ctrl := &scopeErrInventoryController{scopeController: base}
	srv, token := scopedAgentServer(t, ctrl, []string{"alpha"})

	// Positive control: with the inventory readable, the entitled server
	// resolves and the unentitled one 404s.
	require.NotEqual(t, http.StatusNotFound, scopeGet(t, srv, "/api/v1/servers/alpha/logs", token).Code,
		"positive control: alpha must resolve while the inventory is readable")
	require.Equal(t, http.StatusNotFound, scopeGet(t, srv, "/api/v1/servers/beta/logs", token).Code,
		"positive control: beta must stay hidden")

	ctrl.fail = true
	rec := scopeGet(t, srv, "/api/v1/servers/alpha/logs", token)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
		"'could not determine' must not be reported as 'does not exist'")

	// An unentitled name is still 404 even when the inventory cannot be read:
	// entitlement is decided before the inventory is consulted, so the 503 can
	// never become an existence oracle.
	assert.Equal(t, http.StatusNotFound, scopeGet(t, srv, "/api/v1/servers/beta/logs", token).Code,
		"an unentitled name must not learn from the read failure")
}

// TestUsageCacheKey_IsInjective is the P6 regression. The key joined the
// allowed-server names with a bare comma and the fields with a bare pipe,
// neither escaped, and config validation rejects only a COLON in a server name.
// A collision serves one tenant's cached response to another.
func TestUsageCacheKey_IsInjective(t *testing.T) {
	base := usageParams{window: "24h", top: usageDefaultTop, sort: usageDefaultSort}

	oneCommaName := base
	oneCommaName.scoped = true
	oneCommaName.allowed = []string{"a,b"}

	twoNames := base
	twoNames.scoped = true
	twoNames.allowed = []string{"a", "b"}

	assert.NotEqual(t, twoNames.cacheKey(), oneCommaName.cacheKey(),
		`a token scoped to the single server "a,b" must not share a cache entry with one scoped to ["a","b"]`)

	pipeInServer := base
	pipeInServer.server = "x|y"

	splitAcrossFields := base
	splitAcrossFields.server = "x"
	splitAcrossFields.tool = "y"

	assert.NotEqual(t, pipeInServer.cacheKey(), splitAcrossFields.cacheKey(),
		"an unescaped separator inside a filter value must not collide with the next field")

	// Positive control: identical params still share an entry, or the cache
	// would simply never hit and the test above would be trivially satisfiable.
	assert.Equal(t, twoNames.cacheKey(), twoNames.cacheKey())
	same := base
	same.scoped = true
	same.allowed = []string{"b", "a"}
	assert.Equal(t, twoNames.cacheKey(), same.cacheKey(),
		"scope order must not fragment the cache")
}

// TestUsageResponse_ScopedHeadlineAgreesWithItsOwnRows is the P7 regression.
// The scoped early return skipped the timeline loop, which is also where
// total_calls / total_errors are accumulated, so the headline read 0 beside
// per-tool rows that plainly summed to a non-zero number.
func TestUsageResponse_ScopedHeadlineAgreesWithItsOwnRows(t *testing.T) {
	now := time.Now()
	snap := &internalRuntime.UsageAggregate{
		Tools: map[string]*internalRuntime.ToolUsage{
			"alpha:t1": {Server: "alpha", Tool: "t1", Calls: 7, Errors: 2, LastUsed: now},
			"beta:t9":  {Server: "beta", Tool: "t9", Calls: 99, Errors: 40, LastUsed: now},
		},
		UpdatedAt: now,
	}

	p := usageParams{window: "24h", top: usageDefaultTop, sort: usageDefaultSort, scoped: true, allowed: []string{"alpha"}}
	resp := buildUsageResponse(snap, nil, p, now)

	require.Len(t, resp.Tools, 1, "positive control: the scoped caller must see exactly its own row")
	require.EqualValues(t, 7, resp.Tools[0].Calls)

	var rowCalls, rowErrors int64
	for _, row := range resp.Tools {
		rowCalls += row.Calls
		rowErrors += row.Errors
	}
	assert.Equal(t, rowCalls, resp.TotalCalls,
		"the headline must agree with the rows the same response carries")
	assert.Equal(t, rowErrors, resp.TotalErrors)
	assert.EqualValues(t, 7, resp.TotalCalls, "beta's traffic must not be folded into the headline")
	assert.Empty(t, resp.Timeline, "the global timeline is still withheld from a scoped caller")
}

// sseRuntimeFrame is one `event:`/`data:` pair read off the stream.
type sseRuntimeFrame struct {
	event string
	data  string
}

// sseReadRuntimeEvent reads forward to the next RUNTIME event, skipping the
// status/ping frames the handler emits on its own schedule.
func sseReadRuntimeEvent(t *testing.T, body interface {
	ReadString(delim byte) (string, error)
}) sseRuntimeFrame {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var frame sseRuntimeFrame
	for time.Now().Before(deadline) {
		line, err := body.ReadString('\n')
		require.NoError(t, err)
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			frame.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			frame.data = strings.TrimPrefix(line, "data: ")
			if frame.event != "" && frame.event != "status" && frame.event != "ping" {
				return frame
			}
			frame = sseRuntimeFrame{}
		}
	}
	t.Fatal("timed out waiting for a runtime SSE frame")
	return frame
}

// scopeUnusedRefs keeps the imports honest for helpers this file shares with
// the rest of the group.
var _ = json.Marshal
var _ = contracts.UsageToolStat{}
