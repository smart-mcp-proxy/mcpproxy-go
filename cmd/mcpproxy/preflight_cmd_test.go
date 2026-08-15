package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/cli/output"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/preflight"
)

// --- T017: typed exit-code error + central classification -------------------

func TestPreflightVerdictError_MapsToSpecExitCodes(t *testing.T) {
	tests := []struct {
		verdict string
		want    int
	}{
		{preflight.VerdictDegradedRetryable, ExitCodePreflightDegradedRetryable},
		{preflight.VerdictBlocked, ExitCodePreflightBlocked},
		{preflight.VerdictUnknownIDs, ExitCodePreflightUnknownIDs},
	}
	for _, tc := range tests {
		t.Run(tc.verdict, func(t *testing.T) {
			err := newPreflightVerdictError(tc.verdict, "1 of 1 tools unavailable")
			require.Error(t, err)
			assert.Equal(t, tc.want, classifyError(err), "the CENTRAL classifier owns the exit code")
			assert.Contains(t, err.Error(), tc.verdict)
		})
	}

	assert.Equal(t, 10, ExitCodePreflightDegradedRetryable)
	assert.Equal(t, 11, ExitCodePreflightBlocked)
	assert.Equal(t, 12, ExitCodePreflightUnknownIDs)
}

func TestPreflightVerdictError_ReadyIsNotAnError(t *testing.T) {
	assert.NoError(t, newPreflightVerdictError(preflight.VerdictReady, ""))
	assert.Equal(t, ExitCodeSuccess, classifyError(nil))
}

// The verdict error must survive wrapping — a caller that adds context must not
// silently downgrade the exit code to the generic 1.
func TestPreflightVerdictError_SurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("running scheduled job: %w", newPreflightVerdictError(preflight.VerdictBlocked, ""))
	assert.Equal(t, ExitCodePreflightBlocked, classifyError(wrapped))
}

// Remediation text is full of words the string-matching heuristics look for
// ("configure", "invalid", "denies", "permission"). The verdict check runs
// first precisely so none of them can reclassify a verdict.
func TestPreflightVerdictError_NotReclassifiedByStringHeuristics(t *testing.T) {
	err := newPreflightVerdictError(preflight.VerdictBlocked,
		"1 of 1 tools unavailable: tool_denied_by_config (invalid configuration, permission denied)")
	assert.Equal(t, ExitCodePreflightBlocked, classifyError(err))
}

// Transport and argument failures keep the general exit code 1 — they are not
// verdicts and a cron wrapper must be able to tell them apart.
func TestPreflightTransportErrorsUseGeneralExitCode(t *testing.T) {
	assert.Equal(t, ExitCodeGeneralError, classifyError(errors.New("mcpproxy daemon is not reachable. Start with: mcpproxy serve")))
}

// A non-verdict preflight failure must be exit 1 even when its message is full
// of the words the string heuristics branch on. Untyped, each of these lands in
// the 4/5 band, which a cron wrapper cannot tell apart from a real config or
// permission problem in mcpproxy itself (FR-009).
func TestPreflightGeneralError_NotReclassifiedByStringHeuristics(t *testing.T) {
	for _, message := range []string{
		"preflight failed: failed to load configuration",
		"preflight failed: invalid configuration in ~/.mcpproxy/mcp_config.json",
		"preflight failed: dial unix ~/.mcpproxy/mcpproxy.sock: permission denied",
		"preflight failed: operation not permitted",
	} {
		t.Run(message, func(t *testing.T) {
			untyped := errors.New(message)
			require.NotEqual(t, ExitCodeGeneralError, classifyError(untyped),
				"precondition: this message is exactly what the heuristics misfile")
			assert.Equal(t, ExitCodeGeneralError, classifyError(newPreflightGeneralError(untyped)))
		})
	}
}

// Wrapping must never swallow a verdict: the verdict IS the command's answer.
func TestPreflightGeneralError_PassesVerdictsThrough(t *testing.T) {
	verdict := newPreflightVerdictError(preflight.VerdictUnknownIDs, "1 of 1 tools unavailable: not_found")
	assert.Equal(t, verdict, newPreflightGeneralError(verdict))
	assert.Equal(t, ExitCodePreflightUnknownIDs, classifyError(newPreflightGeneralError(verdict)))
	assert.Nil(t, newPreflightGeneralError(nil))
}

// --- T018: exit-code precedence (worst class wins, 12 > 11 > 10) ------------

