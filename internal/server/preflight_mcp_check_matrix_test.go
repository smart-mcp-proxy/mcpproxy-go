package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// Spec 099 T007 — the in-band half of the committed sabotage matrix.
//
// Every mcp-check and mcp-plain row is DRIVEN here, and the last assertion in
// TestPreflightMatrixMCPSurfaces is the one that keeps that true: a row added
// to the JSON without a driver fails, exactly as a reason code without a row
// fails the reflection gate. Between the two, "the matrix says X" and "the
// surface does X" cannot drift apart.
//
// Cells that need a live connection-state snapshot (the three connection
// reasons) or readable upstream annotations (policy_filtered — Bleve stores
// identity and text only, so an index round-trip always loses annotations)
// cannot be induced by fixture state alone. They are driven through the SAME
// call path as every other row — the real describe_tool handler, the real
// scope/tier glue, the real projection — with ONLY the snapshot injected, at
// the one seam production reads it from. Injecting the snapshot rather than the
// EvalContext is what keeps the glue wiring (scope composition, tier pinning,
// index-annotation enrichment, the activity write) inside the test instead of
// beside it.

// injectPreflightSnapshot makes the fixture's proxy see a connection-state
// snapshot: every configured server in the given state, and the given
// annotations on gh:create_issue (the tool the annotation cells check).
func injectPreflightSnapshot(t *testing.T, f *describeCheckFixture, state preflight.ServerRuntimeState, annotations *config.ToolAnnotations) {
	t.Helper()
	f.proxy.preflightStateSource = func() (preflight.StateReader, func(serverName, toolName string) *config.ToolAnnotations, error) {
		return stubState{state: state}, func(serverName, toolName string) *config.ToolAnnotations {
			if serverName == "gh" && toolName == "create_issue" {
				return annotations
			}
			return nil
		}, nil
	}
	t.Cleanup(func() { f.proxy.preflightStateSource = nil })
}

// assertMatrixCell checks one per-tool result against its committed row.
func assertMatrixCell(t *testing.T, scenario sabotageScenario, result describeCheckResult, verdict string) {
	t.Helper()
	expect := scenario.Expect
	assert.Equalf(t, expect.Status, result.Status, "[%s] status", scenario.Scenario)
	if expect.Status == preflight.StatusReady {
		assert.Emptyf(t, result.Reason, "[%s] a ready result carries no reason", scenario.Scenario)
		assert.Nilf(t, result.Retryable, "[%s] a ready result carries no retryable flag", scenario.Scenario)
		assert.Emptyf(t, result.Action, "[%s] a ready result carries no action", scenario.Scenario)
	} else {
		assert.Equalf(t, expect.Reason, result.Reason, "[%s] reason", scenario.Scenario)
		if assert.NotNilf(t, result.Retryable, "[%s] a failure result must carry retryable", scenario.Scenario) {
			require.NotNil(t, expect.Retryable)
			assert.Equalf(t, *expect.Retryable, *result.Retryable, "[%s] retryable", scenario.Scenario)
		}
		assert.Equalf(t, expect.Action, result.Action, "[%s] action", scenario.Scenario)
		assert.NotEmptyf(t, result.Remediation, "[%s] a failure result must carry a remediation", scenario.Scenario)
	}
	assert.Equalf(t, expect.Verdict, verdict, "[%s] set verdict", scenario.Scenario)
	assert.Equalf(t, expect.ExitCode, preflight.ExitCode(verdict), "[%s] CLI exit code", scenario.Scenario)
	// The in-band payload never carries a hash, whatever the cell (FR-004).
	assert.Equalf(t, preflight.TierAgentToken, scenario.Tier, "[%s] tier", scenario.Scenario)
}

