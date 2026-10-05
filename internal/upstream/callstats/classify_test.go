package callstats

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/limiter"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/upstream/managed"
)

func TestClassify(t *testing.T) {
	okResult := &mcp.CallToolResult{}
	toolErr := &mcp.CallToolResult{IsError: true}

	tests := []struct {
		name    string
		result  *mcp.CallToolResult
		err     error
		counted bool
		failed  bool
		kind    Kind
	}{
		{"success", okResult, nil, true, false, KindNone},
		{"nil result nil error counts as success", nil, nil, true, false, KindNone},
		{"isError tool result is not a transport failure", toolErr, nil, false, false, KindNone},
		{"limiter rejection", nil, &limiter.LimitError{}, false, false, KindNone},
		{"wrapped limiter rejection", nil, fmt.Errorf("x: %w", &limiter.LimitError{}), false, false, KindNone},
		{"generation refusal", nil, managed.ErrConnectionGenerationChanged, false, false, KindNone},
		{"caller cancel", nil, context.Canceled, false, false, KindNone},
		{"wrapped caller cancel", nil, fmt.Errorf("call: %w", context.Canceled), false, false, KindNone},
		{"upstream error mentioning cancel, live caller ctx", nil, errors.New("backend request failed: context canceled"), true, true, KindOther},
		{"auth required", nil, errors.New("authorization required"), false, false, KindNone},
		{"unauthorized", nil, errors.New("HTTP 401 Unauthorized"), false, false, KindNone},
		{"invalid_token", nil, errors.New("invalid_token"), false, false, KindNone},
		{"connection refused", nil, errors.New("dial tcp: connection refused"), true, true, KindNetwork},
		{"transport closed", nil, errors.New("transport closed"), true, true, KindNetwork},
		{"deadline exceeded after dispatch", nil, context.DeadlineExceeded, true, true, KindTimeout},
		{"timeout text", nil, errors.New("request timeout"), true, true, KindTimeout},
		{"http 502", nil, errors.New("request failed with status 502 Bad Gateway"), true, true, KindHTTP},
		{"session terminated", nil, errors.New("session terminated"), true, true, KindSession},
		{"json-rpc error", nil, errors.New("jsonrpc error: -32603 internal error"), true, true, KindJSONRPC},
		{"untyped error", nil, errors.New("something odd"), true, true, KindOther},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counted, failed, kind := Classify(context.Background(), tt.result, tt.err)
			assert.Equal(t, tt.counted, counted, "counted")
			assert.Equal(t, tt.failed, failed, "failed")
			assert.Equal(t, tt.kind, kind, "kind")
		})
	}
}

// The text-only cancellation rule (sentinel stripped by the transport) applies
// only while the caller's own context is done.
func TestClassify_StrippedCancelNeedsDoneCallerContext(t *testing.T) {
	err := errors.New("request failed: context canceled")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	counted, _, _ := Classify(ctx, nil, err)
	assert.False(t, counted, "caller went away: not the upstream's fault")

	counted, failed, _ := Classify(context.Background(), nil, err)
	assert.True(t, counted && failed, "live caller ctx: an upstream error that mentions cancellation is a failure")

	counted, failed, _ = Classify(nil, nil, err) //nolint:staticcheck // nil ctx is documented as allowed
	assert.True(t, counted && failed)
}
