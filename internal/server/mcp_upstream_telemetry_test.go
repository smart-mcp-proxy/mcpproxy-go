package server

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
)

// TEL-03 / TEL-04: one success contract for activation and one attempt
// definition for upstream_tool_call_count_bucket, on every dispatch path
// (call_tool_* wrapper, direct routing, each code_execution JS sub-call).
// Every case is a full dispatch through the registered handler on a fresh
// install (fresh runtime, fresh lifetime activation store); the oracles are
// the persisted activation flag, the counter-registry bucket and the
// upstream's own invocation count.

type telPath string

const (
	telWrapper telPath = "wrapper"
	telDirect  telPath = "direct"
	telJS      telPath = "js"
)

type telUpstream string

const (
	upOK        telUpstream = "ok"
	upIsError   telUpstream = "iserror"
	upTransport telUpstream = "transport_error"
	upSecret    telUpstream = "secret_output"
)

func TestUpstreamTelemetry_ActivationAndAttemptsOnEveryDispatchPath(t *testing.T) {
	type tc struct {
		name         string
		up           telUpstream
		refused      bool // caller lacks the destructive tier for the target
		wantUpstream int64
		wantFlag     bool
		wantBucket   string
		blockOutput  bool
		skipPaths    map[telPath]bool
	}
	cases := []tc{
		{name: "success", up: upOK, wantUpstream: 1, wantFlag: true, wantBucket: "1-10"},
		{name: "upstream isError", up: upIsError, wantUpstream: 1, wantFlag: false, wantBucket: "1-10"},
		{name: "transport error", up: upTransport, wantUpstream: 1, wantFlag: false, wantBucket: "1-10"},
		{name: "policy refusal", up: upOK, refused: true, wantUpstream: 0, wantFlag: false, wantBucket: "0"},
		// Direct routing has no output-sanitisation seam of its own, so this
		// cell is wrapper + JS only.
		{name: "output policy rejection", up: upSecret, blockOutput: true, wantUpstream: 1, wantFlag: false, wantBucket: "1-10",
			skipPaths: map[telPath]bool{telDirect: true}},
	}
	for _, path := range []telPath{telWrapper, telDirect, telJS} {
		for _, c := range cases {
			if c.skipPaths[path] {
				continue
			}
			c := c
			path := path
			t.Run(fmt.Sprintf("%s/%s", path, c.name), func(t *testing.T) {
				pinTelemetryEnvEnabledForServer(t)
				f := newDirectRoutingFixture(t, directServerSpec{server: "fixture",
					tools: []toolSpec{readSpec("probe"), destructiveSpec("wipe")}})
				f.rt.SetTelemetry("v0.70.0", "personal")
				require.NotNil(t, f.rt.TelemetryService())
				require.False(t, loadActivationFromRuntime(t, f.rt).FirstRealToolCallEver, "fresh install")

				up := f.ups["fixture"]
				for _, name := range []string{"probe", "wipe"} {
					name := name
					up.mcpSrv.AddTool(mcp.Tool{Name: name, InputSchema: mcp.ToolInputSchema{Type: "object"}},
						func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
							up.record(req.Params.Name)
							switch c.up {
							case upIsError:
								return mcp.NewToolResultError("synthetic upstream rejection"), nil
							case upTransport:
								return nil, errors.New("synthetic transport failure")
							case upSecret:
								return mcp.NewToolResultText("leaked " + awsKeyFixture), nil
							}
							return mcp.NewToolResultText("ok"), nil
						})
				}
				if c.blockOutput {
					oc := config.DefaultOutputSanitisationConfig()
					oc.ResponseAction = "block"
					f.proxy.config.OutputSanitisation = oc
					f.proxy.sanitisationDetector = security.NewDetector(config.DefaultSensitiveDataDetectionConfig())
				}

				tool, variant := "probe", contracts.ToolVariantRead
				perms := []string{auth.PermRead, auth.PermWrite, auth.PermDestructive}
				if c.refused {
					tool, variant = "wipe", contracts.ToolVariantDestructive
					perms = []string{auth.PermRead}
				}
				ctx := agentCtx([]string{"fixture"}, perms, "")

				switch path {
				case telWrapper:
					req := mcp.CallToolRequest{}
					req.Params.Name = variant
					req.Params.Arguments = map[string]interface{}{"name": "fixture:" + tool, "args": map[string]interface{}{}}
					res, err := f.proxy.handleCallToolVariant(ctx, req, variant)
					require.NoError(t, err)
					require.NotNil(t, res)
				case telDirect:
					f.call(t, ctx, "fixture", tool)
				case telJS:
					call := runSandboxCallTool(t, f.proxy, ctx, "fixture", tool)
					require.Equal(t, !c.refused && (c.up == upOK || c.up == upIsError), call.OK, "script-visible outcome: %+v", call)
				}

				require.Equal(t, c.wantUpstream, up.count.Load(), "upstream invocations")
				require.Equal(t, c.wantFlag, loadActivationFromRuntime(t, f.rt).FirstRealToolCallEver, "first_real_tool_call_ever")
				require.Equal(t, c.wantBucket, f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket, "upstream attempt bucket")
			})
		}
	}
}