// scopedAgentContext is the session every in-band row is observed under: an
// agent token scoped to the fixture's own servers.
func scopedAgentContext() context.Context {
	return auth.WithAuthContext(context.Background(), &auth.AuthContext{
		Type:           auth.AuthTypeAgent,
		AgentName:      "matrix-bot",
		AllowedServers: []string{"gh", "locked", "off", "denied"},
		Permissions:    []string{auth.PermRead},
	})
}

func TestPreflightMatrixMCPSurfaces(t *testing.T) {
	scenarios := loadSabotageMatrix(t)
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "secret", "exfiltrate")

	// One id per fixture-inducible cell.
	byID := map[string]string{
		"mcp_check_all_ready":             "gh:create_issue",
		"mcp_check_quarantine":            "locked:lingering",
		"mcp_check_server_disabled":       "off:sleeping",
		"mcp_check_tool_pending_approval": "gh:pending_tool",
		"mcp_check_tool_changed":          "gh:changed_tool",
		"mcp_check_tool_blocked_by_user":  "gh:blocked_tool",
		"mcp_check_tool_denied_by_config": "denied:forbidden",
		"mcp_check_unknown_tool_id":       "gh:no_such_tool",
		"mcp_check_malformed_id":          "not-an-id",
		"mcp_check_unknown_server":        "nosuch:tool",
		"mcp_check_out_of_scope":          "secret:exfiltrate",
	}
	// Cells whose state cannot be induced without a live snapshot or readable
	// annotations: same handler, same glue, only the snapshot injected.
	injected := map[string]func(t *testing.T) (describeCheckResult, string){
		"mcp_check_oauth_required": func(t *testing.T) (describeCheckResult, string) {
			return checkWithSnapshot(t, fixture, preflight.RuntimeStatePendingAuth, nil, nil)
		},
		"mcp_check_server_unhealthy": func(t *testing.T) (describeCheckResult, string) {
			return checkWithSnapshot(t, fixture, preflight.RuntimeStateError, nil, nil)
		},
		"mcp_check_server_initializing": func(t *testing.T) (describeCheckResult, string) {
			return checkWithSnapshot(t, fixture, preflight.RuntimeStateConnecting, nil, nil)
		},
	}
	for filterKey, annotations := range map[string]*config.ToolAnnotations{
		"read_only_only":      {ReadOnlyHint: boolPtr(false)},
		"exclude_destructive": {DestructiveHint: boolPtr(true)},
		"exclude_open_world":  {OpenWorldHint: boolPtr(true)},
	} {
		filterKey, annotations := filterKey, annotations
		injected["mcp_check_policy_filtered_"+filterKey] = func(t *testing.T) (describeCheckResult, string) {
			return checkWithSnapshot(t, fixture, preflight.RuntimeStateReady, annotations,
				map[string]interface{}{"filters": map[string]interface{}{filterKey: true}})
		}
	}

	driven := make(map[string]bool)

	// --- fixture-inducible verdict cells ---
	for scenarioName, id := range byID {
		scenario, ok := scenarios[scenarioName]
		require.Truef(t, ok, "scenario %q is missing from %s", scenarioName, preflightMatrixPath)
		driven[scenarioName] = true
		t.Run(scenarioName, func(t *testing.T) {
			payload, raw := fixture.check(t, scopedAgentContext(), []interface{}{id}, nil)
			require.Len(t, payload.Results, 1)
			assertMatrixCell(t, scenario, payload.Results[0], payload.Verdict)
			assert.NotContains(t, raw, "\"hash\"", "no hash is ever returned in band")
		})
	}

	// --- snapshot-injected verdict cells ---
	for scenarioName, drive := range injected {
		scenario, ok := scenarios[scenarioName]
		require.Truef(t, ok, "scenario %q is missing from %s", scenarioName, preflightMatrixPath)
		driven[scenarioName] = true
		t.Run(scenarioName, func(t *testing.T) {
			result, verdict := drive(t)
			assertMatrixCell(t, scenario, result, verdict)
		})
	}

	// --- missing_annotation cells (inducible: an indexed tool with no
	// annotations is exactly the missing-annotation case) ---
	for _, filterKey := range describeCheckFilterKeys {
		scenarioName := "mcp_check_missing_annotation_" + filterKey
		scenario, ok := scenarios[scenarioName]
		require.Truef(t, ok, "scenario %q is missing from %s", scenarioName, preflightMatrixPath)
		driven[scenarioName] = true
		t.Run(scenarioName, func(t *testing.T) {
			payload, _ := fixture.check(t, scopedAgentContext(), []interface{}{"gh:create_issue"},
				map[string]interface{}{"filters": map[string]interface{}{filterKey: true}})
			require.Len(t, payload.Results, 1)
			assertMatrixCell(t, scenario, payload.Results[0], payload.Verdict)
		})
	}

	// --- cap boundary ---
	driven["mcp_check_cap_boundary_50"] = true
	t.Run("mcp_check_cap_boundary_50", func(t *testing.T) {
		scenario := scenarios["mcp_check_cap_boundary_50"]
		ids := make([]interface{}, 0, maxDescribeCheckIDs)
		for i := 0; i < maxDescribeCheckIDs; i++ {
			name := fmt.Sprintf("bulk_%02d", i)
			fixture.indexTool(t, "gh", name)
			ids = append(ids, "gh:"+name)
		}
		payload, _ := fixture.check(t, scopedAgentContext(), ids, nil)
		require.Len(t, payload.Results, maxDescribeCheckIDs, "all 50 ids are evaluated in one call")
		for _, result := range payload.Results {
			assertMatrixCell(t, scenario, result, payload.Verdict)
		}
	})

	// --- request-error rows ---
	requestErrors := map[string]map[string]interface{}{
		"mcp_check_cap_exceeded_51":        {"tool_ids": overCapIDs(), "check": true},
		"mcp_check_filters_without_check":  {"tool_ids": []interface{}{"gh:create_issue"}, "filters": map[string]interface{}{"read_only_only": true}},
		"mcp_check_expect_hashes_reserved": {"tool_ids": []interface{}{"gh:create_issue"}, "check": true, "expect_hashes": map[string]interface{}{"gh:create_issue": "sha256/v2:abc123"}},
	}
	for scenarioName, args := range requestErrors {
		scenario, ok := scenarios[scenarioName]
		require.Truef(t, ok, "scenario %q is missing from %s", scenarioName, preflightMatrixPath)
		require.NotEmptyf(t, scenario.Expect.RequestError, "scenario %q must carry request_error", scenarioName)
		driven[scenarioName] = true
		t.Run(scenarioName, func(t *testing.T) {
			fixture.records = nil
			result := fixture.callCheck(t, scopedAgentContext(), args)
			require.True(t, result.IsError, "[%s] the request must be rejected", scenarioName)
			assert.Contains(t, resultText(t, result), scenario.Expect.RequestError)
			assert.Empty(t, fixture.records, "[%s] a rejected request runs no check and records nothing", scenarioName)
		})
	}

	// --- the plain surface row (FR-011) ---
	driven["mcp_plain_out_of_scope"] = true
	t.Run("mcp_plain_out_of_scope", func(t *testing.T) {
		scenario := scenarios["mcp_plain_out_of_scope"]
		require.NotEmpty(t, scenario.Expect.PlainError)

		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]interface{}{"tool_ids": []interface{}{"secret:exfiltrate"}}
		result, err := fixture.proxy.handleDescribeTool(scopedAgentContext(), req)
		require.NoError(t, err)
		require.False(t, result.IsError)

		var plain describeToolResponse
		require.NoError(t, json.Unmarshal([]byte(resultText(t, result)), &plain))
		assert.Empty(t, plain.Definitions, "an out-of-scope id never resolves to a definition")
		require.Len(t, plain.Errors, 1)
		assert.Equal(t, scenario.Expect.PlainError, plain.Errors[0]["error"])
		assert.Equal(t, describeNotFoundRemediation, plain.Errors[0]["remediation"],
			"the remediation was already the shared not-found text; only the code moved")
	})

	// The gate that keeps this file honest: every in-band row is driven above.
	var undriven []string
	for name, scenario := range scenarios {
		if scenario.Surface != surfaceMCPCheck && scenario.Surface != surfaceMCPPlain {
			continue
		}
		if !driven[name] {
			undriven = append(undriven, name)
		}
	}
	sort.Strings(undriven)
	assert.Empty(t, undriven,
		"every mcp-check/mcp-plain row must be driven by this test — a committed expectation nothing asserts is a claim, not a check")
}