func TestPreflightExitVerdict_WorstClassWins(t *testing.T) {
	result := func(id, reason string) contracts.PreflightToolResult {
		if reason == "" {
			return contracts.PreflightToolResult{ID: id, Status: preflight.StatusReady}
		}
		retryable := preflight.Retryable(reason)
		return contracts.PreflightToolResult{
			ID: id, Status: preflight.StatusUnavailable, Reason: reason, Retryable: &retryable,
		}
	}

	tests := []struct {
		name     string
		tools    []contracts.PreflightToolResult
		wantExit int
	}{
		{
			name:     "all ready",
			tools:    []contracts.PreflightToolResult{result("a:1", ""), result("a:2", "")},
			wantExit: 0,
		},
		{
			name:     "retryable only",
			tools:    []contracts.PreflightToolResult{result("a:1", ""), result("a:2", preflight.ReasonServerInitializing)},
			wantExit: 10,
		},
		{
			name: "blocked beats retryable",
			tools: []contracts.PreflightToolResult{
				result("a:1", preflight.ReasonServerInitializing),
				result("a:2", preflight.ReasonToolChanged),
			},
			wantExit: 11,
		},
		{
			name: "unknown id beats blocked and retryable",
			tools: []contracts.PreflightToolResult{
				result("a:1", preflight.ReasonServerInitializing),
				result("a:2", preflight.ReasonToolChanged),
				result("a:3", preflight.ReasonNotFound),
			},
			wantExit: 12,
		},
		{
			name:     "server_not_configured is an unknown id",
			tools:    []contracts.PreflightToolResult{result("ghost:1", preflight.ReasonServerNotConfigured)},
			wantExit: 12,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &contracts.PreflightResponse{
				// The daemon's own verdict is deliberately understated here so
				// the local aggregation is what the assertion measures.
				Verdict: preflight.VerdictReady,
				Tools:   tc.tools,
			}
			verdict := preflightExitVerdict(resp)
			assert.Equal(t, tc.wantExit, preflight.ExitCode(verdict))
			assert.Equal(t, tc.wantExit, classifyErrorOrZero(newPreflightVerdictError(verdict, "")))
		})
	}
}

// The daemon's verdict still wins when it is the WORSE reading — the local
// recomputation may only escalate, never soften.
func TestPreflightExitVerdict_TakesTheWorseOfBothReadings(t *testing.T) {
	resp := &contracts.PreflightResponse{
		Verdict: preflight.VerdictBlocked,
		Tools:   []contracts.PreflightToolResult{{ID: "a:1", Status: preflight.StatusReady}},
	}
	assert.Equal(t, preflight.VerdictBlocked, preflightExitVerdict(resp))
}

func classifyErrorOrZero(err error) int {
	if err == nil {
		return 0
	}
	return classifyError(err)
}

// --- T018: request building -------------------------------------------------