// Each JS sub-call is one attempt, counted separately from the code_execution
// invocation (a built-in call). The bucket edge makes the count exact: ten
// sub-calls stay in "1-10" (an extra count for the invocation, or any double
// count, would cross into "11-100"), and one more sub-call from a second
// invocation crosses it (a missed count would not).
func TestUpstreamTelemetry_JSSubCallsCountedSeparatelyFromInvocation(t *testing.T) {
	pinTelemetryEnvEnabledForServer(t)
	f := newDirectRoutingFixture(t, directServerSpec{server: "fixture", tools: []toolSpec{readSpec("probe")}})
	f.rt.SetTelemetry("v0.70.0", "personal")
	ctx := fullTierAgentOn("fixture")
	run := func(n int) {
		res, err := f.proxy.handleCodeExecution(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
			Name: "code_execution",
			Arguments: map[string]interface{}{
				"code":    fmt.Sprintf(`var n = 0; for (var i = 0; i < %d; i++) { if (call_tool("fixture", "probe", {}).ok) n++; } ({ n: n })`, n),
				"input":   map[string]interface{}{},
				"options": map[string]interface{}{"timeout_ms": 10000, "max_tool_calls": 0},
			},
		}})
		require.NoError(t, err)
		require.False(t, res.IsError, "%v", res.Content)
	}

	run(10)
	require.Equal(t, int64(10), f.ups["fixture"].count.Load())
	require.Equal(t, "1-10", f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket)
	require.True(t, loadActivationFromRuntime(t, f.rt).FirstRealToolCallEver)

	run(1)
	require.Equal(t, int64(11), f.ups["fixture"].count.Load())
	require.Equal(t, "11-100", f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket)
}

// A dispatch is one attempt on the wrapper and direct paths, not two. Ten
// calls stay in "1-10" (any double count crosses the edge); the eleventh
// crosses it (a missed count would not).
func TestUpstreamTelemetry_DispatchCountsExactlyOnce(t *testing.T) {
	for _, path := range []telPath{telWrapper, telDirect} {
		path := path
		t.Run(string(path), func(t *testing.T) {
			pinTelemetryEnvEnabledForServer(t)
			f := newDirectRoutingFixture(t, directServerSpec{server: "fixture", tools: []toolSpec{readSpec("probe")}})
			f.rt.SetTelemetry("v0.70.0", "personal")
			ctx := fullTierAgentOn("fixture")
			one := func() {
				if path == telDirect {
					f.call(t, ctx, "fixture", "probe")
					return
				}
				req := mcp.CallToolRequest{}
				req.Params.Name = contracts.ToolVariantRead
				req.Params.Arguments = map[string]interface{}{"name": "fixture:probe", "args": map[string]interface{}{}}
				_, err := f.proxy.handleCallToolVariant(ctx, req, contracts.ToolVariantRead)
				require.NoError(t, err)
			}
			for i := 0; i < 10; i++ {
				one()
			}
			require.Equal(t, int64(10), f.ups["fixture"].count.Load())
			require.Equal(t, "1-10", f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket)
			one()
			require.Equal(t, "11-100", f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket)
		})
	}
}