func overCapIDs() []interface{} {
	ids := make([]interface{}, 0, maxDescribeCheckIDs+1)
	for i := 0; i <= maxDescribeCheckIDs; i++ {
		ids = append(ids, fmt.Sprintf("gh:tool_%02d", i))
	}
	return ids
}

// checkWithSnapshot runs one real check-mode call for gh:create_issue with a
// connection-state snapshot injected, and returns its single result plus the set
// verdict the handler computed.
func checkWithSnapshot(
	t *testing.T,
	fixture *describeCheckFixture,
	state preflight.ServerRuntimeState,
	annotations *config.ToolAnnotations,
	extra map[string]interface{},
) (describeCheckResult, string) {
	t.Helper()
	injectPreflightSnapshot(t, fixture, state, annotations)
	payload, raw := fixture.check(t, scopedAgentContext(), []interface{}{"gh:create_issue"}, extra)
	require.Len(t, payload.Results, 1)
	assert.NotContains(t, raw, "\"hash\"", "no hash is ever returned in band")
	return payload.Results[0], payload.Verdict
}

// TestPreflightMatrixNeverIndexedWhileConnecting is the FR-016 erratum: on a
// server that is not Ready, the connection-state verdict wins over not_found,
// because existence is unknowable until the server has listed its tools. The
// matrix used to claim the opposite in the mid_indexing row's note.
func TestPreflightMatrixNeverIndexedWhileConnecting(t *testing.T) {
	scenarios := loadSabotageMatrix(t)
	scenario, ok := scenarios["never_indexed_while_connecting"]
	require.True(t, ok, "the never_indexed_while_connecting cell must exist in the matrix")
	require.Equal(t, surfaceStateInjected, scenario.Surface)

	assert.NotContains(t, scenarios["mid_indexing"].Sabotage, "existence outranks connection state",
		"the corrected note must not re-assert the claim the evaluator contradicts")

	// The server is configured and enabled but has NEVER been indexed, and it
	// is connecting.
	results, err := preflight.Evaluate(context.Background(), preflight.EvalContext{
		Index:     stubIndex{tools: map[string][]string{"gh": {}}},
		Approvals: stubApprovals{},
		State:     stubState{state: preflight.RuntimeStateConnecting},
		Policy:    stubPolicy{enabled: map[string]bool{"gh": true}},
		Tier:      preflight.TierOperator,
	}, []preflight.ToolRef{{ID: "gh:never_seen"}})
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, scenario.Expect.Reason, results[0].Reason,
		"a never-indexed tool on a connecting server reports the connection verdict, not not_found")
	assert.Equal(t, preflight.ReasonServerInitializing, results[0].Reason)
	verdict := preflight.VerdictForResults(results)
	assert.Equal(t, scenario.Expect.Verdict, verdict)
	assert.Equal(t, scenario.Expect.ExitCode, preflight.ExitCode(verdict))
}