func TestBuildPreflightRequest(t *testing.T) {
	t.Run("ids, pins, profile, filters and wait", func(t *testing.T) {
		req, err := buildPreflightRequest(
			[]string{"ctl:echo", "ctl:add"},
			[]string{"ctl:echo=sha256/v1:abcd"},
			"work",
			5*time.Second,
			contracts.PreflightPolicy{ReadOnlyOnly: true, ExcludeDestructive: true},
		)
		require.NoError(t, err)
		require.Len(t, req.Tools, 2)
		assert.Equal(t, "ctl:echo", req.Tools[0].ID)
		assert.Equal(t, "sha256/v1:abcd", req.Tools[0].PinHash)
		assert.Equal(t, "ctl:add", req.Tools[1].ID)
		assert.Empty(t, req.Tools[1].PinHash)
		assert.Equal(t, "work", req.Profile)
		assert.Equal(t, 5000, req.WaitMS)
		require.NotNil(t, req.Policy)
		assert.True(t, req.Policy.ReadOnlyOnly)
		assert.True(t, req.Policy.ExcludeDestructive)
		assert.False(t, req.Policy.ExcludeOpenWorld)
	})

	t.Run("no filters means no policy object", func(t *testing.T) {
		req, err := buildPreflightRequest([]string{"ctl:echo"}, nil, "", 0, contracts.PreflightPolicy{})
		require.NoError(t, err)
		assert.Nil(t, req.Policy)
		assert.Zero(t, req.WaitMS)
	})

	t.Run("pin for an id that was not requested is a usage error", func(t *testing.T) {
		_, err := buildPreflightRequest([]string{"ctl:echo"}, []string{"ctl:other=sha256/v1:abcd"}, "", 0, contracts.PreflightPolicy{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ctl:other")
	})

	t.Run("malformed pin", func(t *testing.T) {
		for _, pin := range []string{"ctl:echo", "=hash", "ctl:echo="} {
			_, err := buildPreflightRequest([]string{"ctl:echo"}, []string{pin}, "", 0, contracts.PreflightPolicy{})
			assert.Error(t, err, "pin %q must be rejected", pin)
		}
	})

	t.Run("conflicting pins for one id", func(t *testing.T) {
		_, err := buildPreflightRequest([]string{"ctl:echo"},
			[]string{"ctl:echo=sha256/v1:aa", "ctl:echo=sha256/v1:bb"}, "", 0, contracts.PreflightPolicy{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "conflicting")
	})

	// The wire field is milliseconds, so the range must be checked on the exact
	// duration: truncation would turn both of these into accepted values (0 and
	// 10000) that the daemon has no way to recognize as out of range.
	t.Run("wait range is validated before the millisecond truncation", func(t *testing.T) {
		_, err := buildPreflightRequest([]string{"ctl:echo"}, nil, "", -1*time.Nanosecond, contracts.PreflightPolicy{})
		require.Error(t, err, "-1ns must be rejected, not truncated to 0")
		assert.Contains(t, err.Error(), "negative")

		_, err = buildPreflightRequest([]string{"ctl:echo"}, nil, "", 10*time.Second+time.Nanosecond, contracts.PreflightPolicy{})
		require.Error(t, err, "10s+1ns must be rejected, not truncated to the in-range 10000ms")
		assert.Contains(t, err.Error(), "cap")

		req, err := buildPreflightRequest([]string{"ctl:echo"}, nil, "", 10*time.Second, contracts.PreflightPolicy{})
		require.NoError(t, err, "exactly the cap is allowed")
		assert.Equal(t, preflight.MaxWaitMS, req.WaitMS)
	})

	t.Run("a hash containing colons and slashes survives the split", func(t *testing.T) {
		req, err := buildPreflightRequest([]string{"ctl:echo"}, []string{"ctl:echo=sha256/v2:deadbeef"}, "", 0, contracts.PreflightPolicy{})
		require.NoError(t, err)
		assert.Equal(t, "sha256/v2:deadbeef", req.Tools[0].PinHash)
	})
}

// --- T018: output formats ---------------------------------------------------

func samplePreflightResponse() *contracts.PreflightResponse {
	retryable := true
	waited := 750
	return &contracts.PreflightResponse{
		Verdict:   preflight.VerdictDegradedRetryable,
		CheckedAt: time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC),
		WaitedMS:  &waited,
		Tools: []contracts.PreflightToolResult{
			{ID: "ctl:echo", Status: preflight.StatusReady},
			{
				ID:          "ctl:add",
				Status:      preflight.StatusUnavailable,
				Reason:      preflight.ReasonServerInitializing,
				Retryable:   &retryable,
				Detail:      "Server \"ctl\" is still starting up.",
				Remediation: preflight.DefaultRemediation(preflight.ReasonServerInitializing),
			},
		},
	}
}

func TestRenderPreflight_JSONUsesWireKeys(t *testing.T) {
	rendered, err := renderPreflight("json", samplePreflightResponse())
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(rendered), &decoded))
	assert.Equal(t, preflight.VerdictDegradedRetryable, decoded["verdict"])
	assert.Contains(t, decoded, "checked_at")
	assert.EqualValues(t, 750, decoded["waited_ms"])
	tools, ok := decoded["tools"].([]interface{})
	require.True(t, ok)
	require.Len(t, tools, 2)
	first := tools[0].(map[string]interface{})
	assert.Equal(t, "ctl:echo", first["id"])
	assert.NotContains(t, first, "reason", "a ready result carries no failure fields")
	second := tools[1].(map[string]interface{})
	assert.Equal(t, preflight.ReasonServerInitializing, second["reason"])
	assert.Equal(t, true, second["retryable"])
}

// YAML must use the SAME key names as JSON — a wrapper switching -o must not
// have to learn a second vocabulary.
func TestRenderPreflight_YAMLUsesTheSameKeysAsJSON(t *testing.T) {
	rendered, err := renderPreflight("yaml", samplePreflightResponse())
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, yaml.Unmarshal([]byte(rendered), &decoded))
	assert.Equal(t, preflight.VerdictDegradedRetryable, decoded["verdict"])
	assert.Contains(t, decoded, "checked_at")
	assert.Contains(t, decoded, "waited_ms")
	assert.NotContains(t, decoded, "waitedms", "yaml must honour the json tags, not the Go field names")
}

func TestRenderPreflight_TableCarriesVerdictAndPerToolReasons(t *testing.T) {
	rendered, err := renderPreflight("table", samplePreflightResponse())
	require.NoError(t, err)

	assert.Contains(t, rendered, "VERDICT: degraded_retryable (exit 10)")
	assert.Contains(t, rendered, "CHECKED: 2026-08-15T10:00:00Z")
	assert.Contains(t, rendered, "WAITED:  750ms")
	assert.Contains(t, rendered, "ctl:echo")
	assert.Contains(t, rendered, "ctl:add")
	assert.Contains(t, rendered, preflight.ReasonServerInitializing)
	assert.Contains(t, rendered, "ID")
	assert.Contains(t, rendered, "RETRYABLE")
}

