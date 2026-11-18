package server

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/auth"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/outputvalidation"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/runtime/stateview"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/security"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/telemetry"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
)

// Spec 126 FR-035 / FR-037 (T081): exactly one completion outcome per logical
// upstream call that dispatched at least one request, on every dispatch path
// (call_tool_* wrapper, direct routing, each code_execution sub-call). The
// oracle is the registry's raw outcome totals (the wire carries buckets).

type outcomeStarter struct {
	name string
	inc  int64
}

func (s outcomeStarter) IsStarter(name string, inc int64) bool {
	return name == s.name && inc == s.inc
}

type outcomeWant struct{ succeeded, unusable, nonStarter int64 }

func outcomeTotals(f *directRoutingFixture) outcomeWant {
	s, u, n := f.proxy.telemetryRegistry().UpstreamOutcomeTotals()
	return outcomeWant{s, u, n}
}

func TestUpstreamOutcome_OnePerLogicalCallOnEveryPath(t *testing.T) {
	type tc struct {
		name           string
		up             telUpstream
		refused        bool // caller lacks the destructive tier: refused before dispatch
		blockOutput    bool // output sanitisation in block mode withholds the result
		validateOutput bool // output-schema validation (strict) withholds the result
		starter        bool // a stub classifier marks the server as the starter
		want           outcomeWant
		wantScope      telemetry.SuccessScope
		onlyPaths      map[telPath]bool
	}
	cases := []tc{
		{name: "usable result", up: upOK, want: outcomeWant{1, 0, 1}, wantScope: telemetry.SuccessScopeNonStarter},
		{name: "upstream isError", up: upIsError, want: outcomeWant{0, 1, 0}},
		{name: "transport error", up: upTransport, want: outcomeWant{0, 1, 0}},
		{name: "refused before dispatch", up: upOK, refused: true, want: outcomeWant{0, 0, 0}},
		{name: "withheld by output sanitisation", up: upSecret, blockOutput: true, want: outcomeWant{0, 1, 0}},
		{name: "withheld by output validation", up: upOK, validateOutput: true, want: outcomeWant{0, 1, 0},
			onlyPaths: map[telPath]bool{telWrapper: true}},
		{name: "starter success", up: upOK, starter: true, want: outcomeWant{1, 0, 0}, wantScope: telemetry.SuccessScopeStarterOnly},
	}
	for _, path := range []telPath{telWrapper, telDirect, telJS} {
		for _, c := range cases {
			if c.onlyPaths != nil && !c.onlyPaths[path] {
				continue
			}
			c, path := c, path
			t.Run(fmt.Sprintf("%s/%s", path, c.name), func(t *testing.T) {
				pinTelemetryEnvEnabledForServer(t)
				f := newDirectRoutingFixture(t, directServerSpec{server: "fixture",
					tools: []toolSpec{readSpec("probe"), destructiveSpec("wipe")}})
				f.rt.SetTelemetry("v0.70.0", "personal")
				require.NotNil(t, f.rt.TelemetryService())

				up := f.ups["fixture"]
				for _, name := range []string{"probe", "wipe"} {
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
				if c.validateOutput {
					f.proxy.config.OutputValidation = &config.OutputValidationConfig{Mode: "strict", MissingStructuredContent: "block"}
					f.proxy.outputValidator = outputvalidation.New(
						f.proxy.config.OutputValidation.EffectiveMaxBytes(),
						f.proxy.config.OutputValidation.EffectiveMaxDepth(),
						zap.NewNop())
					// The tool declares an output schema; the upstream answers
					// prose only, which strict mode withholds.
					f.rt.Supervisor().StateView().UpdateServer("fixture", func(s *stateview.ServerStatus) {
						for i := range s.Tools {
							if s.Tools[i].Name == "probe" {
								s.Tools[i].OutputSchemaJSON = `{"type":"object","properties":{"url":{"type":"string"}}}`
							}
						}
					})
				}
				if c.starter {
					inc, ok := f.rt.ServerIncarnation("fixture")
					require.True(t, ok)
					f.rt.SetStarterClassifier(outcomeStarter{name: "fixture", inc: inc})
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
					runSandboxCallTool(t, f.proxy, ctx, "fixture", tool)
				}

				wantUpstream := int64(1)
				if c.refused {
					wantUpstream = 0
				}
				require.Equal(t, wantUpstream, up.count.Load(), "upstream invocations")
				require.Equal(t, c.want, outcomeTotals(f), "outcome totals {succeeded unusable non_starter}")
				scope, err := telemetry.LoadSuccessScope(f.rt.TelemetryService().ActivationDB())
				require.NoError(t, err)
				require.Equal(t, c.wantScope, scope, "success_scope_ever")
			})
		}
	}
}

// Outcomes count logical calls; dispatch counts requests. A session-recovery
// repeat hands the request to the transport twice: two dispatches, one
// outcome. A logical call with zero dispatches records none.
func TestUpstreamOutcome_DispatchCountVersusLogicalCall(t *testing.T) {
	pinTelemetryEnvEnabledForServer(t)
	f := newDirectRoutingFixture(t, directServerSpec{server: "fixture", tools: []toolSpec{readSpec("probe")}})
	f.rt.SetTelemetry("v0.70.0", "personal")

	// A session-recovery repeat: the recorder saw two hand-offs.
	ctx, _ := transport.WithCallRecorder(context.Background())
	transport.MarkDispatched(ctx)
	transport.MarkDispatched(ctx)
	f.proxy.noteUpstreamDispatched(ctx)
	o := f.proxy.beginUpstreamOutcome(ctx, "fixture", 1)
	o.markUsable(mcp.NewToolResultText("ok"), nil)
	o.record()
	o.record() // idempotent: a second record (e.g. an explicit call plus the defer) adds nothing
	require.Equal(t, outcomeWant{1, 0, 1}, outcomeTotals(f))
	require.Equal(t, "1-10", f.proxy.telemetryRegistry().Snapshot().UpstreamToolCallCountBucket)

	// Zero dispatches (refused by the managed client, shed, cancelled in the
	// admission queue): no outcome.
	none, _ := transport.WithCallRecorder(context.Background())
	z := f.proxy.beginUpstreamOutcome(none, "fixture", 1)
	z.record()
	require.Equal(t, outcomeWant{1, 0, 1}, outcomeTotals(f))

	// Not marked usable: unusable.
	again, _ := transport.WithCallRecorder(context.Background())
	transport.MarkDispatched(again)
	u := f.proxy.beginUpstreamOutcome(again, "fixture", 1)
	u.markUsable(mcp.NewToolResultError("upstream said no"), nil)
	u.record()
	require.Equal(t, outcomeWant{1, 1, 1}, outcomeTotals(f), "an isError result is not usable")
}

// Every exit after the dispatch is covered: in each of the three dispatch
// functions the outcome is begun and its record deferred immediately after
// noteUpstreamDispatched, before any statement that can return. A new early
// return added later therefore still records exactly one outcome.
func TestUpstreamOutcome_DeferredRightAfterDispatchOnEveryPath(t *testing.T) {
	// The call_tool_* wrapper (handleCallToolVariant), direct routing
	// (makeDirectModeHandler) and code_execution sub-calls
	// (upstreamToolCaller.callTool).
	cases := []struct{ file string }{{"mcp.go"}, {"mcp_routing.go"}, {"mcp_code_execution.go"}}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, c.file, nil, 0)
			require.NoError(t, err)
			var found bool
			ast.Inspect(file, func(n ast.Node) bool {
				var stmts []ast.Stmt
				switch b := n.(type) {
				case *ast.BlockStmt:
					stmts = b.List
				default:
					return true
				}
				for i, st := range stmts {
					if !isDirectCall(st, "noteUpstreamDispatched") {
						continue
					}
					found = true
					require.Greater(t, len(stmts), i+2, "%s: statements must follow noteUpstreamDispatched", c.file)
					assign, ok := stmts[i+1].(*ast.AssignStmt)
					require.True(t, ok && callsMethod(assign, "beginUpstreamOutcome"),
						"%s:%d: the statement right after noteUpstreamDispatched must begin the outcome", c.file, fset.Position(stmts[i+1].Pos()).Line)
					def, ok := stmts[i+2].(*ast.DeferStmt)
					require.True(t, ok && callsMethod(def, "record"),
						"%s:%d: the outcome's record must be deferred right after it begins", c.file, fset.Position(stmts[i+2].Pos()).Line)
				}
				return true
			})
			require.True(t, found, "%s: no noteUpstreamDispatched call found", c.file)
		})
	}
}

// isDirectCall reports whether st is itself the statement `x.name(...)` (not a
// statement that merely contains such a call somewhere inside it).
func isDirectCall(st ast.Stmt, name string) bool {
	es, ok := st.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := es.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

func callsMethod(n ast.Node, name string) bool {
	hit := false
	ast.Inspect(n, func(m ast.Node) bool {
		if call, ok := m.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
				hit = true
			}
		}
		return !hit
	})
	return hit
}