// --- FR-017: in-band vs REST parity -----------------------------------------

// preflightParityComparedFields are the per-result fields the parity loop
// asserts equal between the two surfaces, by their wire names.
//
// The per-surface exclusion maps below name the ONLY fields allowed to differ, each with
// the reason it does. FR-017 requires the exclusions to be named rather than
// expressed as "whatever the loop happens not to touch": an omission is
// invisible, a name is reviewable. TestPreflightParityExclusionsAreNamed turns
// that into a gate — a field added to either payload belongs on one list or the
// other, or the build of the parity claim fails.
var (
	preflightParityComparedFields = []string{"id", "status", "reason", "retryable", "action", "detail", "remediation", "did_you_mean"}

	// Exclusions are scoped PER SURFACE: a reason that justifies excluding a
	// field from one payload says nothing about the other. A globally excluded
	// "hash" would let a future hash field on the MCP payload silently escape
	// comparison even though FR-004 forbids it there — the exact narrowing
	// FR-017's "by name" rule exists to prevent.
	preflightParityExcludedMCPFields = map[string]string{
		"checked_at": "a timestamp, not a verdict: each surface stamps its own instant (FR-004)",
		"request_id": "in-band only: the correlation id an agent hands a human (FR-004)",
		"verdict":    "compared once at the set level, not per result",
		"results":    "the in-band container of the compared results",
	}
	preflightParityExcludedRESTFields = map[string]string{
		"checked_at": "a timestamp, not a verdict: each surface stamps its own instant (FR-004)",
		"hash":       "REST may disclose a pin at the operator tier; in band, never, at any tier (FR-004)",
		"waited_ms":  "REST only: check mode takes no wait budget (non-goal)",
		"verdict":    "compared once at the set level, not per result",
		"tools":      "the REST container of the compared results",
	}
)

