package management

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/config"
	"github.com/smart-mcp-proxy/mcpproxy-go/internal/oauth"
)

// Issue #1148, round 8 finding 2. The doctor aggregator copies `health.detail`
// and `last_error` into UpstreamErrors.ErrorMessage — the same two strings
// oauth.RedactServerSecretFields scrubs with the name rule PLUS the
// value-shaped detector on the REST/SSE door. It ran the name rule alone.
func TestDoctor_UsesTheSharedFreeTextRuleForUpstreamErrors(t *testing.T) {
	const token = "ghp_1234567890abcdefghijABCDEFGHIJ123456"
	msg := `Post "https://h/mcp?opaque=` + token + `": no such host`

	logger := zaptest.NewLogger(t).Sugar()
	rt := newMockRuntime()
	rt.servers = []map[string]interface{}{
		{"name": "leaky", "last_error": msg},
	}

	svc := NewService(rt, &config.Config{}, "", &mockEventEmitter{}, nil, logger)
	diag, err := svc.Doctor(context.Background())
	require.NoError(t, err)
	require.Len(t, diag.UpstreamErrors, 1)

	assert.NotContains(t, diag.UpstreamErrors[0].ErrorMessage, token,
		"the doctor door published a credential its sibling REST door masks")
	assert.Equal(t, oauth.ScrubUpstreamText(msg), diag.UpstreamErrors[0].ErrorMessage,
		"UpstreamErrors.ErrorMessage must use the one shared free-text rule")
}