// NewFormatter is case-insensitive, so the render branch has to be too:
// `-o JSON` selected the JSON formatter and then took the table path, emitting
// a "VERDICT:" header followed by JSON-encoded table rows.
func TestRenderPreflight_FormatIsCaseInsensitive(t *testing.T) {
	for _, format := range []string{"JSON", " Json ", "YAML", "TABLE"} {
		t.Run(format, func(t *testing.T) {
			rendered, err := renderPreflight(format, samplePreflightResponse())
			require.NoError(t, err)

			if strings.EqualFold(strings.TrimSpace(format), "table") {
				assert.Contains(t, rendered, "VERDICT: degraded_retryable (exit 10)")
				return
			}
			assert.NotContains(t, rendered, "VERDICT:",
				"a structured format must not be rendered through the table branch")

			var decoded map[string]interface{}
			require.NoError(t, yaml.Unmarshal([]byte(rendered), &decoded),
				"YAML parses JSON too, so one check covers both structured formats")
			assert.Equal(t, preflight.VerdictDegradedRetryable, decoded["verdict"])
		})
	}
}

func TestRenderPreflight_UnknownFormatIsAStructuredError(t *testing.T) {
	_, err := renderPreflight("xml", samplePreflightResponse())
	require.Error(t, err)
	var structured output.StructuredError
	require.True(t, errors.As(err, &structured))
	assert.Equal(t, output.ErrCodeInvalidOutputFormat, structured.Code)
}

// MCPPROXY_OUTPUT drives the format when no -o flag is given, per the repo's
// CLI output conventions (FR-009).
func TestPreflightOutputFormatHonoursEnvVar(t *testing.T) {
	t.Setenv("MCPPROXY_OUTPUT", "json")
	prevFormat, prevJSON := globalOutputFormat, globalJSONOutput
	t.Cleanup(func() { globalOutputFormat, globalJSONOutput = prevFormat, prevJSON })
	globalOutputFormat = ""
	globalJSONOutput = false

	format := ResolveOutputFormat()
	require.Equal(t, "json", format)

	rendered, err := renderPreflight(format, samplePreflightResponse())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(rendered), "{"))
}

func TestPreflightSummaryNamesTheFailingReasons(t *testing.T) {
	summary := preflightSummary(samplePreflightResponse())
	assert.Contains(t, summary, "1 of 2 tools unavailable")
	assert.Contains(t, summary, preflight.ReasonServerInitializing)

	ready := &contracts.PreflightResponse{
		Verdict: preflight.VerdictReady,
		Tools:   []contracts.PreflightToolResult{{ID: "ctl:echo", Status: preflight.StatusReady}},
	}
	assert.Empty(t, preflightSummary(ready))
}

// --- T018: command wiring & --help-json metadata ----------------------------

func TestToolsPreflightCommand_FlagsAndHelpJSON(t *testing.T) {
	cmd := newToolsPreflightCmd()

	require.NotNil(t, cmd.Args, "at least one tool id is required")
	assert.Error(t, cmd.Args(cmd, nil), "no ids must be rejected before any request is made")
	assert.NoError(t, cmd.Args(cmd, []string{"ctl:echo"}))

	// --help-json must reach the help hook even with no ids: it is the
	// discovery call an agent makes BEFORE it knows what to pass.
	withHelpJSON := newToolsPreflightCmd()
	withHelpJSON.Flags().Bool("help-json", false, "")
	require.NoError(t, withHelpJSON.Flags().Set("help-json", "true"))
	assert.NoError(t, withHelpJSON.Args(withHelpJSON, nil))

	info := output.ExtractHelpInfo(cmd)
	assert.Equal(t, "preflight", info.Name)
	assert.NotEmpty(t, info.Description)

	flagNames := make(map[string]string, len(info.Flags))
	for _, f := range info.Flags {
		flagNames[f.Name] = f.Type
	}
	for name, wantType := range map[string]string{
		"profile":             "string",
		"pin":                 "stringArray",
		"read-only-only":      "bool",
		"exclude-destructive": "bool",
		"exclude-open-world":  "bool",
		"wait":                "duration",
	} {
		gotType, ok := flagNames[name]
		assert.True(t, ok, "--%s must appear in --help-json metadata", name)
		assert.Equal(t, wantType, gotType, "--%s type", name)
	}

	// The exit-code contract is the command's whole point, so it must be
	// discoverable from the help text alone.
	for _, code := range []string{"10", "11", "12"} {
		assert.Contains(t, cmd.Long, code)
	}
}

func TestToolsPreflightCommand_IsRegisteredUnderTools(t *testing.T) {
	var found bool
	for _, sub := range GetToolsCommand().Commands() {
		if sub.Name() == "preflight" {
			found = true
		}
	}
	assert.True(t, found, "tools preflight must be registered on the tools command")
}