// Every field on either payload is either compared or excluded BY NAME. This is
// the gate FR-017 asks for: adding a field to one surface's result without
// deciding whether it must agree with the other's fails here, instead of
// silently leaving the parity claim narrower than it reads.
func TestPreflightParityExclusionsAreNamed(t *testing.T) {
	compared := make(map[string]bool, len(preflightParityComparedFields))
	for _, name := range preflightParityComparedFields {
		compared[name] = true
		assert.NotContainsf(t, preflightParityExcludedMCPFields, name,
			"%q cannot be both compared and MCP-excluded", name)
		assert.NotContainsf(t, preflightParityExcludedRESTFields, name,
			"%q cannot be both compared and REST-excluded", name)
	}

	surfaces := []struct {
		payloads []any
		excluded map[string]string
		surface  string
	}{
		{[]any{describeCheckResult{}, describeCheckPayload{}}, preflightParityExcludedMCPFields, "mcp-check"},
		{[]any{contracts.PreflightToolResult{}, contracts.PreflightResponse{}}, preflightParityExcludedRESTFields, "rest"},
	}
	for _, sf := range surfaces {
		for _, payload := range sf.payloads {
			typ := reflect.TypeOf(payload)
			for i := 0; i < typ.NumField(); i++ {
				name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
				require.NotEmptyf(t, name, "%s.%s has no json tag", typ.Name(), typ.Field(i).Name)
				if compared[name] {
					continue
				}
				_, excluded := sf.excluded[name]
				assert.Truef(t, excluded,
					"%s.%s (%q) is neither compared by the parity test nor excluded from it by name on the %s surface",
					typ.Name(), typ.Field(i).Name, name, sf.surface)
			}
		}
		// The reverse direction: an exclusion that names no real field on its
		// own surface is stale and must be pruned, not carried.
		for name := range sf.excluded {
			found := false
			for _, payload := range sf.payloads {
				typ := reflect.TypeOf(payload)
				for i := 0; i < typ.NumField(); i++ {
					if tag, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ","); tag == name {
						found = true
					}
				}
			}
			assert.Truef(t, found, "excluded field %q names no field on the %s surface", name, sf.surface)
		}
	}
}

