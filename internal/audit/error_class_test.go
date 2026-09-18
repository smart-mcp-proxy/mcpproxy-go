// error_class_test.go — Phase D.2 / T105a (Spec 107 PR-D).
//
// Compile-red until T105: this file references audit.ErrorClassOf and
// audit.ErrorClass, which do not exist yet (internal/audit/error_class.go
// is implemented in T105). Until then `go test ./internal/audit` fails to
// build — that is the expected state for this task. No production code is
// added here.
//
// Per contracts/audit-line-events.md ("tool_call" `error_class` row) the
// bounded class set is: upstream_error | upstream_timeout |
// upstream_unavailable | validation | sanitisation | internal | cancelled —
// a closed enum, never message text. This table exercises ErrorClassOf
// against the typed errors that can actually reach a `tool_call` completion
// path today:
//
//   - context.Canceled / context.DeadlineExceeded (and %w-wrapped forms),
//     recognised via errors.Is so a deep wrap chain still classifies.
//   - *limiter.LimitError (internal/upstream/limiter/errors.go): the shed
//     identity returned to the completion path. Per research.md D6 a shed
//     normally surfaces as `tool_call` outcome:rejected, never outcome:error
//     — this table still pins ErrorClassOf's own verdict on the type
//     defensively, in case a caller ever classifies one as an error.
//   - *transport.HTTPError (internal/transport/http.go): classified by
//     status code — 503/502/504 style "server can't currently serve this"
//     codes are upstream_unavailable, everything else upstream_error.
//   - *transport.JSONRPCError: upstream_error (a well-formed upstream
//     response carrying a protocol-level failure).
//   - *jsonschema.ValidationError (santhosh-tekuri/jsonschema/v6, already a
//     module dependency, Spec 085 pre-dispatch arg validation): validation.
//   - audit.ErrSanitisationFailed: a sentinel this task expects T105 to
//     define alongside ErrorClassOf in error_class.go, for the case where
//     StripInternalArgs/canonicalisation-adjacent sanitisation itself fails
//     (distinct from the post-dispatch output_sanitisation *block* reason,
//     which never reaches ErrorClassOf because it is outcome:blocked, not
//     outcome:error).
//   - a plain, untyped error: internal (the fallback for anything the
//     table above does not recognise).
package audit_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/audit"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/transport"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
)

// mustValidationError compiles a minimal object schema requiring "x" and
// validates an empty instance against it, returning the resulting
// *jsonschema.ValidationError (santhosh-tekuri v6's Validate always returns
// that concrete type on failure).
func mustValidationError(t *testing.T) error {
	t.Helper()
	schemaJSON := `{"type":"object","required":["x"]}`
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemaJSON))
	if err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://error-class-test/schema", doc); err != nil {
		t.Fatalf("AddResource: %v", err)
	}
	sch, err := c.Compile("mem://error-class-test/schema")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	verr := sch.Validate(map[string]interface{}{})
	if verr == nil {
		t.Fatal("expected validation failure, got nil")
	}
	var ve *jsonschema.ValidationError
	if !errors.As(verr, &ve) {
		t.Fatalf("expected *jsonschema.ValidationError, got %T", verr)
	}
	return verr
}

func TestErrorClassOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want audit.ErrorClass
	}{
		{
			name: "context canceled direct",
			err:  context.Canceled,
			want: audit.ErrorClassCancelled,
		},
		{
			name: "context canceled wrapped",
			err:  fmt.Errorf("upstream call: %w", context.Canceled),
			want: audit.ErrorClassCancelled,
		},
		{
			name: "context deadline exceeded direct",
			err:  context.DeadlineExceeded,
			want: audit.ErrorClassUpstreamTimeout,
		},
		{
			name: "context deadline exceeded wrapped",
			err:  fmt.Errorf("dial tcp: %w", context.DeadlineExceeded),
			want: audit.ErrorClassUpstreamTimeout,
		},
		{
			name: "limiter server unavailable",
			err: &limiter.LimitError{
				Scope:  limiter.ScopeServer,
				Reason: limiter.ReasonServerUnavailable,
				Server: "github",
			},
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "limiter global queue full",
			err: &limiter.LimitError{
				Scope:  limiter.ScopeGlobal,
				Reason: limiter.ReasonQueueFull,
				Limit:  8,
			},
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "limiter queue timeout wrapped",
			err: fmt.Errorf("acquire: %w", &limiter.LimitError{
				Scope:  limiter.ScopeServer,
				Reason: limiter.ReasonQueueTimeout,
				Server: "slack",
			}),
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "http 503 service unavailable",
			err:  transport.NewHTTPError(503, "", "POST", "https://upstream.example/mcp", nil, nil),
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "http 502 bad gateway",
			err:  transport.NewHTTPError(502, "", "POST", "https://upstream.example/mcp", nil, nil),
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "http 504 gateway timeout",
			err:  transport.NewHTTPError(504, "", "POST", "https://upstream.example/mcp", nil, nil),
			want: audit.ErrorClassUpstreamUnavailable,
		},
		{
			name: "http 500 internal server error",
			err:  transport.NewHTTPError(500, "boom", "POST", "https://upstream.example/mcp", nil, nil),
			want: audit.ErrorClassUpstreamError,
		},
		{
			name: "jsonrpc protocol error",
			err:  &transport.JSONRPCError{Code: -32000, Message: "server error"},
			want: audit.ErrorClassUpstreamError,
		},
		{
			name: "jsonrpc error wrapping http",
			err: &transport.JSONRPCError{
				Code:      -32000,
				Message:   "server error",
				HTTPError: transport.NewHTTPError(500, "", "POST", "https://upstream.example/mcp", nil, nil),
			},
			want: audit.ErrorClassUpstreamError,
		},
		{
			name: "schema validation failure",
			err:  mustValidationError(t),
			want: audit.ErrorClassValidation,
		},
		{
			name: "sanitisation failure sentinel",
			err:  audit.ErrSanitisationFailed,
			want: audit.ErrorClassSanitisation,
		},
		{
			name: "sanitisation failure wrapped",
			err:  fmt.Errorf("mask args: %w", audit.ErrSanitisationFailed),
			want: audit.ErrorClassSanitisation,
		},
		{
			name: "unknown plain error",
			err:  errors.New("something went sideways"),
			want: audit.ErrorClassInternal,
		},
		{
			name: "nil error still classifies as internal",
			err:  nil,
			want: audit.ErrorClassInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := audit.ErrorClassOf(tt.err)
			if got != tt.want {
				t.Errorf("ErrorClassOf(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