// For identical ids and identical proxy state, the in-band surface and the REST
// surface AT THE SAME TIER name the same thing. The two payloads deliberately
// differ only on the fields named in the per-surface exclusion maps — excluded by
// name, not by a loose matcher — and anything else that differs is a defect in
// the glue.
func TestPreflightInBandRESTParityAtAgentTokenTier(t *testing.T) {
	fixture := newDescribeCheckFixture(t, nil)
	seedCheckFixture(t, fixture)
	fixture.addServer(t, &config.ServerConfig{Name: "secret", Enabled: true, Protocol: "http"})
	fixture.indexTool(t, "secret", "exfiltrate")

	ids := []string{
		"gh:create_issue",   // ready
		"locked:lingering",  // server_quarantined
		"off:sleeping",      // server_disabled
		"gh:pending_tool",   // tool_pending_approval
		"gh:changed_tool",   // tool_changed
		"gh:blocked_tool",   // tool_blocked_by_user
		"denied:forbidden",  // tool_denied_by_config
		"gh:no_such_tool",   // not_found
		"nosuch:tool",       // server_not_configured ⇒ not_found at this tier
		"secret:exfiltrate", // out of scope ⇒ not_found at this tier
		"not-an-id",         // malformed ⇒ not_found
	}

	inBandIDs := make([]interface{}, 0, len(ids))
	restRefs := make([]preflight.ToolRef, 0, len(ids))
	for _, id := range ids {
		inBandIDs = append(inBandIDs, id)
		restRefs = append(restRefs, preflight.ToolRef{ID: id})
	}

	// The same session scope, expressed the way each surface expresses it: the
	// in-band call carries an agent token on the context, the REST call carries
	// the token's allowed_servers in its params.
	inBand, _ := fixture.check(t, scopedAgentContext(), inBandIDs, nil)
	rest, err := fixture.proxy.RunPreflight(context.Background(), preflight.Params{
		Tools:        restRefs,
		Tier:         preflight.TierAgentToken,
		TokenServers: []string{"gh", "locked", "off", "denied"},
	})
	require.NoError(t, err)
	require.Len(t, rest.Results, len(ids))

	assert.Equal(t, rest.Verdict, inBand.Verdict, "the set verdict is the same aggregate")

	for _, id := range ids {
		t.Run(id, func(t *testing.T) {
			mcpResult := checkResultByID(t, inBand, id)
			restResult := resultByID(t, rest, id)

			// The compared set, field by field and by wire name. It is the
			// same list TestPreflightParityExclusionsAreNamed measures the two
			// payloads against, so "compared" cannot quietly shrink.
			assert.Equal(t, restResult.ID, mcpResult.ID, "id")
			assert.Equal(t, restResult.Status, mcpResult.Status, "status")
			assert.Equal(t, restResult.Reason, mcpResult.Reason, "reason")
			assert.Equal(t, restResult.Action, mcpResult.Action, "action")
			if restResult.Status == preflight.StatusReady {
				assert.Nil(t, mcpResult.Retryable)
			} else {
				require.NotNil(t, mcpResult.Retryable)
				assert.Equal(t, restResult.Retryable, *mcpResult.Retryable, "retryable")
			}
			// Detail and remediation are not in the FR-017 tuple, but they come
			// from the same evaluator result, so a divergence would mean the
			// projection rewrote them. did_you_mean is compared for the same
			// reason: it is computed over the caller-visible scope, which the
			// two surfaces must resolve identically.
			assert.Equal(t, restResult.Detail, mcpResult.Detail, "detail")
			assert.Equal(t, restResult.Remediation, mcpResult.Remediation, "remediation")
			assert.Equal(t, restResult.DidYouMean, mcpResult.DidYouMean, "did_you_mean")

			// hash is one of the named exclusions and the only one with a
			// disclosure consequence, so it gets an assertion of its own: the
			// in-band payload has no such field at all, and at this tier the
			// REST payload must not fill one either.
			assert.Empty(t, restResult.Hash, "the agent-token tier discloses no hash on either surface")
		})
	}
}
